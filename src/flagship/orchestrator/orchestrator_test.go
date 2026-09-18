package orchestrator_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/engine"
	"github.com/flagship-dev/flagship/internal/knowledge"
	"github.com/flagship-dev/flagship/internal/store"
	"github.com/flagship-dev/flagship/orchestrator"
)

func tempDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "store.db")
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(tempDB(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func openKB(t *testing.T) *knowledge.Center {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "kb")
	kc, err := knowledge.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return kc
}

func seedProject(t *testing.T, s *store.Store, projectID string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"name": projectID, "root_path": "/tmp/" + projectID})
	s.Append(store.Event{Type: store.ProjectCreated, ProjectID: projectID, Payload: payload})
}

func seedTask(t *testing.T, s *store.Store, projectID, nodeID, goal string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"goal": goal})
	s.Append(store.Event{Type: store.TaskCreated, ProjectID: projectID, NodeID: &nodeID, Payload: payload})
}

func seedStatus(t *testing.T, s *store.Store, projectID, nodeID, from, to string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"from": from, "to": to})
	s.Append(store.Event{Type: store.StatusChanged, ProjectID: projectID, NodeID: &nodeID, Payload: payload})
}

// --- Mock Engine ---

type mockEngine struct {
	spawnCalls []engine.SpawnConfig
	readOutput string
	spawnErr   error
	readErr    error
	closeErr   error
	paneID     string
}

func (m *mockEngine) Spawn(config engine.SpawnConfig) (engine.AgentHandle, error) {
	m.spawnCalls = append(m.spawnCalls, config)
	if m.spawnErr != nil {
		return engine.AgentHandle{}, m.spawnErr
	}
	id := m.paneID
	if id == "" {
		id = "mock-pane-1"
	}
	return engine.AgentHandle{ID: id, Engine: "mock"}, nil
}

func (m *mockEngine) Read(handle engine.AgentHandle) (string, error) {
	return m.readOutput, m.readErr
}

func (m *mockEngine) Close(handle engine.AgentHandle) error {
	return m.closeErr
}

// --- Tests ---

func TestRunTaskComposesPromptWithContext(t *testing.T) {
	s := openStore(t)
	kc := openKB(t)
	me := &mockEngine{readOutput: "task completed successfully"}

	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "implement auth")
	seedStatus(t, s, "proj", "t1", "pending", "active")

	o := orchestrator.New(s, kc, me)
	result, err := o.RunTask("proj", "t1", "dev-task")
	if err != nil {
		t.Fatal(err)
	}

	// Check that the spawn config has a prompt with project context.
	if len(me.spawnCalls) != 1 {
		t.Fatalf("expected 1 spawn call, got %d", len(me.spawnCalls))
	}
	prompt := me.spawnCalls[0].Prompt
	if !strings.Contains(prompt, "implement auth") {
		t.Errorf("prompt missing task goal: %s", prompt)
	}
	if !strings.Contains(prompt, "proj") {
		t.Errorf("prompt missing project context: %s", prompt)
	}

	if result.AgentOutput != "task completed successfully" {
		t.Errorf("result: %s", result.AgentOutput)
	}
}

func TestRunTaskIncludesProcedures(t *testing.T) {
	// AC6: if a knowledge center procedure exists, it should appear in the prompt.
	s := openStore(t)
	kc := openKB(t)
	me := &mockEngine{readOutput: "done"}

	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "implement feature")

	// Add a procedure for dev-task.
	kc.Add("dev-task-procedure", []byte(`name: dev-task-procedure
type: procedure
trigger: dev-task
steps:
  - write failing test first
  - implement the narrowest change
  - run tests
`))

	o := orchestrator.New(s, kc, me)
	_, err := o.RunTask("proj", "t1", "dev-task")
	if err != nil {
		t.Fatal(err)
	}

	prompt := me.spawnCalls[0].Prompt
	if !strings.Contains(prompt, "write failing test first") {
		t.Errorf("prompt missing procedure steps: %s", prompt)
	}
}

func TestRunTaskSetsSkill(t *testing.T) {
	s := openStore(t)
	kc := openKB(t)
	me := &mockEngine{readOutput: "ok"}

	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "task")

	// Add a routing rule.
	kc.Add("skill-routing", []byte(`name: skill-routing
type: routing
trigger: dev-task
steps:
  - dev-task
`))

	o := orchestrator.New(s, kc, me)
	_, err := o.RunTask("proj", "t1", "dev-task")
	if err != nil {
		t.Fatal(err)
	}

	if me.spawnCalls[0].Skill != "dev-task" {
		t.Errorf("skill: %s", me.spawnCalls[0].Skill)
	}
}

func TestRunTaskBlocksOnMissingPrerequisite(t *testing.T) {
	// AC14: prerequisite check — block if required docs missing.
	s := openStore(t)
	kc := openKB(t)
	me := &mockEngine{readOutput: "ok"}

	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "implement feature")

	// Add a prerequisite playbook that requires a spec.
	kc.Add("task-prerequisites", []byte(`name: task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - check: ensure locked spec exists
  - check: ensure architecture doc exists
`))

	o := orchestrator.New(s, kc, me)
	_, err := o.RunTask("proj", "t1", "dev-task")
	if err == nil {
		t.Fatal("expected error for missing prerequisites")
	}
	if !strings.Contains(err.Error(), "prerequisite") {
		t.Errorf("error should mention prerequisite: %v", err)
	}
	// Should NOT have spawned.
	if len(me.spawnCalls) != 0 {
		t.Errorf("should not spawn when prerequisites missing, got %d calls", len(me.spawnCalls))
	}
}

func TestRunTaskPassesPrerequisiteWhenDocsExist(t *testing.T) {
	// AC14: prerequisite check passes when docs are present.
	s := openStore(t)
	kc := openKB(t)
	me := &mockEngine{readOutput: "ok"}

	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "implement feature")

	// Add prerequisite playbook.
	kc.Add("task-prerequisites", []byte(`name: task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - check: ensure locked spec exists
  - check: ensure architecture doc exists
`))

	// Provide a prerequisite checker that says docs exist.
	o := orchestrator.New(s, kc, me)
	o.SetPrereqChecker(func(step string) bool { return true })

	_, err := o.RunTask("proj", "t1", "dev-task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(me.spawnCalls) != 1 {
		t.Fatalf("expected 1 spawn, got %d", len(me.spawnCalls))
	}
}

func TestRunTaskEngineUnavailable(t *testing.T) {
	// SYSTEM-DESIGN R4: engine unavailable → report error.
	s := openStore(t)
	kc := openKB(t)
	me := &mockEngine{spawnErr: engine.ErrUnavailable("herdr not running")}

	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "task")

	o := orchestrator.New(s, kc, me)
	_, err := o.RunTask("proj", "t1", "dev-task")
	if err == nil {
		t.Fatal("expected error when engine unavailable")
	}
	if !strings.Contains(err.Error(), "herdr not running") {
		t.Errorf("error: %v", err)
	}
}

func TestRunTaskTaskNotFound(t *testing.T) {
	s := openStore(t)
	kc := openKB(t)
	me := &mockEngine{}

	seedProject(t, s, "proj")

	o := orchestrator.New(s, kc, me)
	_, err := o.RunTask("proj", "nonexistent", "dev-task")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestRunTaskPromptIncludesSummary(t *testing.T) {
	s := openStore(t)
	kc := openKB(t)
	me := &mockEngine{readOutput: "done"}

	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "implement auth")
	seedTask(t, s, "proj", "t2", "fix database")
	seedStatus(t, s, "proj", "t2", "pending", "done")

	o := orchestrator.New(s, kc, me)
	_, err := o.RunTask("proj", "t1", "dev-task")
	if err != nil {
		t.Fatal(err)
	}

	// Prompt should include the project summary (other tasks, context).
	prompt := me.spawnCalls[0].Prompt
	if !strings.Contains(prompt, "fix database") {
		t.Errorf("prompt missing sibling task context: %s", prompt)
	}
}

func TestRunTaskNoProcedureStillWorks(t *testing.T) {
	// When no procedure/routing playbook exists, orchestrator still works.
	s := openStore(t)
	kc := openKB(t)
	me := &mockEngine{readOutput: "output"}

	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "task")

	o := orchestrator.New(s, kc, me)
	result, err := o.RunTask("proj", "t1", "dev-task")
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentOutput != "output" {
		t.Errorf("output: %s", result.AgentOutput)
	}
}

func TestRunTaskAppendsTaskBlockedOnReadFailure(t *testing.T) {
	// G5: When engine.Read() fails, orchestrator appends task-blocked event.
	s := openStore(t)
	kc := openKB(t)
	me := &mockEngine{readErr: fmt.Errorf("agent stalled")}

	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "implement feature")
	seedStatus(t, s, "proj", "t1", "pending", "active")

	// Create a command handler so orchestrator can write events.
	dbPath := s.Path()
	cmdH, err := command.NewHandler(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmdH.Close() })

	o := orchestrator.New(s, kc, me)
	o.SetCommandHandler(cmdH)

	_, err = o.RunTask("proj", "t1", "dev-task")
	if err == nil {
		t.Fatal("expected error from read failure")
	}

	// Verify task-blocked event was appended.
	events, _ := s.Replay("proj", &store.ReplayFilter{Types: []store.EventType{store.TaskBlocked}})
	if len(events) == 0 {
		t.Fatal("expected task-blocked event in store")
	}
	// Verify the event references the right node and has the error.
	blocked := events[0]
	if blocked.NodeID == nil || *blocked.NodeID != "t1" {
		t.Errorf("task-blocked node_id: %v", blocked.NodeID)
	}
	var payload struct{ Reason string }
	json.Unmarshal(blocked.Payload, &payload)
	if !strings.Contains(payload.Reason, "agent stalled") {
		t.Errorf("task-blocked reason: %s", payload.Reason)
	}
}

func TestRunTaskAppendsTaskBlockedOnSpawnFailure(t *testing.T) {
	// G5: When engine.Spawn() fails, orchestrator appends task-blocked event.
	s := openStore(t)
	kc := openKB(t)
	me := &mockEngine{spawnErr: fmt.Errorf("herdr not running")}

	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "implement feature")

	dbPath := s.Path()
	cmdH, err := command.NewHandler(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmdH.Close() })

	o := orchestrator.New(s, kc, me)
	o.SetCommandHandler(cmdH)

	_, err = o.RunTask("proj", "t1", "dev-task")
	if err == nil {
		t.Fatal("expected error from spawn failure")
	}

	// Verify task-blocked event was appended.
	events, _ := s.Replay("proj", &store.ReplayFilter{Types: []store.EventType{store.TaskBlocked}})
	if len(events) == 0 {
		t.Fatal("expected task-blocked event in store")
	}
	var payload struct{ Reason string }
	json.Unmarshal(events[0].Payload, &payload)
	if !strings.Contains(payload.Reason, "herdr not running") {
		t.Errorf("task-blocked reason: %s", payload.Reason)
	}
}

func TestRunTaskClosesPaneAfterRead(t *testing.T) {
	s := openStore(t)
	kc := openKB(t)

	closeCalled := false
	me := &mockEngine{
		readOutput: "done",
	}
	// We'll verify Close was called via the result summary.
	// Since mockEngine doesn't track close calls directly, let's add that.
	tracker := &closeTracker{Engine: me}

	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "task")

	o := orchestrator.New(s, kc, tracker)
	_, err := o.RunTask("proj", "t1", "dev-task")
	if err != nil {
		t.Fatal(err)
	}
	closeCalled = tracker.closed
	if !closeCalled {
		t.Error("pane should be closed after read (Keep=false)")
	}
}

type closeTracker struct {
	engine.Engine
	closed bool
}

func (ct *closeTracker) Spawn(config engine.SpawnConfig) (engine.AgentHandle, error) {
	return ct.Engine.Spawn(config)
}
func (ct *closeTracker) Read(handle engine.AgentHandle) (string, error) {
	return ct.Engine.Read(handle)
}
func (ct *closeTracker) Close(handle engine.AgentHandle) error {
	ct.closed = true
	return ct.Engine.Close(handle)
}
