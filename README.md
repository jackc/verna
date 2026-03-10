# Verna

A systemd-native blue/green deployment tool for compiled web applications.

Verna deploys pre-compiled binaries (Go, Rust, C, etc.) to Ubuntu servers using systemd, Caddy, and SSH. No Docker, no buildpacks, no server-side compilation.

## How it works

Verna manages two **slots** per application (blue and green), each backed by a systemd service instance listening on a fixed local port. At any time, only one slot is live — Caddy reverse-proxies traffic to it. Deploying targets the inactive slot, and Caddy switches over only after a health check passes. The previous slot remains available for instant rollback.

```
                ┌──────────┐
  traffic ──▶   │  Caddy   │
                └────┬─────┘
                     │
              ┌──────┴──────┐
              ▼             ▼
        ┌──────────┐  ┌──────────┐
        │  blue    │  │  green   │
        │ :18001   │  │ :18002   │
        └──────────┘  └──────────┘
         (active)      (standby)
```

### Deploy sequence

1. Determine the inactive slot
2. Package and upload the artifact (tarball with manifest)
3. Unpack into an immutable release directory
4. Update the slot's symlink to the new release
5. Restart the slot's systemd unit
6. Health check the new instance
7. Switch Caddy's upstream to the new slot
8. Stop the old slot

If the health check fails, the old slot stays live — nothing changes.

## Installation

```sh
go install github.com/jackc/verna/cmd/verna@latest
```

Or build from source:

```sh
git clone https://github.com/jackc/verna.git
cd verna
go build -o verna ./cmd/verna
```

## Configuration

There is no local configuration file. Server connection is specified via CLI flags on every command:

```sh
verna --host myserver.example.com <command>
```

Global flags:
- `--host` — server hostname (required)
- `--user` — SSH user (default: root)
- `--port` — SSH port (default: 22)
- `--key-file` — path to SSH private key (optional, also tries SSH agent)

All app configuration (domains, env vars, health check settings, ports) lives on the server in `/var/lib/verna/verna.json`. Configuration is set via CLI commands like `verna app init` and `verna app env set`.

### Server state (`verna.json`)

The server state file tracks all app configuration and deployment state:

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
        "DATABASE_URL": "postgres://localhost/myapp"
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

Port pairs are auto-assigned during `app init`. Environment variables are managed via `verna app env set` and written to each slot's `env/runtime.env` automatically.

## Application contract

Your application must:

- **Read the `PORT` environment variable** and listen on it
- **Serve a health check endpoint** (`GET /health` by default) that returns HTTP 200 when ready
- **Handle `SIGTERM`** for graceful shutdown

## Usage

### Install Caddy

```sh
verna --host myserver server install-caddy
```

Downloads the latest Caddy release from GitHub, installs it to `/usr/local/bin`, creates a `caddy` system user, sets up a systemd unit, and verifies the admin API is responding on `localhost:2019`. Caddy is configured to run with `--resume`, so configuration pushed via the admin API is automatically persisted and restored on restart.

### Initialize the server

```sh
verna --host myserver server init
```

Creates `/var/lib/verna/` and an empty `verna.json` on the server. This is a one-time setup step.

### Initialize an app

```sh
verna --host myserver app init myapp --domain myapp.example.com
```

Creates the directory structure, system user, systemd template unit, and Caddy route on the server. Registers the app in `verna.json` with auto-assigned ports.

### Manage environment variables

```sh
# Set one or more variables (restarts the active slot)
verna --host myserver app env set myapp DATABASE_URL=postgres://localhost/myapp SECRET_KEY=hunter2

# List all variables
verna --host myserver app env list myapp

# Get a single variable (scriptable)
verna --host myserver app env get myapp DATABASE_URL

# Remove a variable (restarts the active slot)
verna --host myserver app env unset myapp SECRET_KEY
```

Environment variables are stored in `verna.json` and written to each slot's `env/runtime.env` file. The `PORT` variable is reserved and managed automatically by verna.

### Deploy

```sh
# Build your binary, then deploy it
GOOS=linux GOARCH=amd64 go build -o bin/myapp .
verna --host myserver deploy myapp --binary bin/myapp --commit $(git rev-parse --short HEAD)
```

Or with a pre-built artifact:

```sh
verna --host myserver deploy myapp --artifact myapp_release.tar.gz
```

### Check status

```sh
verna --host myserver status myapp
```

```
App:         myapp
Active Slot: blue

Slot blue (active) (port 18001):
  Release:  20260307T120102Z-1f2e3d4
  Deployed: 2026-03-07T12:01:15Z
  Commit:   1f2e3d4
  Service:  active
  Health:   200

Slot green (port 18002):
  Release:  20260306T221500Z-aabbcc
  Deployed: 2026-03-06T22:15:12Z
  Commit:   aabbcc
  Service:  inactive
  Health:   unreachable
```

### Rollback

```sh
verna --host myserver rollback myapp
```

Restarts the previous slot, health checks it, then switches Caddy back.

### View logs

```sh
verna --host myserver logs myapp              # active slot
verna --host myserver logs myapp --slot blue  # specific slot
verna --host myserver logs myapp -f           # follow
verna --host myserver logs myapp -n 100       # last 100 lines
```

### Prune old releases

```sh
verna --host myserver prune myapp
```

Removes old release directories beyond the retention count (default 5), preserving any release currently referenced by either slot.

## Server layout

```
/var/lib/verna/
  verna.json                         # server-wide deployment state
  apps/
    myapp/
      releases/
        20260307T120102Z-1f2e3d4/    # immutable release
          manifest.json
          bin/myapp
          public/
          env/runtime.env
        20260306T221500Z-aabbcc/
          ...
      slots/
        blue -> ../releases/20260307T120102Z-1f2e3d4
        green -> ../releases/20260306T221500Z-aabbcc
      shared/                        # persistent mutable data
```

Releases are immutable. Slots are symlinks. Rollback is a symlink swap.

## Server prerequisites

- **Ubuntu** with systemd and journald
- **Caddy** running with the admin API enabled (default on `localhost:2019`)
- **Root SSH access** from your local machine to the server
- **curl** on the server (used for health checks and Caddy API calls)

## Artifact format

Artifacts are `.tar.gz` files containing:

```
manifest.json       # release metadata
bin/myapp           # compiled binary
public/             # static assets (optional)
```

The `manifest.json` records the app name, release ID, git commit, build time, OS, and architecture:

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

## CI example

```sh
# In your CI pipeline:
GOOS=linux GOARCH=amd64 go build -o bin/myapp .
verna --host myserver deploy myapp --binary bin/myapp --commit $CI_COMMIT_SHA
```

## Design decisions

- **SSH-only, no server daemon** — all operations run over SSH from the local CLI
- **No local config file** — connection info via CLI flags; all app config lives server-side
- **`golang.org/x/crypto/ssh`** — pure Go SSH client; supports SSH agent and key file auth
- **cobra for CLI** — subcommand support (`server init`, `app init`), built-in help and flag parsing
- **Server-wide `verna.json`** — single source of truth for all app config and deployment state
- **Auto-assigned port pairs** — ports allocated from a starting range during `app init`
- **Caddy admin API** — atomic upstream switching without config file rewriting
- **Tarball artifacts** — atomic upload, verifiable, easy retention and rollback
- **Immutable releases** — rollback is a symlink swap + service restart
