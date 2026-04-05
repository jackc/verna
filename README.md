# Verna

A blue/green deployment tool for compiled web applications managed by systemd and listening behind a Caddy reverse-proxy.

Verna deploys pre-compiled binaries (Go, Rust, C, etc.) to Ubuntu servers using systemd, Caddy, and SSH. No Docker, no buildpacks, no server-side compilation.

Verna was designed for my deployment preferences. If your needs are the same, verna may work for you. Otherwise, verna will likely not be useful to you.

## How it works

Verna manages two **slots** per application (blue and green), each backed by a systemd service instance listening on a fixed local port. At any time, only one slot is live — Caddy reverse-proxies traffic to it. Deploying targets the inactive slot, and Caddy switches over only after a health check passes. The previous slot remains available for instant rollback.

### Deploy sequence

1. Determine the inactive slot
2. Upload the tarball artifact
3. Unpack into an immutable release directory
4. Validate the executable exists and is executable
5. Update the slot's symlink to the new release
6. Restart the slot's systemd unit
7. Health check the new instance
8. Switch Caddy's upstream to the new slot
9. Stop the old slot
10. Prune old releases beyond retention limit

If the health check fails, the old slot stays live — nothing changes.

## Installation

Download from Github Releases or install with `go install`:

```sh
go install github.com/jackc/verna/cmd/verna@latest
```

## Configuration

There is no local configuration file. Server connection is specified via CLI flags on every command:

```sh
verna --ssh-host myserver.example.com <command>
```

Global flags:
- `--ssh-host` — server hostname (required)
- `--ssh-user` — SSH user (default: root)
- `--ssh-port` — SSH port (default: 22)
- `--ssh-key-file` — path to SSH private key (optional, also tries SSH agent)

All global flags support `VERNA_` environment variable equivalents (e.g. `VERNA_SSH_HOST`). The `--app` flag supports `VERNA_APP`.

All app configuration (domains, env vars, health check settings, ports) lives on the server in `/var/lib/verna/verna.json`. Configuration is set via CLI commands like `verna app init` and `verna app config set`.

### Server state

The server state file `/var/lib/verna/verna.json` tracks all app configuration and deployment state.

Port pairs are auto-assigned during `app init`. Environment variables are managed via `verna app env set` and written to each slot's `env/runtime.env` automatically.

## Application contract

Your application must:

- **Read the `PORT` environment variable** and listen on it
- **Serve a health check endpoint** (`GET /health` by default) that returns HTTP 200 when ready
- **Handle `SIGTERM`** for graceful shutdown

## Usage

### Install Caddy

Caddy is required. You may install it via your package manager or manually or you may have verna install it:

```sh
verna --ssh-host myserver server install-caddy
```

Downloads the latest Caddy release from GitHub, installs it to `/usr/local/bin`, creates a `caddy` system user, sets up a systemd unit, and verifies the admin API is responding on `localhost:2019`. Caddy is configured to run with `--resume`, so configuration pushed via the admin API is automatically persisted and restored on restart.

### Initialize the server

```sh
verna --ssh-host myserver server init
```

Creates `/var/lib/verna/` and an empty `verna.json` on the server. This is a one-time setup step.

### Check server prerequisites

```sh
verna --ssh-host myserver server doctor
```

Verifies that the server has all required prerequisites: systemd, Caddy running with admin API, and curl.

### Initialize an app

```sh
verna --ssh-host myserver app --app myapp init --domain myapp.example.com --exec-path bin/myapp
```

Creates the directory structure, system user, systemd template unit, and Caddy route on the server. Registers the app in `verna.json` with auto-assigned ports.

Options:
- `--exec-path` (required) — relative path to the executable within the artifact directory (e.g. `bin/myapp`)
- `--caddy-handle-template-path` — path within the artifact where the Caddy handle template is stored (default: `deploy/caddy-handle-template.json`)
- `--domain` (required, repeatable) — domain name(s) for the app
- `--health-check-path` — health check endpoint path (default: `/health`)
- `--exec-arg` — arguments appended to the executable in ExecStart (repeatable)

#### Caddy handle template

The Caddy handle template is a Go `text/template` producing a JSON array of Caddy handlers, stored as a file in your project and included in the deploy artifact. During `app init`, if the template file doesn't exist at the configured path, you'll be prompted to create one from a preset.

Template variables: `{{.Dial}}` (e.g. `127.0.0.1:18001`) and `{{.SlotDir}}` (e.g. `/var/lib/verna/apps/myapp/slots/blue`).

**Available presets:**

| Preset | Description |
|--------|-------------|
| `proxy` | Reverse proxy only (default). For API-only apps with no static file serving. |
| `static-proxy` | Try static files from `public/` first, fall back to reverse proxy. Includes precompressed file support (gzip, zstd, brotli). |
| `spa-proxy` | SPA with reverse-proxied API. Serves `/assets/*` with immutable cache headers, proxies `/api/*` to the backend, and falls back to `index.html` for client-side routing. For SvelteKit (adapter-static) or similar SPA frameworks. |

Each deploy reads the template from the artifact, so different versions of your app can have different Caddy routing. Rollback restores the previous deployment's routing configuration.

### Manage app configuration

```sh
# List all settings
verna --ssh-host myserver app --app myapp config list

# Get a single setting
verna --ssh-host myserver app --app myapp config get exec-path

# Update settings (regenerates systemd unit and Caddy route as needed)
verna --ssh-host myserver app --app myapp config set --domain newdomain.example.com --health-check-timeout 30
```

### Manage environment variables

```sh
# Set one or more variables (restarts the active slot)
verna --ssh-host myserver app --app myapp env set DATABASE_URL=postgres://localhost/myapp SECRET_KEY=hunter2

# List all variables
verna --ssh-host myserver app --app myapp env list

# Get a single variable (scriptable)
verna --ssh-host myserver app --app myapp env get DATABASE_URL

# Remove a variable (restarts the active slot)
verna --ssh-host myserver app --app myapp env unset SECRET_KEY
```

Environment variables are stored in `verna.json` and written to each slot's `env/runtime.env` file. The `PORT` variable is reserved and managed automatically by verna.

### Deploy

```sh
# Build your binary and create a tarball, then deploy it
mkdir -p dist/bin
GOOS=linux GOARCH=amd64 go build -o dist/bin/myapp .
tar czf myapp.tar.gz -C dist .
verna --ssh-host myserver app --app myapp deploy myapp.tar.gz
```

The deploy command takes a `.tar.gz` file as its argument. The build system (goreleaser, Makefile, CI script, etc.) is responsible for producing the tarball. Verna uploads it to the server, unpacks it, and validates that the executable exists. The binary path is configured as an app-level setting (see `app init` above). The Caddy handle template is read from the artifact at the configured path (see `app init`), so each deploy can have its own routing configuration.

Old releases beyond the retention limit (default 5) are automatically pruned after each successful deploy.

### Check status

```sh
verna --ssh-host myserver app --app myapp status
```

```
App:        myapp
Domains:    myapp.example.com

Active:     blue (port 18001)
Release:    20260307T120102Z-a1b2c3d4e5f6
Deployed:   2026-03-07T12:01:15Z
Service:    active
Health:     200

Inactive:   green (port 18002)
Release:    20260306T221500Z-f6e5d4c3b2a1
Deployed:   2026-03-06T22:15:12Z
Service:    inactive
```

### Rollback

```sh
verna --ssh-host myserver app --app myapp rollback
```

Restarts the previous slot, health checks it, then switches Caddy back.

### View logs

```sh
verna --ssh-host myserver app --app myapp logs              # both slots (interleaved by timestamp)
verna --ssh-host myserver app --app myapp logs --slot blue   # specific slot
verna --ssh-host myserver app --app myapp logs -f            # follow
verna --ssh-host myserver app --app myapp logs -n 100        # last 100 lines
```

Extra arguments after `--` are passed through to journalctl:

```sh
verna --ssh-host myserver app --app myapp logs -- --since "1 hour ago"
verna --ssh-host myserver app --app myapp logs -- --grep "panic" --priority err
```

### Delete an app

```sh
verna --ssh-host myserver app --app myapp delete
```

Stops services, removes the systemd unit, Caddy route, app directory, and state entry. Prompts for confirmation (use `--yes` to skip).

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
- **curl** on the server (used for health checks in `status`)

## Artifact format

Artifacts are `.tar.gz` files produced by your build system. There is no specific format. The app `--exec-path` controls what executable is run. The Caddy handle template file (default at `deploy/caddy-handle-template.json` within the artifact) controls routing behavior.

Release IDs are generated automatically from the deploy timestamp and a SHA-256 hash prefix of the tarball contents (e.g. `20260307T120102Z-a1b2c3d4e5f6`).

## CI example

```sh
# In your CI pipeline:
mkdir -p dist/bin
GOOS=linux GOARCH=amd64 go build -o dist/bin/myapp .
cp -r templates dist/templates  # include extra files as needed
tar czf myapp.tar.gz -C dist .
verna --ssh-host myserver app --app myapp deploy myapp.tar.gz
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

## Testing

Unit tests run without any special setup:

```sh
go test ./...
```

### Integration tests

Integration tests connect to a real server over SSH and exercise the full verna workflow: server init, Caddy install, app lifecycle, deploy, rollback, and cleanup. The test server must be a **fresh Ubuntu 24.04** machine — the tests install Caddy, create systemd units, and manage system users.

#### Test server setup

1. Create a marker file on the server so the tests know it's safe to use. Without this file, tests will abort to avoid accidentally damaging another host:

    ```sh
    # On the test server (as root):
    touch /verna-integration-test-target
    ```

2. Ensure root SSH access from the machine running the tests. If you don't already have a key:

    ```sh
    ssh-keygen -t ed25519 -f ~/.ssh/verna-test -N ""
    ssh-copy-id -i ~/.ssh/verna-test root@<test-server-ip>
    ```

#### Running the integration tests

If your SSH agent already has a key for the test server:

```sh
VERNA_TEST_SSH_HOST=<ip> go test -v -timeout 300s ./integration_test/
```

Or with an explicit key file:

```sh
VERNA_TEST_SSH_HOST=<ip> VERNA_TEST_SSH_KEY_FILE=~/.ssh/verna-test go test -v -timeout 300s ./integration_test/
```

Environment variables:

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `VERNA_TEST_SSH_HOST` | Yes | — | Test server IP or hostname |
| `VERNA_TEST_SSH_PORT` | No | `22` | SSH port |
| `VERNA_TEST_SSH_KEY_FILE` | No | — | Path to SSH private key (falls back to SSH agent) |
| `VERNA_TEST_GOARCH` | No | `amd64` | Architecture for cross-compiling the test app binary |

The tests clean up all verna files when done and return the server to its original state.

#### SSH from a devcontainer

If you're developing inside a devcontainer that cannot directly reach the test server, you can use `socat` on the host to relay the SSH connection. For example, if the test server is at `192.168.1.100` and your devcontainer maps port 2222:

```sh
# On the host machine:
socat TCP-LISTEN:2222,fork,reuseaddr TCP:192.168.1.100:22
```

Then from inside the devcontainer:

```sh
VERNA_TEST_SSH_HOST=host.docker.internal VERNA_TEST_SSH_PORT=2222 go test -v -timeout 300s ./integration_test/
```

## Contributing

verna is open source, but closed contribution. It's designed for my personal needs and preferences. You may submit issues, but unless your needs and preferences align exactly with mine it is unlikely they will be accepted. Please do not submit PR's. In the unlikely event that a suggested change is accepted, then I will implement it myself.

## License

MIT
