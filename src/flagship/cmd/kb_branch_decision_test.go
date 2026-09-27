package main

// The branch decision, run for real: one answer per state, in the exit status.
//
// The teardown's one judgement — what to do with the branch a delivery created —
// used to be assembled inside the step out of shell test results: a text match
// against git's listing, then `[ -n "$b" ] || exit 0`. That shape cannot tell
// "the match found nothing" from "there is correctly nothing to do", because
// both are empty stdout and exit 0. The decision now lives in
// `defaults/scripts/branch-decision.sh`, whose exit status is the answer:
//
//	0  nothing to do                     1  delete the branch named on stdout
//	2  the command line was wrong        3  refuse, with the reason on stderr
//
// The property that makes a caller safe is arithmetic rather than attention: the
// branch name reaches stdout on exactly one status, and it is not 0. So the
// table below asserts all three channels on every row — a status, what stdout
// held, and whether stderr had anything to say — and the rows where stdout must
// be empty are the proof that a caller that ignores the status has no name to
// delete unless the seam authorised the deletion.
//
// The rows are the findings turned into cases rather than shell guards, and they
// are split by what the answer needs to be decided:
//
//   - Rows that need no repository at all: the command line, and a path that
//     cannot be resolved. The answer is decided before git is asked anything,
//     which is why a plain directory is the whole fixture.
//   - Rows that need a real repository: every case about the repository's own
//     facts — which branch a worktree stands on, where else that branch is
//     checked out, which branch the repository records as its default, whether
//     the branch is merged. There is no way to state these to the decision
//     without a repository holding them, and a scratch repo with one commit is
//     what states them.
//
// Two call sites drive the seam — `code`'s teardown and `batch`'s batch-exit
// teardown, once per member — and both are tested here end to end, through the
// shipped step bodies the engine runs, rather than trusted to follow the
// contract.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// seamRun runs the shipped branch decision the way a step reaches it: the binary
// under test, `script branch-decision.sh`, from the project root. It keeps the
// three channels apart, because the answer is spread across all three and a test
// that folded them could not tell one state from another.
//
// fsBin is what the script is told its own binary is. That is how it reaches
// resolve-path.sh, and the empty string is a state the engine really exports:
// `os.Executable()` unreadable is exported empty, which a step must treat as
// missing rather than as a path.
func seamRun(t *testing.T, dir, fsBin string, args ...string) (stdout, stderr string, status int) {
	t.Helper()

	cmd := exec.Command(getFS(t), append([]string{"script", "branch-decision.sh"}, args...)...)
	cmd.Dir = dir
	// FS_BIN is replaced rather than appended: this process's environment is
	// inherited wholesale so the decision sees the machine a step would see, but
	// a duplicate FS_BIN would leave which one wins to the libc.
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "FS_BIN=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "FS_BIN="+fsBin)

	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("running the branch decision: %v", err)
		}
		status = exit.ExitCode()
	}
	return out.String(), errOut.String(), status
}

// seamRow is one state, and the answer it must produce on all three channels.
// overRepo is the row's own statement of what it needs: a scratch repository, or
// only a directory.
type seamRow struct {
	name     string
	overRepo bool
	// noFSBin is the state `os.Executable()` unreadable leaves behind: FS_BIN
	// exported empty.
	noFSBin bool
	// build returns the directory the decision runs in and its arguments.
	build func(t *testing.T) (string, []string)
	// wantStatus is the answer. wantOut is stdout exactly — empty for every state
	// but the one that authorises a deletion, and the branch name alone for that
	// one. wantErr is a substring of stderr, or the empty string for a state that
	// must say nothing there.
	wantStatus int
	wantOut    string
	wantErr    string
}

// The spellings a record may name the project's own checkout with. Each is the
// same state: the guard compares resolved paths, so a fold that misses one of
// these is a branch deleted in the project's own checkout — which is the
// destructive defect this whole decision exists around. `a symlink naming it` is
// the `/tmp`-against-`/private/tmp` shape a record picks up on a machine whose
// temporary directory is a link.
var ownCheckoutSpellings = []struct {
	name  string
	spell func(t *testing.T, repo string) string
}{
	{"plain", func(t *testing.T, repo string) string { return repo }},
	{"a trailing /.", func(t *testing.T, repo string) string { return repo + "/." }},
	{"a /.. past a subdirectory", func(t *testing.T, repo string) string { return repo + "/sub/.." }},
	{"a doubled leading slash", func(t *testing.T, repo string) string { return "/" + repo }},
	{"a symlink naming it", func(t *testing.T, repo string) string {
		link := filepath.Join(t.TempDir(), "project-root-link")
		if err := os.Symlink(repo, link); err != nil {
			t.Fatal(err)
		}
		return link
	}},
}

func TestBranchDecisionAnswersOneStatePerStatus(t *testing.T) {
	rows := []seamRow{
		// ---- no repository needed ----
		{
			// A step that lost its argument must not decide anything, and the
			// usage state is where that lands.
			name: "no path at all",
			build: func(t *testing.T) (string, []string) {
				return t.TempDir(), nil
			},
			wantStatus: 2, wantOut: "", wantErr: "usage",
		},
		{
			name: "an empty path",
			build: func(t *testing.T) (string, []string) {
				return t.TempDir(), []string{""}
			},
			wantStatus: 2, wantOut: "", wantErr: "PATH is empty",
		},
		{
			name: "a third argument",
			build: func(t *testing.T) (string, []string) {
				return t.TempDir(), []string{"/somewhere", "member w1", "one too many"}
			},
			wantStatus: 2, wantOut: "", wantErr: "usage",
		},
		{
			// The path is resolved before anything is asked of git, so a record
			// naming a directory that does not exist refuses here — and the
			// refusal names the path it was about, not the empty stdout the
			// failed call left behind.
			name: "a path that cannot be resolved",
			build: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				return dir, []string{filepath.Join(dir, "no-such-parent", "child")}
			},
			wantStatus: 3, wantOut: "", wantErr: "cannot resolve the recorded path",
		},
		{
			// A git that cannot be asked is a refusal, never a pass: this is the
			// fail-closed direction the whole decision is built on.
			name: "a directory that is not a repository",
			build: func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				return dir, []string{dir}
			},
			wantStatus: 3, wantOut: "", wantErr: "cannot read git's worktree list",
		},
		{
			// The engine exports FS_BIN empty when os.Executable() cannot be
			// read, and a step must treat empty as missing rather than as a path.
			name:       "FS_BIN exported empty",
			noFSBin:    true,
			build:      func(t *testing.T) (string, []string) { return t.TempDir(), []string{"/somewhere"} },
			wantStatus: 3, wantOut: "", wantErr: "FS_BIN is not set",
		},

		// ---- a real repository needed ----
		{
			// A detached worktree has no branch line, which is the same answer as
			// no worktree at all for the same reason: there is no branch here to
			// delete.
			name:     "a registered worktree, detached",
			overRepo: true,
			build: func(t *testing.T) (string, []string) {
				repo := scratchGitRepo(t)
				checkout := filepath.Join(repo, "wt")
				goGit(t, repo, "worktree", "add", "-q", "--detach", checkout)
				return repo, []string{checkout}
			},
			wantStatus: 0, wantOut: "", wantErr: "",
		},
		{
			// `HEAD` arrives here as a branch name, and it is not one: a worktree
			// whose HEAD was pointed at `refs/heads/HEAD` makes git's listing print
			// `branch refs/heads/HEAD`, so the name is read out of the registration
			// like any other. It is not a name a delivery holds a branch by — `git
			// check-ref-format --branch HEAD`, `git branch HEAD` and `git worktree
			// add -b HEAD` all refuse it — so there is no delivery branch here to
			// free, whatever git would do with a ref of that name. This row exists
			// because the guard that used to stand in the step was read as
			// unreachable while this decision was written, on the argument that no
			// branch is called HEAD; the argument is wrong, and the construction
			// below is what makes it wrong.
			name:     "no branch here: a worktree whose branch is named HEAD",
			overRepo: true,
			build: func(t *testing.T) (string, []string) {
				repo := scratchGitRepo(t)
				checkout := filepath.Join(repo, "wt")
				goGit(t, repo, "worktree", "add", "-q", "--detach", checkout)
				goGit(t, checkout, "symbolic-ref", "HEAD", "refs/heads/HEAD")
				return repo, []string{checkout}
			},
			wantStatus: 0, wantOut: "", wantErr: "",
		},
		{
			// The half-torn-down state `git worktree remove` leaves: a directory
			// at the recorded path that git registers no worktree at. Nothing
			// about it says which branch to look at, so nothing may be touched.
			name:     "a path git registers no worktree at",
			overRepo: true,
			build: func(t *testing.T) (string, []string) {
				repo := scratchGitRepo(t)
				sub := filepath.Join(repo, "sub")
				if err := os.Mkdir(sub, 0o755); err != nil {
					t.Fatal(err)
				}
				return repo, []string{sub}
			},
			wantStatus: 0, wantOut: "", wantErr: "",
		},
		{
			name:     "delete: a merged branch in a live worktree",
			overRepo: true,
			build: func(t *testing.T) (string, []string) {
				repo, _, p := worktreeInState(t, true, false, true)
				return repo, []string{p}
			},
			wantStatus: 1, wantOut: "worktree-delivery\n", wantErr: "",
		},
		{
			// The answer does not depend on the directory being there: the branch
			// is named from git's registration, which a `rm -rf` leaves behind.
			// The caller acts differently — prune rather than detach — and that
			// is the caller's question, not this one's.
			name:     "delete: a merged branch whose checkout is gone",
			overRepo: true,
			build: func(t *testing.T) (string, []string) {
				repo, _, p := worktreeInState(t, false, false, true)
				return repo, []string{p}
			},
			wantStatus: 1, wantOut: "worktree-delivery\n", wantErr: "",
		},
		{
			// A branch that is not merged holds the only copy of that work.
			name:     "refuse: an unmerged branch",
			overRepo: true,
			build: func(t *testing.T) (string, []string) {
				repo, _, p := worktreeInState(t, true, false, false)
				return repo, []string{p}
			},
			wantStatus: 3, wantOut: "", wantErr: "is not merged into HEAD",
		},
		{
			// `git branch -d` will not refuse this one for us: only this worktree
			// has the branch, so git would delete it happily. The repository
			// records which branch is its default in refs/remotes/origin/HEAD.
			name:     "refuse: the repository's default branch",
			overRepo: true,
			build: func(t *testing.T) (string, []string) {
				repo := scratchGitRepo(t)
				defaultBranch := trunkBranch(t, repo)
				goGit(t, repo, "checkout", "-q", "-b", "release")
				checkout := filepath.Join(repo, "wt")
				goGit(t, repo, "worktree", "add", "-q", checkout, defaultBranch)
				sha := strings.TrimSpace(gitOut(t, repo, "rev-parse", defaultBranch))
				goGit(t, repo, "update-ref", "refs/remotes/origin/"+defaultBranch, sha)
				goGit(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+defaultBranch)
				return repo, []string{checkout}
			},
			wantStatus: 3, wantOut: "", wantErr: "default branch",
		},
		{
			// The same guard with nothing to answer it: a repository with no
			// remote records its default branch nowhere, and the branch the trunk
			// stands on is then only readable as the fact it is — another
			// worktree has it checked out.
			name:     "refuse: the branch the trunk stands on, with no remote naming a default",
			overRepo: true,
			build: func(t *testing.T) (string, []string) {
				repo := scratchGitRepo(t)
				trunk := trunkBranch(t, repo)
				checkout := filepath.Join(repo, "wt")
				goGit(t, repo, "worktree", "add", "-q", "--detach", checkout)
				goGit(t, checkout, "checkout", "-q", "--ignore-other-worktrees", trunk)
				return repo, []string{checkout}
			},
			wantStatus: 3, wantOut: "", wantErr: "another worktree",
		},
		{
			// The finding that had no guard at all. git refuses to delete a
			// branch another worktree holds, so a "delete" here would be an
			// authorisation git will not honour — the answer has to be right
			// before anything is asked of git.
			name:     "refuse: a branch another worktree holds",
			overRepo: true,
			build: func(t *testing.T) (string, []string) {
				repo := scratchGitRepo(t)
				const branch = "worktree-delivery"
				goGit(t, repo, "worktree", "add", "-q", "-b", branch, filepath.Join(repo, "held"))
				delivery := filepath.Join(repo, "wt")
				goGit(t, repo, "worktree", "add", "-q", "--detach", delivery)
				goGit(t, delivery, "checkout", "-q", "--ignore-other-worktrees", branch)
				return repo, []string{delivery}
			},
			wantStatus: 3, wantOut: "", wantErr: "another worktree",
		},
		{
			// A delivery's worktree is never the repository's main checkout, so a
			// record naming it is a record that is wrong about what the delivery
			// was given. It is reachable when the project root is itself a linked
			// worktree, which the row below sets up.
			name:     "refuse: the repository's main checkout",
			overRepo: true,
			build: func(t *testing.T) (string, []string) {
				repo := scratchGitRepo(t)
				root := filepath.Join(t.TempDir(), "project-root")
				goGit(t, repo, "worktree", "add", "-q", "-b", "project-root", root)
				return root, []string{repo}
			},
			wantStatus: 3, wantOut: "", wantErr: "repository's main checkout",
		},
		{
			// The same state, with the main checkout standing on `HEAD`. The
			// question "is this the project's own checkout" is not conditioned on
			// what that checkout stands on, so it is asked before any question
			// about a branch — and with the checkout guard sitting below the
			// `HEAD` guard this record answered nothing to do instead, losing the
			// refusal. `refs/heads/HEAD` is not a name git's branch commands will
			// make — `git branch HEAD` refuses it — so the ref is put there out of
			// band, with `update-ref`.
			name:     "refuse: the repository's main checkout standing on HEAD",
			overRepo: true,
			build: func(t *testing.T) (string, []string) {
				repo := scratchGitRepo(t)
				root := filepath.Join(t.TempDir(), "project-root")
				goGit(t, repo, "worktree", "add", "-q", "-b", "project-root", root)
				goGit(t, repo, "update-ref", "refs/heads/HEAD", gitOut(t, repo, "rev-parse", "HEAD"))
				goGit(t, repo, "symbolic-ref", "HEAD", "refs/heads/HEAD")
				return root, []string{repo}
			},
			wantStatus: 3, wantOut: "", wantErr: "repository's main checkout",
		},
		{
			// The same state with the main checkout detached — the instance the
			// "no branch line" guard pre-empted, and the one that makes this a
			// question about the checkout rather than about a branch: a checkout
			// on no branch was answered nothing-to-do about before the guard was
			// asked.
			name:     "refuse: the repository's main checkout, detached",
			overRepo: true,
			build: func(t *testing.T) (string, []string) {
				repo := scratchGitRepo(t)
				root := filepath.Join(t.TempDir(), "project-root")
				goGit(t, repo, "worktree", "add", "-q", "-b", "project-root", root)
				goGit(t, repo, "checkout", "-q", "--detach")
				return root, []string{repo}
			},
			wantStatus: 3, wantOut: "", wantErr: "repository's main checkout",
		},
		{
			// A project root is allowed to be a linked worktree, and then the
			// repository's main checkout is a different directory. The checkout
			// this runs in is the project's own, whichever one it is.
			name:     "refuse: a project root that is itself a linked worktree",
			overRepo: true,
			build: func(t *testing.T) (string, []string) {
				repo := scratchGitRepo(t)
				root := filepath.Join(t.TempDir(), "project-root")
				goGit(t, repo, "worktree", "add", "-q", "-b", "project-root", root)
				return root, []string{root}
			},
			wantStatus: 3, wantOut: "", wantErr: "the project's own checkout",
		},
	}

	for _, sp := range ownCheckoutSpellings {
		rows = append(rows, seamRow{
			name:     "refuse: the project's own checkout, " + sp.name,
			overRepo: true,
			build: func(t *testing.T) (string, []string) {
				repo := scratchGitRepo(t)
				return repo, []string{sp.spell(t, repo)}
			},
			wantStatus: 3, wantOut: "", wantErr: "the project's own checkout",
		})
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			dir, args := row.build(t)
			fsBin := getFS(t)
			if row.noFSBin {
				fsBin = ""
			}
			out, errOut, status := seamRun(t, dir, fsBin, args...)

			if status != row.wantStatus {
				t.Errorf("status = %d, want %d\nstdout: %q\nstderr: %q", status, row.wantStatus, out, errOut)
			}
			if out != row.wantOut {
				t.Errorf("stdout = %q, want %q — the branch name is on stdout for exactly one state, "+
					"and every other state must leave a caller with nothing to delete", out, row.wantOut)
			}
			if row.wantErr == "" {
				if errOut != "" {
					t.Errorf("stderr = %q, want it empty: a state that is not a refusal says nothing", errOut)
				}
			} else if !strings.Contains(errOut, row.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, row.wantErr)
			}

			if !row.overRepo {
				// Every row's claim about what it needs is asserted rather than
				// assumed: a row that needs no repository is one whose answer is
				// decided before git is asked anything, and if its directory
				// turned out to sit inside a repository, the row would be
				// testing something else.
				probe := exec.Command("git", "rev-parse", "--show-toplevel")
				probe.Dir = dir
				probe.Env = []string{"PATH=" + os.Getenv("PATH")}
				if out, err := probe.CombinedOutput(); err == nil {
					t.Errorf("%s is inside the repository at %s, so this row needs a repository after all",
						dir, strings.TrimSpace(string(out)))
				}
			}
		})
	}
}

// The regression the seam has to be watched for: a decision that is safe in the
// tests and a foot-gun in the shell. A caller is not careful by construction —
// the step bodies are shell, and the careless shape is the one that deleted a
// branch that was not the delivery's — so both careless callers are run against
// real repositories.
//
// What makes them safe is arithmetic, not attention: the branch name reaches
// stdout on exactly one status and it is not 0. A caller that ignores the status
// has no name to delete unless the seam authorised the deletion; a caller under
// `set -e` deletes nothing at all, because the assignment fails and the step
// aborts. The third case is here so the first two cannot pass for the wrong
// reason: the same careless caller on a state the seam authorises still deletes
// the branch, so "nothing was deleted" is a statement about the contract rather
// than about a caller that never deletes anything.
func TestBranchDecisionIsSafeForACarelessCaller(t *testing.T) {
	// The step's own actions with the status thrown away: read what the script
	// printed, and act on whether it printed anything. That is the shape the old
	// step had, and the shape that deleted a wrong branch — because the "answer"
	// was a text match that could find the wrong thing.
	const ignoresTheStatus = `b=$("$FS_BIN" script branch-decision.sh "$FS_WORKTREE_PATH"); ` +
		`[ -n "$b" ] || exit 0; ` +
		`if [ -e "$FS_WORKTREE_PATH" ]; then git -C "$FS_WORKTREE_PATH" checkout -q --detach && git branch -d "$b"; ` +
		`else git worktree prune && git branch -d "$b"; fi`

	t.Run("the status ignored, on a state the seam refuses", func(t *testing.T) {
		repo := scratchGitRepo(t)
		trunk := trunkBranch(t, repo)

		// The project's own checkout is the state the old step tore down: it
		// detached this checkout and deleted this branch, reporting exit 0.
		out, err := runTeardownStep(t, ignoresTheStatus, repo, map[string]string{
			"FS_WORKTREE_PATH": repo, "FS_WORKTREE": "wX",
		})
		if err != nil {
			t.Fatalf("the careless caller failed rather than standing down on a refusal: %v\n%s", err, out)
		}
		if !gitHasBranch(t, repo, trunk) {
			t.Fatalf("the careless caller deleted the branch the project stands on (%s):\n%s", trunk, out)
		}
		if branch, attached := headBranch(t, repo); !attached || branch != trunk {
			t.Errorf("the careless caller left the project's checkout detached (HEAD=%q attached=%v):\n%s",
				branch, attached, out)
		}
	})

	t.Run("set -e, on a state the seam authorises", func(t *testing.T) {
		repo, branch, p := worktreeInState(t, true, false, true)

		// The strictest shell there is: `set -e` turns the seam's non-zero status
		// into an abort, so the step stops before it acts — and the state it
		// stopped on is one the seam AUTHORISED. Nothing is deleted and nothing
		// is detached, which is the fail-safe direction: a teardown that does
		// nothing is a close that refuses and a person looking at it, where a
		// teardown that guesses is a branch nobody can get back.
		body := `set -e; b=$("$FS_BIN" script branch-decision.sh "$FS_WORKTREE_PATH"); ` +
			`git -C "$FS_WORKTREE_PATH" checkout -q --detach; git branch -d "$b"`
		out, err := runTeardownStep(t, body, repo, map[string]string{
			"FS_WORKTREE_PATH": p, "FS_WORKTREE": "wX",
		})
		if err == nil {
			t.Errorf("the assignment under `set -e` did not stop the step, so it acted:\n%s", out)
		}
		if !gitHasBranch(t, repo, branch) {
			t.Fatalf("a `set -e` caller deleted branch %s:\n%s", branch, out)
		}
		if head, attached := headBranch(t, p); !attached || head != branch {
			t.Errorf("a `set -e` caller detached the worktree (HEAD=%q attached=%v) before aborting:\n%s",
				head, attached, out)
		}
	})

	t.Run("the status ignored, on a state the seam authorises", func(t *testing.T) {
		repo, branch, p := worktreeInState(t, true, false, true)

		out, err := runTeardownStep(t, ignoresTheStatus, repo, map[string]string{
			"FS_WORKTREE_PATH": p, "FS_WORKTREE": "wX",
		})
		if err != nil {
			t.Fatalf("the careless caller failed on a branch the decision authorised: %v\n%s", err, out)
		}
		if gitHasBranch(t, repo, branch) {
			t.Fatalf("the careless caller left branch %s behind, so the rows above would pass for "+
				"a caller that never deletes anything:\n%s", branch, out)
		}
	})
}

// The other call site, end to end: the batch's batch-exit branch step, once per
// member, over a real fan-out. The member that refuses is the one whose branch
// is not merged, and the refusal has to name it — a fan-out's close is a loop
// over members, and a refusal that could only say "a branch was not deleted"
// would send its reader through five delivery records looking for which.
func TestBatchBranchStepNamesTheMemberItRefusesFor(t *testing.T) {
	_, root, batch, members := batchWithWorktreeMembers(t, "one", "two")
	refused := members[0]
	// A commit only this member's branch has: unmerged, and the only copy of it.
	goGit(t, refused.path, "-c", "user.email=fs@test.invalid", "-c", "user.name=fs test",
		"commit", "-q", "--allow-empty", "-m", "work only this member has")

	out, err := runBatchStep(t, batchBranchStep(t), root, batch)
	if err == nil {
		t.Fatalf("the batch's branch step passed over a member whose branch is not merged:\n%s", out)
	}
	if !strings.Contains(out, "member "+refused.worktree+":") {
		t.Errorf("the refusal must name the member it refused for (member %s); got:\n%s",
			refused.worktree, out)
	}
	if !gitHasBranch(t, root, refused.branch) {
		t.Errorf("the refusal deleted the branch it refused for: %s", refused.branch)
	}
}

// The finding with no guard at all, at the step that acted on it. git refuses to
// delete a branch another worktree holds — but only after the delivery's worktree
// has been detached for it, so the old step left the worktree detached and
// refused with git's sentence rather than with a decision of its own. The seam
// refuses first, and the worktree is untouched.
func TestCodeTeardownRefusesABranchAnotherWorktreeHolds(t *testing.T) {
	repo := scratchGitRepo(t)
	const branch = "worktree-delivery"
	goGit(t, repo, "worktree", "add", "-q", "-b", branch, filepath.Join(repo, "held"))
	delivery := filepath.Join(repo, "wt")
	goGit(t, repo, "worktree", "add", "-q", "--detach", delivery)
	goGit(t, delivery, "checkout", "-q", "--ignore-other-worktrees", branch)

	out, err := runTeardownStep(t, branchStep(t), repo, map[string]string{
		"FS_WORKTREE_PATH": delivery, "FS_WORKTREE": "wX",
	})
	if err == nil {
		t.Errorf("the step acted on a branch another worktree holds instead of refusing:\n%s", out)
	}
	if !gitHasBranch(t, repo, branch) {
		t.Fatalf("branch %s was deleted while another worktree had it checked out:\n%s", branch, out)
	}
	if head, attached := headBranch(t, delivery); !attached || head != branch {
		t.Errorf("the delivery's worktree was left detached (HEAD=%q attached=%v) by a step that "+
			"refused:\n%s", head, attached, out)
	}
	if !strings.Contains(out, "another worktree") {
		t.Errorf("the refusal must say the branch is another worktree's; got:\n%s", out)
	}
}
