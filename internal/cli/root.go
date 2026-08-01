// Package cli implements the omp-sync command-line interface.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/example/omp-sync/internal/audit"
	"github.com/example/omp-sync/internal/backend"
	"github.com/example/omp-sync/internal/config"
	"github.com/example/omp-sync/internal/omp"
	"github.com/example/omp-sync/internal/version"
	"github.com/example/omp-sync/internal/sync"
	"github.com/spf13/cobra"
)

// Exit codes (per contracts/cli.md).
const (
	ExitOK       = 0
	ExitGeneric  = 1
	ExitBackend  = 2
	ExitInternal = 3
	ExitUsage    = 64
)

// PersistentState holds state that persists across a single invocation.
type PersistentState struct {
	Config    *config.Config
	StatePath string
	Audit     *audit.Logger
	Backends  *backend.Registry
	StartTime time.Time
}

// GlobalFlags holds values from the root command's persistent flags.
type GlobalFlags struct {
	ConfigPath string
	NoColor    bool
	Quiet      bool
	JSON       bool
	Verbose    bool
}

var globalFlags GlobalFlags

// NewRootCmd constructs the root command.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "omp-sync",
		Short:         "Synchronize your omp config across devices",
		Long:          "omp-sync centralizes your omp configuration so it can be shared across machines via WebDAV, GitHub, or a local folder.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Version,
	}

	pflags := root.PersistentFlags()
	pflags.StringVar(&globalFlags.ConfigPath, "config", config.DefaultPath(), "path to the config file")
	pflags.BoolVar(&globalFlags.NoColor, "no-color", false, "disable ANSI styling")
	pflags.BoolVar(&globalFlags.Quiet, "quiet", false, "suppress non-error output")
	pflags.BoolVar(&globalFlags.JSON, "json", false, "emit machine-readable JSON on stdout")
	pflags.BoolVar(&globalFlags.Verbose, "verbose", false, "increase log verbosity")

	root.AddCommand(
		newInitCmd(),
		newPushCmd(),
		newPullCmd(),
		newStatusCmd(),
		newDiffCmd(),
		newConfigCmd(),
		newTUICmd(),
	)
	return root
}

// Execute runs the CLI. It returns the program's exit code.
func Execute() error {
	cmd := NewRootCmd()

	// Install signal handler that translates Ctrl-C into a clean exit.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\ninterrupted")
		os.Exit(130)
	}()

	if err := cmd.Execute(); err != nil {
		var ec *exitCodeError
		if errors.As(err, &ec) {
			return ec
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return &exitCodeError{Code: ExitGeneric, Err: err}
	}
	return nil
}

// exitCodeError allows commands to return a specific exit code.
type exitCodeError struct {
	Code int
	Err  error
}

func (e *exitCodeError) Error() string { return e.Err.Error() }
func (e *exitCodeError) Unwrap() error { return e.Err }

// ExitCode returns the code stored in err, or 0 if err is nil, or 1 otherwise.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ec *exitCodeError
	if errors.As(err, &ec) {
		return ec.Code
	}
	return 1
}

// loadContext loads the config and state, opens the audit log, and
// returns a PersistentState ready for a subcommand to use.
func loadContext() (*PersistentState, error) {
	cfg, err := config.Load(globalFlags.ConfigPath)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return nil, fmt.Errorf("config not found at %s. Create one or pass --config.", globalFlags.ConfigPath)
		}
		return nil, err
	}

	statePath := sync.StatePath()
	auditLogger, err := audit.Open(audit.Path())
	if err != nil {
		return nil, err
	}

	return &PersistentState{
		Config:    cfg,
		StatePath: statePath,
		Audit:     auditLogger,
		Backends:  backend.Default,
		StartTime: time.Now(),
	}, nil
}

// buildBackend constructs a configured backend from the loaded config.
func buildBackend(cfg *config.Config) (backend.Backend, error) {
	switch cfg.Backend {
	case "local":
		return buildLocalBackend(cfg)
	case "webdav":
		return buildWebDAVBackend(cfg)
	case "github":
		return buildGitHubBackend(cfg)
	default:
		return nil, fmt.Errorf("unknown backend %q", cfg.Backend)
	}
}

// Joins are filled in by build_backend_*.go to keep the file count
// manageable. See build_local.go, build_webdav.go, build_github.go.
var (
	buildLocalBackend   func(*config.Config) (backend.Backend, error)
	buildWebDAVBackend  func(*config.Config) (backend.Backend, error)
	buildGitHubBackend  func(*config.Config) (backend.Backend, error)
)

// maybeWarnOmpRunning prints a warning if omp is running.
func maybeWarnOmpRunning(out io.Writer) {
	if omp.IsRunning() {
		fmt.Fprintln(out,
			"warning: an omp process appears to be running. Restart omp after this command so the changes take effect.")
	}
}
