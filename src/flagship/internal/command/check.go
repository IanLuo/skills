// Running a recorded check, and what a block's check says about it.
//
// A block reason is prose, so nothing re-verifies it. A block may also record
// the shell command that would show its condition is over — "0 work-type
// playbooks" was true when it was written and false hours later — and fs
// pending and fs unfinished re-run it. Visible, not automatic: nothing unblocks
// itself, but a reader no longer reads an expired reason as current truth.
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

// BlockStatus is the status to report a node with: for a blocked node that
// recorded a check, the check is re-run in dir and, when it exits 0, the status
// says the condition no longer holds. Every other node keeps its own status — a
// block with no check is prose and gets no claim of freshness.
func BlockStatus(n UnfinishedNode, dir string) string {
	return blockStatus(n.Status, n.BlockCheck, dir)
}

// blockStatus is the one re-verification fs status, fs pending, and fs unfinished
// share, so a blocked node reads the same in all three: plain "blocked", or
// "blocked (condition no longer holds …)" once the recorded check exits 0.
func blockStatus(status, check, dir string) string {
	if status != "blocked" || check == "" {
		return status
	}
	if _, err := RunShell(check, dir, nil); err == nil {
		return fmt.Sprintf("blocked (condition no longer holds — %s exited 0)", check)
	}
	return status
}

// refreshBlockChecks replaces each node's status with its BlockStatus. rootOf
// resolves a scope to the directory its checks run in; a nil rootOf, or a scope
// it cannot resolve, runs in this process's own directory.
func refreshBlockChecks(nodes []UnfinishedNode, rootOf func(scope string) string) {
	for i := range nodes {
		dir := ""
		if rootOf != nil {
			dir = rootOf(nodes[i].ProjectID)
		}
		nodes[i].Status = BlockStatus(nodes[i], dir)
	}
}
