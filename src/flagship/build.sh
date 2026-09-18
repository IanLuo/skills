#!/usr/bin/env bash
#
# build.sh — compile the fs Go binary and place it in the skill's
# deployable scripts/ folder.
#
# The compiled binary is gitignored; this is the only build step. Requires a
# Go toolchain — use the repo flake:  nix develop -c bash <this-file>
#
# Usage:
#   nix develop -c bash src/flagship/build.sh

set -euo pipefail

# src/flagship → repo root is two levels up.
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
SRC_DIR="$REPO_ROOT/src/flagship"
OUT="$REPO_ROOT/skills/flagship/scripts/fs"

cd "$SRC_DIR"
go build -o "$OUT" ./cmd/
chmod +x "$OUT"
printf 'built %s\n' "$OUT"
