# Judging gate — when an answer is "locked-ready"

Read this to decide whether an answer is solid enough to move to the next rung. Same
inclusion-gate *spirit* as init-context, but for *content* not *metadata*.

An answer is **locked-ready** iff all three hold:

1. **It's not recoverable from one command/read against the code or docs.** If `git
   show` or `grep` already states it, don't re-write it in the locked doc — point to it.
   The locked doc earns its keep only by recording what the repo can't tell you.
2. **A fresh agent would plausibly get it WRONG (not just slow) without it.** Style
   preferences the model already honors fail this test. Frozen "because" rules ("never
   touch the mask RT shape, the serializer depends on it") pass it.
3. **Empty/edge/assumption is handled.** The answer names the smallest input that
   should succeed, the smallest that should be rejected, and any unstated assumption is
   either made explicit (labeled) or parked as an open question — never silently
   assumed. This is the `grill`-inherited edge-case discipline, aimed upward.

If any one fails, re-grill the rung. Don't advance on a soft answer — the whole point of
lock is that the study downstream treats it as a frozen input.

## Contract gate (for the contract rung)

The contract rung is locked-ready iff:

- **upstream** is either `none` (PRD root) *or* names docs that exist on disk and are
  themselves locked. Naming an unlocked doc as upstream is a warning, not a block —
  surface it: "you named `<path>` as upstream but it isn't locked; is that intentional?"
- **referrers** are inferable from the doc's type and confirmed with the user. Don't
  guess the dependency edge — ask "who should cite this when they change?" and confirm.

## Locked doc ≠ a log

The doc is rewritten in place at each update. Sections that didn't re-grill stay verbatim;
re-grilled sections replace their predecessors. Never append a "v2" section after a "v1" —
the lock marker + sha record the history; the body records the current truth.