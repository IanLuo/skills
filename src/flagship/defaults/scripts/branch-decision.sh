#!/usr/bin/env bash
#
# branch-decision.sh — one answer, in the exit status, for what the teardown may
# do with the branch a delivery's worktree stands on.
#
# The teardown has one thing to decide: what to do with the branch the delivery
# created. Four rounds of fixes produced seven defects from that decision, and
# three of them were the same defect — the step assembled its answer out of shell
# test results, a text match against git's listing followed by
# `[ -n "$b" ] || exit 0`, so "the match found nothing" and "there is correctly
# nothing to do" were the same value: empty stdout, exit 0. A branch that matched
# nothing was skipped in silence; a branch that matched the wrong worktree was
# deleted. The defect was never the language. It was where the answer was
# assembled.
#
# So the decision lives here and answers in its status, one state per status,
# never in the text it prints:
#
#   0  nothing to do. stdout and stderr are both empty. Nothing to do covers all
#      three ways of there being no branch to delete: no worktree at the path, a
#      worktree standing on no branch, and a worktree standing on `HEAD` — the
#      one name that arrives looking like a branch and is not one.
#   1  delete the branch named on stdout. stdout is the branch name and nothing
#      else; stderr is empty.
#   2  the command line was wrong. stderr says how.
#   3  refuse. stderr is the reason, one line, prefixed with LABEL when given.
#
# The property that makes a caller safe is arithmetic, not attention: the branch
# name reaches stdout on exactly one status, and it is not 0. A caller that
# ignores the status can delete only the branch this script authorised, because
# on every other status there is no name to delete; a caller under `set -e`
# deletes nothing at all, because the assignment fails and the step aborts. The
# status that authorises destruction is not the status of not looking.
#
# Usage:
#   branch-decision.sh PATH [LABEL]
#
# PATH is the worktree path a delivery recorded, spelled any way. LABEL, when
# given, prefixes every refusal so a fan-out can name the member it was deciding
# about; it is never read as part of the decision.
set -uo pipefail

usage() {
    echo "branch-decision: usage: branch-decision.sh PATH [LABEL]" >&2
    exit 2
}

[ $# -ge 1 ] || usage
[ $# -le 2 ] || usage
[ -n "$1" ] || {
    echo "branch-decision: PATH is empty, so it names no worktree" >&2
    exit 2
}

label=""
if [ $# -ge 2 ] && [ -n "$2" ]; then
    label="$2: "
fi

p="$1"

# FS_BIN is the running binary's own path, exported by both env builders. A step
# has no other way to reach a script the binary ships, and an unreadable
# os.Executable() exports it empty — which is missing, not a path, so the refusal
# names it rather than reporting a path that could not be resolved.
[ -n "${FS_BIN:-}" ] || {
    echo "${label}FS_BIN is not set, so the recorded path $p cannot be resolved, and which worktree it names cannot be told" >&2
    exit 3
}

# The recorded path is resolved first, and named $p only once that succeeded: the
# fold is what makes a spelling git prints and a spelling a record carries the
# same string, and assigning the empty stdout of a failed call to $p would make
# every message below name nothing.
rp=$("$FS_BIN" script resolve-path.sh "$p") || {
    echo "${label}cannot resolve the recorded path $p, so which worktree it names cannot be told" >&2
    exit 3
}
p="$rp"

# git's own registration is where the branch is named from — never by asking the
# directory: `git -C "$p" rev-parse` answers for whatever repository ENCLOSES $p,
# so a path naming the project's own checkout came back with the project's own
# branch and the step deleted it.
listing=$(git worktree list --porcelain) || {
    echo "${label}cannot read git's worktree list, so which worktree $p names cannot be told" >&2
    exit 3
}

# The exit steps run from the project root, so `git rev-parse --show-toplevel` is
# that checkout, whichever one it is — including when the project root is itself
# a linked worktree, where the repository's main checkout is a different
# directory.
rq=$(git rev-parse --show-toplevel 2>/dev/null)
here=$("$FS_BIN" script resolve-path.sh "$rq") || {
    echo "${label}cannot resolve this project's own checkout ${rq}, so whether the recorded path $p is it cannot be told" >&2
    exit 3
}

# A delivery's worktree is never the project's own checkout. A record naming it
# is a wrong record, and acting on it detached the project's own checkout and
# deleted the project's own branch — exit 0, no error.
[ "$p" != "$here" ] || {
    echo "${label}the recorded path $p is the project's own checkout, not a delivery's worktree; refusing to touch it" >&2
    exit 3
}

# The branch the worktree at $p stands on, out of its own stanza. No stanza at
# that path is not an error and not a failure to look: the worktree was already
# torn down, or there never was one there, and there is nothing to do. A detached
# worktree prints `detached` and no branch line, which is the same answer for the
# same reason — there is no branch here to delete.
b=$(printf '%s\n' "$listing" | grep -A2 -xF "worktree $p" | sed -n 's|^branch refs/heads/||p')
[ -n "$b" ] || exit 0

# `HEAD` is a name git's listing prints in the branch slot, and it is not a
# branch. `git -C <worktree> symbolic-ref HEAD refs/heads/HEAD` makes the
# registration read `branch refs/heads/HEAD`, so the name is read out of git's
# own output like any other — and it is the one name git will not delete:
# `git branch -d HEAD` refuses — with the worktree in place, because that
# worktree uses it. A decision answering "delete" here would authorise a destroy
# git cannot honour: the caller detaches the worktree for nothing and the step
# ends non-zero, on a state whose only correct answer is silence.
#
# The guard that used to stand in the step, `[ "$b" = HEAD ] && exit 0`, was
# dropped while this decision was being written on the argument that no branch
# is called HEAD — an unreachability inferred from a failed attempt to build the
# state rather than from the state being unbuildable. It is buildable, in one
# command, and this is the row in the tests that builds it. It sits next to the
# "no branch line" answer because it is the same answer for the same reason:
# there is no branch here to delete.
[ "$b" != HEAD ] || exit 0

# The first stanza is the repository's main checkout, and a delivery's worktree
# is never that either: it exists only in a record that is wrong about what the
# delivery was given. This is asked as a path, the same question as the guard
# above, because the fact about the main checkout is where it is — naming its
# branch instead refused a linked worktree that held the same name, and said the
# project's own checkout when it meant this one.
main=$(printf '%s\n' "$listing" | sed -n '1s|^worktree ||p')
[ "$p" != "$main" ] || {
    echo "${label}the recorded path $p is the repository's main checkout, not a delivery's worktree; refusing to touch it" >&2
    exit 3
}

# A branch is checked out in exactly one worktree unless git was talked into it
# with --ignore-other-worktrees, and git refuses to delete a branch another
# worktree holds. One registration line is this worktree's; two or more mean the
# branch is not only here, and a delete this script authorised would be a delete
# git refuses — the authorisation has to be one git will honour.
n=$(printf '%s\n' "$listing" | grep -cxF "branch refs/heads/$b")
[ "$n" -le 1 ] || {
    echo "${label}branch $b is checked out in another worktree, not only at $p; refusing to delete it" >&2
    exit 3
}

# Where the repository records which branch is its default. A repository with no
# remote records it nowhere, and this guard then stands down — a branch that is
# the default only by convention, checked out nowhere, is not a fact anything
# here can read.
default=$(git symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null) || default=""
default="${default#*/}"
[ -z "$default" ] || [ "$b" != "$default" ] || {
    echo "${label}branch $b is the repository's default branch; refusing to delete it" >&2
    exit 3
}

# A branch that is not merged into HEAD holds the only copy of that work, and
# `git branch -d` is only the second guard: the answer has to be right before
# anything is asked of git.
git merge-base --is-ancestor "$b" HEAD || {
    echo "${label}branch $b is not merged into HEAD; refusing to delete it" >&2
    exit 3
}

printf '%s\n' "$b"
exit 1
