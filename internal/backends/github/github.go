// Package github implements the GitHub backend using go-git.
//
// The remote is a regular git repository accessed over HTTPS with a
// personal access token. Each snapshot is a single commit on the
// configured branch. The snapshot id is the commit SHA.
package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/donrami/omp-sync/internal/backend"
	"github.com/donrami/omp-sync/internal/credentials"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

// backendImpl is the GitHub backend.
type backendImpl struct {
	repo        string
	branch      string
	credName    string
	authorName  string
	authorEmail string
	workDir     string
}

// NewConfigured returns a backend.Factory that builds a GitHub backend.
func NewConfigured(repo, branch, credName, authorName, authorEmail string) backend.Factory {
	return func() (backend.Backend, error) {
		if repo == "" || credName == "" {
			return nil, fmt.Errorf("%w: repo and credential are required", backend.ErrAuth)
		}
		if branch == "" {
			branch = "main"
		}
		if authorName == "" {
			authorName = "omp-sync"
		}
		if authorEmail == "" {
			authorEmail = "omp-sync@localhost"
		}
		if _, err := credentials.Lookup(credName); err != nil {
			return nil, fmt.Errorf("%w: credential %q", backend.ErrAuth, credName)
		}
		return &backendImpl{
			repo:        repo,
			branch:      branch,
			credName:    credName,
			authorName:  authorName,
			authorEmail: authorEmail,
		}, nil
	}
}

// Name returns "github".
func (b *backendImpl) Name() string { return "github" }

// Verify clones the repo shallow and confirms it is reachable.
func (b *backendImpl) Verify(ctx context.Context) error {
	if _, err := b.openOrClone(); err != nil {
		return fmt.Errorf("%w: %v", backend.ErrUnreachable, err)
	}
	return nil
}

// CurrentSnapshot returns the current HEAD commit SHA, or ErrNoSnapshot if no commits.
func (b *backendImpl) CurrentSnapshot(ctx context.Context) (backend.SnapshotID, error) {
	repo, err := b.openOrClone()
	if err != nil {
		return "", err
	}
	head, err := repo.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) || errors.Is(err, transport.ErrEmptyRemoteRepository) {
			return "", backend.ErrNoSnapshot
		}
		return "", fmt.Errorf("%w: %v", backend.ErrUnreachable, err)
	}
	return backend.SnapshotID(head.Hash().String()), nil
}

// UploadSnapshot writes rootDir's contents into a new commit on the configured branch.
func (b *backendImpl) UploadSnapshot(ctx context.Context, rootDir string, expectedPrevious backend.SnapshotID) (backend.SnapshotID, error) {
	repo, err := b.openOrClone()
	if err != nil {
		return "", err
	}

	cur, err := b.CurrentSnapshot(ctx)
	if err != nil && !errors.Is(err, backend.ErrNoSnapshot) {
		return "", err
	}
	if cur != expectedPrevious {
		return "", fmt.Errorf("%w: have %q, expected %q", backend.ErrConflict, cur, expectedPrevious)
	}

	w, err := repo.Worktree()
	if err != nil {
		return "", err
	}

	if cur != "" {
		if err := w.Reset(&gogit.ResetOptions{
			Mode:   gogit.HardReset,
			Commit: plumbing.NewHash(string(cur)),
		}); err != nil {
			return "", fmt.Errorf("reset: %w", err)
		}
	}

	entries, err := os.ReadDir(b.workDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".git") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(b.workDir, e.Name())); err != nil {
			return "", err
		}
	}

	if err := copyDir(rootDir, b.workDir); err != nil {
		return "", err
	}

	if err := filepath.Walk(b.workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == b.workDir {
			return nil
		}
		rel, err := filepath.Rel(b.workDir, path)
		if err != nil {
			return err
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(os.PathSeparator)) {
			return nil
		}
		if info.IsDir() {
			_, err := w.Add(rel)
			return err
		}
		_, err = w.Add(rel)
		return err
	}); err != nil {
		return "", fmt.Errorf("stage: %w", err)
	}

	author := &object.Signature{Name: b.authorName, Email: b.authorEmail, When: time.Now().UTC()}
	commitHash, err := w.Commit("omp-sync snapshot "+time.Now().UTC().Format(time.RFC3339), &gogit.CommitOptions{Author: author, AllowEmptyCommits: true})
	if err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}

	if err := b.push(repo); err != nil {
		return "", err
	}
	return backend.SnapshotID(commitHash.String()), nil
}

// DownloadSnapshot checks out the given commit into destDir.
func (b *backendImpl) DownloadSnapshot(ctx context.Context, id backend.SnapshotID, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	repo, err := b.openOrClone()
	if err != nil {
		return err
	}
	commit, err := repo.CommitObject(plumbing.NewHash(string(id)))
	if err != nil {
		return backend.ErrNoSnapshot
	}
	tree, err := commit.Tree()
	if err != nil {
		return err
	}
	return tree.Files().ForEach(func(f *object.File) error {
		target := filepath.Join(destDir, filepath.FromSlash(f.Name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		reader, err := f.Reader()
		if err != nil {
			return err
		}
		defer reader.Close()
		buf := make([]byte, f.Size)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return err
		}
		return os.WriteFile(target, buf, os.FileMode(mode))
	})
}

// ListSnapshots returns the most recent commits reachable from HEAD.
func (b *backendImpl) ListSnapshots(ctx context.Context, limit int) ([]backend.SnapshotInfo, error) {
	repo, err := b.openOrClone()
	if err != nil {
		return nil, err
	}
	head, err := repo.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) || errors.Is(err, transport.ErrEmptyRemoteRepository) {
			return nil, nil
		}
		return nil, err
	}
	cIter, err := repo.Log(&gogit.LogOptions{From: head.Hash()})
	if err != nil {
		return nil, err
	}
	defer cIter.Close()
	var out []backend.SnapshotInfo
	for {
		c, err := cIter.Next()
		if err != nil {
			break
		}
		out = append(out, backend.SnapshotInfo{
			ID:        backend.SnapshotID(c.Hash.String()),
			CreatedAt: c.Author.When,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// openOrClone returns an open repository, cloning shallow if needed.
// If the remote repository is empty (newly created, no commits), the
// returned *gogit.Repository is local-only — HEAD operations will
// surface errors that callers (CurrentSnapshot, ListSnapshots) catch
// and report as backend.ErrNoSnapshot.
func (b *backendImpl) openOrClone() (*gogit.Repository, error) {
	if b.repo == "" {
		return nil, errors.New("github backend not configured")
	}
	if b.workDir == "" {
		dir, err := os.MkdirTemp("", "omp-sync-github-*")
		if err != nil {
			return nil, err
		}
		b.workDir = dir
	}
	if _, err := os.Stat(filepath.Join(b.workDir, ".git")); err == nil {
		return gogit.PlainOpen(b.workDir)
	}
	if err := os.MkdirAll(b.workDir, 0o755); err != nil {
		return nil, err
	}
	repo, err := gogit.PlainClone(b.workDir, false, &gogit.CloneOptions{
		URL:        b.repo,
		RemoteName: "origin",
		Auth:       b.auth(),
	})
	if err == nil {
		return repo, nil
	}
	// If the remote is empty (no commits), init a local repo and
	// register the remote — calls will see no HEAD and surface
	// ErrNoSnapshot themselves.
	if errors.Is(err, transport.ErrEmptyRemoteRepository) {
		repo, ierr := gogit.PlainInit(b.workDir, false)
		if ierr != nil {
			return nil, fmt.Errorf("init after empty remote: %w", ierr)
		}
		if _, rerr := repo.CreateRemote(&config.RemoteConfig{
			Name: "origin",
			URLs: []string{b.repo},
		}); rerr != nil {
			return nil, fmt.Errorf("create remote: %w", rerr)
		}
		return repo, nil
	}
	return nil, err
}

func (b *backendImpl) push(repo *gogit.Repository) error {
	remote, err := repo.Remote("origin")
	if err != nil {
		return err
	}
	// Derive the local branch from HEAD so the refspec matches the
	// branch the commit landed on (PlainInit defaults to master unless
	// the config overrides it; the config branch may differ).
	localBranch := "refs/heads/" + b.branch
	head, err := repo.Head()
	if err == nil && head.Name().IsBranch() {
		localBranch = head.Name().String()
	}
	refSpec := config.RefSpec(fmt.Sprintf("+%s:refs/heads/%s", localBranch, b.branch))
	if err := remote.Push(&gogit.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{refSpec},
		Auth:       b.auth(),
		Force:      true,
	}); err != nil {
		return fmt.Errorf("push: %w", err)
	}
	return nil
}

func (b *backendImpl) auth() *http.BasicAuth {
	password, _ := credentials.Lookup(b.credName)
	return &http.BasicAuth{
		Username: "x-access-token",
		Password: password,
	}
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		data, err := os.ReadFile(path) //nolint:gosec // controlled path
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

var _ backend.Backend = (*backendImpl)(nil)
