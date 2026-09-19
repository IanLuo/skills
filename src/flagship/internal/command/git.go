package command

import (
	"os/exec"
	"strings"
)

// DetectCommitSHAIn returns the git HEAD sha of the repository at root, or nil
// when root is not inside a git repo. An empty root means the process cwd.
// (ARCHITECTURE R4: commit_sha auto-populated from git HEAD — of the project
// the event is about, not of wherever the CLI happens to be running)
func DetectCommitSHAIn(root string) *string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return nil
	}
	return &sha
}

// DetectProjectRoot returns the git repo root, or "" if not in a git repo.
func DetectProjectRoot() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
