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
	"fmt"
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

// The matrix this step is judged on, and the one a regression walked out of: a
// worktree reached plainly or through a symlink, still there or deleted out of
// band, its branch merged or not. The step has two duties — no cell may leave a
// merged branch behind, and no cell may delete an unmerged one — so every cell
// is asserted rather than a representative few. The symlinked columns are the
// ones that broke: the branch was named by matching the recorded path against
// git's listing, and git prints the path it resolved, so a path whose last
// segment was a link matched nothing and the step exited 0 in silence.
func TestCodeTeardownBranchStepAcrossSpellingsAndStates(t *testing.T) {
	for _, live := range []bool{true, false} {
		for _, symlink := range []bool{false, true} {
			for _, merged := range []bool{true, false} {
				name := fmt.Sprintf("live=%v symlink=%v merged=%v", live, symlink, merged)
				t.Run(name, func(t *testing.T) {
					repo, branch, p := worktreeInState(t, live, symlink, merged)

					out, err := runTeardownStep(t, branchStep(t), repo, map[string]string{
						"FS_WORKTREE_PATH": p, "FS_WORKTREE": "wX",
					})

					if merged {
						if err != nil {
							t.Fatalf("a merged branch must not refuse the close: %v\n%s", err, out)
						}
						if gitHasBranch(t, repo, branch) {
							t.Errorf("branch %s survived the teardown:\n%s", branch, out)
						}
						return
					}
					// Unmerged: the branch is the only place that work exists, so
					// it stays and the close refuses — that refusal is what puts a
					// person in front of the tree before it is gone.
					if !gitHasBranch(t, repo, branch) {
						t.Fatalf("branch %s was deleted with unmerged work on it:\n%s", branch, out)
					}
					if err == nil {
						t.Errorf("the step passed over an unmerged branch instead of refusing:\n%s", out)
					}
				})
			}
		}
	}
}

// The postcondition that asserts git no longer registers the worktree, after the
// steps that claim to have removed it. It matched the recorded path the same
// parent-only way the branch step did, so a symlinked path matched nothing and
// the check PASSED while git still had the worktree registered — a gate
// reporting success over the one thing it exists to catch, which is worse than
// the leak it was meant to find. It refuses under both spellings now, and still
// passes once the registration is genuinely gone, so it is not refusing
// everything.
func TestCodeRegistrationCheckRefusesASymlinkedPathGitStillRegisters(t *testing.T) {
	repo := scratchGitRepo(t)
	const branch = "worktree-delivery"
	real := filepath.Join(repo, "real-wt")
	goGit(t, repo, "worktree", "add", "-q", "-b", branch, real)
	link := filepath.Join(repo, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	check := registrationCheck(t)
	for _, p := range []string{real, link} {
		out, err := runTeardownStep(t, check, repo, map[string]string{
			"FS_WORKTREE_PATH": p, "FS_WORKTREE": "wX",
		})
		if err == nil {
			t.Errorf("the check passed while git still registers the worktree, reached as %s:\n%s", p, out)
		}
	}

	// Gone and pruned: the goal state, and the check has to say so — a gate that
	// refuses everything is not a gate either.
	if err := os.RemoveAll(real); err != nil {
		t.Fatal(err)
	}
	goGit(t, repo, "worktree", "prune")
	for _, p := range []string{real, link} {
		out, err := runTeardownStep(t, check, repo, map[string]string{
			"FS_WORKTREE_PATH": p, "FS_WORKTREE": "wX",
		})
		if err != nil {
			t.Errorf("the check refused a worktree git no longer registers, reached as %s: %v\n%s", p, err, out)
		}
	}
}

// worktreeInState builds a scratch repo holding one worktree in the named state:
// live or deleted out of band, reached plainly or through a symlink, its branch
// merged into HEAD or not. It returns the repo, the branch and the path the
// delivery would have recorded.
func worktreeInState(t *testing.T, live, symlink, merged bool) (repo, branch, path string) {
	t.Helper()

	repo = scratchGitRepo(t)
	branch = "worktree-delivery"
	real := filepath.Join(repo, "real-wt")
	goGit(t, repo, "worktree", "add", "-q", "-b", branch, real)

	if !merged {
		// A commit only this branch has: unrecoverable if the branch is deleted.
		goGit(t, real, "-c", "user.email=fs@test.invalid", "-c", "user.name=fs test",
			"commit", "-q", "--allow-empty", "-m", "work in the worktree")
	}

	path = real
	if symlink {
		path = filepath.Join(repo, "link")
		if err := os.Symlink(real, path); err != nil {
			t.Fatal(err)
		}
	}
	if !live {
		// Deleted out of band — `rm -rf`, not `git worktree remove` — because the
		// registration is the part that survives, and, through a symlink, the one
		// thing left naming the directory.
		if err := os.RemoveAll(real); err != nil {
			t.Fatal(err)
		}
	}
	return repo, branch, path
}

// branchStep is the shipped branch step of the `code` teardown: the first `do:`
// in code.yaml that asks git whether a branch is merged.
func branchStep(t *testing.T) string {
	t.Helper()
	return shippedStep(t, shippedDefault(t, codePb), "do", "merge-base")
}

// registrationCheck is the shipped postcondition that asserts git no longer
// registers the worktree. `grep -qxF` is what tells it from the branch step,
// which reads the same listing with `grep -A2 -xF`.
func registrationCheck(t *testing.T) string {
	t.Helper()
	return shippedStep(t, shippedDefault(t, codePb), "check", "grep -qxF")
}

// runTeardownStep runs one declared step the way the playbook engine runs it —
// through a shell, from the project root, with the delivery's variables in the
// environment — so what is tested is the line that actually runs, not a
// paraphrase of it. FS_BATCH is set to the empty string the engine exports for a
// dispatch that is not a batch member: the step's first clause reads it. FS_BIN
// is the binary under test, which is what the step reaches its shipped scripts
// through, so the script a step runs is the one this build embeds.
func runTeardownStep(t *testing.T, body, dir string, extra map[string]string) (string, error) {
	t.Helper()

	environ := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			environ[k] = v
		}
	}
	environ["FS_BATCH"] = ""
	environ["FS_BIN"] = getFS(t)
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
