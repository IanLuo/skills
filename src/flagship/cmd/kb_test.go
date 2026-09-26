package main

// The playbooks live in three scopes — a project's KB, the global KB, and the
// defaults embedded in the binary — and fs kb is how a reader asks what
// resolved, what is on disk, and what has drifted from what the binary ships.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The shipped playbooks a test names by constant. A playbook is selected by its
// phase and its applies_when and by nothing else, so the names below say which
// phase gate each one is rather than what it used to be called.
const (
	capPlaybook   = "cap"
	codePb        = "code"
	herdrPlaybook = "herdr"
	batchPb       = "batch"
	memberPb      = "member"
	integrationPb = "integration"

	// Fixture playbooks a test writes into its own home. A playbook may be
	// called anything, so these keep the two names the code participant used to
	// ship under: they are not the shipped set, and nothing here asserts they
	// are.
	codeEntryPb = "code-entry"
	codeExitPb  = "code-exit"

	// Stub playbooks a worktree-teardown test writes: an entry playbook that
	// declares the realization and a passing check, and an exit playbook that
	// carries the shipped teardown. Both have no applies_when, so they sit in
	// every dispatch's plan — those tests are about the teardown, not about
	// which tags select it.
	teardownEntryPb = "teardown-entry"
	teardownExitPb  = "teardown-exit"
)

// shippedPlaybooks is every playbook the binary ships. fs kb list names all of
// them, with or without a file on disk; the cap's standing rules are built from
// the cap-phase ones.
var shippedPlaybooks = []string{
	capPlaybook, herdrPlaybook, "worker", codePb, "plan",
	"development", "tdd", "review", independentReviewPb, "research", batchPb, memberPb, integrationPb,
	"spec",
}

// independentReviewPb is the playbook a `--type review-task` dispatch selects:
// the independent reviewer's protocol, the counterpart to the self-reviewing
// `review`.
const independentReviewPb = "independent-review"

// shippedDefault is a default the binary is expected to carry. The test process
// runs in this package's directory, so the module's defaults/ is one level up:
// the same files //go:embed put into the binary.
func shippedDefault(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "defaults", "kb", name+".yaml"))
	if err != nil {
		t.Fatalf("read shipped default %s: %v", name, err)
	}
	return string(data)
}

// fileStamp is the stamp line fs writes for a default body. It spells the
// on-disk format out literally: that format is the contract between a playbook
// file and the binary that seeded it.
func fileStamp(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "# fs-default: sha256=" + hex.EncodeToString(sum[:]) + "\n"
}

// editedStepMarker is appended to a shipped step's body to make an edit whose
// old and new text a diff must name.
const editedStepMarker = " EDITED-BY-TEST"

// shippedStep returns the body of the first step of the given kind in a shipped
// playbook, so a test can mean "the playbook has a step about X" without
// hardcoding the sentence that states it. A non-empty marker selects the first
// step whose body contains it; an empty marker takes the first of that kind.
// The shipped playbook stays editable: only removing the behaviour a test names
// may break it, never rewording it.
func shippedStep(t *testing.T, playbook, kind, marker string) string {
	t.Helper()
	prefix := "- " + kind + ": "
	for _, line := range strings.Split(playbook, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		body := strings.TrimPrefix(line, prefix)
		if marker == "" || strings.Contains(body, marker) {
			return body
		}
	}
	t.Fatalf("playbook has no %s step containing %q:\n%s", kind, marker, playbook)
	return ""
}

// editFirstStep rewrites the body of the first step line in a playbook body —
// the first "- <kind>: <body>" line — by appending editedStepMarker, and returns
// the edited body plus the old and new step lines. Deriving the edit from the
// shipped default keeps the caller about diffing, not about the default's
// wording: an edit that named a sentence would match nothing after a reword,
// write the file unedited, and fail a test that a legitimate edit may not fail.
func editFirstStep(t *testing.T, body string) (edited, oldStep, newStep string) {
	t.Helper()
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "- ") || !strings.Contains(line, ": ") {
			continue
		}
		oldStep = line
		newStep = line + editedStepMarker
		lines[i] = newStep
		return strings.Join(lines, "\n"), oldStep, newStep
	}
	t.Fatalf("shipped default has no step line to edit:\n%s", body)
	return "", "", ""
}

// playbooksOf indexes a kb list response's playbooks by name.
func playbooksOf(t *testing.T, resp map[string]any) map[string]map[string]any {
	t.Helper()
	raw, ok := resp["data"].(map[string]any)["playbooks"].([]any)
	if !ok {
		t.Fatalf("response carries no playbooks: %v", resp)
	}
	byName := make(map[string]map[string]any, len(raw))
	for _, item := range raw {
		pb := item.(map[string]any)
		byName[pb["name"].(string)] = pb
	}
	return byName
}

// runFSOK runs fs and fails the test unless it succeeded.
func runFSOK(t *testing.T, bin, dir string, args ...string) map[string]any {
	t.Helper()
	resp, code := runFS(t, bin, dir, args...)
	if code != 0 {
		t.Fatalf("fs %s exit %d: %v", strings.Join(args, " "), code, resp["error"])
	}
	return resp
}

func readRaw(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func writeRaw(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// fs kb list answers "what is actually running": every playbook the binary
// ships is a playbook, and on a fresh home each one resolves out of the binary
// with nothing written to disk. There is no install step between a new home and
// a working set of playbooks.
func TestCLIKBListNamesEveryShippedPlaybookWithItsScope(t *testing.T) {
	bin := getFS(t)
	home := testHome(t)

	listed := playbooksOf(t, runFSOK(t, bin, t.TempDir(), "kb", "list"))
	if len(listed) != len(shippedPlaybooks) {
		t.Fatalf("kb list names %d playbooks, want the %d shipped: %v",
			len(listed), len(shippedPlaybooks), listed)
	}

	for _, name := range shippedPlaybooks {
		pb, ok := listed[name]
		if !ok {
			t.Errorf("kb list does not name the shipped %s: %v", name, listed)
			continue
		}
		if pb["scope"] != "shipped" || pb["state"] != "shipped" {
			t.Errorf("%s = %v, want scope and state shipped: nothing is on disk", name, pb)
		}
		if pb["path"] != "" {
			t.Errorf("%s path = %v, want empty", name, pb["path"])
		}
		if pb["edited"] != false || pb["stale"] != false {
			t.Errorf("%s = %v, want no drift flags: there is no copy", name, pb)
		}
	}

	// The list is a live reading, not a listing of a directory: it named all
	// fourteen playbooks and wrote none of them.
	entries, err := os.ReadDir(filepath.Join(home, ".fs", "kb"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read the global KB: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("kb list left %d files in the global KB, want none", len(entries))
	}
}

// A copy the user asks for is written by one command, and what it writes is the
// shipped default verbatim plus its stamp — the format that makes the copy
// recognisable as a copy rather than as an override.
func TestCLIKBResetWritesTheShippedDefaultOnRequest(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	home := testHome(t)

	resp := runFSOK(t, bin, dir, "kb", "reset", capPlaybook, "--yes")
	path := resp["data"].(map[string]any)["path"].(string)
	if want := filepath.Join(home, ".fs", "kb", capPlaybook+".yaml"); path != want {
		t.Errorf("reset path = %q, want %q", path, want)
	}
	if got, want := readRaw(t, path), fileStamp(shippedDefault(t, capPlaybook))+shippedDefault(t, capPlaybook); got != want {
		t.Errorf("reset wrote\n%q\nwant %q", got, want)
	}

	// The copy is transparent: the playbook still resolves out of the binary, so
	// what was written is a copy to edit and not a second source of truth.
	if pb := playbooksOf(t, runFSOK(t, bin, dir, "kb", "list"))[capPlaybook]; pb["scope"] != "shipped" || pb["state"] != "default" {
		t.Errorf("a fresh copy = %v, want the default to keep running", pb)
	}
}

// A copy of a shipped default the user never changed is not a playbook of its
// own: it restates what the binary already ships. The shipped default is what a
// dispatch reads instead — including when the copy has fallen behind it, which
// is the defect this pins. A stale ~/.fs/kb copy used to win, so a change to
// defaults/kb/*.yaml could land, pass its tests, and be inert.
//
// The cases that must NOT change are here too: an override keeps winning. That
// is the whole rule, and testing only the half that fixes today's bug would let
// the fix demote the user scope instead of the copy.
func TestAKBCopyParticipatesOnlyWhenItIsAnOverride(t *testing.T) {
	bin := getFS(t)
	def := shippedDefault(t, codePb)
	shippedGate := shippedStep(t, def, "check", "script check-specs.sh")
	myGate := "my own gate"

	// The same one-line edit, in the three shapes a file can be in: a body the
	// stamp does not match, a body with no stamp, and a body that is the default.
	editedDefault := strings.Replace(def, "- check: "+shippedGate, "- check: "+myGate, 1)
	if editedDefault == def {
		t.Fatalf("the fixture did not edit the shipped default:\n%s", def)
	}
	older := "name: code\napplies_when: code\nentry:\n  - check: " + myGate + "\n"

	for _, tc := range []struct {
		name, content string
		override      bool // the file is what runs, rather than the shipped default
	}{
		{"an unmodified copy is transparent", fileStamp(def) + def, false},
		{"a stale copy is transparent", fileStamp(older) + older, false},
		{"an unstamped copy of the default is transparent", def, false},
		{"an edited copy is an override", fileStamp(def) + editedDefault, true},
		{"an edited file with no stamp is an override", editedDefault, true},
		{"a hand-written playbook is an override", older, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := testHome(t)
			writeKBPlaybook(t, home, codePb, tc.content)

			resp := runFSOK(t, bin, t.TempDir(), "kb", "get", codePb)
			var got []string
			for _, raw := range resp["data"].(map[string]any)["entry"].([]any) {
				got = append(got, raw.(map[string]any)["body"].(string))
			}

			want, instead := shippedGate, myGate
			if tc.override {
				want, instead = myGate, shippedGate
			}
			if !slices.Contains(got, want) {
				t.Errorf("the resolved gate is %q, want the one from %s", got, want)
			}
			if slices.Contains(got, instead) {
				t.Errorf("the resolved gate is %q, which is the one from the other file", got)
			}
		})
	}
}

// The list answers both questions a reader has, per playbook and in one
// command: "is my edit live" (scope and edited) and "is my copy stale" (stale,
// with the path of the copy it is talking about).
func TestCLIKBListReportsWhatRunsAndWhatIsOnDisk(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	home := testHome(t)
	kbDir := filepath.Join(home, ".fs", "kb")

	// A copy that restates the default is transparent: it is reported with the
	// path it lives at, its own state default, and the scope that runs.
	runFSOK(t, bin, dir, "kb", "reset", codePb, "--yes")
	state := playbooksOf(t, runFSOK(t, bin, dir, "kb", "list"))[codePb]
	if state["scope"] != "shipped" || state["state"] != "default" || state["edited"] != false {
		t.Errorf("a copy of the default = %v, want scope shipped, state default, edited false", state)
	}
	if state["path"] != filepath.Join(kbDir, codePb+".yaml") {
		t.Errorf("a copy of the default path = %v, want the file it lives at", state["path"])
	}

	// A one-line change is an override: edited, and the scope it resolves from is
	// the global KB, because that file is now what runs.
	devPath := filepath.Join(kbDir, codePb+".yaml")
	writeRaw(t, devPath, readRaw(t, devPath)+"  - say: an added step\n")
	state = playbooksOf(t, runFSOK(t, bin, dir, "kb", "list"))[codePb]
	if state["scope"] != "global" || state["state"] != "edited" || state["edited"] != true {
		t.Errorf("an edited playbook = %v, want scope global, state edited", state)
	}

	// A playbook whose stamp matches its own body but not the shipped default was
	// written from a default that has since moved on: stale, and still not what
	// runs — the scope stays shipped, which is the half that says the copy is
	// being ignored.
	older := "name: cap\ncap:\n  - say: older\n"
	writeKBPlaybook(t, home, capPlaybook, fileStamp(older)+older)
	state = playbooksOf(t, runFSOK(t, bin, dir, "kb", "list"))[capPlaybook]
	if state["scope"] != "shipped" || state["state"] != "stale" || state["stale"] != true || state["edited"] != false {
		t.Errorf("a stale playbook = %v, want scope shipped with state stale", state)
	}

	// A hand-written playbook with no shipped default is local.
	writeKBPlaybook(t, home, "hand-written", "name: hand-written\nwork:\n  - say: mine\n")
	if state := playbooksOf(t, runFSOK(t, bin, dir, "kb", "list"))["hand-written"]; state["state"] != "local" {
		t.Errorf("a local playbook = %v, want state local", state)
	}
}

// A playbook in a project's scope resolves from that scope, and the list says
// so: the same reading has to name the project when a project is what runs.
func TestCLIKBListNamesTheProjectScope(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	home := testHome(t)
	runFSOK(t, bin, dir, "project", "create", "--name", "proj")
	kbDir := filepath.Join(home, ".fs", "projects", "proj", "kb")
	if err := os.MkdirAll(kbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRaw(t, filepath.Join(kbDir, capPlaybook+".yaml"), "# mine\nname: cap\ncap:\n  - say: this project's rule\n")

	if state := playbooksOf(t, runFSOK(t, bin, dir, "kb", "list", "--project", "proj"))[capPlaybook]; state["scope"] != "proj" {
		t.Errorf("a project's playbook = %v, want scope proj", state)
	}
	// Without the project, the same name is the shipped default again: the scope
	// is a fact about the reading, not about the file.
	if state := playbooksOf(t, runFSOK(t, bin, dir, "kb", "list"))[capPlaybook]; state["scope"] != "shipped" {
		t.Errorf("the same playbook outside its project = %v, want scope shipped", state)
	}
}

func TestCLIKBDiffAndReset(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	home := testHome(t)
	capPath := filepath.Join(home, ".fs", "kb", capPlaybook+".yaml")

	diffOf := func(name string) string {
		t.Helper()
		return runFSOK(t, bin, dir, "kb", "diff", name)["data"].(map[string]any)["diff"].(string)
	}

	// Nothing is on disk, so there is nothing to diff: saying so is more useful
	// than an empty diff, which reads as "your file is fine".
	resp, code := runFS(t, bin, dir, "kb", "diff", capPlaybook)
	if code != 1 {
		t.Fatalf("diff with no copy exit = %d, want 1: %v", code, resp)
	}
	if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "no copy on disk") {
		t.Errorf("error %q must say there is no copy", errMsg)
	}

	// The copy one command writes diffs empty: it is the shipped default.
	runFSOK(t, bin, dir, "kb", "reset", capPlaybook, "--yes")
	if diff := diffOf(capPlaybook); diff != "" {
		t.Errorf("a copy of the default must diff empty, got:\n%s", diff)
	}

	body := shippedDefault(t, capPlaybook)
	editedBody, oldStep, newStep := editFirstStep(t, body)
	edited := fileStamp(body) + editedBody
	writeRaw(t, capPath, edited)

	diff := diffOf(capPlaybook)
	if diff == "" {
		t.Fatal("an edited playbook must diff non-empty")
	}
	for _, want := range []string{
		"-" + oldStep,
		"+" + newStep,
		" name: cap", // unchanged lines are context
	} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff missing %q:\n%s", want, diff)
		}
	}
	if strings.Contains(diff, "# fs-default:") {
		t.Errorf("the stamp line must not appear in a diff:\n%s", diff)
	}

	// Without --yes, reset refuses and changes nothing.
	resp, code = runFS(t, bin, dir, "kb", "reset", capPlaybook)
	if code != 1 {
		t.Fatalf("reset without --yes exit = %d, want 1: %v", code, resp)
	}
	if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "--yes") {
		t.Errorf("error %q must ask for --yes", errMsg)
	}
	if got := readRaw(t, capPath); got != edited {
		t.Errorf("a refused reset modified the playbook:\ngot  %q\nwant %q", got, edited)
	}

	// With --yes it restores the shipped default, and the diff goes empty.
	runFSOK(t, bin, dir, "kb", "reset", capPlaybook, "--yes")
	if got, want := readRaw(t, capPath), fileStamp(body)+body; got != want {
		t.Errorf("reset content =\n%q\nwant %q", got, want)
	}
	if diff := diffOf(capPlaybook); diff != "" {
		t.Errorf("diff after reset must be empty, got:\n%s", diff)
	}

	// A playbook with no shipped default cannot be diffed or reset.
	for _, args := range [][]string{
		{"kb", "diff", "no-such-default"},
		{"kb", "reset", "no-such-default", "--yes"},
	} {
		resp, code := runFS(t, bin, dir, args...)
		if code != 1 {
			t.Errorf("fs %s exit = %d, want 1: %v", strings.Join(args, " "), code, resp)
		}
		if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "no-such-default") {
			t.Errorf("fs %s error %q must name the missing default", strings.Join(args, " "), errMsg)
		}
	}
}

// The parallel gate must read the batch's cards from the dispatch that runs it,
// not from a list someone edits per batch: batch.yaml runs check-parallel.sh
// with $FS_CARDS, the variable fs dispatch --cards exports. The script is the
// binary's own, run as "$FS_BIN" script check-parallel.sh, so this pins the
// wiring wherever the module is built; tests/check-parallel covers the script's
// own behaviour.
func TestShippedParallelGateReadsTheDispatchedCards(t *testing.T) {
	bin := getFS(t)
	writeKBPlaybook(t, testHome(t), batchPb, shippedDefault(t, batchPb))

	resp, code := runFS(t, bin, t.TempDir(), "kb", "get", batchPb)
	if code != 0 {
		t.Fatalf("kb get exit %d: %v", code, resp["error"])
	}

	// The gate reads the batch from the dispatch itself: $FS_CARDS is the
	// variable fs dispatch --cards exports. Naming the variable rather than the
	// whole command pins the wiring while leaving the playbook's wording free.
	want := shippedStep(t, shippedDefault(t, batchPb), "check", "$FS_CARDS")
	var checks []string
	for _, raw := range resp["data"].(map[string]any)["entry"].([]any) {
		step := raw.(map[string]any)
		if step["kind"] != "check" {
			continue
		}
		body := step["body"].(string)
		checks = append(checks, body)
		if body == want {
			return
		}
	}
	t.Errorf("the batch gate's checks are %v, want one running %q", checks, want)
}

// fs kb prompt is the delivery path for the playbooks: the cap's standing rules
// come out as plain text on stdout, so a session starts from the rules the
// binary ships rather than from a prompt retyped by hand.
func TestCLIKBPromptPrintsTheCapStandingRules(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	home := testHome(t)

	stdout, stderr, code := rawFS(t, bin, dir, home, "kb", "prompt")
	if code != 0 {
		t.Fatalf("kb prompt exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stderr != "" {
		t.Errorf("kb prompt wrote to stderr: %q", stderr)
	}
	if strings.HasPrefix(stdout, "{") {
		t.Errorf("kb prompt must be plain text, not the JSON envelope: %q", stdout)
	}

	// The standing-rules line, one header per cap playbook the session's tags
	// select, and the rules themselves — read from the shipped default, so
	// rewording the playbook cannot break a test that only means "the prompt
	// carries the herdr rules". Nothing was written to the home: the rules come
	// out of the binary, which is what the header says.
	for _, want := range []string{
		"Standing rules for this session",
		"not suggestions",
		"# cap — moments cap; shipped",
		"# herdr — moments cap; shipped",
		shippedStep(t, shippedDefault(t, herdrPlaybook), "say", ""),
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("kb prompt must contain %q; got:\n%s", want, stdout)
		}
	}
	// An entry playbook is a gate a dispatch runs, not a standing rule. The
	// header shape is what makes this precise: `code` is also an ordinary word
	// the cap's own rules may use.
	if strings.Contains(stdout, "# "+codePb+" — moments") {
		t.Errorf("an entry playbook must not be part of the prompt:\n%s", stdout)
	}

	// Piped whole into one --append-system-prompt argument: a blank line would
	// break it into separate blocks.
	if strings.Contains(stdout, "\n\n") {
		t.Errorf("kb prompt must contain no blank lines:\n%q", stdout)
	}
}

// A copy that has fallen behind the default does not run, and the prompt says
// both halves in one header: the rules the cap is reading came out of the
// binary, and the file beneath them is the user's, stale, and stamped. A cap
// that edited its own playbook can tell from its own prompt that the edit is
// not what it is following.
func TestCLIKBPromptNamesTheRunningScopeAndTheStaleCopyBeneathIt(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	home := testHome(t)

	older := "name: cap\ncap:\n  - say: older\n"
	writeKBPlaybook(t, home, capPlaybook, fileStamp(older)+older)

	stdout, _, code := rawFS(t, bin, dir, home, "kb", "prompt")
	if code != 0 {
		t.Fatalf("kb prompt exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "# cap — moments cap; shipped, stale, stamp sha256:") {
		t.Errorf("a stale copy must be named beneath the scope that runs:\n%s", stdout)
	}
	if strings.Contains(stdout, "older") {
		t.Errorf("the stale copy's steps do not run and must not be printed:\n%s", stdout)
	}
}

// A fresh home is not an empty knowledge center: the playbooks the binary ships
// are what runs, so the cap's standing rules are there without anything being
// written first — and they still are after a copy is written, since a copy of
// the default is not an override.
func TestCLIKBPromptOnAFreshHomeCarriesTheShippedRules(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	home := t.TempDir()

	stdout, _, code := rawFS(t, bin, dir, home, "kb", "prompt")
	if code != 0 {
		t.Fatalf("kb prompt exit = %d, want 0", code)
	}
	for _, want := range []string{"Standing rules for this session", "# cap — moments cap; shipped"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("a fresh home must carry the shipped rules (%q):\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".fs", "kb", capPlaybook+".yaml")); !os.IsNotExist(err) {
		t.Errorf("kb prompt wrote a playbook to disk: %v", err)
	}
}

// stepsOf returns one shipped playbook's steps in one moment's section, parsed
// by the binary itself — the same parse the dispatcher and the exit gate use.
func stepsOf(t *testing.T, bin, name, moment string) []map[string]any {
	t.Helper()
	writeKBPlaybook(t, testHome(t), name, shippedDefault(t, name))
	resp, code := runFS(t, bin, t.TempDir(), "kb", "get", name)
	if code != 0 {
		t.Fatalf("kb get %s exit %d: %v", name, code, resp["error"])
	}
	raw, ok := resp["data"].(map[string]any)[moment].([]any)
	if !ok {
		t.Fatalf("kb get %s has no %q section: %v", name, moment, resp["data"])
	}
	var steps []map[string]any
	for _, r := range raw {
		steps = append(steps, r.(map[string]any))
	}
	return steps
}

// bodiesOf returns the bodies of one kind of step, in order.
func bodiesOf(steps []map[string]any, kind string) []string {
	var bodies []string
	for _, step := range steps {
		if step["kind"] == kind {
			bodies = append(bodies, step["body"].(string))
		}
	}
	return bodies
}

// postconditionsOf returns the bodies of the checks declared after the first
// `do` step — the ones that verify what a teardown removed, as opposed to the
// ones that gate whether it runs at all.
func postconditionsOf(t *testing.T, playbook string, steps []map[string]any) []string {
	t.Helper()
	var post []string
	afterTeardown := false
	for _, step := range steps {
		switch step["kind"] {
		case "do":
			afterTeardown = true
		case "check":
			if afterTeardown {
				post = append(post, step["body"].(string))
			}
		}
	}
	if len(post) == 0 {
		t.Errorf("%s declares no post-action check: nothing verifies the teardown it ran", playbook)
	}
	return post
}

// preconditionsOf returns the bodies of the checks declared before the first
// `do` step — the ones that decide whether the teardown runs.
func preconditionsOf(steps []map[string]any) []string {
	var pre []string
	for _, step := range steps {
		if step["kind"] == "do" {
			return pre
		}
		if step["kind"] == "check" {
			pre = append(pre, step["body"].(string))
		}
	}
	return pre
}

// A gate must check what its own dispatch owns: not a proxy for it, and not a
// condition belonging to a different dispatch. This pins where each shipped gate
// draws that line, so a later edit that reaches for a proxy fails here.
func TestShippedGatesAreScopedToTheirOwnDispatch(t *testing.T) {
	bin := getFS(t)

	// The member's exit gate checks the process, not the outcome, and it owns no
	// teardown: the member's tree is the batch's, so there is no do step and no
	// postcondition here. The merged-ness check it replaced asserted an outcome a
	// hand merge satisfies, so it is deleted rather than kept beside the
	// stronger check.
	memberSteps := stepsOf(t, bin, memberPb, "exit")
	if pre := preconditionsOf(memberSteps); len(pre) != 1 || !strings.Contains(pre[0], "integrated $FS_NODE") {
		t.Errorf("member-exit's checks = %v, want the integration link alone", pre)
	}
	if len(bodiesOf(memberSteps, "do")) != 0 {
		t.Errorf("member-exit declares teardown: a member's tree is the batch's to remove")
	}
	if asks := bodiesOf(memberSteps, "ask"); len(asks) != 1 ||
		!strings.Contains(asks[0], "its worker node") {
		t.Errorf("member-exit asks = %v, want the one question about this member's node", asks)
	}

	// The integration's exit gate asks about the suite rather than checking it.
	// The command that runs a project's tests is the project's to supply: this
	// playbook ships in the binary and runs in every project, so the check it
	// used to carry — `bash tests/run.sh` — exited 127 everywhere but here. A
	// project with a suite adds it in that project's scope. The six teardown
	// steps it used to share with parallel-exit stay deleted: they are the
	// batch's, not a per-member integration's.
	intSteps := stepsOf(t, bin, integrationPb, "exit")
	if got := preconditionsOf(intSteps); len(got) != 0 {
		t.Errorf("integration-exit's checks = %v, want none: a shipped gate runs in every project, "+
			"so the suite command is the project's to declare", got)
	}
	if len(bodiesOf(intSteps, "do")) != 0 {
		t.Errorf("integration-exit declares teardown: a member's tree is the batch's to remove")
	}
	if asks := bodiesOf(intSteps, "ask"); len(asks) != 2 ||
		!strings.Contains(asks[0], "suite green") || !strings.Contains(asks[1], "integration verdict") {
		t.Errorf("integration-exit asks = %v, want the suite question then the verdict", asks)
	}

	// The batch's exit gate is where the fan-out's teardown lives, declared once.
	// Its first step is the fs batch gate — the read that refuses, naming the
	// unintegrated members, before any teardown — and every step after it
	// iterates the batch's members, so a member's tree is removed only after
	// every member is integrated.
	batchSteps := stepsOf(t, bin, batchPb, "exit")
	if pre := preconditionsOf(batchSteps); len(pre) != 1 || !strings.Contains(pre[0], "batch $FS_NODE") {
		t.Errorf("batch-exit's gate = %v, want the fs batch read alone", pre)
	}
	if actions := bodiesOf(batchSteps, "do"); len(actions) != 3 {
		t.Errorf("batch-exit declares %d teardown actions, want 3 (branch, worktree, workspace)", len(actions))
	}
	for _, body := range postconditionsOf(t, batchPb, batchSteps) {
		if !strings.Contains(body, `batch "$FS_NODE"`) {
			t.Errorf("batch-exit postcondition %q is not scoped to the batch's members", body)
		}
	}

	// The entry gate is member-scoped, and its compound ask is split: a rubric
	// cannot partition two questions asked as one.
	entry := bodiesOf(stepsOf(t, bin, integrationPb, "entry"), "check")
	if len(entry) != 1 || !strings.Contains(entry[0], "task get") || !strings.Contains(entry[0], "$FS_INTEGRATES") {
		t.Errorf("integration-entry checks = %v, want the member check on $FS_INTEGRATES", entry)
	}
	for _, body := range entry {
		if strings.Contains(body, "worktree list") {
			t.Errorf("integration-entry check %q is repo-wide; it cannot be run per member", body)
		}
	}
	if asks := bodiesOf(stepsOf(t, bin, integrationPb, "entry"), "ask"); len(asks) != 2 {
		t.Errorf("integration-entry asks = %v, want the order and the staged-changes question apart", asks)
	}

	// Ordinary code work has an exit gate at all, and it declares its own
	// teardown rather than relying on machinery in fs close. The ask comes
	// first, because the teardown destroys the tree the answer is about: a
	// question asked after the worktree is gone is a question about a checkout
	// nobody can look at any more.
	dev := stepsOf(t, bin, codePb, "exit")
	if len(dev) == 0 || dev[0]["kind"] != "ask" {
		t.Errorf("code's first exit step = %v, want the ask before the teardown", dev)
	}
	asks := bodiesOf(dev, "ask")
	if len(asks) != 1 || !strings.Contains(asks[0], "nothing important died with its transcript") {
		t.Errorf("code's exit asks = %v, want the question about the worker's node", asks)
	}
	if len(bodiesOf(dev, "do")) == 0 {
		t.Error("code's exit section declares no teardown: a dispatch that had a worktree would leak its checkout")
	}
	for _, body := range bodiesOf(dev, "do") {
		if !strings.Contains(body, "FS_BATCH") {
			t.Errorf("code's exit action %q does not defer on FS_BATCH — a member's tree would not survive its close", body)
		}
	}
	// The rule is that a postcondition is about a resource THIS dispatch's record
	// owns, and it used to be spelled "contains FS_WORKTREE" — true of every
	// postcondition there was, because both of them were about the worktree (its
	// path, and the herdr workspace the delivery recorded with it). The branch
	// postcondition is the first that is about neither, and it is about a
	// resource of exactly the same kind: FS_BRANCH is written by this delivery
	// from what it created, and the check reads it from there rather than
	// re-deriving it. So the assertion is widened to the property it was a proxy
	// for, not relaxed: a body that names none of the record's own fields is
	// still caught, and one that reaches for the repo instead still is.
	//
	// The FS_BATCH rule below is new, and it is the same rule the do steps above
	// are held to. A member's tree and branch belong to its batch, and the batch's
	// close removes them — so a member's postcondition that fired over its own
	// still-present worktree would refuse every member's close, which is the
	// false gate this task is about, one layer over.
	for _, body := range postconditionsOf(t, codePb, dev) {
		if !strings.Contains(body, "FS_WORKTREE") && !strings.Contains(body, "FS_BRANCH") {
			t.Errorf("code's exit postcondition %q names no resource this dispatch's record owns", body)
		}
		if !strings.Contains(body, "FS_BATCH") {
			t.Errorf("code's exit postcondition %q does not defer on FS_BATCH — a member's tree and branch are the batch's to remove", body)
		}
	}
	// One pre-teardown check, and it is about this dispatch's own record rather
	// than about the state of the repo: a clean tree and a merged branch are
	// checks a close cannot make true, because the repo is routinely dirty with
	// another agent's work. The review gate is the one question of that shape
	// that CAN be answered — the cap dispatches a review, and the refusal names
	// the command that does it — so it is admitted here, and the rule the old
	// assertion was protecting is kept by asserting the shape rather than the
	// count.
	pre := preconditionsOf(dev)
	if len(pre) != 1 || !strings.Contains(pre[0], `reviews "$FS_NODE"`) {
		t.Errorf("code's exit pre-teardown checks = %v, want the review gate on $FS_NODE alone", pre)
	}
	for _, body := range pre {
		if !strings.Contains(body, "FS_WITHOUT_REVIEW") {
			t.Errorf("code's exit check %q has no --without-review guard: a dispatch that needs no review would be unclosable", body)
		}
		for _, world := range []string{"git status", "merge-base"} {
			if strings.Contains(body, world) {
				t.Errorf("code's exit check %q asserts the state of the repo, which this close cannot make true", body)
			}
		}
	}
}
