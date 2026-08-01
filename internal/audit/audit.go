// Package audit writes append-only JSONL records of every mutating command.
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one record in the audit log.
type Entry struct {
	Timestamp          string `json:"timestamp"`
	Op                 string `json:"op"`
	Backend            string `json:"backend"`
	SnapshotIDBefore   string `json:"snapshot_id_before"`
	SnapshotIDAfter    string `json:"snapshot_id_after"`
	FilesAdded         int    `json:"files_added"`
	FilesModified      int    `json:"files_modified"`
	FilesDeleted       int    `json:"files_deleted"`
	FilesUnchanged     int    `json:"files_unchanged"`
	FilesIgnored       int    `json:"files_ignored"`
	Exit               int    `json:"exit"`
	DurationMS         int64  `json:"duration_ms"`
	Error              string `json:"error,omitempty"`
	OmpDir             string `json:"omp_dir"`
	ToolVersion        string `json:"tool_version"`
}

// Path returns the default audit log path, taking XDG_STATE_HOME into account.
func Path() string {
	if base := os.Getenv("OMP_SYNC_AUDIT_LOG"); base != "" {
		return base
	}
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "omp-sync", "audit.log")
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".local", "state", "omp-sync", "audit.log")
}

// Logger is an append-only audit log writer. Concurrent-safe.
type Logger struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

// Open returns a Logger that appends to the given path. The file is created
// if it does not exist.
func Open(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir audit dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open audit log: %w", err)
	}
	return &Logger{path: path, f: f}, nil
}

// Close flushes and closes the underlying file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// Append writes a single entry as one JSON line. The timestamp is set if
// the entry's timestamp is empty.
func (l *Logger) Append(e Entry) error {
	if e.Timestamp == "" {
		e.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal audit entry: %w", err)
	}
	data = append(data, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return fmt.Errorf("audit logger is closed")
	}
	if _, err := l.f.Write(data); err != nil {
		return fmt.Errorf("write audit entry: %w", err)
	}
	return nil
}

// File returns the path on disk.
func (l *Logger) File() string { return l.path }
