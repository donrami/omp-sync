package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/donrami/omp-sync/internal/backend"
	"github.com/donrami/omp-sync/internal/snapshot"
)

// fakeBackendPull is a minimal in-memory backend for pull subset tests.
type fakeBackendPull struct {
	id    backend.SnapshotID
	files map[string][]byte
}

func (f *fakeBackendPull) Name() string { return "fakepull" }
func (f *fakeBackendPull) Verify(ctx context.Context) error { return nil }
func (f *fakeBackendPull) CurrentSnapshot(ctx context.Context) (backend.SnapshotID, error) {
	if f.id == "" {
		return "", backend.ErrNoSnapshot
	}
	return f.id, nil
}
func (f *fakeBackendPull) ListSnapshots(ctx context.Context, limit int) ([]backend.SnapshotInfo, error) {
	if f.id == "" {
		return nil, nil
	}
	return []backend.SnapshotInfo{{ID: f.id}}, nil
}
func (f *fakeBackendPull) UploadSnapshot(ctx context.Context, rootDir string, expectedPrevious backend.SnapshotID) (backend.SnapshotID, error) {
	return "", nil
}
func (f *fakeBackendPull) DownloadSnapshot(ctx context.Context, id backend.SnapshotID, destDir string) error {
	if err := os.MkdirAll(filepath.Join(destDir, snapshot.FilesDir), 0o755); err != nil {
		return err
	}
	m := snapshot.Manifest{Version: snapshot.ManifestVersion, CreatedAt: time.Now().UTC()}
	for p, body := range f.files {
		if err := os.WriteFile(filepath.Join(destDir, snapshot.FilesDir, filepath.FromSlash(p)), body, 0o644); err != nil {
			return err
		}
		m.Files = append(m.Files, snapshot.FileEntry{Path: p, Mode: 0o644, Size: int64(len(body)), SHA256: sha256HexStr(body)})
	}
	data, _ := m.CanonicalJSON()
	return os.WriteFile(filepath.Join(destDir, snapshot.ManifestName), data, 0o644)
}

var tNow = "2026-08-01T00:00:00Z"

func mustParseTime(s string) timeAlias { return timeAlias{} }

type timeAlias struct{}

func TestSubsetForApply(t *testing.T) {
	m := &snapshot.Manifest{
		Files: []snapshot.FileEntry{
			{Path: "agents/coding.md"},
			{Path: "snippets/hello.md"},
			{Path: "README.md"},
		},
	}

	cases := []struct {
		name           string
		include        []string
		exclude        []string
		override       bool
		want           []string
		wantSkipped    int
	}{
		{"no override applies all", nil, nil, false, []string{"agents/coding.md", "snippets/hello.md", "README.md"}, 0},
		{"include agents", []string{"agents/**"}, nil, true, []string{"agents/coding.md"}, 2},
		{"include both dirs", []string{"agents/**", "snippets/**"}, nil, true, []string{"agents/coding.md", "snippets/hello.md"}, 1},
		{"exclude README", nil, []string{"README.md"}, true, []string{"agents/coding.md", "snippets/hello.md"}, 1},
		{"include+exclude", []string{"**"}, []string{"snippets/**"}, true, []string{"agents/coding.md", "README.md"}, 1},
		{"no match", []string{"nope/**"}, nil, true, []string{}, 3},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, skipped := subsetForApply(m, tt.include, tt.exclude, tt.override)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("subset = %v, want %v", got, tt.want)
			}
			if skipped != tt.wantSkipped {
				t.Errorf("skipped = %d, want %d", skipped, tt.wantSkipped)
			}
		})
	}
}

func TestPathMatchesAny(t *testing.T) {
	ok, err := pathMatchesAny("agents/coding.md", []string{"agents/**"})
	if err != nil || !ok {
		t.Errorf("agents/** vs agents/coding.md: ok=%v err=%v", ok, err)
	}
	ok, err = pathMatchesAny("snippets/x.md", []string{"agents/**"})
	if err != nil || ok {
		t.Errorf("agents/** vs snippets/x.md: ok=%v err=%v", ok, err)
	}
	// Empty filter means everything matches (used for include).
	ok, err = pathMatchesAny("anything.md", nil)
	if err != nil || !ok {
		t.Errorf("empty filter: ok=%v err=%v", ok, err)
	}
	// Invalid glob errors.
	if _, err := pathMatchesAny("x", []string{"["}); err == nil {
		t.Error("expected error for invalid glob")
	}
}

func TestDiffJSON_Shape(t *testing.T) {
	out := diffOutput{
		Backend:       "local",
		FilesWithDiff: 1,
		Files: []diffFileEntry{
			{Path: "a.md", Lines: []string{"- x", "+ y"}, HasDiff: true},
		},
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	// JSON keys must match the contract (files_with_diff, has_diff).
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"backend", "files_with_diff", "files"} {
		if _, ok := parsed[k]; !ok {
			t.Errorf("missing key %q in %s", k, data)
		}
	}
}
