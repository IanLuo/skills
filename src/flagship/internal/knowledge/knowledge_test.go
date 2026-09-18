package knowledge_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/flagship-dev/flagship/internal/knowledge"
)

const samplePlaybook = `name: dev-task-prereqs
type: prerequisite
trigger: dev-task
steps:
  - ensure locked spec exists
  - ensure architecture doc exists
  - verify test harness available
engine_scope: herdr
`

const samplePlaybook2 = `name: code-review
type: procedure
trigger: review-task
steps:
  - check diff against code quality bar
  - verify tests pass
`

func tempKB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "kb")
}

func TestOpenCreatesDir(t *testing.T) {
	dir := tempKB(t)
	_, err := knowledge.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("dir not created: %v", err)
	}
}

func TestAddAndGet(t *testing.T) {
	kc, err := knowledge.Open(tempKB(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := kc.Add("dev-task-prereqs", []byte(samplePlaybook)); err != nil {
		t.Fatalf("Add: %v", err)
	}

	pb, err := kc.Get("dev-task-prereqs")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if pb.Name != "dev-task-prereqs" {
		t.Errorf("name: %s", pb.Name)
	}
	if pb.Type != "prerequisite" {
		t.Errorf("type: %s", pb.Type)
	}
	if pb.Trigger != "dev-task" {
		t.Errorf("trigger: %s", pb.Trigger)
	}
	if len(pb.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(pb.Steps))
	}
	if pb.Steps[0] != "ensure locked spec exists" {
		t.Errorf("step 0: %s", pb.Steps[0])
	}
	if pb.EngineScope != "herdr" {
		t.Errorf("engine_scope: %s", pb.EngineScope)
	}
}

func TestAddDuplicateRejects(t *testing.T) {
	kc, err := knowledge.Open(tempKB(t))
	if err != nil {
		t.Fatal(err)
	}

	kc.Add("test", []byte(samplePlaybook))
	err = kc.Add("test", []byte(samplePlaybook))
	if err == nil {
		t.Fatal("expected error for duplicate add")
	}
}

func TestAddNameMismatch(t *testing.T) {
	kc, err := knowledge.Open(tempKB(t))
	if err != nil {
		t.Fatal(err)
	}

	// File has name: dev-task-prereqs, but we pass --name different.
	err = kc.Add("wrong-name", []byte(samplePlaybook))
	if err == nil {
		t.Fatal("expected error for name mismatch")
	}
}

func TestAddInvalidYAML(t *testing.T) {
	kc, err := knowledge.Open(tempKB(t))
	if err != nil {
		t.Fatal(err)
	}

	err = kc.Add("test", []byte("this is not valid yaml at all"))
	if err == nil {
		t.Fatal("expected error for invalid content")
	}
}

func TestGetNotFound(t *testing.T) {
	kc, err := knowledge.Open(tempKB(t))
	if err != nil {
		t.Fatal(err)
	}

	_, err = kc.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent playbook")
	}
}

func TestList(t *testing.T) {
	kc, err := knowledge.Open(tempKB(t))
	if err != nil {
		t.Fatal(err)
	}

	// Empty list.
	names, err := kc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("expected 0, got %d", len(names))
	}

	kc.Add("dev-task-prereqs", []byte(samplePlaybook))
	kc.Add("code-review", []byte(samplePlaybook2))

	names, err = kc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2, got %d", len(names))
	}
	// Sorted alphabetically.
	if names[0] != "code-review" || names[1] != "dev-task-prereqs" {
		t.Errorf("names: %v", names)
	}
}

func TestEdit(t *testing.T) {
	kc, err := knowledge.Open(tempKB(t))
	if err != nil {
		t.Fatal(err)
	}

	kc.Add("dev-task-prereqs", []byte(samplePlaybook))

	updated := `name: dev-task-prereqs
type: prerequisite
trigger: dev-task
steps:
  - ensure locked spec exists
  - ensure architecture doc exists
  - verify test harness available
  - new step added
engine_scope: herdr
`
	if err := kc.Edit("dev-task-prereqs", []byte(updated)); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	pb, _ := kc.Get("dev-task-prereqs")
	if len(pb.Steps) != 4 {
		t.Fatalf("expected 4 steps after edit, got %d", len(pb.Steps))
	}
}

func TestEditNotFound(t *testing.T) {
	kc, err := knowledge.Open(tempKB(t))
	if err != nil {
		t.Fatal(err)
	}

	err = kc.Edit("nonexistent", []byte(samplePlaybook))
	if err == nil {
		t.Fatal("expected error for editing nonexistent playbook")
	}
}

func TestRemove(t *testing.T) {
	kc, err := knowledge.Open(tempKB(t))
	if err != nil {
		t.Fatal(err)
	}

	kc.Add("dev-task-prereqs", []byte(samplePlaybook))

	if err := kc.Remove("dev-task-prereqs"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	_, err = kc.Get("dev-task-prereqs")
	if err == nil {
		t.Fatal("expected error after removal")
	}

	names, _ := kc.List()
	if len(names) != 0 {
		t.Errorf("expected 0 after remove, got %d", len(names))
	}
}

func TestRemoveNotFound(t *testing.T) {
	kc, err := knowledge.Open(tempKB(t))
	if err != nil {
		t.Fatal(err)
	}

	err = kc.Remove("nonexistent")
	if err == nil {
		t.Fatal("expected error for removing nonexistent playbook")
	}
}

func TestParsePlaybookWithoutEngineScope(t *testing.T) {
	kc, err := knowledge.Open(tempKB(t))
	if err != nil {
		t.Fatal(err)
	}

	kc.Add("code-review", []byte(samplePlaybook2))
	pb, _ := kc.Get("code-review")

	if pb.EngineScope != "" {
		t.Errorf("engine_scope should be empty, got %s", pb.EngineScope)
	}
	if pb.Name != "code-review" {
		t.Errorf("name: %s", pb.Name)
	}
}
