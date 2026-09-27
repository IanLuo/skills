// Running a recorded check, and what it says about the node that recorded it.
//
// A node's prose is not re-verifiable. A node may also record the shell command
// that would show its claim is over — a block's condition, a gap's defect — and
// every reader that renders a node's status re-runs it here. Visible, not
// automatic: nothing unblocks or closes itself, but a reader no longer reads an
// expired claim as current truth.
//
// RunShell is shared with the prerequisite and cleanup gates, which run the same
// kind of command in the same kind of place.
package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CheckTimeout bounds a re-run. A reader re-runs one check per node it renders
// — fs gaps walks every scope — so a check that blocks would otherwise stall the
// reader with no output and no way to tell it is waiting.
//
// Ten seconds is the heaviest shipped check measured in this repo (check-specs.sh
// at 0.22s) two orders of magnitude over, because the two ways of being wrong are
// not alike: too tight reports a claim that held as one nobody finished checking,
// which is a false report, while too loose only waits. It leaves room for the
// class of check a gap's claim can legitimately need — a git walk, a small test.
const CheckTimeout = 10 * time.Second

// checkPipeGrace is what the re-run waits for its output pipe after the deadline
// has killed the shell. A check that leaves a child behind holds that pipe open,
// and the wait on it outlives the shell however dead the shell is: measured,
// `sh -c 'sleep 30 & sleep 30'` under a two-second deadline returned at 30s
// without this, so the deadline alone does not bound the reader.
const checkPipeGrace = time.Second

// ErrCheckTimeout is what a re-run reports when it stopped the check at the
// deadline. The check did not answer, which is neither the condition holding nor
// the claim being over, and a reader is told which.
var ErrCheckTimeout = errors.New("check did not finish")

// RunShell executes a check body as a shell command with cwd = dir — the project
// root a prerequisite check runs in. An empty dir means this process's own
// working directory, which is the fallback for a scope with no registered root.
// env is added to this process's environment rather than replacing it, so the
// shell keeps PATH and the rest of what a command needs. It returns the
// command's combined output and the run's error.
//
// It is unbounded, which is what a gate and a step get: they run a command that
// may do real work. The re-run is bounded — see runCheck.
func RunShell(body, dir string, env []string) (string, error) {
	return runShell(body, dir, env, 0)
}

// runShell is RunShell with a deadline: a non-zero timeout stops the command at
// it and reports ErrCheckTimeout rather than the exit the command never reached.
// reRun is the caller that passes one.
func runShell(body, dir string, env []string, timeout time.Duration) (string, error) {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", body)
	cmd.Dir = dir
	cmd.Env = shellEnv(env)
	if timeout > 0 {
		cmd.WaitDelay = checkPipeGrace
	}
	out, err := cmd.CombinedOutput()

	// The deadline is read from the context, not from the error: a killed
	// process reports `signal: killed`, which is also what an ordinary death by
	// signal looks like.
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return string(out), fmt.Errorf("%w — %s exceeded %s", ErrCheckTimeout, body, timeout)
	}
	return string(out), err
}

// reRun is the re-run: the recorded check, under the deadline. It is not named
// runCheck, which dispatch already uses for a gate or exit step — a different
// question (does this step pass?) with a different answer.
func reRun(body, dir string) (string, error) {
	return runShell(body, dir, nil, CheckTimeout)
}

// shellEnv is this process's environment plus env, and FS_BIN when env does not
// already name it.
//
// FS_BIN is how every card and playbook in this repo addresses the binary — it
// is installed into ~/.fs and is not on PATH — so a check written `"$FS_BIN" …`
// exits 127 in a shell that was not given one, and its claim never reads as
// expired. The entry gate and the exit steps give their steps the same value, so
// `"$FS_BIN"` means one thing everywhere.
//
// A caller that names FS_BIN keeps it, even empty: the gates export it
// deliberately, empty when os.Executable cannot be read, which a step has to be
// able to see. A second entry would leave which one wins to the libc.
func shellEnv(env []string) []string {
	full := append(os.Environ(), env...)
	for _, kv := range env {
		if strings.HasPrefix(kv, "FS_BIN=") {
			return full
		}
	}
	return append(full, "FS_BIN="+BinaryPath())
}

// BinaryPath is the path of the binary that is running, or "" when it cannot be
// read. The empty case is deliberate: a step that needs fs has to be able to see
// it is missing, and a gate that cannot evaluate what it was asked must refuse
// rather than pass.
//
// os.Executable is the running process's own path, which is the point: a step
// runs the binary that dispatched it, not one a PATH lookup happens to find.
// filepath.Abs because a step's cwd is the worktree or the project root, never
// the directory fs was started from, so a relative path would resolve to
// somewhere else — or nowhere.
func BinaryPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return ""
	}
	return abs
}

// NodeStatus is the status to report a node with: a node that recorded a check
// has it re-run in dir, and the report says so once the check exits 0. Every
// other node keeps its own status — no check is prose alone, with no claim of
// freshness.
func NodeStatus(n UnfinishedNode, dir string) string {
	return reportedStatus(n.Status, n.BlockCheck, n.Check, dir)
}

// reportedStatus is the one re-verification every reader that renders a status
// shares, so a node reads the same in all of them.
//
// A node's recorded check exits 0 if and only if the condition it asserts is
// over: for a block, that the condition no longer holds; for a gap, that the
// defect is gone. The one rule covers every kind. It cannot tell a check written
// the other way round — one that exits 0 while the defect stands — from a right
// one, and it does not try: such a check reads as expired, which is what it is,
// and the polarity is the author's to get right.
//
// A check that did not finish is a third reading, and not a shade of either: the
// deadline stopped it, so the claim was neither confirmed nor denied, and saying
// it held would report a condition nobody finished asking about.
//
// A blocked node is read through the check its block recorded, which is the
// check about the block. The node's own check is about the node, and is read
// whenever the node is not blocked. A done node is done: there is nothing left
// to re-verify.
func reportedStatus(status, blockCheck, check, dir string) string {
	if status == "done" {
		return status
	}
	if status == "blocked" {
		if blockCheck != "" {
			if _, err := reRun(blockCheck, dir); err == nil {
				return fmt.Sprintf("blocked (condition no longer holds — %s exited 0)", blockCheck)
			} else if errors.Is(err, ErrCheckTimeout) {
				return fmt.Sprintf("blocked (%v)", err)
			}
		}
		return status
	}
	if check != "" {
		if _, err := reRun(check, dir); err == nil {
			return fmt.Sprintf("%s (claim no longer holds — %s exited 0)", status, check)
		} else if errors.Is(err, ErrCheckTimeout) {
			return fmt.Sprintf("%s (%v)", status, err)
		}
	}
	return status
}

// refreshChecks replaces each node's status with its NodeStatus. rootOf resolves
// a scope to the directory its checks run in; a nil rootOf, or a scope it cannot
// resolve, runs in this process's own directory.
func refreshChecks(nodes []UnfinishedNode, rootOf func(scope string) string) {
	for i := range nodes {
		dir := ""
		if rootOf != nil {
			dir = rootOf(nodes[i].ProjectID)
		}
		nodes[i].Status = NodeStatus(nodes[i], dir)
	}
}
