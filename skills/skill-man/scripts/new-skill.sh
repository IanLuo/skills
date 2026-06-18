#!/usr/bin/env bash
#
# new-skill.sh — scaffold a valid skill directory in this repo.
#
# Usage:
#   new-skill.sh <name> [--resources scripts,references,assets] [--example]
#
# <name> must be lowercase-hyphen-case (a-z0-9 and hyphens; no leading/trailing/
# double hyphens; ≤64 chars). The script normalizes obvious cases ("My Skill" →
# "my-skill") but rejects anything that still doesn't match after normalization.
# Non-ASCII input (e.g. "Café") is rejected with a clear error rather than
# silently mangled.
#
# Creates skills/<name>/ with a SKILL.md whose frontmatter uses ONLY the fields the
# spec allows (name + description placeholder). Optionally creates resource subdirs.
# Designed to pass validate.py immediately, so the author just fills in the body.

set -euo pipefail

# ── Resolve repo root (shared with other skill-man scripts) ──────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=lib/repo_root.sh
source "$SCRIPT_DIR/lib/repo_root.sh"
SKILLS_DIR="$REPO_ROOT/skills"

# ── Helpers ───────────────────────────────────────────────────────────────
err() { printf 'new-skill: ✖ %s\n' "$*" >&2; }
log() { printf 'new-skill: %s\n' "$*"; }

# Print usage to stdout and exit 0 (only for explicit -h/--help).
usage() {
  cat <<'USAGE'
new-skill.sh <name> [--resources scripts,references,assets] [--example]

  <name>        lowercase-hyphen-case (a-z0-9-); no leading/trailing/double
                hyphens; ≤64 chars. Normalized from e.g. "My Skill".
  --resources   comma-separated subdirs to create (scripts,references,assets)
  --example     also drop a sample references/example.md
USAGE
  exit 0
}

# Print usage to stderr and exit 2 (for argument errors).
usage_err() {
  cat <<'USAGE' >&2
new-skill.sh <name> [--resources scripts,references,assets] [--example]
USAGE
  exit 2
}

# Reject non-ASCII input so accented names aren't byte-mangled into hyphens.
is_ascii() {
  printf '%s' "$1" | LC_ALL=C grep -q '^[[:print:][:space:]]*$'
}

# Normalize a human name to lowercase-hyphen-case (ASCII-only input expected).
normalize() {
  local n="$1"
  n="$(printf '%s' "$n" | tr '[:upper:]' '[:lower:]')"
  n="$(printf '%s' "$n" | tr -c 'a-z0-9-' '-' | tr -s '-')"
  n="${n#-}"; n="${n%-}"
  printf '%s' "$n"
}

valid_name() {  # 0 if matches ^[a-z0-9-]+$ and length rules
  local n="$1"
  [ -n "$n" ] || return 1
  [[ "$n" =~ ^[a-z0-9-]+$ ]] || return 1
  [[ "$n" != -* && "$n" != *- && "$n" != *--* ]] || return 1
  [ "${#n}" -le 64 ] || return 1
  return 0
}

# ── Args ──────────────────────────────────────────────────────────────────
NAME=""
RESOURCES=""
EXAMPLE=0
while [ $# -gt 0 ]; do
  case "$1" in
    --resources) shift; RESOURCES="${1:-}"; shift ;;
    --example)   EXAMPLE=1; shift ;;
    -h|--help)   usage ;;
    -*)          err "Unknown option: $1"; usage_err ;;
    *)           [ -z "$NAME" ] && NAME="$1" || { err "Unexpected argument: $1"; usage_err; }; shift ;;
  esac
done

[ -n "$NAME" ] || { err "Skill name required."; usage_err; }

# Reject non-ASCII before normalizing (avoids silent byte-mangling).
if ! is_ascii "$NAME"; then
  err "Skill name must be ASCII (got non-ASCII characters in '$NAME')."
  err "Use lowercase letters, digits, and hyphens only."
  exit 1
fi

# Normalize + validate.
NAME="$(normalize "$NAME")"
if ! valid_name "$NAME"; then
  err "Invalid skill name even after normalization: '$NAME'"
  err "Must match ^[a-z0-9-]+\$ — lowercase letters, digits, hyphens only;"
  err "  no leading/trailing/double hyphens; ≤64 chars."
  exit 1
fi

# ── Pre-validate the resource list BEFORE creating anything ───────────────
# (so a bad --resources type doesn't leave a partial, rerun-blocking dir).
RES_ARR=()
if [ -n "$RESOURCES" ]; then
  IFS=',' read -ra RES <<<"$RESOURCES"
  for r in "${RES[@]}"; do
    r="${r// /}"
    [ -n "$r" ] || continue
    case "$r" in
      scripts|references|assets) RES_ARR+=("$r") ;;
      *) err "Unknown resource type '$r' (expected: scripts, references, assets)"; exit 1 ;;
    esac
  done
fi

# ── Create the skill directory ────────────────────────────────────────────
SKILL_DIR="$SKILLS_DIR/$NAME"
if [ -e "$SKILL_DIR" ]; then
  err "A skill already exists at $SKILL_DIR"
  exit 1
fi
mkdir -p "$SKILL_DIR"
for r in "${RES_ARR[@]:-}"; do
  [ -n "$r" ] && mkdir -p "$SKILL_DIR/$r"
done

# ── Write a valid SKILL.md ────────────────────────────────────────────────
cat >"$SKILL_DIR/SKILL.md" <<EOF
---
name: $NAME
description: TODO — replace with a trigger-oriented description. State both WHAT this skill does and WHEN to use it (the description is the primary trigger). Keep ≤1024 chars and use no angle brackets.
metadata:
  audience: personal
  domain: general
---

# $NAME

## What this skill is for

One or two sentences on the job this skill does and when to reach for it.

## Working rules

- Imperative, unambiguous steps the assistant should follow.
- Add only what the model does not already know — prefer concrete examples over prose.

## Interaction style

- How it should talk to the user: lead with X, confirm before Y.
EOF

if [ "$EXAMPLE" -eq 1 ] && [ -d "$SKILL_DIR/references" ]; then
  cat >"$SKILL_DIR/references/example.md" <<'EOF'
# Example reference

Reference files load on demand, not at trigger time. Link each one from SKILL.md with
a one-line "read this when…". Keep references one level deep from SKILL.md.
EOF
fi

# ── Done ──────────────────────────────────────────────────────────────────
log "Created skill at: $SKILL_DIR"
log ""
log "Next steps:"
log "  1. Edit $SKILL_DIR/SKILL.md — fill in description (the trigger!) and body."
log "  2. Add resources under scripts/ references/ assets/ if you created them."
log "  3. Validate:  python3 $REPO_ROOT/skills/skill-man/scripts/validate.py"
log "  4. Deploy:    bash $REPO_ROOT/bin/deploy.sh --skill $NAME"
