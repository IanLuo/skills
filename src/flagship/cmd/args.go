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
}

var cmdSpecs = map[string]cmdSpec{
	"project create": {
		usage: "Usage: fs project create [--name NAME] [--root PATH]",
		flags: map[string]bool{"--name": true, "--root": true},
	},
	"project list": {usage: "Usage: fs project list"},
	"project get":  {usage: "Usage: fs project get NAME", positionals: 1},

	"task add": {
		usage: "Usage: fs task add --goal GOAL [--parent NODE_ID] [--kind work|dispatch|gap] [--found-by PROJECT:NODE] [--project PROJECT_ID]",
		flags: map[string]bool{"--goal": true, "--parent": true, "--kind": true, "--found-by": true, "--project": true},
	},
	"task update": {
		usage:       "Usage: fs task update NODE_ID --status STATUS [--decision TEXT] [--project PROJECT_ID]",
		flags:       map[string]bool{"--status": true, "--decision": true, "--project": true},
		positionals: 1,
	},
	"task get": {
		usage:       "Usage: fs task get NODE_ID [--project PROJECT_ID] [--kind work|dispatch|gap]",
		flags:       map[string]bool{"--project": true, "--kind": true},
		positionals: 1,
	},
	"task edit": {
		usage:       "Usage: fs task edit NODE_ID [--goal GOAL] [--kind work|dispatch|gap] [--project PROJECT_ID]",
		flags:       map[string]bool{"--goal": true, "--kind": true, "--project": true},
		positionals: 1,
	},
	"task block": {
		usage:       "Usage: fs task block NODE_ID --reason REASON [--check \"SHELL COMMAND\"] [--project PROJECT_ID]",
		flags:       map[string]bool{"--reason": true, "--check": true, "--project": true},
		positionals: 1,
	},
	"task unblock": {
		usage:       "Usage: fs task unblock NODE_ID [--project PROJECT_ID]",
		flags:       map[string]bool{"--project": true},
		positionals: 1,
	},
	"task knowledge": {
		usage:       "Usage: fs task knowledge NODE_ID --summary TEXT [--project PROJECT_ID]",
		flags:       map[string]bool{"--summary": true, "--project": true},
		positionals: 1,
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

	"bootstrap": {usage: "Usage: fs bootstrap"},

	"dispatch": {
		usage: `Usage: fs dispatch --project PROJECT_ID --type TASK_TYPE --goal GOAL [--cards CARD[,CARD...]] [--tag K[=V]] [--also NAME] [--without NAME] [--confirm] [--allow-unresolved] [--deliver] [--worktree WORKSPACE_ID]

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

Every entry check step runs with this dispatch's own inputs in its environment,
so a gate can ask about this dispatch and not only about the world:

  FS_PROJECT      the resolved project
  FS_TYPE         the task type
  FS_GOAL         the goal text
  FS_CARDS        the --cards value, comma-separated; empty when none was given
  FS_INTEGRATES   the --integrates value — the member cap node this integration
                  merges; empty when none was given

--cards names the batch's cards. It is repeatable, and each value may be a
comma-separated list.

--integrates names the member cap node an integration merges. It must exist in
the cap scope and be a dispatch node; the link is recorded on this dispatch's own
task-created payload, so fs integrated can answer the member's question from the
record rather than from a goal string."`,
		flags: map[string]bool{
			"--project": true, "--type": true, "--goal": true,
			"--cards": true, "--integrates": true,
			"--tag": true, "--also": true, "--without": true,
			"--confirm": false, "--allow-unresolved": false,
			"--deliver": false, "--worktree": true,
		},
	},
	"close": {
		usage: `Usage: fs close --node CAP_NODE [--worker PROJECT:NODE] --decision TEXT [--confirm]
       fs close --node CAP_NODE --abandoned --reason TEXT

--abandoned closes a dispatch without a verdict, delivered or not. One that was
never delivered records "abandoned, never delivered: <reason>"; one that was
delivered records "abandoned after delivery to <project>:<node>: <reason>". It
skips the exit phase's checks and asks, but not its declared actions: what the
delivery opened is still torn down, and an action that fails is a warning.

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
  FS_CARDS          empty — a close has no batch — but present, not missing
  FS_INTEGRATES     the member this integration merges; empty when it merges none
  FS_PANE           the pane the delivery opened
  FS_WORKTREE       the worktree workspace the dispatch ran in; empty without one
  FS_WORKTREE_PATH  that worktree's checkout path; empty without one

Checks run in the tree the dispatch worked in; declared do steps run in the
project root, so an action that removes the worktree does not stand inside it.`,
		flags: map[string]bool{
			"--node": true, "--worker": true, "--decision": true,
			"--confirm": false, "--abandoned": false, "--reason": true,
		},
	},
	"unfinished": {usage: "Usage: fs unfinished"},
	"gaps":       {usage: "Usage: fs gaps"},
	"pending":    {usage: "Usage: fs pending"},
	"integrated": {
		usage:       "Usage: fs integrated MEMBER_NODE",
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

  --project PROJECT_ID  the project whose logs hold the trace. Without it, the
                        trace is found by searching every project's logs, and
                        more than one match is refused as ambiguous.
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

// usageError reports a usage mistake as the usual envelope with exit 2, keeping
// it distinct from an app error's exit 1.
func usageError(format string, args ...any) {
	resp := command.Response{OK: false, Error: fmt.Sprintf(format, args...)}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(resp)
	os.Exit(2)
}
