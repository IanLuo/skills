package main

// A review is a node kind with a link, never a gate.
//
// `fs dispatch --reviews CAP_NODE` records which dispatch a review reads, on the
// review's own task-created payload, and `fs reviews CAP_NODE` answers from that
// link: a done review that names the node. Nothing else is read — no plan, no
// revision, no exit check — so these tests are about the link in both
// directions and about the two refusals that are about the kind of node named.

import (
	"strings"
	"testing"
)

// --reviews refuses a node that is not a dispatch, and a node that is not
// there, and it refuses them differently: "you named the wrong kind of node"
// and "you named nothing" send the cap to different fixes.
func TestCLIDispatchReviewsRefusesANonDispatchNodeAndAMissingOne(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)

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

	// The work is still open when its review is dispatched — that is the order
	// the relation is about — and no override is needed to say so: the dispatch
	// a review names is what the review is about, not open work it is piling on.
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

// fs reviews answers from the link: an open review is named as open, a done one
// answers, and a dispatch no review names is refused with the route to one.
func TestCLIReviewsAnswersFromTheLink(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)

	work := capNodeOf(t, bin, root, "dispatch dev-task: the change", "dispatch")

	resp, code := runFS(t, bin, root, "reviews", work)
	if code == 0 {
		t.Fatalf("an unreviewed dispatch must exit 1, got %v", resp)
	}
	if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "--reviews") {
		t.Errorf("refusal %q must name the route to a review", errMsg)
	}

	review := capNodeOf(t, bin, root, "dispatch review: read the change", "dispatch")
	if resp, code := runFS(t, bin, root, "task", "update", review, "--status", "done", "--decision", "reviewed", "--project", "cap"); code != 0 {
		t.Fatalf("task update exit %d: %v", code, resp["error"])
	}

	// The review names nothing yet, so it answers for nothing.
	if resp, code := runFS(t, bin, root, "reviews", work); code == 0 {
		t.Fatalf("a done review that names nothing must not answer, got %v", resp)
	}

	// Re-record the link by dispatching the review the way the cap does.
	resolveCapScope(t, bin, root)
	resp, code = runFS(t, bin, root, "dispatch", "--project", "skills", "--type", "review-task", "--goal", "read it again", "--reviews", work)
	if code != 0 {
		t.Fatalf("dispatch --reviews exit %d: %v", code, resp["error"])
	}
	second := resp["data"].(map[string]any)["cap_node_id"].(string)
	if resp, code := runFS(t, bin, root, "task", "update", second, "--status", "done", "--decision", "reviewed", "--project", "cap"); code != 0 {
		t.Fatalf("task update exit %d: %v", code, resp["error"])
	}

	resp, code = runFS(t, bin, root, "reviews", work)
	if code != 0 {
		t.Fatalf("reviews exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	if data["node"] != work || data["reviewed_by"] != second {
		t.Errorf("reviews data = %v, want %s reviewed by %s", data, work, second)
	}
}
