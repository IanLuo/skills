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
