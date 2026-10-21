package dispatch_test

// The branch a delivery created: from herdr's answer, onto the delivery record,
// and into the exit steps as $FS_BRANCH.
//
// Four rounds of fixes to the teardown's branch step all worked on naming the
// branch to DELETE, and none of them left anything behind that could say whether
// the deletion happened. The close asserted the tree and the workspace were gone
// and never asked about the branch, so a branch step that skipped in silence —
// or that deleted a branch which was not the delivery's — closed green with the
// branch still there. A postcondition that refuses that can only be written over
// data, and the data has to exist before the assertion can.
//
// These tests are that half: what the delivery writes, what it writes for a
// worktree it did not create, and what reaches the exit step as $FS_BRANCH.

import (
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/dispatch"
	"github.com/flagship-dev/flagship/internal/query"
)

// A delivery that created the worktree records the branch herdr created it on.
// herdr reports it in the same response as the workspace id and the checkout
// path, so this is data the delivery already had — recording it adds no input
// and no second question about the checkout, which is the substitution the
// teardown's branch step was rewritten four times to stop making.
func TestDeliverRecordsTheBranchOfTheWorktreeItCreated(t *testing.T) {
	f := newFixture(t, t.TempDir())
	brief := prepareOnly(t, f, devReq("in a new tree"))
	brief.WorktreeNew = true
	herdr := &fakeHerdr{
		tabID: "w1:t9", paneID: "w1:p9",
		worktreeNewID: "w-new", worktreeNewPath: "/tmp/wt-new", worktreeNewBranch: "worktree-delivery",
		rootPaneID: "w-new:p1", rootPaneTab: "w-new:t1", checkoutPath: "/tmp/wt-new",
	}

	resp := dispatch.Deliver(f.h, herdr, brief)
	if !resp.OK {
		t.Fatalf("Deliver: %s", resp.Error)
	}

	d, ok, err := f.h.DeliveryFor("cap", brief.CapNodeID)
	if err != nil || !ok {
		t.Fatalf("delivery record: ok=%v err=%v", ok, err)
	}
	if d.Branch != "worktree-delivery" {
		t.Errorf("delivery branch = %q, want %q — the branch herdr reported creating it on",
			d.Branch, "worktree-delivery")
	}
}

// A worktree that came from elsewhere is not this delivery's to remove, and the
// record is where that is decided. `--worktree` names a workspace the delivery
// did not create, so it records no branch — even when a branch is sitting right
// there to be recorded. An exit step reads that empty field and stands down,
// which is how it tells "the branch was not mine to remove" from "the branch I
// was responsible for is still here" without asking a question it cannot answer
// after the tree is gone.
func TestDeliverRecordsNoBranchForAWorktreeItDidNotCreate(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)
	nodeID := addCapNode(t, f, "dispatch parallel: sample")

	herdr := &fakeHerdr{
		rootPaneID: "wA:p1", rootPaneTab: "wA:t1", checkoutPath: "/checkouts/wA",
		// Set, so that a delivery recording it could only be doing so by
		// guessing: this branch belongs to the workspace's own dispatcher.
		worktreeNewBranch: "not-this-deliverys",
	}
	brief := &dispatch.Brief{
		Goal: "sample", Project: "skills", RootPath: root, TaskType: "parallel",
		CapNodeID: nodeID, Worktree: "wA",
	}

	resp := dispatch.Deliver(f.h, herdr, brief)
	if !resp.OK {
		t.Fatalf("Deliver: %s", resp.Error)
	}
	if len(herdr.worktreesCreated) != 0 {
		t.Errorf("worktrees created = %v, want none: --worktree names one that already exists",
			herdr.worktreesCreated)
	}

	d, ok, err := f.h.DeliveryFor("cap", nodeID)
	if err != nil || !ok {
		t.Fatalf("delivery record: ok=%v err=%v", ok, err)
	}
	if d.Branch != "" {
		t.Errorf("delivery branch = %q, want empty: this delivery created no worktree, "+
			"so no branch of its is the teardown's to remove", d.Branch)
	}
	// The worktree itself is still recorded: standing down on the branch must
	// not stand down on the resources the delivery did open.
	if d.Worktree != "wA" || d.WorktreePath != "/checkouts/wA" {
		t.Errorf("delivery = worktree %q path %q, want the named workspace wA at /checkouts/wA",
			d.Worktree, d.WorktreePath)
	}
}

// A created worktree whose checkout stands on no branch at all records none.
// The field is a claim about what was created, never a guess at what to delete:
// an empty Branch and an empty Branch-from-herdr are the same value here, and
// both mean the branch postcondition has nothing of its own to assert. What this
// test rules out is the delivery filling the gap with something it inferred.
func TestDeliverRecordsNoBranchForAWorktreeHerdrCreatedDetached(t *testing.T) {
	f := newFixture(t, t.TempDir())
	brief := prepareOnly(t, f, devReq("in a detached new tree"))
	brief.WorktreeNew = true
	herdr := &fakeHerdr{
		tabID: "w1:t9", paneID: "w1:p9",
		worktreeNewID: "w-new", worktreeNewPath: "/tmp/wt-new", // no branch reported
		rootPaneID: "w-new:p1", rootPaneTab: "w-new:t1", checkoutPath: "/tmp/wt-new",
	}

	if resp := dispatch.Deliver(f.h, herdr, brief); !resp.OK {
		t.Fatalf("Deliver: %s", resp.Error)
	}

	d, ok, err := f.h.DeliveryFor("cap", brief.CapNodeID)
	if err != nil || !ok {
		t.Fatalf("delivery record: ok=%v err=%v", ok, err)
	}
	if d.Branch != "" {
		t.Errorf("delivery branch = %q, want empty: herdr reported no branch for the checkout it made",
			d.Branch)
	}
	if d.WorktreePath != "/tmp/wt-new" {
		t.Errorf("delivery path = %q, want the created checkout /tmp/wt-new", d.WorktreePath)
	}
}

// recordWorktreeDelivery writes a delivery record bound to a worktree workspace,
// the shape a worktree dispatch records: the exit gate has to find that tree,
// and the teardown has to verify it gone afterwards.
// path is the checkout the workspace was made from — what the record keeps so the
// worktree can be removed after the workspace is forgotten.
func recordWorktreeDelivery(t *testing.T, f *fixture, capNode, paneID, workerID, wsID, path string) {
	t.Helper()
	recordWorktreeDeliveryOnBranch(t, f, capNode, paneID, workerID, wsID, path, "")
}

// recordWorktreeDeliveryOnBranch is the same record with the branch the delivery
// created, which a test writes when it is about what a close asserts over the
// branch. The delivery records a branch only for a worktree it made itself, so
// the empty branch above is the ordinary record rather than a missing field.
func recordWorktreeDeliveryOnBranch(t *testing.T, f *fixture, capNode, paneID, workerID, wsID, path, branch string) {
	t.Helper()
	resp := f.h.RecordDelivery("cap", capNode, command.DeliveryRecord{
		PaneID: paneID, TabID: wsID + ":t1", Agent: "dispatch-parallel", Engine: "herdr",
		Project: "skills", Node: workerID, Type: "parallel", Worktree: wsID, WorktreePath: path,
		Branch: branch,
	})
	if !resp.OK {
		t.Fatalf("RecordDelivery: %s", resp.Error)
	}
}

// addWorkerNode creates the worker's node in the skills scope, the way Deliver
// does — a dispatch node — with the given status.
func addWorkerNode(t *testing.T, f *fixture, status string) string {
	t.Helper()
	added := f.h.TaskAddKind("skills", "worker task", query.KindDispatch, "", "", "", "", "", nil)
	if !added.OK {
		t.Fatalf("task add: %s", added.Error)
	}
	nodeID := added.Data.(command.EventData).NodeID
	if status != "" && status != "pending" {
		if resp := f.h.TaskUpdate("skills", nodeID, status, "", nil); !resp.OK {
			t.Fatalf("task update: %s", resp.Error)
		}
	}
	return nodeID
}
