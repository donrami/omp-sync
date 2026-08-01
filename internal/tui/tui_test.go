package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/donrami/omp-sync/internal/backend"
)

type stubBackend struct {
	id      backend.SnapshotID
	snaps   []backend.SnapshotInfo
	listErr error
}

func (s *stubBackend) Name() string { return "stub" }
func (s *stubBackend) Verify(ctx context.Context) error { return nil }
func (s *stubBackend) CurrentSnapshot(ctx context.Context) (backend.SnapshotID, error) {
	if s.id == "" {
		return "", backend.ErrNoSnapshot
	}
	return s.id, nil
}
func (s *stubBackend) ListSnapshots(ctx context.Context, limit int) ([]backend.SnapshotInfo, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.snaps, nil
}
func (s *stubBackend) UploadSnapshot(ctx context.Context, rootDir string, expectedPrevious backend.SnapshotID) (backend.SnapshotID, error) {
	return "", errors.New("not implemented")
}
func (s *stubBackend) DownloadSnapshot(ctx context.Context, id backend.SnapshotID, destDir string) error {
	return errors.New("not implemented")
}

func TestModel_New(t *testing.T) {
	b := &stubBackend{id: "abc12345"}
	m := New("local", b, []backend.SnapshotInfo{
		{ID: "abc12345", CreatedAt: time.Now()},
	})
	if m.backendName != "local" {
		t.Errorf("backendName: got %q", m.backendName)
	}
	if m.mode != ModeList {
		t.Errorf("mode: got %d", m.mode)
	}
	if m.ExecPath == "" {
		t.Error("ExecPath should be populated")
	}
}

func TestActionLabel(t *testing.T) {
	if actionLabel(confirmPush) == "" {
		t.Error("push label should be non-empty")
	}
	if actionLabel(confirmPull) == "" {
		t.Error("pull label should be non-empty")
	}
	if actionLabel(confirmNone) == "" {
		t.Error("none label should be non-empty")
	}
}

func keyMsg(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code}
}

func TestHandleKey_Navigation(t *testing.T) {
	m := New("local", &stubBackend{}, []backend.SnapshotInfo{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	})
	if m.cursor != 0 {
		t.Errorf("initial cursor: got %d", m.cursor)
	}
	model, _ := m.handleKey(keyMsg('j'))
	m = model.(*Model)
	if m.cursor != 1 {
		t.Errorf("after j: got %d", m.cursor)
	}
	model, _ = m.handleKey(keyMsg('k'))
	m = model.(*Model)
	if m.cursor != 0 {
		t.Errorf("after k (cursor=1): got %d", m.cursor)
	}
	for range 4 {
		model, _ = m.handleKey(keyMsg('j'))
	}
	m = model.(*Model)
	if m.cursor != 2 {
		t.Errorf("after multiple j: got %d", m.cursor)
	}
}

func TestHandleKey_PromptAndCancel(t *testing.T) {
	m := New("local", &stubBackend{}, nil)
	model, _ := m.handleKey(keyMsg('p'))
	m = model.(*Model)
	if m.mode != ModeConfirm {
		t.Errorf("after p: got mode=%d", m.mode)
	}
	if m.confirm != confirmPush {
		t.Errorf("after p: got confirm=%d", m.confirm)
	}
	model, _ = m.handleKey(keyMsg('n'))
	m = model.(*Model)
	if m.mode != ModeList {
		t.Errorf("after n: got mode=%d", m.mode)
	}
}

func TestHandleKey_Quit(t *testing.T) {
	m := New("local", &stubBackend{}, nil)
	model, cmd := m.handleKey(keyMsg('q'))
	m = model.(*Model)
	if !m.quitting {
		t.Error("expected quitting=true after q")
	}
	if cmd == nil {
		t.Error("expected a quit cmd")
	}
}

func TestRunConfirmed_NoPathsFails(t *testing.T) {
	m := New("local", &stubBackend{}, nil)
	m.ExecPath = ""
	m.ConfigPath = ""
	cmd := m.runConfirmed()
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	if m.err == nil {
		t.Error("expected error when exec/config are missing")
	}
}

func TestRenderList_Empty(t *testing.T) {
	m := New("local", &stubBackend{}, nil)
	out := renderList(m)
	if !strings.Contains(out, "no snapshots yet") {
		t.Errorf("expected empty placeholder, got: %q", out)
	}
}

func TestRenderList_Populated(t *testing.T) {
	m := New("local", &stubBackend{}, []backend.SnapshotInfo{
		{ID: "abcdefghijklmnop", CreatedAt: time.Now()},
	})
	out := renderList(m)
	if !strings.Contains(out, "abcdefghij") {
		t.Errorf("expected short id, got: %q", out)
	}
}

func TestRenderConfirm(t *testing.T) {
	m := New("local", &stubBackend{}, nil)
	out := renderConfirm(m, "Push local snapshot?")
	if !strings.Contains(out, "Push local snapshot?") {
		t.Errorf("renderConfirm: %q", out)
	}
}

func TestRenderActionResult_OK(t *testing.T) {
	m := New("local", &stubBackend{}, nil)
	m.LastAction = ActionResult{
		Op:       "push",
		Output:   "Pushed 3 files.",
		ExitCode: 0,
		Duration: 123 * time.Millisecond,
	}
	out := renderActionResult(m)
	if !strings.Contains(out, "OK") {
		t.Errorf("expected OK in result, got: %q", out)
	}
	if !strings.Contains(out, "Pushed 3 files.") {
		t.Errorf("expected output in result, got: %q", out)
	}
}

func TestRenderActionResult_Failed(t *testing.T) {
	m := New("local", &stubBackend{}, nil)
	m.LastAction = ActionResult{
		Op:        "push",
		ErrOutput: "remote has changed",
		ExitCode:  2,
		Duration:  10 * time.Millisecond,
	}
	out := renderActionResult(m)
	if !strings.Contains(out, "FAILED") {
		t.Errorf("expected FAILED in result, got: %q", out)
	}
	if !strings.Contains(out, "remote has changed") {
		t.Errorf("expected stderr in result, got: %q", out)
	}
}

func TestShortID(t *testing.T) {
	if got := shortID("abcdefghij"); got != "abcdefghij" {
		t.Errorf("short id (10 chars): got %q", got)
	}
	if got := shortID("abcdefghijklmnop"); got != "abcdefghij" {
		t.Errorf("short id (long): got %q", got)
	}
	if got := shortID(""); got != "" {
		t.Errorf("short id (empty): got %q", got)
	}
}
