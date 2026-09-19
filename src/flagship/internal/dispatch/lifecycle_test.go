package dispatch_test

import (
	"encoding/json"
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
	tabID     string
	paneID    string
	createErr error
	startErr  error
	promptErr error
	closeErr  error
	tabClose  error
	agents    map[string]string
	agentsErr error

	created []string
	started []string
	prompts []promptCall
	closed  []string
	tabs    []string
}

// promptCall is one brief handed to a pane, so a test can read the text the
// worker would have received.
type promptCall struct{ pane, text string }

func (f *fakeHerdr) CreateTab(cwd string) (string, string, error) {
	if f.createErr != nil {
		return "", "", f.createErr
	}
	f.created = append(f.created, cwd)
	return f.tabID, f.paneID, nil
}

func (f *fakeHerdr) StartAgent(paneID, name string) error {
	f.started = append(f.started, paneID+"/"+name)
	return f.startErr
}

func (f *fakeHerdr) Prompt(paneID, text string) error {
	f.prompts = append(f.prompts, promptCall{pane: paneID, text: text})
	return f.promptErr
}

func (f *fakeHerdr) Agents() (map[string]string, error) {
	if f.agentsErr != nil {
		return nil, f.agentsErr
	}
	return f.agents, nil
}

func (f *fakeHerdr) ClosePane(paneID string) error {
	if f.closeErr != nil {
		return f.closeErr
	}
	f.closed = append(f.closed, paneID)
	return nil
}

func (f *fakeHerdr) CloseTab(tabID string) error {
	if f.tabClose != nil {
		return f.tabClose
	}
	f.tabs = append(f.tabs, tabID)
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

// recordDelivery appends the structural binding as Deliver does: the pane and
// tab carrying the brief, and the worker's node in the target project.
func recordDelivery(t *testing.T, f *fixture, capNode, paneID, workerID string) {
	t.Helper()
	resp := f.h.RecordDelivery("cap", capNode, command.DeliveryRecord{
		PaneID: paneID, TabID: "w1:t9", Agent: "dispatch-dev-task", Engine: "herdr",
		Project: "skills", Node: workerID,
	})
	if !resp.OK {
		t.Fatalf("RecordDelivery: %s", resp.Error)
	}
}

// recordLegacyDelivery writes the pane binding the way the binary did before the
// worker's node was part of it: pane, agent, and engine only.
func recordLegacyDelivery(t *testing.T, f *fixture, capNode, paneID string) {
	t.Helper()
	s, err := store.Open(f.storeDB)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	payload, err := json.Marshal(map[string]string{
		"pane_id": paneID, "agent": "dispatch-dev-task", "engine": "herdr",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(store.Event{
		Type: store.DeliveryRecorded, ProjectID: "cap", NodeID: &capNode, Payload: payload,
	}); err != nil {
		t.Fatalf("append legacy delivery-recorded: %v", err)
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

func TestDeliverRecordsTheTabPaneAndWorkerBindingStructurally(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")

	herdr := &fakeHerdr{tabID: "w1:t9", paneID: "w1:p9"}
	brief := &dispatch.Brief{Goal: "sample", Project: "skills", RootPath: root, TaskType: "dev-task", CapNodeID: nodeID}

	resp := dispatch.Deliver(f.h, herdr, brief)
	if !resp.OK {
		t.Fatalf("Deliver: %s", resp.Error)
	}
	// The worker gets a tab of its own, at the project root, and the agent runs
	// in that tab's root pane.
	if len(herdr.created) != 1 || herdr.created[0] != root {
		t.Errorf("created tabs = %v, want one rooted at %s", herdr.created, root)
	}
	if brief.Delivery == nil || brief.Delivery.PaneID != "w1:p9" ||
		brief.Delivery.TabID != "w1:t9" || brief.Delivery.Engine != "herdr" {
		t.Fatalf("brief delivery = %+v, want the recorded binding", brief.Delivery)
	}
	// The agent is named after the node it owns, so two dispatches of one type
	// running at once are two names rather than one name in two panes.
	workerID := strings.TrimPrefix(brief.WorkerNode, "skills:")
	wantAgent := "dispatch-dev-task-" + workerID
	if len(herdr.started) != 1 || herdr.started[0] != "w1:p9/"+wantAgent {
		t.Errorf("started = %v, want the agent %s started in the new tab's root pane", herdr.started, wantAgent)
	}
	if len(herdr.prompts) != 1 || herdr.prompts[0].pane != "w1:p9" {
		t.Errorf("prompts = %v, want the brief sent to the new tab's root pane", herdr.prompts)
	}

	events := capEvents(t, f, store.DeliveryRecorded)
	if len(events) != 1 {
		t.Fatalf("cap has %d delivery-recorded events, want 1", len(events))
	}
	payload := string(events[0].Payload)
	for _, want := range []string{
		`"pane_id":"w1:p9"`, `"tab_id":"w1:t9"`, `"agent":"` + wantAgent + `"`,
		`"engine":"herdr"`, `"project":"skills"`,
		`"node":"` + workerID + `"`,
	} {
		if !strings.Contains(payload, want) {
			t.Errorf("delivery payload %s must carry %s", payload, want)
		}
	}

	// The prose decision is for the reader; the binding above is for the program.
	want := "delivered to pane w1:p9 in tab w1:t9, agent " + wantAgent
	if decisions := capDecisions(t, f, nodeID); !contains(decisions, want) {
		t.Errorf("decisions = %v, want %q", decisions, want)
	}
	if status := nodeStatus(t, f, "cap", nodeID); status != "active" {
		t.Errorf("cap node status = %q, want active", status)
	}
}

// The delivery creates the worker's node in the target project — carrying the
// cap node's goal — and the brief tells the worker that node is already its own.
func TestDeliverCreatesTheWorkerNodeAndNamesItInTheBrief(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)
	capNode := addCapNode(t, f, "dispatch dev-task: sample")

	herdr := &fakeHerdr{tabID: "w1:t9", paneID: "w1:p9"}
	resp := dispatch.Deliver(f.h, herdr, &dispatch.Brief{
		Goal: "sample", Project: "skills", RootPath: root, TaskType: "dev-task", CapNodeID: capNode,
	})
	if !resp.OK {
		t.Fatalf("Deliver: %s", resp.Error)
	}
	brief := resp.Data.(*dispatch.Brief)

	workerID, ok := strings.CutPrefix(brief.WorkerNode, "skills:")
	if !ok || workerID == "" {
		t.Fatalf("worker_node = %q, want skills:<node>", brief.WorkerNode)
	}
	worker, found := scopeTask(t, f, "skills", workerID)
	if !found {
		t.Fatalf("worker node %s is not in the skills scope", brief.WorkerNode)
	}
	if worker.Status != "pending" {
		t.Errorf("worker node status = %q, want pending for the worker to pick up", worker.Status)
	}
	if worker.Goal != "dispatch dev-task: sample" {
		t.Errorf("worker node goal = %q, want the cap node's goal", worker.Goal)
	}

	if len(herdr.prompts) != 1 {
		t.Fatalf("prompts = %v, want the brief sent once", herdr.prompts)
	}
	briefText := herdr.prompts[0].text
	if !strings.Contains(briefText, "Note your work on "+brief.WorkerNode) {
		t.Errorf("the brief must name the worker's node; got:\n%s", briefText)
	}
	if !strings.Contains(briefText, "do not create another") {
		t.Errorf("the brief must say the node is the worker's, not one to invent; got:\n%s", briefText)
	}
}

func TestDeliverFailsLeavesNodePendingAndClosesTheWorkerTab(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")

	// A failure after the worker's node exists closes the tab and rolls the node
	// back: the store cannot take an event back, so it is marked done with the
	// reason, and nothing is left outstanding in the target project.
	herdr := &fakeHerdr{tabID: "w1:t9", paneID: "w1:p9", promptErr: errors.New("agent_prompt_stalled")}
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
	// Closing the pane is not enough on its own: the tab this delivery opened
	// must not be left behind.
	if len(herdr.tabs) != 1 || herdr.tabs[0] != "w1:t9" {
		t.Errorf("closed tabs = %v, want the tab this delivery opened", herdr.tabs)
	}
	if len(capEvents(t, f, store.DeliveryRecorded)) != 0 {
		t.Error("a failed delivery must not record a binding")
	}
	if status := nodeStatus(t, f, "cap", nodeID); status != "pending" {
		t.Errorf("cap node status = %q, want pending", status)
	}

	unfinished, err := f.h.UnfinishedIn("skills")
	if err != nil {
		t.Fatal(err)
	}
	if len(unfinished) != 0 {
		t.Errorf("skills has %d outstanding nodes after a failed delivery, want 0: %v", len(unfinished), unfinished)
	}
	workers := scopeTasks(t, f, "skills")
	if len(workers) != 1 || workers[0].Status != "done" {
		t.Fatalf("skills nodes = %+v, want the one rolled-back node, done", workers)
	}
	want := "dispatch never delivered: send brief: agent_prompt_stalled"
	if !contains(workers[0].Decisions, want) {
		t.Errorf("rolled-back node decisions = %v, want %q", workers[0].Decisions, want)
	}
}

// With herdr unavailable the failure precedes the worker's node, so there is
// nothing to roll back and the target project stays untouched.
func TestDeliverHerdrUnavailableLeavesNodePending(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")

	herdr := &fakeHerdr{createErr: errors.New("exec: herdr: executable file not found in $PATH")}
	brief := &dispatch.Brief{Goal: "sample", Project: "skills", RootPath: root, TaskType: "dev-task", CapNodeID: nodeID}

	resp := dispatch.Deliver(f.h, herdr, brief)
	if resp.OK {
		t.Fatal("herdr being unavailable must fail the delivery")
	}
	if len(capEvents(t, f, store.DeliveryRecorded)) != 0 {
		t.Error("no binding may be recorded when the tab was never created")
	}
	if status := nodeStatus(t, f, "cap", nodeID); status != "pending" {
		t.Errorf("cap node status = %q, want pending", status)
	}
	if workers := scopeTasks(t, f, "skills"); len(workers) != 0 {
		t.Errorf("skills nodes = %+v, want none: no node may be created for a delivery that never happened", workers)
	}
}

// Two dispatches of one type running at once are two agents, not one name in
// two panes: the name carries the worker's node, and the record carries the
// name herdr knows.
func TestDeliverNamesEachAgentAfterItsOwnWorkerNode(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)

	first := &dispatch.Brief{Goal: "first", Project: "skills", RootPath: root, TaskType: "dev-task", CapNodeID: addCapNode(t, f, "dispatch dev-task: first")}
	second := &dispatch.Brief{Goal: "second", Project: "skills", RootPath: root, TaskType: "dev-task", CapNodeID: addCapNode(t, f, "dispatch dev-task: second")}

	herdr := &fakeHerdr{tabID: "w1:t9", paneID: "w1:p9"}
	for _, brief := range []*dispatch.Brief{first, second} {
		if resp := dispatch.Deliver(f.h, herdr, brief); !resp.OK {
			t.Fatalf("Deliver %s: %s", brief.Goal, resp.Error)
		}
	}

	if len(herdr.started) != 2 {
		t.Fatalf("started = %v, want two agents", herdr.started)
	}
	if herdr.started[0] == herdr.started[1] {
		t.Fatalf("both dispatches started the same agent name %q", herdr.started[0])
	}
	for _, brief := range []*dispatch.Brief{first, second} {
		agent := "dispatch-dev-task-" + strings.TrimPrefix(brief.WorkerNode, "skills:")
		if !contains(herdr.started, "w1:p9/"+agent) {
			t.Errorf("started = %v, want %s", herdr.started, agent)
		}
		if brief.Delivery == nil || brief.Delivery.Agent != agent {
			t.Errorf("delivery for %q = %+v, want agent %s", brief.Goal, brief.Delivery, agent)
		}
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

// The worker's node comes from the delivery record, so a cap that passes no
// --worker still cannot close out work that is not done.
func TestCloseRefusesAWorkerThatIsNotDone(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "active")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)

	resp := dispatch.Close(f.h, &fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "x"})
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
	recordDelivery(t, f, nodeID, "w1:p9", "t-00000000")

	resp := dispatch.Close(f.h, &fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "x"})
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
	workerID := addWorkerNode(t, f, "done")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)

	resp := dispatch.Close(f.h, &fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID})
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
	workerID := addWorkerNode(t, f, "done")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)

	herdr := &fakeHerdr{}
	resp := dispatch.Close(f.h, herdr, dispatch.CloseRequest{
		NodeID: nodeID, Decision: "verified the worker's node",
	})
	if !resp.OK {
		t.Fatalf("Close: %s", resp.Error)
	}
	result, ok := resp.Data.(dispatch.CloseResult)
	if !ok || result.PaneID != "w1:p9" || result.Warning != "" {
		t.Fatalf("result = %#v, want the closed pane and no warning", resp.Data)
	}
	if result.TabID != "w1:t9" {
		t.Errorf("result tab = %q, want the recorded tab w1:t9", result.TabID)
	}
	if result.Worker != "skills:"+workerID {
		t.Errorf("result worker = %q, want the recorded node skills:%s", result.Worker, workerID)
	}
	if len(herdr.closed) != 1 || herdr.closed[0] != "w1:p9" {
		t.Errorf("closed = %v, want w1:p9", herdr.closed)
	}
	// Closing the pane is not the whole teardown: an empty tab must not be left
	// behind, so the tab from the record is closed too.
	if len(herdr.tabs) != 1 || herdr.tabs[0] != "w1:t9" {
		t.Errorf("closed tabs = %v, want w1:t9", herdr.tabs)
	}
	if status := nodeStatus(t, f, "cap", nodeID); status != "done" {
		t.Errorf("cap node status = %q, want done", status)
	}
	if decisions := capDecisions(t, f, nodeID); !contains(decisions, "verified the worker's node") {
		t.Errorf("decisions = %v, want the verdict", decisions)
	}
}

// --worker is still accepted; against a matching record it is a check that the
// cap and the record agree, not a second source of truth.
func TestCloseAcceptsAWorkerThatMatchesTheRecord(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)

	resp := dispatch.Close(f.h, &fakeHerdr{}, dispatch.CloseRequest{
		NodeID: nodeID, Worker: "skills:" + workerID, Decision: "verified",
	})
	if !resp.OK {
		t.Fatalf("a --worker that matches the record must close: %s", resp.Error)
	}
}

// Two dispatches into one project must not be confusable: --worker naming a
// different node is refused against the recorded one, before any pane is
// touched.
func TestCloseRefusesAMismatchedWorker(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	otherID := addWorkerNode(t, f, "done")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)

	herdr := &fakeHerdr{}
	resp := dispatch.Close(f.h, herdr, dispatch.CloseRequest{
		NodeID: nodeID, Worker: "skills:" + otherID, Decision: "verified",
	})
	if resp.OK {
		t.Fatal("a --worker that is not the recorded node must be refused")
	}
	for _, want := range []string{"skills:" + otherID, "does not match", "skills:" + workerID, "wrong work"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}
	if len(herdr.closed) != 0 {
		t.Errorf("closed = %v, want no pane touched by a refused close", herdr.closed)
	}
	if status := nodeStatus(t, f, "cap", nodeID); status == "done" {
		t.Errorf("cap node status = %q, a refused close must not close it", status)
	}
}

// A record written before the link existed carries no node. Close without
// --worker refuses and names the one way such a record can be closed; with
// --worker it still works, so an old dispatch is not stranded.
func TestCloseRefusesARecordWithNoNode(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordLegacyDelivery(t, f, nodeID, "w1:p9")

	resp := dispatch.Close(f.h, &fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if resp.OK {
		t.Fatal("a record with no worker node must not be closed by guesswork")
	}
	for _, want := range []string{"carries no worker node", "--worker PROJECT:NODE"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}

	migrated := dispatch.Close(f.h, &fakeHerdr{}, dispatch.CloseRequest{
		NodeID: nodeID, Worker: "skills:" + workerID, Decision: "verified",
	})
	if !migrated.OK {
		t.Fatalf("the old --worker form must still close an old record: %s", migrated.Error)
	}
	if result := migrated.Data.(dispatch.CloseResult); result.Worker != "skills:"+workerID {
		t.Errorf("result worker = %q, want the node the cap named", result.Worker)
	}
}

func TestCloseSucceedsWhenThePaneIsAlreadyGone(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)

	resp := dispatch.Close(f.h, &fakeHerdr{closeErr: dispatch.ErrPaneGone}, dispatch.CloseRequest{
		NodeID: nodeID, Decision: "verified",
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
	workerID := addWorkerNode(t, f, "done")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)

	herdr := &fakeHerdr{closeErr: errors.New("herdr: server is down")}
	resp := dispatch.Close(f.h, herdr, dispatch.CloseRequest{
		NodeID: nodeID, Decision: "verified",
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

// A record written before the worker got a tab of its own carries no tab, and
// must still close: there is nothing to assert the absence of.
func TestCloseToleratesARecordWithNoTab(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	if resp := f.h.RecordDelivery("cap", nodeID, command.DeliveryRecord{
		PaneID: "w1:p9", Agent: "dispatch-dev-task", Engine: "herdr",
		Project: "skills", Node: workerID,
	}); !resp.OK {
		t.Fatalf("RecordDelivery: %s", resp.Error)
	}

	herdr := &fakeHerdr{}
	resp := dispatch.Close(f.h, herdr, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if !resp.OK {
		t.Fatalf("a pre-tab record must still close: %s", resp.Error)
	}
	if len(herdr.closed) != 1 || herdr.closed[0] != "w1:p9" {
		t.Errorf("closed = %v, want w1:p9", herdr.closed)
	}
	if len(herdr.tabs) != 0 {
		t.Errorf("closed tabs = %v, want none for a record that names no tab", herdr.tabs)
	}
}

// Closing the pane of a tab can remove the tab with it, so a tab herdr no
// longer knows about is the goal state — not a warning.
func TestCloseSucceedsWhenTheTabIsAlreadyGone(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)

	resp := dispatch.Close(f.h, &fakeHerdr{tabClose: dispatch.ErrTabGone}, dispatch.CloseRequest{
		NodeID: nodeID, Decision: "verified",
	})
	if !resp.OK {
		t.Fatalf("an already-closed tab must not fail the close-out: %s", resp.Error)
	}
	if result := resp.Data.(dispatch.CloseResult); result.Warning != "" {
		t.Errorf("warning = %q, want none for an already-closed tab", result.Warning)
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

// scopeTasks returns every node in a scope, via derived state.
func scopeTasks(t *testing.T, f *fixture, scope string) []command.TaskInfo {
	t.Helper()
	resp := f.h.Status(scope)
	if !resp.OK {
		t.Fatalf("Status: %s", resp.Error)
	}
	return resp.Data.(command.StatusResult).Tasks
}

// scopeTask returns one node's derived state, or ok=false when the scope has no
// such node.
func scopeTask(t *testing.T, f *fixture, scope, nodeID string) (command.TaskInfo, bool) {
	t.Helper()
	for _, task := range scopeTasks(t, f, scope) {
		if task.NodeID == nodeID {
			return task, true
		}
	}
	return command.TaskInfo{}, false
}

// nodeStatus reads one node's derived status the same way fs status reports it.
func nodeStatus(t *testing.T, f *fixture, scope, nodeID string) string {
	t.Helper()
	task, ok := scopeTask(t, f, scope, nodeID)
	if !ok {
		t.Fatalf("node %s not found in scope %s", nodeID, scope)
	}
	return task.Status
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
