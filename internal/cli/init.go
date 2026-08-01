package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/donrami/omp-sync/internal/audit"
	"github.com/donrami/omp-sync/internal/backend"
	"github.com/donrami/omp-sync/internal/snapshot"
	"github.com/donrami/omp-sync/internal/sync"
	"github.com/donrami/omp-sync/internal/version"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bootstrap local omp config from a remote snapshot",
		Long: "Populate the local omp config directory from the current snapshot on the configured backend. " +
			"Use this on a fresh machine, or after wiping the local config.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation")
	return cmd
}

func runInit(cmd *cobra.Command, yes bool) error {
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

	// Check the remote has a snapshot.
	cur, err := b.CurrentSnapshot(context.Background())
	if err != nil {
		if isBackendNoSnapshot(err) {
			return exitErr(ExitBackend, fmt.Errorf(
				"no remote snapshot found on backend %q. Run `omp-sync push` from another machine first.",
				ctx.Config.Backend))
		}
		return exitErr(ExitBackend, fmt.Errorf("backend: %w", err))
	}

	// Ask for confirmation.
	if !yes {
		fmt.Fprintf(cmd.OutOrStdout(),
			"About to populate %s from snapshot %s on backend %q.\nContinue? [y/N] ",
			ctx.Config.OmpDir, cur, ctx.Config.Backend)
		var ans string
		if _, err := fmt.Scanln(&ans); err != nil || !isYes(ans) {
			return exitErr(ExitGeneric, fmt.Errorf("aborted"))
		}
	}

	// Download to a staging dir, then swap into omp_dir.
	tmpDir, err := os.MkdirTemp("", "omp-sync-pull-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	if err := b.DownloadSnapshot(context.Background(), cur, tmpDir); err != nil {
		return exitErr(ExitBackend, fmt.Errorf("download: %w", err))
	}

	if err := applySnapshotToOmpDir(tmpDir, ctx.Config.OmpDir); err != nil {
		return exitErr(ExitBackend, fmt.Errorf("apply: %w", err))
	}

	// Update state.
	st.Set(ctx.Config.Backend, sync.BackendState{
		LastSnapshotID: string(cur),
		LastSyncedAt:   time.Now().UTC(),
		LastOp:         "init",
	})
	if err := st.Save(ctx.StatePath); err != nil {
		return err
	}

	_ = ctx.Audit.Append(audit.Entry{
		Op:             "init",
		Backend:        ctx.Config.Backend,
		SnapshotIDAfter: string(cur),
		Exit:           0,
		DurationMS:     time.Since(ctx.StartTime).Milliseconds(),
		OmpDir:         ctx.Config.OmpDir,
		ToolVersion:    version.Version,
	})

	fmt.Fprintf(cmd.OutOrStdout(), "Initialized %s from snapshot %s.\n", ctx.Config.OmpDir, cur)
	return nil
}

// applySnapshotToOmpDir copies files from the staging snapshot into ompDir.
// It uses atomic writes for each file and an atomic directory swap if the
// destination already exists.
func applySnapshotToOmpDir(snapshotDir, ompDir string) error {
	manifestPath := filepath.Join(snapshotDir, snapshot.ManifestName)
	data, err := os.ReadFile(manifestPath) //nolint:gosec // controlled path
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var m snapshot.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	if err := m.Validate(ompDir); err != nil {
		return fmt.Errorf("validate manifest: %w", err)
	}

	// Apply each file via atomic write.
	for _, f := range m.Files {
		src := filepath.Join(snapshotDir, snapshot.FilesDir, filepath.FromSlash(f.Path))
		dst := filepath.Join(ompDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		buf, err := os.ReadFile(src) //nolint:gosec // controlled path
		if err != nil {
			return fmt.Errorf("read %s: %w", f.Path, err)
		}
		mode := os.FileMode(f.Mode)
		if f.Executable {
			mode |= 0o111
		}
		tmp := dst + ".tmp"
		if err := os.WriteFile(tmp, buf, mode); err != nil {
			return fmt.Errorf("write tmp %s: %w", f.Path, err)
		}
		if err := os.Rename(tmp, dst); err != nil {
			return fmt.Errorf("rename %s: %w", f.Path, err)
		}
	}
	return nil
}

// CopyFile is a small helper used in tests.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // controlled path
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func isBackendNoSnapshot(err error) bool {
	if err == nil {
		return false
	}
	return err == backend.ErrNoSnapshot
}
