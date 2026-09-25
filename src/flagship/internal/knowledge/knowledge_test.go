package knowledge_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/knowledge"
)

const samplePlaybook = `name: code-entry
applies_when: code
order: 10
entry:
  - check: test -f AGENTS.md
  - ask: is the spec locked?
  - check: grep -rl 'specs:locked' .
`

const samplePlaybook2 = `name: code-review
applies_when: code
order: 50
work:
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
	if !pb.Serves(knowledge.MomentEntry) {
		t.Errorf("moments: %v", pb.Moments())
	}
	if got := pb.AppliesWhenString(); got != "code" {
		t.Errorf("applies_when: %s", got)
	}
	if pb.Order != 10 {
		t.Errorf("order: %d", pb.Order)
	}
	steps := pb.StepsFor(knowledge.MomentEntry)
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	if steps[0] != (knowledge.Step{Kind: knowledge.KindCheck, Body: "test -f AGENTS.md"}) {
		t.Errorf("step 0: %+v", steps[0])
	}
	if steps[1].Kind != knowledge.KindAsk {
		t.Errorf("step 1 kind: %s", steps[1].Kind)
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
work:
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
	steps := pb.StepsFor(knowledge.MomentEntry)
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps after edit, got %d", len(steps))
	}
	if steps[2].Body != "go vet ./..." {
		t.Errorf("last step = %+v, want the appended check", steps[2])
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
work:
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
	if got := pb.StepsFor(knowledge.MomentWork); !reflect.DeepEqual(got, want) {
		t.Errorf("steps = %+v, want %+v", got, want)
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
entry:
  - check: test -f AGENTS.md
  - use: dev-task — the code bar
`,
			want: []string{"entry playbook", "step 2", `"use"`, "allowed kinds: check, ask, say, include"},
		},
		{
			name: "entry refuses do",
			pb: `name: p
entry:
  - check: test -f AGENTS.md
  - do: rm -rf build
`,
			want: []string{"step 2", "rm -rf build", `"do"`, "allowed kinds: check, ask, say, include"},
		},
		{
			name: "work refuses check",
			pb: `name: p
work:
  - say: read the diff
  - check: go test ./...
`,
			want: []string{"work playbook", "step 2", `"check"`, "allowed kinds: say, use, include"},
		},
		{
			name: "work refuses ask",
			pb: `name: p
work:
  - say: read the diff
  - ask: is the acceptance check stated?
`,
			want: []string{"step 2", "is the acceptance check stated?", `"ask"`, "allowed kinds: say, use, include"},
		},
		{
			name: "cap refuses check",
			pb: `name: p
cap:
  - say: hold the plan
  - check: true
`,
			want: []string{"cap playbook", `"check"`, "allowed kinds: say, include"},
		},
		{
			name: "exit refuses use",
			pb: `name: p
exit:
  - check: true
  - use: dev-task
`,
			want: []string{"exit playbook", `"use"`, "allowed kinds: check, ask, do, say, include"},
		},
		{
			name: "work refuses an empty use",
			pb: `name: p
work:
  - say: read the diff
  - use:
`,
			want: []string{"step 2", `"use"`, "empty body"},
		},
		{
			name: "exit refuses an empty do",
			pb: `name: p
exit:
  - check: true
  - do:
`,
			want: []string{"step 2", `"do"`, "empty body"},
		},
		{
			name: "every phase refuses an empty include",
			pb: `name: p
work:
  - say: read the diff
  - include:
`,
			want: []string{"step 2", `"include"`, "empty body"},
		},
		{
			name: "a playbook with no steps is not a protocol",
			pb: `name: p
`,
			want: []string{"no steps"},
		},
		{
			name: "a playbook with no name declares nothing",
			pb: `work:
  - say: anything
`,
			want: []string{"must declare a name"},
		},
		{
			name: "an order that is not a number is refused",
			pb: `name: p
order: soon
work:
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
work:
  - say: a shared rule
`
	if err := kc.Add("shared-say", []byte(included)); err != nil {
		t.Fatalf("Add shared-say: %v", err)
	}

	for _, phase := range []string{knowledge.MomentCap, knowledge.MomentEntry, knowledge.MomentWork, knowledge.MomentExit} {
		t.Run(phase, func(t *testing.T) {
			name := "ph-" + phase
			body := "name: " + name + "\n" + phase + ":\n  - include: shared-say\n"
			if err := kc.Add(name, []byte(body)); err != nil {
				t.Fatalf("a %s playbook must accept an include: %v", phase, err)
			}
			pb, err := kc.Get(name)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			// The include is spliced away: the resolved playbook carries the
			// included step, not the include.
			if got := pb.StepsFor(phase); len(got) != 1 || got[0] != (knowledge.Step{Kind: knowledge.KindSay, Body: "a shared rule"}) {
				t.Errorf("resolved steps = %+v, want the spliced say", got)
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
exit:
  - do: rm -rf build
`
	if err := kc.Add("acting", []byte(acting)); err != nil {
		t.Fatalf("Add acting: %v", err)
	}
	const gate = `name: gate
entry:
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
work:
  - say: spliced one
  - say: spliced two
`
	if err := kc.Add("middle", []byte(middle)); err != nil {
		t.Fatal(err)
	}
	const outer = `name: outer
work:
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
	for _, step := range pb.StepsFor(knowledge.MomentWork) {
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
work:
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
		body := "name: " + name + "\nwork:\n  - say: x\n  - include: " + other + "\n"
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

// A project playbook with the same name is the one a lookup resolves to, and an
// include resolves under the same rule — so a project `code-exit` that includes
// `code-exit` gets the global one rather than itself.
func TestProjectScopeShadowsTheGlobalOne(t *testing.T) {
	fsRoot := tempFS(t)
	global, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := global.Add("code-exit", []byte(`name: code-exit
exit:
  - say: the global teardown
`)); err != nil {
		t.Fatal(err)
	}

	project := global.InProject("skills")
	if err := project.Add("code-exit", []byte(`name: code-exit
exit:
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
	for _, step := range pb.StepsFor(knowledge.MomentExit) {
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
	if got := globalPB.StepsFor(knowledge.MomentExit); len(got) != 1 || got[0].Body != "the global teardown" {
		t.Errorf("the global playbook was shadowed in its own scope: %+v", got)
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
	if err := os.WriteFile(kbPath(fsRoot, "broken"), []byte("name: broken\nnonsense:\n  - say: x\n"), 0o644); err != nil {
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
			pb:   "name: p\ntrigger: cap\ncap:\n  - say: x\n",
			want: []string{"removed field `trigger: cap`", "the replacement is the `cap:` section"},
		},
		{
			name: "engine_scope",
			pb:   "name: p\nengine_scope: herdr\ncap:\n  - say: x\n",
			want: []string{"removed field `engine_scope: herdr`", "`engine: herdr`", "applies_when: engine=herdr", "match an engine"},
		},
		{
			name: "type",
			pb:   "name: p\ntype: prerequisite\nentry:\n  - check: true\n",
			want: []string{"removed field `type: prerequisite`", "`entry:`", "`exit:`", "applies_when"},
		},
		{
			name: "phase",
			pb:   "name: p\nphase: entry\nsteps:\n  - check: true\n",
			want: []string{"removed field `phase: entry`", "the replacement is the `entry:` section"},
		},
		{
			name: "steps",
			pb:   "name: p\nsteps:\n  - check: true\n",
			want: []string{"removed field `steps`", "one section per moment"},
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
		knowledge.MomentCap:   "name: p\ncap:\n  - say: hold the plan\n",
		knowledge.MomentEntry: "name: p\nentry:\n  - check: true\n  - ask: is it stated\n  - say: context\n",
		knowledge.MomentWork:  "name: p\nwork:\n  - say: do the work\n  - use: dev-task — the code bar\n",
		knowledge.MomentExit:  "name: p\nexit:\n  - check: true\n  - ask: is it reviewed\n  - do: rm -rf build\n  - say: done\n",
	}
	for moment, pb := range cases {
		t.Run(moment, func(t *testing.T) {
			kc, err := knowledge.Open(tempFS(t))
			if err != nil {
				t.Fatal(err)
			}
			if err := kc.Add("p", []byte(pb)); err != nil {
				t.Fatalf("a legal %s playbook must be accepted: %v", moment, err)
			}
		})
	}
}

// engine: and agent: are parsed and resolved, and each must be a single
// non-empty token when present — a name, not an expression.
func TestEngineAndAgentAreParsedAndValidated(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := kc.Add("worker", []byte("name: worker\nengine: herdr\nagent: pi\nwork:\n  - say: x\n")); err != nil {
		t.Fatalf("a playbook declaring engine and agent must be accepted: %v", err)
	}
	got, err := kc.Get("worker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Engine != "herdr" || got.Agent != "pi" {
		t.Errorf("engine/agent = %q/%q, want herdr/pi", got.Engine, got.Agent)
	}

	for _, tc := range []struct{ field, val string }{
		{"engine", "her dr"},
		{"agent", "pi=1"},
		{"engine", ""},
	} {
		body := "name: worker\n" + tc.field + ": " + tc.val + "\nwork:\n  - say: x\n"
		if err := kc.Add("bad-"+tc.field, []byte(body)); err == nil {
			t.Errorf("%s %q must be refused as not a single non-empty token", tc.field, tc.val)
		}
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
		[]byte("name: hand-edited\nentry:\n  - do: rm -rf build\n"), 0o644); err != nil {
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
		pb := &knowledge.Playbook{Name: "p", AppliesWhen: tc.when}
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
	if err := kc.Add("p", []byte("name: p\napplies_when: code, ,\nwork:\n  - say: x\n")); err != nil {
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
	for _, want := range []string{`"name":"code-entry"`, `"entry":[`, `"applies_when":"code"`, `"order":10`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("json %s must carry %s", encoded, want)
		}
	}
}

// The two silent drops the schema's parser used to have, characterised before
// they were closed. A key the switch did not know was ignored, so `agent_arg:`
// produced a playbook that read as configured; and a `- ` item outside a moment
// section was discarded, so `agent_args:` written as a block list parsed as no
// arguments at all — which is the exact symptom the field exists to fix.
func TestAFieldTheSchemaDoesNotKnowIsRefused(t *testing.T) {
	cases := []struct{ name, pb, key string }{
		{"a near-miss spelling", "name: p\nagent_arg: --permission-mode\nwork:\n  - say: x\n", "`agent_arg`"},
		{"a case the parser does not fold", "name: p\nAgentArgs: --permission-mode\nwork:\n  - say: x\n", "`AgentArgs`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kc, err := knowledge.Open(tempFS(t))
			if err != nil {
				t.Fatal(err)
			}
			err = kc.Add("p", []byte(tc.pb))
			if err == nil {
				t.Fatal("a field the schema does not know must be refused, not ignored")
			}
			for _, want := range []string{tc.key, "the schema is:", "agent_args"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q must contain %q", err, want)
				}
			}
		})
	}
}

// The block-list form is the mistake the field must not reproduce: the parser is
// line-oriented with `moment` as its only state, so a `- ` item under a
// non-moment field went nowhere. It is refused, naming the comma-separated form
// to write instead.
func TestABlockListOutsideAMomentSectionIsRefused(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}
	err = kc.Add("p", []byte("name: p\nagent_args:\n  - --permission-mode\n  - auto\nwork:\n  - say: x\n"))
	if err == nil {
		t.Fatal("a block list outside a moment section must be refused, not dropped")
	}
	for _, want := range []string{"agent_args", "comma-separated", "--permission-mode, auto"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must contain %q", err, want)
		}
	}
}

// agent_args is parsed the way applies_when is — comma-separated terms on the
// field's own line, empties dropped, so a trailing comma is not a term. Its
// terms are deliberately not held to the shape engine and agent are: `--add-dir
// /tmp/x` holds a slash and a space, and both are legal argv. This is the proof
// the two validators are different on purpose.
func TestAgentArgsAreParsedAsCommaSeparatedArgvElements(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}
	body := "name: worker\nengine: herdr\nagent: claude\n" +
		"agent_args: --permission-mode, auto, --add-dir /tmp/x,\nwork:\n  - say: x\n"
	if err := kc.Add("worker", []byte(body)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	pb, err := kc.Get("worker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := []string{"--permission-mode", "auto", "--add-dir /tmp/x"}
	if !reflect.DeepEqual(pb.AgentArgs, want) {
		t.Errorf("agent_args = %q, want %q", pb.AgentArgs, want)
	}
}

// `fs kb get` renders the playbook the way its file spells it, so the
// realization has a parallel field list in MarshalJSON — a field missing from it
// is one a reader of kb get never sees.
func TestMarshalJSONCarriesAgentArgs(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}
	body := "name: worker\nengine: herdr\nagent: claude\nagent_args: --permission-mode, auto\nwork:\n  - say: x\n"
	if err := kc.Add("worker", []byte(body)); err != nil {
		t.Fatal(err)
	}
	pb, _ := kc.Get("worker")
	encoded, err := pb.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	for _, want := range []string{`"agent":"claude"`, `"agent_args":"--permission-mode, auto"`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("json %s must carry %s", encoded, want)
		}
	}
}

// A present-but-empty field is refused, as `engine:`, `agent:` and `creates:`
// are — and a value that is nothing but separators is the same field saying the
// same nothing.
func TestAnEmptyAgentArgsIsRefused(t *testing.T) {
	for _, pb := range []string{
		"name: p\nagent_args:\nwork:\n  - say: x\n",
		"name: p\nagent_args: , ,\nwork:\n  - say: x\n",
	} {
		kc, err := knowledge.Open(tempFS(t))
		if err != nil {
			t.Fatal(err)
		}
		if err := kc.Add("p", []byte(pb)); err == nil {
			t.Errorf("an empty agent_args must be refused:\n%s", pb)
		}
	}
}

// The tolerated shapes stay tolerated. A `---` document start and a line with no
// colon are not fields, so refusing an unknown field must not refuse them.
func TestLinesThatAreNotFieldsStayTolerated(t *testing.T) {
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}
	body := "---\nname: p\nthis line has no colon\nwork:\n  - say: x\n"
	if err := kc.Add("p", []byte(body)); err != nil {
		t.Fatalf("a document start and a colonless line must stay tolerated: %v", err)
	}
}

// The moment vocabulary is closed, and Validate is the backstop for a playbook
// constructed with a moment that is not one of the four: a parser never
// produces one, but a resolved playbook is held to the same table the write
// path is.
func TestValidateRefusesAnUnknownMoment(t *testing.T) {
	pb := &knowledge.Playbook{
		Name: "p",
		Sections: []knowledge.Section{{
			Moment: "procedure",
			Steps:  []knowledge.Step{{Kind: knowledge.KindSay, Body: "anything"}},
		}},
	}
	err := knowledge.Validate(pb)
	if err == nil {
		t.Fatal("a playbook with an unknown moment must be refused")
	}
	for _, want := range []string{`"procedure"`, "moments are: cap, entry, work, exit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must contain %q", err, want)
		}
	}
}

// stepBodies lists one moment's step bodies, the shape a reader of a resolved
// playbook cares about.
func stepBodies(pb *knowledge.Playbook, moment string) []string {
	var bodies []string
	for _, step := range pb.StepsFor(moment) {
		bodies = append(bodies, step.Body)
	}
	return bodies
}

// writePlaybookFile places a playbook by hand, the way the operator writes a
// project shadow — Add refuses a file with no steps of its own, which is the
// other half of the same stance the read path has to hold.
func writePlaybookFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A project playbook inherits every field it does not declare from the
// same-named playbook in the scope beneath it, and keeps the ones it does.
// This is the one-edit case: the shadow names the field it is for and nothing
// else, so changing the dispatch engine is a single line in one file.
func TestShadowInheritsTheFieldsItDoesNotDeclare(t *testing.T) {
	fsRoot := tempFS(t)
	global, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := global.Add("worker", []byte(`name: worker
applies_when: code
order: 10
engine: herdr
agent: pi
creates: batch
work:
  - say: the global brief
`)); err != nil {
		t.Fatal(err)
	}

	project := global.InProject("skills")
	if err := project.Add("worker", []byte("name: worker\nagent: claude\n")); err != nil {
		t.Fatalf("a shadow declaring only the field it changes must be writable: %v", err)
	}

	pb, err := project.Get("worker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if pb.Agent != "claude" {
		t.Errorf("agent = %q, want the declared one", pb.Agent)
	}
	for _, tc := range []struct{ field, got, want string }{
		{"engine", pb.Engine, "herdr"},
		{"creates", pb.Creates, "batch"},
		{"applies_when", pb.AppliesWhenString(), "code"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want it inherited as %q", tc.field, tc.got, tc.want)
		}
	}
	if pb.Order != 10 {
		t.Errorf("order = %d, want it inherited as 10", pb.Order)
	}
	if got := stepBodies(pb, knowledge.MomentWork); !reflect.DeepEqual(got, []string{"the global brief"}) {
		t.Errorf("work steps = %v, want the inherited section", got)
	}

	// The global center is unaffected: inheritance reads downwards, nothing
	// travels up.
	globalPB, err := global.Get("worker")
	if err != nil {
		t.Fatalf("global Get: %v", err)
	}
	if globalPB.Agent != "pi" || globalPB.Engine != "herdr" {
		t.Errorf("the global playbook changed in its own scope: agent=%q engine=%q",
			globalPB.Agent, globalPB.Engine)
	}
}

// A shadow that declares a section keeps it — inheritance fills gaps, it does
// not merge on top of what the file already says.
func TestShadowKeepingASectionDoesNotInheritIt(t *testing.T) {
	fsRoot := tempFS(t)
	global, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := global.Add("worker", []byte("name: worker\nengine: herdr\nagent: pi\nwork:\n  - say: the global brief\n")); err != nil {
		t.Fatal(err)
	}

	project := global.InProject("skills")
	if err := project.Add("worker", []byte("name: worker\nagent: claude\nwork:\n  - say: this project's brief\n")); err != nil {
		t.Fatal(err)
	}

	pb, err := project.Get("worker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := stepBodies(pb, knowledge.MomentWork); !reflect.DeepEqual(got, []string{"this project's brief"}) {
		t.Errorf("work steps = %v, want the declared section alone", got)
	}
	if pb.Engine != "herdr" {
		t.Errorf("engine = %q, want it still inherited", pb.Engine)
	}
}

// include: and inheritance are two mechanisms over the same scope chain, and
// conflating them double-splices. This is the shape every shadow on the machine
// already has, so getting it wrong duplicates every worker's standing
// obligations — and the duplicate is silent, which is why the count is asserted
// and not just the presence.
func TestShadowIncludingItsOwnNameSplicesTheGlobalStepsOnce(t *testing.T) {
	fsRoot := tempFS(t)
	global, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := global.Add("worker", []byte("name: worker\norder: 10\nengine: herdr\nagent: pi\nwork:\n  - say: one\n  - say: two\n")); err != nil {
		t.Fatal(err)
	}

	project := global.InProject("skills")
	if err := project.Add("worker", []byte("name: worker\nagent: claude\nwork:\n  - include: worker\n")); err != nil {
		t.Fatal(err)
	}

	pb, err := project.Get("worker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got := stepBodies(pb, knowledge.MomentWork)
	if !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Errorf("work steps = %v (%d), want the global steps exactly once (2)", got, len(got))
	}
	if pb.Engine != "herdr" {
		t.Errorf("engine = %q, want it inherited", pb.Engine)
	}
	if pb.Agent != "claude" {
		t.Errorf("agent = %q, want the declared one", pb.Agent)
	}
}

// A two-level chain resolves: the shadow takes its fields from the global
// playbook, whose work section is itself a splice of a third file. An inherited
// section arrives resolved, so its include is not spliced a second time.
func TestTwoLevelChainResolves(t *testing.T) {
	fsRoot := tempFS(t)
	global, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := global.Add("brief", []byte("name: brief\nwork:\n  - say: from brief\n")); err != nil {
		t.Fatal(err)
	}
	if err := global.Add("worker", []byte(`name: worker
order: 10
engine: herdr
agent_args: --permission-mode, auto
work:
  - include: brief
`)); err != nil {
		t.Fatal(err)
	}

	project := global.InProject("skills")
	if err := project.Add("worker", []byte("name: worker\nagent: claude\n")); err != nil {
		t.Fatal(err)
	}

	pb, err := project.Get("worker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if pb.Agent != "claude" {
		t.Errorf("agent = %q, want the declared one", pb.Agent)
	}
	if pb.Engine != "herdr" {
		t.Errorf("engine = %q, want it inherited through the chain", pb.Engine)
	}
	if !reflect.DeepEqual(pb.AgentArgs, []string{"--permission-mode", "auto"}) {
		t.Errorf("agent_args = %v, want them inherited through the chain", pb.AgentArgs)
	}
	if got := stepBodies(pb, knowledge.MomentWork); !reflect.DeepEqual(got, []string{"from brief"}) {
		t.Errorf("work steps = %v, want the spliced steps once", got)
	}
}

// Absent and declared-empty are different. `applies_when:` with no terms is a
// deliberate standing declaration, so it wins over the scope below; the key
// being absent inherits. Nothing here is inferable from zero values, which is
// why parse records presence instead.
func TestDeclaredEmptyAppliesWhenIsStandingNotAbsent(t *testing.T) {
	fsRoot := tempFS(t)
	global, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := global.Add("worker", []byte("name: worker\napplies_when: type=batch\nengine: herdr\nwork:\n  - say: x\n")); err != nil {
		t.Fatal(err)
	}
	if err := global.Add("standing", []byte("name: standing\napplies_when: type=batch\nengine: herdr\nwork:\n  - say: x\n")); err != nil {
		t.Fatal(err)
	}

	project := global.InProject("skills")
	writePlaybookFile(t, projectKBPath(fsRoot, "skills", "standing"), "name: standing\napplies_when:\nwork:\n  - say: y\n")
	writePlaybookFile(t, projectKBPath(fsRoot, "skills", "worker"), "name: worker\nwork:\n  - say: y\n")

	// The declared-empty one is standing, and does not take type=batch.
	standing, err := project.Get("standing")
	if err != nil {
		t.Fatalf("Get standing: %v", err)
	}
	if got := standing.AppliesWhenString(); got != "" {
		t.Errorf("applies_when = %q, want the declared empty value to win as standing", got)
	}
	if standing.Engine != "herdr" {
		t.Errorf("engine = %q, want it inherited", standing.Engine)
	}

	// The absent one inherits the terms.
	pb, err := project.Get("worker")
	if err != nil {
		t.Fatalf("Get worker: %v", err)
	}
	if got := pb.AppliesWhenString(); got != "type=batch" {
		t.Errorf("applies_when = %q, want it inherited as type=batch", got)
	}
}

// An undeclared field with no counterpart below is simply absent — absence is
// the global-only case, never an error and never a refusal.
func TestAnAbsentFieldWithNoCounterpartIsSimplyAbsent(t *testing.T) {
	fsRoot := tempFS(t)
	global, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := global.Add("worker", []byte("name: worker\nwork:\n  - say: x\n")); err != nil {
		t.Fatal(err)
	}

	project := global.InProject("skills")
	if err := project.Add("worker", []byte("name: worker\norder: 3\nwork:\n  - say: y\n")); err != nil {
		t.Fatal(err)
	}

	pb, err := project.Get("worker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if pb.Engine != "" || pb.Agent != "" || pb.Creates != "" {
		t.Errorf("undeclared fields with no counterpart must stay absent: engine=%q agent=%q creates=%q",
			pb.Engine, pb.Agent, pb.Creates)
	}
	if pb.Order != 3 {
		t.Errorf("order = %d, want the declared 3", pb.Order)
	}
}

// A shadow that declares nothing but its name would inherit every field and
// section and change nothing, while reading as a deliberate override. Refused,
// naming that it changes nothing — the same stance as the unknown-key refusal.
func TestShadowThatChangesNothingIsRefused(t *testing.T) {
	fsRoot := tempFS(t)
	global, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := global.Add("worker", []byte("name: worker\nengine: herdr\nagent: pi\nwork:\n  - say: x\n")); err != nil {
		t.Fatal(err)
	}

	project := global.InProject("skills")
	writePlaybookFile(t, projectKBPath(fsRoot, "skills", "worker"), "name: worker\n")

	_, err = project.Get("worker")
	if err == nil {
		t.Fatal("a shadow that declares nothing must be refused")
	}
	if !strings.Contains(err.Error(), "changes nothing") {
		t.Errorf("error %q must say the shadow changes nothing", err)
	}

	// The write path refuses it too, rather than installing a file that cannot
	// be read back.
	if err := project.Add("worker", []byte("name: worker\n")); err == nil {
		t.Error("Add must refuse a shadow that declares nothing")
	}
}

// `fs kb get` shows the resolved playbook, so a reader must be able to tell
// what the file declared from what it inherited; without that, a shadow stops
// being self-describing.
func TestKbGetMarksTheInheritedFields(t *testing.T) {
	fsRoot := tempFS(t)
	global, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := global.Add("worker", []byte(`name: worker
applies_when: code
order: 10
engine: herdr
creates: batch
work:
  - say: the global brief
`)); err != nil {
		t.Fatal(err)
	}

	project := global.InProject("skills")
	if err := project.Add("worker", []byte("name: worker\nagent: claude\n")); err != nil {
		t.Fatal(err)
	}

	pb, err := project.Get("worker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	out, err := json.Marshal(pb)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	var inherited []string
	for _, name := range got["inherited"].([]any) {
		inherited = append(inherited, name.(string))
	}
	want := []string{"applies_when", "order", "engine", "creates", "work"}
	if !reflect.DeepEqual(inherited, want) {
		t.Errorf("inherited = %v, want %v", inherited, want)
	}

	// A playbook read from one file with nothing below it marks nothing.
	globalPB, err := global.Get("worker")
	if err != nil {
		t.Fatalf("global Get: %v", err)
	}
	out, err = json.Marshal(globalPB)
	if err != nil {
		t.Fatal(err)
	}
	got = map[string]any{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if _, present := got["inherited"]; present {
		t.Errorf("a playbook with nothing below it marked %v as inherited", got["inherited"])
	}
}

// MarshalJSON keeps a second, parallel field list — the struct it renders. This
// proves the two agree: a schema field missing from the output would inherit
// silently and be marked as neither declared nor inherited, which is invisible
// in `fs kb get`, and an output key outside the schema is a name nothing can
// inherit.
func TestTheOutputSchemaAndTheInheritableVocabularyAgree(t *testing.T) {
	// Every field and every moment declared, so nothing is omitted from the
	// output and the key set is the whole schema.
	full := &knowledge.Playbook{
		Name:        "p",
		AppliesWhen: []string{"code"},
		Order:       10,
		Engine:      "herdr",
		Agent:       "pi",
		AgentArgs:   []string{"--permission-mode", "auto"},
		Creates:     "batch",
		Sections: []knowledge.Section{
			{Moment: knowledge.MomentCap, Steps: []knowledge.Step{{Kind: knowledge.KindSay, Body: "a"}}},
			{Moment: knowledge.MomentEntry, Steps: []knowledge.Step{{Kind: knowledge.KindCheck, Body: "b"}}},
			{Moment: knowledge.MomentWork, Steps: []knowledge.Step{{Kind: knowledge.KindSay, Body: "c"}}},
			{Moment: knowledge.MomentExit, Steps: []knowledge.Step{{Kind: knowledge.KindSay, Body: "d"}}},
		},
	}
	bare, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(bare, &got); err != nil {
		t.Fatal(err)
	}

	// `inherited` is the one key the output adds to the schema; `name` is the
	// playbook's identity and is never inherited.
	want := append([]string{"name"}, knowledge.SchemaNames()...)
	var keys []string
	for key := range got {
		if key == "inherited" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sort.Strings(want)
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("the output schema is %v, want exactly the inheritable vocabulary %v", keys, want)
	}
}
