package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendAndRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	l, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := l.Append(Entry{
		Op:      "push",
		Backend: "local",
		Exit:    0,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	var e Entry
	if err := json.Unmarshal(data[:len(data)-1], &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Op != "push" {
		t.Errorf("Op: got %q", e.Op)
	}
	if e.Timestamp == "" {
		t.Error("timestamp empty")
	}
}
