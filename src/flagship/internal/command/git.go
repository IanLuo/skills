package command

import (
	"os/exec"
	"strings"
	"time"
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

// GitLogAfter returns one-line commit summaries after the given timestamp.
// Format: "<short-sha> <subject>". Returns nil if not in a git repo or
// no commits exist after the timestamp. The dir parameter sets the working
// directory for the git command; if empty, uses the current directory.
func GitLogAfter(dir string, after time.Time) []string {
	afterStr := after.Format(time.RFC3339)
	cmd := exec.Command("git", "log", "--after="+afterStr, "--oneline", "--no-decorate")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}
