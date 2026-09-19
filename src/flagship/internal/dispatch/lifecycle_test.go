package dispatch_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/dispatch"
	"github.com/flagship-dev/flagship/internal/store"
)

// fakeHerdr stands in for the herdr CLI: it records what was asked of it and
// returns canned results, so the gates run without a live terminal.
type fakeHerdr struct {
	paneID    string
	splitErr  error
	startErr  error
	promptErr error
	closeErr  error

	started  []string
	prompted []string
	closed   []string
}

func (f *fakeHerdr) SplitPane(cwd string) (string, error) {
	if f.splitErr != nil {
		return "", f.splitErr
	}
	return f.paneID, nil
}

func (f *fakeHerdr) StartAgent(paneID, name string) error {
	f.started = append(f.started, paneID+"/"+name)
	return f.startErr
}

func (f *fakeHerdr) Prompt(paneID, text string) error {
	f.prompted = append(f.prompted, paneID)
	return f.promptErr
}

func (f *fakeHerdr) ClosePane(paneID string) error {
	if f.closeErr != nil {
		return f.closeErr
	}
	f.closed = append(f.closed, paneID)
	return nil
}

// addCapNode records a cap node the way Prepare does, without a playbook.
func addCapNode(t *testing.T, f *fixture, goal string) string {
	t.Helper()
	added := f.h.TaskAdd("cap", goal, nil)
	if !added.OK {
		t.Fatalf("task add: %s", added.Error)
	}
	return added.Data.(command.EventData).NodeID
}

// recordDelivery appends the structural pane binding, as Deliver does.
func recordDelivery(t *testing.T, f *fixture, nodeID, paneID string) {
	t.Helper()
	resp := f.h.RecordDelivery("cap", nodeID, command.DeliveryRecord{
		PaneID: paneID, Agent: "dispatch-dev-task", Engine: "herdr",
	})
	if !resp.OK {
		t.Fatalf("RecordDelivery: %s", resp.Error)
	}
}

// addWorkerNode creates a node in the skills scope with the given status.
func addWorkerNode(t *testing.T, f *fixture, status string) string {
	t.Helper()
	added := f.h.TaskAdd("skills", "worker task", nil)
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

func capEvents(t *testing.T, f *fixture, eventType store.EventType) []store.Event {
	t.Helper()
	s, err := store.Open(f.storeDB)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	events, err := s.Replay("cap", &store.ReplayFilter{Types: []store.EventType{eventType}})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	return events
}

func TestDeliverRecordsThePaneBindingStructurally(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")

	herdr := &fakeHerdr{paneID: "w1:p9"}
	brief := &dispatch.Brief{Goal: "sample", Project: "skills", RootPath: root, TaskType: "dev-task", CapNodeID: nodeID}

	resp := dispatch.Deliver(f.h, herdr, brief)
	if !resp.OK {
		t.Fatalf("Deliver: %s", resp.Error)
	}
	if brief.Delivery == nil || brief.Delivery.PaneID != "w1:p9" || brief.Delivery.Engine != "herdr" {
		t.Fatalf("brief delivery = %+v, want the recorded binding", brief.Delivery)
	}
	if len(herdr.started) != 1 || herdr.started[0] != "w1:p9/dispatch-dev-task" {
		t.Errorf("started = %v, want the agent started in the new pane", herdr.started)
	}
	if len(herdr.prompted) != 1 || herdr.prompted[0] != "w1:p9" {
		t.Errorf("prompted = %v, want the brief sent to the new pane", herdr.prompted)
	}

	events := capEvents(t, f, store.DeliveryRecorded)
	if len(events) != 1 {
		t.Fatalf("cap has %d delivery-recorded events, want 1", len(events))
	}
	payload := string(events[0].Payload)
	for _, want := range []string{`"pane_id":"w1:p9"`, `"agent":"dispatch-dev-task"`, `"engine":"herdr"`} {
		if !strings.Contains(payload, want) {
			t.Errorf("delivery payload %s must carry %s", payload, want)
		}
	}

	// The prose decision is for the reader; the binding above is for the program.
	want := "delivered to pane w1:p9, agent dispatch-dev-task"
	if decisions := capDecisions(t, f, nodeID); !contains(decisions, want) {
		t.Errorf("decisions = %v, want %q", decisions, want)
	}
	if status := nodeStatus(t, f, "cap", nodeID); status != "active" {
		t.Errorf("cap node status = %q, want active", status)
	}
}

func TestDeliverFailsLeavesNodePendingAndClosesPane(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")

	herdr := &fakeHerdr{paneID: "w1:p9", promptErr: errors.New("agent_prompt_stalled")}
	brief := &dispatch.Brief{Goal: "sample", Project: "skills", RootPath: root, TaskType: "dev-task", CapNodeID: nodeID}

	resp := dispatch.Deliver(f.h, herdr, brief)
	if resp.OK {
		t.Fatal("a failed send must not report a delivery")
	}
	if !strings.Contains(resp.Error, nodeID) || !strings.Contains(resp.Error, "unresolved") {
		t.Errorf("error %q must name the unresolved cap node", resp.Error)
	}
	if len(herdr.closed) != 1 || herdr.closed[0] != "w1:p9" {
		t.Errorf("closed = %v, want the pane this delivery opened", herdr.closed)
	}
	if len(capEvents(t, f, store.DeliveryRecorded)) != 0 {
		t.Error("a failed delivery must not record a binding")
	}
	if status := nodeStatus(t, f, "cap", nodeID); status != "pending" {
		t.Errorf("cap node status = %q, want pending", status)
	}
}

func TestDeliverHerdrUnavailableLeavesNodePending(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")

	herdr := &fakeHerdr{splitErr: errors.New("exec: herdr: executable file not found in $PATH")}
	brief := &dispatch.Brief{Goal: "sample", Project: "skills", RootPath: root, TaskType: "dev-task", CapNodeID: nodeID}

	resp := dispatch.Deliver(f.h, herdr, brief)
	if resp.OK {
		t.Fatal("herdr being unavailable must fail the delivery")
	}
	if len(capEvents(t, f, store.DeliveryRecorded)) != 0 {
		t.Error("no binding may be recorded when the pane was never created")
	}
	if status := nodeStatus(t, f, "cap", nodeID); status != "pending" {
		t.Errorf("cap node status = %q, want pending", status)
	}
}

func TestCloseRefusesANonDispatchNode(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "cap loop")

	resp := dispatch.Close(f.h, &fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "x"})
	if resp.OK {
		t.Fatal("a non-dispatch cap node must not be closable as a dispatch")
	}
	if !strings.Contains(resp.Error, "not a dispatch node") {
		t.Errorf("error %q must say the node is not a dispatch", resp.Error)
	}
}

func TestCloseRefusesWithoutADeliveryRecord(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")

	resp := dispatch.Close(f.h, &fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Worker: "skills:x", Decision: "x"})
	if resp.OK {
		t.Fatal("a dispatch with no delivery record must not close")
	}
	for _, want := range []string{"no delivery recorded", "was this dispatched, or prepared and abandoned?"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}
}

func TestCloseRefusesAWorkerThatIsNotDone(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	recordDelivery(t, f, nodeID, "w1:p9")
	workerID := addWorkerNode(t, f, "active")

	resp := dispatch.Close(f.h, &fakeHerdr{}, dispatch.CloseRequest{
		NodeID: nodeID, Worker: "skills:" + workerID, Decision: "x",
	})
	if resp.OK {
		t.Fatal("an unfinished worker must block close-out")
	}
	for _, want := range []string{"worker skills:" + workerID, "is active", "let it finish", "--abandoned"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}
}

func TestCloseRefusesAnUnknownWorker(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	recordDelivery(t, f, nodeID, "w1:p9")

	resp := dispatch.Close(f.h, &fakeHerdr{}, dispatch.CloseRequest{
		NodeID: nodeID, Worker: "skills:t-00000000", Decision: "x",
	})
	if resp.OK {
		t.Fatal("a worker node that does not exist must block close-out")
	}
	if !strings.Contains(resp.Error, "worker skills:t-00000000 not found") {
		t.Errorf("error %q must name the missing worker", resp.Error)
	}
}

func TestCloseRefusesWithoutAVerdict(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	recordDelivery(t, f, nodeID, "w1:p9")
	workerID := addWorkerNode(t, f, "done")

	resp := dispatch.Close(f.h, &fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Worker: "skills:" + workerID})
	if resp.OK {
		t.Fatal("a close-out without a verdict must be refused")
	}
	if !strings.Contains(resp.Error, "--decision is required") {
		t.Errorf("error %q must require --decision", resp.Error)
	}
}

func TestCloseClosesThePaneAndMarksTheNodeDone(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	recordDelivery(t, f, nodeID, "w1:p9")
	workerID := addWorkerNode(t, f, "done")

	herdr := &fakeHerdr{}
	resp := dispatch.Close(f.h, herdr, dispatch.CloseRequest{
		NodeID: nodeID, Worker: "skills:" + workerID, Decision: "verified the worker's node",
	})
	if !resp.OK {
		t.Fatalf("Close: %s", resp.Error)
	}
	result, ok := resp.Data.(dispatch.CloseResult)
	if !ok || result.PaneID != "w1:p9" || result.Warning != "" {
		t.Fatalf("result = %#v, want the closed pane and no warning", resp.Data)
	}
	if len(herdr.closed) != 1 || herdr.closed[0] != "w1:p9" {
		t.Errorf("closed = %v, want w1:p9", herdr.closed)
	}
	if status := nodeStatus(t, f, "cap", nodeID); status != "done" {
		t.Errorf("cap node status = %q, want done", status)
	}
	if decisions := capDecisions(t, f, nodeID); !contains(decisions, "verified the worker's node") {
		t.Errorf("decisions = %v, want the verdict", decisions)
	}
}

func TestCloseSucceedsWhenThePaneIsAlreadyGone(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	recordDelivery(t, f, nodeID, "w1:p9")
	workerID := addWorkerNode(t, f, "done")

	resp := dispatch.Close(f.h, &fakeHerdr{closeErr: dispatch.ErrPaneGone}, dispatch.CloseRequest{
		NodeID: nodeID, Worker: "skills:" + workerID, Decision: "verified",
	})
	if !resp.OK {
		t.Fatalf("an already-closed pane must not fail the close-out: %s", resp.Error)
	}
	if result := resp.Data.(dispatch.CloseResult); result.Warning != "" {
		t.Errorf("warning = %q, want none for an already-closed pane", result.Warning)
	}
}

func TestCloseWarnsButSucceedsWhenHerdrIsUnavailable(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	recordDelivery(t, f, nodeID, "w1:p9")
	workerID := addWorkerNode(t, f, "done")

	herdr := &fakeHerdr{closeErr: errors.New("herdr: server is down")}
	resp := dispatch.Close(f.h, herdr, dispatch.CloseRequest{
		NodeID: nodeID, Worker: "skills:" + workerID, Decision: "verified",
	})
	if !resp.OK {
		t.Fatalf("close-out must not depend on the dispatcher being up: %s", resp.Error)
	}
	result := resp.Data.(dispatch.CloseResult)
	if !strings.Contains(result.Warning, "server is down") || !strings.Contains(result.Warning, "w1:p9") {
		t.Errorf("warning = %q, want the pane and the reason", result.Warning)
	}
	if status := nodeStatus(t, f, "cap", nodeID); status != "done" {
		t.Errorf("cap node status = %q, want done despite the warning", status)
	}
}

func TestCloseAbandonedRequiresAReason(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")

	resp := dispatch.Close(f.h, &fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Abandoned: true})
	if resp.OK {
		t.Fatal("--abandoned without --reason must be refused")
	}
	if !strings.Contains(resp.Error, "--reason") {
		t.Errorf("error %q must require --reason", resp.Error)
	}
}

func TestCloseAbandonedSkipsDeliveryAndWorkerGates(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")

	herdr := &fakeHerdr{}
	resp := dispatch.Close(f.h, herdr, dispatch.CloseRequest{
		NodeID: nodeID, Abandoned: true, Reason: "the user withdrew the request",
	})
	if !resp.OK {
		t.Fatalf("Close --abandoned: %s", resp.Error)
	}
	if len(herdr.closed) != 0 {
		t.Errorf("closed = %v, want no pane touched", herdr.closed)
	}
	want := "abandoned, never delivered: the user withdrew the request"
	if decisions := capDecisions(t, f, nodeID); !contains(decisions, want) {
		t.Errorf("decisions = %v, want %q", decisions, want)
	}
	if status := nodeStatus(t, f, "cap", nodeID); status != "done" {
		t.Errorf("cap node status = %q, want done", status)
	}
}

// nodeStatus reads one node's derived status the same way fs status reports it.
func nodeStatus(t *testing.T, f *fixture, scope, nodeID string) string {
	t.Helper()
	resp := f.h.Status(scope)
	if !resp.OK {
		t.Fatalf("Status: %s", resp.Error)
	}
	for _, task := range resp.Data.(command.StatusResult).Tasks {
		if task.NodeID == nodeID {
			return task.Status
		}
	}
	t.Fatalf("node %s not found in scope %s", nodeID, scope)
	return ""
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
