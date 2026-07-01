#!/usr/bin/env bash
#
# deploy.sh — deploy personal skills from this repo into the global skills
# folders of supported coding agents, via symlinks.
#
# Usage:
#   ./deploy.sh                  # deploy all skills to all agents (default)
#   ./deploy.sh --list           # list skills + agents, deploy nothing
#   ./deploy.sh --skill NAME     # deploy only the named skill(s)
#   ./deploy.sh --agent AGENT    # deploy only to the named agent(s)
#   ./deploy.sh --all            # alias for default (all skills, all agents)
#   ./deploy.sh --no-skip-system # also deploy into a system/managed dir
#                                #   (dangerous: overwrites nix-managed skills)
#   ./deploy.sh --dry-run        # show what would happen, change nothing
#   ./deploy.sh --prune          # remove deployed symlinks whose skill was deleted from the repo
#                                #   (only touches symlinks pointing into this repo; never nix/third-party)
#
# Skills live in ./skills/<skill-name>/SKILL.md (one skill per directory).
# Agents are defined in the AGENTS table below. Each is a (name, global dir).
# A skill is deployed as a symlink:  <global-dir>/<skill> -> <repo>/skills/<skill>
#
# Safety:
#   - Existing entries that are NOT symlinks (real dirs/files, e.g. nix-managed
#     skills) are skipped by default. Use --no-skip-system to override.
#   - Existing symlinks are refreshed (repointed to this repo).
#   - Never deletes anything the user didn't point here.

set -euo pipefail

# ──────────────────────────────────────────────────────────────────────────
# Resolve repo root (directory containing this script)
# ──────────────────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." 2>/dev/null && pwd)"
[ "${SCRIPT_DIR}" = "${SCRIPT_DIR%/bin}" ] && REPO_ROOT="$SCRIPT_DIR"
# Fallback: if there is no ../bin, assume the script sits at repo root.
if [ ! -d "${REPO_ROOT}/skills" ]; then REPO_ROOT="$SCRIPT_DIR"; fi
SKILLS_SRC="${REPO_ROOT}/skills"

# ──────────────────────────────────────────────────────────────────────────
# Supported agents and their global skills directories.
# `$HOME` is expanded at runtime. Non-existent dirs are created on demand.
# ──────────────────────────────────────────────────────────────────────────
AGENTS=(
  "claude|${HOME}/.claude/skills"
  "opencode|${HOME}/.config/opencode/skills"
  "codex|${HOME}/.codex/skills"
  "agents|${HOME}/.agents/skills"
  "cursor|${HOME}/.cursor/skills"
  "gemini|${HOME}/.gemini/skills"
  "hermes|${HOME}/.hermes/skills"
  "windsurf|${HOME}/.codeium/skills"
  "zed|${HOME}/.config/zed/skills"
  "aider|${HOME}/.aider/skills"
  "cline|${HOME}/.cline/skills"
)

# ──────────────────────────────────────────────────────────────────────────
# State
# ──────────────────────────────────────────────────────────────────────────
DRY_RUN=0
SKIP_SYSTEM=1
WANT_SKILLS=()
WANT_AGENTS=()
LIST_ONLY=0
DOCTOR=0
PRUNE=0

# ──────────────────────────────────────────────────────────────────────────
# Helpers
# ──────────────────────────────────────────────────────────────────────────
log()  { printf '[deploy] %s\n' "$*"; }
warn() { printf '[deploy] ⚠ %s\n' "$*" >&2; }
err()  { printf '[deploy] ✖ %s\n' "$*" >&2; }

usage() {
  sed -n '2,/^set -euo/p' "$0" | sed 's/^# \{0,1\}//' | sed 's/^#//'
  exit 0
}

contains() {  # contains <needle> <item1> <item2> ...
  local needle="$1"; shift
  for x in "$@"; do [ "$x" = "$needle" ] && return 0; done
  return 1
}

# All skills currently in ./skills/ (dir basenames that contain SKILL.md)
all_skills() {
  [ -d "$SKILLS_SRC" ] || return 0
  local d
  for d in "$SKILLS_SRC"/*/; do
    [ -d "$d" ] || continue
    [ -f "${d}SKILL.md" ] && basename "$d"
  done
}

agent_names() {
  local entry
  for entry in "${AGENTS[@]}"; do printf '%s\n' "${entry%%|*}"; done
}

# ──────────────────────────────────────────────────────────────────────────
# Args
# ──────────────────────────────────────────────────────────────────────────
while [ $# -gt 0 ]; do
  case "$1" in
    -l|--list)        LIST_ONLY=1; shift ;;
    --skill)          shift; [ $# -gt 0 ] || { err "--skill needs a name"; exit 2; }; WANT_SKILLS+=("$1"); shift ;;
    --agent)          shift; [ $# -gt 0 ] || { err "--agent needs a name"; exit 2; }; WANT_AGENTS+=("$1"); shift ;;
    --all)            shift ;;
    --no-skip-system) SKIP_SYSTEM=0; shift ;;
    --dry-run)        DRY_RUN=1; shift ;;
    --doctor)         DOCTOR=1; shift ;;
    --prune)          PRUNE=1; shift ;;
    -h|--help)        usage ;;
    *) err "Unknown option: $1"; usage ;;
  esac
done

# ──────────────────────────────────────────────────────────────────────────
# Resolve selection
# ──────────────────────────────────────────────────────────────────────────
mapfile -t SKILLS < <(all_skills)
if [ ${#WANT_SKILLS[@]} -gt 0 ]; then
  for s in "${WANT_SKILLS[@]}"; do
    contains "$s" "${SKILLS[@]}" || { err "Skill not found in ${SKILLS_SRC}: $s"; exit 1; }
  done
  SKILLS=("${WANT_SKILLS[@]}")
fi

SELECTED_AGENTS=()
if [ ${#WANT_AGENTS[@]} -gt 0 ]; then
  for a in "${WANT_AGENTS[@]}"; do
    contains "$a" $(agent_names) || { err "Unknown agent: $a (see --list)"; exit 1; }
    SELECTED_AGENTS+=("$a")
  done
fi

# ──────────────────────────────────────────────────────────────────────────
# Doctor mode: health-check deployed symlinks
# ──────────────────────────────────────────────────────────────────────────
if [ "$DOCTOR" -eq 1 ]; then
  issues=0
  log "Doctor: checking deployed skill links across agents"
  log "  repo: $REPO_ROOT"
  echo ""
  for entry in "${AGENTS[@]}"; do
    name="${entry%%|*}"; gdir="${entry#*|}"
    [ -d "$gdir" ] || continue
    for link in "$gdir"/*; do
      # Skip non-symlinks except real dirs (which are a divergence risk).
      [ -e "$link" ] || [ -L "$link" ] || continue
      base="$(basename "$link")"
      if [ -L "$link" ]; then
        target="$(readlink "$link")"
        # Dangling?
        if [ ! -e "$link" ]; then
          printf '  ✖ [%s] %s: DANGLING symlink -> %s (target missing)\n' "$name" "$base" "$target"
          issues=$((issues + 1))
        # Points outside this repo?
        elif [[ "$target" != "$REPO_ROOT"* ]]; then
          printf '  ~ [%s] %s: out-of-repo symlink -> %s\n' "$name" "$base" "$target"
        else
          printf '  ✓ [%s] %s\n' "$name" "$base"
        fi
      elif [ -d "$link" ]; then
        # Real directory — would be skipped on redeploy; divergence risk.
        printf '  ~ [%s] %s: real dir (not a symlink; skipped on redeploy — divergence risk)\n' "$name" "$base"
      fi
    done
  done
  echo ""
  if [ "$issues" -gt 0 ]; then
    log "✖ $issues dangling symlink(s) found — re-run ./bin/deploy.sh to repoint, or move the repo back."
    exit 1
  fi
  log "✓ No dangling symlinks. (~ = advisory; ✓ = healthy)"
  exit 0
fi

# ──────────────────────────────────────────────────────────────────────────
# Prune mode: remove deployed symlinks whose skill was deleted from the repo.
# Safety: only removes symlinks whose target is inside this repo's skills/.
# Never touches real files/dirs, out-of-repo symlinks (nix/third-party), or
# dangling links whose target can't be confirmed as a former repo skill.
# ──────────────────────────────────────────────────────────────────────────
if [ "$PRUNE" -eq 1 ]; then
  [ "$DRY_RUN" -eq 1 ] && log "DRY RUN — no changes will be made"
  log "Prune: removing symlinks to skills no longer in $SKILLS_SRC"
  removed=0
  for entry in "${AGENTS[@]}"; do
    name="${entry%%|*}"; gdir="${entry#*|}"
    [ -d "$gdir" ] || continue
    for link in "$gdir"/*; do
      [ -L "$link" ] || continue   # only symlinks; skip real files/dirs
      target="$(readlink "$link")"
      base="$(basename "$link")"
      # Only touch symlinks that point into this repo's skills source.
      # Resolve to an absolute path for the comparison.
      case "$target" in
        "$SKILLS_SRC"/*) src_skill_dir="$target" ;;
        *) continue ;;  # out-of-repo (nix/third-party) — leave alone
      esac
      # If the target still exists as a dir, the skill is still in the repo — keep it.
      if [ -d "$src_skill_dir" ]; then
        continue
      fi
      # Target is gone (or dangling) AND pointed into our skills source → prune.
      if [ "$DRY_RUN" -eq 1 ]; then
        log "  [${name}] ${base}: would remove (skill no longer in repo)"
      else
        rm "$link"
        log "  [${name}] ${base}: removed (skill no longer in repo)"
      fi
      removed=$((removed + 1))
    done
  done
  echo ""
  if [ "$removed" -eq 0 ]; then
    log "✓ Nothing to prune. No deployed symlinks point to deleted skills."
  else
    [ "$DRY_RUN" -eq 1 ] && log "Would remove $removed symlink(s). Re-run without --dry-run to apply." \
                          || log "Removed $removed symlink(s)."
  fi
  exit 0
fi

# ──────────────────────────────────────────────────────────────────────────
# List mode
# ──────────────────────────────────────────────────────────────────────────
if [ "$LIST_ONLY" -eq 1 ]; then
  printf 'Repo:    %s\n' "$REPO_ROOT"
  printf 'Source:  %s\n\n' "$SKILLS_SRC"
  printf 'Skills (%d):\n' "${#SKILLS[@]}"
  if [ "${#SKILLS[@]}" -eq 0 ]; then printf '  (none — add ./skills/<name>/SKILL.md)\n'; fi
  for s in "${SKILLS[@]}"; do printf '  • %s\n' "$s"; done
  printf '\nAgents (%d):\n' "${#AGENTS[@]}"
  for entry in "${AGENTS[@]}"; do printf '  • %-9s %s\n' "${entry%%|*}" "${entry#*|}"; done
  exit 0
fi

[ "${#SKILLS[@]}" -gt 0 ] || { err "No skills found in $SKILLS_SRC"; err "Create one with: mkdir -p skills/<name> && edit skills/<name>/SKILL.md"; exit 1; }

# ──────────────────────────────────────────────────────────────────────────
# Deploy
# ──────────────────────────────────────────────────────────────────────────
[ "$DRY_RUN" -eq 1 ] && log "DRY RUN — no changes will be made"
log "Deploying ${#SKILLS[@]} skill(s) from $SKILLS_SRC"

deploy_one() {  # deploy_one <agent_name> <global_dir> <skill_name>
  local agent="$1" gdir="$2" skill="$3"
  local src="${SKILLS_SRC}/${skill}"
  local dst="${gdir}/${skill}"

  # Ensure global dir exists
  if [ ! -d "$gdir" ]; then
    if [ "$DRY_RUN" -eq 1 ]; then
      log "  [${agent}] would mkdir -p ${gdir}"
    else
      mkdir -p "$gdir" || { warn "[${agent}] cannot create ${gdir} — skipping"; return 1; }
    fi
  fi

  if [ -e "$dst" ] || [ -L "$dst" ]; then
    if [ -L "$dst" ]; then
      # Existing symlink — repoint it.
      if [ "$DRY_RUN" -eq 1 ]; then
        log "  [${agent}] ${skill}: relink -> ${src}"
      else
        ln -sfn "$src" "$dst"
        log "  [${agent}] ${skill}: relinked -> ${src}"
      fi
      return 0
    elif [ "$SKIP_SYSTEM" -eq 1 ]; then
      # Real file/dir (likely system-managed, e.g. nix). Skip by default.
      warn "[${agent}] ${skill}: exists as real path, skipping (--no-skip-system to overwrite)"
      return 0
    else
      if [ "$DRY_RUN" -eq 1 ]; then
        log "  [${agent}] ${skill}: would replace real path -> ${src}"
      else
        rm -rf "$dst"
        ln -s "$src" "$dst"
        log "  [${agent}] ${skill}: replaced -> ${src}"
      fi
      return 0
    fi
  fi

  # Nothing there — create the symlink.
  if [ "$DRY_RUN" -eq 1 ]; then
    log "  [${agent}] ${skill}: link -> ${src}"
  else
    ln -s "$src" "$dst"
    log "  [${agent}] ${skill}: linked -> ${src}"
  fi
}

for entry in "${AGENTS[@]}"; do
  name="${entry%%|*}"; gdir="${entry#*|}"
  # If the user selected specific agents, honor that.
  if [ ${#SELECTED_AGENTS[@]} -gt 0 ] && ! contains "$name" "${SELECTED_AGENTS[@]}"; then
    continue
  fi
  # Skip agents whose home dir is absent AND not explicitly selected — likely
  # not installed. Always attempt explicitly selected agents.
  home_base="${gdir}"
  if [ ${#SELECTED_AGENTS[@]} -eq 0 ]; then
    # Only skip if the agent's top-level dotdir is missing (e.g. ~/.cursor).
    local_top="${HOME}/.${name}"
    # For opencode the dotdir is ~/.config/opencode; for agents it's ~/.agents.
    case "$name" in
      opencode) local_top="${HOME}/.config/opencode" ;;
      agents)   local_top="${HOME}/.agents" ;;
      windsurf) local_top="${HOME}/.codeium" ;;
      zed)      local_top="${HOME}/.config/zed" ;;
      *)        local_top="${HOME}/.${name}" ;;
    esac
    if [ ! -d "$local_top" ]; then
      log "[${name}] not detected (${local_top} missing) — skipping (use --agent ${name} to force)"
      continue
    fi
  fi
  for skill in "${SKILLS[@]}"; do
    deploy_one "$name" "$gdir" "$skill" || true
  done
done

log "Done."
