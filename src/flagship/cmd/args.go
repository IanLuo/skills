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
		usage: "Usage: fs task add --goal GOAL [--parent NODE_ID] [--project PROJECT_ID]",
		flags: map[string]bool{"--goal": true, "--parent": true, "--project": true},
	},
	"task update": {
		usage:       "Usage: fs task update NODE_ID --status STATUS [--decision TEXT] [--project PROJECT_ID]",
		flags:       map[string]bool{"--status": true, "--decision": true, "--project": true},
		positionals: 1,
	},
	"task edit": {
		usage:       "Usage: fs task edit NODE_ID --goal GOAL [--project PROJECT_ID]",
		flags:       map[string]bool{"--goal": true, "--project": true},
		positionals: 1,
	},
	"task block": {
		usage:       "Usage: fs task block NODE_ID --reason REASON [--project PROJECT_ID]",
		flags:       map[string]bool{"--reason": true, "--project": true},
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
		usage: "Usage: fs kb add --name NAME --file PATH",
		flags: map[string]bool{"--name": true, "--file": true},
	},
	"kb get":    {usage: "Usage: fs kb get NAME", positionals: 1},
	"kb list":   {usage: "Usage: fs kb list"},
	"kb diff":   {usage: "Usage: fs kb diff NAME", positionals: 1},
	"kb reset":  {usage: "Usage: fs kb reset NAME --yes", flags: map[string]bool{"--yes": false}, positionals: 1},
	"kb edit":   {usage: "Usage: fs kb edit --name NAME --file PATH", flags: map[string]bool{"--name": true, "--file": true}},
	"kb remove": {usage: "Usage: fs kb remove NAME", positionals: 1},

	"bootstrap": {usage: "Usage: fs bootstrap"},

	"dispatch": {
		usage: "Usage: fs dispatch --project PROJECT_ID --type TASK_TYPE --goal GOAL [--confirm] [--deliver]",
		flags: map[string]bool{
			"--project": true, "--type": true, "--goal": true,
			"--confirm": false, "--deliver": false,
		},
	},
	"close": {
		usage: "Usage: fs close --node CAP_NODE --worker PROJECT:NODE --decision TEXT | --abandoned --reason TEXT",
		flags: map[string]bool{
			"--node": true, "--worker": true, "--decision": true,
			"--abandoned": false, "--reason": true,
		},
	},
	"unfinished": {usage: "Usage: fs unfinished"},
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
