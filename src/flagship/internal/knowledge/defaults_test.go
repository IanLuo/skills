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
	capYAML   = "name: cap\ntype: procedure\nsteps:\n  - say: hold the plan\n"
	movedYAML = "name: moved\ntype: procedure\nsteps:\n  - say: v1\n"
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
	dir := tempKB(t)
	kc, err := knowledge.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	shipped := shippedFS(map[string]string{
		"cap.yaml":                    capYAML,
		"dev-task-prerequisites.yaml": movedYAML,
		"ignored.txt":                 "not a playbook",
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
		if want := filepath.Join(dir, outcome.Name+".yaml"); outcome.Path != want {
			t.Errorf("%s path = %q, want %q", outcome.Name, outcome.Path, want)
		}
	}

	// The written file is the default's body verbatim, with one stamp line added.
	if got, want := readFile(t, filepath.Join(dir, "cap.yaml")), stampOf(capYAML)+capYAML; got != want {
		t.Errorf("seeded cap.yaml = %q, want %q", got, want)
	}

	// A second run changes nothing and says so.
	before := readFile(t, filepath.Join(dir, "cap.yaml"))
	outcomes, err = kc.Seed(shipped)
	if err != nil {
		t.Fatalf("second Seed: %v", err)
	}
	for _, outcome := range outcomes {
		if outcome.Action != knowledge.ActionKept {
			t.Errorf("%s action = %q, want %q", outcome.Name, outcome.Action, knowledge.ActionKept)
		}
	}
	if after := readFile(t, filepath.Join(dir, "cap.yaml")); after != before {
		t.Errorf("second Seed rewrote cap.yaml:\nbefore %q\nafter  %q", before, after)
	}
}

func TestSeedNeverOverwritesAnExistingPlaybook(t *testing.T) {
	dir := tempKB(t)
	kc, err := knowledge.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	mine := "# mine: do not touch\nname: cap\ntype: procedure\nsteps:\n  - say: my own rule\n"
	writeFile(t, filepath.Join(dir, "cap.yaml"), mine)

	outcomes, err := kc.Seed(shippedFS(map[string]string{"cap.yaml": capYAML, "other.yaml": movedYAML}))
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}

	if got := readFile(t, filepath.Join(dir, "cap.yaml")); got != mine {
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

func TestStatesReportsDrift(t *testing.T) {
	dir := tempKB(t)
	kc, err := knowledge.Open(dir)
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
	writeFile(t, filepath.Join(dir, "cap.yaml"), stampOf(capYAML)+capYAML+"  - say: an added rule\n")
	// A playbook whose stamp matches its own body but not today's default was
	// seeded from a default that has since moved on: stale, not edited.
	older := "name: moved\ntype: procedure\nsteps:\n  - say: v0\n"
	writeFile(t, filepath.Join(dir, "moved.yaml"), stampOf(older)+older)
	// Both at once: the user edited it and the default moved on.
	writeFile(t, filepath.Join(dir, "both.yaml"), stampOf(movedYAML)+movedYAML+"  - say: an added rule\n")
	// A hand-written playbook with no shipped default is local.
	local := "name: hand\ntype: procedure\nsteps:\n  - say: mine\n"
	writeFile(t, filepath.Join(dir, "hand.yaml"), local)

	states, err = kc.States(shippedFS(map[string]string{
		"cap.yaml":   capYAML,
		"moved.yaml": "name: moved\ntype: procedure\nsteps:\n  - say: v2\n",
		"both.yaml":  "name: both\ntype: procedure\nsteps:\n  - say: v2\n",
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

func TestDiffIsEmptyForAPristineSeedAndShowsAnEdit(t *testing.T) {
	dir := tempKB(t)
	kc, err := knowledge.Open(dir)
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
	writeFile(t, filepath.Join(dir, "cap.yaml"), edited)

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
	dir := tempKB(t)
	kc, err := knowledge.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	shipped := shippedFS(map[string]string{"cap.yaml": capYAML})
	if _, err := kc.Seed(shipped); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	writeFile(t, filepath.Join(dir, "cap.yaml"), stampOf(capYAML)+"name: cap\nsteps:\n  - say: something else\n")

	path, err := kc.Reset(shipped, "cap")
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if want := filepath.Join(dir, "cap.yaml"); path != want {
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
