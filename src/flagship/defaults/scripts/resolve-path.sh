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
# Three things about that loop are easy to get wrong, and two of them were, so
# they are named here. A trailing slash has to go before the loop: `/tmp/` names
# the same directory as `/tmp`, but `-L` on it asks the question of the directory
# rather than of the link, says no, and the path is then resolved as if it were
# not a symlink at all — the exact silent miss this script exists to prevent.
# And a target read out of a link can be relative — macOS has /tmp pointing at
# `private/tmp` — so it is joined to the directory the link sits in, which is
# what the kernel does; joining with a bare `/` builds `//private/tmp`, which is
# not a spelling git ever prints.
#
# The loop is bounded, because a link can lead back to a link: `a -> b -> a`, or
# a link to itself, never terminates. `readlink` does not resolve, it only reads
# the target, so nothing underneath refuses on the script's behalf and the loop
# spins forever — and a teardown step that hangs is a close that never returns.
# The kernel gives up on such a chain with ELOOP after a fixed number of links,
# and this gives up in the same place, at the same exit code as any other path it
# cannot resolve, so the caller refuses instead of waiting.
#
# A last component can also be a reference to a directory rather than a name for
# one — `/x/.`, `/x/y/..` — and a leading run of slashes can be doubled, which
# POSIX leaves to the implementation and the shell does not fold for us: `cd //x
# && pwd -P` answers `//x`. None of those is a spelling git prints, and every one
# of them used to come back out exactly as it went in, so a caller that matched
# the result against `git worktree list --porcelain` matched nothing and read
# that as "already gone". In the teardown's branch step that was a branch left
# behind — exit 0, nothing said — which is the same failure mode as the deletion
# these callers were just fixed for, pointed the other way. So the spelling is
# made canonical HERE, once, where the symlink is folded too; no caller has to
# know which spellings git would have printed.
#
# Usage:
#   resolve-path.sh PATH
#
# Exit codes:
#   0  the resolved path, alone on stdout
#   1  the path cannot be resolved — the directory holding it does not exist, so
#      there is nothing to match and the caller must refuse rather than pass, or
#      the final component is a symlink chain that never ends
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

# A run of leading slashes is one slash. `/x` and `//x` name the same directory
# but are not the same string, and the shell keeps the doubled one all the way
# through `cd ... && pwd -P`, so it would reach the caller unchanged.
case "$path" in
    //*)
        rest="${path#/}"
        while [ "${rest#/}" != "$rest" ]; do rest="${rest#/}"; done
        path="/$rest"
        ;;
esac

# A trailing slash names the same directory and hides a symlink at the end of
# the path from -L, so it goes first. "/" itself keeps its one slash.
while [ "$path" != "/" ] && [ "${path%/}" != "$path" ]; do
    path="${path%/}"
done

# A trailing `.` or `..` is a reference to a directory, not a name for one, and
# it has to go before the last component is read: `dirname /x/.` is `/x` and
# `basename /x/.` is `.`, so the two put the spelling back together exactly as
# it came in. Anywhere earlier in the path the kernel folds these for us, which
# is why this looks only at the end — and why it repeats, since folding one can
# expose another.
while :; do
    case "$path" in
        */.)  path="${path%/.}" ;;
        */..) rest="${path%/..}"
              case "$rest" in
                  /*)  parent="${rest%/*}"; [ -n "$parent" ] || parent="/" ;;
                  */*) parent="${rest%/*}" ;;
                  *)   parent="." ;;
              esac
              path="$parent"
              ;;
        *) break ;;
    esac
done

# A path that was nothing but `.` or `..` folds to one of them, and neither can
# be split into a directory and a last component at all. It is a directory that
# exists — a path that did not could not have been asked about — so `cd` answers
# it, which is the same resolution the directory below gets.
case "$path" in
    .|..) path="$(cd "$path" 2>/dev/null && pwd -P)" || exit 1 ;;
esac

# Follow a final symlink, however many of them there are — up to the kernel's
# own limit, past which the chain is a cycle or a path nothing would resolve.
hops=0
while [ -L "$path" ]; do
    hops=$((hops + 1))
    if [ "$hops" -gt 40 ]; then
        echo "resolve-path: $1 is a symlink chain that never ends — a cycle, or more than 40 links" >&2
        exit 1
    fi
    target="$(readlink "$path")" || break
    case "$target" in
        /*) path="$target" ;;
        *)  parent="$(dirname "$path")"
            if [ "$parent" = "/" ]; then path="/$target"; else path="$parent/$target"; fi
            ;;
    esac
done

# The root has no last component to append, and building one from the format
# below would print it as `//` — a spelling nothing prints. A path can fold to
# the root too, now that a trailing `..` is folded: `/x/..` for any top-level x
# is the root and not `x`'s parent as a spelling.
if [ "$path" = "/" ]; then
    printf '/\n'
    exit 0
fi

# Resolve the directory that holds it. This is the part that folds /tmp to
# /private/tmp, and the part that fails when the path is beyond asking about.
dir="$(cd "$(dirname "$path")" 2>/dev/null && pwd -P)" || exit 1
dir="${dir%/}"

printf '%s/%s\n' "$dir" "$(basename "$path")"
