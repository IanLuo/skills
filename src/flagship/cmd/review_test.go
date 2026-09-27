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
// Every test below runs the playbooks the binary ships, straight out of the
// binary and with nothing written to ~/.fs/kb, rather than restating them: a
// test that wrote its own copy of code's exit gate would keep passing after the
// shipped one drifted.
//
// The binary under test is deliberately kept OFF PATH. The gate shells out to
// `fs reviews $FS_NODE`, and an earlier version of this file put the binary on
// PATH as `fs` so it would resolve — which is exactly how the gate shipped
// broken: it worked in the test and died on `command not found` in every live
// close. A shipped step reaches fs through $FS_BIN, the running binary's own
// path, and these tests fail if it goes back to needing PATH.

import (
	"slices"
	"strings"
	"testing"
)

// reviewEnv prepares the environment a review-gate test runs in: the fake herdr
// for the delivery, and nothing else — the shipped playbooks resolve out of the
// binary, so the isolated home needs no KB at all. Nothing puts the binary on
// PATH.
func reviewEnv(t *testing.T) []string {
	t.Helper()
	env, _, _ := fakeHerdrOnPath(t)
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

// prepareWork runs a code work dispatch without --deliver: the plan is recorded
// and the cap node exists, but no pane was opened and no delivery was written.
// It is the shape fs pending calls unlinked and tells the cap to close with
// --abandoned — and the shape a close runs no exit step for at all.
func prepareWork(t *testing.T, bin, root string, env []string) string {
	t.Helper()
	resp, code := runFSEnv(t, bin, root, env, "dispatch",
		"--project", "skills", "--type", "dev-task", "--goal", "change the code",
		"--tag", "code", "--confirm", "--allow-unresolved")
	if code != 0 {
		t.Fatalf("dispatch exit %d: %v", code, resp["error"])
	}
	return resp["data"].(map[string]any)["cap_node_id"].(string)
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
	// the gate is about — and no override is needed to say so: the dispatch a
	// review names is what the review is about, not other open work it would
	// otherwise be piling on top of.
	work := capNodeOf(t, bin, root, "dispatch dev-task: the change", "dispatch")
	resp, code := runFS(t, bin, root, "dispatch", "--project", "skills", "--type", "review-task", "--goal", "review the change", "--reviews", work)
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
// the thing the flag exists to prevent — and whitespace is nothing, not a
// reason: three spaces record a waiver that says nothing at all.
func TestCLIWithoutReviewRequiresAReason(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)

	for _, reason := range []string{"", "   ", "\t\n"} {
		resp, code := runFS(t, bin, root, "close", "--node", "t-00000000", "--decision", "done", "--without-review", reason)
		if code == 0 {
			t.Fatalf("--without-review %q was accepted: %v", reason, resp)
		}
		if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "--without-review requires a reason") {
			t.Errorf("refusal %q must name the flag and what it is missing", errMsg)
		}
	}
}

// The gate itself, in the case it exists for: a change is finished, nothing
// reviewed it, and the close refuses — naming the fix rather than the absence.
// Then the one way past it, and the record that makes the skip visible.
func TestCLIReviewGateRefusesACloseWithNoReview(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	env := reviewEnv(t)

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

	// The waiver is a visible skip, not a review. It writes a decision and
	// nothing else, so it cannot answer fs reviews — otherwise the one way past
	// the gate would also be the one way to make the gate report it passed.
	// Refusing for the ordinary reason, and not because the close left the node
	// in some state fs reviews cannot read at all.
	resp, code = runFSEnv(t, bin, root, env, "reviews", capNode)
	if code == 0 {
		t.Errorf("fs reviews passed on a change whose review was waived: %v", resp)
	}
	refusal, _ = resp["error"].(string)
	if !strings.Contains(refusal, "--reviews "+capNode) {
		t.Errorf("fs reviews refusal %q must be the no-review-names-it refusal, naming the fix", refusal)
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
	env := reviewEnv(t)

	work, workWorker := deliverWork(t, bin, root, env)
	finishWorker(t, bin, root, workWorker)

	// The review is dispatched while the work is still open, which is the whole
	// point of the ordering — and it needs no override, because the dispatch it
	// names is the subject of this one rather than open work in the way.
	reviewResp, code := runFSEnv(t, bin, root, env, "dispatch", "--deliver",
		"--project", "skills", "--type", "review-task",
		"--goal", "review the change to the gate", "--reviews", work)
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

// A waiver names a skip, and there is a skip only where there was a gate to
// stand down. Two closes face none: an abandoned one, which runs no exit check
// at all, and one whose plan carries no review gate. Recording the reason
// anyway writes "waived the review gate" on a node whose close never met one —
// the record asserting the very thing the flag exists to make visible — so both
// refuse instead, and the refusal says which of the two it found.
func TestCLIWithoutReviewRefusesWhenNoGateWouldStandDown(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	env := reviewEnv(t)

	// A review dispatch is the plan with no gate in it: `code` is selected by
	// the declared `code` tag, which a --type review-task dispatch does not
	// carry — the same structural fact that keeps a review from demanding a
	// review of itself.
	work, workWorker := deliverWork(t, bin, root, env)
	finishWorker(t, bin, root, workWorker)
	reviewResp, code := runFSEnv(t, bin, root, env, "dispatch", "--deliver",
		"--project", "skills", "--type", "review-task",
		"--goal", "review the change", "--reviews", work)
	if code != 0 {
		t.Fatalf("dispatch review exit %d: %v", code, reviewResp["error"])
	}
	reviewData := reviewResp["data"].(map[string]any)
	review := reviewData["cap_node_id"].(string)
	finishWorker(t, bin, root, reviewData["worker_node"].(string))

	const waiver = "--without-review waived the review gate: docs-only"
	ungated, code := closeWork(t, bin, root, env, review, "--confirm", "--without-review", "docs-only")
	if code == 0 {
		t.Fatalf("a close with no review gate to waive accepted a waiver: %v", ungated)
	}
	if errMsg, _ := ungated["error"].(string); !strings.Contains(errMsg, "no review gate") {
		t.Errorf("refusal %q must say there is no gate to stand down", errMsg)
	}
	for _, decision := range decisionsOf(t, bin, root, env, review) {
		if decision == waiver {
			t.Errorf("the refused close recorded %q: nothing was skipped", decision)
		}
	}

	// And an abandoned close runs no exit check, so it faced no gate either. This
	// is a DELIVERED node, so it still reaches the refusal inside the exit run.
	abandoned, code := closeWork(t, bin, root, env, work,
		"--abandoned", "--reason", "the change was withdrawn", "--without-review", "docs-only")
	if code == 0 {
		t.Fatalf("an abandoned close accepted a waiver: %v", abandoned)
	}
	if errMsg, _ := abandoned["error"].(string); !strings.Contains(errMsg, "abandoned") {
		t.Errorf("refusal %q must say the close was abandoned, so no check ran", errMsg)
	}

	// The reproduction the review actually filed, and the case the delivered one
	// above cannot reach: a dispatch that was PREPARED and never delivered. The
	// refusal lived inside the exit run, which this close skips entirely, so it
	// recorded "waived the review gate" on a node whose cleanup was "no exit
	// phase" — asserting a skip that never happened. The refusal alone does not
	// catch that (the delivered case above refuses either way); the waiver not
	// being recorded is the assertion that does.
	prepared := prepareWork(t, bin, root, env)
	noWaiver, code := closeWork(t, bin, root, env, prepared,
		"--abandoned", "--reason", "never delivered", "--without-review", "docs-only")
	if code == 0 {
		t.Fatalf("a never-delivered close accepted a waiver: %v", noWaiver)
	}
	if errMsg, _ := noWaiver["error"].(string); !strings.Contains(errMsg, "--without-review") {
		t.Errorf("refusal %q must name the flag", errMsg)
	}
	for _, decision := range decisionsOf(t, bin, root, env, prepared) {
		if strings.Contains(decision, "waived the review gate") {
			t.Errorf("a close that ran no exit step recorded %q: no gate was faced", decision)
		}
	}
}

// fs reviews tells a typo from a wrong kind. Neither node is reviewed, but "you
// named no node" and "you named a node that is not a dispatch" send the cap to
// different fixes — the same two questions --reviews answers at write time, so
// the read side answers them the same way.
func TestCLIReviewsDistinguishesAMissingNodeFromANonDispatchNode(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)

	missing, code := runFS(t, bin, root, "reviews", "t-00000000")
	if code == 0 {
		t.Fatalf("fs reviews passed on a node that is not there: %v", missing)
	}
	missingMsg, _ := missing["error"].(string)
	for _, want := range []string{"t-00000000", "no node"} {
		if !strings.Contains(missingMsg, want) {
			t.Errorf("refusal %q must mention %q", missingMsg, want)
		}
	}

	gap := capNodeOf(t, bin, root, "a gap, never dispatched", "gap")
	wrongKind, code := runFS(t, bin, root, "reviews", gap)
	if code == 0 {
		t.Fatalf("fs reviews passed on a node that is not a dispatch: %v", wrongKind)
	}
	wrongKindMsg, _ := wrongKind["error"].(string)
	for _, want := range []string{gap, "not a dispatch node"} {
		if !strings.Contains(wrongKindMsg, want) {
			t.Errorf("refusal %q must mention %q", wrongKindMsg, want)
		}
	}
	if wrongKindMsg == missingMsg {
		t.Errorf("a missing node and a wrong-kind node refuse identically: %q", missingMsg)
	}
}

// A shipped step may only name what fs ships, and fs is one of those things —
// but `fs` is not on PATH, so naming it bare is naming nothing. Both env
// builders export FS_BIN, the running binary's own path, and this is the entry
// half of that contract: a check that runs it passes.
func TestCLIEntryGateReachesTheBinaryThroughFSBin(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)

	writeKBPlaybook(t, testHome(t), "fsbin-entry", `name: fsbin-entry
applies_when: fsbin
engine: herdr
agent: pi
entry:
  - check: [ -x "$FS_BIN" ] && "$FS_BIN" project list >/dev/null
`)

	resp, code := runFS(t, bin, root, "dispatch", "--project", "skills", "--type", "dev-task", "--goal", "run fs through FS_BIN", "--tag", "fsbin")
	if code != 0 {
		t.Fatalf("a check running $FS_BIN failed, exit %d: %v", code, resp["error"])
	}
}
