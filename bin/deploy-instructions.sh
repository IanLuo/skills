#!/usr/bin/env bash
#
# deploy-instructions.sh — deploy the global agent instruction file (global/AGENTS.md)
# into every coding agent's global-instruction location, via symlinks.
#
# One canonical file, symlinked everywhere: edit global/AGENTS.md, run this, and
# every agent picks it up (symlinks are live). This is the "grill-as-a-file"
# layer — always-on instructions every agent loads in every project.
#
# Usage:
#   ./deploy-instructions.sh               # link to all supported agents
#   ./deploy-instructions.sh --list        # show agents + their global instruction files
#   ./deploy-instructions.sh --agent NAME  # link only to the named agent(s)
#   ./deploy-instructions.sh --dry-run     # show what would happen, change nothing
#   ./deploy-instructions.sh --doctor      # verify existing links point at the repo source
#   ./deploy-instructions.sh --force       # overwrite an existing REAL file (backs it up to .bak)
#
# A link is:  <agent-global-file> -> <repo>/global/AGENTS.md
# If <agent-global-file> is a real file (not our symlink), deploy refuses unless
# --force, which renames it to <file>.bak first — never clobber user data silently.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd -P)"
SOURCE="$REPO_ROOT/global/AGENTS.md"

# Agent → global instruction file. Only agents with a documented global
# markdown-instruction file are wired. cursor/zed/aider/cline use settings or
# .mdc/other formats — not yet wired (documented below for future work).
INSTRUCTIONS=(
  "claude|${HOME}/.claude/CLAUDE.md"
  "agents|${HOME}/.agents/AGENTS.md"
  "opencode|${HOME}/.config/opencode/AGENTS.md"
  "codex|${HOME}/.codex/AGENTS.md"
  "gemini|${HOME}/.gemini/GEMINI.md"
)
# Not yet wired (no clean global markdown-instruction file):
#   pi (reads AGENTS.md from cwd) · cursor (.cursor/rules *.mdc) · hermes ·
#   windsurf (~/.codeium) · zed (settings) · aider (.aider.conf.yml) · cline (settings)

DRY_RUN=0
FORCE=0
DOCTOR=0
LIST_ONLY=0
WANT=()

log()  { printf '[deploy-instructions] %s\n' "$*"; }
warn() { printf '[deploy-instructions] ⚠ %s\n' "$*" >&2; }
err()  { printf '[deploy-instructions] ✖ %s\n' "$*" >&2; }

usage() {
  sed -n '2,/^set -euo/p' "$0" | sed 's/^# \{0,1\}//' | sed 's/^#//'
  exit 0
}

contains() {
  local needle="$1"; shift
  for x in "$@"; do [ "$x" = "$needle" ] && return 0; done
  return 1
}

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run)  DRY_RUN=1; shift ;;
    --force)    FORCE=1; shift ;;
    --doctor)   DOCTOR=1; shift ;;
    --list)     LIST_ONLY=1; shift ;;
    --agent)    shift; WANT+=("$1"); shift ;;
    -h|--help)  usage ;;
    *) err "unknown option: $1"; usage ;;
  esac
done

[ -f "$SOURCE" ] || { err "missing $SOURCE — create global/AGENTS.md first"; exit 1; }

if [ "$LIST_ONLY" -eq 1 ]; then
  printf 'Global instruction file: %s\n\n' "$SOURCE"
  printf 'Agents (%d):\n' "${#INSTRUCTIONS[@]}"
  for entry in "${INSTRUCTIONS[@]}"; do
    printf '  • %-9s %s\n' "${entry%%|*}" "${entry#*|}"
  done
  printf '\nNot yet wired: pi, cursor, hermes, windsurf, zed, aider, cline\n'
  exit 0
fi

DOCTOR_FAIL=0
for entry in "${INSTRUCTIONS[@]}"; do
  name="${entry%%|*}"
  path="${entry#*|}"
  top="${path%/*}"

  if [ ${#WANT[@]} -gt 0 ] && ! contains "$name" "${WANT[@]}"; then continue; fi

  # Only act if the agent's top-level config dir exists (mirrors deploy-skills.sh).
  if [ ! -d "$top" ]; then
    log "[$name] not present ($top missing) — skipping"
    continue
  fi

  if [ "$DOCTOR" -eq 1 ]; then
    if [ -L "$path" ] && [ "$(readlink "$path")" = "$SOURCE" ]; then
      log "✓ [$name] $path → $SOURCE"
    else
      warn "✗ [$name] $path is NOT linked to $SOURCE"
      DOCTOR_FAIL=1
    fi
    continue
  fi

  if [ -L "$path" ]; then
    if [ "$(readlink "$path")" = "$SOURCE" ]; then
      log "[$name] already linked"
    else
      err "[$name] $path is a symlink elsewhere (to $(readlink "$path")) — remove it or point it at $SOURCE"
    fi
    continue
  fi

  if [ -e "$path" ]; then
    if [ "$FORCE" -ne 1 ]; then
      warn "[$name] $path is a real file — refusing (use --force to back it up to .bak and replace)"
      continue
    fi
    log "[$name] backing up real file → ${path}.bak"
    if [ "$DRY_RUN" -ne 1 ]; then mv "$path" "${path}.bak"; fi
  fi

  if [ "$DRY_RUN" -eq 1 ]; then
    log "[$name] would link $path → $SOURCE"
  else
    ln -s "$SOURCE" "$path"
    log "[$name] linked → $SOURCE"
  fi
done

if [ "$DOCTOR" -eq 1 ] && [ "$DOCTOR_FAIL" -eq 1 ]; then
  err "doctor: ${DOCTOR_FAIL} link(s) not pointing at $SOURCE"
  exit 1
fi

exit 0
