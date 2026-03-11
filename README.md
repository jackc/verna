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
2. Upload the tarball artifact
3. Unpack into an immutable release directory
3a. Validate the executable exists and is executable
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
      "exec_path": "bin/myapp",
      "public_path": "public",
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
verna --host myserver app --app myapp init --domain myapp.example.com --exec-path bin/myapp
```

Creates the directory structure, system user, systemd template unit, and Caddy route on the server. Registers the app in `verna.json` with auto-assigned ports.

Options:
- `--exec-path` (required) — relative path to the executable within the artifact directory (e.g. `bin/myapp`)
- `--public-path` — relative path to the public assets directory within the artifact directory (e.g. `public`)
- `--domain` (required, repeatable) — domain name(s) for the app
- `--health-check-path` — health check endpoint path (default: `/health`)
- `--exec-arg` — arguments appended to the executable in ExecStart (repeatable)

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
# Build your binary and create a tarball, then deploy it
mkdir -p dist/bin
GOOS=linux GOARCH=amd64 go build -o dist/bin/myapp .
tar czf myapp.tar.gz -C dist .
verna --host myserver app --app myapp deploy myapp.tar.gz
```

The deploy command takes a `.tar.gz` file as its argument. The build system (goreleaser, Makefile, CI script, etc.) is responsible for producing the tarball. Verna uploads it to the server, unpacks it, and validates that the executable exists. The binary path and public directory are configured as app-level settings (see `app init` above).

### Check status

```sh
verna --host myserver status myapp
```

```
App:         myapp
Active Slot: blue

Slot blue (active) (port 18001):
  Release:  20260307T120102Z-a1b2c3d4e5f6
  Deployed: 2026-03-07T12:01:15Z
  Service:  active
  Health:   200

Slot green (port 18002):
  Release:  20260306T221500Z-f6e5d4c3b2a1
  Deployed: 2026-03-06T22:15:12Z
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
        20260307T120102Z-a1b2c3d4e5f6/  # immutable release
          bin/myapp
          public/
          env/runtime.env
        20260306T221500Z-f6e5d4c3b2a1/
          ...
      slots/
        blue -> ../releases/20260307T120102Z-a1b2c3d4e5f6
        green -> ../releases/20260306T221500Z-f6e5d4c3b2a1
      shared/                        # persistent mutable data
```

Releases are immutable. Slots are symlinks. Rollback is a symlink swap.

## Server prerequisites

- **Ubuntu** with systemd and journald
- **Caddy** running with the admin API enabled (default on `localhost:2019`)
- **Root SSH access** from your local machine to the server
- **curl** on the server (used for health checks and Caddy API calls)

## Artifact format

Artifacts are `.tar.gz` files produced by your build system. Verna does not create them — it only uploads and unpacks them. The tarball should contain your application files at the top level:

```
bin/myapp           # executable (path configured via --exec-path)
public/             # static assets (optional, path configured via --public-path)
templates/          # extra files are included as-is
config.toml         # any other files
```

Release IDs are generated automatically from the deploy timestamp and a SHA-256 hash prefix of the tarball contents (e.g. `20260307T120102Z-a1b2c3d4e5f6`).

## CI example

```sh
# In your CI pipeline:
mkdir -p dist/bin
GOOS=linux GOARCH=amd64 go build -o dist/bin/myapp .
cp -r templates dist/templates  # include extra files as needed
tar czf myapp.tar.gz -C dist .
verna --host myserver app --app myapp deploy myapp.tar.gz
```

## Design decisions

- **SSH-only, no server daemon** — all operations run over SSH from the local CLI
- **No local config file** — connection info via CLI flags; all app config lives server-side
- **`golang.org/x/crypto/ssh`** — pure Go SSH client; supports SSH agent and key file auth
- **cobra for CLI** — subcommand support (`server init`, `app init`), built-in help and flag parsing
- **Server-wide `verna.json`** — single source of truth for all app config and deployment state
- **Auto-assigned port pairs** — ports allocated from a starting range during `app init`
- **Caddy admin API** — atomic upstream switching without config file rewriting
- **Pre-built tarballs** — build system produces `.tar.gz`; Verna uploads, unpacks, and validates
- **Immutable releases** — rollback is a symlink swap + service restart
