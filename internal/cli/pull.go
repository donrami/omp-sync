package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/example/omp-sync/internal/audit"
	"github.com/example/omp-sync/internal/snapshot"
	"github.com/example/omp-sync/internal/sync"
	"github.com/example/omp-sync/internal/version"
	"github.com/spf13/cobra"
)

func newPullCmd() *cobra.Command {
	var (
		dryRun  bool
		yes     bool
		force   bool
		include []string
		exclude []string
	)
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Apply the remote snapshot to the local omp config",
		Long: "Download the current snapshot from the configured backend and apply " +
			"it to the local omp config. Refuses if local has un-pushed changes " +
			"(use --force to overwrite).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPull(cmd, dryRun, yes, force, include, exclude)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan but do not modify the local config")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite local unsynced changes")
	cmd.Flags().StringSliceVar(&include, "include", nil, "override include patterns (comma-separated)")
	cmd.Flags().StringSliceVar(&exclude, "exclude", nil, "override exclude patterns (comma-separated)")
	return cmd
}

type pullOutput struct {
	Operation        string `json:"operation"`
	SnapshotIDBefore string `json:"snapshot_id_before"`
	SnapshotIDAfter  string `json:"snapshot_id_after"`
	LocalRefused     bool   `json:"local_refused"`
	DurationMS       int64  `json:"duration_ms"`
}

// lastManifestPath is the per-backend record of the last applied manifest,
// stored under $XDG_STATE_HOME/omp-sync/last_manifest.json.
func lastManifestPath(stateDir string) string {
	return filepath.Join(stateDir, "last_manifest.json")
}

func runPull(cmd *cobra.Command, dryRun, yes, force bool, includeFlag, excludeFlag []string) error {
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

	remote, err := b.CurrentSnapshot(context.Background())
	if err != nil {
		if isBackendNoSnapshot(err) {
			return exitErr(ExitBackend, fmt.Errorf("no remote snapshot found on backend %q.", ctx.Config.Backend))
		}
		return exitErr(ExitBackend, fmt.Errorf("backend: %w", err))
	}

	prior := st.Get(ctx.Config.Backend)

	if !force && prior.LastSnapshotID != "" {
		diverged, err := localDivergeFromManifest(ctx.Config.OmpDir, lastManifestPath(filepath.Dir(ctx.StatePath)))
		if err == nil && diverged {
			return exitErr(ExitGeneric, fmt.Errorf(
				"local omp config has un-pushed changes. Use --force to overwrite, or `omp-sync push` first."))
		}
	}

	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(),
			"[dry-run] Would apply snapshot %s from backend %q (include=%v exclude=%v).\n",
			remote, ctx.Config.Backend, includeFlag, excludeFlag)
		return nil
	}

	if !yes {
		fmt.Fprintf(cmd.OutOrStdout(),
			"About to apply snapshot %s from backend %q to %s.\nContinue? [y/N] ",
			remote, ctx.Config.Backend, ctx.Config.OmpDir)
		var ans string
		if _, err := fmt.Scanln(&ans); err != nil || !isYes(ans) {
			return exitErr(ExitGeneric, fmt.Errorf("aborted"))
		}
	}

	tmpDir, err := os.MkdirTemp("", "omp-sync-pull-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	if err := b.DownloadSnapshot(context.Background(), remote, tmpDir); err != nil {
		return exitErr(ExitBackend, fmt.Errorf("download: %w", err))
	}

	_, _ = effectivePatterns(ctx.Config, includeFlag, excludeFlag)

	if err := applySnapshotToOmpDir(tmpDir, ctx.Config.OmpDir); err != nil {
		return exitErr(ExitBackend, fmt.Errorf("apply: %w", err))
	}

	if err := persistAppliedManifest(tmpDir, lastManifestPath(filepath.Dir(ctx.StatePath))); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not persist last manifest: %v\n", err)
	}

	st.Set(ctx.Config.Backend, sync.BackendState{
		LastSnapshotID: string(remote),
		LastSyncedAt:   time.Now().UTC(),
		LastOp:         "pull",
	})
	if err := st.Save(ctx.StatePath); err != nil {
		return err
	}

	dur := time.Since(ctx.StartTime).Milliseconds()
	_ = ctx.Audit.Append(audit.Entry{
		Op:               "pull",
		Backend:          ctx.Config.Backend,
		SnapshotIDBefore: prior.LastSnapshotID,
		SnapshotIDAfter:  string(remote),
		Exit:             0,
		DurationMS:       dur,
		OmpDir:           ctx.Config.OmpDir,
		ToolVersion:      version.Version,
	})

	out := pullOutput{
		Operation:        "pull",
		SnapshotIDBefore: prior.LastSnapshotID,
		SnapshotIDAfter:  string(remote),
		DurationMS:       dur,
	}
	if globalFlags.JSON {
		_ = PrintJSON(cmd.OutOrStdout(), out)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Pulled snapshot %s.\n", remote)
	}

	return nil
}

// localDivergeFromManifest returns true if any local file in the included set
// has a sha that differs from the corresponding entry in the last applied manifest.
func localDivergeFromManifest(ompDir, manifestPath string) (bool, error) {
	if _, err := os.Stat(manifestPath); err != nil {
		return false, err
	}
	prior, err := readAppliedManifest(manifestPath)
	if err != nil {
		return false, err
	}
	for path, priorHash := range prior {
		abs := filepath.Join(ompDir, filepath.FromSlash(path))
		data, err := os.ReadFile(abs) //nolint:gosec
		if err != nil {
			// Local file removed: do not treat as divergence.
			continue
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if got != priorHash {
			return true, nil
		}
	}
	return false, nil
}

// readAppliedManifest reads the persisted manifest snapshot into path→sha map.
func readAppliedManifest(manifestPath string) (map[string]string, error) {
	data, err := os.ReadFile(manifestPath) //nolint:gosec
	if err != nil {
		return nil, err
	}
	var m snapshot.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(m.Files))
	for _, f := range m.Files {
		out[f.Path] = f.SHA256
	}
	return out, nil
}

// persistAppliedManifest copies the freshly applied snapshot's manifest to a
// well-known location for later local-changes detection.
func persistAppliedManifest(snapshotDir, targetPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		return err
	}
	src, err := os.Open(filepath.Join(snapshotDir, snapshot.ManifestName)) //nolint:gosec
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}
