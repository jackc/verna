# Verna: Systemd-native Blue/Green Deployment Tool

## Context

Verna is a lightweight deployment tool for compiled web applications (Go, Rust, etc.) on Ubuntu servers. It fills a gap between full PaaS platforms (Dokku, Piku) and manual deployment scripts. The key insight: when deploying compiled binaries, you don't need buildpacks, containers, or server-side compilation — just a thin orchestration layer over systemd, Caddy, and SSH.

**Core philosophy:** A release switcher for systemd services, not a mini-Heroku.

**Module:** `github.com/jackc/verna`

---

## Architecture

### Two components

1. **Local CLI** (`verna`) — written in Go, runs on dev machine or CI
2. **No server-side daemon** — CLI executes all server operations over SSH using `golang.org/x/crypto/ssh`

### Core concepts

| Concept | Description |
|---------|-------------|
| **Server** | A remote host with its own config and state (`/var/lib/verna/verna.json`) |
| **Application** | A named service with domains, ports, exec path, health check config |
| **Release** | An immutable timestamped artifact directory |
| **Slot** | Blue or green — each with a fixed port and systemd unit |
| **Active slot** | The one currently receiving traffic via Caddy |

---

## Configuration

### No local config file

There is no local configuration file. Server connection is specified via CLI flags:

```sh
verna --ssh-host prod-1 server init
verna --ssh-host prod-1 app --app myapp init --domain myapp.example.com --exec-path bin/myapp
verna --ssh-host prod-1 app --app myapp deploy myapp.tar.gz
```

Global flags: `--ssh-host` (required), `--ssh-user` (default: root), `--ssh-port` (default: 22), `--ssh-key-file` (optional, also tries SSH agent). All support `VERNA_` env var equivalents (e.g. `VERNA_SSH_HOST`).

App flag: `--app` (on the `app` command, inherited by subcommands), also settable via `VERNA_APP`.

### `/var/lib/verna/verna.json` (server config and state)

All app configuration and deployment state lives on the server in a single file, managed atomically. Configuration is set via CLI commands (e.g. `verna app init`, `verna app env set`) and stored here.

```json
{
  "next_port": 18005,
  "apps": {
    "myapp": {
      "domains": ["myapp.example.com"],
      "exec_path": "bin/myapp",
      "public_path": "public",
      "health_check_path": "/health",
      "health_check_timeout": 15,
      "release_retention": 5,
      "user": "myapp",
      "group": "myapp",
      "env": {
        "DATABASE_URL": "postgres://..."
      },
      "active_slot": "blue",
      "slots": {
        "blue": {
          "port": 18001,
          "release": "20260307T120102Z-a1b2c3d4e5f6",
          "deployed_at": "2026-03-07T12:01:15Z"
        },
        "green": {
          "port": 18002,
          "release": "20260306T221500Z-f6e5d4c3b2a1",
          "deployed_at": "2026-03-06T22:15:12Z"
        }
      }
    }
  }
}
```

Port pairs are auto-assigned from a starting port (18001) during `app init`. The `next_port` field in the root tracks the next available port.

App-level settings:
- `exec_path` — relative path to the executable within the artifact directory (e.g. `bin/myapp`)
- `public_path` — relative path to the public assets directory within the artifact directory (optional)

---

## Server-side directory layout

```
/var/lib/verna/
  verna.json               # server-wide state
  apps/
    myapp/
      releases/
        20260307T120102Z-a1b2c3d4e5f6/
          bin/myapp
          public/...
          templates/...    # extra files deployed alongside
        20260306T221500Z-f6e5d4c3b2a1/
          ...
      slots/
        blue -> /var/lib/verna/apps/myapp/releases/20260307T120102Z-1f2e3d4
        green -> /var/lib/verna/apps/myapp/releases/20260306T221500Z-aabbcc
      shared/              # mutable data (uploads, tmp, etc.)
```

- **Releases are immutable** — makes rollback trivial
- **Slots are symlinks** to release directories
- **Shared directory** for mutable data that persists across deploys
- **Working directory** is the release root (the slot symlink target)

---

## Systemd integration

Templated unit file per app: `myapp@.service`

```ini
[Unit]
Description=myapp (%i)
After=network.target

[Service]
Type=simple
User=myapp
Group=myapp
WorkingDirectory=/var/lib/verna/apps/myapp/slots/%i
EnvironmentFile=-/var/lib/verna/apps/myapp/slots/%i/env/runtime.env
ExecStart=/var/lib/verna/apps/myapp/slots/%i/bin/myapp
Environment=VERNA_APP=myapp
Environment=VERNA_SLOT=%i
Restart=always
RestartSec=2
StandardOutput=journal
StandardError=journal
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/verna/apps/myapp/shared

[Install]
WantedBy=multi-user.target
```

- **Fixed ports per slot:** blue always on e.g. `127.0.0.1:18001`, green on `127.0.0.1:18002`
- Instance names: `myapp@blue.service`, `myapp@green.service`
- Logs naturally separated via journald: `journalctl -u myapp@blue`
- PORT is set via `runtime.env` (per-slot `EnvironmentFile`)
- `ExecStart` path is derived from the app's `exec_path` setting

---

## Caddy integration

Use **Caddy admin API** (`localhost:2019`) for atomic upstream switching — no config file rewriting or reload needed.

- On deploy: PATCH the upstream for the app's route to point at the new slot's port
- Caddy config is rendered from verna's server state, not the other way around
- Initial setup generates Caddy config for the app's domain(s)

---

## Artifact format

The deploy command takes a pre-built `.tar.gz` file as its argument. The build system produces the tarball; Verna uploads and unpacks it.

```
bin/myapp                  # executable (path configured via --exec-path)
public/...                 # static assets (optional, path configured via --public-path)
templates/...              # extra files are included as-is
config.toml                # any other files
```

Release IDs are generated from deploy timestamp + SHA-256 hash prefix of the tarball (e.g. `20260307T120102Z-a1b2c3d4e5f6`).

---

## Deploy state machine

```
1. Load server state from verna.json
2. Determine active slot, target the inactive slot
3. Upload tarball via SSH
4. Unpack to new release directory (immutable)
4a. Validate executable exists and is executable
5. Update inactive slot symlink -> new release
6. Write slot env file (PORT, app env vars)
7. Start/restart inactive slot's systemd unit
8. Health check: GET http://127.0.0.1:<slot-port>/health
9. On success: PATCH Caddy upstream to new slot's port
10. Stop old slot's systemd unit
11. Update verna.json (active slot, release metadata)
12. Prune old releases beyond retention limit
```

**Fail-safe:** If anything fails before step 9, the old slot stays live untouched.

---

## App contract

Each application must:
- Read `PORT` env var for the listen port
- Serve `GET /health` returning `200` when ready (path configurable)
- Handle `SIGTERM` gracefully

---

## CLI commands (MVP)

Built with **cobra** (`github.com/spf13/cobra`).

| Command | Description |
|---------|-------------|
| `verna server init` | Initialize verna on the server (create `/var/lib/verna/`, `verna.json`) |
| `verna app init` | Set up an app on the server (dirs, systemd unit, Caddy route, user) |
| `verna app set` | Update app settings (domains, exec-path, public-path, health check, retention, exec args) |
| `verna app env list` | List all environment variables |
| `verna app env get <key>` | Get the value of an environment variable |
| `verna app env set KEY=VAL` | Set env vars, restart active slot |
| `verna app env unset KEY` | Remove env vars, restart active slot |
| `verna app deploy <tarball>` | Upload pre-built tarball, activate on inactive slot |
| `verna app delete` | Stop services, remove systemd unit, Caddy route, app directory, state entry |
| `verna status <app>` | Show active slot, current release, health |
| `verna rollback <app>` | Switch traffic back to the previous slot |
| `verna logs <app>` | Tail journald logs (`--slot`, `-f`, `-n`) |
| `verna prune <app>` | Remove old releases beyond retention count |

Global flags: `--ssh-host` (required), `--ssh-user` (default: root), `--ssh-port` (default: 22), `--ssh-key-file` (optional). All support `VERNA_` env vars.

App flag: `--app` on the `app` command (inherited by all subcommands), also `VERNA_APP`.

---

## Project structure

```
verna/
  cmd/
    verna/
      main.go              # cobra root command setup
      server.go            # verna server init, server install-caddy
      app.go               # verna app init, app delete
      app_set.go           # verna app set
      env.go               # verna app env {list,get,set,unset}
      deploy.go            # verna app deploy
      status.go            # verna status
      rollback.go          # verna rollback
      logs.go              # verna logs
      prune.go             # verna prune
  internal/
    ssh/
      ssh.go               # SSH client using golang.org/x/crypto/ssh
    deploy/
      deploy.go            # Deploy state machine orchestration
      artifact.go          # Release ID generation (timestamp + tarball hash)
    caddy/
      caddy.go             # Caddy admin API interactions (via SSH tunnel)
    systemd/
      systemd.go           # systemd unit generation and management
    server/
      state.go             # Server-wide verna.json state management
      env.go               # Runtime env file generation
    health/
      health.go            # Health check logic
  go.mod
  go.sum
```

---

## Implementation plan

### Phase 1: Project scaffolding ✓
- Initialize Go module: `go mod init github.com/jackc/verna`
- Add cobra dependency: `github.com/spf13/cobra`
- Set up root command with global connection flags (`--host`, `--user`, `--port`, `--key-file`)

### Phase 2: SSH transport ✓
- SSH client using `golang.org/x/crypto/ssh` (supports agent auth, key files)
- File upload by streaming into a remote command (e.g. `cat > /path/to/file`)
- Helper functions: run remote command, upload file, stream tarball

### Phase 3: Server-wide state management ✓
- Define `ServerState` struct with per-app config and state
- Read/write `/var/lib/verna/verna.json` over SSH
- Atomic writes (write to temp file, rename)

### Phase 4: Server init (`verna server init`) ✓
- Create `/var/lib/verna/` directory structure on server
- Create empty `verna.json`
- Verify prerequisites (systemd, Caddy running, curl available)

### Phase 5: App init (`verna app init`) ✓
- Create app directory structure (`releases/`, `slots/`, `shared/`)
- Create app system user/group (uses app name directly; reserved system names are rejected)
- Generate and install systemd template unit
- Configure initial Caddy route (via admin API, with server bootstrap)
- Register app in `verna.json` with allocated ports
- Flags: `--domain` (required, repeatable), `--exec-path` (required), `--public-path`, `--health-check-path`, `--health-check-timeout`, `--release-retention`, `--exec-arg`

### Phase 5.5: Environment variable management (`verna app env`) ✓
- `app env list <app>` — list all env vars (sorted `KEY=value`)
- `app env get <app> <key>` — print single value (scriptable)
- `app env set <app> KEY=VAL [...]` — set vars, write runtime.env, restart active slot
- `app env unset <app> KEY [...]` — remove vars, write runtime.env, restart active slot
- Reusable `FormatRuntimeEnv` / `WriteRuntimeEnv` in `internal/server/env.go` for deploy command to reuse
- PORT is reserved and always managed by verna

### Phase 6: Artifact packaging ✓ (simplified)
- Deploy accepts a pre-built `.tar.gz` file — the build system produces the tarball, not Verna
- `internal/deploy/artifact.go` — `GenerateReleaseID()` produces `YYYYMMDDTHHMMSSZ-<sha256prefix12>` from timestamp + tarball content hash
- No manifest, no `BuildArtifact()`, no client-side validation — tarball is uploaded as-is
- Server-side validation: after unpack, verifies executable exists and is executable
- Caddy static asset strategy: `/assets/*` gets immutable cache, other static files get `no-cache` with `pass_thru` to reverse proxy

### Phase 7: Deploy command ✓
- `internal/deploy/deploy.go` — 13-step deploy state machine: upload, unpack, validate executable, chown, symlink, env, systemd restart, health check, Caddy switch, stop old slot, update state, prune
- `internal/health/health.go` — `WaitForHealthy()` polls health endpoint over SSH with 500ms interval
- `internal/caddy/caddy.go` — `UpdateAppRoute()` replaces route via PUT `/id/verna_<app>`; `buildRouteWithPublicJSON()` creates subroute with `/assets/*` immutable cache, `file_server` with `pass_thru` + `no-cache`, and `reverse_proxy` fallback
- Fail-safe: if health check fails, failed slot is stopped, old slot stays live, state unchanged
- Auto-prunes old releases beyond retention after successful deploy
- `selectReleasesToPrune()` extracted as testable pure function

### Phase 7.5: App management commands ✓
- `app set` — update domains, exec-path, public-path, health check, retention, exec args; regenerates systemd unit and restarts if needed
- `app delete` — stop services, remove systemd unit, Caddy route, app directory, state entry (with confirmation prompt)

### Phase 8: Supporting commands
- `status` — read verna.json, check systemd unit status, health check
- `rollback` — restart previous slot, health check, switch Caddy
- `logs` — proxy `journalctl` over SSH
- `prune` — remove old releases beyond retention

---

## Testing

### Unit tests (no server needed)

Pure logic that can be tested in isolation:

- **Server state serialization** — round-trip `Marshal`/`Parse`, verify empty state, verify per-app state updates don't clobber other apps, verify defaults (health check path `/health`, retention 5)
- **Release ID generation** — verify timestamp format, verify content hash suffix, verify same file produces same hash, verify different files produce different hashes
- **Systemd unit generation** — render a unit for a known config, verify `User=`, `Group=`, `ExecStart=` (uses configured `exec_path`), `WorkingDirectory=`, `ReadWritePaths=` contain correct paths; verify `EnvironmentFile=-` uses the `-` prefix (optional file)
- **Slot env generation** — verify `PORT=<port>` appears, verify additional env vars from config are included
- **Release naming** — verify timestamp format `YYYYMMDDTHHMMSSZ`, verify SHA-256 hash suffix is 12 hex chars

### Integration tests (require a server)

The full deploy lifecycle needs a real Ubuntu environment with systemd, Caddy, and SSH. Options for providing one:

1. **Vagrant** (recommended for local dev) — a `Vagrantfile` that provisions an Ubuntu VM with Caddy installed and root SSH configured for key-based auth
2. **Throwaway cloud instance** — same provisioning via a script, suitable for CI
3. **Docker with systemd** — use a systemd-capable base image (e.g. `jrei/systemd-ubuntu`), run Caddy inside, SSH to localhost on a mapped port; functional but fights Docker's design

#### Test app

A minimal Go HTTP server (~20 lines) that satisfies the application contract:

- Reads `PORT` env var
- Serves `GET /health` returning 200
- Handles `SIGTERM` with graceful shutdown
- Serves `GET /` returning the value of `VERNA_SLOT` env var (useful for verifying which slot is live)

#### End-to-end test sequence

Run against the test server with the test app:

1. `verna server init` — verify `/var/lib/verna/` created, `verna.json` exists and is valid
2. `verna app init testapp --exec-path bin/testapp --domain test.example.com` — verify directory structure, systemd unit, app user, state
3. `verna app deploy dist/` — verify release unpacked, slot symlink updated, systemd active, health check passes, Caddy routes traffic, state updated
4. `verna status testapp` — verify output shows active slot, release, service status, health 200
5. `verna app deploy dist/` (second deploy) — verify it targets the *other* slot, verify Caddy switches, old slot stopped, state updated
6. `verna rollback testapp` — verify traffic returns to previous slot, health check passes, state updated
7. `verna logs testapp` — verify journald output streams for the active slot
8. `verna prune testapp` — deploy several times to exceed retention, verify old releases removed but active releases preserved

#### Failure scenarios

- **Health check failure** — deploy an app that doesn't serve `/health`; verify the deploy aborts, the new slot is stopped, the old slot remains live and unchanged, state is not updated
- **Bad artifact** — deploy with a corrupt or missing tarball; verify the deploy fails before any slot switch
- **Missing app config** — run `verna app deploy nonexistent`; verify a clear error message
- **First deploy** (no prior state) — verify state is updated, first slot is picked, Caddy is configured
- **Server not initialized** — run `verna app init` before `verna server init`; verify a clear error message

### What not to unit test

- SSH transport internals — mocking `golang.org/x/crypto/ssh` adds ceremony without value; covered by integration tests
- Caddy API calls — these are curl-over-SSH commands, better validated end-to-end
- The deploy state machine as a unit — it's orchestration glue; its value is in the real interaction between systemd, Caddy, and the filesystem

---

## Design decisions

- **SSH-only, no server daemon** — simplest model
- **`golang.org/x/crypto/ssh` for transport** — pure Go, supports SSH agent auth and key files; file transfers by streaming into remote commands (no SFTP dependency needed since we only upload a single tarball)
- **cobra for CLI** — subcommand support (`server init`, `app init`), built-in help, flag parsing
- **Server-wide `verna.json`** — single source of truth for all app deployment state on the server; avoids scattered per-app state files
- **Separate `server init` and `app init`** — server setup is a one-time operation; app setup can be repeated
- **Auto-assigned port pairs** — ports allocated from a starting range during `app init`, tracked in `verna.json`
- **No local config file** — connection info via CLI flags; all app config lives server-side in `verna.json`
- **Pre-built tarballs** — deploy takes a `.tar.gz` produced by the build system; Verna only uploads, unpacks, and validates
- **Exec path as app config** — `--exec-path` and `--public-path` are app-level settings stored in `verna.json`, not deploy-time flags
- **Caddy admin API** — atomic upstream switching without config file management
- **Tarballs, not rsync** — atomic uploads, easy retention
- **Immutable releases** — rollback is just a symlink swap + restart
