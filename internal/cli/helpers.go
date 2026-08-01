package cli

import (
	"fmt"
	"io"

	"github.com/donrami/omp-sync/internal/config"
)

// exitErr wraps err in an *exitCodeError with the supplied code.
func exitErr(code int, err error) error {
	return &exitCodeError{Code: code, Err: err}
}

// isYes returns true if the user typed one of y/yes/Y/YES.
func isYes(s string) bool {
	switch s {
	case "y", "Y", "yes", "Yes", "YES":
		return true
	}
	return false
}

// loadConfig is a thin wrapper around config.Load that produces a
// user-friendly error if the file is missing.
func loadConfig(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		if err == config.ErrNotFound {
			return nil, fmt.Errorf("config not found at %s. Create one or pass --config.", path)
		}
		return nil, err
	}
	return cfg, nil
}

// silentClose is a small helper for the defer Close pattern in commands.
func silentClose(c io.Closer) {
	if c != nil {
		_ = c.Close()
	}
}
