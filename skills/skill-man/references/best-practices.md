# Best practices for authoring skills

Distilled from the official skill-creator guide and verified patterns in the
flagship Anthropic skills (`pdf`, `docx`, `xlsx`) and `obra/superpowers`. Read
this before and while writing a SKILL.md body. The spec itself is in
[skill-spec.md](skill-spec.md).

## TOC

1. The context window is a shared, scarce resource
2. Progressive disclosure — three loading levels
3. Set the right degree of freedom
4. Description-as-trigger (incl. trigger enumeration + negative triggers)
5. Write for another instance of the model
6. Forward-test on real tasks (incl. TDD-for-skills + evidence-before-assertions)
7. Anti-patterns to avoid
8. Naming

## 1. The context window is a shared, scarce resource

A skill shares the context window with the system prompt, conversation history, other
skills' metadata, and the user's request. Every token a skill consumes has a cost.

**Default assumption: the model is already very smart.** Add only what it does not
already know. For each sentence, ask: "Does this justify its token cost?" Prefer
concise examples over verbose explanations.

## 2. Progressive disclosure — three loading levels

Skills use a three-level loading system. Design for it deliberately:

1. **Metadata (`name` + `description`)** — always in context (~100 words). This is the
   only thing that decides whether the skill triggers. Optimize it ruthlessly.
2. **SKILL.md body** — loaded only when the skill triggers. Keep under **500 lines**.
3. **Bundled resources** (`scripts/`, `references/`, `assets/`) — loaded as needed, on
   demand. References are read into context when relevant; scripts can be *executed*
   without being read at all; assets are never read, only used in output.

The goal: a user request that doesn't need your skill pays only the metadata cost.
A request that triggers it pays the body cost. Only the specific sub-topic the request
needs pays the reference cost.

### When to split to references

Approaching 500 lines, or when the skill supports multiple variants/frameworks/domains,
move detail out. Keep only the **core workflow + selection guidance** in SKILL.md.

```
cloud-deploy/
├── SKILL.md          # workflow + how to choose a provider
└── references/
    ├── aws.md        # loaded only when user picks AWS
    ├── gcp.md
    └── azure.md
```

When splitting, **reference each file from SKILL.md** with a one-line "read this when…"
so the reader knows it exists. Keep references one level deep — don't have references
link to other references.

## 3. Set the right degree of freedom

Match the specificity of your instructions to the task's fragility and variability:

| Freedom | Form | Use when |
|---|---|---|
| **High** | text guidance | many valid approaches; decisions depend on context; heuristics guide the way |
| **Medium** | pseudocode / parameterized scripts | a preferred pattern exists; some variation acceptable; config affects behavior |
| **Low** | strict scripts, few params | operations are fragile/error-prone; consistency is critical; a specific sequence must be followed exactly |

Think of the model as exploring a path: a narrow bridge over cliffs needs guardrails
(low freedom); an open field allows many routes (high freedom). Use strict scripts for
the fragile parts and leave the rest as judgment.

## 4. Description-as-trigger

(See [skill-spec.md](skill-spec.md) for the rules; this is the *why*.)

The `description` is the only signal the harness uses to decide whether your skill
fires. So:

- Put **all** "when to use" information in the description, never in the body.
- A "When to Use this Skill" section in the body is dead weight — by the time the body
  loads, the description has already won (or lost) the trigger.
- Be specific and trigger-oriented: name the concrete situations, file types, or task
  shapes. Avoid generic verbs ("helps with…").

### Enumerate literal triggers

The flagship skills don't rely on prose alone — they enumerate concrete triggers.
The `docx` skill's description lists: *any mention of "Word doc", "word document",
".docx"*, … The `pdf` skill ends: *"If the user mentions a .pdf file or asks to
produce one, use this skill."* Literal enumeration (file extensions, exact phrases)
outperforms abstract description. Prefer:

> "Triggers include: .docx, 'word document', 'create a Word doc'…"

### Add negative triggers to disambiguate

When a sibling skill could also match, say what NOT to use this skill for. The `docx`
skill: *"Do NOT use for PDFs, spreadsheets, Google Docs, or general coding tasks."*
The `xlsx` skill likewise uses "Do NOT trigger when…". A negative trigger stops the
harness from picking the wrong skill among near-neighbors.

### The 1% rule (trigger aggressiveness)

From `obra/superpowers`'s `using-superpowers`: *"If you think there is even a 1%
chance a skill might apply to what you are doing, you ABSOLUTELY MUST invoke the
skill."* Author descriptions with this in mind — make the trigger surface broad
enough that a 1%-plausible match is captured, then let the skill's body decide
whether to actually act. (This is a convention some skill ecosystems enforce; your
target harness may or may not, but broad triggers help either way.)

## 5. Write for another instance of the model

The skill is consumed by a fresh model instance with no memory of your conversation.
Include what is **beneficial and non-obvious** to that instance: procedural knowledge,
domain specifics, reusable assets. State things the model couldn't derive from the
request alone.

## 6. Forward-test on real tasks

After authoring (especially for tricky skills), stress-test with a fresh subagent:

- The subagent should **not** know it's testing a skill. Prompt it like a real user:
  `Use $skill-x at /path/to/skill-x to solve problem y`.
- Pass **raw artifacts** (example prompts, outputs, diffs, logs), not your diagnosis of
  what's wrong or the intended answer. Success must depend on the skill's transferable
  reasoning, not leaked context.
- Use fresh threads for independent passes; clean up artifacts between iterations so
  they don't contaminate the next pass.
- If the skill only succeeds when the subagent sees leaked context, tighten the skill
  or the test setup before trusting it.

Err on the side of forward-testing, but ask the user first if it would take a long
time, need extra approvals, or touch production.

### Baseline-then-write (TDD-for-skills)

From `obra/superpowers`'s `writing-skills`: treat skill authoring as TDD applied to
process documentation. *"If you didn't watch an agent fail without the skill, you
don't know if the skill teaches the right thing."*

1. **RED** — Write a pressure scenario (a realistic task). Run it with a fresh
   subagent that does *not* have the skill. Watch it fail or produce a worse result.
   This baseline is your proof the skill is needed.
2. **GREEN** — Write/adjust the skill. Re-run the same scenario. Watch the subagent
   now comply or succeed.
3. **REFACTOR** — Tighten the skill to close loopholes the subagent found.

Baseline-then-write makes the existing forward-test step concrete and falsifiable:
you have a documented failure to compare against.

### Evidence before assertions

A skill that drives verification should itself be verified. `obra/superpowers`'s
`verification-before-completion` distills the rule: *"NO COMPLETION CLAIMS WITHOUT
FRESH VERIFICATION EVIDENCE."* When you (or a skill-using agent) claim work is done,
run the verification commands and show the output. Consider installing
`verification-before-completion` as a companion to any skill that ships code or
config — it's the canonical evidence-before-assertions shape.

## 7. Anti-patterns to avoid

- **Auxiliary docs.** No README, CHANGELOG, install guides, or process notes. The skill
  is for an agent.
- **Deep reference nesting.** References should link directly from SKILL.md, not from
  each other. Deep chains hide content and bloat navigation.
- **Duplication.** A fact should live in either SKILL.md or a reference — not both.
  Keep core procedure in SKILL.md; move detailed reference material out.
- **Generic descriptions.** "Helps with X" doesn't trigger well. Name the concrete
  triggers.
- **Over-explaining what the model knows.** Don't re-explain standard tooling or common
  knowledge. Spend tokens only on what's non-obvious.
- **Unbounded body.** A 2000-line SKILL.md defeats progressive disclosure — the whole
  point is that triggering the skill is cheap.

## 8. Naming

- Lowercase-hyphen-case, ≤64 chars, verb-led when possible (`rotate-pdf`).
- Namespace by tool when it aids triggering (`gh-address-comments`).
- Folder name must equal the `name` field exactly.
