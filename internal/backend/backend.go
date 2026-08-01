// Package backend defines the storage abstraction for snapshots.
//
// A backend can store, retrieve, and promote snapshots. The shipped
// implementations are local, WebDAV, and GitHub. Third-party backends
// implement the Backend interface and register with the Registry.
package backend

import (
	"context"
	"errors"
	"fmt"

	"sort"
	"sync"
	"time"

	"github.com/example/omp-sync/internal/snapshot"
)

// Sentinel errors. Backends return these; the engine maps them to messages.
var (
	// ErrConflict is returned when the backend's current snapshot id differs from
	// the expected previous one (FR-009).
	ErrConflict = errors.New("remote has changed since last sync")
	// ErrNoSnapshot is returned when no snapshot has ever been published.
	ErrNoSnapshot = errors.New("no remote snapshot found")
	// ErrAuth is returned when credentials are missing or invalid.
	ErrAuth = errors.New("authentication failed")
	// ErrUnreachable is returned for any other backend access failure.
	ErrUnreachable = errors.New("backend unreachable")
)

// SnapshotID is a backend-native identifier. Clients compare strings only.
type SnapshotID string

// Snapshot is a snapshot file set plus identification metadata.
type Snapshot struct {
	ID        SnapshotID
	CreatedAt time.Time
	Files     []snapshot.FileEntry
}

// Backend stores and retrieves snapshots.
type Backend interface {
	Name() string
	Verify(ctx context.Context) error
	CurrentSnapshot(ctx context.Context) (SnapshotID, error)
	// UploadSnapshot atomically publishes a new snapshot and makes it
	// current. expectedPrevious is the id the caller believes is current;
	// returns ErrConflict if the backend's current id differs.
	// rootDir is a local directory containing manifest.json and files/.
	UploadSnapshot(ctx context.Context, rootDir string, expectedPrevious SnapshotID) (SnapshotID, error)
	// DownloadSnapshot populates destDir with the snapshot's manifest.json
	// and files/. Returns the snapshot id.
	DownloadSnapshot(ctx context.Context, id SnapshotID, destDir string) error
	// ListSnapshots returns snapshot ids in chronological order (oldest first).
	// Used by the TUI.
	ListSnapshots(ctx context.Context, limit int) ([]SnapshotInfo, error)
}

// SnapshotInfo is a minimal projection used by status and the TUI.
type SnapshotInfo struct {
	ID        SnapshotID
	CreatedAt time.Time
}

// Factory constructs a fully-configured backend from a parsed Config.
// The CLI looks up factories by name and calls them when it needs
// a backend instance.
type Factory func() (Backend, error)

// Registry tracks backend factories. Built-in backends register here.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

// Register adds a backend factory. Returns an error if the name is
// already registered.
func (r *Registry) Register(name string, f Factory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.factories[name]; ok {
		return fmt.Errorf("backend %q already registered", name)
	}
	r.factories[name] = f
	return nil
}

// MustRegister registers a factory and panics on error. Use only in init.
func (r *Registry) MustRegister(name string, f Factory) {
	if err := r.Register(name, f); err != nil {
		panic(err)
	}
}

// Build returns a new backend instance by name.
func (r *Registry) Build(name string) (Backend, error) {
	r.mu.RLock()
	f, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("backend %q not registered", name)
	}
	return f()
}

// List returns the registered backend names in lexicographic order.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for n := range r.factories {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Default is the process-wide registry. Built-in backends register here.
var Default = NewRegistry()
