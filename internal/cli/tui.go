package cli

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/example/omp-sync/internal/backend"
	"github.com/example/omp-sync/internal/tui"
	"github.com/spf13/cobra"
)

func newTUICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Launch the interactive TUI",
		Long:  "Open a terminal UI for browsing snapshots and diffs. The CLI is the source of truth; the TUI is optional.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(cmd)
		},
	}
	return cmd
}

// runTUI builds and runs the bubbletea-based interactive UI.
func runTUI(cmd *cobra.Command) error {
	ctx, err := loadContext()
	if err != nil {
		return err
	}
	defer ctx.Audit.Close()

	b, err := buildBackend(ctx.Config)
	if err != nil {
		return err
	}

	// Fetch the snapshot list (best-effort; empty list is fine).
	snaps, err := b.ListSnapshots(cmd.Context(), 0)
	if err != nil {
		// Non-fatal: the TUI renders an empty list and the user can still trigger push.
		snaps = nil
	}

	model := tui.New(ctx.Config.Backend, b, snaps)
	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "tui:", err)
		return err
	}
	if model.Err() != nil {
		return model.Err()
	}

	// Compile-time assertion: ensure the backend interface is satisfied.
	var _ backend.Backend = b
	return nil
}
