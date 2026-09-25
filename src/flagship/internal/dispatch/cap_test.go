package dispatch_test

// One cap, reachable: the claim that gives the scope an address, the report a
// worker pushes at whoever holds it, and the guard that keeps a worker from
// coordinating.

import (
	"errors"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/dispatch"
	"github.com/flagship-dev/flagship/internal/query"
)

// liveCap claims the cap scope for a pane herdr still has, and returns the pane.
func liveCap(t *testing.T, f *fixture, hc *fakeHerdr, pane string) {
	t.Helper()
	hc.paneExists = true
	hc.agentForPane = map[string]string{pane: "cap-agent"}
	resp := dispatch.Claim(f.h, hc, dispatch.ClaimRequest{PaneID: pane, TabID: "w1:t1"})
	if !resp.OK {
		t.Fatalf("Claim: %s", resp.Error)
	}
}

// workerNode creates a node in the target project for a worker to report on.
func workerNode(t *testing.T, f *fixture, goal string) string {
	t.Helper()
	resp := f.h.TaskAddKind("skills", goal, query.KindWork, "", "", nil)
	if !resp.OK {
		t.Fatalf("TaskAddKind: %s", resp.Error)
	}
	return resp.Data.(command.EventData).NodeID
}

// deliverTo records a delivery binding a dispatch node to a worker pane.
func deliverTo(t *testing.T, f *fixture, capNode, pane, workerID string) {
	t.Helper()
	resp := f.h.RecordDelivery("cap", capNode, command.DeliveryRecord{
		PaneID: pane, TabID: "w1:t9", Agent: "dispatch-dev-task-" + workerID,
		Engine: "herdr", Project: "skills", Node: workerID, Type: "dev-task",
	})
	if !resp.OK {
		t.Fatalf("RecordDelivery: %s", resp.Error)
	}
}

func capStatusOf(t *testing.T, f *fixture, hc *fakeHerdr) dispatch.CapStatus {
	t.Helper()
	status, err := dispatch.CapStatusOf(f.h, hc)
	if err != nil {
		t.Fatalf("CapStatusOf: %v", err)
	}
	return status
}

func TestClaimTakesAnUnclaimedScope(t *testing.T) {
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{paneExists: true, agentForPane: map[string]string{"w1:p1": "cap-agent"}}

	resp := dispatch.Claim(f.h, hc, dispatch.ClaimRequest{PaneID: "w1:p1", TabID: "w1:t1"})
	if !resp.OK {
		t.Fatalf("Claim: %s", resp.Error)
	}

	status := capStatusOf(t, f, hc)
	if status.State != dispatch.CapLive || status.PaneID != "w1:p1" || status.Agent != "cap-agent" {
		t.Fatalf("status: %+v", status)
	}
	if status.ClaimedAt == "" {
		t.Fatal("the address records no time")
	}
}

func TestClaimIsRefusedWhileTheAddressIsLive(t *testing.T) {
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{paneExists: true}
	liveCap(t, f, hc, "w1:p1")

	resp := dispatch.Claim(f.h, hc, dispatch.ClaimRequest{PaneID: "w1:p2", TabID: "w1:t2"})
	if resp.OK {
		t.Fatal("a claim over a live cap must be refused")
	}
	if !strings.Contains(resp.Error, "w1:p1") {
		t.Errorf("the refusal must name the live pane: %s", resp.Error)
	}

	// The refusal names when it was seen, so a person can tell a cap claimed a
	// minute ago from one claimed this morning.
	claim, _, err := f.h.CapFor("cap")
	if err != nil {
		t.Fatal(err)
	}
	if claim.ClaimedAt == "" || !strings.Contains(resp.Error, claim.ClaimedAt) {
		t.Errorf("the refusal must name when the live pane was seen: %s", resp.Error)
	}
}

func TestClaimOverADeadAddressRequiresReconstruction(t *testing.T) {
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{paneExists: true}
	liveCap(t, f, hc, "w1:p1")
	first, _, err := f.h.CapFor("cap")
	if err != nil {
		t.Fatal(err)
	}

	// herdr no longer has the pane: the address is provably dead.
	hc.paneExists = false
	resp := dispatch.Claim(f.h, hc, dispatch.ClaimRequest{PaneID: "w1:p2", TabID: "w1:t2"})
	if resp.OK {
		t.Fatal("a take-over without --reconstructed must be refused")
	}
	if !strings.Contains(resp.Error, "--reconstructed") {
		t.Errorf("the refusal must ask for the reconstruction: %s", resp.Error)
	}

	resp = dispatch.Claim(f.h, hc, dispatch.ClaimRequest{
		PaneID: "w1:p2", TabID: "w1:t2",
		Reconstructed: "read fs pending: two dispatches open, nothing blocked",
	})
	if !resp.OK {
		t.Fatalf("take-over: %s", resp.Error)
	}

	claim, _, err := f.h.CapFor("cap")
	if err != nil {
		t.Fatal(err)
	}
	if claim.PaneID != "w1:p2" {
		t.Fatalf("the claim should name the new address, got %q", claim.PaneID)
	}
	if claim.TakenOverFrom != "w1:p1" || claim.TakenOverAt != first.ClaimedAt {
		t.Errorf("handover = %q at %q, want the old address w1:p1 at %q", claim.TakenOverFrom, claim.TakenOverAt, first.ClaimedAt)
	}
	if claim.Reconstructed != "read fs pending: two dispatches open, nothing blocked" {
		t.Errorf("the reconstruction must be recorded on the claim: %+v", claim)
	}
}

func TestClaimRefusesWhenHerdrCannotSayTheAddressIsDead(t *testing.T) {
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{paneExists: true}
	liveCap(t, f, hc, "w1:p1")

	// An unanswerable question is not an answer: a claim must not take a scope
	// that may still be held.
	hc.paneExistsErr = errors.New("herdr is not running")
	resp := dispatch.Claim(f.h, hc, dispatch.ClaimRequest{PaneID: "w1:p2", Reconstructed: "read the node"})
	if resp.OK {
		t.Fatal("a claim must be refused when herdr cannot prove the address dead")
	}
}

func TestClaimRefusesASessionWithNoPane(t *testing.T) {
	f := newFixture(t, t.TempDir())

	resp := dispatch.Claim(f.h, &fakeHerdr{}, dispatch.ClaimRequest{TabID: "w1:t1"})
	if resp.OK {
		t.Fatal("a claim from outside herdr must be refused")
	}
	if !strings.Contains(resp.Error, "HERDR_PANE_ID") {
		t.Errorf("the refusal must say why: %s", resp.Error)
	}
}

func TestCapStatusDistinguishesNoClaimFromADeadOne(t *testing.T) {
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{}

	// No claim at all: the scope is free, and that is not a dead cap.
	if status := capStatusOf(t, f, hc); status.State != dispatch.CapNone {
		t.Fatalf("unclaimed scope reads %q, want %q", status.State, dispatch.CapNone)
	}

	liveCap(t, f, hc, "w1:p1")
	hc.paneExists = false
	status := capStatusOf(t, f, hc)
	if status.State != dispatch.CapDead {
		t.Fatalf("a claim whose pane is gone reads %q, want %q", status.State, dispatch.CapDead)
	}
	if status.PaneID != "w1:p1" {
		t.Fatalf("a dead cap still names its address: %+v", status)
	}
}

func TestCapStatusIsUnknownWhenHerdrCannotBeAsked(t *testing.T) {
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{paneExists: true}
	liveCap(t, f, hc, "w1:p1")

	// herdr unreachable is not a dead cap: reporting one would invite a
	// take-over of a scope that is still held.
	hc.paneExistsErr = errors.New("herdr is not running")
	if status := capStatusOf(t, f, hc); status.State != dispatch.CapUnknown {
		t.Fatalf("unreadable herdr reads %q, want %q", status.State, dispatch.CapUnknown)
	}
}

func TestReportWritesTheEventAndThenNotifies(t *testing.T) {
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{paneExists: true}
	liveCap(t, f, hc, "w1:p1")
	hc.prompts = nil

	node := workerNode(t, f, "a unit of work")
	resp := dispatch.Report(f.h, hc, dispatch.ReportRequest{
		Project: "skills", Node: node, Kind: command.ReportBlocked,
		Line: "the fixture has no cap",
	})
	if !resp.OK {
		t.Fatalf("Report: %s", resp.Error)
	}
	result := resp.Data.(dispatch.ReportResult)
	if !result.Notified {
		t.Fatalf("a block must notify the live cap: %+v", result)
	}

	// Written first and always: the report and the block are in the store, and
	// the notification names the node rather than carrying the work.
	reports, err := f.h.ReportsFor("skills", node)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].Kind != command.ReportBlocked {
		t.Fatalf("reports: %+v", reports)
	}
	if len(hc.prompts) != 1 {
		t.Fatalf("prompts: %+v", hc.prompts)
	}
	if hc.prompts[0].pane != "w1:p1" {
		t.Errorf("the report went to %q, want the cap's pane", hc.prompts[0].pane)
	}
	if !strings.Contains(hc.prompts[0].text, "skills:"+node) {
		t.Errorf("the notification must name the node: %q", hc.prompts[0].text)
	}
}

func TestReportWithNoCapIsStillAReport(t *testing.T) {
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{}
	node := workerNode(t, f, "a unit of work")

	resp := dispatch.Report(f.h, hc, dispatch.ReportRequest{
		Project: "skills", Node: node, Kind: command.ReportBlocked,
		Line: "the fixture has no cap",
	})
	if !resp.OK {
		t.Fatalf("a report must succeed with no cap: %s", resp.Error)
	}
	result := resp.Data.(dispatch.ReportResult)
	if result.Notified {
		t.Fatal("nothing was there to notify")
	}
	if !strings.Contains(result.Note, "recorded, not notified") {
		t.Fatalf("note: %q", result.Note)
	}
	if len(hc.prompts) != 0 {
		t.Fatalf("no cap exists, so nothing may be sent: %+v", hc.prompts)
	}

	// The store is the channel: the report is there for whoever holds the cap
	// next, and the node is blocked for every reader that already handles one.
	reports, err := f.h.ReportsFor("skills", node)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 {
		t.Fatalf("reports: %+v", reports)
	}
}

func TestReportWithADeadCapSaysRecordedNotNotified(t *testing.T) {
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{paneExists: true}
	liveCap(t, f, hc, "w1:p1")
	hc.paneExists = false
	hc.prompts = nil

	node := workerNode(t, f, "a unit of work")
	resp := dispatch.Report(f.h, hc, dispatch.ReportRequest{
		Project: "skills", Node: node, Kind: command.ReportQuestion, Line: "which store?",
	})
	if !resp.OK {
		t.Fatalf("Report: %s", resp.Error)
	}
	result := resp.Data.(dispatch.ReportResult)
	if result.Notified || !strings.Contains(result.Note, "recorded, not notified") {
		t.Fatalf("result: %+v", result)
	}
	if len(hc.prompts) != 0 {
		t.Fatalf("a dead cap must not be sent to: %+v", hc.prompts)
	}
}

func TestReportThatHerdrRefusesIsNotAFailedReport(t *testing.T) {
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{paneExists: true, promptErr: errors.New("herdr: pane gone")}
	liveCap(t, f, hc, "w1:p1")

	node := workerNode(t, f, "a unit of work")
	resp := dispatch.Report(f.h, hc, dispatch.ReportRequest{
		Project: "skills", Node: node, Kind: command.ReportDone, Line: "the parser and its tests",
	})
	if !resp.OK {
		t.Fatalf("a refused notification must not fail the report: %s", resp.Error)
	}
	result := resp.Data.(dispatch.ReportResult)
	if result.Notified || !strings.Contains(result.Note, "recorded, not notified") {
		t.Fatalf("result: %+v", result)
	}
	reports, err := f.h.ReportsFor("skills", node)
	if err != nil || len(reports) != 1 {
		t.Fatalf("the report must survive a refused notification: %+v err=%v", reports, err)
	}
}

func TestReportWithoutAKindSendsNothing(t *testing.T) {
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{paneExists: true}
	liveCap(t, f, hc, "w1:p1")
	hc.prompts = nil

	node := workerNode(t, f, "a unit of work")
	resp := dispatch.Report(f.h, hc, dispatch.ReportRequest{
		Project: "skills", Node: node, Line: "halfway through the parser",
	})
	if !resp.OK {
		t.Fatalf("Report: %s", resp.Error)
	}
	result := resp.Data.(dispatch.ReportResult)
	if result.Notified {
		t.Fatal("a report with no kind must not interrupt the cap")
	}
	if len(hc.prompts) != 0 {
		t.Fatalf("a kindless report must send nothing: %+v", hc.prompts)
	}
}

func TestReportRefusedWritesNothing(t *testing.T) {
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{paneExists: true}
	liveCap(t, f, hc, "w1:p1")
	before := scopeEventCount(t, f, "skills")

	resp := dispatch.Report(f.h, hc, dispatch.ReportRequest{
		Project: "skills", Node: "t-nope", Kind: command.ReportBlocked, Line: "nowhere",
	})
	if resp.OK {
		t.Fatal("a report on a node that does not exist must be refused")
	}
	if got := scopeEventCount(t, f, "skills"); got != before {
		t.Fatalf("a refused report wrote %d events", got-before)
	}
	if len(hc.prompts) != 0 {
		t.Fatalf("a refused report must notify nobody: %+v", hc.prompts)
	}
}

func TestGuardRefusesThePaneAWorkerWasDispatchedTo(t *testing.T) {
	f := newFixture(t, t.TempDir())
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	worker := workerNode(t, f, "a unit of work")
	deliverTo(t, f, capNode, "w1:p9", worker)

	err := dispatch.Guard(f.h, "w1:p9")
	if err == nil {
		t.Fatal("a pane with a delivery record must not coordinate")
	}
	if !strings.Contains(err.Error(), "skills:"+worker) {
		t.Errorf("the refusal must name the node the pane worked: %s", err)
	}

	// A pane fs holds no record for is unclaimed, not a worker: fs refuses on
	// what it knows and nothing else.
	if err := dispatch.Guard(f.h, "w1:p8"); err != nil {
		t.Errorf("a pane with no delivery record must not be refused: %s", err)
	}
}

func TestReportingIsAllowedFromThePanesTheGuardRefuses(t *testing.T) {
	// AC6: one environment, two acts. The same pane that cannot dispatch can
	// still report, because reporting is not acting as cap.
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{paneExists: true}
	liveCap(t, f, hc, "w1:p1")
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	worker := workerNode(t, f, "a unit of work")
	deliverTo(t, f, capNode, "w1:p9", worker)

	if err := dispatch.Guard(f.h, "w1:p9"); err == nil {
		t.Fatal("the worker's pane must be refused a coordinating act")
	}

	resp := dispatch.Report(f.h, hc, dispatch.ReportRequest{
		Project: "skills", Node: worker, Kind: command.ReportBlocked,
		Line: "the fixture has no cap",
	})
	if !resp.OK {
		t.Fatalf("the worker's own report must succeed: %s", resp.Error)
	}
	if !resp.Data.(dispatch.ReportResult).Notified {
		t.Fatal("the report from the refused pane must still reach the cap")
	}
}

func TestTellResolvesTheDeliveryRecordAndSends(t *testing.T) {
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{}
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	worker := workerNode(t, f, "a unit of work")
	deliverTo(t, f, capNode, "w1:p9", worker)

	resp := dispatch.Tell(f.h, hc, dispatch.TellRequest{
		Project: "skills", Node: worker, Text: "use the sqlite store, and report when it lands",
	})
	if !resp.OK {
		t.Fatalf("Tell: %s", resp.Error)
	}
	if len(hc.prompts) != 1 || hc.prompts[0].pane != "w1:p9" {
		t.Fatalf("tell must send to the worker's pane: %+v", hc.prompts)
	}

	// A reply is a message, not a record: it writes nothing, on either side.
	before := scopeEventCount(t, f, "skills")
	if resp := dispatch.Tell(f.h, hc, dispatch.TellRequest{
		Project: "skills", Node: worker, Text: "and again when the tests pass",
	}); !resp.OK {
		t.Fatalf("Tell: %s", resp.Error)
	}
	if got := scopeEventCount(t, f, "skills"); got != before {
		t.Fatalf("fs tell wrote %d events; a reply is never stored", got-before)
	}
}

func TestTellRefusesANodeNobodyCreated(t *testing.T) {
	f := newFixture(t, t.TempDir())

	resp := dispatch.Tell(f.h, &fakeHerdr{}, dispatch.TellRequest{Project: "skills", Node: "t-nope", Text: "hello"})
	if resp.OK {
		t.Fatal("an unknown node must be refused")
	}
	if !strings.Contains(resp.Error, "t-nope") {
		t.Errorf("the refusal must name the node: %s", resp.Error)
	}
}

func TestTellRefusesANodeWithNoDeliveryRecord(t *testing.T) {
	f := newFixture(t, t.TempDir())
	worker := workerNode(t, f, "a unit of work")

	resp := dispatch.Tell(f.h, &fakeHerdr{}, dispatch.TellRequest{Project: "skills", Node: worker, Text: "hello"})
	if resp.OK {
		t.Fatal("a node with no delivery record must be refused")
	}
	if !strings.Contains(resp.Error, worker) {
		t.Errorf("the refusal must name the node: %s", resp.Error)
	}
}

func TestTellRefusesWhenHerdrRefuses(t *testing.T) {
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{promptErr: errors.New("herdr: pane gone")}
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	worker := workerNode(t, f, "a unit of work")
	deliverTo(t, f, capNode, "w1:p9", worker)

	// Unlike a report, a reply has nothing behind it: a tell that did not
	// arrive is a failure, not a record with a missed notification.
	resp := dispatch.Tell(f.h, hc, dispatch.TellRequest{Project: "skills", Node: worker, Text: "hello"})
	if resp.OK {
		t.Fatal("a tell herdr refused must be refused")
	}
}

func TestPendingNamesTheCapAndSurfacesEveryOpenReport(t *testing.T) {
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{paneExists: true}
	liveCap(t, f, hc, "w1:p1")

	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	worker := workerNode(t, f, "a unit of work")
	deliverTo(t, f, capNode, "w1:p9", worker)
	// The worker is still going: herdr has its agent, and it is not done.
	hc.agents = map[string]string{"dispatch-dev-task-" + worker: "working"}
	if resp := f.h.ReportAdd("skills", worker, command.ReportQuestion, "which store?"); !resp.OK {
		t.Fatalf("ReportAdd: %s", resp.Error)
	}

	result := pendingOf(t, dispatch.Pending(f.h, hc))
	if result.Cap.State != dispatch.CapLive || result.Cap.PaneID != "w1:p1" {
		t.Fatalf("pending must name the cap and its liveness: %+v", result.Cap)
	}

	// A worker waiting on a decision reads as running, whose documented meaning
	// is "let it work" — so the report is what makes the wait visible.
	entry := pendingEntryFor(t, result, capNode)
	if entry.State != dispatch.StateRunning {
		t.Fatalf("state: %q", entry.State)
	}
	if len(entry.Reports) != 1 || entry.Reports[0].Line != "which store?" {
		t.Fatalf("reports: %+v", entry.Reports)
	}
}

func TestPendingReportsADeadCapAsDead(t *testing.T) {
	f := newFixture(t, t.TempDir())
	hc := &fakeHerdr{paneExists: true}
	liveCap(t, f, hc, "w1:p1")
	hc.paneExists = false

	result := pendingOf(t, dispatch.Pending(f.h, hc))
	if result.Cap.State != dispatch.CapDead {
		t.Fatalf("a dead cap must read as dead from the cap's own read: %+v", result.Cap)
	}
}

func TestPendingSaysWhenNoCapHasClaimed(t *testing.T) {
	f := newFixture(t, t.TempDir())

	result := pendingOf(t, dispatch.Pending(f.h, &fakeHerdr{}))
	if result.Cap.State != dispatch.CapNone {
		t.Fatalf("an unclaimed scope must say so: %+v", result.Cap)
	}
}
