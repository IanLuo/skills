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
	"github.com/flagship-dev/flagship/internal/query"
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

	// checkoutPath/checkoutErr stand in for the checkout path a worktree
	// workspace resolves to — the tree the cleanup gate must run its checks in.
	checkoutPath string
	checkoutErr  error

	created   []string
	started   []string
	prompts   []promptCall
	closed    []string
	removed   []string
	rooted    []string
	checkouts []string
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

func (f *fakeHerdr) WorktreeRootPane(wsID string) (string, string, string, error) {
	f.rooted = append(f.rooted, wsID)
	if f.rootPaneErr != nil {
		return "", "", "", f.rootPaneErr
	}
	return f.rootPaneID, f.rootPaneTab, f.checkoutPath, nil
}

func (f *fakeHerdr) WorktreeCheckout(wsID string) (string, error) {
	f.checkouts = append(f.checkouts, wsID)
	if f.checkoutErr != nil {
		return "", f.checkoutErr
	}
	return f.checkoutPath, nil
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

// fakeGitWorktrees stands in for git's worktree bookkeeping: it says which
// worktrees the repository has registered and records what was removed, so the
// teardown gate can be exercised against a leak — and against a removal that
// leaves the worktree where it was — without a real repository.
type fakeGitWorktrees struct {
	// listed is what git reports. Remove clears the entry, unless keepListed
	// models a removal that reports success and changes nothing.
	listed     []string
	keepListed bool
	listErr    error
	removeErr  error

	lists   int
	removed []string
}

func (f *fakeGitWorktrees) List(repo string) ([]string, error) {
	f.lists++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listed, nil
}

func (f *fakeGitWorktrees) Remove(repo, path string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, path)
	if !f.keepListed {
		listed := f.listed[:0]
		for _, candidate := range f.listed {
			if candidate != path {
				listed = append(listed, candidate)
			}
		}
		f.listed = listed
	}
	return nil
}

// addCapNode records a dispatch node in the cap scope the way Prepare does,
// without a playbook: the kind carries what the goal prefix used to.
func addCapNode(t *testing.T, f *fixture, goal string) string {
	t.Helper()
	return addCapKindNode(t, f, goal, query.KindDispatch)
}

// addCapKindNode records a cap node of a named kind, so a test can show the
// dispatch gates pass over ordinary cap work.
func addCapKindNode(t *testing.T, f *fixture, goal string, kind query.Kind) string {
	t.Helper()
	added := f.h.TaskAddKind("cap", goal, kind, "", "", nil)
	if !added.OK {
		t.Fatalf("task add: %s", added.Error)
	}
	return added.Data.(command.EventData).NodeID
}

// addIntegrationNode records an integration dispatch that names a member, the
// way fs dispatch --integrates does: one task-created event carrying the link.
func addIntegrationNode(t *testing.T, f *fixture, member string) string {
	t.Helper()
	added := f.h.TaskAddKind("cap", "dispatch integrate: merge the member", query.KindDispatch, "", member, nil)
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

// recordWorktreeDelivery writes a delivery record bound to a worktree workspace,
// the shape a worktree dispatch records: a batch's workers ran in one, the
// cleanup gate has to find that tree, and the teardown has to verify it gone
// afterwards. path is the checkout the workspace was made from — what the record
// keeps so the worktree can be removed after the workspace is forgotten.
func recordWorktreeDelivery(t *testing.T, f *fixture, capNode, paneID, workerID, wsID, path string) {
	t.Helper()
	resp := f.h.RecordDelivery("cap", capNode, command.DeliveryRecord{
		PaneID: paneID, TabID: wsID + ":t1", Agent: "dispatch-parallel", Engine: "herdr",
		Project: "skills", Node: workerID, Type: "parallel", Worktree: wsID, WorktreePath: path,
	})
	if !resp.OK {
		t.Fatalf("RecordDelivery: %s", resp.Error)
	}
}

// addWorkerNode creates the worker's node in the skills scope, the way Deliver
// does — a dispatch node — with the given status.
func addWorkerNode(t *testing.T, f *fixture, status string) string {
	t.Helper()
	added := f.h.TaskAddKind("skills", "worker task", query.KindDispatch, "", "", nil)
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
	if worker.Kind != query.KindDispatch {
		t.Errorf("worker node kind = %q, want %q", worker.Kind, query.KindDispatch)
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
	// Every worker is told how a gap outlives it, with the found_by set to the
	// node it owns — the message is not read and closing destroys the transcript.
	for _, want := range []string{
		"Gaps:", "--kind gap", "--found-by " + brief.WorkerNode, "your final message is not read",
	} {
		if !strings.Contains(briefText, want) {
			t.Errorf("the brief must carry the worker obligation %q; got:\n%s", want, briefText)
		}
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

// A dispatch that ran in a worktree records the worktree workspace and the
// checkout it was made from, so close can remove it: the record, not the goal
// prose, is where close looks. The path is recorded because the workspace id
// can be gone by close — herdr forgetting it does not make the checkout
// disappear — and then the id alone names nothing that can be removed.
func TestDeliverRecordsTheWorktreeItRunsIn(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)
	capNode := addCapNode(t, f, "dispatch parallel: sample")

	herdr := &fakeHerdr{rootPaneID: "w7:p1", rootPaneTab: "w7:t1", checkoutPath: "/checkouts/w7"}
	brief := &dispatch.Brief{
		Goal: "sample", Project: "skills", RootPath: root, TaskType: "parallel",
		CapNodeID: capNode, Worktree: "w7",
	}
	resp := dispatch.Deliver(f.h, herdr, brief)
	if !resp.OK {
		t.Fatalf("Deliver: %s", resp.Error)
	}
	if brief.Delivery == nil || brief.Delivery.WorktreePath != "/checkouts/w7" {
		t.Errorf("brief delivery = %+v, want the worktree's checkout path", brief.Delivery)
	}

	payload := string(capEvents(t, f, store.DeliveryRecorded)[0].Payload)
	for _, want := range []string{
		`"type":"parallel"`, `"worktree":"w7"`, `"worktree_path":"/checkouts/w7"`,
	} {
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

	herdr := &fakeHerdr{rootPaneID: "wA:p1", rootPaneTab: "wA:t1", checkoutPath: "/checkouts/wA"}
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
	nodeID := addCapKindNode(t, f, "cap loop", query.KindWork)

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "x"})
	if resp.OK {
		t.Fatal("a non-dispatch cap node must not be closable as a dispatch")
	}
	if !strings.Contains(resp.Error, "not a dispatch node") {
		t.Errorf("error %q must say the node is not a dispatch", resp.Error)
	}
}

// Close reads the node's kind, so a dispatch whose goal was rewritten is still
// the dispatch it was — the prefix convention refused this node.
func TestCloseClosesADispatchWhoseGoalWasEdited(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)
	if resp := f.h.TaskEdit("cap", nodeID, "reworded entirely", "", nil); !resp.OK {
		t.Fatalf("task edit: %s", resp.Error)
	}

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "read the worker node; verified"})
	if !resp.OK {
		t.Fatalf("a goal edit must not un-dispatch a node: %s", resp.Error)
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

// An abandoned close is a successful close: the node is marked done, so what
// the delivery opened must not outlive it. The worker here is still active —
// the dispatch the cap gives up on — and the record names a worktree, so both
// the pane and the checkout have to die with the node.
func TestCloseAbandonedTearsDownTheDeliveredPaneAndWorktree(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "active")
	recordWorktreeDelivery(t, f, nodeID, "w7:p1", workerID, "w7", "/checkouts/w7")
	f.git.listed = []string{"/checkouts/w7"}

	herdr := &fakeHerdr{}
	resp := f.close(herdr, dispatch.CloseRequest{
		NodeID: nodeID, Abandoned: true, Reason: "the user withdrew the request",
	})
	if !resp.OK {
		t.Fatalf("Close --abandoned: %s", resp.Error)
	}
	if len(herdr.closed) != 1 || herdr.closed[0] != "w7:p1" {
		t.Errorf("closed = %v, want the delivered pane", herdr.closed)
	}
	if len(herdr.removed) != 1 || herdr.removed[0] != "w7" {
		t.Errorf("herdr removed = %v, want the delivered workspace", herdr.removed)
	}
	if len(f.git.removed) != 1 || f.git.removed[0] != "/checkouts/w7" {
		t.Errorf("git removed = %v, want the leaked checkout", f.git.removed)
	}
	result := resp.Data.(dispatch.CloseResult)
	if result.PaneID != "w7:p1" {
		t.Errorf("pane_id = %q, want the delivered pane named", result.PaneID)
	}
	for _, want := range []string{"pane w7:p1", "worktree w7"} {
		if !contains(result.TornDown, want) {
			t.Errorf("torn_down = %v, must name %q so the cleanup is visible", result.TornDown, want)
		}
	}
	if result.Warning != "" {
		t.Errorf("warning = %q, want none", result.Warning)
	}
	// The durable record has to agree with what was just done: a delivered
	// dispatch abandoned and torn down must not read "never delivered".
	want := "abandoned after delivery to skills:" + workerID +
		": the user withdrew the request (pane w7:p1 and worktree w7 torn down)"
	if decisions := capDecisions(t, f, nodeID); !contains(decisions, want) {
		t.Errorf("decisions = %v, want %q", decisions, want)
	}
	if result.Decision != want {
		t.Errorf("result.Decision = %q, want %q", result.Decision, want)
	}
	if status := nodeStatus(t, f, "cap", nodeID); status != "done" {
		t.Errorf("cap node status = %q, want done", status)
	}
}

// A refused close keeps everything, and the abandoned path has its own refusal:
// a missing --reason. The record names a live pane and a live worktree, and
// neither may be touched before the refusal — the distinction between "we are
// not gating this" and "we are not cleaning this".
func TestCloseAbandonedWithoutAReasonKeepsThePaneAndWorktree(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "active")
	recordWorktreeDelivery(t, f, nodeID, "w7:p1", workerID, "w7", "/checkouts/w7")
	f.git.listed = []string{"/checkouts/w7"}

	herdr := &fakeHerdr{}
	resp := f.close(herdr, dispatch.CloseRequest{NodeID: nodeID, Abandoned: true})
	if resp.OK {
		t.Fatal("--abandoned without --reason must be refused")
	}
	if len(herdr.closed) != 0 || len(herdr.removed) != 0 {
		t.Errorf("a refused close touched herdr: closed=%v removed=%v", herdr.closed, herdr.removed)
	}
	if len(f.git.removed) != 0 {
		t.Errorf("a refused close removed %v, want nothing", f.git.removed)
	}
	if status := nodeStatus(t, f, "cap", nodeID); status == "done" {
		t.Errorf("cap node status = %q, a refused close must leave it open", status)
	}
}

// Abandoning is a decision; a stuck pane is a cleanup problem, not a reason to
// keep the node open. The teardown that cannot finish warns instead of refusing,
// and what it did not finish is never claimed as torn down.
func TestCloseAbandonedWarnsButStillMarksDoneWhenTeardownFails(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "active")
	recordWorktreeDelivery(t, f, nodeID, "w7:p1", workerID, "w7", "/checkouts/w7")
	f.git.listed = []string{"/checkouts/w7"}
	f.git.keepListed = true

	resp := f.close(&fakeHerdr{closeErr: errors.New("herdr: server is down")}, dispatch.CloseRequest{
		NodeID: nodeID, Abandoned: true, Reason: "the user withdrew the request",
	})
	if !resp.OK {
		t.Fatalf("an abandoned dispatch must still be marked done: %s", resp.Error)
	}
	result := resp.Data.(dispatch.CloseResult)
	for _, want := range []string{"server is down", "w7:p1", "/checkouts/w7", "still registered"} {
		if !strings.Contains(result.Warning, want) {
			t.Errorf("warning = %q, must mention %q", result.Warning, want)
		}
	}
	if len(result.TornDown) != 0 {
		t.Errorf("torn_down = %v, want nothing: a failed teardown is a warning, not a claim", result.TornDown)
	}
	// The record says the delivery happened and that the teardown did not
	// finish, rather than claiming a clean-up or denying the delivery.
	if !strings.Contains(result.Decision, "abandoned after delivery to skills:"+workerID) {
		t.Errorf("decision = %q, must name the delivery", result.Decision)
	}
	if strings.Contains(result.Decision, "torn down") {
		t.Errorf("decision = %q, must not claim a teardown that failed", result.Decision)
	}
	if !strings.Contains(result.Decision, "teardown reported") {
		t.Errorf("decision = %q, must say the teardown did not finish", result.Decision)
	}
	if status := nodeStatus(t, f, "cap", nodeID); status != "done" {
		t.Errorf("cap node status = %q, want done despite the warning", status)
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

// A worktree record's checks must run in the worktree, not in the project root.
// The shipped parallel-cleanup checks assert things about the worktree — no
// uncommitted files, branch merged — and the main checkout is routinely another
// agent's dirty tree, so a gate that runs there fails for reasons the dispatch
// had nothing to do with. Here the root carries the file the worktree does not,
// and the worktree the file the root does not: only the worktree passes both.
//
// The record is the legacy shape — it names the workspace but no path, as every
// record written before the path was recorded does — so this also pins the
// fallback: the workspace is resolved for the tree to check in.
func TestCloseRunsCleanupChecksInTheWorktreeTheRecordNames(t *testing.T) {
	root := t.TempDir()
	worktree := t.TempDir()
	writeFile(t, filepath.Join(worktree, "clean.marker"), "")
	writeFile(t, filepath.Join(root, "uncommitted.marker"), "")

	f := newFixture(t, root)
	f.writePlaybook(t, "parallel-cleanup", `name: parallel-cleanup
type: cleanup
trigger: parallel
steps:
  - check: test -f clean.marker
  - check: test ! -f uncommitted.marker
`)

	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "done")
	recordWorktreeDelivery(t, f, nodeID, "w7:p1", workerID, "w7", "")

	herdr := &fakeHerdr{checkoutPath: worktree}
	resp := f.close(herdr, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if !resp.OK {
		t.Fatalf("Close: %s", resp.Error)
	}
	if len(herdr.checkouts) == 0 || herdr.checkouts[0] != "w7" {
		t.Errorf("worktrees resolved = %v, want the recorded w7", herdr.checkouts)
	}
	// The note names the tree the checks ran in: a gate whose footing is
	// invisible is how checking the wrong tree went unnoticed.
	note := resp.Data.(dispatch.CloseResult).Cleanup
	if !strings.Contains(note, "2 checks passed") || !strings.Contains(note, "in worktree w7") {
		t.Errorf("cleanup note = %q, want the checks and the tree they ran in", note)
	}
}

// The recorded path is enough to run the gate, so a workspace herdr has already
// forgotten does not cost the close its footing: the checks run in the tree the
// dispatch worked in, and herdr is never asked. Refusing instead would leave the
// checkout this work exists to remove.
func TestCloseRunsCleanupChecksInTheRecordedPathWithoutAskingHerdr(t *testing.T) {
	root := t.TempDir()
	worktree := t.TempDir()
	writeFile(t, filepath.Join(worktree, "clean.marker"), "")

	f := newFixture(t, root)
	f.writePlaybook(t, "parallel-cleanup", `name: parallel-cleanup
type: cleanup
trigger: parallel
steps:
  - check: test -f clean.marker
`)

	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "done")
	recordWorktreeDelivery(t, f, nodeID, "w7:p1", workerID, "w7", worktree)

	// herdr has forgotten the workspace entirely: neither the checks' footing nor
	// the teardown may depend on it.
	herdr := &fakeHerdr{
		checkoutErr: fmt.Errorf("%w: herdr has no workspace w7", dispatch.ErrNoWorktree),
		worktreeErr: dispatch.ErrWorktreeGone,
	}
	resp := f.close(herdr, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if !resp.OK {
		t.Fatalf("Close: %s", resp.Error)
	}
	if len(herdr.checkouts) != 0 {
		t.Errorf("herdr was asked to resolve %v, want the recorded path used", herdr.checkouts)
	}
	if note := resp.Data.(dispatch.CloseResult).Cleanup; !strings.Contains(note, "1 checks passed") {
		t.Errorf("cleanup note = %q, want the checks reported", note)
	}
}

// A check that genuinely fails in the worktree still refuses, naming the command
// and its output. The root is clean here, so a gate that fell back to it would
// pass — the failure has to come from the worktree's own state.
func TestCloseRefusesAWorktreeCleanupCheckThatFailsThere(t *testing.T) {
	root := t.TempDir()
	worktree := t.TempDir()
	writeFile(t, filepath.Join(root, "state.txt"), "clean\n")
	writeFile(t, filepath.Join(worktree, "state.txt"), "dirty\n")

	f := newFixture(t, root)
	f.writePlaybook(t, "parallel-cleanup", `name: parallel-cleanup
type: cleanup
trigger: parallel
steps:
  - check: grep -c clean state.txt
`)

	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "done")
	recordWorktreeDelivery(t, f, nodeID, "w7:p1", workerID, "w7", worktree)

	herdr := &fakeHerdr{checkoutPath: worktree}
	resp := f.close(herdr, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if resp.OK {
		t.Fatal("a check that fails in the worktree must refuse the close")
	}
	for _, want := range []string{"cleanup check failed", `"grep -c clean state.txt"`, "0", "fix it"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}
	if len(herdr.closed) != 0 || len(herdr.removed) != 0 {
		t.Errorf("a refused close touched herdr: closed=%v removed=%v", herdr.closed, herdr.removed)
	}
	if status := nodeStatus(t, f, "cap", nodeID); status == "done" {
		t.Errorf("cap node status = %q, a refused close must leave it open", status)
	}
}

// A record naming a worktree that cannot be resolved refuses, naming the
// workspace. It must not fall back to the project root: checking a tree the
// dispatch never touched, and reporting that the gate ran, is how the wrong-tree
// bug hid in the first place.
func TestCloseRefusesWhenTheRecordedWorktreeCannotBeResolved(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(root, "check-ran")
	f := newFixture(t, root)
	f.writePlaybook(t, "parallel-cleanup", fmt.Sprintf(`name: parallel-cleanup
type: cleanup
trigger: parallel
steps:
  - check: touch %s
`, sentinel))

	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "done")
	recordWorktreeDelivery(t, f, nodeID, "w7:p1", workerID, "wZZ", "")

	herdr := &fakeHerdr{checkoutErr: fmt.Errorf("%w: herdr has no workspace wZZ", dispatch.ErrNoWorktree)}
	resp := f.close(herdr, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if resp.OK {
		t.Fatal("an unresolvable worktree must refuse the close")
	}
	for _, want := range []string{"wZZ", "parallel-cleanup", "herdr has no workspace", "--abandoned"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}
	// The sentinel is the proof it did not check the root instead: had it fallen
	// back, this check would have run there.
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("a check ran in the project root despite the worktree being unresolvable: stat err = %v", err)
	}
	if len(herdr.closed) != 0 || len(herdr.removed) != 0 {
		t.Errorf("a refused close touched herdr: closed=%v removed=%v", herdr.closed, herdr.removed)
	}
	if status := nodeStatus(t, f, "cap", nodeID); status == "done" {
		t.Errorf("cap node status = %q, a refused close must leave it open", status)
	}
}

// An outstanding ask must not mask a failing check: the checks run first, so the
// refusal names what the user has to fix rather than asking them to confirm a
// gate that cannot pass, and only revealing the failure afterwards.
func TestCloseRunsCleanupChecksBeforeTheAsks(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-cleanup", `name: dev-task-cleanup
type: cleanup
trigger: dev-task
steps:
  - check: echo boom >&2; false
  - ask: was the merge reviewed before this closed
`)

	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if resp.OK {
		t.Fatal("a failing check must refuse the close even with an ask outstanding")
	}
	for _, want := range []string{"cleanup check failed", `"echo boom >&2; false"`, "boom"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}
	if strings.Contains(resp.Error, "unconfirmed steps") {
		t.Errorf("error %q reports the ask; the check has to be refused first", resp.Error)
	}
}

// A record that names no worktree keeps the footing it always had: the checks
// run in the worker project's root, and herdr is not consulted at all.
func TestCloseRunsCleanupChecksInTheProjectRootWithoutAWorktree(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "clean.marker"), "")

	f := newFixture(t, root)
	f.writePlaybook(t, "dev-task-cleanup", `name: dev-task-cleanup
type: cleanup
trigger: dev-task
steps:
  - check: test -f clean.marker
`)

	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)

	herdr := &fakeHerdr{}
	resp := f.close(herdr, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if !resp.OK {
		t.Fatalf("Close: %s", resp.Error)
	}
	if len(herdr.checkouts) != 0 {
		t.Errorf("worktrees resolved = %v for a record that names none", herdr.checkouts)
	}
	if note := resp.Data.(dispatch.CloseResult).Cleanup; !strings.Contains(note, "1 checks passed") {
		t.Errorf("cleanup note = %q, want the check reported", note)
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

// Close makes the worktree the record names gone, and it decides that with git,
// not with herdr. Here herdr reports the workspace removed and the checkout is
// still registered — the shape of the leak, where a workspace teardown that
// succeeded said nothing about the tree it was made from.
func TestCloseRemovesTheWorktreeHerdrReportsRemovedButGitStillLists(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "done")
	recordWorktreeDelivery(t, f, nodeID, "w7:p1", workerID, "w7", "/checkouts/w7")
	f.git.listed = []string{"/checkouts/w7"}

	herdr := &fakeHerdr{}
	resp := f.close(herdr, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if !resp.OK {
		t.Fatalf("Close: %s", resp.Error)
	}
	if len(herdr.removed) != 1 || herdr.removed[0] != "w7" {
		t.Errorf("herdr removed = %v, want the recorded workspace w7", herdr.removed)
	}
	if len(f.git.removed) != 1 || f.git.removed[0] != "/checkouts/w7" {
		t.Errorf("git removed = %v, want the checkout the workspace left behind", f.git.removed)
	}
	if len(f.git.listed) != 0 {
		t.Errorf("git still lists %v, want the worktree gone", f.git.listed)
	}
	if result := resp.Data.(dispatch.CloseResult); result.Warning != "" {
		t.Errorf("warning = %q, want none: the worktree is verified gone", result.Warning)
	}
}

// The workspace can be gone before close — its agent having ended — while the
// checkout it was made from survives. herdr says as much with
// workspace_not_found, and that is not evidence the worktree is gone: the close
// still has to remove it.
func TestCloseRemovesALeakedWorktreeWhenHerdrSaysTheWorkspaceIsAlreadyGone(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "done")
	recordWorktreeDelivery(t, f, nodeID, "w7:p1", workerID, "w7", "/checkouts/w7")
	f.git.listed = []string{"/checkouts/w7"}

	resp := f.close(&fakeHerdr{worktreeErr: dispatch.ErrWorktreeGone}, dispatch.CloseRequest{
		NodeID: nodeID, Decision: "verified",
	})
	if !resp.OK {
		t.Fatalf("Close: %s", resp.Error)
	}
	if len(f.git.removed) != 1 || f.git.removed[0] != "/checkouts/w7" {
		t.Errorf("git removed = %v, want the leaked checkout removed despite herdr saying it was gone", f.git.removed)
	}
	if result := resp.Data.(dispatch.CloseResult); result.Warning != "" {
		t.Errorf("warning = %q, want none: the worktree is verified gone", result.Warning)
	}
}

// A worktree that really is gone is the goal state, and git is what says so —
// the verified path decides it, not herdr's error code.
func TestCloseToleratesAnAlreadyRemovedWorktree(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "done")
	recordWorktreeDelivery(t, f, nodeID, "w7:p1", workerID, "w7", "/checkouts/w7")

	resp := f.close(&fakeHerdr{worktreeErr: dispatch.ErrWorktreeGone}, dispatch.CloseRequest{
		NodeID: nodeID, Decision: "verified",
	})
	if !resp.OK {
		t.Fatalf("an already-removed worktree must not fail the close: %s", resp.Error)
	}
	if len(f.git.removed) != 0 {
		t.Errorf("git removed %v, want nothing: no worktree was registered", f.git.removed)
	}
	if result := resp.Data.(dispatch.CloseResult); result.Warning != "" {
		t.Errorf("warning = %q, want none for an already-removed worktree", result.Warning)
	}
	if status := nodeStatus(t, f, "cap", nodeID); status != "done" {
		t.Errorf("cap node status = %q, want done", status)
	}
}

// herdr being unable to remove the workspace is a warning, not a refusal —
// close-out must not depend on the dispatcher being up — and the worktree is
// still verified: here git reports none registered, so no leak is claimed.
func TestCloseWarnsButSucceedsWhenHerdrCannotRemoveTheWorkspace(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "done")
	recordWorktreeDelivery(t, f, nodeID, "w7:p1", workerID, "w7", "/checkouts/w7")

	resp := f.close(&fakeHerdr{worktreeErr: errors.New("herdr: server is down")}, dispatch.CloseRequest{
		NodeID: nodeID, Decision: "verified",
	})
	if !resp.OK {
		t.Fatalf("close-out must not depend on the dispatcher being up: %s", resp.Error)
	}
	result := resp.Data.(dispatch.CloseResult)
	if !strings.Contains(result.Warning, "server is down") || !strings.Contains(result.Warning, "w7") {
		t.Errorf("warning = %q, want the workspace and the reason", result.Warning)
	}
}

// A removal that reports success and leaves the worktree registered is exactly
// the failure this gate exists to catch, so the gate re-checks instead of
// believing the exit code: the warning names the path that is still there.
func TestCloseWarnsWhenTheWorktreeIsStillRegisteredAfterRemoval(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "done")
	recordWorktreeDelivery(t, f, nodeID, "w7:p1", workerID, "w7", "/checkouts/w7")
	f.git.listed = []string{"/checkouts/w7"}
	f.git.keepListed = true

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if !resp.OK {
		t.Fatalf("a leaked worktree is a warning, not a refusal: %s", resp.Error)
	}
	result := resp.Data.(dispatch.CloseResult)
	if !strings.Contains(result.Warning, "/checkouts/w7") || !strings.Contains(result.Warning, "still registered") {
		t.Errorf("warning = %q, want the path that is still there", result.Warning)
	}
	if status := nodeStatus(t, f, "cap", nodeID); status != "done" {
		t.Errorf("cap node status = %q, want done: the warning is what the cap acts on", status)
	}
}

// A worktree git will not remove — a dirty checkout, a lock — is a warning
// naming the path, never silence and never a claimed success.
func TestCloseWarnsWhenGitCannotRemoveTheWorktree(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "done")
	recordWorktreeDelivery(t, f, nodeID, "w7:p1", workerID, "w7", "/checkouts/w7")
	f.git.listed = []string{"/checkouts/w7"}
	f.git.removeErr = errors.New("contains modified or untracked files")

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if !resp.OK {
		t.Fatalf("a worktree that cannot be removed is a warning, not a refusal: %s", resp.Error)
	}
	result := resp.Data.(dispatch.CloseResult)
	for _, want := range []string{"/checkouts/w7", "could not remove", "modified or untracked"} {
		if !strings.Contains(result.Warning, want) {
			t.Errorf("warning = %q, must mention %q", result.Warning, want)
		}
	}
}

// When git cannot be asked, the close says so instead of reporting the worktree
// gone: an unverifiable worktree is exactly what leaked silently before.
func TestCloseWarnsWhenTheWorktreeCannotBeVerified(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "done")
	recordWorktreeDelivery(t, f, nodeID, "w7:p1", workerID, "w7", "/checkouts/w7")
	f.git.listErr = errors.New("fatal: not a git repository")

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if !resp.OK {
		t.Fatalf("an unverifiable worktree is a warning, not a refusal: %s", resp.Error)
	}
	result := resp.Data.(dispatch.CloseResult)
	for _, want := range []string{"/checkouts/w7", "could not verify", "not a git repository"} {
		if !strings.Contains(result.Warning, want) {
			t.Errorf("warning = %q, must mention %q", result.Warning, want)
		}
	}
}

// A record written before the path was recorded names only a workspace, and it
// is still verified when herdr can resolve one. When herdr cannot, the warning
// names the workspace: there is no path to check, and that is not success.
func TestCloseWarnsWhenTheRecordNamesNoPathAndHerdrCannotResolveIt(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch parallel: sample")
	workerID := addWorkerNode(t, f, "done")
	recordWorktreeDelivery(t, f, nodeID, "w7:p1", workerID, "w7", "")

	resp := f.close(&fakeHerdr{checkoutErr: fmt.Errorf("%w: herdr has no workspace w7", dispatch.ErrNoWorktree)}, dispatch.CloseRequest{
		NodeID: nodeID, Decision: "verified",
	})
	if !resp.OK {
		t.Fatalf("Close: %s", resp.Error)
	}
	result := resp.Data.(dispatch.CloseResult)
	for _, want := range []string{"w7", "could not verify"} {
		if !strings.Contains(result.Warning, want) {
			t.Errorf("warning = %q, must mention %q", result.Warning, want)
		}
	}
}

// A dispatch that ran in the project root has no worktree to verify, and the
// teardown must not invent one: git is never asked.
func TestCloseWithoutAWorktreeNeverAsksGit(t *testing.T) {
	f := newFixture(t, t.TempDir())
	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)
	f.git.listErr = errors.New("must not be asked")

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if !resp.OK {
		t.Fatalf("Close: %s", resp.Error)
	}
	if f.git.lists != 0 {
		t.Errorf("git was asked about worktrees %d time(s) for a dispatch that had none", f.git.lists)
	}
	if result := resp.Data.(dispatch.CloseResult); result.Warning != "" {
		t.Errorf("warning = %q, want none", result.Warning)
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

// Exit checks see the closing dispatch's own inputs as FS_*, the same treatment
// entry checks get, so an exit gate can ask about the dispatch it is gating
// instead of a proxy for it.
//
// printenv rather than echo is deliberate: it fails on an absent variable, so a
// step passing proves the variable is set — and FS_CARDS, which a close has no
// value for, must read as set-and-empty rather than missing.
func TestCloseGivesCleanupChecksTheClosingDispatchEnv(t *testing.T) {
	f := newFixture(t, t.TempDir())

	nodeID := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordDelivery(t, f, nodeID, "w1:p9", workerID)

	f.writePlaybook(t, "dev-task-cleanup", fmt.Sprintf(`name: dev-task-cleanup
type: cleanup
trigger: dev-task
steps:
  - check: test "$FS_PROJECT" = skills
  - check: test "$FS_TYPE" = dev-task
  - check: test "$FS_NODE" = %s
  - check: test "$FS_WORKER" = skills:%s
  - check: test -z "$FS_CARDS" && printenv FS_CARDS >/dev/null
  - check: test -z "$FS_INTEGRATES" && printenv FS_INTEGRATES >/dev/null
`, nodeID, workerID))

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if !resp.OK {
		t.Fatalf("Close: %s", resp.Error)
	}
	if note := resp.Data.(dispatch.CloseResult).Cleanup; !strings.Contains(note, "6 checks passed") {
		t.Errorf("cleanup note = %q, want every env check reported", note)
	}
}

// An integration's exit gate sees the member the integration names, so a gate
// can be about this integration's own link.
func TestCloseGivesCleanupChecksTheIntegratesLink(t *testing.T) {
	f := newFixture(t, t.TempDir())

	member := addCapKindNode(t, f, "dispatch parallel: member", query.KindDispatch)
	nodeID := addIntegrationNode(t, f, member)
	workerID := addWorkerNode(t, f, "done")
	recordTypedDelivery(t, f, nodeID, "w1:p9", workerID, "integrate")

	f.writePlaybook(t, "integrate-cleanup", fmt.Sprintf(`name: integrate-cleanup
type: cleanup
trigger: integrate
steps:
  - check: test "$FS_NODE" = %s
  - check: test "$FS_INTEGRATES" = %s
`, nodeID, member))

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: nodeID, Decision: "verified"})
	if !resp.OK {
		t.Fatalf("Close: %s", resp.Error)
	}
	if note := resp.Data.(dispatch.CloseResult).Cleanup; !strings.Contains(note, "2 checks passed") {
		t.Errorf("cleanup note = %q, want the link check reported", note)
	}
}

// A member's integration closes per member: closing integration 1 leaves member
// 2's tree, branch and open node exactly as they were. The batch-global checks
// this gate used to carry made that impossible — they asked about the whole
// batch, so no single member's integration could pass while another was
// unfinished.
func TestCloseOfOneMembersIntegrationIsUnaffectedByTheOtherMember(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "gate.marker"), "")

	f := newFixture(t, root)
	f.writePlaybook(t, "integrate-cleanup", `name: integrate-cleanup
type: cleanup
trigger: integrate
steps:
  - check: test -f gate.marker
  - ask: was the integration verdict recorded and read
`)

	first := addCapKindNode(t, f, "dispatch parallel: member one", query.KindDispatch)
	second := addCapKindNode(t, f, "dispatch parallel: member two", query.KindDispatch)
	integration := addIntegrationNode(t, f, first)
	workerID := addWorkerNode(t, f, "done")
	recordTypedDelivery(t, f, integration, "w1:p9", workerID, "integrate")

	// Member two is still open: the whole point is that its state cannot reach
	// member one's integration gate.
	if status := nodeStatus(t, f, "cap", second); status == "done" {
		t.Fatalf("member two must still be open for this test to mean anything, got %q", status)
	}

	resp := f.close(&fakeHerdr{}, dispatch.CloseRequest{NodeID: integration, Decision: "merged one", Confirm: true})
	if !resp.OK {
		t.Fatalf("closing one member's integration must not depend on the other member: %s", resp.Error)
	}
	if status := nodeStatus(t, f, "cap", second); status == "done" {
		t.Errorf("closing an integration for member one closed member two (status %q)", status)
	}
}
