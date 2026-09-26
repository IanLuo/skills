package main

// The review gate: a close asks whether its own change was reviewed, and the
// only answer it accepts is a review dispatch that finished.
//
// The defect these tests are about is a sequencing one. `independent-review`
// already refused to close a review until every finding was dispositioned, but
// nothing dispatched a review and nothing noticed when one was skipped, so two
// changes that moved a locked acceptance criterion were closed before any
// review of them ran. A gate that runs after the close cannot fix that; the
// close has to ask, and it asks in the one place it can — an exit check.
//
// Every test below runs the playbooks the binary ships, seeded by fs bootstrap,
// rather than restating them: a test that wrote its own copy of code's exit
// gate would keep passing after the shipped one drifted. That is also why the
// binary under test goes on PATH as `fs` — the gate shells out to
// `fs reviews $FS_NODE` exactly as a live close does.

import (
	"slices"
	"strings"
	"testing"
)

// reviewEnv prepares the environment a review-gate test runs in: the fake herdr
// for the delivery, the built binary on PATH as `fs` for the gate that shells
// out to it, and the shipped playbooks seeded into the isolated home.
func reviewEnv(t *testing.T, bin, dir string) []string {
	t.Helper()
	env, _, _ := fakeHerdrOnPath(t)
	env = fsOnPath(t, bin, env)
	if resp, code := runFSEnv(t, bin, dir, env, "bootstrap"); code != 0 {
		t.Fatalf("bootstrap exit %d: %v", code, resp["error"])
	}
	return env
}

// deliverWork runs a code work dispatch for real and returns its cap node and
// its worker. --confirm is what carries it past code's entry checks, which
// assert things a fresh temp project cannot have; the exit gate is what this
// file is about, and --confirm answers no check.
func deliverWork(t *testing.T, bin, root string, env []string) (capNode, workerRef string) {
	t.Helper()
	capNode, _, _, workerRef = deliverProbeGoal(t, bin, root, env, "change the code", "--tag", "code", "--confirm")
	return capNode, workerRef
}

// closeWork closes a delivered dispatch. The response is returned rather than
// asserted on, because a refusal is the measurement in half these tests.
func closeWork(t *testing.T, bin, root string, env []string, capNode string, extra ...string) (map[string]any, int) {
	t.Helper()
	args := append([]string{"close", "--node", capNode, "--decision", "the work is done"}, extra...)
	return runFSEnv(t, bin, root, env, args...)
}

// decisionsOf reads a cap node's recorded decisions.
func decisionsOf(t *testing.T, bin, root string, env []string, node string) []string {
	t.Helper()
	resp, code := runFSEnv(t, bin, root, env, "task", "get", node, "--project", "cap")
	if code != 0 {
		t.Fatalf("task get %s exit %d: %v", node, code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	raw, _ := data["decisions"].([]any)
	out := make([]string, 0, len(raw))
	for _, d := range raw {
		out = append(out, d.(string))
	}
	return out
}

// planOf lists the playbook names a dispatch's plan resolved.
func planOf(t *testing.T, resp map[string]any) []string {
	t.Helper()
	raw, ok := resp["data"].(map[string]any)["plan"].([]any)
	if !ok {
		t.Fatalf("dispatch response carries no plan: %v", resp["data"])
	}
	names := make([]string, 0, len(raw))
	for _, entry := range raw {
		names = append(names, entry.(map[string]any)["name"].(string))
	}
	return names
}

// --reviews refuses a node that is not a dispatch, and a node that is not
// there, and it refuses them differently: "you named the wrong kind of node"
// and "you named nothing" send the cap to different fixes.
func TestCLIDispatchReviewsRefusesANonDispatchNodeAndAMissingOne(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), codeEntryPb, herdrTestPlaybook)

	gap := capNodeOf(t, bin, root, "a gap, never dispatched", "gap")
	resp, code := runFS(t, bin, root, "dispatch", "--project", "skills", "--type", "review-task", "--goal", "review the change", "--reviews", gap)
	if code == 0 {
		t.Fatalf("--reviews naming a gap node must be refused, got %v", resp)
	}
	errMsg, _ := resp["error"].(string)
	for _, want := range []string{gap, "not a dispatch node", "--reviews"} {
		if !strings.Contains(errMsg, want) {
			t.Errorf("refusal %q must mention %q", errMsg, want)
		}
	}

	resp, code = runFS(t, bin, root, "dispatch", "--project", "skills", "--type", "review-task", "--goal", "review the change", "--reviews", "t-00000000")
	if code == 0 {
		t.Fatalf("--reviews naming no node must be refused, got %v", resp)
	}
	if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "t-00000000") {
		t.Errorf("refusal %q must name the node", errMsg)
	}
}

// A valid link lands on the reviewing dispatch's own task-created payload,
// which is what makes "which change does this review read?" a structural read
// rather than a parse of the goal.
func TestCLIDispatchRecordsTheReviewsLink(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), codeEntryPb, herdrTestPlaybook)

	// The work is still open when its review is dispatched — that is the order
	// the gate is about — so --allow-unresolved is what says so.
	work := capNodeOf(t, bin, root, "dispatch dev-task: the change", "dispatch")
	resp, code := runFS(t, bin, root, "dispatch", "--project", "skills", "--type", "review-task", "--goal", "review the change", "--reviews", work, "--allow-unresolved")
	if code != 0 {
		t.Fatalf("dispatch --reviews exit %d: %v", code, resp["error"])
	}
	review := resp["data"].(map[string]any)["cap_node_id"].(string)

	log, code := runFS(t, bin, root, "log", "--project", "cap", "--node", review, "--type", "task-created")
	if code != 0 {
		t.Fatalf("log exit %d: %v", code, log["error"])
	}
	events := log["data"].(map[string]any)["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("the review has %d task-created events, want 1", len(events))
	}
	payload, _ := events[0].(map[string]any)["payload"].(map[string]any)
	if payload["reviews"] != work {
		t.Errorf("task-created payload = %v, want reviews %s", payload, work)
	}
	if payload["integrates"] != nil {
		t.Errorf("task-created payload = %v, want no integrates: a review reads, it does not merge", payload)
	}
}

// The escape has to carry a reason. --without-review with nothing after it is
// refused rather than read as an empty waiver, because an unrecorded skip is
// the thing the flag exists to prevent.
func TestCLIWithoutReviewRequiresAReason(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)

	resp, code := runFS(t, bin, root, "close", "--node", "t-00000000", "--decision", "done", "--without-review", "")
	if code == 0 {
		t.Fatalf("--without-review with no reason was accepted: %v", resp)
	}
	if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "--without-review requires a reason") {
		t.Errorf("refusal %q must name the flag and what it is missing", errMsg)
	}
}

// The gate itself, in the case it exists for: a change is finished, nothing
// reviewed it, and the close refuses — naming the fix rather than the absence.
// Then the one way past it, and the record that makes the skip visible.
func TestCLIReviewGateRefusesACloseWithNoReview(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	env := reviewEnv(t, bin, root)

	capNode, workerRef := deliverWork(t, bin, root, env)

	// Nothing names this node, and fs reviews says so with the command that
	// would. The refusal is the gate's own output, byte for byte.
	resp, code := runFSEnv(t, bin, root, env, "reviews", capNode)
	if code == 0 {
		t.Fatalf("fs reviews passed with no review of %s: %v", capNode, resp)
	}
	errMsg, _ := resp["error"].(string)
	for _, want := range []string{capNode, "--type review-task", "--reviews " + capNode} {
		if !strings.Contains(errMsg, want) {
			t.Errorf("fs reviews refusal %q must mention %q", errMsg, want)
		}
	}

	finishWorker(t, bin, root, workerRef)

	closed, code := closeWork(t, bin, root, env, capNode, "--confirm")
	if code == 0 {
		t.Fatalf("close passed with no review of the change: %v", closed)
	}
	refusal, _ := closed["error"].(string)
	if !strings.Contains(refusal, "playbook code") {
		t.Errorf("close refusal %q must name the playbook the gate is in", refusal)
	}
	for _, want := range []string{"--type review-task", "--reviews " + capNode} {
		if !strings.Contains(refusal, want) {
			t.Errorf("close refusal %q must name the fix (%q), not only the absence", refusal, want)
		}
	}

	// The escape proceeds, and the reason lands on the node: a skipped review
	// is visible, not impossible.
	const reason = "the change is the gate protocol itself and the cap is reviewing it live"
	closed, code = closeWork(t, bin, root, env, capNode, "--confirm", "--without-review", reason)
	if code != 0 {
		t.Fatalf("close --without-review exit %d: %v", code, closed["error"])
	}
	want := "--without-review waived the review gate: " + reason
	if decisions := decisionsOf(t, bin, root, env, capNode); !slices.Contains(decisions, want) {
		t.Errorf("decisions = %q, want the waiver recorded as %q", decisions, want)
	}
}

// The honest cycle, end to end, under the gate: work is dispatched, the review
// is dispatched with --reviews, the review closes, and only then does the work
// close — with no override, because the gate had nothing to refuse.
//
// The review dispatch is the recursion guard as well. `code` matches a declared
// `code` tag and `independent-review` matches `type=review-task`; if a review
// inherited the gate that demands a review of the thing it reviews, no review
// could ever close. So the plan is asserted to carry the reviewer and not
// `code`, and the review is closed with --confirm and nothing else.
func TestCLIReviewGatePassesWhenADoneReviewNamesTheChange(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	env := reviewEnv(t, bin, root)

	work, workWorker := deliverWork(t, bin, root, env)
	finishWorker(t, bin, root, workWorker)

	// The review is dispatched while the work is still open, which is the whole
	// point of the ordering, and --allow-unresolved is the flag that says so.
	reviewResp, code := runFSEnv(t, bin, root, env, "dispatch", "--deliver",
		"--project", "skills", "--type", "review-task",
		"--goal", "review the change to the gate", "--reviews", work, "--allow-unresolved")
	if code != 0 {
		t.Fatalf("dispatch review exit %d: %v", code, reviewResp["error"])
	}
	data := reviewResp["data"].(map[string]any)
	review := data["cap_node_id"].(string)
	reviewWorker := data["worker_node"].(string)

	names := planOf(t, reviewResp)
	if !slices.Contains(names, independentReviewPb) {
		t.Errorf("review plan %v does not carry the reviewer", names)
	}
	if slices.Contains(names, codePb) {
		t.Errorf("review plan %v carries code, so the review would demand a review of itself", names)
	}

	// A review that names this change but has not finished is not an answer:
	// "not done" and "not there at all" are different facts, and fs reviews
	// says which one it found.
	resp, code := runFSEnv(t, bin, root, env, "reviews", work)
	if code == 0 {
		t.Fatalf("fs reviews passed on a review that has not closed: %v", resp)
	}
	errMsg, _ := resp["error"].(string)
	if !strings.Contains(errMsg, review) || !strings.Contains(errMsg, "not done") {
		t.Errorf("fs reviews refusal %q must name the open review %s and say it is not done", errMsg, review)
	}

	finishWorker(t, bin, root, reviewWorker)
	closed, code := closeWork(t, bin, root, env, review, "--confirm")
	if code != 0 {
		t.Fatalf("the review could not close with neither a review of itself nor an override: %v", closed["error"])
	}

	resp, code = runFSEnv(t, bin, root, env, "reviews", work)
	if code != 0 {
		t.Fatalf("fs reviews refused a done review of %s: %v", work, resp["error"])
	}
	if got := resp["data"].(map[string]any)["reviewed_by"]; got != review {
		t.Errorf("fs reviews reported reviewed_by %v, want %s", got, review)
	}

	// Only now. The close passes on the gate's own terms: no --without-review,
	// and no waiver on the node's record.
	closed, code = closeWork(t, bin, root, env, work, "--confirm")
	if code != 0 {
		t.Fatalf("close refused a change with a done review: %v", closed["error"])
	}
	for _, decision := range decisionsOf(t, bin, root, env, work) {
		if strings.Contains(decision, "waived the review gate") {
			t.Errorf("the close recorded a waiver it did not use: %q", decision)
		}
	}
}
