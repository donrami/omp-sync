package cli

import (
	"github.com/example/omp-sync/internal/backend"
	"github.com/example/omp-sync/internal/backends/github"
	"github.com/example/omp-sync/internal/config"
)

func init() {
	buildGitHubBackend = func(cfg *config.Config) (backend.Backend, error) {
		if cfg.GitHub == nil {
			return nil, errMissing("github")
		}
		return github.NewConfigured(
			cfg.GitHub.Repo,
			cfg.GitHub.Branch,
			cfg.GitHub.Credential,
			cfg.GitHub.AuthorName,
			cfg.GitHub.AuthorEmail,
		)()
	}
}

var _ = backend.Default
