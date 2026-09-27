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

# Everything that names a directory by REFERENCE rather than by name — a
# doubled slash, a trailing slash, a `/.`, a `..` — is folded away here, before
# the last component is read: `dirname /x/.` is `/x` and `basename /x/.` is `.`,
# so a reference left on the end is put back together exactly as it came in — a
# spelling git never prints, and a match that finds nothing.
#
# The fold is TEXTUAL, which is what makes it a fold rather than a visit. The
# path being resolved need not exist — the branch step resolves the recorded
# path of a worktree whose checkout was deleted out of band, and a `..` in that
# path still has to fold — so `cd`, which refuses a path it cannot enter, is
# wrong for exactly the case this exists for. Folding one KIND of reference at a
# time is wrong for the combinations: a trailing slash hides a `/.`, a `/.`
# hides a `//`, and a `..` is not a suffix to truncate at all — the parent of
# `x/sub/..` is `x`, not `x/sub`. So the path is walked once, component by
# component, `.` dropped and `..` popping the component before it. That is every
# one of these cases at once, and it is why the result has nothing left to fold:
# resolving it again changes nothing.
#
# The textual fold costs exactly one case, and it is the price of the property
# above rather than an oversight. `..` pops the component before it in the
# STRING, so a `..` behind a component that is itself a symlink pops the LINK
# rather than the directory the link names: with `l -> B`, `A/l/../sibling`
# answers `A/sibling`, where the kernel — and so `chdir`, which a shell's
# logical `cd` does not imitate — reaches the sibling of `B`'s parent. Two
# different directories, and a physical fold would agree with the kernel, which
# is deliberately not what is done here: a visit asks the filesystem about every
# component, and `readlink -f` refuses a dangling final link outright — the
# checkout deleted out of band with the link left is exactly the case this
# script exists for, so a resolving answer is worth more to its callers than a
# kernel-faithful one. Text is also what the idempotence rests on: nothing is
# left to fold, so resolving the answer again returns it unchanged, which is
# what a caller matching this string against git's listing needs. A path handed
# in with a `..` behind a symlink is therefore answered lexically, by choice.
#
# `..` at the root is the root, and a relative path keeps the `..`s it cannot
# pop, so the result is always a path — never the empty string, which a caller
# would read as the working directory and match against git's listing as a
# miss. `.` and `..` are the two results that are not a directory plus a last
# component at all, and the `cd` below is where they are answered.
abs=""
case "$path" in /*) abs="/" ;; esac
out=""
rest="$path"
while :; do
    case "$rest" in
        */*) comp="${rest%%/*}"; rest="${rest#*/}" ;;
        *)   comp="$rest"; rest="" ;;
    esac
    case "$comp" in
        ""|.) ;;
        ..) case "$out" in
                ""|..) [ -n "$abs" ] || out="$out${out:+/}.." ;;
                */*)   out="${out%/*}" ;;
                *)     out="" ;;
            esac ;;
        *) out="$out${out:+/}$comp" ;;
    esac
    [ -n "$rest" ] || break
done
case "$abs" in
    "/") path="/$out" ;;
    *)   [ -n "$out" ] || out="."
         path="$out" ;;
esac

# `.` and `..` are directories that exist — a caller resolving them is asking
# about a directory it is in or above — and neither splits into a directory plus
# a last component, so `cd` answers both, the same way it answers the directory
# below. A relative path that folds away to nothing is spelled `.` rather than
# left empty, because that is what `a/..` means: the directory the caller is in.
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
