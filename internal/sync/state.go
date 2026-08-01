// Package sync state persistence.
package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State is the per-machine sync bookkeeping.
type State struct {
	mu sync.Mutex

	// SchemaVersion is the version of this state file format.
	SchemaVersion int `json:"schema_version"`
	// Backends maps backend names to their last-known state.
	Backends map[string]BackendState `json:"backends"`
}

// BackendState is the per-backend portion of the state.
type BackendState struct {
	LastSnapshotID string    `json:"last_snapshot_id"`
	LastSyncedAt   time.Time `json:"last_synced_at"`
	LastOp         string    `json:"last_op"`
}

// StatePath returns the default state file path.
func StatePath() string {
	if v := os.Getenv("OMP_SYNC_STATE"); v != "" {
		return v
	}
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "omp-sync", "state.json")
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".local", "state", "omp-sync", "state.json")
}

// LoadState reads the state file from path. Returns an empty State if
// the file does not exist.
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path) //nolint:gosec // controlled path
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &State{SchemaVersion: 1, Backends: map[string]BackendState{}}, nil
		}
		return nil, fmt.Errorf("read state: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	if s.Backends == nil {
		s.Backends = map[string]BackendState{}
	}
	return &s, nil
}

// Save writes the state file atomically.
func (s *State) Save(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir state: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// Get returns the state for the given backend (zero value if absent).
func (s *State) Get(backend string) BackendState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Backends[backend]
}

// Set updates the state for the given backend.
func (s *State) Set(backend string, bs BackendState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Backends[backend] = bs
}
