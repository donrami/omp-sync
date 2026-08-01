package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/example/omp-sync/internal/audit"
	"github.com/example/omp-sync/internal/backend"
	"github.com/example/omp-sync/internal/snapshot"
	"github.com/example/omp-sync/internal/sync"
	"github.com/example/omp-sync/internal/version"
	"github.com/spf13/cobra"
)

func newPushCmd() *cobra.Command {
	var (
		dryRun  bool
		yes     bool
		message string
		include []string
		exclude []string
	)
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Publish local omp config to the backend",
		Long: "Walk the local omp config, build a snapshot, and atomically " +
			"publish it to the configured backend. Refuses if the remote has " +
			"changed since the last sync (FR-009).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPush(cmd, dryRun, yes, message, include, exclude)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan but do not modify the remote")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation")
	cmd.Flags().StringVar(&message, "message", "", "snapshot note (informational; not all backends persist)")
	cmd.Flags().StringSliceVar(&include, "include", nil, "override include patterns (comma-separated)")
	cmd.Flags().StringSliceVar(&exclude, "exclude", nil, "override exclude patterns (comma-separated)")
	return cmd
}

type pushOutput struct {
	Operation         string   `json:"operation"`
	SnapshotIDBefore  string   `json:"snapshot_id_before"`
	SnapshotIDAfter   string   `json:"snapshot_id_after"`
	FilesAdded        []string `json:"files_added"`
	FilesModified     []string `json:"files_modified"`
	FilesDeleted      []string `json:"files_deleted"`
	FilesUnchanged    int      `json:"files_unchanged"`
	FilesIgnored      []string `json:"files_ignored"`
	DurationMS        int64    `json:"duration_ms"`
}

func runPush(cmd *cobra.Command, dryRun, yes bool, message string, includeFlag, excludeFlag []string) error {
	ctx, err := loadContext()
	if err != nil {
		return err
	}
	defer ctx.Audit.Close()

	b, err := buildBackend(ctx.Config)
	if err != nil {
		return err
	}

	st, err := sync.LoadState(ctx.StatePath)
	if err != nil {
		return err
	}

	maybeWarnOmpRunning(cmd.ErrOrStderr())

	// Effective include/exclude = flags override config.
	include, exclude := effectivePatterns(ctx.Config, includeFlag, excludeFlag)

	snap, err := sync.Walk(ctx.Config.OmpDir, include, exclude)
	if err != nil {
		return exitErr(ExitGeneric, fmt.Errorf("walk: %w", err))
	}

	// Compare with prior remote manifest to compute added/modified/deleted counts.
	prior := st.Get(ctx.Config.Backend)
	expectedPrevious := backend.SnapshotID(prior.LastSnapshotID)

	counts := struct {
		Added, Modified, Deleted, Unchanged int
		AddedPaths, ModifiedPaths, DeletedPaths []string
		Ignored []string
	}{Ignored: append([]string(nil), snap.Ignored...)}

	if prior.LastSnapshotID != "" {
		priorFiles, perr := fetchPriorManifest(b, prior.LastSnapshotID)
		if perr == nil {
			diffCounts(&counts, priorFiles, snap.Files)
		}
	} else {
		// No prior snapshot: every included file is "added".
		for _, f := range snap.Files {
			counts.AddedPaths = append(counts.AddedPaths, f.Path)
		}
		counts.Added = len(snap.Files)
	}

	if dryRun {
		out := pushOutput{
			Operation:        "push (dry-run)",
			SnapshotIDBefore: string(expectedPrevious),
			FilesAdded:       counts.AddedPaths,
			FilesModified:    counts.ModifiedPaths,
			FilesDeleted:     counts.DeletedPaths,
			FilesIgnored:     counts.Ignored,
		}
		if globalFlags.JSON {
			return PrintJSON(cmd.OutOrStdout(), out)
		}
		fmt.Fprintf(cmd.OutOrStdout(),
			"[dry-run] Would push %d files (%d added, %d modified, %d deleted); %d ignored.\n",
			counts.Added+counts.Modified+counts.Deleted, counts.Added, counts.Modified, counts.Deleted, len(counts.Ignored))
		return nil
	}

	if !yes {
		fmt.Fprintf(cmd.OutOrStdout(),
			"About to push %d files to backend %q.\nContinue? [y/N] ",
			len(snap.Files), ctx.Config.Backend)
		var ans string
		if _, err := fmt.Scanln(&ans); err != nil || !isYes(ans) {
			return exitErr(ExitGeneric, fmt.Errorf("aborted"))
		}
	}

	stagingDir, err := os.MkdirTemp("", "omp-sync-push-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagingDir)

	if _, err := sync.ComputeSnapshotDir(snap, stagingDir, version.Version); err != nil {
		return exitErr(ExitGeneric, fmt.Errorf("stage: %w", err))
	}

	if err := b.Verify(context.Background()); err != nil {
		return exitErr(ExitBackend, fmt.Errorf("verify: %w", err))
	}

	id, err := b.UploadSnapshot(context.Background(), stagingDir, expectedPrevious)
	if err != nil {
		if isBackendConflict(err) {
			return exitErr(ExitBackend, fmt.Errorf(
				"remote has changed since you last synced. Run `omp-sync pull` first."))
		}
		return exitErr(ExitBackend, fmt.Errorf("upload: %w", err))
	}

	st.Set(ctx.Config.Backend, sync.BackendState{
		LastSnapshotID: string(id),
		LastSyncedAt:   time.Now().UTC(),
		LastOp:         "push",
	})
	if err := st.Save(ctx.StatePath); err != nil {
		return err
	}

	dur := time.Since(ctx.StartTime).Milliseconds()
	_ = ctx.Audit.Append(audit.Entry{
		Op:               "push",
		Backend:          ctx.Config.Backend,
		SnapshotIDBefore: string(expectedPrevious),
		SnapshotIDAfter:  string(id),
		FilesAdded:       counts.Added,
		FilesModified:    counts.Modified,
		FilesDeleted:     counts.Deleted,
		FilesIgnored:     len(counts.Ignored),
		Exit:             0,
		DurationMS:       dur,
		OmpDir:           ctx.Config.OmpDir,
		ToolVersion:      version.Version,
	})

	out := pushOutput{
		Operation:        "push",
		SnapshotIDBefore: string(expectedPrevious),
		SnapshotIDAfter:  string(id),
		FilesAdded:       counts.AddedPaths,
		FilesModified:    counts.ModifiedPaths,
		FilesDeleted:     counts.DeletedPaths,
		FilesIgnored:     counts.Ignored,
		DurationMS:       dur,
	}

	if globalFlags.JSON {
		_ = PrintJSON(cmd.OutOrStdout(), out)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Pushed %d files. New snapshot: %s\n", len(snap.Files), id)
	}

	_ = message
	return nil
}

// effectivePatterns merges config patterns with CLI overrides (CLI wins).
func effectivePatterns(cfg interface {
	EffectiveInclude() []string
	EffectiveExclude() []string
}, includeFlag, excludeFlag []string) ([]string, []string) {
	inc := cfg.EffectiveInclude()
	exc := cfg.EffectiveExclude()
	if len(includeFlag) > 0 {
		inc = includeFlag
	}
	if len(excludeFlag) > 0 {
		exc = excludeFlag
	}
	return inc, exc
}

// fetchPriorManifest downloads the previous snapshot and returns its file index.
func fetchPriorManifest(b backend.Backend, id string) (map[string]snapshot.FileEntry, error) {
	dir, err := os.MkdirTemp("", "omp-sync-prior-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := b.DownloadSnapshot(context.Background(), backend.SnapshotID(id), dir); err != nil {
		return nil, err
	}
	return readManifestIndex(dir)
}

// diffCounts classifies each local file against the prior manifest.
func diffCounts(out *struct {
	Added, Modified, Deleted, Unchanged int
	AddedPaths, ModifiedPaths, DeletedPaths []string
	Ignored []string
}, prior map[string]snapshot.FileEntry, localFiles []snapshot.FileEntry) {
	for _, lf := range localFiles {
		pf, ok := prior[lf.Path]
		if !ok {
			out.Added++
			out.AddedPaths = append(out.AddedPaths, lf.Path)
			continue
		}
		if pf.SHA256 != lf.SHA256 {
			out.Modified++
			out.ModifiedPaths = append(out.ModifiedPaths, lf.Path)
			continue
		}
		out.Unchanged++
	}
	for p := range prior {
		found := false
		for _, lf := range localFiles {
			if lf.Path == p {
				found = true
				break
			}
		}
		if !found {
			out.Deleted++
			out.DeletedPaths = append(out.DeletedPaths, p)
		}
	}
}

func isBackendConflict(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, backend.ErrConflict) {
		return true
	}
	return errStrContains(err, "remote has changed")
}

func errStrContains(err error, sub string) bool {
	for err != nil {
		if contains(err.Error(), sub) {
			return true
		}
		type u interface{ Unwrap() error }
		next, ok := err.(u)
		if !ok {
			return false
		}
		err = next.Unwrap()
	}
	return false
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
