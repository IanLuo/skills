package knowledge_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/knowledge"
)

const samplePlaybook = `name: dev-task-prereqs
type: prerequisite
trigger: dev-task
steps:
  - check: test -f AGENTS.md
  - ask: is the spec locked?
  - check: grep -rl 'specs:locked' .
engine_scope: herdr
`

const samplePlaybook2 = `name: code-review
type: procedure
trigger: review-task
steps:
  - say: read the diff
  - check: go test ./...
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
	if pb.Steps[0] != (knowledge.Step{Kind: knowledge.KindCheck, Body: "test -f AGENTS.md"}) {
		t.Errorf("step 0: %+v", pb.Steps[0])
	}
	if pb.Steps[1].Kind != knowledge.KindAsk {
		t.Errorf("step 1 kind: %s", pb.Steps[1].Kind)
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
  - check: test -f AGENTS.md
  - ask: is the spec locked?
  - check: grep -rl 'specs:locked' .
  - check: go vet ./...
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

// A step without a known kind prefix — including prose carrying a colon of its
// own — defaults to say, so playbooks written before kinds existed keep parsing.
func TestStepKindDefaultsToSay(t *testing.T) {
	kc, err := knowledge.Open(tempKB(t))
	if err != nil {
		t.Fatal(err)
	}

	const prose = `name: p
type: procedure
trigger: t
steps:
  - read AGENTS.md at the project root
  - grep -rl for the specs:locked marker
  - say: read this: carefully
`
	if err := kc.Add("p", []byte(prose)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	pb, _ := kc.Get("p")

	want := []knowledge.Step{
		{Kind: knowledge.KindSay, Body: "read AGENTS.md at the project root"},
		{Kind: knowledge.KindSay, Body: "grep -rl for the specs:locked marker"},
		{Kind: knowledge.KindSay, Body: "read this: carefully"},
	}
	if !reflect.DeepEqual(pb.Steps, want) {
		t.Errorf("steps = %+v, want %+v", pb.Steps, want)
	}
}

func TestAddValidatesStepKinds(t *testing.T) {
	cases := []struct {
		name string
		pb   string
		want []string // substrings the error must carry
	}{
		{
			name: "prerequisite refuses say",
			pb: `name: p
type: prerequisite
trigger: t
steps:
  - check: test -f AGENTS.md
  - say: read AGENTS.md before editing
`,
			want: []string{"step 2", "read AGENTS.md before editing", `"say"`, "allowed kinds: check, ask"},
		},
		{
			name: "procedure refuses ask",
			pb: `name: p
type: procedure
trigger: t
steps:
  - say: read the diff
  - ask: is the acceptance check stated?
`,
			want: []string{"step 2", "is the acceptance check stated?", `"ask"`, "allowed kinds: say, check"},
		},
		{
			name: "routing refuses two steps",
			pb: `name: p
type: routing
trigger: t
steps:
  - say: dev-task
  - say: review-task
`,
			want: []string{"step 2", "review-task", "exactly one step", "allowed kinds: say"},
		},
		{
			name: "routing refuses a non-say kind",
			pb: `name: p
type: routing
trigger: t
steps:
  - check: dev-task
`,
			want: []string{"dev-task", `"check"`, "allowed kinds: say"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kc, err := knowledge.Open(tempKB(t))
			if err != nil {
				t.Fatal(err)
			}
			err = kc.Add("p", []byte(tc.pb))
			if err == nil {
				t.Fatal("expected Add to refuse the playbook")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q must contain %q", err, want)
				}
			}
		})
	}
}

func TestEditValidatesStepKinds(t *testing.T) {
	kc, err := knowledge.Open(tempKB(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := kc.Add("dev-task-prereqs", []byte(samplePlaybook)); err != nil {
		t.Fatalf("Add: %v", err)
	}

	invalid := strings.Replace(samplePlaybook, "  - check: test -f AGENTS.md", "  - say: test -f AGENTS.md", 1)
	if err := kc.Edit("dev-task-prereqs", []byte(invalid)); err == nil {
		t.Fatal("expected Edit to refuse a prerequisite carrying a say step")
	}

	// A refused edit must leave the file on disk untouched.
	pb, err := kc.Get("dev-task-prereqs")
	if err != nil {
		t.Fatal(err)
	}
	if pb.Steps[0].Kind != knowledge.KindCheck {
		t.Errorf("file changed after a refused edit: %+v", pb.Steps[0])
	}
}

func TestRoutingAcceptsOneSayStep(t *testing.T) {
	kc, err := knowledge.Open(tempKB(t))
	if err != nil {
		t.Fatal(err)
	}

	const route = `name: route-dev
type: routing
trigger: dev-task
steps:
  - say: dev-task
`
	if err := kc.Add("route-dev", []byte(route)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	pb, _ := kc.Get("route-dev")
	if len(pb.Steps) != 1 || pb.Steps[0] != (knowledge.Step{Kind: knowledge.KindSay, Body: "dev-task"}) {
		t.Errorf("steps = %+v", pb.Steps)
	}
}

// Validation is a write-path rule: Get must return whatever is on disk.
func TestGetReadsInvalidPlaybook(t *testing.T) {
	dir := tempKB(t)
	kc, err := knowledge.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	const raw = `name: p
type: prerequisite
trigger: t
steps:
  - say: prose only
`
	if err := os.WriteFile(filepath.Join(dir, "p.yaml"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	pb, err := kc.Get("p")
	if err != nil {
		t.Fatalf("Get must read whatever is on disk: %v", err)
	}
	if len(pb.Steps) != 1 || pb.Steps[0].Kind != knowledge.KindSay {
		t.Errorf("steps = %+v", pb.Steps)
	}
}
