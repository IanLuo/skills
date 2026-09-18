package query_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/store"
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

func strPtr(s string) *string { return &s }

// seedProject creates a project and returns the store.
func seedProject(t *testing.T, s *store.Store, projectID string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"name": projectID, "root_path": "/tmp/" + projectID})
	_, err := s.Append(store.Event{
		Type:      store.ProjectCreated,
		ProjectID: projectID,
		Payload:   payload,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// seedTask adds a task and returns its node ID.
func seedTask(t *testing.T, s *store.Store, projectID, nodeID, goal string, parent *string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"goal": goal})
	_, err := s.Append(store.Event{
		Type:         store.TaskCreated,
		ProjectID:    projectID,
		NodeID:       &nodeID,
		ParentNodeID: parent,
		Payload:      payload,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func seedStatus(t *testing.T, s *store.Store, projectID, nodeID, from, to string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"from": from, "to": to})
	_, err := s.Append(store.Event{
		Type:      store.StatusChanged,
		ProjectID: projectID,
		NodeID:    &nodeID,
		Payload:   payload,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func seedDecision(t *testing.T, s *store.Store, projectID, nodeID, summary string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"summary": summary})
	_, err := s.Append(store.Event{
		Type:      store.DecisionRecorded,
		ProjectID: projectID,
		NodeID:    &nodeID,
		Payload:   payload,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func seedKnowledge(t *testing.T, s *store.Store, projectID, nodeID, summary string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"summary": summary})
	_, err := s.Append(store.Event{
		Type:      store.KnowledgeAdded,
		ProjectID: projectID,
		NodeID:    &nodeID,
		Payload:   payload,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func seedMetadataChanged(t *testing.T, s *store.Store, projectID, nodeID, field, oldVal, newVal string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"field": field, "old_value": oldVal, "new_value": newVal})
	_, err := s.Append(store.Event{
		Type:      store.MetadataChanged,
		ProjectID: projectID,
		NodeID:    &nodeID,
		Payload:   payload,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// --- DeriveTree tests ---

func TestDeriveTreeEmptyProject(t *testing.T) {
	s := openStore(t)
	seedProject(t, s, "proj")

	e := query.NewEngine(s)
	tree, err := e.DeriveTree("proj")
	if err != nil {
		t.Fatal(err)
	}
	if tree.ProjectID != "proj" {
		t.Errorf("project_id: %s", tree.ProjectID)
	}
	if len(tree.Roots) != 0 {
		t.Errorf("expected 0 roots, got %d", len(tree.Roots))
	}
}

func TestDeriveTreeSingleTask(t *testing.T) {
	s := openStore(t)
	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "implement auth", nil)

	e := query.NewEngine(s)
	tree, err := e.DeriveTree("proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Roots) != 1 {
		t.Fatalf("expected 1 root, got %d", len(tree.Roots))
	}

	node := tree.Roots[0]
	if node.NodeID != "t1" {
		t.Errorf("node_id: %s", node.NodeID)
	}
	if node.Goal != "implement auth" {
		t.Errorf("goal: %s", node.Goal)
	}
	if node.Status != "pending" {
		t.Errorf("status: %s", node.Status)
	}
	if node.ParentID != "" {
		t.Errorf("parent: %s", node.ParentID)
	}
	if len(node.Children) != 0 {
		t.Errorf("children: %d", len(node.Children))
	}
}

func TestDeriveTreeWithHierarchy(t *testing.T) {
	s := openStore(t)
	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "parent task", nil)
	seedTask(t, s, "proj", "t2", "child task", strPtr("t1"))
	seedTask(t, s, "proj", "t3", "grandchild", strPtr("t2"))

	e := query.NewEngine(s)
	tree, err := e.DeriveTree("proj")
	if err != nil {
		t.Fatal(err)
	}

	// Only t1 is a root.
	if len(tree.Roots) != 1 {
		t.Fatalf("expected 1 root, got %d", len(tree.Roots))
	}
	root := tree.Roots[0]
	if root.NodeID != "t1" {
		t.Errorf("root: %s", root.NodeID)
	}
	if len(root.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(root.Children))
	}
	child := root.Children[0]
	if child.NodeID != "t2" {
		t.Errorf("child: %s", child.NodeID)
	}
	if len(child.Children) != 1 {
		t.Fatalf("expected 1 grandchild, got %d", len(child.Children))
	}
	if child.Children[0].NodeID != "t3" {
		t.Errorf("grandchild: %s", child.Children[0].NodeID)
	}
}

func TestDeriveTreeStatusDerived(t *testing.T) {
	s := openStore(t)
	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "task", nil)
	seedStatus(t, s, "proj", "t1", "pending", "active")
	seedStatus(t, s, "proj", "t1", "active", "done")

	e := query.NewEngine(s)
	tree, err := e.DeriveTree("proj")
	if err != nil {
		t.Fatal(err)
	}
	if tree.Roots[0].Status != "done" {
		t.Errorf("status: %s", tree.Roots[0].Status)
	}
}

func TestDeriveTreeDecisionsAndKnowledge(t *testing.T) {
	s := openStore(t)
	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "task", nil)
	seedDecision(t, s, "proj", "t1", "chose JWT")
	seedDecision(t, s, "proj", "t1", "chose PostgreSQL")
	seedKnowledge(t, s, "proj", "t1", "JWT expires in 1h")

	e := query.NewEngine(s)
	tree, err := e.DeriveTree("proj")
	if err != nil {
		t.Fatal(err)
	}
	node := tree.Roots[0]
	if len(node.Decisions) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(node.Decisions))
	}
	if node.Decisions[0] != "chose JWT" {
		t.Errorf("decision[0]: %s", node.Decisions[0])
	}
	if len(node.Knowledge) != 1 {
		t.Fatalf("expected 1 knowledge, got %d", len(node.Knowledge))
	}
	if node.Knowledge[0] != "JWT expires in 1h" {
		t.Errorf("knowledge[0]: %s", node.Knowledge[0])
	}
}

func TestDeriveTreeMetadataChanged(t *testing.T) {
	s := openStore(t)
	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "original goal", nil)
	seedMetadataChanged(t, s, "proj", "t1", "goal", "original goal", "revised goal")

	e := query.NewEngine(s)
	tree, err := e.DeriveTree("proj")
	if err != nil {
		t.Fatal(err)
	}
	if tree.Roots[0].Goal != "revised goal" {
		t.Errorf("goal: %s", tree.Roots[0].Goal)
	}
}

func TestDeriveTreeMultipleRoots(t *testing.T) {
	// AC13: concurrent work streams — multiple top-level tasks.
	s := openStore(t)
	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "stream A", nil)
	seedTask(t, s, "proj", "t2", "stream B", nil)
	seedStatus(t, s, "proj", "t1", "pending", "active")
	seedStatus(t, s, "proj", "t2", "pending", "active")

	e := query.NewEngine(s)
	tree, err := e.DeriveTree("proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Roots) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(tree.Roots))
	}
}

func TestDeriveTreeOrphanEvents(t *testing.T) {
	// Events for a node_id with no task-created — should not crash.
	s := openStore(t)
	seedProject(t, s, "proj")
	seedStatus(t, s, "proj", "ghost", "pending", "active")
	seedDecision(t, s, "proj", "ghost", "decided something")

	e := query.NewEngine(s)
	tree, err := e.DeriveTree("proj")
	if err != nil {
		t.Fatal(err)
	}
	// Ghost node created implicitly with empty goal.
	if len(tree.Roots) != 1 {
		t.Fatalf("expected 1 root (orphan), got %d", len(tree.Roots))
	}
	if tree.Roots[0].Status != "active" {
		t.Errorf("orphan status: %s", tree.Roots[0].Status)
	}
}

func TestDeriveTreeNonexistentProject(t *testing.T) {
	s := openStore(t)

	e := query.NewEngine(s)
	tree, err := e.DeriveTree("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Roots) != 0 {
		t.Errorf("expected 0 roots for nonexistent project, got %d", len(tree.Roots))
	}
}

func TestDeriveTreeNodeLookup(t *testing.T) {
	s := openStore(t)
	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "parent", nil)
	seedTask(t, s, "proj", "t2", "child", strPtr("t1"))

	e := query.NewEngine(s)
	tree, err := e.DeriveTree("proj")
	if err != nil {
		t.Fatal(err)
	}

	// Nodes map should contain all nodes.
	if len(tree.Nodes) != 2 {
		t.Fatalf("expected 2 nodes in lookup, got %d", len(tree.Nodes))
	}
	n, ok := tree.Nodes["t2"]
	if !ok {
		t.Fatal("t2 not in Nodes map")
	}
	if n.ParentID != "t1" {
		t.Errorf("t2 parent: %s", n.ParentID)
	}
}

// --- Summary tests ---

func TestSummaryEmptyProject(t *testing.T) {
	s := openStore(t)
	seedProject(t, s, "proj")

	e := query.NewEngine(s)
	summary, err := e.Summary("proj")
	if err != nil {
		t.Fatal(err)
	}
	if summary == "" {
		t.Error("summary should not be empty even for empty project")
	}
	if !strings.Contains(summary, "proj") {
		t.Errorf("summary should mention project: %s", summary)
	}
}

func TestSummaryWithTasks(t *testing.T) {
	s := openStore(t)
	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "implement auth", nil)
	seedStatus(t, s, "proj", "t1", "pending", "active")
	seedDecision(t, s, "proj", "t1", "chose JWT")
	seedTask(t, s, "proj", "t2", "fix database", nil)

	e := query.NewEngine(s)
	summary, err := e.Summary("proj")
	if err != nil {
		t.Fatal(err)
	}

	// Summary should mention task goals, statuses, and decisions.
	if !strings.Contains(summary, "implement auth") {
		t.Errorf("summary missing task goal: %s", summary)
	}
	if !strings.Contains(summary, "active") {
		t.Errorf("summary missing status: %s", summary)
	}
	if !strings.Contains(summary, "chose JWT") {
		t.Errorf("summary missing decision: %s", summary)
	}
	if !strings.Contains(summary, "fix database") {
		t.Errorf("summary missing second task: %s", summary)
	}
}

func TestSummaryWithHierarchy(t *testing.T) {
	s := openStore(t)
	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "parent task", nil)
	seedTask(t, s, "proj", "t2", "subtask", strPtr("t1"))

	e := query.NewEngine(s)
	summary, err := e.Summary("proj")
	if err != nil {
		t.Fatal(err)
	}

	// Should show hierarchy.
	if !strings.Contains(summary, "subtask") {
		t.Errorf("summary missing subtask: %s", summary)
	}
}

func TestSummaryWithKnowledge(t *testing.T) {
	s := openStore(t)
	seedProject(t, s, "proj")
	seedTask(t, s, "proj", "t1", "auth task", nil)
	seedKnowledge(t, s, "proj", "t1", "JWT expires in 1h")

	e := query.NewEngine(s)
	summary, err := e.Summary("proj")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(summary, "JWT expires in 1h") {
		t.Errorf("summary missing knowledge: %s", summary)
	}
}
