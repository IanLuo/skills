#!/usr/bin/env bash
#
# build.sh — compile the cred-run Rust binary and place it in the skill's
# deployable scripts/ folder.
#
# The compiled binary is gitignored; this is the only build step. Requires a
# Rust toolchain — use the repo flake:  nix develop -c bash <this-file>
#
# Usage:
#   nix develop -c bash src/credentials/build.sh

set -euo pipefail

# src/credentials → repo root is two levels up.
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
SRC_DIR="$REPO_ROOT/src/credentials"
CARGO_TOML="$SRC_DIR/Cargo.toml"
OUT="$REPO_ROOT/skills/credentials/scripts/cred-run"

cargo build --release --manifest-path "$CARGO_TOML"
cp "$SRC_DIR/target/release/cred-run" "$OUT"
chmod +x "$OUT"
printf 'built %s\n' "$OUT"
