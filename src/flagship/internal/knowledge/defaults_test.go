package knowledge_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/flagship-dev/flagship/internal/knowledge"
)

const (
	capYAML   = "name: cap\nphase: cap\nsteps:\n  - say: hold the plan\n"
	movedYAML = "name: moved\nphase: work\nsteps:\n  - say: v1\n"
)

// shippedFS is the embedded defaults a test seeds from.
func shippedFS(files map[string]string) fs.FS {
	m := fstest.MapFS{}
	for name, body := range files {
		m[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

// stampOf is the stamp line a seeded playbook carries for a default body. It
// spells the on-disk format out literally: that format is the contract between
// a playbook file and the binary that seeded it.
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

func TestSeedCreatesStampedPlaybooksThenKeepsThem(t *testing.T) {
	fsRoot := tempFS(t)
	kc, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	shipped := shippedFS(map[string]string{
		"cap.yaml":    capYAML,
		"moved.yaml":  movedYAML,
		"ignored.txt": "not a playbook",
	})

	outcomes, err := kc.Seed(shipped)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("seeded %d playbooks, want 2: %v", len(outcomes), outcomes)
	}
	for _, outcome := range outcomes {
		if outcome.Action != knowledge.ActionCreated {
			t.Errorf("%s action = %q, want %q", outcome.Name, outcome.Action, knowledge.ActionCreated)
		}
		if want := kbPath(fsRoot, outcome.Name); outcome.Path != want {
			t.Errorf("%s path = %q, want %q", outcome.Name, outcome.Path, want)
		}
	}

	// The written file is the default's body verbatim, with one stamp line added.
	if got, want := readFile(t, kbPath(fsRoot, "cap")), stampOf(capYAML)+capYAML; got != want {
		t.Errorf("seeded cap.yaml = %q, want %q", got, want)
	}

	// A second run changes nothing and says so.
	before := readFile(t, kbPath(fsRoot, "cap"))
	outcomes, err = kc.Seed(shipped)
	if err != nil {
		t.Fatalf("second Seed: %v", err)
	}
	for _, outcome := range outcomes {
		if outcome.Action != knowledge.ActionKept {
			t.Errorf("%s action = %q, want %q", outcome.Name, outcome.Action, knowledge.ActionKept)
		}
	}
	if after := readFile(t, kbPath(fsRoot, "cap")); after != before {
		t.Errorf("second Seed rewrote cap.yaml:\nbefore %q\nafter  %q", before, after)
	}
}

func TestSeedNeverOverwritesAnExistingPlaybook(t *testing.T) {
	fsRoot := tempFS(t)
	kc, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}

	mine := "# mine: do not touch\nname: cap\nphase: cap\nsteps:\n  - say: my own rule\n"
	writeFile(t, kbPath(fsRoot, "cap"), mine)

	outcomes, err := kc.Seed(shippedFS(map[string]string{"cap.yaml": capYAML, "other.yaml": movedYAML}))
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}

	if got := readFile(t, kbPath(fsRoot, "cap")); got != mine {
		t.Errorf("an existing playbook was modified:\ngot  %q\nwant %q", got, mine)
	}
	for _, outcome := range outcomes {
		want := knowledge.ActionCreated
		if outcome.Name == "cap" {
			want = knowledge.ActionKept
		}
		if outcome.Action != want {
			t.Errorf("%s action = %q, want %q", outcome.Name, outcome.Action, want)
		}
	}
}

// Seeding a KB that already holds a playbook from the removed schema keeps it
// byte-for-byte — seeding never deletes — and the file it keeps is one nothing
// can select. That is the migration an operator has to finish by hand.
func TestSeedKeepsASupersededPlaybookAndStatesCallsItInert(t *testing.T) {
	fsRoot := tempFS(t)
	kc, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}

	old := "name: moved\ntype: prerequisite\ntrigger: moved\nsteps:\n  - check: true\n"
	writeFile(t, kbPath(fsRoot, "moved"), old)

	outcomes, err := kc.Seed(shippedFS(map[string]string{"moved.yaml": movedYAML}))
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if outcomes[0].Action != knowledge.ActionKept {
		t.Errorf("action = %q, want the superseded file kept", outcomes[0].Action)
	}
	if got := readFile(t, kbPath(fsRoot, "moved")); got != old {
		t.Errorf("seed rewrote a superseded playbook:\ngot  %q\nwant %q", got, old)
	}

	states, err := kc.States(shippedFS(map[string]string{"moved.yaml": movedYAML}))
	if err != nil {
		t.Fatalf("States: %v", err)
	}
	got := stateOf(t, states, "moved")
	if got.UsedBy != knowledge.UsedByNone {
		t.Errorf("a playbook carrying a removed field is reported used_by %q, want %q", got.UsedBy, knowledge.UsedByNone)
	}
}

func TestStatesReportsDrift(t *testing.T) {
	fsRoot := tempFS(t)
	kc, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	shipped := shippedFS(map[string]string{
		"cap.yaml":   capYAML,
		"moved.yaml": movedYAML,
		"both.yaml":  movedYAML,
	})
	if _, err := kc.Seed(shipped); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	// An untouched seed is default.
	states, err := kc.States(shipped)
	if err != nil {
		t.Fatalf("States: %v", err)
	}
	if got := stateOf(t, states, "cap"); got.State != knowledge.StateDefault || got.Edited || got.Stale {
		t.Errorf("untouched seed = %+v, want state %q with no flags", got, knowledge.StateDefault)
	}

	// A one-line change is edited, not stale: the default has not moved.
	writeFile(t, kbPath(fsRoot, "cap"), stampOf(capYAML)+capYAML+"  - say: an added rule\n")
	// A playbook whose stamp matches its own body but not today's default was
	// seeded from a default that has since moved on: stale, not edited.
	older := "name: moved\nphase: work\nsteps:\n  - say: v0\n"
	writeFile(t, kbPath(fsRoot, "moved"), stampOf(older)+older)
	// Both at once: the user edited it and the default moved on.
	writeFile(t, kbPath(fsRoot, "both"), stampOf(movedYAML)+movedYAML+"  - say: an added rule\n")
	// A hand-written playbook with no shipped default is local.
	local := "name: hand\nphase: work\nsteps:\n  - say: mine\n"
	writeFile(t, kbPath(fsRoot, "hand"), local)

	states, err = kc.States(shippedFS(map[string]string{
		"cap.yaml":   capYAML,
		"moved.yaml": "name: moved\nphase: work\nsteps:\n  - say: v2\n",
		"both.yaml":  "name: both\nphase: work\nsteps:\n  - say: v2\n",
	}))
	if err != nil {
		t.Fatalf("States: %v", err)
	}

	for _, tc := range []struct {
		name          string
		state         string
		edited, stale bool
	}{
		{"cap", knowledge.StateEdited, true, false},
		{"moved", knowledge.StateStale, false, true},
		{"both", knowledge.StateStale, true, true},
		{"hand", knowledge.StateLocal, false, false},
	} {
		got := stateOf(t, states, tc.name)
		if got.State != tc.state || got.Edited != tc.edited || got.Stale != tc.stale {
			t.Errorf("%s = %+v, want state %q edited=%v stale=%v",
				tc.name, got, tc.state, tc.edited, tc.stale)
		}
	}
}

// A project's playbook is reported with the project as its scope, so where a
// playbook came from is part of its state rather than only its name.
func TestStatesReportsTheScopeOfEachPlaybook(t *testing.T) {
	fsRoot := tempFS(t)
	global, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := global.Seed(shippedFS(map[string]string{"cap.yaml": capYAML})); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	project := global.InProject("skills")
	if err := project.Add("house", []byte("name: house\nphase: work\nsteps:\n  - say: this project's way\n")); err != nil {
		t.Fatalf("Add: %v", err)
	}

	states, err := project.States(fstest.MapFS{})
	if err != nil {
		t.Fatalf("States: %v", err)
	}
	if got := stateOf(t, states, "cap").Scope; got != knowledge.ScopeGlobal {
		t.Errorf("cap scope = %q, want %q", got, knowledge.ScopeGlobal)
	}
	if got := stateOf(t, states, "house").Scope; got != "skills" {
		t.Errorf("house scope = %q, want %q", got, "skills")
	}
}

func TestDiffIsEmptyForAPristineSeedAndShowsAnEdit(t *testing.T) {
	fsRoot := tempFS(t)
	kc, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	shipped := shippedFS(map[string]string{"cap.yaml": capYAML})
	if _, err := kc.Seed(shipped); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	diff, err := kc.Diff(shipped, "cap")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff != "" {
		t.Errorf("pristine seed diff = %q, want empty", diff)
	}

	edited := stampOf(capYAML) + strings.Replace(capYAML, "hold the plan", "hold the plan and the budget", 1)
	writeFile(t, kbPath(fsRoot, "cap"), edited)

	diff, err = kc.Diff(shipped, "cap")
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

	if _, err := kc.Diff(shipped, "no-such-default"); err == nil {
		t.Error("diffing a playbook with no shipped default must fail")
	}
}

func TestResetRestoresTheShippedDefault(t *testing.T) {
	fsRoot := tempFS(t)
	kc, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	shipped := shippedFS(map[string]string{"cap.yaml": capYAML})
	if _, err := kc.Seed(shipped); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	writeFile(t, kbPath(fsRoot, "cap"), stampOf(capYAML)+"name: cap\nphase: cap\nsteps:\n  - say: something else\n")

	path, err := kc.Reset(shipped, "cap")
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if want := kbPath(fsRoot, "cap"); path != want {
		t.Errorf("Reset path = %q, want %q", path, want)
	}
	if got, want := readFile(t, path), stampOf(capYAML)+capYAML; got != want {
		t.Errorf("reset content = %q, want %q", got, want)
	}
	if diff, err := kc.Diff(shipped, "cap"); err != nil || diff != "" {
		t.Errorf("diff after reset = %q (err %v), want empty", diff, err)
	}

	if _, err := kc.Reset(shipped, "no-such-default"); err == nil {
		t.Error("resetting a playbook with no shipped default must fail")
	}
}

// used_by answers "who loads this playbook?" — the question that makes an inert
// playbook visible. A playbook is selected by its phase and its applies_when and
// by nothing else: the cap phase is loaded by the session prompt, the other
// three by a dispatch whose tags satisfy the terms, and a file that does not
// parse declares nothing, so nothing selects it.
func TestStatesReportsWhoUsesEachPlaybook(t *testing.T) {
	fsRoot := tempFS(t)
	kc, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{
		"code-entry":    "name: code-entry\nphase: entry\napplies_when: code\nsteps:\n  - check: true\n",
		"parallel-exit": "name: parallel-exit\nphase: exit\napplies_when: type=parallel\nsteps:\n  - check: true\n",
		"cap":           "name: cap\nphase: cap\nsteps:\n  - say: hold the plan\n",
		"herdr":         "name: herdr\nphase: cap\napplies_when: engine=herdr\nsteps:\n  - say: the herdr rules\n",
		// No terms at all: it loads for every dispatch, and says so rather than
		// reading as something nothing selects.
		"worker": "name: worker\nphase: work\nsteps:\n  - say: note your node\n",
		// Every term must hold, so this one wants both tags at once.
		"code-research": "name: code-research\nphase: work\napplies_when: code, type=dev-task\nsteps:\n  - say: x\n",
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
	states, err := kc.States(fstest.MapFS{})
	if err != nil {
		t.Fatalf("States: %v", err)
	}

	for _, tc := range []struct{ name, usedBy string }{
		{"code-entry", "dispatch: code"},
		{"parallel-exit", "dispatch: type=parallel"},
		{"cap", "prompt: always"},
		{"herdr", "prompt: engine=herdr"},
		{"worker", "dispatch: always"},
		{"code-research", "dispatch: code,type=dev-task"},
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
// rather than restated, so a new shipped playbook nothing loads fails here.
func TestEveryShippedPlaybookIsUsed(t *testing.T) {
	shipped := os.DirFS(filepath.Join("..", "..", "defaults", "kb"))

	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kc.Seed(shipped); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	states, err := kc.States(shipped)
	if err != nil {
		t.Fatalf("States: %v", err)
	}
	for _, state := range states {
		if state.UsedBy == knowledge.UsedByNone {
			t.Errorf("shipped playbook %s is inert: no mechanism selects it", state.Name)
		}
	}

	for name, usedBy := range map[string]string{
		"cap":               "prompt: always",
		"herdr":             "prompt: engine=herdr",
		"worker":            "dispatch: always",
		"code-entry":        "dispatch: code",
		"code-exit":         "dispatch: code",
		"plan":              "dispatch: code",
		"development":       "dispatch: code",
		"tdd":               "dispatch: code",
		"review":            "dispatch: code",
		"research":          "dispatch: research",
		"parallel-entry":    "dispatch: type=parallel",
		"parallel-exit":     "dispatch: type=parallel",
		"integration-entry": "dispatch: type=integrate",
		"integration-exit":  "dispatch: type=integrate",
	} {
		if got := stateOf(t, states, name).UsedBy; got != usedBy {
			t.Errorf("shipped %s used_by = %q, want %q", name, got, usedBy)
		}
	}
}
