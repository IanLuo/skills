package command

import (
	"os/exec"
	"strings"
)

// DetectCommitSHA returns the current git HEAD sha if in a git repo, or nil.
// (ARCHITECTURE R4: commit_sha auto-populated from git HEAD)
func DetectCommitSHA() *string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
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
