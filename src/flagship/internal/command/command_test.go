package command_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
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

	resp := h.TaskEdit("proj", nodeID, "revised goal", nil)
	if !resp.OK {
		t.Fatalf("not OK: %s", resp.Error)
	}
	data := resp.Data.(command.EventData)
	if data.EventType != store.MetadataChanged {
		t.Errorf("type: %s", data.EventType)
	}
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

	h.TaskEdit("proj", nodeID, "revised", nil)

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
	unfinished := resp.Data.(map[string]any)["unfinished"].([]command.UnfinishedNode)
	if len(unfinished) != 3 {
		t.Fatalf("unfinished has %d nodes, want 3: %+v", len(unfinished), unfinished)
	}

	byGoal := map[string]command.UnfinishedNode{}
	for _, node := range unfinished {
		byGoal[node.Goal] = node
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
	for i := 1; i < len(unfinished); i++ {
		prev, cur := unfinished[i-1], unfinished[i]
		if prev.ProjectID > cur.ProjectID {
			t.Errorf("unfinished not ordered by scope: %v", unfinished)
			break
		}
		if prev.ProjectID == cur.ProjectID && prev.NodeID > cur.NodeID {
			t.Errorf("unfinished not ordered by node id within a scope: %v", unfinished)
			break
		}
	}
}
