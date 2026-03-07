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
| **Server** | A remote host with its own config and state (`/var/verna/verna.json`) |
| **Application** | A named service with domains, ports, health check config |
| **Release** | An immutable timestamped artifact directory |
| **Slot** | Blue or green — each with a fixed port and systemd unit |
| **Active slot** | The one currently receiving traffic via Caddy |

---

## Configuration

### No local config file

There is no local configuration file. Server connection is specified via CLI flags:

```sh
verna --host prod-1 server init
verna --host prod-1 app init myapp --domain myapp.example.com
verna --host prod-1 deploy myapp --binary bin/myapp
```

Global flags: `--host` (required), `--user` (default: root), `--port` (default: 22), `--key-file` (optional, also tries SSH agent).

### `/var/verna/verna.json` (server config and state)

All app configuration and deployment state lives on the server in a single file, managed atomically. Configuration is set via CLI commands (e.g. `verna app init`, `verna app env set`) and stored here.

```json
{
  "next_port": 18005,
  "apps": {
    "myapp": {
      "domains": ["myapp.example.com"],
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
          "release": "20260307T120102Z-1f2e3d4",
          "deployed_at": "2026-03-07T12:01:15Z",
          "commit": "1f2e3d4"
        },
        "green": {
          "port": 18002,
          "release": "20260306T221500Z-aabbcc",
          "deployed_at": "2026-03-06T22:15:12Z",
          "commit": "aabbcc"
        }
      }
    }
  }
}
```

Port pairs are auto-assigned from a starting port (18001) during `app init`. The `next_port` field in the root tracks the next available port.

---

## Server-side directory layout

```
/var/verna/
  verna.json               # server-wide state
  apps/
    myapp/
      releases/
        20260307T120102Z-1f2e3d4/
          bin/myapp
          public/...
        20260306T221500Z-aabbcc/
          ...
      slots/
        blue -> /var/verna/apps/myapp/releases/20260307T120102Z-1f2e3d4
        green -> /var/verna/apps/myapp/releases/20260306T221500Z-aabbcc
      shared/              # mutable data (uploads, tmp, etc.)
```

- **Releases are immutable** — makes rollback trivial
- **Slots are symlinks** to release directories
- **Shared directory** for mutable data that persists across deploys

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
WorkingDirectory=/var/verna/apps/myapp/slots/%i
EnvironmentFile=-/var/verna/apps/myapp/slots/%i/env/runtime.env
ExecStart=/var/verna/apps/myapp/slots/%i/bin/myapp
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
ReadWritePaths=/var/verna/apps/myapp/shared

[Install]
WantedBy=multi-user.target
```

- **Fixed ports per slot:** blue always on e.g. `127.0.0.1:18001`, green on `127.0.0.1:18002`
- Instance names: `myapp@blue.service`, `myapp@green.service`
- Logs naturally separated via journald: `journalctl -u myapp@blue`
- PORT is set via `runtime.env` (per-slot `EnvironmentFile`)

---

## Caddy integration

Use **Caddy admin API** (`localhost:2019`) for atomic upstream switching — no config file rewriting or reload needed.

- On deploy: PATCH the upstream for the app's route to point at the new slot's port
- Caddy config is rendered from verna's server state, not the other way around
- Initial setup generates Caddy config for the app's domain(s)

---

## Artifact format

**Tarball with manifest** (not raw rsync):

```
myapp_20260307T120102Z_1f2e3d4.tar.gz
  manifest.json
  bin/myapp
  public/...
```

`manifest.json`:
```json
{
  "app": "myapp",
  "release": "20260307T120102Z-1f2e3d4",
  "commit": "1f2e3d4",
  "build_time": "2026-03-07T12:01:02Z",
  "os": "linux",
  "arch": "amd64"
}
```

Benefits: atomic upload, verification, retention, future signing/checksums.

---

## Deploy state machine

```
1. Load server state from verna.json
2. Determine active slot, target the inactive slot
3. Upload artifact via SSH (stream tarball)
4. Unpack to new release directory (immutable)
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
| `verna server init` | Initialize verna on the server (create `/var/verna/`, `verna.json`) |
| `verna app init <app>` | Set up an app on the server (dirs, systemd unit, Caddy route, user) |
| `verna deploy <app>` | Package artifact, upload, activate on inactive slot |
| `verna status <app>` | Show active slot, current release, health |
| `verna rollback <app>` | Switch traffic back to the previous slot |
| `verna logs <app>` | Tail journald logs (`--slot`, `-f`, `-n`) |
| `verna prune <app>` | Remove old releases beyond retention count |

Global flags: `--host` (required), `--user` (default: root), `--port` (default: 22), `--key-file` (optional).

---

## Project structure

```
verna/
  cmd/
    verna/
      main.go              # cobra root command setup
      server.go            # verna server init
      app.go               # verna app init
      deploy.go            # verna deploy
      status.go            # verna status
      rollback.go          # verna rollback
      logs.go              # verna logs
      prune.go             # verna prune
  internal/
    ssh/
      ssh.go               # SSH client using golang.org/x/crypto/ssh
    deploy/
      deploy.go            # Deploy state machine orchestration
      artifact.go          # Tarball creation and upload
    caddy/
      caddy.go             # Caddy admin API interactions (via SSH tunnel)
    systemd/
      systemd.go           # systemd unit generation and management
    server/
      state.go             # Server-wide verna.json state management
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
- Read/write `/var/verna/verna.json` over SSH
- Atomic writes (write to temp file, rename)

### Phase 4: Server init (`verna server init`) ✓
- Create `/var/verna/` directory structure on server
- Create empty `verna.json`
- Verify prerequisites (systemd, Caddy running, curl available)

### Phase 5: App init (`verna app init`)
- Create app directory structure (`releases/`, `slots/`, `shared/`)
- Create app system user/group
- Generate and install systemd template unit
- Write initial slot env files
- Configure initial Caddy route
- Register app in `verna.json`

### Phase 6: Artifact packaging
- Create tarball from binary + assets
- Generate `manifest.json` with commit, timestamp, arch

### Phase 7: Deploy command
- Implement the full deploy state machine
- Fail-safe: abort without switching if health check fails

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
- **Artifact creation** — create a tarball from a temp binary + extra files, read it back, verify `manifest.json` contents (app name, release format, commit truncation to 7 chars), verify binary is at `bin/<name>` inside the archive, verify file permissions are preserved
- **Systemd unit generation** — render a unit for a known config, verify `User=`, `Group=`, `ExecStart=`, `WorkingDirectory=`, `ReadWritePaths=` contain correct paths; verify `EnvironmentFile=-` uses the `-` prefix (optional file); verify env vars from config appear in the output
- **Slot env generation** — verify `PORT=<port>` appears, verify additional env vars from config are included
- **Release naming** — verify timestamp format `YYYYMMDDTHHMMSSZ`, verify commit suffix is appended and truncated, verify no suffix when commit is empty

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

Run against the test server with the test app binary:

1. `verna server init` — verify `/var/verna/` created, `verna.json` exists and is valid
2. `verna app init testapp` — verify directory structure exists on server (`releases/`, `slots/`, `shared/`), verify systemd unit installed at `/etc/systemd/system/testapp@.service`, verify app user created, verify app registered in `verna.json`
3. `verna deploy testapp` — verify release unpacked to `releases/<timestamp>/`, verify slot symlink updated, verify systemd unit is active, verify health check passes, verify Caddy routes traffic to the correct port, verify `verna.json` shows correct active slot and release
4. `verna status testapp` — verify output shows active slot, release, service status, health 200
5. `verna deploy testapp` (second deploy) — verify it targets the *other* slot, verify Caddy switches upstream, verify old slot is stopped, verify `verna.json` updated
6. `verna rollback testapp` — verify traffic returns to previous slot, verify the rolled-back-to slot's health check passes before switching, verify `verna.json` updated
7. `verna logs testapp` — verify journald output streams for the active slot
8. `verna prune testapp` — deploy several times to exceed retention, verify old releases are removed but active releases are preserved

#### Failure scenarios

- **Health check failure** — deploy an app that doesn't serve `/health`; verify the deploy aborts, the new slot is stopped, the old slot remains live and unchanged, `verna.json` is not updated
- **Bad artifact** — deploy with a corrupt or missing tarball; verify the deploy fails before any slot switch
- **Missing app config** — run `verna deploy nonexistent`; verify a clear error message
- **First deploy** (no prior state) — verify `verna.json` is updated, first slot is picked, Caddy is configured
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
- **Caddy admin API** — atomic upstream switching without config file management
- **Tarballs, not rsync** — atomic uploads, verifiable, easier retention
- **Immutable releases** — rollback is just a symlink swap + restart
