package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/donrami/omp-sync/internal/snapshot"
	"github.com/donrami/omp-sync/internal/sync"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show drift between local and remote",
		Long:  "Print which files differ between the local omp config and the configured backend's current snapshot.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd)
		},
	}
	return cmd
}

type statusOutput struct {
	Backend          string   `json:"backend"`
	OmpDir           string   `json:"omp_dir"`
	InSync           bool     `json:"in_sync"`
	LocalOnly        []string `json:"local_only"`
	RemoteOnly       []string `json:"remote_only"`
	LocalModified    []string `json:"local_modified"`
	RemoteModified   []string `json:"remote_modified"`
	Ignored          []string `json:"ignored"`
	LastSnapshotID   string   `json:"last_snapshot_id"`
	LastSyncedAt     string   `json:"last_synced_at"`
	RemoteSnapshotID string   `json:"remote_snapshot_id"`
}

func runStatus(cmd *cobra.Command) error {
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

	remote, remoteErr := b.CurrentSnapshot(context.Background())
	if remoteErr != nil && !isBackendNoSnapshot(remoteErr) {
		return exitErr(ExitBackend, fmt.Errorf("backend: %w", remoteErr))
	}

	prior := st.Get(ctx.Config.Backend)

	snap, err := sync.Walk(ctx.Config.OmpDir, ctx.Config.EffectiveInclude(), ctx.Config.EffectiveExclude())
	if err != nil {
		return exitErr(ExitGeneric, fmt.Errorf("walk: %w", err))
	}

	var remoteFiles map[string]snapshot.FileEntry
	if remoteErr == nil {
		tmpDir, err := os.MkdirTemp("", "omp-sync-status-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)
		if err := b.DownloadSnapshot(context.Background(), remote, tmpDir); err != nil {
			return exitErr(ExitBackend, fmt.Errorf("download: %w", err))
		}
		remoteFiles, err = readManifestIndex(tmpDir)
		if err != nil {
			return err
		}
	}

	out := statusOutput{
		Backend:          ctx.Config.Backend,
		OmpDir:           ctx.Config.OmpDir,
		Ignored:          snap.Ignored,
		LastSnapshotID:   prior.LastSnapshotID,
		LastSyncedAt:     prior.LastSyncedAt.Format(time.RFC3339),
		RemoteSnapshotID: string(remote),
		InSync:           true,
	}

	localByPath := map[string]snapshot.FileEntry{}
	for _, f := range snap.Files {
		localByPath[f.Path] = f
	}

	for p, lf := range localByPath {
		rf, ok := remoteFiles[p]
		if !ok {
			out.LocalOnly = append(out.LocalOnly, p)
			out.InSync = false
			continue
		}
		if rf.SHA256 != lf.SHA256 {
			out.LocalModified = append(out.LocalModified, p)
			out.InSync = false
		}
	}
	for p := range remoteFiles {
		if _, ok := localByPath[p]; !ok {
			out.RemoteOnly = append(out.RemoteOnly, p)
			out.InSync = false
		}
	}

	sort.Strings(out.LocalOnly)
	sort.Strings(out.RemoteOnly)
	sort.Strings(out.LocalModified)
	sort.Strings(out.RemoteModified)

	if globalFlags.JSON {
		return PrintJSON(cmd.OutOrStdout(), out)
	}
	if out.InSync {
		fmt.Fprintln(cmd.OutOrStdout(), "In sync.")
		return nil
	}
	if len(out.LocalOnly) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Local only (%d):\n", len(out.LocalOnly))
		for _, p := range out.LocalOnly {
			fmt.Fprintf(cmd.OutOrStdout(), "  + %s\n", p)
		}
	}
	if len(out.RemoteOnly) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Remote only (%d):\n", len(out.RemoteOnly))
		for _, p := range out.RemoteOnly {
			fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", p)
		}
	}
	if len(out.LocalModified) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Local modified (%d):\n", len(out.LocalModified))
		for _, p := range out.LocalModified {
			fmt.Fprintf(cmd.OutOrStdout(), "  * %s\n", p)
		}
	}
	if len(out.Ignored) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Ignored (%d):\n", len(out.Ignored))
		for _, p := range out.Ignored {
			fmt.Fprintf(cmd.OutOrStdout(), "  . %s\n", p)
		}
	}
	return nil
}

func readManifestIndex(dir string) (map[string]snapshot.FileEntry, error) {
	manifestPath := dir + string(os.PathSeparator) + "manifest.json"
	data, err := os.ReadFile(manifestPath) //nolint:gosec // controlled path
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m snapshot.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	out := make(map[string]snapshot.FileEntry, len(m.Files))
	for _, f := range m.Files {
		out[f.Path] = f
	}
	return out, nil
}
