// Package main is the `fs` command: five verbs over shared memstore, and no
// policy of its own.
//
// This layer owns flags, the JSON envelope and the exit codes. Every decision —
// what a task is, who may claim it, what counts as evidence of completion —
// belongs to internal/coord, and every read and write belongs to
// internal/memory. Nothing here knows a key, a scope or a record.
package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/flagship-dev/flagship/internal/coord"
	"github.com/flagship-dev/flagship/internal/logging"
	"github.com/flagship-dev/flagship/internal/memory"
)

// usageText is the whole interface. Five verbs: add, claim, report, done, status.
const usageText = `Usage: fs <command> [flags]

  fs add    --project P --goal G --check C [--check C]... [--path DIR]
            [--review-of TID]        Record a task. A review task names the
                                     development task it reviews and runs that
                                     task's declared checks.
  fs claim  --project P --session S [--task TID]
                                     Bind this session to one task and hold it.
                                     Without --task, the oldest eligible task.
  fs report --project P --session S --task TID --state S
            [--role dev|review|runtime] [--command C --exit N]
            [--digest D] [--note TEXT]   Record a report. This is also the
                                     keepalive: there is no renew verb.
  fs done   --project P --session S --task TID [--note TEXT]
                                     Complete a review. Refused unless this
                                     reviewer ran every declared check and each
                                     one passed.
  fs status [--project P] [--session S]
                                     What fs knows. A session sees only what its
                                     binding authorizes; the user sees every
                                     project.

States: progress, dev-done, pass, fail, blocked.
The store is $MEMSTORE_DB, else ~/.memstore/memory.db. fs owns no store of its own.`

// logger is the running log: JSON to stderr, teed to a rotating file. It records
// ids and outcomes only, and never gates a decision. It is discarded until main
// sets it, so a test that calls run() logs nowhere.
var logger = slog.New(slog.NewJSONHandler(io.Discard, nil))

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run parses one invocation and returns its exit code. It is a function rather
// than a body in main so the envelopes and the exit codes can be tested without
// starting a process.
func run(args []string, stdout, stderr io.Writer) int {
	args, verbose := stripVerbose(args)

	level, err := logging.ResolveLevel(os.Getenv("FS_LOG_LEVEL"), verbose)
	if err != nil {
		return emit(stdout, stderr, nil, usageErr{err.Error()}, usageText)
	}
	logger = logging.NewLogger(logDir(), level)

	if len(args) == 0 {
		return emit(stdout, stderr, nil, usageErr{"no command given"}, usageText)
	}
	verb, rest := args[0], args[1:]

	var data any
	switch verb {
	case "add":
		data, err = addCmd(rest)
	case "claim":
		data, err = claimCmd(rest)
	case "report":
		data, err = reportCmd(rest)
	case "done":
		data, err = doneCmd(rest)
	case "status":
		data, err = statusCmd(rest)
	case "help", "-h", "--help":
		return emit(stdout, stderr, nil, usageErr{"fs <add|claim|report|done|status> [flags]"}, usageText)
	default:
		return emit(stdout, stderr, nil, usageErr{fmt.Sprintf("unknown command %q", verb)}, usageText)
	}

	code := emit(stdout, stderr, data, err, usageText)
	if err != nil {
		logger.Warn("command refused", "verb", verb, "reason", err.Error())
		return code
	}
	logger.Info("command", "verb", verb)
	return code
}

// addCmd records one task. The user owns the intent: goal, project scope, the
// checks a reviewer must run.
func addCmd(args []string) (any, error) {
	fs := newFlags("add")
	project := fs.String("project", "", "project scope the task belongs to")
	goal := fs.String("goal", "", "what the task is for")
	path := fs.String("path", "", "the worktree the task runs in, if it has one")
	reviewOf := fs.String("review-of", "", "the development task this review reviews")
	var checks repeated
	fs.Var(&checks, "check", "a command a reviewer must run to complete the task; repeatable")
	if err := parse(fs, args); err != nil {
		return nil, err
	}

	s, err := openStore()
	if err != nil {
		return nil, err
	}
	defer s.Close()

	return coord.Add(s, coord.AddInput{
		Project: *project, Goal: *goal, Checks: checks, Path: *path, ReviewOf: *reviewOf,
	})
}

// claimCmd binds a session to one task and holds it. Without --task it takes the
// oldest eligible one.
func claimCmd(args []string) (any, error) {
	fs := newFlags("claim")
	project := fs.String("project", "", "project scope to claim in")
	session := fs.String("session", "", "this agent session's stable identity")
	task := fs.String("task", "", "claim this task rather than the next eligible one")
	if err := parse(fs, args); err != nil {
		return nil, err
	}

	s, err := openStore()
	if err != nil {
		return nil, err
	}
	defer s.Close()

	return coord.Claim(s, coord.ClaimInput{Project: *project, Session: *session, Task: *task})
}

// reportCmd records one report. It is also the writer's keepalive.
func reportCmd(args []string) (any, error) {
	fs := newFlags("report")
	project := fs.String("project", "", "project scope the task belongs to")
	session := fs.String("session", "", "this agent session's stable identity")
	task := fs.String("task", "", "the task this session holds")
	state := fs.String("state", "", "progress, dev-done, pass, fail or blocked")
	role := fs.String("role", "dev", "who is reporting: dev, review or runtime")
	command := fs.String("command", "", "the exact check that was run")
	digest := fs.String("digest", "", "a short digest of what the check produced")
	note := fs.String("note", "", "one bounded line of context")
	var exit *int
	fs.Func("exit", "the check's exit code", func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("exit %q is not a number", v)
		}
		exit = &n
		return nil
	})
	if err := parse(fs, args); err != nil {
		return nil, err
	}

	s, err := openStore()
	if err != nil {
		return nil, err
	}
	defer s.Close()

	return coord.Report(s, coord.ReportInput{
		Project: *project, Session: *session, Task: *task, State: *state, Role: *role,
		Command: *command, Exit: exit, Digest: *digest, Note: *note,
	})
}

// doneCmd asks the completion gate. Only a reviewer's passing, independently run
// checks close a task.
func doneCmd(args []string) (any, error) {
	fs := newFlags("done")
	project := fs.String("project", "", "project scope the task belongs to")
	session := fs.String("session", "", "this reviewer session's stable identity")
	task := fs.String("task", "", "the review task this session holds")
	note := fs.String("note", "", "one bounded line of context")
	if err := parse(fs, args); err != nil {
		return nil, err
	}

	s, err := openStore()
	if err != nil {
		return nil, err
	}
	defer s.Close()

	return coord.Done(s, coord.DoneInput{Project: *project, Session: *session, Task: *task, Note: *note})
}

// statusCmd answers what fs knows, from rows alone.
func statusCmd(args []string) (any, error) {
	fs := newFlags("status")
	project := fs.String("project", "", "one project; omit for every project")
	session := fs.String("session", "", "this agent session's stable identity")
	if err := parse(fs, args); err != nil {
		return nil, err
	}

	s, err := openStore()
	if err != nil {
		return nil, err
	}
	defer s.Close()

	return coord.Status(s, coord.StatusInput{Project: *project, Session: *session})
}

// openStore opens the shared memstore file. A store that cannot be opened is a
// refusal: fs does not fall back to another source of coordination state.
func openStore() (*memory.Store, error) {
	return memory.Open("")
}

// newFlags builds a flag set whose parse errors become refusals with an exit code.
// The set prints nothing itself: fs's answers go out as JSON, not as usage noise.
func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

// parse runs the flag set and turns flag's error into a usage refusal.
func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return usageErr{fs.Name() + ": " + err.Error()}
	}
	if fs.NArg() > 0 {
		return usageErr{fmt.Sprintf("%s: unexpected argument %q", fs.Name(), fs.Arg(0))}
	}
	return nil
}

// repeated collects a flag given more than once, in order. --check is repeatable
// because a task may need several checks and each one is required.
type repeated []string

func (r *repeated) String() string { return fmt.Sprint([]string(*r)) }

func (r *repeated) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// stripVerbose pulls the global -v/--verbose out of the args before a verb's own
// flag set sees them, so one flag works for every verb without being declared
// five times.
func stripVerbose(args []string) ([]string, bool) {
	out := make([]string, 0, len(args))
	verbose := false
	for _, a := range args {
		if a == "-v" || a == "--verbose" {
			verbose = true
			continue
		}
		out = append(out, a)
	}
	return out, verbose
}

// logDir is the running log's directory, resolved without creating anything: the
// sink creates it on its first write, so a refusal path that never logs touches
// nothing on disk.
func logDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".fs", "logs")
}
