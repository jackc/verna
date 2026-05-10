#!/bin/bash
set -e

# Persistent volumes are mounted root-owned and empty on first attach.
sudo chown vscode:vscode /persist/local /persist/shared

# Pre-create every subdirectory referenced by containerEnv (plus
# devcontainer-downloads / .scratch / devcontainer for the hooks below).
mkdir -p /persist/shared/{claude,go,go-build,mise/{data,cache},atuin/{config,data},devcontainer-downloads,.scratch,devcontainer}

# Per-developer / per-environment customization hook. Was previously sourced
# from a sibling host directory at ../shared/devcontainer/install; now lives on
# the shared volume so it survives rebuilds and is shared across worktrees.
if [ -x /persist/shared/devcontainer/install ]; then
  /persist/shared/devcontainer/install
fi

# Symlink the shared scratch dir into the workspace root for convenience. Was
# previously ../shared/.scratch; now /persist/shared/.scratch.
if [ ! -e .scratch ] && [ ! -L .scratch ]; then
  ln -s /persist/shared/.scratch
fi
