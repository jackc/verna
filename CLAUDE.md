# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Verna is a systemd-native blue/green deployment tool for compiled web applications (Go, Rust, etc.) on Ubuntu servers. It is a local CLI (no server-side daemon) that orchestrates deployments over SSH using systemd, Caddy, and symlink-based slot switching.

**Module:** `github.com/jackc/verna`

## Build and Test Commands

```sh
go build -o verna ./cmd/verna       # build the CLI
go test ./...                       # run all tests
go test ./internal/server/          # run tests for a single package
go test -run TestName ./internal/server/  # run a single test
```

## Architecture

### No local config file
Server connection is specified via CLI flags (`--ssh-host`, `--ssh-user`, `--ssh-port`, `--ssh-key-file`) or corresponding `VERNA_` environment variables. The target app is specified via `--app` flag or `VERNA_APP` env var on the `app` command. All app configuration and deployment state lives server-side in `/var/lib/verna/verna.json`, modified via CLI commands.

### SSH transport
All server operations use `golang.org/x/crypto/ssh`. File uploads stream tarballs into remote commands (no SFTP). State file reads/writes use `cat` and write-to-temp-then-rename over SSH.

### Blue/green slots
Each app has two slots (blue/green) with auto-assigned ports. Slots are symlinks under `/var/lib/verna/apps/<app>/slots/` pointing to immutable release directories under `releases/`. Rollback is a symlink swap + service restart.

### Artifact tarball
The deploy command takes a pre-built `.tar.gz` file as its argument. The build system (goreleaser, Makefile, CI script, etc.) is responsible for producing the tarball. Verna uploads it to the server and unpacks it. The executable path is configured as an app-level setting (`--exec-path` on `app init`/`app config set`) and is a relative path within the tarball. After unpacking, Verna validates that the executable exists and is executable on the server. Caddy routing is controlled via `--caddy-handle-template`, a Go `text/template` producing the JSON `handle` array with `{{.Dial}}` and `{{.SlotDir}}` variables.

### Deploy state machine
The deploy targets the inactive slot: upload tarball, unpack to release dir, update symlink, write env file, restart systemd unit, health check, then atomically switch Caddy's upstream via its admin API (localhost:2019). If anything fails before the Caddy switch, the old slot stays live.

### CLI
Built with `github.com/spf13/cobra`. Commands: `server init`, `app init`, `app config {list,set}`, `app env {list,get,set,unset}`, `deploy`, `status`, `rollback`, `logs`, `prune`.

## Key Design Constraints

- Releases are immutable — never modify a release directory after creation
- Server state (`verna.json`) is a single file for all app config and deployment state, written atomically
- Caddy upstream switching uses the admin API, not config file rewriting
- Verna connects as root over SSH (no sudo needed)
- Applications must read `PORT` env var, serve a health endpoint, and handle `SIGTERM`

## Keep Up To Date

Especially in this initial construction phase, update CLAUDE.md, PLAN.md, and README.md as appropriate. Be sure to make a note of compled phases in PLAN.md.
