package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	t.Setenv("OMP_SYNC_CONFIG", "/tmp/omp-sync-test/config.toml")
	if got := DefaultPath(); got != "/tmp/omp-sync-test/config.toml" {
		t.Errorf("DefaultPath with env: got %q", got)
	}
}

func TestValidate_Local(t *testing.T) {
	c := &Config{
		Backend: "local",
		OmpDir:  "/tmp/omp",
		Local:   &LocalBlock{Path: "/tmp/remote"},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate local: %v", err)
	}
}

func TestValidate_RequiresBackend(t *testing.T) {
	c := &Config{OmpDir: "/tmp/omp"}
	if err := c.Validate(); err == nil {
		t.Error("expected error when backend is missing")
	}
}

func TestValidate_RequiresOmpDir(t *testing.T) {
	c := &Config{Backend: "local"}
	if err := c.Validate(); err == nil {
		t.Error("expected error when omp_dir is missing")
	}
}

func TestValidate_RejectsRelativePaths(t *testing.T) {
	c := &Config{Backend: "local", OmpDir: "relative/path", Local: &LocalBlock{Path: "/tmp"}}
	if err := c.Validate(); err == nil {
		t.Error("expected error for relative omp_dir")
	}
	c = &Config{Backend: "local", OmpDir: "/tmp", Local: &LocalBlock{Path: "relative"}}
	if err := c.Validate(); err == nil {
		t.Error("expected error for relative local.path")
	}
}

func TestValidate_RejectsSameOmpDirAndLocalPath(t *testing.T) {
	c := &Config{
		Backend: "local",
		OmpDir:  "/tmp/same",
		Local:   &LocalBlock{Path: "/tmp/same"},
	}
	if err := c.Validate(); err == nil {
		t.Error("expected error when local.path equals omp_dir")
	}
}

func TestValidate_UnknownBackend(t *testing.T) {
	c := &Config{
		Backend: "mystery",
		OmpDir:  "/tmp/omp",
	}
	if err := c.Validate(); err == nil {
		t.Error("expected error for unknown backend")
	}
}

func TestValidate_WebDAVMissingFields(t *testing.T) {
	c := &Config{
		Backend: "webdav",
		OmpDir:  "/tmp/omp",
	}
	if err := c.Validate(); err == nil {
		t.Error("expected error when [webdav] block is missing")
	}
	c.WebDAV = &WebDAVBlock{URL: "https://x", Username: "u"}
	if err := c.Validate(); err == nil {
		t.Error("expected error when credential is missing")
	}
}

func TestValidate_GitHubDefaults(t *testing.T) {
	c := &Config{
		Backend: "github",
		OmpDir:  "/tmp/omp",
		GitHub:  &GitHubBlock{Repo: "https://example.com/u/r.git", Credential: "tok"},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate github: %v", err)
	}
	if c.GitHub.Branch != "main" {
		t.Errorf("default branch: got %q", c.GitHub.Branch)
	}
	if c.GitHub.AuthorName == "" {
		t.Error("default author name not set")
	}
	if c.GitHub.AuthorEmail == "" {
		t.Error("default author email not set")
	}
}

func TestEffectiveInclude(t *testing.T) {
	c := &Config{}
	got := c.EffectiveInclude()
	if len(got) != 1 || got[0] != "**" {
		t.Errorf("default include: got %v", got)
	}
	c.Include = []string{"a", "b"}
	got = c.EffectiveInclude()
	if len(got) != 2 {
		t.Errorf("override: got %v", got)
	}
}

func TestEffectiveExclude(t *testing.T) {
	c := &Config{}
	if got := c.EffectiveExclude(); len(got) != 0 {
		t.Errorf("default exclude: got %v", got)
	}
	c.Exclude = []string{"x"}
	if got := c.EffectiveExclude(); len(got) != 1 {
		t.Errorf("override exclude: got %v", got)
	}
}

func TestLoad_NotFound(t *testing.T) {
	if _, err := Load("/nonexistent/omp-sync/config.toml"); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadAndValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
backend = "local"
omp_dir = "/tmp/omp"
include = ["agents/**"]
exclude = ["env.local.json"]

[local]
path = "/tmp/remote"
`
	if err := writeFile(path, body); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Backend != "local" {
		t.Errorf("backend: got %q", cfg.Backend)
	}
	if cfg.Include[0] != "agents/**" {
		t.Errorf("include: got %v", cfg.Include)
	}
	if cfg.Exclude[0] != "env.local.json" {
		t.Errorf("exclude: got %v", cfg.Exclude)
	}
}

func TestLoadAndValidate_BadPattern(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
backend = "local"
omp_dir = "/tmp/omp"
include = ["["]

[local]
path = "/tmp/remote"
`
	if err := writeFile(path, body); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Error("expected validation error for malformed pattern")
	} else if !strings.Contains(err.Error(), "include") {
		t.Errorf("expected include error, got %v", err)
	}
}

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}
