# Document format — for agents, not humans

The reader of any durable doc this repo's skills produce (spec, system-design,
architecture, design-system, AGENTS, library entry) is an **agent**, not a
person. Write for it: minimum size, complete information. A human reads prose; an
agent reads structure.

## Rules

- **Bullets only.** No narrative, no intro/transition sentences, no restating of
  upstream docs — point to them instead. Terseness is a cost; cut every word an
  agent doesn't need.
- **Completeness via checklist, not wordcount.** Every required section is a fixed
  header that MUST be present and non-empty. An explicit `N/A: <one-line why>` beats
  an absent section — an agent verifies completeness by grepping headers, not by
  reading prose.
- **Stable, machine-greppable anchors.** Keep section headers verbatim from the
  defining ladder/template. Keep lock markers and link contracts exactly as the lock
  script writes them — never hand-edit.
- **Explicit states.** `locked` / `superseded` / `N/A` — say so in the doc. Never
  silently overwrite a frozen section; mark it `Superseded by: <doc or N/A>`.
- **Freshness.** The doc carries its date (lock marker or frontmatter). Optionally end
  with `Last reviewed: <YYYY-MM-DD> · <owner>`, bumped only by a real re-review. An
  agent that sees an old date treats the doc as possibly stale.
- **Code is truth, docs are cache.** If implementation contradicts a doc, flag the
  discrepancy to the user — never silently rewrite the doc to match code.
- **Supersede vs rotate is a conscious decision.** A locked doc can be re-opened and
  re-locked (`--force`, in place) OR superseded by a new doc with a back-reference.
  Pick deliberately; don't let it happen silently.
