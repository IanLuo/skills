package dispatch_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/dispatch"
	"github.com/flagship-dev/flagship/internal/store"
)

// fakeHerdr stands in for the herdr CLI: it records what was asked of it and
// returns canned results, so the gates run without a live terminal.
type fakeHerdr struct {
	tabID       string
	paneID      string
	splitErr    error
	startErr    error
	promptErr   error
	closeErr    error
	agents      map[string]string
	agentsErr   error
	worktreeErr error

	// rootPaneID/rootPaneTab/rootPaneErr stand in for the root pane a worktree
	// workspace already has — the pane a worktree dispatch must start in.
	rootPaneID  string
	rootPaneTab string
	rootPaneErr error

	created []string
	started []string
	prompts []promptCall
	closed  []string
	removed []string
	rooted  []string
}

// promptCall is one brief handed to a pane, so a test can read the text the
// worker would have received.
type promptCall struct{ pane, text string }

func (f *fakeHerdr) SplitPane(cwd string) (string, string, error) {
	if f.splitErr != nil {
		return "", "", f.splitErr
	}
	f.created = append(f.created, cwd)
	return f.paneID, f.tabID, nil
}

func (f *fakeHerdr) WorktreeRootPane(wsID string) (string, string, error) {
	f.rooted = append(f.rooted, wsID)
	if f.rootPaneErr != nil {
		return "", "", f.rootPaneErr
	}
	return f.rootPaneID, f.rootPaneTab, nil
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

func (f *fakeHerdr) RemoveWorktree(wsID string) error {
	if f.worktreeErr != nil {
		return f.worktreeErr
	}
	f.removed = append(f.removed, wsID)
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
// tab carrying the brief, the worker's node in the target project, and the task
// type the exit gate is named after.
func recordDelivery(t *testing.T, f *fixture, capNode, paneID, workerID string) {
	t.Helper()
	recordTypedDelivery(t, f, capNode, paneID, workerID, "dev-task")
}

// recordTypedDelivery writes a delivery record for a named task type. A record
// written before the type existed carries none, which is the legacy shape.
func recordTypedDelivery(t *testing.T, f *fixture, capNode, paneID, workerID, taskType string) {
	t.Helper()
	resp := f.h.RecordDelivery("cap", capNode, command.DeliveryRecord{
		PaneID: paneID, TabID: "w1:t9", Agent: "dispatch-dev-task", Engine: "herdr",
		Project: "skills", Node: workerID, Type: taskType,
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

func TestDeliverRecordsThePaneAndWorkerBindingStructurally(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")

	herdr := &fakeHerdr{tabID: "w1:t9", paneID: "w1:p9"}
	brief := &dispatch.Brief{Goal: "sample", Project: "skills", RootPath: root, TaskType: "dev-task", CapNodeID: nodeID}

	resp := dispatch.Deliver(f.h, herdr, brief)
	if !resp.OK {
		t.Fatalf("Deliver: %s", resp.Error)
	}
	// The worker gets a sibling pane of the cap's own, at the project root, and
	// the agent runs in that pane.
	if len(herdr.created) != 1 || herdr.created[0] != root {
		t.Errorf("split panes = %v, want one split at %s", herdr.created, root)
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
		t.Errorf("started = %v, want the agent %s started in the new pane", herdr.started, wantAgent)
	}
	if len(herdr.prompts) != 1 || herdr.prompts[0].pane != "w1:p9" {
		t.Errorf("prompts = %v, want the brief sent to the new pane", herdr.prompts)
	}

	events := capEvents(t, f, store.DeliveryRecorded)
	if len(events) != 1 {
		t.Fatalf("cap has %d delivery-recorded events, want 1", len(events))
	}
	payload := string(events[0].Payload)
	for _, want := range []string{
		`"pane_id":"w1:p9"`, `"tab_id":"w1:t9"`, `"agent":"` + wantAgent + `"`,
		`"engine":"herdr"`, `"project":"skills"`,
		`"node":"` + workerID + `"`, `"type":"dev-task"`,
	} {
		if !strings.Contains(payload, want) {
			t.Errorf("delivery payload %s must carry %s", payload, want)
		}
	}

	// The prose decision is for the reader; the binding above is for the program.
	want := "delivered to pane w1:p9, agent " + wantAgent
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

func TestDeliverFailsLeavesNodePendingAndClosesTheWorkerPane(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")

	// A failure after the worker's node exists closes the pane and rolls the node
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
		t.Errorf("closed = %v, want the pane this delivery split", herdr.closed)
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

	herdr := &fakeHerdr{splitErr: errors.New("exec: herdr: executable file not found in $PATH")}
	brief := &dispatch.Brief{Goal: "sample", Project: "skills", RootPath: root, TaskType: "dev-task", CapNodeID: nodeID}

	resp := dispatch.Deliver(f.h, herdr, brief)
	if resp.OK {
		t.Fatal("herdr being unavailable must fail the delivery")
	}
	if len(capEvents(t, f, store.DeliveryRecorded)) != 0 {
		t.Error("no binding may be recorded when no pane was split")
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

// A dispatch that ran in a worktree records the worktree workspace, so close
// can remove it: the record, not the goal prose, is where close looks.
func TestDeliverRecordsTheWorktreeItRunsIn(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)
	capNode := addCapNode(t, f, "dispatch parallel: sample")

	herdr := &fakeHerdr{rootPaneID: "w7:p1", rootPaneTab: "w7:t1"}
	resp := dispatch.Deliver(f.h, herdr, &dispatch.Brief{
		Goal: "sample", Project: "skills", RootPath: root, TaskType: "parallel",
		CapNodeID: capNode, Worktree: "w7",
	})
	if !resp.OK {
		t.Fatalf("Deliver: %s", resp.Error)
	}

	payload := string(capEvents(t, f, store.DeliveryRecorded)[0].Payload)
	for _, want := range []string{`"type":"parallel"`, `"worktree":"w7"`} {
		if !strings.Contains(payload, want) {
			t.Errorf("delivery payload %s must carry %s", payload, want)
		}
	}
}

// A worktree dispatch must start the worker inside the worktree, so it starts it
// in the pane herdr already made for the workspace and splits nothing. Splitting
// the cap's pane would run the worker in the main checkout — the record would
// still claim isolation, which is worse than no isolation at all.
func TestDeliverStartsTheWorkerInTheWorktreeRootPane(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)
	capNode := addCapNode(t, f, "dispatch parallel: sample")

	herdr := &fakeHerdr{rootPaneID: "wA:p1", rootPaneTab: "wA:t1"}
	brief := &dispatch.Brief{
		Goal: "sample", Project: "skills", RootPath: root, TaskType: "parallel",
		CapNodeID: capNode, Worktree: "wA",
	}
	resp := dispatch.Deliver(f.h, herdr, brief)
	if !resp.OK {
		t.Fatalf("Deliver: %s", resp.Error)
	}

	if len(herdr.created) != 0 {
		t.Errorf("a worktree dispatch split the cap's pane at %v; it must use the worktree's own pane", herdr.created)
	}
	if len(herdr.rooted) != 1 || herdr.rooted[0] != "wA" {
		t.Errorf("root panes asked for = %v, want the worktree workspace wA", herdr.rooted)
	}

	workerID := strings.TrimPrefix(brief.WorkerNode, "skills:")
	agent := "dispatch-parallel-" + workerID
	if len(herdr.started) != 1 || herdr.started[0] != "wA:p1/"+agent {
		t.Errorf("started = %v, want %s started in the worktree's root pane wA:p1", herdr.started, agent)
	}
	if len(herdr.prompts) != 1 || herdr.prompts[0].pane != "wA:p1" {
		t.Errorf("prompts = %v, want the brief sent to wA:p1", herdr.prompts)
	}

	if brief.Delivery == nil || brief.Delivery.PaneID != "wA:p1" ||
		brief.Delivery.TabID != "wA:t1" || brief.Delivery.Worktree != "wA" {
		t.Fatalf("brief delivery = %+v, want the worktree's pane, tab, and workspace", brief.Delivery)
	}
	payload := string(capEvents(t, f, store.DeliveryRecorded)[0].Payload)
	for _, want := range []string{
		`"pane_id":"wA:p1"`, `"tab_id":"wA:t1"`, `"worktree":"wA"`, `"agent":"` + agent + `"`,
	} {
		if !strings.Contains(payload, want) {
			t.Errorf("delivery payload %s must carry %s", payload, want)
		}
	}
}

// A worktree workspace with no pane to start in is refused, and the refusal
// names the workspace. The refusal must not fall back to splitting the cap's
// pane: a silent fallback recreates the bug this fixes.
func TestDeliverRefusesAWorktreeWithoutARootPane(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)
	capNode := addCapNode(t, f, "dispatch parallel: sample")

	herdr := &fakeHerdr{rootPaneErr: fmt.Errorf("%w: herdr has no workspace wZZ", dispatch.ErrNoRootPane)}
	brief := &dispatch.Brief{
		Goal: "sample", Project: "skills", RootPath: root, TaskType: "parallel",
		CapNodeID: capNode, Worktree: "wZZ",
	}
	resp := dispatch.Deliver(f.h, herdr, brief)
	if resp.OK {
		t.Fatal("a worktree with no root pane must be refused, not delivered")
	}
	for _, want := range []string{"wZZ", "no workspace", capNode, "unresolved"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}

	if len(herdr.created) != 0 {
		t.Errorf("the refusal split the cap's pane at %v", herdr.created)
	}
	if len(herdr.started) != 0 || len(herdr.prompts) != 0 {
		t.Errorf("the refusal started work: started=%v prompts=%v", herdr.started, herdr.prompts)
	}
	if workers := scopeTasks(t, f, "skills"); len(workers) != 0 {
		t.Errorf("skills nodes = %+v, want none: no worker node for a refused dispatch", workers)
	}
	if events := capEvents(t, f, store.DeliveryRecorded); len(events) != 0 {
		t.Errorf("cap has %d delivery-recorded events, want none", len(events))
	}
}

func TestCloseRefusesANonDispatchNode(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "cap loop")

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "x"})
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

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Worker: "skills:x", Decision: "x"})
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

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "x"})
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

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "x"})
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

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID})
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
	resp := f.close(herdr, dispatch.CloseRequest{
		NodeID: nodeID, Decision: "verified the worker's node",
	})
	if !resp.OK {
		t.Fatalf("Close: %s", resp.Error)
	}
	result, ok := resp.Data.(dispatch.CloseResult)
	if !ok || result.PaneID != "w1:p9" || result.Warning != "" {
		t.Fatalf("result = %#v, want the closed pane and no warning", resp.Data)
	}
	if result.Worker != "skills:"+workerID {
		t.Errorf("result worker = %q, want the recorded node skills:%s", result.Worker, workerID)
	}
	// The pane is the whole teardown. The tab in the record is the cap's own —
	// the worker shares it — so close must never touch it. That is structural
	// rather than asserted: nothing in this package can close a tab.
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

// --worker is still accepted; against a matching record it is a check that the
// cap and the record agree, not a second source of truth.
func TestCloseAcceptsAWorkerThatMatchesTheRecord(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{
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
	resp := f.close(herdr, dispatch.CloseRequest{
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

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if resp.OK {
		t.Fatal("a record with no worker node must not be closed by guesswork")
	}
	for _, want := range []string{"carries no worker node", "--worker PROJECT:NODE"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}

	migrated := f.close(&fakeHerdr{}, dispatch.CloseRequest{
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

	resp := f.close(&fakeHerdr{closeErr: dispatch.ErrPaneGone}, dispatch.CloseRequest{
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
	resp := f.close(herdr, dispatch.CloseRequest{
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

// A record that names no tab — one written before the binding carried one, or
// one written by a herdr whose split response named the pane alone — must still
// close: close reads the pane, and the tab is the cap's own either way.
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
	resp := f.close(herdr, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if !resp.OK {
		t.Fatalf("a record with no tab must still close: %s", resp.Error)
	}
	if len(herdr.closed) != 1 || herdr.closed[0] != "w1:p9" {
		t.Errorf("closed = %v, want w1:p9", herdr.closed)
	}
}

func TestCloseAbandonedRequiresAReason(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Abandoned: true})
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
	resp := f.close(herdr, dispatch.CloseRequest{
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

// The exit gate is named by the record's type, not by the goal prose. A failing
// check refuses, fail-fast, naming the command and its output — the same shape
// as fs dispatch, and for the same reason. The sentinel proves the later check
// never ran, and a refused close leaves the node open.
func TestCloseRefusesAFailingCleanupCheckAndStopsTheRun(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "check3-ran")
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-cleanup", fmt.Sprintf(`name: dev-task-cleanup
type: cleanup
trigger: dev-task
steps:
  - check: true
  - check: echo boom >&2; false
  - check: touch %s
`, sentinel))

	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)

	herdr := &fakeHerdr{}
	resp := f.close(herdr, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if resp.OK {
		t.Fatal("a failing cleanup check must refuse the close")
	}
	for _, want := range []string{"cleanup check failed", `"echo boom >&2; false"`, "boom", "fix it"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("check 3 ran despite check 2 failing: stat err = %v", err)
	}
	if len(herdr.closed) != 0 || len(herdr.removed) != 0 {
		t.Errorf("a refused close touched herdr: closed=%v removed=%v", herdr.closed, herdr.removed)
	}
	if status := nodeStatus(t, f, "cap", nodeID); status == "done" {
		t.Errorf("cap node status = %q, a refused close must leave it open", status)
	}
}

// A type with no cleanup playbook has no gate — and the close says so, because
// a missing exit gate must be visible, never silent.
func TestCloseSaysWhenThereIsNoCleanupPlaybook(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordTypedDelivery(t, f, nodeID, "w1:p9", workerID, "parallel")

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if !resp.OK {
		t.Fatalf("a missing cleanup playbook must not block the close: %s", resp.Error)
	}
	result := resp.Data.(dispatch.CloseResult)
	if !strings.Contains(result.Cleanup, "no cleanup gate") ||
		!strings.Contains(result.Cleanup, "parallel-cleanup.yaml") {
		t.Errorf("cleanup note = %q, want it to name the missing playbook", result.Cleanup)
	}
}

// A record written before types existed carries none, so close cannot name a
// gate. It says that too rather than silently skipping the exit gate.
func TestCloseSaysWhenTheRecordCarriesNoType(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordLegacyDelivery(t, f, nodeID, "w1:p9")

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{
		NodeID: nodeID, Worker: "skills:" + workerID, Decision: "verified",
	})
	if !resp.OK {
		t.Fatalf("close: %s", resp.Error)
	}
	if note := resp.Data.(dispatch.CloseResult).Cleanup; !strings.Contains(note, "no cleanup gate") ||
		!strings.Contains(note, "no task type") {
		t.Errorf("cleanup note = %q, want it to say the record names no type", note)
	}
}

// The steps only the user can answer are refused until --confirm says they have
// been answered, and then each is recorded as its own decision — so the log
// names what was confirmed, not just that something was.
func TestCloseRequiresConfirmForTheCleanupAsks(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-cleanup", `name: dev-task-cleanup
type: cleanup
trigger: dev-task
steps:
  - check: true
  - ask: was the merge reviewed before this closed
  - ask: is the follow-up work tracked
`)

	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if resp.OK {
		t.Fatal("an unconfirmed ask step must refuse the close")
	}
	for _, want := range []string{
		"unconfirmed steps",
		`"was the merge reviewed before this closed"`,
		`"is the follow-up work tracked"`,
		"--confirm",
	} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}

	if status := nodeStatus(t, f, "cap", nodeID); status == "done" {
		t.Errorf("a refused close must leave the node open, got %q", status)
	}

	confirmed := f.close(&fakeHerdr{}, dispatch.CloseRequest{
		NodeID: nodeID, Decision: "verified", Confirm: true,
	})
	if !confirmed.OK {
		t.Fatalf("Close --confirm: %s", confirmed.Error)
	}
	decisions := capDecisions(t, f, nodeID)
	for _, want := range []string{
		"user confirmed cleanup step: was the merge reviewed before this closed",
		"user confirmed cleanup step: is the follow-up work tracked",
	} {
		if !contains(decisions, want) {
			t.Errorf("decisions = %v, want %q", decisions, want)
		}
	}
	if note := confirmed.Data.(dispatch.CloseResult).Cleanup; !strings.Contains(note, "2 confirmed") {
		t.Errorf("cleanup note = %q, want the confirmed asks counted", note)
	}
}

// Close removes the worktree the record names, so a worktree cannot outlive a
// closed node. One already gone is the goal state, and herdr being unavailable
// is a warning — the same rules the pane teardown follows.
func TestCloseRemovesTheRecordedWorktree(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "done")
	if resp := f.h.RecordDelivery("cap", nodeID, command.DeliveryRecord{
		PaneID: "w7:p1", TabID: "w7:t1", Agent: "dispatch-parallel", Engine: "herdr",
		Project: "skills", Node: workerID, Type: "parallel", Worktree: "w7",
	}); !resp.OK {
		t.Fatalf("RecordDelivery: %s", resp.Error)
	}

	herdr := &fakeHerdr{}
	resp := f.close(herdr, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if !resp.OK {
		t.Fatalf("Close: %s", resp.Error)
	}
	if len(herdr.removed) != 1 || herdr.removed[0] != "w7" {
		t.Errorf("removed = %v, want the recorded worktree w7", herdr.removed)
	}
	if result := resp.Data.(dispatch.CloseResult); result.Warning != "" {
		t.Errorf("warning = %q, want none", result.Warning)
	}
}

func TestCloseToleratesAnAlreadyRemovedWorktree(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "done")
	if resp := f.h.RecordDelivery("cap", nodeID, command.DeliveryRecord{
		PaneID: "w7:p1", TabID: "w7:t1", Agent: "dispatch-parallel", Engine: "herdr",
		Project: "skills", Node: workerID, Type: "parallel", Worktree: "w7",
	}); !resp.OK {
		t.Fatalf("RecordDelivery: %s", resp.Error)
	}

	resp := f.close(&fakeHerdr{worktreeErr: dispatch.ErrWorktreeGone}, dispatch.CloseRequest{
		NodeID: nodeID, Decision: "verified",
	})
	if !resp.OK {
		t.Fatalf("an already-removed worktree must not fail the close: %s", resp.Error)
	}
	if result := resp.Data.(dispatch.CloseResult); result.Warning != "" {
		t.Errorf("warning = %q, want none for an already-removed worktree", result.Warning)
	}
	if status := nodeStatus(t, f, "cap", nodeID); status != "done" {
		t.Errorf("cap node status = %q, want done", status)
	}
}

func TestCloseWarnsButSucceedsWhenTheWorktreeCannotBeRemoved(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "done")
	if resp := f.h.RecordDelivery("cap", nodeID, command.DeliveryRecord{
		PaneID: "w7:p1", TabID: "w7:t1", Agent: "dispatch-parallel", Engine: "herdr",
		Project: "skills", Node: workerID, Type: "parallel", Worktree: "w7",
	}); !resp.OK {
		t.Fatalf("RecordDelivery: %s", resp.Error)
	}

	resp := f.close(&fakeHerdr{worktreeErr: errors.New("herdr: server is down")}, dispatch.CloseRequest{
		NodeID: nodeID, Decision: "verified",
	})
	if !resp.OK {
		t.Fatalf("close-out must not depend on the dispatcher being up: %s", resp.Error)
	}
	result := resp.Data.(dispatch.CloseResult)
	if !strings.Contains(result.Warning, "server is down") || !strings.Contains(result.Warning, "w7") {
		t.Errorf("warning = %q, want the worktree and the reason", result.Warning)
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

// The trigger rule is the same at exit as at entry: fs close loads
// "<type>-cleanup", so a cleanup playbook whose trigger names another type is
// refused rather than run.
func TestCloseRefusesACleanupPlaybookWhoseTriggerContradictsItsType(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-cleanup", `name: dev-task-cleanup
type: cleanup
trigger: nonsense
steps:
  - check: true
`)

	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if resp.OK {
		t.Fatal("a contradictory cleanup trigger must refuse the close")
	}
	for _, want := range []string{"nonsense", `"dev-task"`, "empty"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}
	if status := nodeStatus(t, f, "cap", nodeID); status == "done" {
		t.Errorf("a refused close must leave the node open, got %q", status)
	}
}
