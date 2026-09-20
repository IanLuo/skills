package command_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/store"
)

func tempDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "store.db")
}

func setup(t *testing.T) *command.Handler {
	t.Helper()
	h, err := command.NewHandler(tempDB(t))
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

func strPtr(s string) *string { return &s }

// setupWithStore returns a handler plus a second store handle on the same
// database, so a test can append an event straight through the store —
// bypassing the write-time validation TaskAdd applies.
func setupWithStore(t *testing.T) (*command.Handler, *store.Store) {
	t.Helper()
	dbPath := tempDB(t)
	h, err := command.NewHandler(dbPath)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	t.Cleanup(func() { h.Close() })

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return h, s
}

func TestResponseEnvelope(t *testing.T) {
	h := setup(t)
	resp := h.ProjectCreate("test-project", "/tmp/test")
	if !resp.OK {
		t.Fatalf("expected OK: %s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("data is nil")
	}

	// Should be valid JSON
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed["ok"] != true {
		t.Error("ok field not true in JSON")
	}
}

func TestProjectCreate(t *testing.T) {
	h := setup(t)
	resp := h.ProjectCreate("myproject", "/tmp/proj")
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}

	data, ok := resp.Data.(command.EventData)
	if !ok {
		t.Fatalf("data type: %T", resp.Data)
	}
	if data.EventID == "" {
		t.Error("empty event_id")
	}
	if data.EventType != store.ProjectCreated {
		t.Errorf("type: %s", data.EventType)
	}
}

func TestProjectCreateRequiresName(t *testing.T) {
	h := setup(t)
	resp := h.ProjectCreate("", "/tmp/proj")
	if resp.OK {
		t.Fatal("should fail with empty name")
	}
	if resp.Error == "" {
		t.Error("error message is empty")
	}
}

func TestProjectCreateIdempotentDerivedState(t *testing.T) {
	// G2: Calling project create twice with same name appends two events
	// (append-only), but derived state is the same project.
	h := setup(t)

	resp1 := h.ProjectCreate("myproject", "/tmp/proj")
	if !resp1.OK {
		t.Fatalf("first create failed: %s", resp1.Error)
	}
	resp2 := h.ProjectCreate("myproject", "/tmp/proj")
	if !resp2.OK {
		t.Fatalf("second create failed: %s", resp2.Error)
	}

	// Two distinct events appended (append-only invariant).
	data1 := resp1.Data.(command.EventData)
	data2 := resp2.Data.(command.EventData)
	if data1.EventID == data2.EventID {
		t.Error("expected different event IDs for two appends")
	}

	// Log shows exactly 2 events.
	logResp := h.Log("myproject", nil, nil)
	logData := logResp.Data.(command.LogResult)
	if len(logData.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(logData.Events))
	}

	// Derived state: status shows same project (0 tasks both times).
	statusResp := h.Status("myproject")
	statusData := statusResp.Data.(command.StatusResult)
	if statusData.ProjectID != "myproject" {
		t.Errorf("project_id: %s", statusData.ProjectID)
	}
	if len(statusData.Tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(statusData.Tasks))
	}
}

func TestTaskAdd(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	resp := h.TaskAdd("proj", "implement auth", nil)
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	data := resp.Data.(command.EventData)
	if data.NodeID == "" {
		t.Error("empty node_id")
	}
}

func TestTaskAddWithParent(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	parent := h.TaskAdd("proj", "parent task", nil)
	parentData := parent.Data.(command.EventData)

	resp := h.TaskAdd("proj", "subtask", &parentData.NodeID)
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
}

func TestTaskAddUnknownParentRefused(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	before := len(h.Log("proj", nil, nil).Data.(command.LogResult).Events)

	resp := h.TaskAdd("proj", "subtask", strPtr("t-nonexistent"))
	if resp.OK {
		t.Fatal("a parent that does not exist must be refused")
	}
	if !strings.Contains(resp.Error, "t-nonexistent") || !strings.Contains(resp.Error, "proj") {
		t.Errorf("error must name the parent and the project: %s", resp.Error)
	}

	after := len(h.Log("proj", nil, nil).Data.(command.LogResult).Events)
	if after != before {
		t.Fatalf("a refused add must write no event: %d -> %d", before, after)
	}
}

// Every writer that names a node must refuse one that does not exist in the
// resolved project, before appending anything: a status or decision for a node
// that was never created is a fabrication, not a record. The event count is the
// assertion — a refusal that still wrote an event is the bug this guards.
func TestWritersRefuseAnUnknownNode(t *testing.T) {
	writes := []struct {
		name  string
		write func(h *command.Handler) command.Response
	}{
		{"task update", func(h *command.Handler) command.Response {
			return h.TaskUpdate("proj", "t-doesnotexist", "done", "probe", nil)
		}},
		{"task edit", func(h *command.Handler) command.Response {
			return h.TaskEdit("proj", "t-doesnotexist", "reworded", "", nil)
		}},
		{"task block", func(h *command.Handler) command.Response {
			return h.TaskBlock("proj", "t-doesnotexist", "waiting")
		}},
		{"task unblock", func(h *command.Handler) command.Response {
			return h.TaskUnblock("proj", "t-doesnotexist")
		}},
		{"task knowledge", func(h *command.Handler) command.Response {
			return h.KnowledgeAdd("proj", "t-doesnotexist", "learned")
		}},
		{"record delivery", func(h *command.Handler) command.Response {
			return h.RecordDelivery("proj", "t-doesnotexist", command.DeliveryRecord{
				PaneID: "w1:p1", Agent: "a", Engine: "herdr", Project: "proj", Node: "t-x",
			})
		}},
	}

	for _, tc := range writes {
		t.Run(tc.name, func(t *testing.T) {
			h := setup(t)
			h.ProjectCreate("proj", "/tmp/proj")

			before := len(h.Log("proj", nil, nil).Data.(command.LogResult).Events)
			resp := tc.write(h)
			if resp.OK {
				t.Fatal("a write to a node that does not exist must be refused")
			}
			if !strings.Contains(resp.Error, "t-doesnotexist") || !strings.Contains(resp.Error, "proj") {
				t.Errorf("error must name the node and the project: %s", resp.Error)
			}
			after := len(h.Log("proj", nil, nil).Data.(command.LogResult).Events)
			if after != before {
				t.Fatalf("a refused write must write no event: %d -> %d", before, after)
			}
		})
	}
}

// A conjured node — events with no task-created event, the damage the old path
// left in a live store — must not accept more writes. It is visible in fs
// status, because derived state materializes a node from any event that names
// it; an existence check against derived state would pass it. This is the case
// acceptance names: `fs task update t-doesnotexist --project skills`.
func TestWritersRefuseAConjuredNode(t *testing.T) {
	h, s := setupWithStore(t)
	h.ProjectCreate("proj", "/tmp/proj")

	conjured := "t-doesnotexist"
	payload, _ := json.Marshal(map[string]string{"summary": "written to a node that was never created"})
	if _, err := s.Append(store.Event{
		Type:      store.DecisionRecorded,
		ProjectID: "proj",
		NodeID:    &conjured,
		Payload:   payload,
	}); err != nil {
		t.Fatalf("append conjured: %v", err)
	}

	// Precondition: the conjured node is visible in derived state, which is
	// exactly why tree membership cannot be the test for existence.
	if got := len(h.Status("proj").Data.(command.StatusResult).Tasks); got != 1 {
		t.Fatalf("precondition: conjured node must be visible in status, got %d tasks", got)
	}

	before := len(h.Log("proj", nil, nil).Data.(command.LogResult).Events)
	resp := h.TaskUpdate("proj", conjured, "done", "probe", nil)
	if resp.OK {
		t.Fatal("a write to a node with no task-created event must be refused")
	}
	if !strings.Contains(resp.Error, conjured) || !strings.Contains(resp.Error, "proj") {
		t.Errorf("error must name the node and the project: %s", resp.Error)
	}
	if after := len(h.Log("proj", nil, nil).Data.(command.LogResult).Events); after != before {
		t.Fatalf("a refused write must write no event: %d -> %d", before, after)
	}
}

// The parent a write names is a node the write references, so it must exist the
// same way: a conjured node is not an acceptable parent.
func TestTaskAddRefusesAConjuredParent(t *testing.T) {
	h, s := setupWithStore(t)
	h.ProjectCreate("proj", "/tmp/proj")

	conjured := "t-doesnotexist"
	payload, _ := json.Marshal(map[string]string{"summary": "conjured"})
	if _, err := s.Append(store.Event{
		Type:      store.DecisionRecorded,
		ProjectID: "proj",
		NodeID:    &conjured,
		Payload:   payload,
	}); err != nil {
		t.Fatalf("append conjured: %v", err)
	}

	before := len(h.Log("proj", nil, nil).Data.(command.LogResult).Events)
	resp := h.TaskAdd("proj", "child of a node that was never created", &conjured)
	if resp.OK {
		t.Fatal("a parent with no task-created event must be refused")
	}
	if !strings.Contains(resp.Error, conjured) || !strings.Contains(resp.Error, "proj") {
		t.Errorf("error must name the parent and the project: %s", resp.Error)
	}
	if after := len(h.Log("proj", nil, nil).Data.(command.LogResult).Events); after != before {
		t.Fatalf("a refused add must write no event: %d -> %d", before, after)
	}
}

// "<project>:<node>" is the form our briefs and fs close --worker use. When the
// qualifier names the resolved project the write lands on the bare node — never
// on a node literally named "proj:t-...".
func TestWritersResolveAQualifiedNodeInItsOwnProject(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	nodeID := h.TaskAdd("proj", "do something", nil).Data.(command.EventData).NodeID

	resp := h.TaskUpdate("proj", "proj:"+nodeID, "done", "", nil)
	if !resp.OK {
		t.Fatalf("a qualified id naming the resolved project must be accepted: %s", resp.Error)
	}

	// The write is on the bare node.
	if events := h.Log("proj", strPtr(nodeID), nil).Data.(command.LogResult).Events; len(events) != 2 {
		t.Errorf("expected task-created + status-changed on %s, got %d events", nodeID, len(events))
	}
	// And no node named literally "proj:<node>" was conjured.
	if events := h.Log("proj", strPtr("proj:"+nodeID), nil).Data.(command.LogResult).Events; len(events) != 0 {
		t.Errorf("a node named %q was created", "proj:"+nodeID)
	}
}

// A qualifier that names a different project than the one the write targets is
// refused, naming both: an accidental cross-scope write is the failure this
// check exists to remove.
func TestWritersRefuseAQualifiedNodeInAnotherProject(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	h.ProjectCreate("other", "/tmp/other")
	otherNode := h.TaskAdd("other", "other work", nil).Data.(command.EventData).NodeID

	before := len(h.Log("other", nil, nil).Data.(command.LogResult).Events)
	resp := h.TaskUpdate("proj", "other:"+otherNode, "done", "", nil)
	if resp.OK {
		t.Fatal("a qualified id naming another project must be refused")
	}
	if !strings.Contains(resp.Error, "other") || !strings.Contains(resp.Error, "proj") {
		t.Errorf("error must name both projects: %s", resp.Error)
	}

	after := len(h.Log("other", nil, nil).Data.(command.LogResult).Events)
	if after != before {
		t.Fatalf("the other project's node must be untouched: %d -> %d", before, after)
	}
}

func TestTaskAddRequiresGoal(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	resp := h.TaskAdd("proj", "", nil)
	if resp.OK {
		t.Fatal("should fail with empty goal")
	}
}

func TestTaskUpdateStatus(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "do something", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID

	resp := h.TaskUpdate("proj", nodeID, "active", "", nil)
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	// Should produce a status-changed event
	data := resp.Data.(command.UpdateResult)
	found := false
	for _, e := range data.Events {
		if e.EventType == store.StatusChanged {
			found = true
		}
	}
	if !found {
		t.Error("no status-changed event")
	}
}

func TestTaskUpdateStatusAndDecision(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "do something", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID

	resp := h.TaskUpdate("proj", nodeID, "done", "chose JWT", nil)
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	data := resp.Data.(command.UpdateResult)
	if len(data.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(data.Events))
	}
}

func TestTaskUpdateInvalidStatus(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "do something", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID

	resp := h.TaskUpdate("proj", nodeID, "invalid-status", "", nil)
	if resp.OK {
		t.Fatal("should fail with invalid status")
	}
}

func TestTaskEdit(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "original goal", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID

	resp := h.TaskEdit("proj", nodeID, "revised goal", "", nil)
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	data := resp.Data.(command.UpdateResult)
	if len(data.Events) != 1 || data.Events[0].EventType != store.MetadataChanged {
		t.Errorf("events: %+v", data.Events)
	}
}

// A node with no kind is the default, so an ordinary task add is unchanged.
func TestTaskAddDefaultsToWork(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	h.TaskAdd("proj", "ordinary work", nil)

	task := onlyTask(t, h, "proj")
	if task.Kind != query.KindWork {
		t.Errorf("kind = %q, want %q", task.Kind, query.KindWork)
	}
}

// A gap carries its kind and the node that found it, so the backlog is
// structural rather than a prefix on the goal.
func TestTaskAddKindWithFoundBy(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	resp := h.TaskAddKind("proj", "the teardown is untested", query.KindGap, "skills:t-found", nil)
	if !resp.OK {
		t.Fatalf("TaskAddKind: %s", resp.Error)
	}

	task := onlyTask(t, h, "proj")
	if task.Kind != query.KindGap {
		t.Errorf("kind = %q, want %q", task.Kind, query.KindGap)
	}
	if task.FoundBy != "skills:t-found" {
		t.Errorf("found_by = %q, want skills:t-found", task.FoundBy)
	}
}

func TestTaskAddRefusesAnUnknownKind(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	resp := h.TaskAddKind("proj", "something", query.Kind("gapp"), "", nil)
	if resp.OK {
		t.Fatal("an unknown kind must be refused at write time, not stored")
	}
	if !strings.Contains(resp.Error, "gapp") {
		t.Errorf("error %q must name the rejected kind", resp.Error)
	}
}

func TestTaskAddRefusesAMalformedFoundBy(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	for _, ref := range []string{"skills", "skills:", ":t-1", "skills:t-1:extra"} {
		resp := h.TaskAddKind("proj", "something", query.KindGap, ref, nil)
		if resp.OK {
			t.Errorf("found_by %q must be refused", ref)
			continue
		}
		if !strings.Contains(resp.Error, "<project>:<node>") {
			t.Errorf("error %q must show the wanted shape", resp.Error)
		}
	}
}

// The kind setter: editing a node's kind moves it between the groups without
// touching its goal — how the backlog was migrated.
func TestTaskEditKind(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "backlog: something noticed", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID

	resp := h.TaskEdit("proj", nodeID, "", query.KindGap, nil)
	if !resp.OK {
		t.Fatalf("TaskEdit: %s", resp.Error)
	}

	task := onlyTask(t, h, "proj")
	if task.Kind != query.KindGap {
		t.Errorf("kind = %q, want %q", task.Kind, query.KindGap)
	}
	if task.Goal != "backlog: something noticed" {
		t.Errorf("goal = %q, want it left alone", task.Goal)
	}
}

func TestTaskEditRefusesAnUnknownKind(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	nodeID := h.TaskAdd("proj", "a task", nil).Data.(command.EventData).NodeID

	resp := h.TaskEdit("proj", nodeID, "", query.Kind("gapp"), nil)
	if resp.OK {
		t.Fatal("an unknown kind must be refused")
	}
}

// Unfinished reports one group per kind, each present even when empty.
func TestUnfinishedGroupsByKind(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	h.TaskAdd("proj", "ordinary work", nil)
	h.TaskAddKind("proj", "a dispatch", query.KindDispatch, "", nil)
	h.TaskAddKind("proj", "a gap", query.KindGap, "skills:t-found", nil)

	resp := h.Unfinished()
	if !resp.OK {
		t.Fatalf("Unfinished: %s", resp.Error)
	}
	groups := resp.Data.(map[string]any)["unfinished"].(map[string][]command.UnfinishedNode)

	for kind, wantGoal := range map[query.Kind]string{
		query.KindWork:     "ordinary work",
		query.KindDispatch: "a dispatch",
		query.KindGap:      "a gap",
	} {
		got := groups[string(kind)]
		if len(got) != 1 {
			t.Fatalf("group %q = %+v, want one node", kind, got)
		}
		if got[0].Goal != wantGoal || got[0].Kind != kind {
			t.Errorf("group %q node = %+v, want goal %q", kind, got[0], wantGoal)
		}
	}
}

// fs gaps lists kind=gap nodes across scopes, open ones first, with found_by.
func TestGapsListsOpenFirstAcrossScopes(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	doneGap := h.TaskAddKind("proj", "a gap already resolved", query.KindGap, "", nil).Data.(command.EventData).NodeID
	if resp := h.TaskUpdate("proj", doneGap, "done", "fixed", nil); !resp.OK {
		t.Fatalf("TaskUpdate: %s", resp.Error)
	}
	h.TaskAddKind("proj", "an open gap", query.KindGap, "skills:t-found", nil)
	h.TaskAddKind("other", "a gap in another scope", query.KindGap, "", nil)
	h.TaskAdd("proj", "ordinary work", nil)

	resp := h.Gaps()
	if !resp.OK {
		t.Fatalf("Gaps: %s", resp.Error)
	}
	gaps := resp.Data.(map[string]any)["gaps"].([]command.GapNode)
	if len(gaps) != 3 {
		t.Fatalf("gaps = %+v, want the three gap nodes and no work node", gaps)
	}
	if gaps[len(gaps)-1].Status != "done" {
		t.Errorf("gaps = %+v, want the done gap last", gaps)
	}
	open := gaps[:len(gaps)-1]
	found := false
	for _, gap := range open {
		if gap.Status != "pending" {
			t.Errorf("open gap = %+v, want pending", gap)
		}
		if gap.FoundBy == "skills:t-found" {
			found = true
		}
	}
	if !found {
		t.Errorf("gaps = %+v, want the open gap carrying found_by", gaps)
	}
}

// onlyTask returns the single task fs status derives for a scope.
func onlyTask(t *testing.T, h *command.Handler, project string) command.TaskInfo {
	t.Helper()
	resp := h.Status(project)
	if !resp.OK {
		t.Fatalf("Status: %s", resp.Error)
	}
	tasks := resp.Data.(command.StatusResult).Tasks
	if len(tasks) != 1 {
		t.Fatalf("status has %d tasks, want 1: %+v", len(tasks), tasks)
	}
	return tasks[0]
}

func TestStatus(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	h.TaskAdd("proj", "task one", nil)
	h.TaskAdd("proj", "task two", nil)

	resp := h.Status("proj")
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	data := resp.Data.(command.StatusResult)
	if data.ProjectID != "proj" {
		t.Errorf("project_id: %s", data.ProjectID)
	}
	if len(data.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(data.Tasks))
	}
	// Both should be pending by default
	for _, task := range data.Tasks {
		if task.Status != "pending" {
			t.Errorf("task %s status: %s", task.NodeID, task.Status)
		}
	}
}

func TestStatusReflectsUpdates(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "my task", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID

	h.TaskUpdate("proj", nodeID, "active", "", nil)
	h.TaskUpdate("proj", nodeID, "done", "", nil)

	resp := h.Status("proj")
	data := resp.Data.(command.StatusResult)
	if data.Tasks[0].Status != "done" {
		t.Errorf("expected done, got %s", data.Tasks[0].Status)
	}
}

func TestStatusReflectsEdit(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "original", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID

	h.TaskEdit("proj", nodeID, "revised", "", nil)

	resp := h.Status("proj")
	data := resp.Data.(command.StatusResult)
	if data.Tasks[0].Goal != "revised" {
		t.Errorf("expected 'revised', got %s", data.Tasks[0].Goal)
	}
}

func TestStatusDecisions(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "task", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID

	h.TaskUpdate("proj", nodeID, "done", "chose JWT", nil)

	resp := h.Status("proj")
	data := resp.Data.(command.StatusResult)
	if len(data.Tasks[0].Decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(data.Tasks[0].Decisions))
	}
	if data.Tasks[0].Decisions[0] != "chose JWT" {
		t.Errorf("decision: %s", data.Tasks[0].Decisions[0])
	}
}

// An orphan — a task-created whose parent never existed — is unreachable from
// the roots, so fs status must surface it explicitly, marked.
func TestStatusSurfacesOrphan(t *testing.T) {
	h, s := setupWithStore(t)
	h.ProjectCreate("proj", "/tmp/proj")
	h.TaskAdd("proj", "root task", nil)

	// Straight through the store, as the log allows: a task naming a parent
	// that is not in the project. Pre-existing orphans must become visible.
	orphanID := "t-orphan"
	payload, _ := json.Marshal(map[string]string{"goal": "orphaned task"})
	if _, err := s.Append(store.Event{
		Type:         store.TaskCreated,
		ProjectID:    "proj",
		NodeID:       &orphanID,
		ParentNodeID: strPtr("t-nonexistent"),
		Payload:      payload,
	}); err != nil {
		t.Fatalf("append orphan: %v", err)
	}

	resp := h.Status("proj")
	if !resp.OK {
		t.Fatalf("Status: %s", resp.Error)
	}
	tasks := resp.Data.(command.StatusResult).Tasks
	if len(tasks) != 2 {
		t.Fatalf("status has %d tasks, want root + orphan: %+v", len(tasks), tasks)
	}
	var orphan *command.TaskInfo
	for i := range tasks {
		if tasks[i].NodeID == orphanID {
			orphan = &tasks[i]
		}
	}
	if orphan == nil {
		t.Fatalf("orphan missing from status: %+v", tasks)
	}
	if !orphan.Orphan {
		t.Errorf("orphan not marked: %+v", orphan)
	}
	if orphan.ParentNodeID != "t-nonexistent" {
		t.Errorf("orphan parent_node_id: %s", orphan.ParentNodeID)
	}

	// fs status and fs unfinished must agree about which nodes exist.
	unfinished, err := h.UnfinishedIn("proj")
	if err != nil {
		t.Fatalf("UnfinishedIn: %v", err)
	}
	unfinishedIDs := map[string]bool{}
	for _, node := range unfinished {
		unfinishedIDs[node.NodeID] = true
	}
	for i := range tasks {
		if !unfinishedIDs[tasks[i].NodeID] {
			t.Errorf("node %s is in status but not unfinished: %+v", tasks[i].NodeID, unfinished)
		}
	}
	if len(unfinished) != len(tasks) {
		t.Fatalf("status has %d nodes, unfinished has %d: %+v", len(tasks), len(unfinished), unfinished)
	}
}

// Status carries the knowledge recorded on a node, and a node with none carries
// no knowledge key at all rather than an empty list.
func TestStatusCarriesKnowledge(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	withKnowledge := h.TaskAdd("proj", "task with knowledge", nil).Data.(command.EventData).NodeID
	without := h.TaskAdd("proj", "task without knowledge", nil).Data.(command.EventData).NodeID
	h.KnowledgeAdd("proj", withKnowledge, "JWT refresh tokens expire after 7 days")

	resp := h.Status("proj")
	if !resp.OK {
		t.Fatalf("Status: %s", resp.Error)
	}
	byID := map[string]command.TaskInfo{}
	for _, task := range resp.Data.(command.StatusResult).Tasks {
		byID[task.NodeID] = task
	}
	if got := byID[withKnowledge].Knowledge; len(got) != 1 || got[0] != "JWT refresh tokens expire after 7 days" {
		t.Errorf("knowledge = %v, want the recorded summary", got)
	}

	b, err := json.Marshal(byID[without])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), `"knowledge"`) {
		t.Errorf("a node with no knowledge must omit the key: %s", b)
	}
}

func TestCommitSHACapture(t *testing.T) {
	// The command layer should accept a commit SHA and pass it through.
	h := setup(t)
	sha := "abc123"
	h.SetCommitSHA(&sha)
	resp := h.ProjectCreate("proj", "/tmp/proj")
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	data := resp.Data.(command.EventData)
	if data.CommitSHA == nil || *data.CommitSHA != sha {
		t.Errorf("commit_sha not captured: %v", data.CommitSHA)
	}
}

func TestQuery(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	h.TaskAdd("proj", "implement authentication with JWT", nil)
	h.TaskAdd("proj", "fix database migration", nil)

	resp := h.Query("proj", "auth")
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	data := resp.Data.(command.QueryResult)
	if len(data.Events) < 1 {
		t.Fatal("expected at least 1 match for 'auth'")
	}
}

func TestQueryRequiresProjectID(t *testing.T) {
	h := setup(t)
	resp := h.Query("", "auth")
	if resp.OK {
		t.Fatal("should fail with empty project_id")
	}
}

func TestQueryRequiresSearchTerm(t *testing.T) {
	h := setup(t)
	resp := h.Query("proj", "")
	if resp.OK {
		t.Fatal("should fail with empty search term")
	}
}

func TestQuery50Events(t *testing.T) {
	// AC5: 50+ events, query returns accurate results.
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	// Seed 50+ events across nodes.
	for i := 0; i < 25; i++ {
		if i%5 == 0 {
			h.TaskAdd("proj", fmt.Sprintf("authentication task %d with JWT tokens", i), nil)
		} else {
			h.TaskAdd("proj", fmt.Sprintf("database migration task %d", i), nil)
		}
	}
	// Add decisions to hit 50+.
	for i := 0; i < 26; i++ {
		nodeID := fmt.Sprintf("extra-node-%d", i)
		payload := fmt.Sprintf(`{"summary":"decision %d about caching"}`, i)
		h.TaskAdd("proj", fmt.Sprintf("extra task %d", i), nil)
		_ = nodeID
		_ = payload
	}

	resp := h.Query("proj", "auth")
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	data := resp.Data.(command.QueryResult)
	// Should find the auth-related tasks (5 of them), not the db migration ones.
	if len(data.Events) < 5 {
		t.Errorf("expected at least 5 auth results, got %d", len(data.Events))
	}
}

func TestLog(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	h.TaskAdd("proj", "task one", nil)
	h.TaskAdd("proj", "task two", nil)

	resp := h.Log("proj", nil, nil)
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	data := resp.Data.(command.LogResult)
	// 1 project-created + 2 task-created = 3 events.
	if len(data.Events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(data.Events))
	}
}

func TestLogFilterByNode(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "task one", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID
	h.TaskAdd("proj", "task two", nil)

	resp := h.Log("proj", &nodeID, nil)
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	data := resp.Data.(command.LogResult)
	if len(data.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(data.Events))
	}
}

func TestLogFilterByType(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	h.TaskAdd("proj", "task one", nil)

	et := store.TaskCreated
	resp := h.Log("proj", nil, &et)
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	data := resp.Data.(command.LogResult)
	if len(data.Events) != 1 {
		t.Fatalf("expected 1 task-created event, got %d", len(data.Events))
	}
}

func TestLogRequiresProjectID(t *testing.T) {
	h := setup(t)
	resp := h.Log("", nil, nil)
	if resp.OK {
		t.Fatal("should fail with empty project_id")
	}
}

func TestTaskBlock(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "task", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID

	resp := h.TaskBlock("proj", nodeID, "waiting on API")
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	data := resp.Data.(command.UpdateResult)
	if len(data.Events) != 2 {
		t.Fatalf("expected 2 events (task-blocked + status-changed), got %d", len(data.Events))
	}
	if data.Events[0].EventType != store.TaskBlocked {
		t.Errorf("first event: %s", data.Events[0].EventType)
	}
	if data.Events[1].EventType != store.StatusChanged {
		t.Errorf("second event: %s", data.Events[1].EventType)
	}

	// Verify status derived as blocked.
	status := h.Status("proj")
	tasks := status.Data.(command.StatusResult).Tasks
	for _, task := range tasks {
		if task.NodeID == nodeID && task.Status != "blocked" {
			t.Errorf("expected blocked, got %s", task.Status)
		}
	}
}

func TestTaskBlockRequiresReason(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "task", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID

	resp := h.TaskBlock("proj", nodeID, "")
	if resp.OK {
		t.Fatal("should fail with empty reason")
	}
}

func TestTaskUnblock(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "task", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID

	h.TaskBlock("proj", nodeID, "waiting")

	resp := h.TaskUnblock("proj", nodeID)
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	data := resp.Data.(command.UpdateResult)
	if len(data.Events) != 2 {
		t.Fatalf("expected 2 events (task-unblocked + status-changed), got %d", len(data.Events))
	}

	// Verify status derived as pending.
	status := h.Status("proj")
	tasks := status.Data.(command.StatusResult).Tasks
	for _, task := range tasks {
		if task.NodeID == nodeID && task.Status != "pending" {
			t.Errorf("expected pending, got %s", task.Status)
		}
	}
}

func TestKnowledgeAdd(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "task", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID

	resp := h.KnowledgeAdd("proj", nodeID, "JWT tokens expire after 1 hour")
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	data := resp.Data.(command.EventData)
	if data.EventType != store.KnowledgeAdded {
		t.Errorf("type: %s", data.EventType)
	}
}

func TestKnowledgeAddRequiresSummary(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "task", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID

	resp := h.KnowledgeAdd("proj", nodeID, "")
	if resp.OK {
		t.Fatal("should fail with empty summary")
	}
}

func TestTaskBlockAtomic(t *testing.T) {
	// G7: TaskBlock uses AppendBatch — both events appear atomically.
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "task", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID

	// Count events before block.
	logBefore := h.Log("proj", nil, nil)
	beforeCount := len(logBefore.Data.(command.LogResult).Events)

	resp := h.TaskBlock("proj", nodeID, "waiting on API")
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	data := resp.Data.(command.UpdateResult)
	if len(data.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(data.Events))
	}

	// Count events after block — exactly 2 more.
	logAfter := h.Log("proj", nil, nil)
	afterCount := len(logAfter.Data.(command.LogResult).Events)
	if afterCount != beforeCount+2 {
		t.Errorf("expected %d events, got %d", beforeCount+2, afterCount)
	}
}

func TestTaskUnblockAtomic(t *testing.T) {
	// G7: TaskUnblock uses AppendBatch — both events appear atomically.
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "task", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID
	h.TaskBlock("proj", nodeID, "waiting")

	logBefore := h.Log("proj", nil, nil)
	beforeCount := len(logBefore.Data.(command.LogResult).Events)

	resp := h.TaskUnblock("proj", nodeID)
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}

	logAfter := h.Log("proj", nil, nil)
	afterCount := len(logAfter.Data.(command.LogResult).Events)
	if afterCount != beforeCount+2 {
		t.Errorf("expected %d events, got %d", beforeCount+2, afterCount)
	}
}

func TestKnowledgeAddSearchable(t *testing.T) {
	// Knowledge added events should be searchable via FTS5.
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	taskResp := h.TaskAdd("proj", "task", nil)
	nodeID := taskResp.Data.(command.EventData).NodeID

	h.KnowledgeAdd("proj", nodeID, "JWT tokens expire after 1 hour for security")

	resp := h.Query("proj", "JWT")
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	data := resp.Data.(command.QueryResult)
	if len(data.Events) < 1 {
		t.Error("expected knowledge event in search results")
	}
}

func TestErrorResponse(t *testing.T) {
	h := setup(t)
	resp := h.Status("nonexistent")
	// Should succeed with empty tasks (no project-created check required for status query)
	if !resp.OK {
		t.Fatalf("status on empty project should be OK: %s", resp.Error)
	}
	data := resp.Data.(command.StatusResult)
	if len(data.Tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(data.Tasks))
	}
}

// Unfinished lists every node that is not done, across every scope in the
// store, including scopes that were never registered.
func TestUnfinishedAcrossScopes(t *testing.T) {
	h := setup(t)

	add := func(project, goal string) string {
		t.Helper()
		resp := h.TaskAdd(project, goal, nil)
		if !resp.OK {
			t.Fatalf("TaskAdd(%s, %s): %s", project, goal, resp.Error)
		}
		return resp.Data.(command.EventData).NodeID
	}

	doneID := add("skills", "finished thing")
	if resp := h.TaskUpdate("skills", doneID, "done", "closed", nil); !resp.OK {
		t.Fatalf("TaskUpdate: %s", resp.Error)
	}
	add("skills", "open thing")
	add("cap", "cap backlog")
	add("never-registered", "orphan thing")

	resp := h.Unfinished()
	if !resp.OK {
		t.Fatalf("Unfinished: %s", resp.Error)
	}
	unfinished := resp.Data.(map[string]any)["unfinished"].(map[string][]command.UnfinishedNode)
	// Every kind is reported, even when it holds nothing: a reader sees the
	// groups it should have looked in.
	for _, kind := range []query.Kind{query.KindWork, query.KindDispatch, query.KindGap} {
		if _, ok := unfinished[string(kind)]; !ok {
			t.Errorf("unfinished has no %q group: %v", kind, unfinished)
		}
	}
	work := unfinished[string(query.KindWork)]
	if len(work) != 3 {
		t.Fatalf("unfinished work has %d nodes, want 3: %+v", len(work), work)
	}

	byGoal := map[string]command.UnfinishedNode{}
	for _, node := range work {
		byGoal[node.Goal] = node
		if node.Kind != query.KindWork {
			t.Errorf("node %s kind = %q, want work", node.NodeID, node.Kind)
		}
	}
	if _, ok := byGoal["finished thing"]; ok {
		t.Error("a done node must not be reported as unfinished")
	}
	for goal, project := range map[string]string{
		"open thing":   "skills",
		"cap backlog":  "cap",
		"orphan thing": "never-registered",
	} {
		node, ok := byGoal[goal]
		if !ok {
			t.Errorf("unfinished %q missing", goal)
			continue
		}
		if node.ProjectID != project {
			t.Errorf("%q project_id = %q, want %q", goal, node.ProjectID, project)
		}
		if node.Status == "done" {
			t.Errorf("%q status = done, want anything else", goal)
		}
	}

	// Deterministic and stable: ordered by scope, then node id.
	for i := 1; i < len(work); i++ {
		prev, cur := work[i-1], work[i]
		if prev.ProjectID > cur.ProjectID {
			t.Errorf("unfinished not ordered by scope: %v", work)
			break
		}
		if prev.ProjectID == cur.ProjectID && prev.NodeID > cur.NodeID {
			t.Errorf("unfinished not ordered by node id within a scope: %v", work)
			break
		}
	}
}
