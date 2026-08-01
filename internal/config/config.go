// Package config loads and validates the omp-sync configuration file.
//
// The default path is $XDG_CONFIG_HOME/omp-sync/config.toml. Users can
// override with --config or the OMP_SYNC_CONFIG environment variable.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/donrami/omp-sync/internal/filter"
	"github.com/pelletier/go-toml/v2"
)

// Config is the top-level configuration.
type Config struct {
	Backend string       `toml:"backend"`
	OmpDir  string       `toml:"omp_dir"`
	Include []string     `toml:"include"`
	Exclude []string     `toml:"exclude"`
	WebDAV  *WebDAVBlock `toml:"webdav"`
	GitHub  *GitHubBlock `toml:"github"`
	Local   *LocalBlock  `toml:"local"`
}

// WebDAVBlock configures the WebDAV backend.
type WebDAVBlock struct {
	URL        string `toml:"url"`
	Username   string `toml:"username"`
	Credential string `toml:"credential"`
	Path       string `toml:"path"`
}

// GitHubBlock configures the GitHub backend.
type GitHubBlock struct {
	Repo         string `toml:"repo"`
	Branch       string `toml:"branch"`
	Credential   string `toml:"credential"`
	AuthorName   string `toml:"author_name"`
	AuthorEmail  string `toml:"author_email"`
}

// LocalBlock configures the local-filesystem backend.
type LocalBlock struct {
	Path string `toml:"path"`
}

// ErrNotFound is returned when the config file does not exist.
var ErrNotFound = errors.New("config file not found")

// DefaultPath returns the default config path, taking XDG_CONFIG_HOME and
// the OMP_SYNC_CONFIG env var into account.
func DefaultPath() string {
	if v := os.Getenv("OMP_SYNC_CONFIG"); v != "" {
		return v
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "omp-sync", "config.toml")
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".config", "omp-sync", "config.toml")
}

// Load reads the config from path. Returns ErrNotFound if the file does
// not exist. Errors are human-readable.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is user-provided
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var c Config
	dec := toml.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config in %s: %w", path, err)
	}
	return &c, nil
}

// Validate enforces the schema documented in data-model.md.
func (c *Config) Validate() error {
	if c.Backend == "" {
		return errors.New("backend is required")
	}
	if c.OmpDir == "" {
		return errors.New("omp_dir is required")
	}
	if !filepath.IsAbs(c.OmpDir) {
		return fmt.Errorf("omp_dir must be absolute: %q", c.OmpDir)
	}

	if err := filter.Validate(c.Include); err != nil {
		return fmt.Errorf("include: %w", err)
	}
	if err := filter.Validate(c.Exclude); err != nil {
		return fmt.Errorf("exclude: %w", err)
	}

	switch c.Backend {
	case "webdav":
		if c.WebDAV == nil {
			return errors.New("[webdav] block is required")
		}
		if c.WebDAV.URL == "" {
			return errors.New("webdav.url is required")
		}
		if c.WebDAV.Username == "" {
			return errors.New("webdav.username is required")
		}
		if c.WebDAV.Credential == "" {
			return errors.New("webdav.credential is required")
		}
	case "github":
		if c.GitHub == nil {
			return errors.New("[github] block is required")
		}
		if c.GitHub.Repo == "" {
			return errors.New("github.repo is required")
		}
		if c.GitHub.Credential == "" {
			return errors.New("github.credential is required")
		}
		if c.GitHub.Branch == "" {
			c.GitHub.Branch = "main"
		}
		if c.GitHub.AuthorName == "" {
			c.GitHub.AuthorName = "omp-sync"
		}
		if c.GitHub.AuthorEmail == "" {
			c.GitHub.AuthorEmail = "omp-sync@localhost"
		}
	case "local":
		if c.Local == nil {
			return errors.New("[local] block is required")
		}
		if c.Local.Path == "" {
			return errors.New("local.path is required")
		}
		if !filepath.IsAbs(c.Local.Path) {
			return fmt.Errorf("local.path must be absolute: %q", c.Local.Path)
		}
		if samePath(c.Local.Path, c.OmpDir) {
			return errors.New("local.path must differ from omp_dir")
		}
	default:
		return fmt.Errorf("unknown backend %q (built-in: local, webdav, github)", c.Backend)
	}
	return nil
}

func samePath(a, b string) bool {
	aa, err := filepath.Abs(a)
	if err != nil {
		return false
	}
	bb, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	return aa == bb
}

// EffectiveInclude returns the include set (defaulting to ["**"]).
func (c *Config) EffectiveInclude() []string {
	if len(c.Include) == 0 {
		return filter.DefaultInclude
	}
	return c.Include
}

// EffectiveExclude returns the exclude set (defaulting to []).
func (c *Config) EffectiveExclude() []string {
	return c.Exclude
}

// Marshal returns the TOML encoding of c, useful for `config set`.
func (c *Config) Marshal() ([]byte, error) {
	return toml.Marshal(c)
}
