package webdav

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donrami/omp-sync/internal/backend"
	"github.com/donrami/omp-sync/internal/credentials"
	"github.com/donrami/omp-sync/internal/snapshot"
)

// davFixture is a minimal DAV server backed by a temp directory.
type davFixture struct {
	ts      *httptest.Server
	rootDir string
}

// newDAVFixture constructs an httptest server backed by a temp directory.
func newDAVFixture(t *testing.T) *davFixture {
	t.Helper()
	root := t.TempDir()
	ts := httptest.NewServer(davHandler(root))
	t.Cleanup(ts.Close)
	return &davFixture{ts: ts, rootDir: root}
}

// toLocal maps an HTTP request URL path to a local filesystem path under
// the fixture root. For `r.URL.Path` it strips the leading slash. For
// absolute destinations (the `Destination` header in MOVE/COPY) it
// parses out the path component.
func toLocal(root, p string) string {
	// Try absolute URL first.
	if u, err := url.Parse(p); err == nil && u.Scheme != "" {
		p = u.Path
	}
	p = strings.TrimPrefix(p, "/")
	return filepath.Join(root, filepath.FromSlash(p))
}

func davHandler(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "PROPFIND":
			handlePropfind(w, r, root)
		case http.MethodGet, http.MethodHead:
			handleGet(w, r, root)
		case "MKCOL":
			handleMkcol(w, r, root)
		case "PUT":
			handlePut(w, r, root)
		case "MOVE":
			handleMove(w, r, root)
		case "DELETE":
			handleDelete(w, r, root)
		default:
			http.Error(w, "unsupported", http.StatusMethodNotAllowed)
		}
	})
}

func handlePropfind(w http.ResponseWriter, r *http.Request, root string) {
	abs := toLocal(root, r.URL.Path)
	info, err := os.Stat(abs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	xml := `<?xml version="1.0"?><multistatus xmlns="DAV:">`
	xml += `<response><propstat><prop>`
	if info.IsDir() {
		xml += `<resourcetype><collection/></resourcetype>`
	} else {
		xml += `<resourcetype/>`
	}
	xml += `</prop><status>HTTP/1.1 200 OK</status></propstat></response>`
	if info.IsDir() {
		if entries, err := os.ReadDir(abs); err == nil {
			for _, e := range entries {
				xml += `<response><href>/` + r.URL.Path
				if !strings.HasSuffix(r.URL.Path, "/") {
					xml += "/"
				}
				xml += url.PathEscape(e.Name()) + `</href><propstat><prop>`
				if e.IsDir() {
					xml += `<resourcetype><collection/></resourcetype>`
				} else {
					xml += `<resourcetype/>`
				}
				xml += `</prop><status>HTTP/1.1 200 OK</status></propstat></response>`
			}
		}
	}
	xml += `</multistatus>`
	_, _ = w.Write([]byte(xml))
}

func handleGet(w http.ResponseWriter, r *http.Request, root string) {
	cleanPath := strings.TrimPrefix(r.URL.Path, "/")
	if cleanPath == "" {
		http.Error(w, "root not allowed", http.StatusForbidden)
		return
	}
	http.ServeFile(w, r, toLocal(root, r.URL.Path))
}

func handleMkcol(w http.ResponseWriter, r *http.Request, root string) {
	if err := os.MkdirAll(toLocal(root, r.URL.Path), 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func handlePut(w http.ResponseWriter, r *http.Request, root string) {
	cleanPath := strings.TrimPrefix(r.URL.Path, "/")
	if cleanPath == "" {
		http.Error(w, "PUT on root denied", http.StatusForbidden)
		return
	}
	abs := toLocal(root, r.URL.Path)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(abs, body, 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func handleMove(w http.ResponseWriter, r *http.Request, root string) {
	dst := r.Header.Get("Destination")
	if dst == "" {
		http.Error(w, "missing Destination", http.StatusBadRequest)
		return
	}
	srcAbs := toLocal(root, r.URL.Path)
	dstAbs := toLocal(root, dst)
	if err := os.Rename(srcAbs, dstAbs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func handleDelete(w http.ResponseWriter, r *http.Request, root string) {
	if err := os.RemoveAll(toLocal(root, r.URL.Path)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func installCredential(t *testing.T, name, value string) {
	t.Helper()
	t.Setenv(credentials.EnvName(name), value)
}

func newTestBackend(t *testing.T, f *davFixture) backend.Backend {
	t.Helper()
	installCredential(t, "wptest", "secret")
	b, err := NewConfigured(f.ts.URL+"/omp-sync", "alice", "wptest", "/")()
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	return b
}

func TestWebDAV_Basics(t *testing.T) {
	f := newDAVFixture(t)
	b := newTestBackend(t, f)

	if err := b.Verify(context.Background()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if _, err := b.CurrentSnapshot(context.Background()); err != backend.ErrNoSnapshot {
		t.Fatalf("expected ErrNoSnapshot, got %v", err)
	}

	tmpSnap := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpSnap, "files", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpSnap, "files", "agents", "a.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"version":1,"created_at":"2026-07-31T00:00:00Z","tool_version":"test",
		"files":[{"path":"agents/a.md","mode":420,"size":2,
		"sha256":"` + shaOf("hi") + `","executable":false}]
	}`
	if err := os.WriteFile(filepath.Join(tmpSnap, snapshot.ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	id, err := b.UploadSnapshot(context.Background(), tmpSnap, "")
	if err != nil {
		t.Fatalf("UploadSnapshot: %v", err)
	}
	if id == "" {
		t.Fatal("empty id")
	}

	cur, err := b.CurrentSnapshot(context.Background())
	if err != nil || cur != id {
		t.Errorf("current: id=%v err=%v", cur, err)
	}

	dst := t.TempDir()
	if err := b.DownloadSnapshot(context.Background(), id, dst); err != nil {
		t.Fatalf("DownloadSnapshot: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "files", "agents", "a.md")) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hi" {
		t.Errorf("downloaded body: got %q", got)
	}

	if _, err := b.UploadSnapshot(context.Background(), tmpSnap, "deadbeef"); err == nil {
		t.Error("expected conflict")
	}
}

func TestNewConfigured_MissingFields(t *testing.T) {
	cases := []struct {
		name string
		f    func() (backend.Backend, error)
	}{
		{"empty url", func() (backend.Backend, error) {
			return NewConfigured("", "alice", "cred", "/")()
		}},
		{"empty user", func() (backend.Backend, error) {
			return NewConfigured("https://x/", "", "cred", "/")()
		}},
		{"empty credential", func() (backend.Backend, error) {
			return NewConfigured("https://x/", "alice", "", "/")()
		}},
		{"missing credential lookup", func() (backend.Backend, error) {
			t.Setenv(credentials.EnvName("missing"), "")
			return NewConfigured("https://example.invalid/", "alice", "missing", "/")()
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.f()
			if !errors.Is(err, backend.ErrAuth) {
				t.Errorf("expected ErrAuth, got %v", err)
			}
		})
	}
}

func shaOf(b string) string {
	sum := sha256.Sum256([]byte(b))
	return hex.EncodeToString(sum[:])
}
