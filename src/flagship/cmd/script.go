// The one subcommand whose output is not fs's envelope.
//
// Every other command prints the JSON response and its exit status is fs's
// verdict on the request. `fs script` runs a script the binary ships and then
// gets out of the way: the script's stdout is the stdout, the script's exit
// status is the exit status. That is not a convenience: a script's own refusal
// has to stay its own, or it reads as fs's verdict on the request.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/flagship-dev/flagship"
)

// scriptCmd runs one shipped script: `fs script <name> [args...]`.
//
// The name and the arguments reach the script unchanged, and it runs with this
// process's environment, working directory and stdio, so a step that runs
// `"$FS_BIN" script branch-decision.sh <path>` behaves exactly as the file
// form did — in a worktree, under a temp HOME, with the project's own FS_*.
//
// The third argument to `bash -c` is the trap this exists to close, and it is
// silent. bash reads the word after the command string as the script's $0 and
// every word after that as $1.., so `bash -c "$body" a,b` makes a,b the
// script's own *name* and leaves $1 empty: the script receives no arguments at
// all, and a script with no arguments often still does something plausible —
// a script that exits 2 on missing arguments is a refusal nobody asked for. Passing the name as $0 (a placeholder that is also true)
// puts args[1:] where the script reads them.
func scriptCmd(args []string) {
	// The spec in cmdSpecs is not enforced here: everything after the name
	// belongs to the script, flags and positionals alike, so the only argument
	// shape fs can have an opinion about is the name itself. Its usage line is
	// still the one --help prints.
	if len(args) > 0 && isHelp(args[0]) {
		fmt.Fprintln(os.Stderr, cmdSpecs["script"].usage)
		os.Exit(0)
	}
	if len(args) == 0 {
		usageError("script: no script was named")
	}

	name := args[0]
	body, err := flagship.Script(name)
	if err != nil {
		fatal(err.Error())
	}

	cmd := exec.Command("bash", append([]string{"-c", string(body), name}, args[1:]...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		fatal("script " + name + ": " + err.Error())
	}
}
