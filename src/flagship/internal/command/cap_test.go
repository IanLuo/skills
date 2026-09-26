package command_test

import (
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/store"
)

// claim is a valid claim of the cap scope by a pane.
func claim(pane, tab, agent string) command.CapClaim {
	return command.CapClaim{PaneID: pane, TabID: tab, Agent: agent}
}

func TestCapForWithNoClaim(t *testing.T) {
	h := setup(t)

	_, claimed, err := h.CapFor("cap")
	if err != nil {
		t.Fatalf("CapFor: %v", err)
	}
	if claimed {
		t.Fatal("expected no claim")
	}
}

func TestClaimCapRecordsTheAddress(t *testing.T) {
	h := setup(t)

	resp := h.ClaimCap("cap", claim("w1:p1", "w1:t1", "cap-agent"))
	if !resp.OK {
		t.Fatalf("ClaimCap: %s", resp.Error)
	}

	got, claimed, err := h.CapFor("cap")
	if err != nil || !claimed {
		t.Fatalf("CapFor: ok=%v err=%v", claimed, err)
	}
	if got.PaneID != "w1:p1" || got.TabID != "w1:t1" || got.Agent != "cap-agent" {
		t.Fatalf("address: %+v", got)
	}
	if got.ClaimedAt == "" {
		t.Fatal("claim recorded no time")
	}
}

func TestClaimCapRequiresAPane(t *testing.T) {
	h := setup(t)

	resp := h.ClaimCap("cap", command.CapClaim{TabID: "w1:t1"})
	if resp.OK {
		t.Fatal("expected a claim naming no pane to be refused")
	}
}

func TestClaimCapIsInvisibleToDerivedState(t *testing.T) {
	h := setup(t)

	if resp := h.ClaimCap("cap", claim("w1:p1", "w1:t1", "cap-agent")); !resp.OK {
		t.Fatalf("ClaimCap: %s", resp.Error)
	}

	// The claim names a scope, not a node: nothing may materialise in the cap
	// scope from it, or every reader would see a card that does not exist.
	resp := h.Status("cap")
	if !resp.OK {
		t.Fatalf("Status: %s", resp.Error)
	}
	result, ok := resp.Data.(command.StatusResult)
	if !ok {
		t.Fatalf("data type: %T", resp.Data)
	}
	if len(result.Tasks) != 0 {
		t.Fatalf("expected no tasks in the cap scope, got %d", len(result.Tasks))
	}
}

func TestClaimCapReplacesTheAddress(t *testing.T) {
	h := setup(t)

	if resp := h.ClaimCap("cap", claim("w1:p1", "w1:t1", "cap-agent")); !resp.OK {
		t.Fatalf("ClaimCap: %s", resp.Error)
	}
	// The take-over's shape: the same append, carrying what it replaced.
	takeover := claim("w1:p2", "w1:t2", "cap-agent")
	takeover.TakenOverFrom = "w1:p1"
	takeover.TakenOverAt = "2026-09-25T00:00:00Z"
	takeover.Reconstructed = "read fs pending: two dispatches open, nothing blocked"
	if resp := h.ClaimCap("cap", takeover); !resp.OK {
		t.Fatalf("ClaimCap: %s", resp.Error)
	}

	got, _, err := h.CapFor("cap")
	if err != nil {
		t.Fatal(err)
	}
	if got.PaneID != "w1:p2" {
		t.Fatalf("claim should name the newest address, got %q", got.PaneID)
	}
	if got.TakenOverFrom != "w1:p1" || got.Reconstructed == "" {
		t.Fatalf("handover lost: %+v", got)
	}
}

func TestReportAddRecordsAKindlessReportSilently(t *testing.T) {
	h := setup(t)
	if resp := h.ProjectCreate("proj", "/tmp/proj"); !resp.OK {
		t.Fatalf("ProjectCreate: %s", resp.Error)
	}
	add := h.TaskAdd("proj", "a unit of work", nil)
	if !add.OK {
		t.Fatalf("TaskAdd: %s", add.Error)
	}
	node := add.Data.(command.EventData).NodeID

	resp := h.ReportAdd("proj", node, "", "halfway through the parser")
	if !resp.OK {
		t.Fatalf("ReportAdd: %s", resp.Error)
	}
	result, ok := resp.Data.(command.UpdateResult)
	if !ok {
		t.Fatalf("data type: %T", resp.Data)
	}
	if len(result.Events) != 1 || result.Events[0].EventType != store.ReportRecorded {
		t.Fatalf("a kindless report writes report-recorded and nothing else: %+v", result.Events)
	}

	reports, err := h.ReportsFor("proj", node)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].Line != "halfway through the parser" || reports[0].Kind != "" {
		t.Fatalf("reports: %+v", reports)
	}
}

func TestReportAddBlockedWritesTheBlockBesideTheReport(t *testing.T) {
	h := setup(t)
	if resp := h.ProjectCreate("proj", "/tmp/proj"); !resp.OK {
		t.Fatalf("ProjectCreate: %s", resp.Error)
	}
	add := h.TaskAdd("proj", "a unit of work", nil)
	node := add.Data.(command.EventData).NodeID

	resp := h.ReportAdd("proj", node, command.ReportBlocked, "blocked: the fixture has no cap")
	if !resp.OK {
		t.Fatalf("ReportAdd: %s", resp.Error)
	}
	result := resp.Data.(command.UpdateResult)
	want := []store.EventType{store.ReportRecorded, store.TaskBlocked, store.StatusChanged}
	if len(result.Events) != len(want) {
		t.Fatalf("events: %+v", result.Events)
	}
	for i, typ := range want {
		if result.Events[i].EventType != typ {
			t.Fatalf("event %d: want %s, got %s", i, typ, result.Events[i].EventType)
		}
	}

	// Every reader that already handles a block is untouched: the node is
	// blocked, with the report's line as the reason.
	resp = h.Status("proj")
	blocked := false
	for _, task := range resp.Data.(command.StatusResult).Tasks {
		if task.NodeID == node {
			blocked = task.Status == "blocked"
		}
	}
	if !blocked {
		t.Fatal("a blocked report must block the node")
	}
}

func TestReportAddDoneDoesNotChangeTheStatus(t *testing.T) {
	h := setup(t)
	if resp := h.ProjectCreate("proj", "/tmp/proj"); !resp.OK {
		t.Fatalf("ProjectCreate: %s", resp.Error)
	}
	add := h.TaskAdd("proj", "a unit of work", nil)
	node := add.Data.(command.EventData).NodeID
	h.TaskUpdate("proj", node, "active", "", nil)

	resp := h.ReportAdd("proj", node, command.ReportDone, "done: the parser and its tests")
	if !resp.OK {
		t.Fatalf("ReportAdd: %s", resp.Error)
	}
	result := resp.Data.(command.UpdateResult)
	if len(result.Events) != 1 {
		t.Fatalf("a done report records the report and nothing else: %+v", result.Events)
	}

	// The node is the record of the work and the cap closes the dispatch; a
	// worker saying "done" must not mark its own node done.
	for _, task := range h.Status("proj").Data.(command.StatusResult).Tasks {
		if task.NodeID == node && task.Status != "active" {
			t.Fatalf("a done report changed the node's status to %q", task.Status)
		}
	}
}

func TestReportAddRefusesAnUnknownKind(t *testing.T) {
	h := setup(t)
	if resp := h.ProjectCreate("proj", "/tmp/proj"); !resp.OK {
		t.Fatalf("ProjectCreate: %s", resp.Error)
	}
	node := h.TaskAdd("proj", "a unit of work", nil).Data.(command.EventData).NodeID

	if resp := h.ReportAdd("proj", node, "urgent", "please look"); resp.OK {
		t.Fatal("expected an unknown kind to be refused")
	}
}

func TestReportAddRequiresALine(t *testing.T) {
	h := setup(t)
	if resp := h.ProjectCreate("proj", "/tmp/proj"); !resp.OK {
		t.Fatalf("ProjectCreate: %s", resp.Error)
	}
	node := h.TaskAdd("proj", "a unit of work", nil).Data.(command.EventData).NodeID

	if resp := h.ReportAdd("proj", node, command.ReportBlocked, ""); resp.OK {
		t.Fatal("expected a report with no line to be refused")
	}
}

func TestReportAddRefusesANodeThatDoesNotExist(t *testing.T) {
	h := setup(t)
	if resp := h.ProjectCreate("proj", "/tmp/proj"); !resp.OK {
		t.Fatalf("ProjectCreate: %s", resp.Error)
	}

	if resp := h.ReportAdd("proj", "t-nope", "", "a report for a node nobody created"); resp.OK {
		t.Fatal("expected a report on a nonexistent node to be refused")
	}
}

func TestReportAddIsAddressedToANodeNotACap(t *testing.T) {
	h := setup(t)
	if resp := h.ProjectCreate("proj", "/tmp/proj"); !resp.OK {
		t.Fatalf("ProjectCreate: %s", resp.Error)
	}
	node := h.TaskAdd("proj", "a unit of work", nil).Data.(command.EventData).NodeID

	// A report is qualified like any other write: naming the cap scope is a
	// cross-scope write, refused — there is no route that addresses a cap.
	if resp := h.ReportAdd("proj", "cap:"+node, "", "for whoever holds the cap"); resp.OK {
		t.Fatal("expected a report naming another scope to be refused")
	}
}

// dispatchNode creates a cap-scope dispatch node, which is what a delivery
// record is recorded on.
func dispatchNode(t *testing.T, h *command.Handler, goal string) string {
	t.Helper()
	resp := h.TaskAddKind("cap", goal, query.KindDispatch, "", "", "", nil)
	if !resp.OK {
		t.Fatalf("TaskAddKind: %s", resp.Error)
	}
	return resp.Data.(command.EventData).NodeID
}

func TestDeliveryForPane(t *testing.T) {
	h := setup(t)
	capNode := dispatchNode(t, h, "dispatch a worker")
	record := command.DeliveryRecord{
		PaneID:  "w1:p2",
		TabID:   "w1:t1",
		Agent:   "dispatch-dev-task-t-1",
		Engine:  "herdr",
		Project: "proj",
		Node:    "t-1",
		Type:    "dev-task",
	}
	if resp := h.RecordDelivery("cap", capNode, record); !resp.OK {
		t.Fatalf("RecordDelivery: %s", resp.Error)
	}

	got, foundOn, found, err := h.DeliveryForPane("cap", "w1:p2")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected the delivery record for the worker's pane")
	}
	if got.Node != "t-1" || got.Project != "proj" || foundOn != capNode {
		t.Fatalf("record: %+v capNode=%s", got, foundOn)
	}

	if _, _, found, _ := h.DeliveryForPane("cap", "w1:p9"); found {
		t.Fatal("a pane with no delivery record must not be matched")
	}
}

func TestDeliveryForPaneTakesTheLatestRecordForAPane(t *testing.T) {
	h := setup(t)
	capOne := dispatchNode(t, h, "dispatch the first worker")
	capTwo := dispatchNode(t, h, "dispatch the second worker")
	first := command.DeliveryRecord{PaneID: "w1:p2", Agent: "a", Engine: "herdr", Project: "proj", Node: "t-1"}
	second := command.DeliveryRecord{PaneID: "w1:p2", Agent: "a", Engine: "herdr", Project: "proj", Node: "t-2"}
	if resp := h.RecordDelivery("cap", capOne, first); !resp.OK {
		t.Fatalf("RecordDelivery: %s", resp.Error)
	}
	if resp := h.RecordDelivery("cap", capTwo, second); !resp.OK {
		t.Fatalf("RecordDelivery: %s", resp.Error)
	}

	// A pane reused for a new dispatch is that dispatch's worker, and the
	// refusal must name the node the pane holds now.
	got, foundOn, _, err := h.DeliveryForPane("cap", "w1:p2")
	if err != nil {
		t.Fatal(err)
	}
	if got.Node != "t-2" || foundOn != capTwo {
		t.Fatalf("expected the newest record, got %+v on %s", got, foundOn)
	}
}

func TestReportsForIgnoresADecisionBesideTheReport(t *testing.T) {
	h := setup(t)
	if resp := h.ProjectCreate("proj", "/tmp/proj"); !resp.OK {
		t.Fatalf("ProjectCreate: %s", resp.Error)
	}
	node := h.TaskAdd("proj", "a unit of work", nil).Data.(command.EventData).NodeID

	if resp := h.ReportAdd("proj", node, command.ReportQuestion, "question: which store?"); !resp.OK {
		t.Fatalf("ReportAdd: %s", resp.Error)
	}
	if resp := h.TaskUpdate("proj", node, "", "I chose the sqlite store", nil); !resp.OK {
		t.Fatalf("TaskUpdate: %s", resp.Error)
	}

	// The cap's answer is a decision on the node; it is not a report and must
	// not read as one.
	reports, err := h.ReportsFor("proj", node)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].Line != "question: which store?" {
		t.Fatalf("reports: %+v", reports)
	}
}

func TestReportsForAnotherNodeIsEmpty(t *testing.T) {
	h := setup(t)
	if resp := h.ProjectCreate("proj", "/tmp/proj"); !resp.OK {
		t.Fatalf("ProjectCreate: %s", resp.Error)
	}
	mine := h.TaskAdd("proj", "a unit of work", nil).Data.(command.EventData).NodeID
	other := h.TaskAdd("proj", "another unit", nil).Data.(command.EventData).NodeID
	h.ReportAdd("proj", mine, "", "a report on my own node")

	reports, err := h.ReportsFor("proj", other)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 0 {
		t.Fatalf("reports on another node: %+v", reports)
	}
}
