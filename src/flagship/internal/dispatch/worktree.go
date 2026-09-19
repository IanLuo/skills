package dispatch

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitWorktrees is the slice of git the close-out teardown needs: which
// worktrees a repository has registered, and removing one.
//
// It exists because a herdr worktree and a git worktree are two different
// artifacts that die separately. Closing the herdr workspace says nothing about
// the checkout it was made from, and herdr's own error codes say less: a close
// that read `workspace_not_found` as "the worktree is gone" declared two leaked
// checkouts clean. So the gate that decides whether a worktree survives its node
// asks the thing that owns the worktree — git — and the answer it trusts is the
// verified path, never the dispatcher's return code.
//
// It is a separate interface for the same reason HerdrCLI is: a gate has to be
// exercised without the external it guards, and both the leak and the stubborn
// worktree have to be constructible on demand.
type GitWorktrees interface {
	// List returns the paths git reports for the repository at repo, the way
	// `git worktree list --porcelain` prints them.
	List(repo string) ([]string, error)
	// Remove removes the worktree at path from the repository at repo. git
	// refuses a worktree with modified or untracked files, and that refusal is
	// the caller's to report, not to force past.
	Remove(repo, path string) error
}

// gitWorktrees shells out to the git binary on PATH.
//
// The questions are asked of the repository root, not of the worktree itself,
// because the worktree may be exactly what is missing: a checkout already
// deleted from disk can still be registered, and only the repository can say so.
type gitWorktrees struct{}

// NewGitWorktrees returns the git adapter the close-out teardown verifies with.
func NewGitWorktrees() GitWorktrees { return gitWorktrees{} }

func (gitWorktrees) List(repo string) ([]string, error) {
	out, err := runGit(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	// The porcelain format is one record per worktree: a "worktree <path>" line,
	// then HEAD, then branch or detached. Only the path is needed — the gate asks
	// whether a path the record names is still registered.
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func (gitWorktrees) Remove(repo, path string) error {
	_, err := runGit(repo, "worktree", "remove", path)
	return err
}

// runGit runs git in dir and returns its stdout. Unlike the herdr CLI, git
// speaks in exit codes and prose on stderr, so the stderr text is what makes the
// failure nameable.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), detail)
	}
	return stdout.String(), nil
}

// removeLeakedWorktree makes sure the checkout at path is gone, and returns a
// warning naming it when it is not.
//
// The order is the whole fix: ask git whether the path is still registered, and
// only then act. A path git no longer lists is the goal state and is verified as
// such — "already gone" decided by the path, not by whatever herdr said. A path
// still listed is removed, and then *re-checked*: a removal that reports success
// and a worktree that is still there is exactly the failure this gate exists to
// catch, so git's exit code is not taken for the answer either.
func removeLeakedWorktree(gw GitWorktrees, repo, path string) string {
	listed, err := gw.List(repo)
	if err != nil {
		return fmt.Sprintf("could not verify worktree %s: %v — check it by hand", path, err)
	}
	if !listsWorktree(listed, path) {
		return ""
	}

	if err := gw.Remove(repo, path); err != nil {
		return fmt.Sprintf("could not remove worktree %s: %v — remove it by hand", path, err)
	}
	listed, err = gw.List(repo)
	if err != nil {
		return fmt.Sprintf("worktree %s was removed but could not be verified gone: %v — check it by hand", path, err)
	}
	if listsWorktree(listed, path) {
		return fmt.Sprintf("worktree %s is still registered after removal — remove it by hand", path)
	}
	return ""
}

// listsWorktree reports whether git still lists path.
//
// Paths are compared as files they point at, not as strings: git prints the
// resolved path (`/private/var/...` on macOS) while the record holds whatever
// herdr reported (`/var/...`), and a string compare would read that as a
// different worktree — deciding the leak was clean, or trying to remove a
// worktree the record never named.
func listsWorktree(listed []string, path string) bool {
	for _, candidate := range listed {
		if sameFile(candidate, path) {
			return true
		}
	}
	return false
}

// sameFile reports whether two paths name the same directory.
//
// Strings are compared first, and when they differ both paths are resolved:
// git prints the real path (`/private/var/...` on macOS) while the record holds
// whatever herdr reported (`/var/...`), and a string compare would read that as
// a different worktree — deciding a leak was clean, or trying to remove a
// worktree the record never named. A path that cannot be resolved — one already
// deleted — is equal to nothing but itself.
func sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
