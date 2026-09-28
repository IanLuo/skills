package registry_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/flagship-dev/flagship/internal/registry"
)

func tempDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "registry.db")
}

func open(t *testing.T) *registry.Registry {
	t.Helper()
	r, err := registry.Open(tempDB(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestOpenCreatesDB(t *testing.T) {
	r, err := registry.Open(tempDB(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
}

func TestOpenSetsWAL(t *testing.T) {
	r := open(t)
	mode, err := r.JournalMode()
	if err != nil {
		t.Fatalf("JournalMode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("expected wal, got %s", mode)
	}
}

func TestRegisterAndGet(t *testing.T) {
	r := open(t)

	err := r.Register("myproj", "/tmp/myproj")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	p, err := r.Get("myproj")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Name != "myproj" {
		t.Errorf("name: %s", p.Name)
	}
	if p.RootPath != "/tmp/myproj" {
		t.Errorf("root_path: %s", p.RootPath)
	}
	if p.CreatedAt.IsZero() {
		t.Error("created_at is zero")
	}
	if p.LastActivity.IsZero() {
		t.Error("last_activity is zero")
	}
}

func TestGetNotFound(t *testing.T) {
	r := open(t)

	_, err := r.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent project")
	}
}

func TestRegisterDuplicate(t *testing.T) {
	r := open(t)

	if err := r.Register("proj", "/tmp/a"); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	// Second register with same name should update root_path (upsert).
	if err := r.Register("proj", "/tmp/b"); err != nil {
		t.Fatalf("second Register: %v", err)
	}

	p, err := r.Get("proj")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.RootPath != "/tmp/b" {
		t.Errorf("root_path not updated: %s", p.RootPath)
	}
}

func TestList(t *testing.T) {
	r := open(t)

	// Empty list.
	projects, err := r.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("expected 0, got %d", len(projects))
	}

	// Add two projects.
	r.Register("alpha", "/tmp/alpha")
	r.Register("beta", "/tmp/beta")

	projects, err = r.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2, got %d", len(projects))
	}

	// Verify both present.
	names := map[string]bool{}
	for _, p := range projects {
		names[p.Name] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("missing project: %v", names)
	}
}

func TestUpdateActivity(t *testing.T) {
	r := open(t)

	r.Register("proj", "/tmp/proj")

	// Get initial activity time.
	p1, _ := r.Get("proj")
	initial := p1.LastActivity

	// Wait a moment so timestamp differs.
	time.Sleep(10 * time.Millisecond)

	err := r.UpdateActivity("proj")
	if err != nil {
		t.Fatalf("UpdateActivity: %v", err)
	}

	p2, _ := r.Get("proj")
	if !p2.LastActivity.After(initial) {
		t.Errorf("last_activity not updated: %v vs %v", p2.LastActivity, initial)
	}
}

func TestUpdateActivityNonexistent(t *testing.T) {
	r := open(t)

	// Should not error — no-op for nonexistent project.
	err := r.UpdateActivity("nonexistent")
	if err != nil {
		t.Fatalf("UpdateActivity: %v", err)
	}
}

func TestBusyTimeout(t *testing.T) {
	r := open(t)
	var timeout int
	row := r.DB().QueryRow("PRAGMA busy_timeout")
	if err := row.Scan(&timeout); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if timeout != 5000 {
		t.Errorf("expected busy_timeout=5000, got %d", timeout)
	}
}

// --- Agent Registry Tests ---

func TestRegisterAndGetAgent(t *testing.T) {
	r := open(t)
	a := registry.Agent{
		Name:      "impl-auth",
		Role:      "worker",
		PaneID:    "w1:p5",
		Workspace: "w1",
		Project:   "myapp",
		Playbook:  "worker-playbook",
	}
	if err := r.RegisterAgent(a); err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}

	got, err := r.GetAgent("impl-auth")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.Name != "impl-auth" || got.Role != "worker" || got.PaneID != "w1:p5" {
		t.Errorf("unexpected agent: %+v", got)
	}
	if got.Status != "active" {
		t.Errorf("expected active status, got %s", got.Status)
	}
	if got.LastSeen == "" || got.CreatedAt == "" {
		t.Error("timestamps should be set")
	}
}

func TestGetAgentNotFound(t *testing.T) {
	r := open(t)
	_, err := r.GetAgent("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetAgentByPane(t *testing.T) {
	r := open(t)
	r.RegisterAgent(registry.Agent{Name: "worker1", Role: "worker", PaneID: "w1:p3", Project: "p"})

	got, err := r.GetAgentByPane("w1:p3")
	if err != nil {
		t.Fatalf("GetAgentByPane: %v", err)
	}
	if got.Name != "worker1" {
		t.Errorf("expected worker1, got %s", got.Name)
	}
}

func TestGetAgentByPaneNotFound(t *testing.T) {
	r := open(t)
	_, err := r.GetAgentByPane("w1:p99")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListAgents(t *testing.T) {
	r := open(t)
	r.RegisterAgent(registry.Agent{Name: "captain", Role: "captain", PaneID: "w1:p1", Project: "myapp"})
	r.RegisterAgent(registry.Agent{Name: "worker1", Role: "worker", PaneID: "w1:p2", Project: "myapp"})
	r.RegisterAgent(registry.Agent{Name: "worker2", Role: "worker", PaneID: "w1:p3", Project: "other"})

	// All agents.
	all, err := r.ListAgents(registry.AgentFilter{})
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3, got %d", len(all))
	}

	// Filter by role.
	workers, _ := r.ListAgents(registry.AgentFilter{Role: "worker"})
	if len(workers) != 2 {
		t.Errorf("expected 2 workers, got %d", len(workers))
	}

	// Filter by project.
	myapp, _ := r.ListAgents(registry.AgentFilter{Project: "myapp"})
	if len(myapp) != 2 {
		t.Errorf("expected 2 in myapp, got %d", len(myapp))
	}
}

func TestSingleCaptainEnforcement(t *testing.T) {
	r := open(t)
	// First captain succeeds.
	err := r.RegisterAgent(registry.Agent{Name: "captain", Role: "captain", PaneID: "w1:p1", Project: "myapp"})
	if err != nil {
		t.Fatalf("first captain: %v", err)
	}

	// Second captain for same project fails.
	err = r.RegisterAgent(registry.Agent{Name: "captain2", Role: "captain", PaneID: "w1:p2", Project: "myapp"})
	if err == nil {
		t.Fatal("expected error for second captain")
	}

	// Re-registering the same captain (name) succeeds (upsert).
	err = r.RegisterAgent(registry.Agent{Name: "captain", Role: "captain", PaneID: "w1:p9", Project: "myapp"})
	if err != nil {
		t.Fatalf("re-register same captain: %v", err)
	}

	// Captain for different project succeeds.
	err = r.RegisterAgent(registry.Agent{Name: "captain-other", Role: "captain", PaneID: "w1:p3", Project: "other"})
	if err != nil {
		t.Fatalf("captain for other project: %v", err)
	}

	// Dead captain allows replacement.
	r.UpdateAgent("captain", "dead", "")
	err = r.RegisterAgent(registry.Agent{Name: "captain-new", Role: "captain", PaneID: "w1:p4", Project: "myapp"})
	if err != nil {
		t.Fatalf("captain after dead: %v", err)
	}
}

func TestTouchAgent(t *testing.T) {
	r := open(t)
	r.RegisterAgent(registry.Agent{Name: "w", Role: "worker", PaneID: "w1:p1", Project: "p"})

	a1, _ := r.GetAgent("w")
	time.Sleep(10 * time.Millisecond)
	r.TouchAgent("w1:p1")
	a2, _ := r.GetAgent("w")

	if a2.LastSeen <= a1.LastSeen {
		t.Errorf("last_seen not updated: %s vs %s", a1.LastSeen, a2.LastSeen)
	}
}

func TestTouchAgentNoOp(t *testing.T) {
	r := open(t)
	// Should not error — no-op.
	r.TouchAgent("nonexistent")
}

func TestHeartbeatAgent(t *testing.T) {
	r := open(t)
	r.RegisterAgent(registry.Agent{Name: "w", Role: "worker", PaneID: "w1:p1", Project: "p"})

	if err := r.HeartbeatAgent("w"); err != nil {
		t.Fatalf("HeartbeatAgent: %v", err)
	}

	// Nonexistent agent errors.
	if err := r.HeartbeatAgent("nope"); err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
}

func TestUpdateAgent(t *testing.T) {
	r := open(t)
	r.RegisterAgent(registry.Agent{Name: "w", Role: "worker", PaneID: "w1:p1", Project: "p"})

	if err := r.UpdateAgent("w", "idle", ""); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	a, _ := r.GetAgent("w")
	if a.Status != "idle" {
		t.Errorf("expected idle, got %s", a.Status)
	}

	// Invalid status.
	if err := r.UpdateAgent("w", "invalid", ""); err == nil {
		t.Fatal("expected error for invalid status")
	}

	// Not found.
	if err := r.UpdateAgent("nope", "dead", ""); err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
}

func TestDeregisterAgent(t *testing.T) {
	r := open(t)
	r.RegisterAgent(registry.Agent{Name: "w", Role: "worker", PaneID: "w1:p1", Project: "p"})

	if err := r.DeregisterAgent("w"); err != nil {
		t.Fatalf("DeregisterAgent: %v", err)
	}
	_, err := r.GetAgent("w")
	if err == nil {
		t.Fatal("expected not found after deregister")
	}

	// Double deregister errors.
	if err := r.DeregisterAgent("w"); err == nil {
		t.Fatal("expected error for already deregistered")
	}
}

func TestRegisterAgentValidation(t *testing.T) {
	r := open(t)

	// Missing name.
	err := r.RegisterAgent(registry.Agent{Role: "worker", PaneID: "p1"})
	if err == nil {
		t.Fatal("expected error for missing name")
	}

	// Invalid role.
	err = r.RegisterAgent(registry.Agent{Name: "x", Role: "boss", PaneID: "p1"})
	if err == nil {
		t.Fatal("expected error for invalid role")
	}

	// Missing pane.
	err = r.RegisterAgent(registry.Agent{Name: "x", Role: "worker"})
	if err == nil {
		t.Fatal("expected error for missing pane")
	}
}

func TestListOrderByLastActivity(t *testing.T) {
	r := open(t)

	r.Register("old", "/tmp/old")
	time.Sleep(10 * time.Millisecond)
	r.Register("new", "/tmp/new")

	projects, _ := r.List()
	if len(projects) != 2 {
		t.Fatalf("expected 2, got %d", len(projects))
	}
	// Most recent first.
	if projects[0].Name != "new" {
		t.Errorf("expected 'new' first, got %s", projects[0].Name)
	}
	if projects[1].Name != "old" {
		t.Errorf("expected 'old' second, got %s", projects[1].Name)
	}
}
