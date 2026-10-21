package dispatch_test

import (
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/dispatch"
	"github.com/flagship-dev/flagship/internal/query"
)

// mustAdd writes a node of the given kind and returns its id. It is the same
// write fs dispatch and fs task add perform, so a test can build the cap-scope
// shape — a batch, its members, their integrations — without a live herdr.
func mustAdd(t *testing.T, h *command.Handler, project, goal string, kind query.Kind, integrates string, parent *string) string {
	t.Helper()
	resp := h.TaskAddKind(project, goal, kind, "", integrates, "", "", "", parent)
	if !resp.OK {
		t.Fatalf("TaskAddKind %s: %s", goal, resp.Error)
	}
	return resp.Data.(command.EventData).NodeID
}

// A batch with no members is refused: complete is never vacuously true, so an
// empty batch cannot close.
func TestBatchRefusesNoMembers(t *testing.T) {
	f := newFixture(t, t.TempDir())
	batch := mustAdd(t, f.h, "cap", "the batch", query.KindBatch, "", nil)

	resp := dispatch.Batch(f.h, batch)
	if resp.OK {
		t.Fatalf("a batch with no members must be refused, got %v", resp.Data)
	}
	if !strings.Contains(resp.Error, "no members") {
		t.Errorf("refusal %q must name the empty batch", resp.Error)
	}
}

// fs batch is the aggregate of fs integrated: it names every member without a
// done integration, and it names none of the integrated ones.
func TestBatchNamesUnintegratedMembers(t *testing.T) {
	f := newFixture(t, t.TempDir())
	batch := mustAdd(t, f.h, "cap", "the batch", query.KindBatch, "", nil)
	m1 := mustAdd(t, f.h, "cap", "member 1", query.KindDispatch, "", &batch)
	m2 := mustAdd(t, f.h, "cap", "member 2", query.KindDispatch, "", &batch)

	// m1 is integrated by a done integration; m2 is not.
	integ := mustAdd(t, f.h, "cap", "integrate m1", query.KindDispatch, m1, nil)
	if !f.h.TaskUpdate("cap", integ, "done", "merged", nil).OK {
		t.Fatal("marking the integration done failed")
	}

	resp := dispatch.Batch(f.h, batch)
	if resp.OK {
		t.Fatalf("an unintegrated member must refuse the read, got %v", resp.Data)
	}
	if !strings.Contains(resp.Error, m2) {
		t.Errorf("refusal %q must name the unintegrated member %s", resp.Error, m2)
	}
	if strings.Contains(resp.Error, m1) {
		t.Errorf("refusal %q must not name the integrated member %s", resp.Error, m1)
	}
}

// Once every member is integrated, fs batch exits 0 and reports each member
// with the integration that merged it.
func TestBatchCompleteReportsMembers(t *testing.T) {
	f := newFixture(t, t.TempDir())
	batch := mustAdd(t, f.h, "cap", "the batch", query.KindBatch, "", nil)
	m := mustAdd(t, f.h, "cap", "member", query.KindDispatch, "", &batch)
	integ := mustAdd(t, f.h, "cap", "integrate member", query.KindDispatch, m, nil)
	if !f.h.TaskUpdate("cap", integ, "done", "merged", nil).OK {
		t.Fatal("marking the integration done failed")
	}

	resp := dispatch.Batch(f.h, batch)
	if !resp.OK {
		t.Fatalf("a fully integrated batch must read complete: %s", resp.Error)
	}
	result, ok := resp.Data.(dispatch.BatchResult)
	if !ok {
		t.Fatalf("data type %T, want dispatch.BatchResult", resp.Data)
	}
	if !result.Complete || len(result.Members) != 1 {
		t.Fatalf("result = %+v, want one complete member", result)
	}
	if result.Members[0].Node != m || result.Members[0].IntegratedBy != integ {
		t.Errorf("member = %+v, want node %s integrated by %s", result.Members[0], m, integ)
	}
}

// --batch naming a node that is not a batch refuses, naming what it is: the
// parent link is written into an immutable event, so the refusal happens before
// anything is recorded.
func TestPrepareBatchMemberRefusesNonBatch(t *testing.T) {
	f := newFixture(t, t.TempDir())
	notABatch := mustAdd(t, f.h, "cap", "a plain dispatch", query.KindDispatch, "", nil)

	resp := f.prepare(dispatch.PrepareRequest{
		Project: "skills", TaskType: "dev-task", Goal: "member", Batch: notABatch,
	})
	if resp.OK {
		t.Fatalf("--batch naming a dispatch node must be refused, got %v", resp.Data)
	}
	for _, want := range []string{notABatch, `"dispatch"`, "batch"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("refusal %q must name %q", resp.Error, want)
		}
	}
}

// A member names its batch with --batch: its cap node is recorded as the batch's
// child, which is the structural fact fs batch reads.
func TestPrepareBatchMemberRecordsTheParentLink(t *testing.T) {
	f := newFixture(t, t.TempDir())
	batch := mustAdd(t, f.h, "cap", "the batch", query.KindBatch, "", nil)

	resp := f.prepare(dispatch.PrepareRequest{
		Project: "skills", TaskType: "dev-task", Goal: "member", Batch: batch,
	})
	if !resp.OK {
		t.Fatalf("Prepare: %s", resp.Error)
	}
	brief := resp.Data.(*dispatch.Brief)

	task, ok := scopeTask(t, f, "cap", brief.CapNodeID)
	if !ok {
		t.Fatalf("cap node %s not found", brief.CapNodeID)
	}
	if task.Kind != query.KindDispatch {
		t.Errorf("member's kind = %q, want dispatch (a member is an ordinary dispatch)", task.Kind)
	}
	if task.ParentNodeID != batch {
		t.Errorf("member's parent = %q, want the batch %s", task.ParentNodeID, batch)
	}
}

// --worktree-new creates the worktree itself and records the workspace and its
// checkout path on the delivery exactly as --worktree does.
func TestDeliverWorktreeNewRecordsTheCreatedWorkspace(t *testing.T) {
	f := newFixture(t, t.TempDir())

	brief := prepareOnly(t, f, devReq("in a new tree"))
	brief.WorktreeNew = true
	herdr := &fakeHerdr{
		tabID: "w1:t9", paneID: "w1:p9",
		worktreeNewID: "w-new", worktreeNewPath: "/tmp/wt-new",
		rootPaneID: "w-new:p1", rootPaneTab: "w-new:t1", checkoutPath: "/tmp/wt-new",
	}

	resp := dispatch.Deliver(f.h, herdr, brief)
	if !resp.OK {
		t.Fatalf("Deliver: %s", resp.Error)
	}
	if len(herdr.worktreesCreated) != 1 || herdr.worktreesCreated[0] != brief.RootPath {
		t.Fatalf("worktree create calls = %v, want one rooted at %s", herdr.worktreesCreated, brief.RootPath)
	}

	d, ok, err := f.h.DeliveryFor("cap", brief.CapNodeID)
	if err != nil || !ok {
		t.Fatalf("delivery record: ok=%v err=%v", ok, err)
	}
	if d.Worktree != "w-new" || d.WorktreePath != "/tmp/wt-new" {
		t.Errorf("delivery = worktree %q path %q, want the created workspace w-new at /tmp/wt-new", d.Worktree, d.WorktreePath)
	}
}
