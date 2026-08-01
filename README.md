# omp-sync

Centralize and synchronize your [oh-my-pi (omp)](https://github.com/can1357/oh-my-pi) settings
across devices. `omp-sync` is a CLI-first (with optional TUI) tool that pushes your
selected `omp` config to a backend of your choice — WebDAV, GitHub, or any local folder
synced via Dropbox, iCloud, or Syncthing — and pulls it everywhere else.

## Features

- **Multi-backend** — WebDAV, GitHub, or a local folder. Pluggable.
- **Selective sync** — include/exclude globs to share only what you want.
- **Atomic** — every command is all-or-nothing; concurrent modifications are detected
  and refused.
- **Audit log** — every mutation is recorded in append-only JSONL.
- **TUI** — bubbletea-based snapshot browser and diff view.
- **Static binary** — single static Go binary; cross-platform via GoReleaser.

## Install

```bash
go install github.com/donrami/omp-sync/cmd/omp-sync@latest
```

Or build from source:

```bash
git clone https://github.com/donrami/omp-sync
cd omp-sync
make build
./bin/omp-sync --help
```

Pre-built binaries (Linux, macOS, Windows on amd64 and arm64) are published with every
release.

## Usage

The shortest happy path:

```bash
# 1. Configure a backend
cat > ~/.config/omp-sync/config.toml <<EOF
backend = "local"
omp_dir = "$HOME/.config/omp"

[local]
path = "$HOME/Dropbox/omp-sync"
EOF

# 2. From machine A: push your existing config
omp-sync push --yes

# 3. From machine B (or after a fresh install):
omp-sync init --yes
omp-sync pull --yes
```

## Commands

| Command     | Purpose                                  |
|-------------|------------------------------------------|
| `init`      | Bootstrap on a fresh machine             |
| `push`      | Publish local changes to the remote      |
| `pull`      | Apply remote changes locally             |
| `status`    | Show drift between local and remote      |
| `diff`      | Show textual diffs                       |
| `config`    | Manage the configuration file            |
| `tui`       | Launch the interactive TUI               |
| `version`   | Print the version                        |

### Command reference

#### `omp-sync init [--yes]`

Populate the local omp config directory from the current snapshot on the configured
backend. Use this on a fresh machine, or after wiping the local config.

Exits with status 2 if the backend has no snapshot.

#### `omp-sync push [--dry-run] [--yes] [--include=<patterns>] [--exclude=<patterns>] [--message=<text>]`

Publish the local omp config to the backend. Refuses if the remote has changed since
the last sync (FR-009). Use `--dry-run` to preview; `--yes` skips the confirmation.

The `--include` and `--exclude` flags override the config patterns for this
invocation; comma-separated glob patterns matching `bmatcuk/doublestar/v4` syntax.

#### `omp-sync pull [--dry-run] [--yes] [--force] [--include=<patterns>] [--exclude=<patterns>]`

Download the current snapshot from the backend and apply it to the local omp config.
By default, refuses to overwrite locally-modified files (FR-009 + US3/AC3); pass
`--force` to override after backing up.

#### `omp-sync status [--json]`

Compare the local config to the current remote snapshot and report per-file drift
(local only, remote only, modified). Exits 0 regardless of drift.

#### `omp-sync diff [--path=<relpath>] [--json]`

Print line-level diffs between local files and the remote snapshot. `--path` restricts
the output to a single file.

#### `omp-sync config`

Subcommands:

- `config list`  — print the resolved configuration as TOML.
- `config get <key>`  — print a single configuration value (`backend` or `omp_dir`).
- `config set credential <name>`  — store a credential in the OS keyring.
- `config schema`  — print the JSON schema for `config.toml`.

## Configuration

`~/.config/omp-sync/config.toml` (overridable via `--config` or `OMP_SYNC_CONFIG`).

A WebDAV config:

```toml
backend = "webdav"
omp_dir = "$HOME/.config/omp"

include = ["agents/**", "snippets/**"]
exclude = ["snippets/secret.md"]

[webdav]
url = "https://dav.example.com/alice"
username = "alice"
credential = "webdav_password"
path = "/omp-sync"
```

A GitHub config:

```toml
backend = "github"
omp_dir = "$HOME/.config/omp"

[github]
repo = "https://github.com/alice/omp-config.git"
branch = "main"
credential = "github_pat"
```

### Credentials

Credentials are never stored in the config file. Two lookup paths are tried, in order:

1. Environment variable `OMP_SYNC_<NAME>` (uppercased, dashes replaced with underscores).
2. OS keyring (macOS Keychain, Linux Secret Service, Windows Credential Manager) via
   `zalando/go-keyring`.

Store a credential with:

```bash
omp-sync config set credential webdav_password
# ... type the password on stdin or use --value
```

## Output

- Data on **stdout**; errors on **stderr**.
- `--json` emits machine-readable JSON on stdout for `status`, `diff`, `push`,
  `pull`, and `config list`.
- `--no-color` disables ANSI styling when piping.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success. |
| 1 | Generic user error (bad config, conflicting state, etc.). |
| 2 | Backend error (unreachable, auth failed, conflict). |
| 3 | Internal error (a bug). |
| 64 | Usage error. |

## Audit log

Every mutating command writes one JSONL record to
`$XDG_STATE_HOME/omp-sync/audit.log` (default `~/.local/state/omp-sync/audit.log`).
Each line contains: timestamp, operation, backend, snapshot id before/after,
files added/modified/deleted/unchanged/ignored, exit code, duration, error.

The log is append-only; rotation is delegated to `logrotate` or equivalent.

## Backends

Three built-in backends ship with the binary:

- **`local`** — filesystem directory tree under the configured path. Atomic promotion
  via flock on `.lock`; current snapshot tracked in `current.id`.
- **`webdav`** — HTTP Basic + a remote directory, treated as a dumb file store.
- **`github`** — git repository over HTTPS with a personal access token; each snapshot
  is a single commit on the configured branch.

Plugin contract: third-party
backends are discovered from `$XDG_CONFIG_HOME/omp-sync/plugins/*.so` (Unix) or from
executables on `$PATH` named `omp-sync-backend-<name>`.

## Internals

- Language: Go 1.25+
- TUI stack: `charm.land/bubbletea/v2`, `lipgloss/v2`, `bubbles/v2`
- WebDAV: `github.com/studio-b12/gowebdav` v0.13.0
- Git: `github.com/go-git/go-git/v5` v5.19.2
- Glob: `github.com/bmatcuk/doublestar/v4` v4.10.0
- CLI: `github.com/spf13/cobra` v1.10.2
- Keyring: `github.com/zalando/go-keyring` v0.2.8

## License

MIT.
