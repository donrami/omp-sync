package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/donrami/omp-sync/internal/sync"
	"github.com/spf13/cobra"
)

func newDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Show textual diffs between local and remote",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(cmd)
		},
	}
	return cmd
}

func runDiff(cmd *cobra.Command) error {
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

	// Walk the local tree.
	snap, err := sync.Walk(ctx.Config.OmpDir, ctx.Config.EffectiveInclude(), ctx.Config.EffectiveExclude())
	if err != nil {
		return err
	}

	for _, f := range snap.Files {
		remotePath := tmpDir + "/files/" + f.Path
		localPath := ctx.Config.OmpDir + "/" + f.Path

		lr, err := os.ReadFile(localPath) //nolint:gosec // controlled path
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "diff --omp %s\n", f.Path)
			fmt.Fprintf(cmd.ErrOrStderr(), "  local missing: %v\n", err)
			continue
		}
		rr, err := os.ReadFile(remotePath) //nolint:gosec // controlled path
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "diff --omp %s\n", f.Path)
			fmt.Fprintf(cmd.ErrOrStderr(), "  remote missing: %v\n", err)
			continue
		}
		if string(lr) == string(rr) {
			continue
		}
		fmt.Fprintf(cmd.OutOrStdout(), "diff --omp %s\n", f.Path)
		printSimpleDiff(cmd.OutOrStdout(), string(rr), string(lr))
	}
	return nil
}

// printSimpleDiff is a minimal line-by-line diff for files that differ.
// It does not aim for unified-diff perfection; it groups consecutive
// remote-only vs local-only lines with - / + prefixes.
func printSimpleDiff(w io.Writer, before, after string) {
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
