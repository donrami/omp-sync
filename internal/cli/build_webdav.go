package cli

import (
	"fmt"

	"github.com/example/omp-sync/internal/backend"
	"github.com/example/omp-sync/internal/backends/webdav"
	"github.com/example/omp-sync/internal/config"
)

func init() {
	buildWebDAVBackend = func(cfg *config.Config) (backend.Backend, error) {
		if cfg.WebDAV == nil {
			return nil, errMissing("webdav")
		}
		return webdav.NewConfigured(
			cfg.WebDAV.URL,
			cfg.WebDAV.Username,
			cfg.WebDAV.Credential,
			cfg.WebDAV.Path,
		)()
	}
}

func errMissing(name string) error {
	return fmt.Errorf("missing [%s] block in config", name)
}

var _ = backend.Default
