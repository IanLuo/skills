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
	"fmt"
	"os"
	"os/exec"
)

// RunShell executes a check body as a shell command with cwd = dir — the project
// root a prerequisite check runs in. An empty dir means this process's own
// working directory, which is the fallback for a scope with no registered root.
// env is added to this process's environment rather than replacing it, so the
// shell keeps PATH and the rest of what a command needs. It returns the
// command's combined output and the run's error.
func RunShell(body, dir string, env []string) (string, error) {
	cmd := exec.Command("sh", "-c", body)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// NodeStatus is the status to report a node with: a node that recorded a check
// has it re-run in dir, and the report says so once the check exits 0. Every
// other node keeps its own status — no check is prose alone, with no claim of
// freshness.
func NodeStatus(n UnfinishedNode, dir string) string {
	return reportedStatus(n.Status, n.BlockCheck, n.Check, dir)
}

// reportedStatus is the one re-verification fs status, fs pending, fs unfinished
// and fs gaps share, so a node reads the same in all four.
//
// A node's recorded check exits 0 if and only if the condition it asserts is
// over: for a block, that the condition no longer holds; for a gap, that the
// defect is gone. The one rule covers every kind. It cannot tell a check written
// the other way round — one that exits 0 while the defect stands — from a right
// one, and it does not try: such a check reads as expired, which is what it is,
// and the polarity is the author's to get right.
//
// A blocked node is read through the check its block recorded, which is the
// check about the block. The node's own check is about the node, and is read
// whenever the node is not blocked. A done node is done: there is nothing left
// to re-verify.
func reportedStatus(status, blockCheck, check, dir string) string {
	if status == "done" {
		return status
	}
	if status == "blocked" && blockCheck != "" {
		if _, err := RunShell(blockCheck, dir, nil); err == nil {
			return fmt.Sprintf("blocked (condition no longer holds — %s exited 0)", blockCheck)
		}
		return status
	}
	if check != "" {
		if _, err := RunShell(check, dir, nil); err == nil {
			return fmt.Sprintf("%s (claim no longer holds — %s exited 0)", status, check)
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
