package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/donrami/omp-sync/internal/sync"
	"github.com/spf13/cobra"
)

func newDiffCmd() *cobra.Command {
	var paths []string
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Show textual diffs between local and remote",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(cmd, paths)
		},
	}
	cmd.Flags().StringSliceVar(&paths, "path", nil, "restrict output to one or more relative paths (doublestar glob)")
	return cmd
}

type diffFileEntry struct {
	Path    string   `json:"path"`
	Lines   []string `json:"lines"`
	HasDiff bool     `json:"has_diff"`
}

type diffOutput struct {
	Backend       string           `json:"backend"`
	OmpDir        string           `json:"omp_dir"`
	RemoteID      string           `json:"remote_id"`
	FilesWithDiff int              `json:"files_with_diff"`
	Files         []diffFileEntry  `json:"files"`
	FilteredOut   []string         `json:"filtered_out,omitempty"`
}

func runDiff(cmd *cobra.Command, pathFilters []string) error {
	ctx, err := loadContext()
	if err != nil {
		return err
	}
	defer ctx.Audit.Close()

	b, err := buildBackend(ctx.Config)
	if err != nil {
		return err
	}

	remote, err := b.CurrentSnapshot(context.Background())
	if err != nil {
		if isBackendNoSnapshot(err) {
			return exitErr(ExitBackend, fmt.Errorf("no remote snapshot found on backend %q.", ctx.Config.Backend))
		}
		return exitErr(ExitBackend, fmt.Errorf("backend: %w", err))
	}

	tmpDir, err := os.MkdirTemp("", "omp-sync-diff-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	if err := b.DownloadSnapshot(context.Background(), remote, tmpDir); err != nil {
		return exitErr(ExitBackend, fmt.Errorf("download: %w", err))
	}

	snap, err := sync.Walk(ctx.Config.OmpDir, ctx.Config.EffectiveInclude(), ctx.Config.EffectiveExclude())
	if err != nil {
		return err
	}

	out := diffOutput{
		Backend:  ctx.Config.Backend,
		OmpDir:   ctx.Config.OmpDir,
		RemoteID: string(remote),
	}
	filteredOut := 0
	for _, f := range snap.Files {
		ok, err := pathMatchesAny(f.Path, pathFilters)
		if err != nil {
			return exitErr(ExitGeneric, fmt.Errorf("invalid --path glob: %w", err))
		}
		if !ok {
			continue
		}
		remotePath := tmpDir + "/files/" + f.Path
		localPath := ctx.Config.OmpDir + "/" + f.Path

		lr, err := os.ReadFile(localPath) //nolint:gosec // user-provided path
		if err != nil {
			out.Files = append(out.Files, diffFileEntry{
				Path:   f.Path,
				Lines:  []string{"local missing: " + err.Error()},
			})
			out.FilesWithDiff++
			continue
		}
		rr, err := os.ReadFile(remotePath) //nolint:gosec // user-provided path
		if err != nil {
			out.Files = append(out.Files, diffFileEntry{
				Path:   f.Path,
				Lines:  []string{"remote missing: " + err.Error()},
			})
			out.FilesWithDiff++
			continue
		}
		if string(lr) == string(rr) {
			continue
		}
		var bld strings.Builder
		writeSimpleDiff(&bld, string(rr), string(lr))
		out.Files = append(out.Files, diffFileEntry{
			Path:    f.Path,
			Lines:   strings.Split(bld.String(), "\n"),
			HasDiff: true,
		})
		out.FilesWithDiff++
	}
	_ = filteredOut // future: surface count of files outside the filter

	if globalFlags.JSON {
		return PrintJSON(cmd.OutOrStdout(), out)
	}

	if out.FilesWithDiff == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no differences")
		return nil
	}
	for _, f := range out.Files {
		if !f.HasDiff {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s: %s\n", f.Path, strings.Join(f.Lines, " | "))
			continue
		}
		fmt.Fprintf(cmd.OutOrStdout(), "diff --omp %s\n", f.Path)
		for _, ln := range f.Lines {
			fmt.Fprintln(cmd.OutOrStdout(), ln)
		}
	}
	return nil
}

// pathMatchesAny returns true when pathFilters is empty OR when any glob in
// pathFilters matches p (doublestar, path-aware).
func pathMatchesAny(p string, pathFilters []string) (bool, error) {
	if len(pathFilters) == 0 {
		return true, nil
	}
	for _, pat := range pathFilters {
		ok, err := doublestar.PathMatch(pat, p)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// writeSimpleDiff prints a minimal line-by-line diff to w.
func writeSimpleDiff(w io.Writer, before, after string) {
	bs := splitLines(before)
	as := splitLines(after)
	i, j := 0, 0
	for i < len(bs) || j < len(as) {
		switch {
		case i >= len(bs):
			fmt.Fprintf(w, "+ %s\n", as[j])
			j++
		case j >= len(as):
			fmt.Fprintf(w, "- %s\n", bs[i])
			i++
		case bs[i] == as[j]:
			fmt.Fprintf(w, "  %s\n", bs[i])
			i++
			j++
		default:
			fmt.Fprintf(w, "- %s\n", bs[i])
			fmt.Fprintf(w, "+ %s\n", as[j])
			i++
			j++
		}
	}
}

func splitLines(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
