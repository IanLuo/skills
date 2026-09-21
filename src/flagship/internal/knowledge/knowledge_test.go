package knowledge_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/knowledge"
)

const samplePlaybook = `name: code-entry
phase: entry
applies_when: code
order: 10
steps:
  - check: test -f AGENTS.md
  - ask: is the spec locked?
  - check: grep -rl 'specs:locked' .
`

const samplePlaybook2 = `name: code-review
phase: work
applies_when: code
order: 50
steps:
  - say: read the diff
  - use: review-task — the review loop
`

// tempFS is a fresh ~/.fs for one test. The global KB lives at its kb/
// subdirectory, so the path to a playbook file is kbPath(dir, name).
func tempFS(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// kbPath is where one global playbook file lives under an fs root.
func kbPath(fsRoot, name string) string {
	return filepath.Join(fsRoot, "kb", name+".yaml")
}

// projectKBPath is where one project's playbook file lives under an fs root.
func projectKBPath(fsRoot, project, name string) string {
	return filepath.Join(fsRoot, "projects", project, "kb", name+".yaml")
}

func TestOpenCreatesDir(t *testing.T) {
	dir := tempFS(t)
	if _, err := knowledge.Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "kb")); err != nil {
		t.Fatalf("global dir not created: %v", err)
	}
	// A project's KB is not created: absence is the global-only case, not a
	// directory to invent.
	if _, err := os.Stat(filepath.Join(dir, "projects")); !os.IsNotExist(err) {
		t.Errorf("Open created a projects dir (stat err = %v); it must not", err)
	}
}

func TestAddAndGet(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := kc.Add("code-entry", []byte(samplePlaybook)); err != nil {
		t.Fatalf("Add: %v", err)
	}

	pb, err := kc.Get("code-entry")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if pb.Name != "code-entry" {
		t.Errorf("name: %s", pb.Name)
	}
	if pb.Phase != knowledge.PhaseEntry {
		t.Errorf("phase: %s", pb.Phase)
	}
	if got := pb.AppliesWhenString(); got != "code" {
		t.Errorf("applies_when: %s", got)
	}
	if pb.Order != 10 {
		t.Errorf("order: %d", pb.Order)
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
}

// An absent order is the default, not zero: a playbook that does not say where
// it sits runs after the ones that did.
func TestOrderDefaultsWhenAbsent(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}
	const noOrder = `name: worker
phase: work
steps:
  - say: note your node
`
	if err := kc.Add("worker", []byte(noOrder)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	pb, _ := kc.Get("worker")
	if pb.Order <= 100 {
		t.Errorf("order = %d, want the default well below the shipped orders", pb.Order)
	}
	if len(pb.AppliesWhen) != 0 {
		t.Errorf("applies_when = %v, want empty (matches every dispatch)", pb.AppliesWhen)
	}
	if !pb.Matches(map[string]any{"type": "anything"}) {
		t.Error("a playbook with no applies_when must match every dispatch")
	}
}

func TestAddDuplicateRejects(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}

	kc.Add("code-entry", []byte(samplePlaybook))
	err = kc.Add("code-entry", []byte(samplePlaybook))
	if err == nil {
		t.Fatal("expected error for duplicate add")
	}
}

func TestAddNameMismatch(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}

	// File declares name: code-entry, but we pass --name different.
	err = kc.Add("wrong-name", []byte(samplePlaybook))
	if err == nil {
		t.Fatal("expected error for name mismatch")
	}
	if !strings.Contains(err.Error(), "must equal its file's") {
		t.Errorf("error %q must say the name and the file have to agree", err)
	}
}

func TestAddInvalidYAML(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}

	err = kc.Add("test", []byte("this is not valid yaml at all"))
	if err == nil {
		t.Fatal("expected error for invalid content")
	}
}

func TestGetNotFound(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}

	_, err = kc.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent playbook")
	}
}

func TestList(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
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

	kc.Add("code-entry", []byte(samplePlaybook))
	kc.Add("code-review", []byte(samplePlaybook2))

	names, err = kc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2, got %d", len(names))
	}
	// Sorted alphabetically.
	if names[0] != "code-entry" || names[1] != "code-review" {
		t.Errorf("names: %v", names)
	}
}

func TestEdit(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}

	kc.Add("code-entry", []byte(samplePlaybook))

	updated := strings.Replace(samplePlaybook, "  - ask: is the spec locked?\n", "", 1) +
		"  - check: go vet ./...\n"
	if err := kc.Edit("code-entry", []byte(updated)); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	pb, _ := kc.Get("code-entry")
	if len(pb.Steps) != 3 {
		t.Fatalf("expected 3 steps after edit, got %d", len(pb.Steps))
	}
	if pb.Steps[2].Body != "go vet ./..." {
		t.Errorf("last step = %+v, want the appended check", pb.Steps[2])
	}
}

func TestEditNotFound(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}

	err = kc.Edit("nonexistent", []byte(samplePlaybook))
	if err == nil {
		t.Fatal("expected error for editing nonexistent playbook")
	}
}

func TestRemove(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}

	kc.Add("code-entry", []byte(samplePlaybook))

	if err := kc.Remove("code-entry"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	_, err = kc.Get("code-entry")
	if err == nil {
		t.Fatal("expected error after removal")
	}

	names, _ := kc.List()
	if len(names) != 0 {
		t.Errorf("expected 0 after remove, got %d", len(names))
	}
}

func TestRemoveNotFound(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}

	err = kc.Remove("nonexistent")
	if err == nil {
		t.Fatal("expected error for removing nonexistent playbook")
	}
}

// A step without a known kind prefix — including prose carrying a colon of its
// own — defaults to say, so a playbook body may hold a colon and still read.
func TestStepKindDefaultsToSay(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}

	const prose = `name: p
phase: work
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

// The kinds a phase allows are the schema's spine: a gate asserts and asks, a
// worker's brief guides, and only the exit phase acts.
func TestAddValidatesStepKinds(t *testing.T) {
	cases := []struct {
		name string
		pb   string
		want []string // substrings the error must carry
	}{
		{
			name: "entry refuses use",
			pb: `name: p
phase: entry
steps:
  - check: test -f AGENTS.md
  - use: dev-task — the code bar
`,
			want: []string{"entry playbook", "step 2", `"use"`, "allowed kinds: check, ask, say, include"},
		},
		{
			name: "entry refuses do",
			pb: `name: p
phase: entry
steps:
  - check: test -f AGENTS.md
  - do: rm -rf build
`,
			want: []string{"step 2", "rm -rf build", `"do"`, "allowed kinds: check, ask, say, include"},
		},
		{
			name: "work refuses check",
			pb: `name: p
phase: work
steps:
  - say: read the diff
  - check: go test ./...
`,
			want: []string{"work playbook", "step 2", `"check"`, "allowed kinds: say, use, include"},
		},
		{
			name: "work refuses ask",
			pb: `name: p
phase: work
steps:
  - say: read the diff
  - ask: is the acceptance check stated?
`,
			want: []string{"step 2", "is the acceptance check stated?", `"ask"`, "allowed kinds: say, use, include"},
		},
		{
			name: "cap refuses check",
			pb: `name: p
phase: cap
steps:
  - say: hold the plan
  - check: true
`,
			want: []string{"cap playbook", `"check"`, "allowed kinds: say, include"},
		},
		{
			name: "exit refuses use",
			pb: `name: p
phase: exit
steps:
  - check: true
  - use: dev-task
`,
			want: []string{"exit playbook", `"use"`, "allowed kinds: check, ask, do, say, include"},
		},
		{
			name: "an unknown phase names the phases there are",
			pb: `name: p
phase: procedure
steps:
  - say: anything
`,
			want: []string{`"procedure"`, "phases are: cap, entry, work, exit"},
		},
		{
			name: "work refuses an empty use",
			pb: `name: p
phase: work
steps:
  - say: read the diff
  - use:
`,
			want: []string{"step 2", `"use"`, "empty body"},
		},
		{
			name: "exit refuses an empty do",
			pb: `name: p
phase: exit
steps:
  - check: true
  - do:
`,
			want: []string{"step 2", `"do"`, "empty body"},
		},
		{
			name: "every phase refuses an empty include",
			pb: `name: p
phase: work
steps:
  - say: read the diff
  - include:
`,
			want: []string{"step 2", `"include"`, "empty body"},
		},
		{
			name: "a playbook with no steps is not a protocol",
			pb: `name: p
phase: work
`,
			want: []string{"no steps"},
		},
		{
			name: "a playbook with no name declares nothing",
			pb: `phase: work
steps:
  - say: anything
`,
			want: []string{"must declare a name"},
		},
		{
			name: "an order that is not a number is refused",
			pb: `name: p
phase: work
order: soon
steps:
  - say: read the diff
`,
			want: []string{`"soon"`, "not a number"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kc, err := knowledge.Open(tempFS(t))
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

// `include` is legal in every phase, because it is spliced away before anything
// runs — and what it splices is held to the including playbook's phase by the
// validation that runs on the resolved result.
func TestIncludeIsAllowedInEveryPhase(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}

	const included = `name: shared-say
phase: work
steps:
  - say: a shared rule
`
	if err := kc.Add("shared-say", []byte(included)); err != nil {
		t.Fatalf("Add shared-say: %v", err)
	}

	for _, phase := range []string{knowledge.PhaseCap, knowledge.PhaseEntry, knowledge.PhaseWork, knowledge.PhaseExit} {
		t.Run(phase, func(t *testing.T) {
			name := "ph-" + phase
			body := "name: " + name + "\nphase: " + phase + "\nsteps:\n  - include: shared-say\n"
			if err := kc.Add(name, []byte(body)); err != nil {
				t.Fatalf("a %s playbook must accept an include: %v", phase, err)
			}
			pb, err := kc.Get(name)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			// The include is spliced away: the resolved playbook carries the
			// included step, not the include.
			if len(pb.Steps) != 1 || pb.Steps[0] != (knowledge.Step{Kind: knowledge.KindSay, Body: "a shared rule"}) {
				t.Errorf("resolved steps = %+v, want the spliced say", pb.Steps)
			}
		})
	}
}

// What an include splices must be legal for the playbook that spliced it, or a
// gate could act by including a playbook that acts.
func TestIncludeRefusesStepsIllegalForTheIncludingPhase(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}
	const acting = `name: acting
phase: exit
steps:
  - do: rm -rf build
`
	if err := kc.Add("acting", []byte(acting)); err != nil {
		t.Fatalf("Add acting: %v", err)
	}
	const gate = `name: gate
phase: entry
steps:
  - check: true
  - include: acting
`
	if err := kc.Add("gate", []byte(gate)); err != nil {
		t.Fatalf("Add: %v", err)
	}

	_, err = kc.Get("gate")
	if err == nil {
		t.Fatal("a gate that includes an action must be refused")
	}
	for _, want := range []string{"step 2", `"do"`, "allowed kinds: check, ask, say, include"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must contain %q", err, want)
		}
	}
}

// The include resolution carries the including playbook's position, and the
// step index is the resolved one — so a spliced step is addressed the same way
// as a written one.
func TestIncludeSplicesInPlace(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}
	const middle = `name: middle
phase: work
steps:
  - say: spliced one
  - say: spliced two
`
	if err := kc.Add("middle", []byte(middle)); err != nil {
		t.Fatal(err)
	}
	const outer = `name: outer
phase: work
steps:
  - say: before
  - include: middle
  - say: after
`
	if err := kc.Add("outer", []byte(outer)); err != nil {
		t.Fatalf("Add: %v", err)
	}

	pb, err := kc.Get("outer")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var bodies []string
	for _, step := range pb.Steps {
		bodies = append(bodies, step.Body)
	}
	want := []string{"before", "spliced one", "spliced two", "after"}
	if !reflect.DeepEqual(bodies, want) {
		t.Errorf("steps = %v, want the splice at the include's position", bodies)
	}
}

// An include that reaches itself is refused by name rather than recursed into.
func TestIncludeCycleIsRefusedNamingTheChain(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}
	const loop = `name: loop
phase: work
steps:
  - say: first
  - include: loop
`
	if err := kc.Add("loop", []byte(loop)); err != nil {
		t.Fatalf("Add: %v", err)
	}

	_, err = kc.Get("loop")
	if err == nil {
		t.Fatal("an include cycle must be refused, not recursed into")
	}
	if !strings.Contains(err.Error(), "cycle") || !strings.Contains(err.Error(), "loop -> loop") {
		t.Errorf("error %q must name the cycle", err)
	}
}

// A mutual include is a cycle too, and the refusal names the whole chain.
func TestIncludeMutualCycleIsRefused(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, other := range map[string]string{"a": "b", "b": "a"} {
		body := "name: " + name + "\nphase: work\nsteps:\n  - say: x\n  - include: " + other + "\n"
		if err := kc.Add(name, []byte(body)); err != nil {
			t.Fatalf("Add %s: %v", name, err)
		}
	}

	_, err = kc.Get("a")
	if err == nil {
		t.Fatal("a mutual include must be refused")
	}
	if !strings.Contains(err.Error(), "a -> b -> a") {
		t.Errorf("error %q must name the whole chain", err)
	}
}

// A project playbook with the same name replaces the global one, and an include
// resolves under the same rule — so a project `code-exit` that includes
// `code-exit` gets the global one rather than itself.
func TestProjectScopeShadowsTheGlobalOne(t *testing.T) {
	fsRoot := tempFS(t)
	global, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := global.Add("code-exit", []byte(`name: code-exit
phase: exit
steps:
  - say: the global teardown
`)); err != nil {
		t.Fatal(err)
	}

	project := global.InProject("skills")
	if err := project.Add("code-exit", []byte(`name: code-exit
phase: exit
steps:
  - say: this project's teardown
  - include: code-exit
`)); err != nil {
		t.Fatalf("a project playbook must be writable: %v", err)
	}

	pb, err := project.Get("code-exit")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var bodies []string
	for _, step := range pb.Steps {
		bodies = append(bodies, step.Body)
	}
	// The project's say first, then the global playbook's — the include saw the
	// playbook beneath it, not itself.
	want := []string{"this project's teardown", "the global teardown"}
	if !reflect.DeepEqual(bodies, want) {
		t.Errorf("steps = %v, want %v", bodies, want)
	}

	// The global center is unaffected, and the file really is in the project's
	// own directory.
	globalPB, err := global.Get("code-exit")
	if err != nil {
		t.Fatalf("global Get: %v", err)
	}
	if len(globalPB.Steps) != 1 || globalPB.Steps[0].Body != "the global teardown" {
		t.Errorf("the global playbook was shadowed in its own scope: %+v", globalPB.Steps)
	}
	if _, err := os.Stat(projectKBPath(fsRoot, "skills", "code-exit")); err != nil {
		t.Errorf("the project playbook is not in the project's KB: %v", err)
	}

	// The list reports the name once, as the project's.
	names, err := project.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "code-exit" {
		t.Errorf("names = %v, want the shadowed name listed once", names)
	}
}

// A project with no KB of its own behaves exactly as global-only: absence is
// never an error.
func TestProjectWithoutAKBReadsTheGlobalOne(t *testing.T) {
	fsRoot := tempFS(t)
	global, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := global.Add("code-entry", []byte(samplePlaybook)); err != nil {
		t.Fatal(err)
	}

	project := global.InProject("no-such-project")
	if !project.Has("code-entry") {
		t.Error("a project with no KB must still see the global playbook")
	}
	pb, err := project.Get("code-entry")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if pb.Name != "code-entry" {
		t.Errorf("got %q, want the global playbook", pb.Name)
	}
}

// Has distinguishes an absent playbook, which a gate treats as no gate, from one
// that is present but unreadable, which a gate must refuse rather than skip.
func TestHasDistinguishesAbsentFromBroken(t *testing.T) {
	fsRoot := tempFS(t)
	kc, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}

	if kc.Has("missing") {
		t.Error("Has must be false for a playbook that is not there")
	}

	// Written by hand, bypassing the write path: present, but not a playbook.
	if err := os.WriteFile(kbPath(fsRoot, "broken"), []byte("name: broken\nphase: nonsense\nsteps:\n  - say: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !kc.Has("broken") {
		t.Error("Has must be true for a file that is there, whether or not it parses")
	}
	if _, err := kc.Get("broken"); err == nil {
		t.Error("Get must refuse a present but illegal playbook")
	}
}

// A deflected field is refused with the replacement named, so a playbook from
// the old schema says how to migrate instead of half-working.
func TestRemovedFieldsAreRefusedNamingTheReplacement(t *testing.T) {
	cases := []struct {
		name string
		pb   string
		want []string
	}{
		{
			name: "trigger",
			pb:   "name: p\nphase: cap\ntrigger: cap\nsteps:\n  - say: x\n",
			want: []string{"removed field `trigger: cap`", "the replacement is `phase: cap`"},
		},
		{
			name: "engine_scope",
			pb:   "name: p\nphase: cap\nengine_scope: herdr\nsteps:\n  - say: x\n",
			want: []string{"removed field `engine_scope: herdr`", "applies_when: engine=herdr"},
		},
		{
			name: "type",
			pb:   "name: p\ntype: prerequisite\nsteps:\n  - check: true\n",
			want: []string{"removed field `type: prerequisite`", "`phase`", "`entry`", "`exit`"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kc, err := knowledge.Open(tempFS(t))
			if err != nil {
				t.Fatal(err)
			}
			err = kc.Add("p", []byte(tc.pb))
			if err == nil {
				t.Fatal("a playbook carrying a removed field must be refused")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q must contain %q", err, want)
				}
			}
			if !strings.Contains(err.Error(), "fs kb remove p") {
				t.Errorf("error %q must say how to clear the old file", err)
			}
		})
	}
}

// The retained fields, including the two kinds the schema gained, are checked
// from the other side: a legal playbook of every phase is accepted.
func TestEveryPhaseAcceptsItsOwnKinds(t *testing.T) {
	cases := map[string]string{
		knowledge.PhaseCap:   "name: p\nphase: cap\nsteps:\n  - say: hold the plan\n",
		knowledge.PhaseEntry: "name: p\nphase: entry\nsteps:\n  - check: true\n  - ask: is it stated\n  - say: context\n",
		knowledge.PhaseWork:  "name: p\nphase: work\nsteps:\n  - say: do the work\n  - use: dev-task — the code bar\n",
		knowledge.PhaseExit:  "name: p\nphase: exit\nsteps:\n  - check: true\n  - ask: is it reviewed\n  - do: rm -rf build\n  - say: done\n",
	}
	for phase, pb := range cases {
		t.Run(phase, func(t *testing.T) {
			kc, err := knowledge.Open(tempFS(t))
			if err != nil {
				t.Fatal(err)
			}
			if err := kc.Add("p", []byte(pb)); err != nil {
				t.Fatalf("a legal %s playbook must be accepted: %v", phase, err)
			}
		})
	}
}

// Validation happens on the read path too: Get returns the playbook the gate
// will run, and a file that is not legal for its phase is refused here rather
// than running half its steps. (The write path is the one that keeps such a file
// off disk in the first place; this is the path a hand-edited file meets.)
func TestGetRefusesAPlaybookThatIsNotLegalForItsPhase(t *testing.T) {
	fsRoot := tempFS(t)
	kc, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(kbPath(fsRoot, "hand-edited"),
		[]byte("name: hand-edited\nphase: entry\nsteps:\n  - do: rm -rf build\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = kc.Get("hand-edited")
	if err == nil {
		t.Fatal("Get must refuse a playbook whose steps are illegal for its phase")
	}
	if !strings.Contains(err.Error(), `"do"`) || !strings.Contains(err.Error(), "allowed kinds") {
		t.Errorf("error %q must name the kind and the allowed set", err)
	}
}

// Matches is presence and equality, and nothing else: no ranges, no negation.
func TestMatches(t *testing.T) {
	tags := map[string]any{"type": "dev-task", "engine": "herdr", "code": true}

	cases := []struct {
		when []string
		want bool
	}{
		{nil, true},
		{[]string{"code"}, true},
		{[]string{"type=dev-task"}, true},
		{[]string{"type=parallel"}, false},
		{[]string{"engine=herdr"}, true},
		{[]string{"engine=other"}, false},
		{[]string{"code", "type=dev-task"}, true},
		{[]string{"code", "type=parallel"}, false},
		{[]string{"absent"}, false},
		{[]string{"absent", "code"}, false},
	}
	for _, tc := range cases {
		pb := &knowledge.Playbook{Name: "p", Phase: knowledge.PhaseWork, AppliesWhen: tc.when}
		if got := pb.Matches(tags); got != tc.want {
			t.Errorf("Matches(%v) = %v, want %v", tc.when, got, tc.want)
		}
	}
}

// A trailing comma in applies_when is dropped rather than kept as a term
// nothing can match — the difference between a typo and an inert playbook.
func TestAppliesWhenDropsEmptyTerms(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := kc.Add("p", []byte("name: p\nphase: work\napplies_when: code, ,\nsteps:\n  - say: x\n")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	pb, _ := kc.Get("p")
	if got := pb.AppliesWhenString(); got != "code" {
		t.Errorf("applies_when = %q, want the one term", got)
	}
}

// MarshalJSON renders the playbook the way its file spells it, so `fs kb get`
// shows the schema a reader edits rather than the resolver's internals.
func TestMarshalJSONShowsTheFileSchema(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := kc.Add("code-entry", []byte(samplePlaybook)); err != nil {
		t.Fatal(err)
	}
	pb, _ := kc.Get("code-entry")

	encoded, err := pb.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	for _, want := range []string{`"name":"code-entry"`, `"phase":"entry"`, `"applies_when":"code"`, `"order":10`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("json %s must carry %s", encoded, want)
		}
	}
}
