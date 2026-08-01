// Package tui implements the optional interactive UI for omp-sync.
//
// The TUI is sugar on top of the CLI. Every action the user takes must be
// equivalent to invoking the corresponding CLI subcommand — there is no
// TUI-only behavior. This guarantees that scripts and humans alike can rely
// on the same semantics.
//
// TUI actions (push, pull) are implemented by re-executing the same binary
// with the appropriate subcommand and the user's --config path. The result
// (stdout/stderr/exit code) is captured and surfaced in the model.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/donrami/omp-sync/internal/backend"
	tea "charm.land/bubbletea/v2"
)

// Mode reflects what the user is looking at.
type Mode int

const (
	ModeList Mode = iota
	ModeDiff
	ModeConfirm
)

// Model is the bubbletea state machine.
type Model struct {
	mode        Mode
	snapshots   []backend.SnapshotInfo
	cursor      int
	width       int
	height      int
	backend     backend.Backend
	backendName string
	quitting    bool
	err         error
	confirm     confirmAction
	pending     *exec.Cmd

	// ConfigPath is the resolved --config flag the binary was launched with.
	// When set, TUI-triggered actions re-exec the binary with this path so
	// the same config is used.
	ConfigPath string

	// ExecPath is the path to the running binary. Captured at New() time.
	ExecPath string

	// LastAction holds the most recent TUI-triggered action's result.
	LastAction ActionResult
}

// ActionResult captures the outcome of a TUI-triggered CLI invocation.
type ActionResult struct {
	Op        string
	Output    string
	ErrOutput string
	ExitCode  int
	Started   time.Time
	Duration  time.Duration
}

// confirmAction identifies which action the user is confirming.
type confirmAction int

const (
	confirmNone confirmAction = iota
	confirmPush
	confirmPull
)

// New constructs a Model with the given backend and snapshot list.
func New(name string, b backend.Backend, snaps []backend.SnapshotInfo) *Model {
	execPath, _ := os.Executable()
	return &Model{
		mode:        ModeList,
		snapshots:   snaps,
		backend:     b,
		backendName: name,
		ExecPath:    execPath,
	}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case actionResultMsg:
		m.LastAction = msg.result
		m.err = nil
		return m, nil
	case actionErrorMsg:
		m.LastAction = msg.result
		m.err = fmt.Errorf("%s failed (exit %d): %s",
			msg.result.Op, msg.result.ExitCode, msg.result.ErrOutput)
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// While a command is running, only 'q' and Ctrl+C are accepted.
	if m.pending != nil {
		switch msg.Code {
		case tea.KeyEsc, 'q':
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}

	// 'q'/Esc at any time quit. While in a confirmation prompt, 'n' cancels.
	switch msg.Code {
	case tea.KeyEsc, 'q':
		m.quitting = true
		return m, tea.Quit
	case tea.KeyDown, 'j':
		if m.mode == ModeList && m.cursor < len(m.snapshots)-1 {
			m.cursor++
		}
	case tea.KeyUp, 'k':
		if m.mode == ModeList && m.cursor > 0 {
			m.cursor--
		}
	case 'p':
		if m.mode == ModeList {
			m.confirm = confirmPush
			m.mode = ModeConfirm
		}
	case 'l':
		if m.mode == ModeList {
			m.confirm = confirmPull
			m.mode = ModeConfirm
		}
	case 'y':
		if m.mode == ModeConfirm {
			cmd := m.runConfirmed()
			m.mode = ModeList
			return m, cmd
		}
	case 'n':
		if m.mode == ModeConfirm {
			m.mode = ModeList
			m.confirm = confirmNone
		}
	}
	return m, nil
}

// actionResultMsg / actionErrorMsg are bubbletea message types emitted by
// runConfirmed via tea.ExecProcess.
type actionResultMsg struct{ result ActionResult }
type actionErrorMsg struct{ result ActionResult }

// runConfirmed invokes the equivalent CLI command via os/exec.
func (m *Model) runConfirmed() tea.Cmd {
	if m.ExecPath == "" || m.ConfigPath == "" {
		m.err = fmt.Errorf("TUI missing exec/config path; cannot run action")
		return tea.Quit
	}
	var op string
	var args []string
	switch m.confirm {
	case confirmPush:
		op = "push"
		args = []string{"push", "--yes"}
	case confirmPull:
		op = "pull"
		args = []string{"pull", "--yes"}
	default:
		m.err = fmt.Errorf("no action selected")
		return tea.Quit
	}
	args = append(args, "--config", m.ConfigPath)
	started := time.Now()
	cmd := exec.Command(m.ExecPath, args...)
	m.pending = cmd

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		result := ActionResult{
			Op:       op,
			Started:  started,
			Duration: time.Since(started),
		}
		if out, eErr := cmd.Output(); eErr == nil {
			result.Output = string(out)
		}
		if ee, ok := err.(*exec.ExitError); ok {
			result.ErrOutput = string(ee.Stderr)
			result.ExitCode = ee.ExitCode()
			return actionErrorMsg{result: result}
		}
		if err != nil {
			result.ErrOutput = err.Error()
			result.ExitCode = -1
			return actionErrorMsg{result: result}
		}
		result.ExitCode = 0
		return actionResultMsg{result: result}
	})
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	if m.err != nil {
		v := tea.NewView(m.err.Error())
		v.AltScreen = true
		return v
	}
	var v tea.View
	switch {
	case m.mode == ModeConfirm:
		v = tea.NewView(renderConfirm(m, actionLabel(m.confirm)))
		v.AltScreen = true
	case m.LastAction.Op != "":
		v = tea.NewView(renderActionResult(m))
		v.AltScreen = true
	case m.quitting:
		v = tea.NewView("")
	default:
		v = tea.NewView(renderList(m))
		v.AltScreen = true
	}
	return v
}

func actionLabel(a confirmAction) string {
	switch a {
	case confirmPush:
		return "Push local snapshot to backend?"
	case confirmPull:
		return "Pull remote snapshot to local?"
	default:
		return "Confirm?"
	}
}

// Err reports any error encountered during the run.
func (m *Model) Err() error { return m.err }

// Compile-time interface check.
var (
	_ tea.Model = (*Model)(nil)
	_          = context.Background
)
