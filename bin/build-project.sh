#!/usr/bin/env bash
#
# build-project.sh — build a project under src/ with nix and install its binary
# into the skill's deployable scripts/ folder.
#
# Nothing here is per-project, on purpose. Every project is a flake output
# (`packages.<project>`); the destination is `skills/<project>/scripts/` because
# the skill folder is named after the project; and the binary is whatever that
# output installs into `bin/`. Adding a project means adding a flake output —
# there is no second list to update.
#
# Note that `nix build` alone is not enough: a derivation writes into the store,
# so the copy into skills/<project>/scripts/ is this script's job. Use this, not
# `nix build`, or the skill keeps its previous binary. `--list` prints the
# projects it would handle; `--doctor` (bin/deploy-skills.sh) flags an artifact
# that is missing or left behind by the current source.
#
# The installed binaries are derived artifacts and gitignored — never edit them.
#
# Usage:
#   ./bin/build-project.sh                # every project the flake exposes
#   ./bin/build-project.sh credentials    # one project
#   ./bin/build-project.sh --list         # names only, one per line

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$REPO_ROOT"

# The flake owns the project list. Enumerated rather than restated so adding a
# project means adding one output and nothing else.
list_projects() {
  local system
  system="$(nix eval --raw --impure --expr builtins.currentSystem)"
  nix eval --json --apply builtins.attrNames ".#packages.$system" |
    python3 -c 'import json,sys; print("\n".join(sorted(json.load(sys.stdin))))'
}

if [ "${1:-}" = "--list" ]; then
  list_projects
  exit 0
fi

if [ $# -gt 0 ]; then
  # Word-splitting is intended: project names cannot contain spaces.
  projects="$*"
else
  projects="$(list_projects)"
fi

for project in $projects; do
  dest="skills/$project/scripts"
  if [ ! -d "$dest" ]; then
    printf 'build-project: no project %q — %s does not exist\n' "$project" "$dest" >&2
    exit 1
  fi

  out="$(nix build ".#$project" --no-link --print-out-paths)"

  installed=0
  for bin in "$out"/bin/*; do
    [ -e "$bin" ] || continue
    install -m 755 "$bin" "$dest/$(basename "$bin")"
    printf 'built %s/%s\n' "$dest" "$(basename "$bin")"
    installed=1
  done
  if [ "$installed" -eq 0 ]; then
    printf 'build-project: %s installed nothing into %s/bin\n' "$project" "$out" >&2
    exit 1
  fi
done
