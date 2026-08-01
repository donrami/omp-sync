package cli

import (
	"github.com/example/omp-sync/internal/backend"
	"github.com/example/omp-sync/internal/backends/local"
	"github.com/example/omp-sync/internal/config"
)

func init() {
	buildLocalBackend = func(cfg *config.Config) (backend.Backend, error) {
		if cfg.Local == nil {
			return nil, errMissing("local")
		}
		return local.NewConfigured(cfg.Local.Path)()
	}
}
