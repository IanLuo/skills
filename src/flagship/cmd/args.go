// The argument shape of every subcommand, and the one pass that enforces it.
//
// flagVal/hasFlag match raw strings and never check names, so before this table
// a typo'd flag parsed as nothing and the command reported success. checkArgs
// refuses anything the subcommand does not accept, and answers --help before
// any of the command's work begins.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/flagship-dev/flagship/internal/command"
)

// cmdSpec is what one subcommand accepts: the flags it knows (each mapped to
// whether it takes a value), how many positional arguments it takes, and the
// usage line --help prints.
type cmdSpec struct {
	usage       string
	flags       map[string]bool // flag name → takes a value
	positionals int             // how many positionals the subcommand accepts
	// address marks a subcommand whose first positional may be a `PROJECT:NODE`
	// address rather than a bare node — the form nodeArg splits, and the only
	// place a refusal can learn a project from a positional. It is declared here,
	// beside the flags and the count, because it is part of the argument shape.
	address bool
}

var cmdSpecs = map[string]cmdSpec{
	"project create": {
		usage: "Usage: fs project create [--name NAME] [--root PATH]",
		flags: map[string]bool{"--name": true, "--root": true},
	},
	"project list": {usage: "Usage: fs project list"},
	"project get":  {usage: "Usage: fs project get NAME", positionals: 1},

	"task add": {
		usage: "Usage: fs task add --goal GOAL [--parent NODE_ID] [--kind work|dispatch|gap] [--found-by PROJECT:NODE] [--check \"SHELL COMMAND\"] [--severity severe|defect|prose] [--project PROJECT_ID]",
		flags: map[string]bool{"--goal": true, "--parent": true, "--kind": true, "--found-by": true, "--check": true, "--severity": true, "--project": true},
	},
	"task update": {
		usage:       "Usage: fs task update NODE_ID --status STATUS [--decision TEXT] [--project PROJECT_ID]",
		flags:       map[string]bool{"--status": true, "--decision": true, "--project": true},
		positionals: 1,
		address:     true,
	},
	"task get": {
		usage:       "Usage: fs task get NODE_ID [--project PROJECT_ID] [--kind work|dispatch|gap]",
		flags:       map[string]bool{"--project": true, "--kind": true},
		positionals: 1,
		address:     true,
	},
	"task edit": {
		usage:       "Usage: fs task edit NODE_ID [--goal GOAL] [--kind work|dispatch|gap] [--severity severe|defect|prose] [--project PROJECT_ID]",
		flags:       map[string]bool{"--goal": true, "--kind": true, "--severity": true, "--project": true},
		positionals: 1,
		address:     true,
	},
	"task merge": {
		usage:       "Usage: fs task merge NODE_ID --into SURVIVOR [--reason TEXT] [--project PROJECT_ID]",
		flags:       map[string]bool{"--into": true, "--reason": true, "--project": true},
		positionals: 1,
		address:     true,
	},
	"task block": {
		usage:       "Usage: fs task block NODE_ID --reason REASON [--check \"SHELL COMMAND\"] [--project PROJECT_ID]",
		flags:       map[string]bool{"--reason": true, "--check": true, "--project": true},
		positionals: 1,
		address:     true,
	},
	"task unblock": {
		usage:       "Usage: fs task unblock NODE_ID [--project PROJECT_ID]",
		flags:       map[string]bool{"--project": true},
		positionals: 1,
		address:     true,
	},
	"task knowledge": {
		usage:       "Usage: fs task knowledge NODE_ID --summary TEXT [--project PROJECT_ID]",
		flags:       map[string]bool{"--summary": true, "--project": true},
		positionals: 1,
		address:     true,
	},

	"status": {
		usage: "Usage: fs status [--project PROJECT_ID]",
		flags: map[string]bool{"--project": true},
	},
	"query": {
		usage:       "Usage: fs query SEARCH_TERM [--project PROJECT_ID]",
		flags:       map[string]bool{"--project": true},
		positionals: 1,
	},
	"log": {
		usage: "Usage: fs log [--project PROJECT_ID] [--node NODE_ID] [--type TYPE]",
		flags: map[string]bool{"--project": true, "--node": true, "--type": true},
	},

	"kb add": {
		usage: "Usage: fs kb add --name NAME --file PATH [--project PROJECT_ID]",
		flags: map[string]bool{"--name": true, "--file": true, "--project": true},
	},
	"kb get":    {usage: "Usage: fs kb get NAME [--project PROJECT_ID]", flags: map[string]bool{"--project": true}, positionals: 1},
	"kb list":   {usage: "Usage: fs kb list [--project PROJECT_ID]", flags: map[string]bool{"--project": true}},
	"kb diff":   {usage: "Usage: fs kb diff NAME [--project PROJECT_ID]", flags: map[string]bool{"--project": true}, positionals: 1},
	"kb reset":  {usage: "Usage: fs kb reset NAME --yes [--project PROJECT_ID]", flags: map[string]bool{"--yes": false, "--project": true}, positionals: 1},
	"kb edit":   {usage: "Usage: fs kb edit --name NAME --file PATH [--project PROJECT_ID]", flags: map[string]bool{"--name": true, "--file": true, "--project": true}},
	"kb remove": {usage: "Usage: fs kb remove NAME [--project PROJECT_ID]", flags: map[string]bool{"--project": true}, positionals: 1},
	"kb prompt": {usage: "Usage: fs kb prompt [--project PROJECT_ID]", flags: map[string]bool{"--project": true}},

	"dispatch": {
		usage: `Usage: fs dispatch --project PROJECT_ID --type TASK_TYPE --goal GOAL [--cards CARD[,CARD...]] [--integrates MEMBER_NODE] [--reviews CAP_NODE] [--fixes CAP_NODE]... [--batch BATCH_NODE] [--tag K[=V]] [--also NAME] [--without NAME] [--confirm] [--allow-unresolved] [--deliver] [--worktree WORKSPACE_ID]

The plan is the playbooks whose applies_when the dispatch's tags satisfy,
ordered by the order they declare and then by name. The tags are
type=<TASK_TYPE>, engine=herdr, and every --tag. Each gate has its own override,
so saying yes to one never waves the other through:

  --confirm            proceed past a FAILING ENTRY CHECK
  --allow-unresolved   proceed with an UNRESOLVED DISPATCH open

  --tag K[=V]          a declared situation term. Repeatable. It may not restate
                       type or engine: those are set by the dispatch itself.
  --also NAME          force a playbook into the plan that the tags did not
                       select. A name that does not resolve is refused.
  --without NAME       drop a playbook the tags did select. It has the last word
                       over --also.

An empty plan is legal and reported: a dispatch whose tags select nothing runs
no protocol, which is visible in the brief and in the trace rather than silent.

Without --deliver the dispatch is prepared only: the cap node and its plan are
recorded, the entry checks run, and the brief is printed — nothing is started, so
no pane is split, no worker node exists and no agent runs. The prepared node
stays open, and the next dispatch into that scope refuses on it until it is
delivered or closed; --allow-unresolved is what proceeds past that.

--deliver carries the prepared brief the rest of the way, and it is the whole
difference between a plan a cap can read and a worker doing the work: it opens
the pane (or the --worktree-new workspace), creates the worker's node, starts an
agent named "dispatch-<type>-<node>", prompts it with the brief, and records the
delivery that close tears down.

Every entry check step runs with this dispatch's own inputs in its environment,
so a gate can ask about this dispatch and not only about the world:

  FS_PROJECT      the resolved project
  FS_TYPE         the task type
  FS_GOAL         the goal text
  FS_CARDS        the --cards value, comma-separated; empty when none was given
  FS_INTEGRATES   the --integrates value — the member cap node this integration
                  merges; empty when none was given
  FS_BIN          the running binary's own path. A step that runs fs must say
                  "$FS_BIN": the binary is installed into ~/.fs and is not on
                  PATH, so a bare fs exits 127. Empty if the path could not be
                  read, which a step must treat as missing, not as a path

A step names a script the binary ships as "$FS_BIN" script <name> [args...]: the
scripts are read out of the binary and written nowhere, so there is no
directory to address and no FS_SCRIPTS. The script's exit status is fs's, so a
gate run this way can fail.

--cards names the batch's cards. It is repeatable, and each value may be a
comma-separated list.

--integrates names the member cap node an integration merges. It must exist in
the cap scope and be a dispatch node; the link is recorded on this dispatch's own
task-created payload, so fs integrated can answer the member's question from the
record rather than from a goal string.

--reviews names the cap node whose change this dispatch reviews. It must exist
in the cap scope and be a dispatch node, and the link is recorded on this
dispatch's own task-created payload, together with the revision the review reads:
the reviewed dispatch's tree as it stands when the review is prepared. A review
that cannot name a revision is refused, and fs reviews accepts a done review only
while the change still stands at the revision it read — a review that names none
does not satisfy the gate at all. It is not --integrates: an integration names
what it merges and lands after it, a review names what it reads and runs before
the dispatch it is about closes. A dispatch may carry either, both, or neither.
fs reviews <CAP_NODE> is the read side — the check a close runs to ask whether
its change was reviewed.

--fixes names the cap nodes this dispatch repairs. Repeatable, because a fix
round answers more than one dispatch in the ordinary case: the change it repairs
and the review whose finding it fixes are both open while the fix is made. Each
must exist in the cap scope and be a dispatch node, and the link is recorded as a
decision on this dispatch's own node. Those dispatches are open by construction,
so naming them is what stops the unresolved-dispatch gate from reading them as
open work piled on top of the fix. Without the flag they are open work like any
other and the round needs --allow-unresolved.

--batch names the batch node this dispatch is a member of. It must be a
kind: batch node; the member's cap node is recorded as the batch's child
(parent_node_id), and the tag member is derived so the member playbooks load.

--worktree-new creates the worktree (herdr worktree create) instead of naming an
existing one with --worktree; the created workspace and checkout path are
recorded on the delivery exactly as --worktree records them, so the existing
teardown removes them with no new flags.`,
		flags: map[string]bool{
			"--project": true, "--type": true, "--goal": true,
			"--cards": true, "--integrates": true, "--reviews": true,
			"--fixes": true, "--batch": true,
			"--tag": true, "--also": true, "--without": true,
			"--confirm": false, "--allow-unresolved": false,
			"--deliver": false, "--worktree": true, "--worktree-new": false,
		},
	},
	"close": {
		usage: `Usage: fs close --node CAP_NODE [--worker PROJECT:NODE] --decision TEXT [--confirm] [--without-review REASON]
       fs close --node CAP_NODE --abandoned --reason TEXT

--abandoned closes a dispatch without a verdict, delivered or not. One that was
never delivered records "abandoned, never delivered: <reason>"; one that was
delivered records "abandoned after delivery to <project>:<node>: <reason>". It
skips the exit phase's checks and asks, but not its declared actions: what the
delivery opened is still torn down, and an action that fails is a warning.

--without-review is the review gate's own override, and it is required, not
optional: the exit phase asks whether this change was reviewed as a check, and
--confirm answers an ask, never a check. So a dispatch that legitimately needs no
review is closed with --without-review "<why>", which proceeds and records the
reason as a decision on the node. An unrecorded skip does not exist: the point is
that a skipped review is visible, not that it is impossible. The reason is
trimmed, so whitespace is refused with the empty case — and so is a waiver on a
close that faced no gate at all, whether because it was abandoned (no exit check
runs) or because its plan carries no review gate (there is none to stand down).
Recording a waiver there would write "waived the review gate" on a node whose
close never met one, which is the record asserting what the flag exists to make
visible.

The exit phase is the plan's phase: exit playbooks, in the order the plan
recorded. close refuses while the dispatch's trace does not account for every
step of that plan — every entry step, every work playbook's handover, every exit
step — naming the playbook, the step and the kind. The trace records what the
dispatch loaded and ran, not what the worker obeyed, so a step with no line is a
step that did not happen; the refusal says so rather than implying disobedience.

Every exit check and action runs with the closing dispatch's own inputs in its
environment, so an exit gate can ask about the dispatch it is gating and act on
what the delivery opened:

  FS_PROJECT        the worker's project
  FS_TYPE           the task type
  FS_NODE           the cap node being closed
  FS_WORKER         the worker's node as "<project>:<node>"; empty without one
  FS_CARDS          the --cards the dispatch named, comma-separated, read back
                    from its recorded plan; empty when it named none, but
                    present, not missing
  FS_BASE           the revision the project stood at when the dispatch was
                    prepared; empty for a project that was not a repository
                    then, which is what the deliverable check stands down on
  FS_INTEGRATES     the member this integration merges; empty when it merges none
  FS_WITHOUT_REVIEW the --without-review reason, empty when the flag was not
                    given; a step that gates on a review stands down on a
                    non-empty value, and on empty must run its check
  FS_PANE           the pane the delivery opened
  FS_WORKTREE       the worktree workspace the dispatch ran in; empty without one
  FS_WORKTREE_PATH  that worktree's checkout path; empty without one
  FS_BIN            the running binary's own path, the same contract the entry
                    gate gets; it is what the review gate below runs, because
                    fs is not on PATH and "$FS_BIN" is. It is also how an exit
                    step names a script the binary ships — "$FS_BIN" script
                    <name> — since the scripts are read out of the binary and
                    there is no FS_SCRIPTS to address

Checks run in the tree the dispatch worked in; declared do steps run in the
project root, so an action that removes the worktree does not stand inside it.`,
		flags: map[string]bool{
			"--node": true, "--worker": true, "--decision": true,
			"--confirm": false, "--abandoned": false, "--reason": true,
			"--without-review": true,
		},
	},
	"script": {
		usage: `Usage: fs script NAME [ARGS...]

Run a script the binary ships — one of the files under
src/flagship/defaults/scripts — read out of the binary and written nowhere.
ARGS reach it unchanged as $1.., its output is fs's output, and its exit status
is fs's exit status, so a gate a playbook runs this way can fail.

This is the spec fs enforces least: everything after the name belongs to the
script, flags and positionals alike. The only argument shape fs has an opinion
about is the name — a NAME the binary does not ship is refused, naming the ones
it does.`,
	},

	"unfinished": {usage: "Usage: fs unfinished"},
	"gaps":       {usage: "Usage: fs gaps"},
	"pending":    {usage: "Usage: fs pending"},

	"version": {
		usage: `Usage: fs version

Print the build this binary is: the revision the flake stamped into it at link
time, whether the tree it was built from had uncommitted changes, and the file
it is running from. Nothing is read and nothing is written — the answer is in
the binary — so it is the one question that can be asked of a build that is too
old to trust.

revision is "unknown" for a build with no stamp (a plain "go build") and for a
build whose flake could read no revision at all (a path or a tarball, where nix
exposes neither rev nor dirtyRev). dirty is then "unknown" too, rather than
"false": a build that cannot say what it was built from does not get to claim it
was built from a clean tree.`,
	},

	"cap claim": {
		usage: `Usage: fs cap claim [--reconstructed "WHAT YOU READ"]

Make this session the cap: the pane in $HERDR_PANE_ID becomes the scope's
address, recorded with its tab, the agent herdr reports there, and the time.

A claim is refused while the registered pane is still live, naming it and when
it was seen. When herdr no longer has it the claim is a take-over: it records
the address it replaced and its last-seen time, and requires --reconstructed,
what the claimant read before taking the scope.`,
		flags: map[string]bool{"--reconstructed": true},
	},
	"cap who": {
		usage: `Usage: fs cap who

Print the cap scope's address — pane, tab, agent, when it was claimed — and
whether herdr still has that pane. Exit 0 when a live cap exists, 1 when none
does, so a script or a playbook step can use it.

"none" and "dead" do not read alike: an unclaimed scope is free to take, and a
claim whose pane is gone is a cap nobody holds.`,
	},

	"report": {
		usage: `Usage: fs report PROJECT:NODE --line "ONE LINE" [--blocked|--question|--done]

A worker's report to whoever holds the cap. The event is written first and
always — durable, addressed to a node and never to a cap, readable by whoever
claims the scope now or later — and only then, best-effort, is the live cap
notified through herdr. One command holds that order, and no flag does either
half alone.

The kind decides whether the cap is interrupted. --blocked, --question and
--done notify; a kindless report records silently for whoever reads. There is no
flag to force a ping for a kind that does not notify.

A failed notification is not a failed report: with no live cap, or with a herdr
that refuses, the event is still written and the command exits 0 saying
"recorded, not notified". A --blocked report also blocks the node, with the line
as its reason, so every reader that already handles a block is untouched.

The line is a pointer, never the content: the node remains the record.`,
		flags: map[string]bool{
			"--line": true, "--project": true,
			"--blocked": false, "--question": false, "--done": false,
		},
		positionals: 1,
		address:     true,
	},
	"tell": {
		usage: `Usage: fs tell PROJECT:NODE "TEXT"

The cap's reply to a worker, resolved through that dispatch's delivery record
and sent through herdr. It is a message and not a record: what the cap decided
lands on the node as a decision, where it outlives the delivery. An unknown
node, or one with no delivery record, is refused naming which.`,
		positionals: 2,
		address:     true,
	},
	"integrated": {
		usage:       "Usage: fs integrated MEMBER_NODE",
		positionals: 1,
	},
	"reviews": {
		usage: `Usage: fs reviews CAP_NODE

Answer a close's question: was this dispatch's change reviewed? Exit 0 and print
the done review dispatch that names this cap node as the one it reads; exit 1
naming what is there instead — that no dispatch names it, or that one does and
is not done yet, which are different facts.

Done is the whole condition, and it is not a formality: a review is a
--type review-task dispatch, and its own exit gate refuses to close it until
every finding is dispositioned. So a done review is a review that finished
dispositioning, and this command asks whether that gate was passed rather than
opening a second one.

It reads only, and the cap scope needs no registered project, so it works from
anywhere — including the worktree the exit check runs it in.`,
		positionals: 1,
	},
	"batch": {
		usage: `Usage: fs batch BATCH_NODE

Answer a batch's question: is every member integrated? Exit 0 iff every member
has a done integration dispatch naming it; exit 1 naming the unintegrated
members otherwise. A batch with no members is refused rather than read as
complete. On success the payload carries each member and the resources its
delivery opened (worktree, checkout path, pane) — what the batch's teardown
loops over.`,
		positionals: 1,
	},
	"trace": {
		usage: `Usage: fs trace NODE [--project PROJECT_ID] [--json]

Print one dispatch's trace: the plan it was measured against, then one line per
step outcome, oldest first. It reads only — no line is ever written by this
command — so it is safe against a dispatch that is still in flight.

The text view ends with the deviation: what the plan asked for that the trace
does not account for, or "deviation: none". It is the same comparison close
refuses on, so what this prints is what would stop a close.

A node with no trace prints "no trace was written for <node>" and exits 1. There
is no empty timeline that reads as success.

  --project PROJECT_ID  an assertion, not a selector: the trace is located by
                        NODE alone, and a line whose project field disagrees
                        with this value is refused.
  --json                the raw lines, as the JSON envelope`,
		flags:       map[string]bool{"--project": true, "--json": false},
		positionals: 1,
	},
}

// checkArgs enforces name's spec. A leading help token prints that subcommand's
// usage to stderr and exits 0 before anything else runs — no store, no registry,
// not even the ~/.fs directory. A flag the subcommand does not accept, a flag
// missing its value, or a stray positional is a usage error: exit 2, naming what
// was wrong. A flag's value is not a positional.
func checkArgs(name string, args []string) {
	spec, ok := cmdSpecs[name]
	if !ok {
		fatal("fs: no argument spec for " + name)
	}

	if len(args) > 0 && isHelp(args[0]) {
		fmt.Fprintln(os.Stderr, spec.usage)
		os.Exit(0)
	}

	stray := 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			stray++
			if stray > spec.positionals {
				usageError("%s: unexpected argument %q", name, arg)
			}
			continue
		}

		takesValue, known := spec.flags[arg]
		if !known {
			usageError("%s: unknown flag %s", name, arg)
		}
		if takesValue {
			if i+1 >= len(args) {
				usageError("%s: flag %s requires a value", name, arg)
			}
			i++ // consume the value; it is not a positional
		}
	}
}

// positionals returns a subcommand's positional arguments, in the order they
// were written, counted the way checkArgs counts them: a flag's value is
// consumed as a value, so `fs task get --project skills t-nope` names t-nope.
//
// A command reads its positionals from here rather than from args[0], which is
// whatever the caller happened to write first — a flag, when the flags come
// first. checkArgs accepts that order deliberately, so the two have to agree:
// the walk is the same one, from the same spec.
func positionals(name string, args []string) []string {
	spec, ok := cmdSpecs[name]
	if !ok {
		fatal("fs: no argument spec for " + name)
	}

	var out []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			out = append(out, arg)
			continue
		}
		if takesValue := spec.flags[arg]; takesValue && i+1 < len(args) {
			i++ // consume the value; it is not a positional
		}
	}
	return out
}

// usageError reports a usage mistake as the usual envelope with exit 2, keeping
// it distinct from an app error's exit 1.
func usageError(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	logRefusal(msg)
	resp := command.Response{OK: false, Error: msg}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(resp)
	os.Exit(2)
}
