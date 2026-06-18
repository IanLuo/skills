#!/usr/bin/env bash
# lib/repo_root.sh — resolve the repo root for skill-man's shell scripts.
#
# Source this file: `source "$(dirname "${BASH_SOURCE[0]}")/lib/repo_root.sh"`.
# Sets REPO_ROOT and SKILLS_SRC. Exits with a helpful error if the resolved
# root doesn't contain a skills/ dir (e.g. script relocated).
#
# Works whether invoked from the repo or through the deployed symlink at
# ~/.agents/skills/skill-man/scripts/... — the *directory* skill-man/ is the
# symlink, so `pwd -P` (which dereferences parent-dir symlinks) is essential.

repo_root_resolve() {
  local self="${BASH_SOURCE[1]:-${BASH_SOURCE[0]}}"
  # Follow the script-file symlink chain if the file itself is linked.
  while [ -L "$self" ]; do
    local dir
    dir="$(cd "$(dirname "$self")" && pwd -P)"
    self="$(readlink "$self")"
    case "$self" in
      /*) : ;;
      *)  self="$dir/$self" ;;
    esac
  done
  local script_dir
  script_dir="$(cd "$(dirname "$self")" && pwd -P)"
  # Script lives at <repo>/skills/skill-man/scripts/[lib/]...
  # Walk up until we find a directory containing a skills/ subdir.
  local cur="$script_dir"
  local i=0
  while [ "$i" -lt 6 ]; do
    if [ -d "$cur/skills" ]; then
      REPO_ROOT="$cur"
      SKILLS_SRC="$cur/skills"
      return 0
    fi
    cur="$(dirname "$cur")"
    [ "$cur" = "/" ] && break
    i=$((i + 1))
  done
  echo "repo_root: could not locate repo root (expected a dir containing skills/)" >&2
  echo "  started from: $script_dir" >&2
  return 1
}

repo_root_resolve || exit 1
