package main

// The postcondition that asserts each delivery's branch is gone by the time the
// close finishes, run for real against the shipped step bodies.
//
// Every gate in this repo before it was about something that had been REMOVED
// and could be looked for: a checkout on disk, a registration in git's listing,
// a workspace in herdr. The branch had no gate at all, and it is the one
// resource the teardown destroys with no copy anywhere else — the tree can be
// re-made from the branch, and nothing can re-make the branch. So a branch step
// that skipped in silence, or that deleted a branch which was not the delivery's,
// left every gate green and the branch behind.
//
// The step therefore cannot be verified by inspecting the tree: by the time it
// runs, the tree is what the teardown just removed. It is verified against the
// RECORD — the branch the delivery wrote down when it created the worktree — and
// against git, which is the only thing that can say whether a name still
// resolves. §"the refusal names the branch" below is the point of the whole step;
// the passing and standing-down cases only keep it from being a gate that refuses
// everything or one that fires over a branch its delivery never made.
//
// The batch form is the same assertion held over the fan-out's members, one at a
// time, and it has to name WHICH member's branch survived: a refusal that says "a
// branch was not deleted" over a batch of five names nothing a reader can act on.
// Its own file's tests build a real batch and run the shipped step against it.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The refusal, first: a branch the delivery created is still there when the
// close reaches its postcondition. This is the case the step exists for, and the
// message has to name the branch — the reader's next move is to go and look at
// it, and "a branch survived" sends them looking through the record for which.
func TestCodeBranchPostconditionRefusesABranchLeftBehind(t *testing.T) {
	repo := scratchGitRepo(t)
	const branch = "worktree-delivery"
	goGit(t, repo, "branch", branch)

	out, err := runTeardownStep(t, branchPostcondition(t), repo, map[string]string{
		"FS_BRANCH": branch, "FS_WORKTREE": "wX", "FS_WORKTREE_PATH": filepath.Join(repo, "wt"),
	})
	if err == nil {
		t.Fatalf("the postcondition passed with branch %s still present:\n%s", branch, out)
	}
	if !strings.Contains(out, branch) {
		t.Errorf("the refusal must name the branch that survived; got:\n%s", out)
	}
	if !gitHasBranch(t, repo, branch) {
		t.Fatalf("the postcondition deleted branch %s: a postcondition asserts, it does not act:\n%s",
			branch, out)
	}
}

// Gone is the goal state, and the step has to say so — a gate that refuses
// everything is not a gate. The delivery's branch was merged and deleted, which
// is what the teardown's branch step does when it works.
func TestCodeBranchPostconditionPassesWhenTheBranchIsGone(t *testing.T) {
	repo := scratchGitRepo(t)

	out, err := runTeardownStep(t, branchPostcondition(t), repo, map[string]string{
		"FS_BRANCH": "worktree-delivery", "FS_WORKTREE": "wX",
	})
	if err != nil {
		t.Fatalf("the postcondition refused a branch that is gone: %v\n%s", err, out)
	}
}

// A delivery that created no worktree is not responsible for any branch, so it
// records none — and the step stands down on that empty record. The repository
// is holding a branch while it does, which is the whole point: the stand-down has
// to be a decision about the record, not something the step stumbles into on an
// empty repository. This is what tells "the branch was not mine to remove" from
// "the branch I was responsible for is still here".
func TestCodeBranchPostconditionStandsDownWhenTheDeliveryMadeNoBranch(t *testing.T) {
	repo := scratchGitRepo(t)
	const notMine = "somebody-elses-branch"
	goGit(t, repo, "branch", notMine)

	out, err := runTeardownStep(t, branchPostcondition(t), repo, map[string]string{
		"FS_BRANCH": "", "FS_WORKTREE": "wX",
	})
	if err != nil {
		t.Fatalf("the postcondition refused over a branch its delivery never made: %v\n%s", err, out)
	}
	if !gitHasBranch(t, repo, notMine) {
		t.Fatalf("branch %s was deleted by a postcondition:\n%s", notMine, out)
	}
}

// A member of a batch leaves its branch to the batch. The member's own close
// runs this same step, and its branch is still there by design — the batch
// removes every member's at once, after every member is integrated. So the step
// stands down on FS_BATCH, and the batch's own postcondition is where the
// assertion lives: same assertion, one layer out, so no member's branch goes
// unasserted.
func TestCodeBranchPostconditionDefersToTheBatch(t *testing.T) {
	repo := scratchGitRepo(t)
	const branch = "worktree-delivery"
	goGit(t, repo, "branch", branch)

	out, err := runTeardownStep(t, branchPostcondition(t), repo, map[string]string{
		"FS_BRANCH": branch, "FS_WORKTREE": "wX", "FS_BATCH": "t-batch",
	})
	if err != nil {
		t.Fatalf("a member's postcondition must defer to its batch: %v\n%s", err, out)
	}
	if !gitHasBranch(t, repo, branch) {
		t.Fatalf("branch %s is the batch's to remove, and the member's close removed it:\n%s", branch, out)
	}
}

// git answers three ways, and exactly one of them passes. Outside a repository
// git cannot answer at all, and a step that read every non-zero exit as "gone"
// would certify a close on a git it never reached — the same fail-open shape the
// workspace postcondition avoids by passing only on workspace_not_found. This is
// the direction the step errs in: it refuses a check it could not evaluate rather
// than passing it.
func TestCodeBranchPostconditionRefusesWhenItCannotAskGit(t *testing.T) {
	notARepo := t.TempDir()

	out, err := runTeardownStep(t, branchPostcondition(t), notARepo, map[string]string{
		"FS_BRANCH": "worktree-delivery", "FS_WORKTREE": "wX",
	})
	if err == nil {
		t.Fatalf("the postcondition passed where git could not be asked:\n%s", out)
	}
	if !strings.Contains(out, "cannot tell") {
		t.Errorf("the refusal must say it could not tell, not that the branch is there; got:\n%s", out)
	}
}

// The teardown reported success and deleted the wrong branch.
//
// The branch step names the branch by matching the recorded path against git's
// listing, so a delivery record that names a DIFFERENT worktree than the one it
// created sends it to delete that other worktree's branch — merged, guarded, the
// step exits 0 — while the branch this delivery actually made stays. Nothing in
// the step can see this: it did exactly what its input told it to. Only the
// record can, and only a postcondition reads it. Everything here is on the happy
// path; the mismatch is the sole defect.
func TestCodeBranchPostconditionRefusesWhenTheTeardownDeletedADifferentBranch(t *testing.T) {
	repo := scratchGitRepo(t)
	const deliveryBranch = "worktree-delivery"
	const otherBranch = "another-deliverys"

	// The delivery's own tree, and a second worktree standing in for a record
	// that names the wrong one.
	own := filepath.Join(repo, "wt-own")
	goGit(t, repo, "worktree", "add", "-q", "-b", deliveryBranch, own)
	other := filepath.Join(repo, "wt-other")
	goGit(t, repo, "worktree", "add", "-q", "-b", otherBranch, other)

	// The teardown as it ran: the branch step, told about the wrong worktree,
	// deletes that other branch and reports success.
	out, err := runTeardownStep(t, branchStep(t), repo, map[string]string{
		"FS_WORKTREE_PATH": other, "FS_WORKTREE": "wX",
	})
	if err != nil {
		t.Fatalf("the branch step refused a merged branch of a plain worktree: %v\n%s", err, out)
	}
	if gitHasBranch(t, repo, otherBranch) {
		t.Fatalf("the setup did not reach the state: %s should have been deleted:\n%s", otherBranch, out)
	}
	if !gitHasBranch(t, repo, deliveryBranch) {
		t.Fatalf("the setup did not reach the state: %s should have survived:\n%s", deliveryBranch, out)
	}

	// Every gate the close had before this one: the tree is gone (the branch
	// step detached and deleted, then the tree step removed it) and git registers
	// no worktree there. Both pass. The branch the delivery made is still on
	// disk.
	if out, err := runTeardownStep(t, treeStep(t), repo, map[string]string{
		"FS_WORKTREE_PATH": other, "FS_WORKTREE": "wX",
	}); err != nil {
		t.Fatalf("the tree step refused: %v\n%s", err, out)
	}
	if out, err := runTeardownStep(t, registrationCheck(t), repo, map[string]string{
		"FS_WORKTREE_PATH": other, "FS_WORKTREE": "wX",
	}); err != nil {
		t.Fatalf("the pre-existing registration postcondition refused a teardown that removed its worktree: %v\n%s", err, out)
	}

	// The postcondition over the record, which is the only thing that knows which
	// branch was the delivery's.
	out, err = runTeardownStep(t, branchPostcondition(t), repo, map[string]string{
		"FS_BRANCH": deliveryBranch, "FS_WORKTREE": "wX", "FS_WORKTREE_PATH": own,
	})
	if err == nil {
		t.Fatalf("the postcondition passed with the delivery's branch %s still present:\n%s", deliveryBranch, out)
	}
	if !strings.Contains(out, deliveryBranch) {
		t.Errorf("the refusal must name the branch that survived; got:\n%s", out)
	}
}

// The silent skip, which is the other half of the class: the branch step exits 0
// and says nothing, and the branch it was supposed to delete is still there.
//
// The state is reached without a defect. `git worktree remove` drops the
// registration and the directory; the branch stays, because it is the branch's
// that git keeps and the tree's that it removes. A close that runs after that —
// the delivery's own record still naming the tree — finds no registration to name
// a branch from, and a step that treats "no registration" as "nothing to do"
// exits 0. Every postcondition the close had before this one passes over the
// result, because the tree really is gone and git really does not register it.
// The branch is the only thing left, and nothing was looking at it.
func TestCodeBranchPostconditionCatchesTheSilentSkip(t *testing.T) {
	repo := scratchGitRepo(t)
	const branch = "worktree-delivery"
	checkout := filepath.Join(repo, "wt")
	goGit(t, repo, "worktree", "add", "-q", "-b", branch, checkout)

	// The teardown ran once already: the registration and the directory are gone,
	// the branch is not.
	goGit(t, repo, "worktree", "remove", checkout)
	if !gitHasBranch(t, repo, branch) {
		t.Fatalf("git worktree remove deleted branch %s; the state this test is about is the one "+
			"where the branch outlives the tree", branch)
	}
	env := map[string]string{"FS_WORKTREE_PATH": checkout, "FS_WORKTREE": "wX"}

	// The branch step, on a record naming a tree git no longer registers: exit 0,
	// no message, nothing done.
	out, err := runTeardownStep(t, branchStep(t), repo, env)
	if err != nil {
		t.Fatalf("the branch step refused a path git registers no worktree at: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("the branch step spoke on a silent skip; the point is that it passes quietly:\n%s", out)
	}
	if !gitHasBranch(t, repo, branch) {
		t.Fatalf("branch %s was deleted, so the silent skip was not reproduced:\n%s", branch, out)
	}

	// The gates that were there before it, on the same state: the tree postcondition
	// passes because git registers no worktree at the path, and the tree step is
	// the no-op prune. This is the close reporting success over a branch it left
	// behind, which is the whole defect.
	if out, err := runTeardownStep(t, treeStep(t), repo, env); err != nil {
		t.Fatalf("the tree step refused: %v\n%s", err, out)
	}
	if out, err := runTeardownStep(t, registrationCheck(t), repo, env); err != nil {
		t.Fatalf("the pre-existing registration postcondition refused: %v\n%s", err, out)
	}

	// And the one gate that reads the record.
	out, err = runTeardownStep(t, branchPostcondition(t), repo, map[string]string{
		"FS_BRANCH": branch, "FS_WORKTREE": "wX", "FS_WORKTREE_PATH": checkout,
	})
	if err == nil {
		t.Fatalf("the postcondition passed over a silently skipped branch %s:\n%s", branch, out)
	}
	if !strings.Contains(out, branch) {
		t.Errorf("the refusal must name the branch that survived; got:\n%s", out)
	}
}

// branchPostcondition is the shipped postcondition that asserts the delivery's
// branch is gone. `refs/heads/$b` is what tells it from every other check in the
// playbook — the branch name it hands git is the only one spelled from a
// variable.
func branchPostcondition(t *testing.T) string {
	t.Helper()
	return shippedStep(t, shippedDefault(t, codePb), "check", "refs/heads/$b")
}

// batchBranchPostcondition is the batch's form of the same assertion: the same
// three answers from git, held over the members `fs batch` reports, each member's
// branch read from that member's own delivery record.
func batchBranchPostcondition(t *testing.T) string {
	t.Helper()
	return shippedStep(t, shippedDefault(t, batchPb), "check", "refs/heads/$b")
}

// batchMember is one member as `fs batch` reports it — the step's own input,
// read the way the step reads it, so a test asserts over the data the step will
// see rather than over a re-derivation of it.
type batchMember struct {
	node     string
	branch   string
	worktree string
	path     string
}

// batchMemberBranches reads what `fs batch` reports for a batch.
func batchMemberBranches(t *testing.T, bin, root, batch string) []batchMember {
	t.Helper()
	env, _, _ := fakeHerdrOnPath(t)
	resp, code := runFSEnv(t, bin, root, env, "batch", batch)
	if code != 0 {
		t.Fatalf("fs batch %s exit %d: %v", batch, code, resp["error"])
	}
	result, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatalf("fs batch data = %T, want an object", resp["data"])
	}
	raw, _ := result["members"].([]any)
	members := make([]batchMember, 0, len(raw))
	for _, m := range raw {
		object, _ := m.(map[string]any)
		member := batchMember{}
		member.node, _ = object["node"].(string)
		member.branch, _ = object["branch"].(string)
		member.worktree, _ = object["worktree"].(string)
		member.path, _ = object["worktree_path"].(string)
		members = append(members, member)
	}
	return members
}

// runBatchStep runs a shipped batch-exit step the way the engine runs it: from
// the project root, with the batch's node in $FS_NODE and the store in $HOME, so
// the `fs batch` inside the step reads the batch this test built.
func runBatchStep(t *testing.T, body, root, batch string) (string, error) {
	t.Helper()
	return runTeardownStep(t, body, root, map[string]string{
		"HOME": testHome(t), "FS_NODE": batch, "FS_BATCH": batch,
	})
}

// The batch's refusal names the member whose branch survived. A fan-out's close
// is a loop over members, and a refusal that could only say "a branch was not
// deleted" would send its reader through five records looking for which — the
// one thing the loop already knows.
//
// The state is the happy path: both members' worktrees were made and integrated,
// and the batch's branch step has not run yet, so both branches are still there.
// The step must refuse, and it must refuse at the first member it finds — naming
// that member and its branch rather than the batch.
func TestBatchBranchPostconditionRefusesNamingTheMember(t *testing.T) {
	_, root, batch, members := batchWithWorktreeMembers(t, "one", "two")

	out, err := runBatchStep(t, batchBranchPostcondition(t), root, batch)
	if err == nil {
		t.Fatalf("the batch postcondition passed with both members' branches present:\n%s", out)
	}

	// It names a member and that member's branch — the two facts a reader acts
	// on. Which member the loop reached first is its order, not something to
	// assert; that it named one of them, and named that one's own branch, is.
	named := false
	for _, m := range members {
		if strings.Contains(out, m.node) && strings.Contains(out, m.branch) {
			named = true
		}
	}
	if !named {
		t.Errorf("the refusal must name the member whose branch survived, and that branch; got:\n%s\nmembers: %v",
			out, members)
	}

	// It asserted, and did not act: every member's branch is still there.
	for _, m := range members {
		if !gitHasBranch(t, root, m.branch) {
			t.Errorf("branch %s was deleted by a postcondition", m.branch)
		}
	}
}

// Every member's branch gone is the state the batch's close is for, and the step
// has to pass it — one member's branch being gone says nothing about the rest, so
// a loop that stopped at the first would certify the others.
//
// The branches are removed by the batch's OWN teardown steps, read from the
// shipped playbook and run the way the engine runs them, not by a test tidying up
// with `git branch -D`. That is what makes the pass a statement about the real
// close: the same steps that delete the branches, and the postcondition that
// reads them afterwards.
func TestBatchBranchPostconditionPassesAfterTheTeardownItselfRuns(t *testing.T) {
	_, root, batch, members := batchWithWorktreeMembers(t, "one", "two")

	batchPb := shippedDefault(t, batchPb)
	for _, kind := range []string{"branch-decision.sh", "worktree remove"} {
		step := shippedStep(t, batchPb, "do", kind)
		if out, err := runBatchStep(t, step, root, batch); err != nil {
			t.Fatalf("the batch's own teardown step (%s) refused: %v\n%s", kind, err, out)
		}
	}
	for _, m := range members {
		if gitHasBranch(t, root, m.branch) {
			t.Fatalf("the teardown left branch %s behind, so this test would pass for the wrong reason", m.branch)
		}
	}

	out, err := runBatchStep(t, batchBranchPostcondition(t), root, batch)
	if err != nil {
		t.Fatalf("the batch postcondition refused a fan-out its own teardown had removed: %v\n%s", err, out)
	}
}

// The read is a gate too, and this is the case that was found in `nix build`:
// the batch's steps read the members through jq, the build's check phase carried
// git and not jq, and `sh` printed `jq: command not found`, wrote no rows, and
// the pipeline exited 0. So the branch step deleted nothing and passed, and the
// postcondition below it certified: a check whose body never ran reported success
// over every member. That is the defect this whole task is about, one layer in —
// a gate that cannot read its input and does not say so.
//
// A missing jq is "cannot tell", not "nothing to do": the members are unknown, so
// nothing about them can be asserted and nothing may be certified. Both steps now
// read in two assignments and refuse on either failing, and this test is what
// holds them to it. It refuses rather than passing, and it does not act — every
// member's branch is still there afterwards, which is what a step that could not
// read its input is allowed to have done.
//
// The PATH is built rather than emptied: a directory holding a symlink to git and
// nothing else. That is deliberate, because on a machine where git and jq share a
// bin directory — this one does, both in ~/.nix-profile/bin — a PATH pointed at
// git's own directory still resolves jq, and a test written that way would prove
// nothing while looking like it did.
func TestBatchStepsRefuseWhenTheyCannotReadTheMemberRecords(t *testing.T) {
	_, root, batch, members := batchWithWorktreeMembers(t, "one", "two")
	path := pathWithGitAlone(t)

	steps := []struct {
		name string
		body string
	}{
		{"the branch step", batchBranchStep(t)},
		{"the branch postcondition", batchBranchPostcondition(t)},
	}
	for _, step := range steps {
		out, err := runTeardownStep(t, step.body, root, map[string]string{
			"HOME": testHome(t), "FS_NODE": batch, "FS_BATCH": batch, "PATH": path,
		})
		if err == nil {
			t.Errorf("%s passed with no jq to read the members through, so it certified a batch it "+
				"never read:\n%s", step.name, out)
			continue
		}
		// The refusal has to be about the read. Without jq the step could also fall
		// over later and refuse for an unrelated reason, which would look the same
		// from the outside and would not be this guard.
		if !strings.Contains(out, "jq") {
			t.Errorf("%s refused, but not because it could not read the members through jq; got:\n%s",
				step.name, out)
		}
	}

	// It refused without acting: nothing was deleted by a step that could not tell
	// whose branch it was looking at.
	for _, m := range members {
		if !gitHasBranch(t, root, m.branch) {
			t.Errorf("branch %s was deleted by a step that could not read the member records", m.branch)
		}
	}
}

// batchBranchStep is the shipped batch step that frees each member's branch — the
// destructive one, and the one whose silent no-op left branches behind.
func batchBranchStep(t *testing.T) string {
	t.Helper()
	return shippedStep(t, shippedDefault(t, batchPb), "do", "which member's branch to free")
}

// pathWithGitAlone is a PATH that resolves git and deliberately does not resolve
// jq: a directory of its own holding a symlink to this machine's git. git runs
// from it — it resolves its own exec path through the symlink — and nothing else
// is there to be found. jq is asserted absent rather than assumed, because the
// whole test is only worth anything if it really is.
func pathWithGitAlone(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is not on PATH, so a PATH with git and without jq cannot be built: %v", err)
	}
	dir := t.TempDir()
	if err := os.Symlink(git, filepath.Join(dir, "git")); err != nil {
		t.Fatalf("symlinking %s into the restricted PATH: %v", git, err)
	}
	// Checked through the same shell the steps run under, so what is asserted is
	// what the step will experience rather than what this process's PATH holds.
	probe := exec.Command("sh", "-c", "command -v jq")
	probe.Env = []string{"PATH=" + dir}
	if out, err := probe.CombinedOutput(); err == nil {
		t.Fatalf("jq resolves at %q through the restricted PATH, so the steps would read fine and this "+
			"test would prove nothing", strings.TrimSpace(string(out)))
	}
	return dir
}

// batchWithWorktreeMembers builds a real batch the way a cap does: a batch node,
// one member per goal dispatched with --worktree-new so its delivery records the
// branch herdr created, and a done integration naming each so `fs batch` reports
// the fan-out as complete. It returns the binary, the project root, the batch
// node, and the members `fs batch` reports.
//
// Nothing here is stood in for: the branches are real branches in a real
// repository, made by the same `herdr worktree create` path a live dispatch uses,
// and recorded by the same delivery that records a live one's.
func batchWithWorktreeMembers(t *testing.T, goals ...string) (bin, root, batch string, members []batchMember) {
	t.Helper()
	bin = getFS(t)
	root = scratchGitRepo(t)
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), teardownEntryPb, `name: teardown-entry
applies_when: type=dev-task
engine: herdr
agent: pi
entry:
  - check: true
`)
	// The integration's own protocol is not what this test is about; it exists
	// here only to make the fan-out read as complete, so it is cut down to a
	// close that simply succeeds.
	writeKBPlaybook(t, testHome(t), integrationPb, `name: integration
applies_when: type=integrate
engine: herdr
agent: pi
entry:
  - check: true
exit:
  - check: true
`)
	env, _, _ := fakeHerdrOnPath(t)
	env = fsOnPath(t, bin, env)

	batch = capNodeOf(t, bin, root, "the batch", "batch")
	for _, goal := range goals {
		// --allow-unresolved because a fan-out is by definition several
		// dispatches in flight at once: the cap dispatches member two while
		// member one is still open. The override is recorded on the node, which
		// is the same thing a live fan-out does.
		resp, code := runFSEnv(t, bin, root, env, "dispatch", "--deliver", "--allow-unresolved",
			"--project", "skills", "--type", "dev-task", "--goal", goal,
			"--batch", batch, "--worktree-new")
		if code != 0 {
			t.Fatalf("dispatch --worktree-new member %s exit %d: %v", goal, code, resp["error"])
		}
		data := resp["data"].(map[string]any)
		member := data["cap_node_id"].(string)
		finishWorker(t, bin, root, data["worker_node"].(string))

		// The integration that makes the member count as done, closed so
		// `fs batch` reads the fan-out as complete.
		integ, code := runFSEnv(t, bin, root, env, "dispatch", "--deliver", "--allow-unresolved",
			"--project", "skills", "--type", "integrate", "--goal", "merge "+goal,
			"--integrates", member)
		if code != 0 {
			t.Fatalf("dispatch --integrates %s exit %d: %v", member, code, integ["error"])
		}
		integData := integ["data"].(map[string]any)
		finishWorker(t, bin, root, integData["worker_node"].(string))
		if _, code := runFSEnv(t, bin, root, env, "close", "--node", integData["cap_node_id"].(string),
			"--decision", "merged the member"); code != 0 {
			t.Fatalf("closing the integration of %s failed", member)
		}
	}

	members = batchMemberBranches(t, bin, root, batch)
	if len(members) != len(goals) {
		t.Fatalf("fs batch reports %d members, want %d: %v", len(members), len(goals), members)
	}
	for _, m := range members {
		if m.branch == "" {
			t.Fatalf("member %s records no branch, so nothing here would test the fan-out: %v", m.node, members)
		}
	}
	return bin, root, batch, members
}
