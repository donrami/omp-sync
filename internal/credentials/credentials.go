// Package credentials resolves credential references against environment
// variables and the OS keyring.
//
// Lookup order for a reference named "github_pat":
//  1. Environment variable OMP_SYNC_GITHUB_PAT
//  2. Keyring entry "github_pat" (via zalando/go-keyring)
//
// The CLI never stores raw secrets in the config file; it stores references.
package credentials

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

// ErrNotFound is returned when no credential could be located.
var ErrNotFound = errors.New("credential not found")

// EnvName returns the environment-variable name a credential would be
// looked up under. Useful for actionable error messages (SC-007).
func EnvName(name string) string {
	return "OMP_SYNC_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// Lookup resolves a credential by name. Returns ErrNotFound if absent.
// If the lookup fails because of a missing keyring entry AND no env var
// was provided, the wrapped error includes the expected env var name so
// the user can act on it.
func Lookup(name string) (string, error) {
	if name == "" {
		return "", errors.New("credential name is empty")
	}
	if v := envLookup(name); v != "" {
		return v, nil
	}
	v, err := keyring.Get(keyringService, name)
	if err == nil {
		return v, nil
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return "", fmt.Errorf("%w (set %s or store it with `omp-sync config set credential %s`)", ErrNotFound, EnvName(name), name)
	}
	return "", fmt.Errorf("keyring: %w", err)
}

// Store sets a credential in the keyring. Used by `omp-sync config set` for
// interactive flows.
func Store(name, value string) error {
	if name == "" {
		return errors.New("credential name is empty")
	}
	return keyring.Set(keyringService, name, value)
}

// Delete removes a credential from the keyring.
func Delete(name string) error {
	if err := keyring.Delete(keyringService, name); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

const keyringService = "omp-sync"

// envLookup searches env vars in priority order. The first non-empty match wins.
func envLookup(name string) string {
	candidates := []string{
		EnvName(name),
		"OMP_SYNC_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")),
	}
	seen := map[string]bool{}
	for _, c := range candidates {
		if seen[c] {
			continue
		}
		seen[c] = true
		if v := os.Getenv(c); v != "" {
			return v
		}
	}
	return ""
}

// testKeyring is a process-wide hook used by tests to override the keyring.
var (
	keyringMu sync.RWMutex
	keyringFn = func(service, name string) (string, error) {
		return keyring.Get(service, name)
	}
)
