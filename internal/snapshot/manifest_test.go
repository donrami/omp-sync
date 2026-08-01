package snapshot

import (
	"strings"
	"testing"
	"time"
)

func TestCanonicalJSON_SortedKeys(t *testing.T) {
	m := Manifest{
		Version:     1,
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ToolVersion: "0.1.0",
		Files: []FileEntry{
			{Path: "b.md", Size: 10, SHA256: strings.Repeat("a", 64)},
			{Path: "a.md", Size: 5, SHA256: strings.Repeat("b", 64)},
		},
	}
	a, err := m.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	b, err := m.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if string(a) != string(b) {
		t.Errorf("not deterministic: %s vs %s", a, b)
	}
	if !strings.HasPrefix(string(a), `{"created_at":`) {
		t.Errorf("expected keys sorted, got: %s", a)
	}
}

func TestValidate(t *testing.T) {
	m := Manifest{
		Version:     1,
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ToolVersion: "0.1.0",
		Files: []FileEntry{
			{Path: "/abs/path", Size: 1, SHA256: strings.Repeat("a", 64)},
		},
	}
	if err := m.Validate("/tmp/omp"); err == nil {
		t.Error("expected error for absolute path")
	}

	m.Files = []FileEntry{{Path: "..", Size: 1, SHA256: strings.Repeat("a", 64)}}
	if err := m.Validate("/tmp/omp"); err == nil {
		t.Error("expected error for ..")
	}

	m.Files = []FileEntry{{Path: "ok.md", Size: 1, SHA256: ""}}
	if err := m.Validate("/tmp/omp"); err == nil {
		t.Error("expected error for missing sha256")
	}
}
