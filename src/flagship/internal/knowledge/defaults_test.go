package knowledge_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/flagship-dev/flagship/internal/knowledge"
)

const (
	capYAML   = "name: cap\ncap:\n  - say: hold the plan\n"
	movedYAML = "name: moved\nwork:\n  - say: v1\n"
	bothV1    = "name: both\nwork:\n  - say: v1\n"
	bothV2    = "name: both\nwork:\n  - say: v2\n"
)

// shippedFS is the embedded defaults a test resolves against.
func shippedFS(files map[string]string) fs.FS {
	m := fstest.MapFS{}
	for name, body := range files {
		m[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

// openWith opens a knowledge center whose last scope is the given shipped tree.
// The defaults are handed to Open and never written to disk: a shipped playbook
// is live without being materialised first.
func openWith(t *testing.T, fsRoot string, files map[string]string) *knowledge.Center {
	t.Helper()
	kc, err := knowledge.Open(fsRoot, shippedFS(files))
	if err != nil {
		t.Fatal(err)
	}
	return kc
}

// stampOf is the stamp line a written copy carries for a default body. It
// spells the on-disk format out literally: that format is the contract between
// a playbook file and the binary that wrote it.
func stampOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "# fs-default: sha256=" + hex.EncodeToString(sum[:]) + "\n"
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// stateOf returns the reported state of one playbook.
func stateOf(t *testing.T, states []knowledge.PlaybookState, name string) knowledge.PlaybookState {
	t.Helper()
	for _, state := range states {
		if state.Name == name {
			return state
		}
	}
	t.Fatalf("playbook %q not reported among %d states", name, len(states))
	return knowledge.PlaybookState{}
}

// A playbook the binary ships but nobody has written to disk is a playbook:
// resolution falls through to the embedded tree, so `fs kb list` reports it and
// nothing has to be written for it to run.
func TestAShippedPlaybookWithNoFileIsListed(t *testing.T) {
	kc := openWith(t, tempFS(t), map[string]string{"cap.yaml": capYAML})

	states, err := kc.States()
	if err != nil {
		t.Fatalf("States: %v", err)
	}
	got := stateOf(t, states, "cap")
	if got.Scope != knowledge.ScopeShipped || got.State != knowledge.StateShipped {
		t.Errorf("cap = %+v, want scope %q and state %q", got, knowledge.ScopeShipped, knowledge.StateShipped)
	}
	if got.Path != "" {
		t.Errorf("cap path = %q, want empty: there is no file", got.Path)
	}
	if got.Edited {
		t.Errorf("cap = %+v, want no edited flag: nothing was written", got)
	}
	if got.UsedBy != "prompt: always" {
		t.Errorf("cap used_by = %q, want the shipped playbook's %q", got.UsedBy, "prompt: always")
	}

	names, err := kc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !slices.Contains(names, "cap") {
		t.Errorf("List = %v, want the shipped cap among them", names)
	}
}

// States reports what runs and what is on disk separately, because they answer
// different questions. A copy that restates the default does not participate —
// including one that has fallen behind the default it was written from — so its
// scope reads shipped while its own state still says the copy is stale.
func TestStatesSeparatesTheCopyFromWhatRuns(t *testing.T) {
	fsRoot := tempFS(t)
	kc := openWith(t, fsRoot, map[string]string{
		"cap.yaml":   capYAML,
		"moved.yaml": "name: moved\nwork:\n  - say: v2\n",
		"both.yaml":  bothV2,
	})

	// An untouched copy restates the default: transparent, so the default runs.
	writeFile(t, kbPath(fsRoot, "cap"), stampOf(capYAML)+capYAML)
	// A copy stamped from an older default is transparent too — the defect this
	// change exists for — and reports itself stale.
	older := "name: moved\nwork:\n  - say: v0\n"
	writeFile(t, kbPath(fsRoot, "moved"), stampOf(older)+older)
	// Both at once: the user edited it and the default moved on. An edit is an
	// override, so this one runs.
	writeFile(t, kbPath(fsRoot, "both"), stampOf(bothV1)+bothV1+"  - say: an added rule\n")
	// A hand-written playbook with no shipped default always runs.
	writeFile(t, kbPath(fsRoot, "hand"), "name: hand\nwork:\n  - say: mine\n")

	states, err := kc.States()
	if err != nil {
		t.Fatalf("States: %v", err)
	}

	for _, tc := range []struct {
		name          string
		scope, state  string
		edited, stale bool
	}{
		{"cap", knowledge.ScopeShipped, knowledge.StateDefault, false, false},
		{"moved", knowledge.ScopeShipped, knowledge.StateStale, false, true},
		{"both", knowledge.ScopeGlobal, knowledge.StateStale, true, true},
		{"hand", knowledge.ScopeGlobal, knowledge.StateLocal, false, false},
	} {
		got := stateOf(t, states, tc.name)
		if got.Scope != tc.scope || got.State != tc.state || got.Edited != tc.edited || got.Stale != tc.stale {
			t.Errorf("%s = %+v, want scope %q state %q edited=%v stale=%v",
				tc.name, got, tc.scope, tc.state, tc.edited, tc.stale)
		}
	}

	// The two questions are answerable from the same row: the copy is stale, and
	// it is not what runs.
	stale := stateOf(t, states, "moved")
	if stale.Path != kbPath(fsRoot, "moved") {
		t.Errorf("moved path = %q, want the copy on disk", stale.Path)
	}
}

// A project's playbook is reported with the project as its scope, so where a
// playbook resolves from is part of its state rather than only its name.
func TestStatesReportsTheScopeOfEachPlaybook(t *testing.T) {
	fsRoot := tempFS(t)
	global := openWith(t, fsRoot, map[string]string{"cap.yaml": capYAML})
	project := global.InProject("skills")

	if err := project.Add("house", []byte("name: house\nwork:\n  - say: this project's way\n")); err != nil {
		t.Fatalf("Add: %v", err)
	}

	states, err := project.States()
	if err != nil {
		t.Fatalf("States: %v", err)
	}
	// Nothing wrote cap to disk, so it resolves from the binary — not from the
	// global scope, which holds no file of that name.
	if got := stateOf(t, states, "cap").Scope; got != knowledge.ScopeShipped {
		t.Errorf("cap scope = %q, want %q", got, knowledge.ScopeShipped)
	}
	if got := stateOf(t, states, "house").Scope; got != "skills" {
		t.Errorf("house scope = %q, want %q", got, "skills")
	}
}

func TestDiffIsEmptyForACopyThatRestatesTheDefault(t *testing.T) {
	fsRoot := tempFS(t)
	kc := openWith(t, fsRoot, map[string]string{"cap.yaml": capYAML})
	writeFile(t, kbPath(fsRoot, "cap"), stampOf(capYAML)+capYAML)

	diff, err := kc.Diff("cap")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff != "" {
		t.Errorf("a copy of the default diff = %q, want empty", diff)
	}

	edited := stampOf(capYAML) + strings.Replace(capYAML, "hold the plan", "hold the plan and the budget", 1)
	writeFile(t, kbPath(fsRoot, "cap"), edited)

	diff, err = kc.Diff("cap")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff == "" {
		t.Fatal("an edited playbook must diff non-empty")
	}
	for _, want := range []string{
		"-  - say: hold the plan",
		"+  - say: hold the plan and the budget",
		" name: cap", // unchanged lines are context
	} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff missing %q:\n%s", want, diff)
		}
	}
	if strings.Contains(diff, "# fs-default:") {
		t.Errorf("the stamp line must not appear in a diff:\n%s", diff)
	}

	if _, err := kc.Diff("no-such-default"); err == nil {
		t.Error("diffing a playbook with no shipped default must fail")
	}
}

// Diff reads the copy, not what resolution picks: a copy that has fallen behind
// the default is exactly the case a reader runs `fs kb diff` to look at, and it
// is the one case where the two differ.
func TestDiffShowsAStaleCopyEvenThoughItDoesNotRun(t *testing.T) {
	fsRoot := tempFS(t)
	kc := openWith(t, fsRoot, map[string]string{"cap.yaml": capYAML})

	older := "name: cap\ncap:\n  - say: v0\n"
	writeFile(t, kbPath(fsRoot, "cap"), stampOf(older)+older)

	diff, err := kc.Diff("cap")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	for _, want := range []string{"-  - say: hold the plan", "+  - say: v0"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff missing %q:\n%s", want, diff)
		}
	}

	// With no copy there is nothing to compare, and saying so is more useful than
	// an empty diff that reads as "your file is fine".
	if _, err := kc.Diff("moved"); err == nil {
		t.Error("diffing a playbook that has no copy on disk must fail")
	}
}

// Reset is the one command that writes a shipped default to disk. It is how a
// reader asks for a copy to edit and how an edited copy goes back, so it works
// where nothing is there yet — no install step stands between a fresh home and a
// playbook to edit.
func TestResetWritesTheShippedDefault(t *testing.T) {
	fsRoot := tempFS(t)
	kc := openWith(t, fsRoot, map[string]string{"cap.yaml": capYAML})

	// Nothing on disk: the scope's directory is made, not assumed.
	path, err := kc.Reset("cap")
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if want := kbPath(fsRoot, "cap"); path != want {
		t.Errorf("Reset path = %q, want %q", path, want)
	}
	if got, want := readFile(t, path), stampOf(capYAML)+capYAML; got != want {
		t.Errorf("reset content = %q, want %q", got, want)
	}

	// An edited copy goes back.
	writeFile(t, kbPath(fsRoot, "cap"), stampOf(capYAML)+"name: cap\ncap:\n  - say: something else\n")
	if _, err := kc.Reset("cap"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if got, want := readFile(t, path), stampOf(capYAML)+capYAML; got != want {
		t.Errorf("reset content = %q, want %q", got, want)
	}
	if diff, err := kc.Diff("cap"); err != nil || diff != "" {
		t.Errorf("diff after reset = %q (err %v), want empty", diff, err)
	}

	if _, err := kc.Reset("no-such-default"); err == nil {
		t.Error("resetting a playbook with no shipped default must fail")
	}
}

// A project playbook that declares one field inherits the rest from the shipped
// default when the user scope holds no file of that name, and the identity it
// inherits from is the default's, not a copy's. This is the chain the change
// exists to keep whole: the last scope is a scope, so it is walked like one.
func TestAShadowInheritsFromTheShippedDefault(t *testing.T) {
	fsRoot := tempFS(t)
	kc := openWith(t, fsRoot, map[string]string{"herdr.yaml": herdrYAML})
	project := kc.InProject("skills")

	// The project's playbook declares its own cap steps and nothing else: the
	// terms that select it come from below.
	shadow := "name: herdr\ncap:\n  - say: this project's own rule\n"
	if err := project.Add("herdr", []byte(shadow)); err != nil {
		t.Fatalf("Add: %v", err)
	}

	pb, err := project.Get("herdr")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := pb.AppliesWhenString(); got != "engine=herdr" {
		t.Errorf("applies_when = %q, want it inherited from the shipped default", got)
	}
	if got := strings.Join(pb.InheritedFields(), ","); got != "applies_when" {
		t.Errorf("inherited = %q, want applies_when alone", got)
	}
	if !pb.Serves(knowledge.MomentCap) {
		t.Error("the project's own cap steps must be kept")
	}
	if got := stateOf(t, mustStates(t, project), "herdr").Scope; got != "skills" {
		t.Errorf("herdr scope = %q, want the project it resolves from", got)
	}
}

// mustStates is States with the error handled, for an assertion that is about one row.
func mustStates(t *testing.T, kc *knowledge.Center) []knowledge.PlaybookState {
	t.Helper()
	out, err := kc.States()
	if err != nil {
		t.Fatalf("States: %v", err)
	}
	return out
}

// used_by answers "who loads this playbook?" — the question that makes an inert
// playbook visible. A playbook is selected by its phase and its applies_when and
// by nothing else: the cap phase is loaded by the session prompt, the other
// three by a dispatch whose tags satisfy the terms, and a file that does not
// parse declares nothing, so nothing selects it.
func TestStatesReportsWhoUsesEachPlaybook(t *testing.T) {
	fsRoot := tempFS(t)
	kc, err := knowledge.Open(fsRoot, nil)
	if err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{
		"code-entry":    "name: code-entry\napplies_when: code\nentry:\n  - check: true\n",
		"parallel-exit": "name: parallel-exit\napplies_when: type=parallel\nexit:\n  - check: true\n",
		"cap":           "name: cap\ncap:\n  - say: hold the plan\n",
		"herdr":         "name: herdr\napplies_when: engine=herdr\ncap:\n  - say: the herdr rules\n",
		// No terms at all: it loads for every dispatch, and says so rather than
		// reading as something nothing selects.
		"worker": "name: worker\nwork:\n  - say: note your node\n",
		// Every term must hold, so this one wants both tags at once.
		"code-research": "name: code-research\napplies_when: code, type=dev-task\nwork:\n  - say: x\n",
		// A phase that is not one of the four: no dispatch runs it.
		"procedure": "name: procedure\nphase: procedure\nsteps:\n  - say: x\n",
		// A playbook from the removed schema: refused at parse, so inert. This
		// is what makes the migration visible in `fs kb list`.
		"superseded": "name: superseded\ntype: prerequisite\ntrigger: superseded\nsteps:\n  - check: true\n",
		// Not a playbook at all: it declares nothing, so nothing selects it.
		"broken": "prose that is not a playbook\n",
	} {
		writeFile(t, kbPath(fsRoot, name), body)
	}

	// No shipped defaults: the fixtures are hand-written playbooks.
	states, err := kc.States()
	if err != nil {
		t.Fatalf("States: %v", err)
	}

	for _, tc := range []struct{ name, usedBy string }{
		{"code-entry", "dispatch: code (entry)"},
		{"parallel-exit", "dispatch: type=parallel (exit)"},
		{"cap", "prompt: always"},
		{"herdr", "prompt: engine=herdr"},
		{"worker", "dispatch: always (work)"},
		{"code-research", "dispatch: code,type=dev-task (work)"},
		{"procedure", knowledge.UsedByNone},
		{"superseded", knowledge.UsedByNone},
		{"broken", knowledge.UsedByNone},
	} {
		if got := stateOf(t, states, tc.name).UsedBy; got != tc.usedBy {
			t.Errorf("%s used_by = %q, want %q", tc.name, got, tc.usedBy)
		}
	}
}

// Every playbook the binary ships is selected by some mechanism; one that
// nothing loads is inert. The defaults are read from the tree the binary embeds
// rather than restated, so a new shipped playbook nothing loads fails here — and
// they are read without being written to disk, which is the same property.
func TestEveryShippedPlaybookIsUsed(t *testing.T) {
	shipped := os.DirFS(filepath.Join("..", "..", "defaults", "kb"))

	kc, err := knowledge.Open(tempFS(t), shipped)
	if err != nil {
		t.Fatal(err)
	}

	states, err := kc.States()
	if err != nil {
		t.Fatalf("States: %v", err)
	}
	for _, state := range states {
		if state.UsedBy == knowledge.UsedByNone {
			t.Errorf("shipped playbook %s is inert: no mechanism selects it", state.Name)
		}
		if state.Scope != knowledge.ScopeShipped {
			t.Errorf("shipped playbook %s scope = %q, want %q: nothing wrote it to disk",
				state.Name, state.Scope, knowledge.ScopeShipped)
		}
	}

	for name, usedBy := range map[string]string{
		"cap":         "prompt: always",
		"herdr":       "prompt: engine=herdr",
		"worker":      "dispatch: always (work)",
		"code":        "dispatch: code (entry, exit)",
		"plan":        "dispatch: code (work)",
		"code-bar":    "dispatch: code (work)",

		"independent-review": "dispatch: type=review-task (work, exit)",
		"research":           "dispatch: research (work)",
		"batch":              "dispatch: type=batch (entry, exit)",
		"member":             "dispatch: member (work, exit)",
		"integration":        "dispatch: type=integrate (entry, exit)",
	} {
		if got := stateOf(t, states, name).UsedBy; got != usedBy {
			t.Errorf("shipped %s used_by = %q, want %q", name, got, usedBy)
		}
	}
}
