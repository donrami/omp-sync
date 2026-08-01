// Package snapshot defines the on-disk and on-remote snapshot format.
//
// A snapshot is a point-in-time representation of a user's omp config tree. It is
// the unit of transfer between local and remote. See contracts/snapshot.md for the
// full schema.
package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ManifestVersion is the schema version of the manifest.json file.
const ManifestVersion = 1

// FileEntry describes one file in a snapshot.
type FileEntry struct {
	Path       string `json:"path"`
	Mode       uint32 `json:"mode"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Executable bool   `json:"executable"`
}

// Manifest is the contents of manifest.json.
type Manifest struct {
	Version    int         `json:"version"`
	CreatedAt  time.Time   `json:"created_at"`
	ToolVersion string     `json:"tool_version"`
	Files      []FileEntry `json:"files"`
}

// SnapshotPath is the on-disk layout of a snapshot directory.
const (
	ManifestName = "manifest.json"
	FilesDir     = "files"
)

// ComputeSHA256 returns the lowercase hex SHA-256 of data.
func ComputeSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// FileSHA256 returns the SHA-256 of the file at path.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // path is user-provided and validated by the caller
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CanonicalJSON returns a deterministic JSON encoding of the manifest.
// Keys are sorted lexicographically; no whitespace is emitted; numbers are
// decimal integers. This guarantees identical hashes across runs and platforms.
func (m *Manifest) CanonicalJSON() ([]byte, error) {
	// Marshal to a generic map first so we can enforce key ordering.
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("re-unmarshal manifest: %w", err)
	}
	sorted := sortKeys(v)
	return marshalCanonical(sorted)
}

// Hash returns the SHA-256 of the canonical manifest JSON.
func (m *Manifest) Hash() (string, error) {
	data, err := m.CanonicalJSON()
	if err != nil {
		return "", err
	}
	return ComputeSHA256(data), nil
}

// Validate checks the manifest for safety. It rejects unknown versions,
// paths containing .. or leading /, and any file outside the configured root.
func (m *Manifest) Validate(root string) error {
	if m.Version != ManifestVersion {
		return fmt.Errorf("unsupported manifest version: %d", m.Version)
	}
	if m.CreatedAt.IsZero() {
		return errors.New("manifest created_at is required")
	}
	seen := make(map[string]struct{}, len(m.Files))
	for i, f := range m.Files {
		if f.Path == "" {
			return fmt.Errorf("file[%d]: empty path", i)
		}
		if strings.HasPrefix(f.Path, "/") {
			return fmt.Errorf("file[%d]: absolute path %q not allowed", i, f.Path)
		}
		if strings.Contains(f.Path, "..") {
			return fmt.Errorf("file[%d]: path %q contains ..", i, f.Path)
		}
		cleaned := filepath.Clean(f.Path)
		if cleaned != f.Path {
			return fmt.Errorf("file[%d]: path %q must be already-clean", i, f.Path)
		}
		if _, dup := seen[f.Path]; dup {
			return fmt.Errorf("file[%d]: duplicate path %q", i, f.Path)
		}
		seen[f.Path] = struct{}{}

		// Path-traversal check: the joined absolute path must stay under root.
		abs := filepath.Join(root, filepath.FromSlash(f.Path))
		rel, err := filepath.Rel(root, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("file[%d]: path %q escapes root", i, f.Path)
		}
		if f.SHA256 == "" || len(f.SHA256) != 64 {
			return fmt.Errorf("file[%d]: invalid sha256", i)
		}
	}
	return nil
}

// sortKeys recursively sorts map keys for canonical JSON encoding.
func sortKeys(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		// JSON encoder with sorted keys requires a different approach:
		// we emit a slice of k/v pairs via a custom struct.
		_ = out
		return canonicalMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = sortKeys(e)
		}
		return out
	default:
		return v
	}
}

// canonicalMap is a json.Marshaler that emits keys in lexicographic order.
type canonicalMap map[string]any

func (m canonicalMap) MarshalJSON() ([]byte, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build a custom ordered representation.
	type kv struct {
		Key string
		Val any
	}
	pairs := make([]kv, len(keys))
	for i, k := range keys {
		pairs[i] = kv{k, sortKeys(m[k])}
	}

	var buf strings.Builder
	buf.WriteByte('{')
	for i, p := range pairs {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(p.Key)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(p.Val)
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return []byte(buf.String()), nil
}

// marshalCanonical encodes v canonically: sorted keys, no whitespace.
func marshalCanonical(v any) ([]byte, error) {
	switch t := v.(type) {
	case canonicalMap:
		return json.Marshal(t)
	default:
		return json.Marshal(v)
	}
}
