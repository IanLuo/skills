---
name: flagship
description: 'Manage projects, tasks, decisions, and knowledge via the flagship (fs) CLI — the persistent memory layer for agent orchestration. Use when the user says "track this project", "create a project", "add a task", "what''s the status", "what tasks are active", "what decisions were made", "record this decision", "resume work on project X", "pick up where I left off", "check prerequisites", "fs", "flagship", or any project/task tracking request. Do NOT use for writing code (dev-task), writing specs (specs), spawning agents (herdr), or research (librarian).'
metadata:
  audience: personal
  domain: agent-orchestration
---

# flagship

Manage projects, tasks, decisions, and knowledge via the `fs` CLI. Every state change is an immutable event — current state is derived by replaying the event log. A dispatch's work is governed by **playbooks**: protocols matched to the situation, ordered, and recorded on a trace that a close-out gates against.

## Setup

Build the binary from the repo root — and ask the binary what it is before you
trust what it says:

```bash
# Build (from the skills repo root)
./bin/build-project.sh flagship

# Check: is this fs the build of the tree I am working on?
fs version                                                    # revision, dirty, and the file it is running from
bash bin/check-binary-fresh.sh ./skills/flagship/scripts/fs   # exit 0 current, 1 refused with the reason
```

Name the build to judge. Bare — `bash bin/check-binary-fresh.sh` — it is exit 2
(`no binary named`), because that form is the gate's: as a playbook check step it
runs with `$FS_BIN` set to the binary that is running the step, so the build
being judged is the one producing the evidence.

`build-project.sh` builds `packages.flagship` with nix — Go 1.27, named because
`go.mod` requires >= 1.26.7 and nixpkgs' default `go` is older, with dependencies
vendored in `src/flagship/vendor` so the build is hermetic and offline — and
installs the result at `skills/flagship/scripts/fs`. Nothing installs it on your
`PATH`: either add that directory yourself or `nix profile install .#flagship`
for a nix-managed entry. In every example below, `fs` means one of those.

Use the script rather than `nix build` on its own: a bare `nix build .#flagship`
leaves `./result/bin/fs` where it is, a second copy of the tool that installs
nothing and that nothing keeps current. Three builds of this binary have sat on
one machine at once, all different, and a fix that worked was twice concluded to
have failed because the build under test predated it. Either way, `fs version`
names the file it is running from, so a build can always be identified:

```bash
./result/bin/fs version   # names the file it is running from and the revision it was built from
```

If the binary doesn't exist, the skill can't function — run the build first.
The playbooks ship inside the binary and are live from it: `~/.fs/kb/` holds your
own overrides and nothing has to be installed there (see Playbooks below).

## The rule: always read context first

**Before any work**, discover projects and read state. Never create tasks or make changes blind:

```bash
fs project list                          # discover all managed projects
fs status --project <name>               # current task tree
fs query "<topic>" --project <name>     # relevant decisions/knowledge
fs kb get <playbook-name>               # applicable procedures
```

In a fresh session, `fs project list` is always the first command — it shows all known projects ordered by last activity. Then drill into the relevant project with `fs status`.

## Workflow

1. **Discover** — `fs project list` to find managed projects, then `fs status` for the relevant one
2. **Prepare a dispatch** — `fs dispatch --project P --type T --goal G` with the tags the work needs (`--tag code`, for example); the brief prints the plan that will govern the work
3. **Pick or create a task** — find an existing pending task or `fs task add`
4. **Mark active** — `fs task update <id> --status active`
5. **Work** — use other skills as appropriate (dev-task, specs, etc.)
6. **Record decisions and knowledge** — along the way, not after
7. **Mark done** — `fs task update <id> --status done --decision "<what was decided>"`

## CLI reference

Every command but one returns JSON to stdout: `{"ok": true, "data": {...}}` or `{"ok": false, "error": "..."}`. Exit codes: 0=success, 1=app error, 2=usage. Logs go to stderr and are teed to `~/.fs/logs/fs.log` (see The running log). The one exception is `fs kb prompt`, which prints plain text to stdout (its whole point is to pipe into a prompt).

Exit 2 means the command line itself was wrong: an unknown flag, a flag missing its value, or a stray positional where the subcommand takes none. It is refused, never ignored — a typo'd flag is an error, not a no-op that reports success. Every subcommand answers `--help` / `-h` / `help` with its usage on stderr and exit 0, without touching the store, the registry, or `~/.fs`.

### Project

```bash
fs project create [--name NAME] [--root PATH]   # create + register
fs project list                                  # all known projects (ordered by last activity)
fs project get NAME                              # single project lookup
```

`--root` defaults to git repo root or cwd. `--name` derives from directory if omitted. All projects are registered in the global registry at `~/.fs/registry.db`.

Commands that take a scope (`status`, `query`, `log`, `task *`) resolve it from the cwd's registered project. Run from a directory that is no registered project's root, they refuse with exit 1 rather than inventing a partition named after the directory. Pass `--project <name>` to target any scope explicitly — registered or not, the implicit `cap` scope included — or run `fs project create` to register the directory.

### Tasks

```bash
# Add (top-level or subtask). --kind names what the node is: work (default), dispatch, or gap.
# --found-by PROJECT:NODE records what a gap was found by. --check names a shell
# command that would show the node's claim is over — a gap's defect, as a block's
# condition — and applies to a gap as much as to a block.
fs task add --goal "Implement auth" [--parent NODE_ID] [--kind work|dispatch|gap] [--found-by PROJECT:NODE] [--check "SHELL COMMAND"] [--project ID]

# Show one task's derived state; exit 1 when no such node exists, or when --kind names a different kind
fs task get NODE_ID [--project ID] [--kind work|dispatch|gap]

# Update status (pending → active → done | blocked)
fs task update NODE_ID --status STATUS [--decision TEXT] [--project ID]

# Edit goal, or its structural kind, or both
fs task edit NODE_ID [--goal "New goal text"] [--kind work|dispatch|gap] [--project ID]

# Block / unblock. --check names a shell command that would show the condition is
# over, re-run by every reader that renders a status.
fs task block NODE_ID --reason "Waiting for API keys" [--check "SHELL COMMAND"] [--project ID]
fs task unblock NODE_ID [--project ID]

# Record knowledge on a task
fs task knowledge NODE_ID --summary "JWT refresh tokens expire after 7 days" [--project ID]
```

## Playbooks

A **playbook** is a protocol for one participant. It declares the moments it takes part in as sections (`cap:`, `entry:`, `work:`, `exit:`), the situations it is for (`applies_when`), and where it sits in the order; a dispatch matches those declarations against its tags, so a playbook expresses a situation instead of a name. The moment is a section, not a field: one file is one participant, read top to bottom, and the moment decides when its steps run — never its position in the file.

### Schema

Playbooks are YAML files. Global ones live at `~/.fs/kb/<name>.yaml`; a project's own live at `~/.fs/projects/<project>/kb/<name>.yaml`. The schema is `name`/`applies_when`/`order`/`engine`/`agent`/`agent_args`/`creates` plus one section per moment the playbook serves:

```yaml
name: code                  # required; must equal the file name
applies_when: code          # optional, default empty = matches every dispatch.
                            # comma-separated terms, ALL must hold. term = `k` (present) or `k=v`
order: 10                   # optional, default 500; ascending within each moment; ties by name
engine: herdr               # optional: the dispatcher this protocol's steps are written for
agent: pi                   # optional: the kind of agent that runs this work
agent_args: --permission-mode, auto   # optional: the argv that agent is started with, one
                            # element per comma-separated term
entry:                      # the moment is a section — one per moment, any combination, any order
  - check: test -f AGENTS.md
  - ask: a locked PRD is present, and it names this one deliverable
```

`engine:` and `agent:` are how the work is **realized** — both are playbook fields, never Go constants. They resolve from the plan in two passes: the *standing* declaration is a playbook with no `applies_when` (the shipped `worker` declares `engine: herdr` and `agent: pi`), and a *situation-specific* declaration in a selected playbook overrides it. Two situation-specific declarations that disagree refuse, naming both playbooks and both values; a plan declaring neither refuses, naming `worker.yaml` as where to declare them; and a declared engine other than the one fs dispatches with refuses. Both values are recorded on the dispatch and in its trace. Each must be a single non-empty token when present.

`agent_args:` sits beside `agent:` and resolves by the same two passes and the same strength-of-claim rule, with one difference forced by what the terms are. Each comma-separated term is **one argv element, never a shell string** — a comma separates rather than a space precisely so a space can sit inside a term — and the list is comma-separated *on the field's own line* because this parser is line-oriented and a YAML block list would be dropped in silence. A situation-specific playbook that overrides the kind and declares no arguments **inherits** the standing ones: coupling the two would require fs to know that arguments are kind-specific, which is the knowledge these fields exist to remove. Two declarations that disagree refuse, naming both playbooks and both lists — never concatenated, because fs cannot read the arguments and merging would let the agent's own last-wins rule decide, with the winner set by two `order:` integers in two files. Unlike `engine:` and `agent:`, the terms are **not** validated: an agent's own command line is not a name, so `--add-dir /tmp/x` and `--settings=a=1` are legal here. herdr refuses what it cannot encode, and the printed line quotes every term. The terms reach the agent through both emitters of the one start command — the argv `fs dispatch --deliver` execs, and the `next_command` line a human copy-pastes — placed after a `--` that comes last, after every option.

A step is `kind: body`. The closed kind vocabulary is `say | use | check | ask | do | include`:

| Kind | Meaning |
|---|---|
| `say` | Guidance for the reader — worker context in a brief, prose in a close report. Never a gate. |
| `use` | A skill hint for the worker, as `<skill> — <when>`. A hint, not a requirement. |
| `check` | A shell command the gate runs; pass/fail plus the command's output. |
| `ask` | A question only the cap can confirm; refused until `--confirm` says it has been. |
| `do` | A shell command that acts — the declared teardown. `exit` section only. |
| `include` | Splices another playbook's steps at this position. |

Which kinds a moment allows is a closed table — checked per section, so a `do` under `entry:` is refused at write time exactly as a `do` in an entry file was:

| moment | runs | allowed kinds |
|---|---|---|
| `cap` | the cap's standing rules, in the session prompt | `say` |
| `entry` | before the worker starts | `check`, `ask`, `say` |
| `work` | the worker's brief | `say`, `use` |
| `exit` | at close | `check`, `ask`, `do`, `say` |

`include` is allowed in every moment (it is spliced away before validation, and what it splices must itself be legal for the including section).

The old fields and forms are **refused at parse** with a message naming the replacement — `trigger: cap` → the `cap:` section; `engine_scope: herdr` → `engine: herdr` (the declaration that now exists), or the term `applies_when: engine=herdr` when the intent is to *match* an engine rather than declare one; `type: prerequisite|cleanup` → the section of that moment plus a matching `applies_when`; and — since the moment became a section — `phase:` together with `steps:` → the `<moment>:` section holding the same list. They never half-work.

### Selection

`fs dispatch` builds a tag set, selects every playbook whose `applies_when` it satisfies, and sorts by `order` then name. The tag set is:

| tag | source |
|---|---|
| `type=<T>` | `--type` — derived |
| `engine=<T>` | the plan's resolved `engine:` declaration — not a constant |
| `k` / `k=v` | `--tag`, repeatable — declared |

A declared `--tag` may not restate `type` or `engine` (those are set by the dispatch itself). `applies_when` matches presence and equality only — no ranges, no negation. An empty `applies_when` matches every dispatch; that is how the `worker` playbook loads for every dispatch.

So `fs dispatch --project P --type dev-task --tag code` loads the `code`-situated playbooks; `--type batch` loads `batch` (matching the derived `type=batch`), whose `creates: batch` declaration stamps the `batch` situation tag — and `batch`'s `exit:` section joins the same plan, since one file is one participant; a dispatch that names `--batch NODE` derives the `member` tag and loads `member` (both its `work:` and `exit:` sections); `--type research` alone loads nothing but `worker` — the `research` playbook matches the declared tag `research`, so `--tag research` is what selects it.

`--also NAME` forces a playbook into the plan that the tags did not select (a name that does not resolve is refused, and so is a `cap:`-only name — those load into the session, not a plan). `--without NAME` drops one the tags did select, and it has the last word over `--also`. An empty plan is legal and reported `plan: empty` — never hidden; with the shipped set it cannot happen, because `worker` always matches.

### The plan and the trace

The plan is produced once: printed in the brief, recorded on the cap's dispatch node as a `plan-recorded` event, and written as the trace's first line. A plan entry is `{name, moments, order}` — `moments` names the sections the playbook serves, so one entry can be both an `entry` and an `exit` participant. The trace is one JSONL file per dispatch at `~/.fs/traces/<project>.<node>.jsonl`, filed flat and append-only, never deleted at close: the project leads the name so a person browsing the directory recognises the file, and a dot separates the halves because both contain hyphens. `fs trace NODE [--project P] [--json]` renders it: the plan, then one line per step outcome, ending with `deviation: …` or `deviation: none` (the same comparison `fs close` refuses on). `--project` is an assertion, not a selector — the file is located by NODE alone (globbing `*.<node>.jsonl`), and a line whose project field disagrees is refused.

Each step line carries `ts`, `id` (`<dispatch>/<playbook>/<step>`), `dispatch`, `project`, `playbook`, `moment`, `step` (1-based), `kind`, `status`, `duration_ms`, and `detail` (the step body, truncated — **never** the command's output, so a check that prints a secret cannot write it into the trace). The `worker` field is present only on lines written once a worker node exists — the `loaded` lines and the exit lines. Statuses are `pass | fail | confirmed | loaded | skipped | refused`. There is deliberately no `followed`: the trace records that a playbook was loaded and a step ran, never that a worker obeyed.

A node with no trace prints `no trace was written for <node>` and exits 1 — never an empty timeline that reads as success.

### Scopes and `include`

Three KB scopes, resolved in this order: a project's `~/.fs/projects/<project>/kb/`, the global `~/.fs/kb/`, and the defaults shipped **inside the binary**. A playbook with the same `name` in a higher scope **overrides the fields and sections it declares and inherits the rest** from the same-named playbook below it — so a shadow that changes only the agent names only `agent:`, and the engine it runs under stays declared in one file. Declared and absent are not the same as empty: `applies_when:` with no terms is a standing declaration and does not inherit the terms below, while an absent `applies_when` does. A shadow that declares nothing but its name changes nothing and is **refused**. `fs kb get` marks which names a resolved playbook inherited. `fs kb list` prints each playbook's `scope` — `shipped`, `global` or the project name — beside its state.

The shipped default is a **scope**, not a copy of one, so nothing has to be installed for the playbooks to run. A file on disk participates only when it is an **override**: a body that differs from the stamp it carries, or an unstamped body that differs from the shipped default. A file that merely restates the default — current or stale — is **transparent** and the default below it is what resolves, so a copy can never shadow the binary it was written from, and a `git pull` that changes a playbook takes effect with no operator step. `fs kb reset NAME --yes` writes the shipped default to disk when you want one to edit, and puts an edited copy back.

`include: <name>` is a **separate** mechanism over the same scopes: it splices that playbook's steps at that position, resolved in the same scope order (project, then global, then the shipped default). Inheritance picks which fields and sections the playbook has; the splice then fills them — so a shadow whose `work:` says `- include: <its own name>` ends with the steps below exactly once, not twice. A cycle is refused with a message naming the chain. A project with no KB directory behaves exactly as global-only — absence is never an error.

### The shipped playbooks

These ship inside the binary and run from there; nothing has to be written to disk (see Knowledge center below):

| name | sections | applies_when | order | job |
|---|---|---|---|---|
| `cap` | cap | — | 10 | the cap's standing rules: hold the plan, elicit decisions, `fs pending` first, close what's ready, file a gap when dispatch refuses for want of a playbook. Carries the **order** rule as a step: after a worker reports and before you close, dispatch the review with `--reviews <the work's cap node>`, read it when it closes, disposition what it found — the gate that enforces the order fires too late to be the reminder. |
| `herdr` | cap | `engine=herdr` | 20 | herdr-operative rules: never wait, never poll — `fs pending` is the read. |
| `worker` | work | — | 10 | every worker's standing obligations: note your node, never invent one, file gaps as `kind: gap`, a claim without its command is not evidence. |
| `code` | entry, exit | `code` | 10 | one file for the code participant: its `entry:` is `test -f AGENTS.md`, the locked-doc grep, `"$FS_BIN" script check-specs.sh` (the locked specs still describe the code they name), and the asks a locked PRD names this deliverable and the acceptance check is stated; its `exit:` is the ask that the worker's node is complete, then the **review gate** — `[ -n "$FS_WITHOUT_REVIEW" ] || "$FS_BIN" reviews "$FS_NODE"`, a `check:` rather than an `ask:` because a record stands behind it and `--confirm` must not wave it through, placed before the teardown because the teardown removes the tree a review reads — then the declared teardown (branch, worktree, herdr workspace) and its postconditions. Declares `engine: herdr` because those `do` steps call it. |
| `plan` | work | `code` | 20 | the locked spec governs; state the plan; ask the cap when the spec is silent, never improvise. |
| `development` | work | `code` | 30 | the code bar: smallest structure, surgical diffs, own your orphans, refactor as its own step. |
| `tdd` | work | `code` | 40 | red → green → broader check; record why a test was skipped. |
| `review` | work | `code` | 50 | re-read your own diff against the bar before claiming done; record what it flags. |
| `independent-review` | work, exit | `type=review-task` | 10 | the *independent* reviewer's protocol — not `review`, which is self-review: this one loads into a `review-task` dispatch and reads somebody else's change, and the two keep out of each other's plans. Its `work:` has the reviewer read the change rather than the worker's summary, classify every finding *against the change* (blocks) or *discovered by the change* (a `kind: gap` node, never blocks — the test is the revert), and report without fixing and without deciding what blocks, because triage is the cap's. Its `exit:` is the termination condition: **every finding dispositioned** — `fixed`, `filed` or `accepted` with a reason. It is an `ask:`, not a `check:`; the comment says why no check can decide it. A review is **not itself reviewed**, structurally: `code`'s gate is selected by the declared `code` tag, which a `--type review-task` dispatch does not carry, so it cannot enter a review's plan. |
| `research` | work | `research` | 20 | sources named on the node; say what would falsify the answer. |
| `batch` | entry, exit | `type=batch` | 10 | the fan-out's own protocol, one file: its `entry:` is the one gate over the cards (`test -d .git`, `herdr worktree list --cwd .`, `"$FS_BIN" script check-parallel.sh $FS_CARDS`, ask: is the batch small enough to review), and its `exit:` is the one teardown (`"$FS_BIN" batch $FS_NODE` gate, then each member's branch, worktree and workspace removal and postconditions). Declares `creates: batch` and `engine: herdr`. |
| `member` | work, exit | `member` | 10 | one file for the whole member: its `work:` is what a member's worker is told (your worktree is the batch's, never touch main, your branch is merged by a dispatched integration), and its `exit:` is `"$FS_BIN" integrated $FS_NODE` plus the read ask. No teardown — the member's tree is the batch's. |
| `integration` | entry, exit | `type=integrate` | 10 | one file for the merge: its `entry:` is `"$FS_BIN" task get $FS_INTEGRATES --project cap --kind dispatch` plus the order and staged-changes asks, and its `exit:` is the suite question plus the verdict-recorded ask. No teardown, and no suite *check*: the command that runs a project's tests is the project's to declare, in a playbook of its own scope. |
| `spec` | work, exit | `spec` | 10 | the participant that keeps the design record true — the protocol `code`'s entry gate defers to. Its `work:` is revise in place, move the lock marker, say which acceptance criterion moved, ask before moving a non-goal; its `exit:` is `"$FS_BIN" script check-specs.sh` current plus the committed-and-named ask. Selected by the declared tag `spec`. |

### Knowledge center

```bash
fs kb add --name NAME --file PATH [--project P]   # add a playbook; --project writes to that project's KB
fs kb get NAME [--project P]                       # read a playbook
fs kb list [--project P]                           # every playbook, the scope it resolves from and what is on disk
fs kb prompt [--project P]                         # the cap's standing rules, as plain text
fs kb edit --name NAME --file PATH [--project P]   # update
fs kb remove NAME [--project P]                    # delete
fs kb diff NAME [--project P]                      # shipped default vs the copy on disk
fs kb reset NAME --yes [--project P]               # write the shipped default to disk
```

`fs kb prompt` is how a playbook reaches the agent that has to follow it: it concatenates every playbook with a `cap:` section whose `applies_when` the session's tags satisfy, in name order, each under a header naming the playbook, its moments, the scope the rules resolved from and the state and stamp of the copy beneath it. Plain text on stdout and nothing else, so it pipes straight into a prompt:

```bash
pi --append-system-prompt "$(fs kb prompt)"
```

No blank lines, deliberately: line breaks would split it into separate blocks. The headers are what make a stale prompt detectable — they say which playbooks went in and where each came from.

The playbooks above ship **inside the binary**. Nothing installs them: `~/.fs/kb/` is for your own overrides, and a fresh home runs the full set with the directory absent.

```bash
fs kb reset NAME --yes                 # write the shipped default to disk, to edit
fs kb diff NAME                        # shipped default vs the copy on disk
```

A copy on disk participates only when it is an **override** — its body differs from the stamp it carries, or it carries no stamp and differs from the shipped default. A copy that restates the default is *transparent*: it is ignored, and the shipped default below it runs. That is what makes the shipped default a scope rather than a seed — a copy can never pin the binary, and a `git pull` that changes a playbook is live with nothing to re-run. Each written copy carries `# fs-default: sha256=<hash of the default body>`, which is how the two are told apart:

| state | meaning | runs? |
|---|---|---|
| `shipped` | no file on disk; the binary's default runs | yes |
| `default` | a copy identical to the shipped default | no — the default runs |
| `edited` | a copy that differs from the stamp it carries | yes |
| `stale` | the copy was stamped from an older default | only if you also edited it; otherwise the default runs |
| `local` | no shipped default with that name — hand-written | yes |

`fs kb list` answers both questions in one row: the `scope` a playbook resolves from (`shipped`, `global` or the project name) and the state of the copy beneath it. A row whose `scope` is `shipped` and whose `state` is `stale` is the case worth learning — the file on disk has fallen behind, it is not what runs, and the binary's default is. Nothing refuses on it, so it is reported rather than silent: `fs kb diff NAME` shows the drift, and `fs kb reset NAME --yes` puts the copy back to the default when you want to start from it again. Nothing is ever written to `~/.fs/kb/` except by `kb add`, `kb edit` and `kb reset`; a playbook the binary does not ship is written by hand or with `kb add`. `fs kb list` also reports `used_by` — which tags load a playbook and, for a dispatch playbook, which moments (`prompt: …` for a playbook with a `cap:` section, `dispatch: … (entry, exit)` otherwise, `none` when nothing selects it).

## Dispatch and close-out

```bash
# Prepare a brief only — the response carries the herdr line to run.
# --tag declares situation terms; --also/--without edit the plan; each gate has
# its own override: --confirm for a failing entry check, --allow-unresolved for
# an unresolved dispatch still open.
fs dispatch --project P --type T --goal "Implement auth" [--tag K[=V]] [--also NAME] [--without NAME] [--confirm] [--allow-unresolved]

# Prepare and deliver: split a sibling pane of your own, start the worker, send
# the brief, record it. --worktree names the herdr worktree workspace the
# dispatch runs in, so close can remove it; --worktree-new creates one instead.
# --cards names the batch's cards (comma-separated, repeatable); every check
# step sees them as $FS_CARDS. --integrates names the member cap node an
# integration merges; --reviews names the cap node whose change this dispatch
# reads. They are not the same link: an integration names what it merges and
# lands after it, a review names what it reads and runs before the dispatch it
# is about closes. A dispatch may carry either, both, or neither.
fs dispatch --deliver --project P --type T --goal "Implement auth" [--cards CARD[,CARD…]] [--integrates MEMBER_NODE] [--reviews CAP_NODE] [--batch BATCH_NODE] [--worktree WS] [--worktree-new]

# Is a batch complete? Exit 0 iff every member has a done integration naming it;
# exit 1 naming the unintegrated ones. A batch with no members is refused.
fs batch BATCH_NODE

# Was this dispatch's change reviewed? Exit 0 iff a done dispatch carries
# reviews: <this node>; exit 1 saying which it found — nothing names it, or one
# does and is not done yet. A node that does not exist, and one that is not a
# dispatch, are refused as themselves rather than as the same absence. This is
# what code's exit gate runs, as "$FS_BIN" reviews.
fs reviews CAP_NODE

# Close out a dispatch: run the exit phase of the plan it recorded, then close
# its pane, then mark the node done. Refuses while the trace does not account
# for every step of that plan, naming the step. --confirm answers the ask steps.
# --without-review is the review gate's own override, and it requires a reason:
# the gate is a check, which --confirm cannot answer, and the reason is recorded
# as a decision on the node so a skipped review is visible rather than silent.
# The reason is trimmed, so whitespace is refused with the empty case — and so
# is a waiver on a close that faced no gate at all (abandoned, or a plan with no
# review gate in it): recording one there would claim a skip that never happened.
fs close --node CAP_NODE [--worker P:NODE] --decision "verdict" [--confirm] [--without-review "why"]

# Close a dispatch without a verdict. A delivered one still tears down its pane
# and worktree.
fs close --node CAP_NODE --abandoned --reason "why"

# What is waiting on you: every open dispatch, classified, with the cap's own
# address and liveness and every open report. Writes nothing
fs pending

# The cap's address: this pane becomes the one herdr routes a report to.
# Refused while a live cap holds it; a take-over of a dead one requires
# --reconstructed "what you read", recorded on the claim.
fs cap claim [--reconstructed "WHAT YOU READ"]

# The address and whether herdr still has it: exit 0 with a live cap, 1 without
fs cap who

# A worker's report: the event is written first and always, then the live cap
# is notified through herdr. --blocked|--question|--done notify; no kind is
# silent. A record with no cap to notify exits 0 saying "recorded, not notified"
fs report PROJECT:NODE --line "ONE LINE" [--blocked|--question|--done]

# The cap's reply, resolved through that dispatch's delivery record and sent
# through herdr. A message, not a record: the decision lands on the node
fs tell PROJECT:NODE "TEXT"
```

`fs dispatch` refuses to prepare while an earlier dispatch node is still unresolved (`pending`/`active`/`blocked`); `--allow-unresolved` is that gate's only override. `--confirm` is a different gate's override — it proceeds past a failing entry `check` — and neither waves the other through.

`--deliver` splits a **sibling pane of your own** — `pane split --current --direction right --no-focus`, recorded as `pane_id`, `tab_id`, `project`, `node`, and the task `type`. The split is anchored to the pane you run in, so the worker lands in *your* workspace and your tab. The worker's pane is the only thing close-out tears down by machinery: the tab in the record is yours.

Start every turn with `fs pending`; never wait on one and never poll with a sleep. It answers "what is waiting on me" in one read:

| state | meaning | what to do |
|---|---|---|
| `ready` | the worker's node is done | read the node, then `fs close` |
| `blocked` | the node is not done and herdr reports the agent `blocked` — parked on a prompt or a modal nothing will dismiss | it stopped and only a person can move it — go look, and never answer an approval prompt for it |
| `running` | the node is not done, herdr answered, and the agent is neither `blocked` nor `done` — working *or* idle, and a value herdr itself cannot place: `unknown`, an unrecognised word, an empty one | let it work |
| `unknown` | the node is not done and herdr could not be asked at all, so nothing about this worker was read — it may be working, dead, or there may be no herdr on this machine | nobody measured it — read the node, and go look before acting on the state |
| `unseen` | the node is not done but herdr reports the agent `done` | finished unseen — go look |
| `gone` | the node is not done and herdr has no such agent | the worker died; needs the user |
| `unlinked` | no delivery record, or one written before it carried a node | close it with `--abandoned`, or `--worker` for an old record |

herdr is a hint about *when to look*, never the truth about whether the work is done — `ready` and `blocked` are the states to act on. `running` is deliberately the wide one: `idle` is not `blocked`, because a worker between two tool calls is thinking and not stuck, and neither is an agent state herdr reports as `unknown` or one it cannot place at all — a state that cried wolf would be one the cap learns to ignore. `unknown` is the one state reached by *failing* to ask herdr — `blocked`, `running`, `unseen` and `gone` each follow an answer it gave (and `running` still asserts an agent exists), while `ready` and `unlinked` are settled without asking at all: `ready` because a done node is done however herdr reads, `unlinked` because there is no node to read.

`fs pending` also answers the other half: it carries the cap's `pane_id`, `tab_id`, `agent`, `claimed_at` and `state` (`none`/`live`/`dead`/`unknown`), and each entry's `reports`. `none` and `dead` do not read alike — an unclaimed scope is free to take, and a claim whose pane is gone is a cap nobody holds. `unknown` means herdr could not be asked — the same word, and the same fact, an entry carries when herdr could not be asked about its worker — which is never a take-over's proof.

### The cap's address

`cap` is a scope name, so the scope had no identity: any session reading the cap's rules was a cap, two could hold it with neither able to tell, and a dead cap read exactly like a quiet one. `fs cap claim` records the pane in `$HERDR_PANE_ID` — with its tab, the agent herdr reports there, and the time — as the cap's **address**. It is refused while the registered pane is still live, naming it and when it was seen. When herdr no longer has that pane the claim is a **take-over**: it records the address it replaced and its last-seen time, and requires `--reconstructed "<what you read>"`. A session with no `HERDR_PANE_ID` cannot claim, and is told why.

### Reporting, and the guard

A worker reports through the store: `fs report <project>:<node> --line "<one line>"` with `--blocked`, `--question` or `--done`. The event is written **first and always** — durable, addressed to a node and never to a cap, so it survives the cap's death — and only then is the live cap notified through herdr. One command holds that order; no flag does either half alone. The three kinds notify and a kindless report records silently for whoever reads, and there is deliberately no flag to force a ping for a kind that does not. **A failed notification is not a failed report:** with no cap, a dead one, or a herdr that refuses, the command exits 0 saying `recorded, not notified`, and the report is there for whoever claims next. A `--blocked` report also blocks the node, with the line as its reason, so every reader that already handles a block is untouched.

The report is a pointer, never the content: the node remains the record and the cap reads the node.

`fs` holds the guard, not a playbook, so a worker cannot reach a coordinating act by not knowing a rule: a pane fs holds a delivery record for is refused **coordinating acts** — any cap-scope write, `dispatch`, `close`, `tell` — naming the node that pane worked, while `fs report` from that same pane succeeds. A pane fs holds no record for is unclaimed rather than a worker, and reads are never refused. It is a **discipline guard and not a security boundary**, and says so: `HERDR_PANE_ID` is an environment variable any process can unset.

### Entry gate

`fs dispatch` runs the plan's `entry` steps at the project root, fail-fast, appending a line to the trace per step. A failing `check` refuses before any node is created (the attempt is rolled back and marked done with the refusal as its decision); `--confirm` records the override as a decision naming the check and its output. An unanswered `ask` is recorded `skipped` and confirmable at close.

Every entry check runs with the dispatch's own inputs in its environment, so a gate can ask about *this* dispatch and not only about the world:

| variable | value |
|---|---|
| `FS_PROJECT` | the resolved project |
| `FS_TYPE` | the task type |
| `FS_GOAL` | the goal text |
| `FS_CARDS` | the `--cards` value, comma-separated; empty when none was given |
| `FS_INTEGRATES` | the `--integrates` value — the member cap node this integration merges; empty when none |
| `FS_BIN` | the running binary's own path. A step that runs fs must say `"$FS_BIN"`: the binary is installed into `~/.fs` and is not on PATH, so a bare `fs` exits 127. It is also how a step runs a script the binary ships — `"$FS_BIN" script <name> [args…]` — since the scripts are read out of the binary and there is no directory to address. Empty if the path could not be read, which a step must treat as missing |

`FS_CARDS` and `FS_INTEGRATES` are exported empty rather than left absent, so a check can tell "none given" from "no environment".

### Delivering

`fs dispatch --deliver` delivers the brief itself: it splits the pane, creates the worker's node in the target project **before** the agent starts (the brief has to name it — *note your work on `<project>:<node>`; do not create another*), starts an agent of the plan's resolved kind and with the plan's resolved arguments (the shipped `worker` declares `agent: pi` and no arguments, which is why a project that wants `--permission-mode auto` declares it there) named `dispatch-<type>-<node>`, sends the brief, appends a `loaded` line per `work` playbook, records the binding as a `delivery-recorded` event, and marks the cap node `active`. A failure after the node exists rolls it back — a delivery that did not happen leaves no `loaded` lines, so close refuses it.

The brief carries: the goal, the plan, the `work:`-section playbooks' `say` steps as **Obligations** and `use` steps as **Skills** (in plan order, under a heading stating they are hints and that no skill is required), the entry gate's checklist with each check's output, and the locked docs. No worker obligation is a Go string literal — every one comes from a playbook.

### Closing out

`fs close` is the gate that makes an omission fail. In order it refuses when: the node is not a dispatch; no `delivery-recorded` event exists (or `--abandoned` is missing `--reason`); the worker's node is missing or not `done`; `--decision` is missing; or the trace does not account for every step of the plan — every entry step, every work playbook's `loaded` line, every exit step — naming the playbook, the step and the kind. A missing trace file is a refusal, never a pass.

It then runs the plan's **exit section** in declared order, then closes the recorded pane, then marks the cap node done with the verdict. The worker's node comes from the delivery record — `--worker <project>:<node>` is optional and, when given, must match it.

Exit steps get the closing dispatch's own inputs in their environment:

| variable | value |
|---|---|
| `FS_PROJECT` | the worker's project |
| `FS_TYPE` | the task type |
| `FS_NODE` | the cap node being closed |
| `FS_WORKER` | the worker's node as `<project>:<node>`; empty without one |
| `FS_CARDS` | empty — a close has no batch — but present, not missing |
| `FS_INTEGRATES` | the member this integration merges; empty when it merges none |
| `FS_WITHOUT_REVIEW` | the `--without-review` reason; empty when the flag was not given. A step that gates on a review stands down on a non-empty value, and on empty must run its check |
| `FS_PANE` | the pane the delivery opened |
| `FS_WORKTREE` | the worktree workspace the dispatch ran in; empty without one |
| `FS_WORKTREE_PATH` | that worktree's checkout path; empty without one |
| `FS_BRANCH` | the branch the delivery created its worktree on; empty when this delivery created no worktree. A step can then tell "this branch was not mine to remove" from "the branch I was responsible for is still here" without re-deriving the branch from a tree it may have just removed |
| `FS_BATCH` | the batch node this dispatch is a member of; empty outside a batch — the marker `code`'s teardown defers on |
| `FS_BIN` | the running binary's own path, the same contract the entry gate gets. It is what the review gate runs, because fs is not on PATH and `"$FS_BIN"` is — and it is how an exit step runs a shipped script, `"$FS_BIN" script <name>` |

Exit `check` steps run in the tree the dispatch worked in (the worktree, or the project root when there was none or the tree is gone); exit `do` steps run in the project root, so an action that removes the worktree does not stand inside it.

The teardown is **declared, not machinery**: `fs close` closes the pane (every dispatch has one), but removing a worktree, deleting a branch and closing a herdr workspace are `do` steps in the exit playbook — reviewable, diffable, per-type — run by the gate and reported, not owned by it. `--abandoned --reason <why>` skips the checks and asks but **not** the declared actions: a delivered dispatch closed this way still tears down its pane and worktree, and records `abandoned after delivery to <project>:<node>: <reason>`.

The gate can check that the worker's node exists and is `done`, and that the trace accounts for the plan. It **cannot** check that the cap actually read that node; reading it is still the cap's job.

**Batches.** A batch is a dispatched `kind: batch` node that holds a fan-out: `fs dispatch --project P --type batch --cards a,b,c` runs `batch`'s `entry:` section (git, the worktree listing, the cards' disjointness, the reviewability ask) and leaves the batch node as the parent. Each member is an ordinary dispatch whose cap node is the batch's child:

```bash
fs dispatch --project P --type dev-task --tag code --batch <batch-node> --worktree-new --deliver
```

The member works in its own worktree; its own close runs `member`'s `exit:` section (`"$FS_BIN" integrated $FS_NODE` plus the read ask) and defers its teardown — `FS_BATCH` is set, so `code`'s exit teardown steps no-op and the tree survives the member's close. Each member is merged by a dispatched integration, one at a time. When `fs batch <batch-node>` exits 0 — every member has a done integration naming it — the cap closes the batch, and `batch`'s `exit:` section tears each member's branch, worktree and workspace down, declared once.

A batch's cards declare their files so the fan-out can be checked mechanically:

```markdown
## Files
src/flagship/cmd/main.go
internal/registry/registry.go
```

`"$FS_BIN" script check-parallel.sh <card…>` intersects the cards' declared sets and refuses an overlap — or a card with no file list. `batch`'s `entry:` section puts it in the entry gate, and that gate reads the cards from the dispatch itself: `fs dispatch --cards` exports them as `$FS_CARDS`.

A shipped playbook may only name what the binary ships — through `"$FS_BIN"`, the running binary's own path: an `fs` subcommand, or a script the binary carries, run as `"$FS_BIN" script <name> [args…]`. It is one rule with one exception, because a playbook in the binary runs in **every** project: `check-parallel.sh` and `check-specs.sh` used to be addressed as `bin/…`, which resolves in one repo and exits 127 everywhere else, and the materialized `~/.fs/scripts/` copy that replaced them is gone too — `fs script` reads the body out of the binary and runs it, so there is nothing on disk to go stale. A generic POSIX tool is the other thing a step may name; a path into a particular repo and a bare `fs` are both the same failure, since the binary is installed into `~/.fs` and not into PATH. `fs script` exits with the **script's** status, not an envelope of its own, which is what lets a gate run this way still fail. `tests/playbooks` enforces the rule.

## Task status

Status is derived by replaying events: a task's status is the `to` of its last `status-changed` event. There are exactly four.

| status | meaning |
|---|---|
| `pending` | prepared but **not delivered** — unresolved, revisit it |
| `active` | dispatched; a worker is on it |
| `blocked` | waiting on the user |
| `done` | closed, with a closing decision |

Never mark an unfinished task `done`. Abandoning a dispatch means marking it `done` with a decision that says it was never delivered: the record then shows the drop instead of hiding it.

## Querying

```bash
# Task tree with statuses, goals, decisions, knowledge
fs status [--project ID]

# Every task not done, across every scope — reads the store's scopes, not the
# registry, so it works after a registry wipe and includes the implicit cap scope
fs unfinished

# Every kind=gap node — the backlog — open ones first, with found_by
fs gaps

# Full-text search across all event payloads (FTS5)
fs query "search term" [--project ID]

# Event log (full history)
fs log [--project ID] [--node NODE_ID] [--type TYPE]
```

### Reading events

`fs status` and `fs query` answer most questions with far less output — reach for them first. `fs log` is the raw history, one JSON envelope per event, meant for machines; filter it with `jq`:

```bash
fs log --project skills --node t-7609aede | jq -r '
  .data.events[] | "\(.timestamp)  \(.type)  \(.node_id // "-")\n    \(.payload)"'
```

## Event model rules

These are non-negotiable:

1. **Never write to `~/.fs/store.db` or `~/.fs/registry.db` directly** — only use `fs` commands
2. **Events are immutable** — no undo; correct with compensating events (update status back, edit goal again)
3. **Always record decisions** — when making a choice, use the `--decision` flag on `fs task update`
4. **Always record knowledge** — when learning something useful, use `fs task knowledge`
5. **Update status honestly** — active when starting, done when finished, blocked when stuck

## Event types

| Type | Produced by | Payload |
|---|---|---|
| `project-created` | `project create` | `{name, root_path}` |
| `task-created` | `task add` | `{goal, kind, found_by}` |
| `status-changed` | `task update/block/unblock` | `{from, to}` |
| `decision-recorded` | `task update --decision` | `{summary}` |
| `knowledge-added` | `task knowledge` | `{summary}` |
| `task-blocked` | `task block` | `{reason, check}` |
| `task-unblocked` | `task unblock` | `{}` |
| `metadata-changed` | `task edit` | `{field, old_value, new_value}` |
| `delivery-recorded` | `dispatch --deliver` | `{pane_id, tab_id, agent, engine, project, node, type, worktree, worktree_path}` |
| `plan-recorded` | `dispatch` | `{plan, tags, engine, agent, agent_args}` — the ordered playbooks, the tag set that selected them, and the resolved realization |
| `report-recorded` | `report` | `{kind, line}` — the worker spoke; what the work is stays on the node |
| `cap-claimed` | `cap claim` | `{pane_id, tab_id, agent, claimed_at, taken_over_from, taken_over_at, reconstructed}` — the cap scope's address. **Scope-level: no `node_id`** |

Every event carries: `id` (ULID), `timestamp` (UTC), `project_id`, `node_id`, `parent_node_id`, `commit_sha`. `node_id` is null only for `project-created` and `cap-claimed`, which address a scope and not a node — a scope-level event is skipped by replay before it can materialise a node nobody created. `commit_sha` is the git HEAD of the project the event is about — the resolved project's registered `root_path` (`status`/`query`/`log`/`task *`), the dispatched-to project (`dispatch`), the worker's project (`close`), or the root being created (`project create`); the cwd's HEAD only when no project is referenced (`unfinished`), and null when that is not a git repo.

## Data locations

Everything lives under `~/.fs/` — no project directory is written to:

- `~/.fs/store.db` — event store: every project's events, partitioned by `project_id`
- `~/.fs/registry.db` — global project registry
- `~/.fs/kb/` — global knowledge center (YAML playbooks)
- `~/.fs/projects/<project>/kb/` — a project's own playbooks, shadowing global by name
- `~/.fs/traces/<project>.<node>.jsonl` — one dispatch's trace (flat; project leads the name)
- `~/.fs/logs/fs.log` — the running log (JSON lines, rotating; see The running log)

## The running log

Every `fs` invocation appends one line per notable step to `~/.fs/logs/fs.log` — JSON lines (slog's `time`/`level`/`msg` plus the command's own fields such as `command`, `project`, `node_id`). It is the same JSON the process writes to stderr, teed to a file that outlives the pane, so a command run with stderr discarded still leaves its record. `project` is a field on each line, never a directory split. Read it with the tools that already filter it:

```bash
tail -f ~/.fs/logs/fs.log                             # watch it
jq -c 'select(.project=="skills")' ~/.fs/logs/fs.log # one project's lines
grep '"command refused"' ~/.fs/logs/fs.log            # every refusal, with its reason
```

The level defaults to INFO; `-v`/`--verbose` turns it up to DEBUG (the flag works on every subcommand), and `FS_LOG_LEVEL=debug|info|warn|error` is the environment fallback, the flag winning. An unparseable level is refused, naming the accepted values. The file rotates at 4 MiB keeping three files (`fs.log`, `fs.log.1`, `fs.log.2`).

**It never gates.** Nothing in fs reads the log back to make a decision; a write failure (an unwritable `~/.fs/logs`, a full disk) produces at most a WARN on stderr and never fails the command. This is the running log's whole distinction from the **trace** (`~/.fs/traces/<project>.<node>.jsonl`): the trace records what a dispatch ran and a missing trace line refuses a close; the log records what a process did and its absence refuses nothing. `fs log` reads *events*, not this file — the two are not each other.

## Project identity

One store holds every project, partitioned by `project_id`. A command targets `--project NAME` if given, else the registered project whose `root_path` owns the working directory, else the directory name. So after `fs project create --name demo`, every command in that repo targets `demo` — and `--project` reaches any registered project from anywhere. See [references/fs-advanced.md](references/fs-advanced.md).

## Example session

```bash
# Discover projects in a fresh session
fs project list

# Resume work on a project
fs status --project myapp
fs query "auth" --project myapp

# Create and work a task
fs task add --goal "Add OAuth2 flow" --project myapp
# → node_id: t-abc12345

fs task update t-abc12345 --status active --project myapp

# Record what you learned
fs task knowledge t-abc12345 \
  --summary "PKCE required for public clients per RFC 7636" --project myapp

# Finish with a decision
fs task update t-abc12345 --status done \
  --decision "Used authorization code + PKCE, no implicit grant" --project myapp
```

## References

**The locked contract this skill implements:** [`.agents/specs/cap-role.md`](../../.agents/specs/cap-role.md) — the claim, the report and the guard: `fs cap claim|who`, `fs report`, `fs tell`, and why the guard sits in `fs` rather than in a playbook. Read it before changing any of them.

**Read [references/fs-advanced.md](references/fs-advanced.md) when you need:**
- Full event payload schemas
- FTS5 query syntax and ranking
- Playbook YAML format details and the per-moment kind table
- Project registry schema and behavior
- Multi-project workflows and KB scopes
- Store integrity and WAL behavior
