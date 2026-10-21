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
	// addressFlags marks the flags on this subcommand whose *value* is a
	// `PROJECT:NODE` address. The declaration is per command and per argument
	// for the positionals, and it is per command and per argument here too: a
	// flag is an address where the command declares it one and where it does
	// not the value is whatever the command says it is. `log --node` is an
	// address — the scope it names is the one the log is of — while `close
	// --node` is an exact node id close resolves through capNode in the cap
	// scope, so its colon is part of the id and names no project. Reading every
	// `--node` as an address put `project` on close's refusal lines naming a
	// scope the command never used.
	addressFlags map[string]bool
	// projectDefault marks a subcommand whose `--project` is a default scope for
	// an operand that carries none, rather than an assertion about the one it
	// does. It is the one declaration under which an address naming another
	// scope is not the conflict nodeArg refuses: task merge's two operands are
	// the ends of a fold and may name two scopes by construction — that is the
	// case the command exists for — so a qualified operand disagreeing with
	// --project is what it was asked to do, not a symptom.
	projectDefault bool
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
		usage: `Usage: fs task merge NODE_ID --into SURVIVOR [--reason TEXT] [--project PROJECT_ID]

Each operand is a node address: a bare "t-<hex>" id, or "<project>:<node>". A
bare id takes the scope the command resolved; a qualified one names its own. The
two ends of a fold may therefore be in two scopes, which is what folding a
finding filed in the cap's scope into the same finding noticed in the worker's
needs — and each half is written into the scope of the node it is about.

For these two operands --project is a default, not an assertion. There is no one
scope for an address to disagree with, so a qualified operand is not refused for
naming another; say which scope each end belongs to, and that is what is used.`,
		flags:          map[string]bool{"--into": true, "--reason": true, "--project": true},
		positionals:    1,
		address:        true,
		projectDefault: true,
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
		usage: `Usage: fs log [--project PROJECT_ID] [--node NODE_ID] [--type TYPE]

--node takes a node address: a bare "t-<hex>" id, or "<project>:<node>". The
address supplies the scope when --project did not. Named both ways and the two
disagreeing is a conflict rather than a precedence question — the node moved
scope, or the project was renamed, so one of the two is stale — and it is
refused: the refusal resolves the node in both scopes and says which half is the
stale one.

An id that resolves to nothing is refused. An empty history and an id that is not
a node are different facts, and answering both with no events is the one answer a
reader cannot act on. --type narrows which of the node's events are shown; it
never decides whether the node is one, so asking for a type it has none of is an
empty list and not a refusal.`,
		flags:        map[string]bool{"--project": true, "--node": true, "--type": true},
		addressFlags: map[string]bool{"--node": true},
	},

	"dispatch": {
		usage: `Usage: fs dispatch --project PROJECT_ID --type TASK_TYPE --goal GOAL [--integrates MEMBER_NODE] [--reviews CAP_NODE] [--fixes CAP_NODE]... [--batch BATCH_NODE] [--allow-unresolved] [--deliver] [--worktree WORKSPACE_ID] [--worktree-new]

A dispatch is a worker node, a rendered brief, and a herdr delivery. There is no
plan and no gate: the launch config — engine=herdr, agent=pi, and the provider
route the agent is started with — is frozen in the binary, and the brief is the
whole instruction the worker receives.

--allow-unresolved proceeds while another dispatch is open and has not been
delivered. It is the dispatch's only override, and it records the dispatches it
proceeded past as a decision on its own node.

Without --deliver the dispatch is prepared only: the cap node and the brief are
recorded and printed — nothing is started, so no pane is split, no worker node
exists and no agent runs. The prepared node stays open, and the next dispatch
into that scope refuses on it until it is delivered or closed.

--deliver carries the prepared brief the rest of the way: it opens the pane (or
the --worktree-new workspace), creates the worker's node, starts an agent named
"dispatch-<type>-<node>" on the launch config, prompts it with the brief, and
records the delivery close tears down.

--integrates names the member cap node an integration merges. It must exist in
the cap scope and be a dispatch node; the link is recorded on this dispatch's own
task-created payload, so fs integrated can answer the member's question from the
record rather than from a goal string.

--reviews names the cap node whose change this dispatch reviews. It must exist
in the cap scope and be a dispatch node, and the link is recorded on this
dispatch's own task-created payload. It is not --integrates: an integration names
what it merges and lands after it, a review names what it reads and runs before
the dispatch it is about closes. fs reviews <CAP_NODE> is the read side, and a
review is a node kind with a link — never a gate.

--fixes names the cap nodes this dispatch repairs. Repeatable, because a fix
round answers more than one dispatch in the ordinary case: the change it repairs
and the review whose finding it fixes are both open while the fix is made. Each
must exist in the cap scope and be a dispatch node, and the link is recorded as a
decision on this dispatch's own node. Those dispatches are open by construction,
so naming them is what stops the unresolved-dispatch read from treating them as
open work piled on top of the fix. Without the flag they are open work like any
other and the round needs --allow-unresolved.

--batch names the batch node this dispatch is a member of. It must be a
kind: batch node; the member's cap node is recorded as the batch's child
(parent_node_id).

--worktree-new creates the worktree (herdr worktree create) instead of naming an
existing one with --worktree; the created workspace and checkout path are
recorded on the delivery exactly as --worktree records them.`,
		flags: map[string]bool{
			"--project": true, "--type": true, "--goal": true,
			"--integrates": true, "--reviews": true,
			"--fixes": true, "--batch": true,
			"--allow-unresolved": false,
			"--deliver":          false, "--worktree": true, "--worktree-new": false,
		},
	},
	"close": {
		usage: `Usage: fs close --node CAP_NODE [--worker PROJECT:NODE] --decision TEXT
       fs close --node CAP_NODE --abandoned --reason TEXT

Close-out is one evidence line and a teardown. It refuses while the worker's node
is not done, and while the tree has not moved past the revision the dispatch was
prepared at: HEAD unmoved means nothing reached the repository, so there is
nothing to close.

The line recorded on the node is the sha HEAD stands at, the base it was measured
against, and the verdict — which is where the suite result belongs, because the
cap is what ran the suite. Two cases are reported rather than refused: a project
that was not a git repository when the dispatch was prepared, and a tree fs
cannot read a revision from. The line says the deliverable was not checked.

--abandoned closes a dispatch without a verdict, delivered or not. One that was
never delivered records "abandoned, never delivered: <reason>"; one that was
delivered records "abandoned after delivery to <project>:<node>: <reason>". It
skips the reads, never the teardown: what the delivery opened is still closed.`,
		flags: map[string]bool{
			"--node": true, "--worker": true, "--decision": true,
			"--abandoned": false, "--reason": true,
		},
		// --worker names the worker node and is a PROJECT:NODE address; --node
		// names the cap node and is an exact id, resolved in the cap scope.
		addressFlags: map[string]bool{"--worker": true},
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

	"state": {usage: `Usage: fs state <subcommand>

Subcommands:
  verify    Re-fold every scope into a scratch projection and compare it with
            the live one. Writes nothing; exits 1 naming the first scope that
            differs, the differing field, and a value from each side.
  rebuild   Re-fold every scope into the live projection file. The repair for a
            drifted or empty projection; it touches no non-fs row.`},
	"state verify":  {usage: "Usage: fs state verify"},
	"state rebuild": {usage: "Usage: fs state rebuild"},
	"unfinished":    {usage: "Usage: fs unfinished"},
	"gaps":          {usage: "Usage: fs gaps"},
	"pending":       {usage: "Usage: fs pending"},

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
