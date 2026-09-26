#!/usr/bin/env bash
#
# resolve-path.sh — print a path in the one spelling git prints it in.
#
# A teardown step has to match a recorded path against `git worktree list
# --porcelain`, and git prints the path it resolved when the worktree was added.
# One directory has several spellings: /tmp and /private/tmp on macOS, a path
# that went through `..`, a trailing slash, and a final component that is itself
# a symlink. Both sides have to be spelled the same way or the match finds
# nothing — and a match that finds nothing looks exactly like a worktree that is
# already gone, which is why this lives in one place instead of being re-derived
# at each step that needs it.
#
# The resolution this replaces folded the directory ABOVE the path and left a
# symlinked final component alone, because `cd $(dirname ...) && pwd -P` says
# nothing about the last segment. Every step built on that match then passed
# silently over a symlinked worktree: the branch step left the branch and exited
# 0, and the registration check reported the worktree gone while git still had it
# registered. Both are the same defect — two spellings of one directory — and
# both are fixed by asking here.
#
# A final symlink is followed first, and `readlink` answers for a dangling one
# too. That is the case that matters: the checkout has been deleted out of band,
# the link is all that is left, and the registration and its branch are still
# there to be collected.
#
# Two things about that loop are easy to get wrong, and both were, so they are
# named here. A trailing slash has to go before the loop: `/tmp/` names the same
# directory as `/tmp`, but `-L` on it asks the question of the directory rather
# than of the link, says no, and the path is then resolved as if it were not a
# symlink at all — the exact silent miss this script exists to prevent. And a
# target read out of a link can be relative — macOS has /tmp pointing at
# `private/tmp` — so it is joined to the directory the link sits in, which is
# what the kernel does; joining with a bare `/` builds `//private/tmp`, which is
# not a spelling git ever prints.
#
# Usage:
#   resolve-path.sh PATH
#
# Exit codes:
#   0  the resolved path, alone on stdout
#   1  the path cannot be resolved — the directory holding it does not exist, so
#      there is nothing to match and the caller must refuse rather than pass
#   2  the command line was wrong
set -uo pipefail

if [ $# -ne 1 ]; then
    echo "resolve-path: usage: resolve-path.sh PATH" >&2
    exit 2
fi
if [ -z "$1" ]; then
    echo "resolve-path: an empty path names no directory" >&2
    exit 2
fi

path="$1"

# A trailing slash names the same directory and hides a symlink at the end of
# the path from -L, so it goes first. "/" itself keeps its one slash.
while [ "$path" != "/" ] && [ "${path%/}" != "$path" ]; do
    path="${path%/}"
done

# Follow a final symlink, however many of them there are.
while [ -L "$path" ]; do
    target="$(readlink "$path")" || break
    case "$target" in
        /*) path="$target" ;;
        *)  parent="$(dirname "$path")"
            if [ "$parent" = "/" ]; then path="/$target"; else path="$parent/$target"; fi
            ;;
    esac
done

# Resolve the directory that holds it. This is the part that folds /tmp to
# /private/tmp, and the part that fails when the path is beyond asking about.
dir="$(cd "$(dirname "$path")" 2>/dev/null && pwd -P)" || exit 1
dir="${dir%/}"

printf '%s/%s\n' "$dir" "$(basename "$path")"
