package main

// The declared teardown's branch step, run for real.
//
// The step is the fix for a branch that nothing would ever collect: a worktree
// whose checkout was deleted out of band — `rm -rf`, or someone else's
// `git worktree remove` — still has a registration and still has its branch
// checked out there, and the step used to name the branch by asking the
// checkout, which is no longer there. The case is therefore built the way it
// happens: make a worktree, delete the directory, run the step as the engine
// runs it, and ask git whether the branch is gone.
//
// The step body is read from the shipped playbook rather than restated, so a
// reworded step keeps testing the same behaviour (the helper's contract), and
// only removing the behaviour can break the test.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodeTeardownDeletesTheBranchOfAWorktreeWhoseCheckoutIsGone(t *testing.T) {
	repo := scratchGitRepo(t)
	const branch = "worktree-delivery"
	checkout := filepath.Join(repo, "wt")
	goGit(t, repo, "worktree", "add", "-q", "-b", branch, checkout)

	// Deleted out of band: `rm -rf`, not `git worktree remove`, because the
	// registration is the part that survives and the part the branch is named
	// from.
	if err := os.RemoveAll(checkout); err != nil {
		t.Fatal(err)
	}

	out, err := runTeardownStep(t, branchStep(t), repo, map[string]string{
		"FS_WORKTREE_PATH": checkout, "FS_WORKTREE": "wX",
	})
	if err != nil {
		t.Fatalf("the branch step refused a merged branch whose checkout is gone: %v\n%s", err, out)
	}
	if gitHasBranch(t, repo, branch) {
		t.Errorf("branch %s survived the teardown of a worktree that is gone:\n%s", branch, out)
	}
	// The main checkout is the one entry that stays: a registration the step
	// left behind is a second `worktree ` line.
	if list := gitOut(t, repo, "worktree", "list", "--porcelain"); strings.Count(list, "\nworktree ") != 0 {
		t.Errorf("git still registers the worktree:\n%s", list)
	}
}

// The other half of the same fix: a worktree that is still there is detached
// from its branch and the branch deleted, exactly as before. The step must not
// have traded one case for the other.
func TestCodeTeardownDeletesTheBranchOfALiveWorktree(t *testing.T) {
	repo := scratchGitRepo(t)
	const branch = "worktree-delivery"
	checkout := filepath.Join(repo, "wt")
	goGit(t, repo, "worktree", "add", "-q", "-b", branch, checkout)

	out, err := runTeardownStep(t, branchStep(t), repo, map[string]string{
		"FS_WORKTREE_PATH": checkout, "FS_WORKTREE": "wX",
	})
	if err != nil {
		t.Fatalf("the branch step refused a merged branch in a live worktree: %v\n%s", err, out)
	}
	if gitHasBranch(t, repo, branch) {
		t.Errorf("branch %s survived the teardown:\n%s", branch, out)
	}
	if _, err := os.Stat(checkout); err != nil {
		t.Errorf("the branch step removed the checkout: %v; git worktree remove is the next step's job", err)
	}
}

// A branch that is not merged into HEAD is not deleted, whether or not the
// checkout is still there. This is the property the step exists around: a close
// that destroys work to succeed is not a close worth having.
func TestCodeTeardownRefusesToDeleteAnUnmergedBranch(t *testing.T) {
	for _, gone := range []bool{false, true} {
		name := "checkout present"
		if gone {
			name = "checkout gone"
		}
		t.Run(name, func(t *testing.T) {
			repo := scratchGitRepo(t)
			const branch = "worktree-delivery"
			checkout := filepath.Join(repo, "wt")
			goGit(t, repo, "worktree", "add", "-q", "-b", branch, checkout)
			// A commit only this branch has: unmerged, and unrecoverable if the
			// branch is deleted.
			goGit(t, checkout, "-c", "user.email=fs@test.invalid", "-c", "user.name=fs test",
				"commit", "-q", "--allow-empty", "-m", "work in the worktree")
			if gone {
				if err := os.RemoveAll(checkout); err != nil {
					t.Fatal(err)
				}
			}

			out, err := runTeardownStep(t, branchStep(t), repo, map[string]string{
				"FS_WORKTREE_PATH": checkout, "FS_WORKTREE": "wX",
			})
			if !gitHasBranch(t, repo, branch) {
				t.Fatalf("branch %s was deleted with unmerged work on it:\n%s", branch, out)
			}
			if err == nil {
				t.Errorf("the step passed over an unmerged branch instead of refusing:\n%s", out)
			}
			if !strings.Contains(out, "is not merged") {
				t.Errorf("the refusal must say the branch is not merged; got:\n%s", out)
			}
		})
	}
}

// branchStep is the shipped branch step of the `code` teardown: the first `do:`
// in code.yaml that asks git whether a branch is merged.
func branchStep(t *testing.T) string {
	t.Helper()
	return shippedStep(t, shippedDefault(t, codePb), "do", "merge-base")
}

// runTeardownStep runs one declared step the way the playbook engine runs it —
// through a shell, from the project root, with the delivery's variables in the
// environment — so what is tested is the line that actually runs, not a
// paraphrase of it. FS_BATCH is set to the empty string the engine exports for a
// dispatch that is not a batch member: the step's first clause reads it.
func runTeardownStep(t *testing.T, body, dir string, extra map[string]string) (string, error) {
	t.Helper()

	environ := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			environ[k] = v
		}
	}
	environ["FS_BATCH"] = ""
	for k, v := range extra {
		environ[k] = v
	}
	env := make([]string, 0, len(environ))
	for k, v := range environ {
		env = append(env, k+"="+v)
	}

	cmd := exec.Command("sh", "-c", body)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// scratchGitRepo makes a scratch repository with one commit, so a worktree can be made
// from it and a branch can be merged into HEAD. The identity is per-invocation,
// so the test neither reads the machine's git configuration nor writes to it.
func scratchGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	goGit(t, repo, "init", "-q")
	goGit(t, repo, "-c", "user.email=fs@test.invalid", "-c", "user.name=fs test",
		"commit", "-q", "--allow-empty", "-m", "base")
	return repo
}

// gitHasBranch answers whether the branch is still there. Absence is an exit
// code and not an error here, which the fatal-on-failure runner cannot say.
func gitHasBranch(t *testing.T, repo, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = repo
	return cmd.Run() == nil
}
