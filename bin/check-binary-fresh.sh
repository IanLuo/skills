#!/usr/bin/env bash
#
# check-binary-fresh.sh — is the fs about to certify this work the build of its tree?
#
# The question is not "is this binary current" in the abstract. It is whether the
# binary that is about to produce evidence is a build of the tree the evidence is
# about, because a verifier older than the change certifies the wrong thing and
# reports success. That is not hypothetical: three builds of this tool sat on one
# machine at once — the documented path, a nix build byproduct, and a fresh build
# — all different, and a fix that worked was concluded twice to have failed
# because the build under test predated it.
#
# The binary can answer it because flake.nix stamps the revision into it
# (`fs version`), which is the part a hermetic build cannot read for itself.
# This script supplies the other half: the tree, and whether it has moved.
#
# It lives in the repo and not inside the binary on purpose. A check shipped in
# the binary is run by the build it is judging, so a stale build would run a
# stale check — the one party that must not decide this question is the artifact
# under suspicion. The stamp is data read out of the binary; the judgement is
# current with the tree.
#
# It is bound as a project-scope exit check for this repo
# (~/.fs/projects/skills/kb/binary-fresh.yaml), which is what keeps it from
# firing in a project that is not fs's own source: there, "the binary is older
# than HEAD" is meaningless and would refuse honest work.
#
# Usage:
#   bin/check-binary-fresh.sh [BINARY]
#
#   BINARY  the binary to ask about; defaults to $FS_BIN, which is what a
#           playbook's check step runs under. The exit moment is where this
#           belongs: that is where a tool certifies.
#
# Exit codes:
#   0  the binary is the build of the tree it belongs to, unchanged since
#   1  it is not, or cannot be shown to be — the reason is named, with the one
#      command that fixes it
#   2  the command line was wrong: no binary named, or not an executable
#
# What it compares, and why it is the whole answer: nix copies a tree's TRACKED
# files, at their on-disk content, into the build — untracked files never reach
# it (flake.nix's vendor note says so, and an untracked file does not even make
# the flake dirty). So "the build of this tree" is exactly "the build of these
# tracked files as they are now", and one `git diff` against the stamped revision
# answers it for both ways the tree can move: commits that landed since the
# build, and edits in the working tree that have not.
set -uo pipefail

bin="${1:-${FS_BIN:-}}"
if [ -z "$bin" ]; then
    echo "check-binary-fresh: no binary named — pass one, or run it as a gate with \$FS_BIN set" >&2
    exit 2
fi
if [ ! -x "$bin" ]; then
    echo "check-binary-fresh: $bin is not an executable file" >&2
    exit 2
fi

# The file, not the spelling: the documented route to this binary is a symlink
# into the repo, and the revision has to be measured against the checkout the
# file actually sits in. `fs version` reports the resolved path, so the binary
# answers this itself wherever it can.
stamp=$("$bin" version 2>/dev/null)
if [ $? -ne 0 ] || [ -z "$stamp" ]; then
    echo "check-binary-fresh: $bin cannot say which build it is." >&2
    echo "It predates the stamp (or is not an fs binary), so there is nothing to" >&2
    echo "check it against. Rebuild it:" >&2
    echo "    ./bin/build-project.sh flagship" >&2
    exit 1
fi

rev=$(printf '%s\n' "$stamp" | sed -n 's/.*"revision": *"\([^"]*\)".*/\1/p')
dirty=$(printf '%s\n' "$stamp" | sed -n 's/.*"dirty": *"\([^"]*\)".*/\1/p')
own=$(printf '%s\n' "$stamp" | sed -n 's/.*"path": *"\([^"]*\)".*/\1/p')

# The tree the build belongs to: walk up from the binary's own file. A binary
# outside any checkout — a nix profile entry, a copy in /tmp — is measured
# against the tree it is being run in instead, which is the tree whose work it
# would be evidence about.
tree=""
dir=$(cd "$(dirname "${own:-$bin}")" 2>/dev/null && pwd -P) || dir=""
while [ -n "$dir" ] && [ "$dir" != "/" ]; do
    if [ -f "$dir/flake.nix" ] && [ -d "$dir/src/flagship" ]; then
        tree="$dir"
        break
    fi
    dir=$(dirname "$dir")
done
if [ -z "$tree" ]; then
    dir=$(pwd -P)
    while [ -n "$dir" ] && [ "$dir" != "/" ]; do
        if [ -f "$dir/flake.nix" ] && [ -d "$dir/src/flagship" ]; then
            tree="$dir"
            break
        fi
        dir=$(dirname "$dir")
    done
fi
if [ -z "$tree" ]; then
    echo "check-binary-fresh: $bin is not in a checkout of this repo and neither is" >&2
    echo "$(pwd -P), so there is no tree to measure the build against." >&2
    exit 1
fi

if [ -z "$rev" ] || [ "$rev" = "unknown" ]; then
    echo "check-binary-fresh: $bin does not name the revision it was built from." >&2
    echo "Nothing can be measured against a build whose tree is unknown — a build" >&2
    echo "id that can be wrong is worse than none. Rebuild it from a checkout:" >&2
    echo "    cd $tree && ./bin/build-project.sh flagship" >&2
    exit 1
fi

# A build of a dirty tree is refused rather than judged. Its stamp says the tree
# had uncommitted changes, and the revision it carries is the commit those
# changes sat on — which cannot say what the build contains. It is not evidence
# that the build is current, and it is not evidence that it is stale: the honest
# answer is that this stamp cannot tell.
if [ "$dirty" != "false" ]; then
    echo "check-binary-fresh: $bin was built from a tree with uncommitted changes" >&2
    echo "($rev, dirty=$dirty), so it cannot be shown to be the build of $tree's" >&2
    echo "current content. Commit, rebuild, and close again:" >&2
    echo "    cd $tree && ./bin/build-project.sh flagship" >&2
    exit 1
fi

# One command, both directions: revision..working tree, limited to what the
# build is made of. A commit that touched only skills/ does not stale a binary,
# and an edit to src/flagship that was never committed does.
moved=$(git -C "$tree" diff --name-only "$rev" -- src/flagship flake.nix flake.lock 2>&1)
if [ $? -ne 0 ]; then
    echo "check-binary-fresh: $bin names revision $rev, which git cannot resolve in" >&2
    echo "$tree: $moved" >&2
    exit 1
fi
if [ -n "$moved" ]; then
    echo "check-binary-fresh: $bin is not the build of $tree — the tree has moved" >&2
    echo "past it (built from $rev; changed since):" >&2
    printf '%s\n' "$moved" | sed 's/^/  /' >&2
    echo "Rebuild it and close again:" >&2
    echo "    cd $tree && ./bin/build-project.sh flagship" >&2
    exit 1
fi

echo "check-binary-fresh: $bin is the build of $tree at $rev (clean tree)"
exit 0
