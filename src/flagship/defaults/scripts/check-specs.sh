#!/usr/bin/env bash
#
# check-specs.sh — assert every locked spec still describes the code it covers.
#
# A spec carries `<!-- specs:locked:<sha> <date> ... -->`, naming the commit it
# was written against. That marker is a claim: the code at <sha> is what this
# document describes. When commits land on the covered code afterwards the claim
# quietly becomes false — the doc still reads as authoritative, and only a
# reader who diffs it against HEAD can tell. This script makes the question
# mechanical: for each spec, list the commits that have landed since its lock.
#
# What "the covered code" means is the document's own answer. A marker may carry
# `covers=path[,path]`, and only commits touching those paths are movement; a
# document that names none is measured against the whole project. This is
# declared in the doc because the doc is what knows it — fs ships this script to
# every project, so it cannot carry one project's layout.
#
# It ships inside the fs binary and fs writes it to ~/.fs/scripts/, so it is
# addressed as "$FS_SCRIPTS/check-specs.sh" — an absolute path that exists in
# every project.
#
# Usage:
#   "$FS_SCRIPTS/check-specs.sh" [--dir DIR] [--covered PATH]...
#
#   --dir DIR       where the locked docs live (default: .agents/specs)
#   --covered PATH  measure every doc against PATH instead of its own covers=,
#                   repeatable or comma-separated. For a project whose docs do
#                   not declare coverage, or to ask a narrower question once.
#
# It runs as a gate step in a playbook, so its exit code is the answer:
#   0 — every spec is current
#   1 — at least one is behind, named with the commits that moved past it
#   2 — the command line itself was wrong
#
# The remedy is one of two, and the choice is the cap's, recorded as a decision:
# revise and re-lock the spec (a `--tag spec` dispatch), or proceed past the
# gate with --confirm, accepting the delta.
set -uo pipefail

dir=.agents/specs
covered=""

while [ $# -gt 0 ]; do
    case "$1" in
        --dir|--covered)
            [ $# -ge 2 ] || { echo "check-specs: $1 needs a value" >&2; exit 2; }
            if [ "$1" = "--dir" ]; then
                dir="$2"
            else
                covered="${covered:+$covered,}$2"
            fi
            shift 2
            ;;
        *)
            echo "check-specs: unknown argument $1" >&2
            exit 2
            ;;
    esac
done

# A project with no docs directory has no locked doc that can be behind: the
# question is vacuously settled, and refusing would gate every dispatch in a
# project that has simply never written one down. Absence is not staleness.
# (The nix build sandbox is a live case: it carries the tracked tree only.)
if [ ! -d "$dir" ]; then
    exit 0
fi

status=0

for doc in "$dir"/*.md; do
    [ -e "$doc" ] || continue

    marker=$(grep -m1 'specs:locked:' "$doc" 2>/dev/null || true)
    sha=$(printf '%s\n' "$marker" | sed -n 's/.*specs:locked:\([0-9a-f]\{7,\}\).*/\1/p')

    if [ -z "$sha" ]; then
        echo "$doc carries no specs:locked marker" >&2
        status=1
        continue
    fi

    if ! git cat-file -e "${sha}^{commit}" 2>/dev/null; then
        echo "$doc is locked at $sha, which is not a commit in this repo" >&2
        status=1
        continue
    fi

    # A lock sha that is not an ancestor of HEAD cannot be compared by range:
    # `sha..HEAD` would report every commit on this branch instead of the ones
    # that moved past the lock. That is a different problem, and it says so.
    if ! git merge-base --is-ancestor "$sha" HEAD 2>/dev/null; then
        echo "$doc is locked at $sha, which is not an ancestor of HEAD" >&2
        status=1
        continue
    fi

    # The doc's own coverage, or the override, or the whole project. Never
    # empty: an empty pathspec would make git log report nothing and pass a
    # gate it never evaluated.
    declaration="${covered:-$(printf '%s\n' "$marker" | sed -n 's/.*covers=\([^ ]*\).*/\1/p')}"
    declaration="${declaration:-.}"
    IFS=',' read -r -a paths <<< "$declaration"

    # A declared path that is not here means the doc names code this repo does
    # not have — a path that moved, or a doc carried into the wrong project.
    # Either way "no commits touched it" would be true and meaningless.
    missing=""
    for p in "${paths[@]}"; do
        [ -e "$p" ] || missing="${missing:+$missing, }$p"
    done
    if [ -n "$missing" ]; then
        echo "$doc is measured against $missing, which does not exist here" >&2
        status=1
        continue
    fi

    moved=$(git log --oneline "$sha"..HEAD -- "${paths[@]}" 2>/dev/null)
    if [ -n "$moved" ]; then
        echo "$doc is locked at $sha but ${declaration} has moved since:" >&2
        printf '%s\n' "$moved" | sed 's/^/  /' >&2
        status=1
    fi
done

exit $status
