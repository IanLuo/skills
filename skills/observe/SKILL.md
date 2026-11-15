---
name: observe
description: Read a local project's goal status from Observe's own output instead of grepping specs — which goals are measured, violated, unverified, or unmeasurable, and the evidence line behind each verdict. Use when asked what the status of a project is, which goals are unmeasured, whether an acceptance criterion is done, what a change might break, or to check work against a locked spec before calling it complete, in a repository that has .agents/specs/ or docs/spec.md and the observe binary. Do NOT use to work on the Observe codebase itself (read its AGENTS.md), to write or lock requirements (specs skill), or to review a diff against its spec (review-task).
metadata:
  audience: personal
  domain: tooling
---

# observe

Observe answers one question: which of this project's stated goals hold, and on
what evidence. Ask it before grepping a spec, and before claiming a task is done.

## Run it

```bash
observe -report -no-open -cache '' <repo> > /tmp/state.json
```

One JSON document, then exit. No server, no browser. It exits **1 when any check is
violated** and prints the count on stderr; the JSON is complete either way, so read the
status first and the document second. Drop `-cache ''` to reuse the
stored cache; the tool is one repository per run.

`observe <repo>` (without `-report`) is for a human at a browser. An agent wants
the JSON.

## What is in the document

| Path | Meaning |
|---|---|
| `.data.commit` | the commit analysed |
| `.data.goals[]` | one per requirement bullet, with `.label`, `.heading`, `.spec_path`, `.reading` |
| `.data.goals[].checks[]` | `.id`, `.status`, `.subject`, `.assertion`, `.measures`, `.reason`, `.evidence[]` |
| `.data.goals[].verification` | `verified` when a test names a check via `covers:` |
| `.data.flows[]` | request routes; `.stop` is a code (`no-store-call`, `factory-argument`, …), never guessed past |
| `.data.structure`, `.data.schema` | modules and import edges; tables and foreign keys from migrations |
| `.data.diagnostics` | what could not be read — not goals, and not failures of the project |

Filter it. The raw document is 170–270 KB; never paste it whole.

## Rules for reading a verdict

- **A `violated` check outranks everything, including a missing one.** A goal with
  no check is a gap; a violated goal failed. Say so first, quote the evidence, and
  treat "done" as blocked until it is fixed or the check is corrected. A non-zero
  exit from the report is that same failure, not a tool error: do not retry it.
- **`unknown` plus its reason is a complete answer.** Report it as it stands. Never
  replace it with an inference from the code.
- **`pass` on a presence check** says a name exists. It says nothing about behaviour.
- **`verification: verified`** means a test names the check (`covers:`). That is
  coverage, never a passing result.
- **`measures: behaviour`** is the only verdict about a run. It says that run passed,
  not that the goal holds.
- **Quote the evidence**, `path:line_start`, or the `reason` verbatim. A paraphrase
  is a new claim.

## Answer the usual questions

```bash
# How much is measured at all
jq -r '"\([.data.goals[]|select((.checks|length)==0)]|length) of \(.data.goals|length) goals have no check"' /tmp/state.json

# Goals with no check, by name
jq -r '.data.goals[] | select((.checks|length)==0) | .label[0:90]' /tmp/state.json

# Violated checks with the line that proves it
jq -r '.data.goals[] | .checks[]? | select(.status=="violated")
  | "\(.id) — \(.reason) — \([.evidence[]|"\(.path):\(.line_start)"]|join(", "))"' /tmp/state.json

# Every unknown, with the reason shortened
jq -r '.data.goals[].checks[]? | select(.status=="unknown") | "\(.id)\t\(.reason[0:70])"' /tmp/state.json

# Which goals a test claims to cover
jq -r '.data.goals[] | select(.verification=="verified")
  | "\(.label[0:40]) <- \([.verification_evidence[].path]|join(", "))"' /tmp/state.json

# Where request flows stop, by code
jq -r '.data.flows[] | select(.stop) | .stop' /tmp/state.json | sort | uniq -c
```

## Producing the report — in that project, not from yours

Observe never runs a test, and neither should you from outside: a report needs that
project's own tests and the services they talk to. Work inside the project, with its
toolchain and its environment.

1. **Ask what is needed.** Every unknown check whose `assertion` is `passes` names the
   test it wants. The name is `subject` minus the leading `test `:

   ```bash
   jq -r '.data.goals[].checks[]? | select(.assertion=="passes" and .status=="unknown")
     | "\(.id)\t\(.subject|ltrimstr("test "))"' /tmp/state.json
   ```

2. **Find that project's own test command** — its `package.json` scripts, `go test`, or
   its README. Produce JUnit XML with the runner it already uses:

   | runner | flag |
   |---|---|
   | vitest | `--reporter=junit --outputFile=junit.xml` |
   | jest | `--reporters=jest-junit --outputFile=junit.xml` |
   | go test | `gotestsum --junitfile junit.xml -- ./...` |

3. **Use its services or make them disposable.** Read the suite's `beforeAll` and
   `beforeEach` first: a database-backed suite often runs `TRUNCATE … CASCADE`, which
   destroys whatever `DATABASE_URL` points at. Never aim that at a shared or
   development database; start a throwaway instance, or ask the user which one is safe.

4. **Hand it over.** The running instance takes a path; nothing restarts:

   ```bash
   token=$(curl -s http://127.0.0.1:8080/ | sed -n 's/.*observe-token" content="\([^"]*\)".*/\1/p')
   curl -s -X POST http://127.0.0.1:8080/api/test-report \
     -H "X-Observe-Token: $token" -d '{"path":"/abs/path/junit.xml"}'
   ```

   For a one-shot run, `observe -report -test-report junit.xml <repo>` does the same
   without a server. A report that cannot be read, or is not JUnit XML, is refused and
   the previous verdicts stand.

5. **Re-read the verdicts**, and say what moved and what did not. A check whose test
   did not appear stays `unknown` — the report has no test of that name.

## When a goal has no verdict

A verdict needs a declaration in a locked document. Prose cannot produce one: a
match by words was measured at 57 nonsense hits out of 99 goals. Two ways, both an
edit plus a re-lock through the `specs` skill:

1. **A check block** in the goal's bullet:

   ```check
   store-exists: module internal/store exists
   ```

   Subjects `symbol`, `module`, `file`; assertions `exists`, `absent`,
   `imports <module>`, `not-imports <module>`, `has-test`.

2. **A test the spec names**:

   ```check
   ac-2-test: test Postgres commerce store > refuses a second redemption passes
   ```

   Then give the report: `observe -report -test-report junit.xml <repo>`. The name
   must match the report exactly — vitest prefixes the `describe` chain, so take the
   name from a run (`vitest list`) rather than writing one from memory.

## Traps

- **A report goes stale on every commit.** After HEAD passes the report's run time,
  the checks read `unknown, older than <time>`. Regenerate rather than explain it.
- **Languages:** Go, TypeScript, `.tsx`. Anything else is `unknown` by design.
- **Missing report is not missing tests:** `unknown — no test report was supplied`
  means exactly that.
- **Do not add a check for something the tool cannot see.** A check that names a
  guessed symbol turns a real goal into a permanent `violated`.
- **Unknown reasons name their subject**, so grouping the full reason fragments into
  one group per check. Truncate as above, or filter on a prefix such as `no test report`.
- **`.data.diagnostics` noise** such as `package.json: outside the V1 language set`
  is pre-existing and not caused by any change you made.

## Prove you used it

Quote the check id, its status, and the evidence line or the unknown reason:
`ac-2-test pass — the report at … records … as passed`, or
`store-exists unknown — the report has no test named …`. If you cannot quote one,
you have not checked the goal.
