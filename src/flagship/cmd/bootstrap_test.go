package main

// fs bootstrap seeds ~/.fs/kb from the playbooks embedded in the binary, and
// fs kb reports how what is on disk relates to them.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shipped playbooks a test names by constant. A playbook is selected by its
// phase and its applies_when and by nothing else, so the names below say which
// phase gate each one is rather than what it used to be called.
const (
	capPlaybook      = "cap"
	codeEntryPb      = "code-entry"
	codeExitPb       = "code-exit"
	herdrPlaybook    = "herdr"
	parEntryPlaybook = "parallel-entry"
	parExitPlaybook  = "parallel-exit"
	intEntryPlaybook = "integration-entry"
	intExitPlaybook  = "integration-exit"
)

// shippedPlaybooks is every playbook the binary ships. fs bootstrap seeds all
// of them; the cap's standing rules are built from the cap-phase ones.
var shippedPlaybooks = []string{
	capPlaybook, herdrPlaybook, "worker", codeEntryPb, codeExitPb, "plan",
	"development", "tdd", "review", "research", parEntryPlaybook, parExitPlaybook,
	intEntryPlaybook, intExitPlaybook,
}

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

// playbooksOf indexes a kb list or bootstrap response's playbooks by name.
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

func TestCLIBootstrapSeedsThenKeeps(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	home := testHome(t)
	kbDir := filepath.Join(home, ".fs", "kb")

	resp := runFSOK(t, bin, dir, "bootstrap")
	if got, want := resp["data"].(map[string]any)["kb_path"], kbDir; got != want {
		t.Errorf("kb_path = %v, want %q", got, want)
	}

	seeded := playbooksOf(t, resp)
	if len(seeded) != len(shippedPlaybooks) {
		t.Fatalf("bootstrap seeded %d playbooks, want the %d shipped: %v",
			len(seeded), len(shippedPlaybooks), seeded)
	}
	for _, name := range shippedPlaybooks {
		pb, shipped := seeded[name]
		if !shipped {
			t.Errorf("bootstrap did not seed %s: %v", name, seeded)
			continue
		}
		if pb["action"] != "created" {
			t.Errorf("%s action = %v, want created", name, pb["action"])
		}
		// The written file is the shipped default verbatim, plus one stamp line.
		want := fileStamp(shippedDefault(t, name)) + shippedDefault(t, name)
		if got := readRaw(t, filepath.Join(kbDir, name+".yaml")); got != want {
			t.Errorf("%s is not the shipped default plus a stamp:\ngot  %q\nwant %q", name, got, want)
		}
	}

	before := readRaw(t, filepath.Join(kbDir, capPlaybook+".yaml"))
	resp = runFSOK(t, bin, dir, "bootstrap")
	for name, pb := range playbooksOf(t, resp) {
		if pb["action"] != "kept" {
			t.Errorf("second bootstrap %s action = %v, want kept", name, pb["action"])
		}
	}
	if after := readRaw(t, filepath.Join(kbDir, capPlaybook+".yaml")); after != before {
		t.Errorf("a second bootstrap rewrote %s:\nbefore %q\nafter  %q", capPlaybook, before, after)
	}
}

func TestCLIBootstrapNeverOverwritesAPlaybook(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	home := testHome(t)

	mine := "# mine: keep this line\nname: cap\nphase: cap\nsteps:\n  - say: mine\n"
	writeKBPlaybook(t, home, capPlaybook, mine)

	seeded := playbooksOf(t, runFSOK(t, bin, dir, "bootstrap"))
	if seeded[capPlaybook]["action"] != "kept" {
		t.Errorf("%s action = %v, want kept", capPlaybook, seeded[capPlaybook]["action"])
	}
	if got := readRaw(t, filepath.Join(home, ".fs", "kb", capPlaybook+".yaml")); got != mine {
		t.Errorf("bootstrap modified an existing playbook:\ngot  %q\nwant %q", got, mine)
	}
	if seeded[codeEntryPb]["action"] != "created" {
		t.Errorf("an absent playbook must still be created: %v", seeded)
	}
}

func TestCLIKBListReportsDrift(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	home := testHome(t)
	kbDir := filepath.Join(home, ".fs", "kb")
	runFSOK(t, bin, dir, "bootstrap")

	// An untouched seed is default.
	for name, state := range playbooksOf(t, runFSOK(t, bin, dir, "kb", "list")) {
		if state["state"] != "default" || state["edited"] != false || state["stale"] != false {
			t.Errorf("untouched seed %s = %v, want state default with no flags", name, state)
		}
	}

	// A one-line change is edited.
	devPath := filepath.Join(kbDir, codeEntryPb+".yaml")
	writeRaw(t, devPath, readRaw(t, devPath)+"  - say: an added step\n")
	if state := playbooksOf(t, runFSOK(t, bin, dir, "kb", "list"))[codeEntryPb]; state["state"] != "edited" {
		t.Errorf("an edited playbook = %v, want state edited", state)
	}

	// A playbook whose stamp matches its own body but not the shipped default
	// was seeded from a default that has since moved on: stale, not edited.
	older := "name: cap\nphase: cap\nsteps:\n  - say: older\n"
	writeKBPlaybook(t, home, capPlaybook, fileStamp(older)+older)
	if state := playbooksOf(t, runFSOK(t, bin, dir, "kb", "list"))[capPlaybook]; state["state"] != "stale" {
		t.Errorf("a stale playbook = %v, want state stale", state)
	}

	// A hand-written playbook with no shipped default is local.
	writeKBPlaybook(t, home, "hand-written", "name: hand-written\nphase: work\nsteps:\n  - say: mine\n")
	if state := playbooksOf(t, runFSOK(t, bin, dir, "kb", "list"))["hand-written"]; state["state"] != "local" {
		t.Errorf("a local playbook = %v, want state local", state)
	}
}

func TestCLIKBDiffAndReset(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	home := testHome(t)
	capPath := filepath.Join(home, ".fs", "kb", capPlaybook+".yaml")
	runFSOK(t, bin, dir, "bootstrap")

	diffOf := func(name string) string {
		t.Helper()
		return runFSOK(t, bin, dir, "kb", "diff", name)["data"].(map[string]any)["diff"].(string)
	}

	if diff := diffOf(capPlaybook); diff != "" {
		t.Errorf("a pristine seed must diff empty, got:\n%s", diff)
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
	resp, code := runFS(t, bin, dir, "kb", "reset", capPlaybook)
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
// not from a list someone edits per batch: parallel-entry.yaml runs
// bin/check-parallel.sh with $FS_CARDS, the variable fs dispatch --cards exports.
// This pins that wiring wherever the module is built, including where the repo's
// bin/ is out of reach; tests/check-parallel covers the script's own behaviour.
func TestShippedParallelGateReadsTheDispatchedCards(t *testing.T) {
	bin := getFS(t)
	writeKBPlaybook(t, testHome(t), parEntryPlaybook, shippedDefault(t, parEntryPlaybook))

	resp, code := runFS(t, bin, t.TempDir(), "kb", "get", parEntryPlaybook)
	if code != 0 {
		t.Fatalf("kb get exit %d: %v", code, resp["error"])
	}

	// The gate reads the batch from the dispatch itself: $FS_CARDS is the
	// variable fs dispatch --cards exports. Naming the variable rather than the
	// whole command pins the wiring while leaving the playbook's wording free.
	want := shippedStep(t, shippedDefault(t, parEntryPlaybook), "check", "$FS_CARDS")
	var checks []string
	for _, raw := range resp["data"].(map[string]any)["steps"].([]any) {
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
	t.Errorf("the parallel gate's checks are %v, want one running %q", checks, want)
}

// fs kb prompt is the delivery path for the playbooks: the cap's standing rules
// come out as plain text on stdout, so a session starts from the rules the
// binary ships rather than from a prompt retyped by hand.
func TestCLIKBPromptPrintsTheCapStandingRules(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	home := testHome(t)
	runFSOK(t, bin, dir, "bootstrap")

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

	// The standing-rules line, one stamped header per cap playbook the session's
	// tags select, and the rules themselves — read from the shipped default, so
	// rewording the playbook cannot break a test that only means "the prompt
	// carries the herdr rules".
	for _, want := range []string{
		"Standing rules for this session",
		"not suggestions",
		"# cap — phase cap; default, stamp sha256:",
		"# herdr — phase cap; default, stamp sha256:",
		shippedStep(t, shippedDefault(t, herdrPlaybook), "say", ""),
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("kb prompt must contain %q; got:\n%s", want, stdout)
		}
	}
	// An entry playbook is a gate a dispatch runs, not a standing rule.
	if strings.Contains(stdout, codeEntryPb) {
		t.Errorf("an entry playbook must not be part of the prompt:\n%s", stdout)
	}

	// Piped whole into one --append-system-prompt argument: a blank line would
	// break it into separate blocks.
	if strings.Contains(stdout, "\n\n") {
		t.Errorf("kb prompt must contain no blank lines:\n%q", stdout)
	}
}

// A prompt built from a playbook whose shipped default has moved on says so, so
// the cap can tell its rules are stale without a second command.
func TestCLIKBPromptMarksAStalePlaybook(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	home := testHome(t)
	runFSOK(t, bin, dir, "bootstrap")

	older := "name: cap\nphase: cap\nsteps:\n  - say: older\n"
	writeKBPlaybook(t, home, capPlaybook, fileStamp(older)+older)

	stdout, _, code := rawFS(t, bin, dir, home, "kb", "prompt")
	if code != 0 {
		t.Fatalf("kb prompt exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "# cap — phase cap; stale, stamp sha256:") {
		t.Errorf("a stale playbook must be marked stale in the prompt:\n%s", stdout)
	}
}

// An empty knowledge center is not an error: a fresh HOME has no playbooks yet,
// and the prompt says what to run rather than coming out empty.
func TestCLIKBPromptOnAFreshHomeSaysToBootstrap(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	home := t.TempDir()

	stdout, _, code := rawFS(t, bin, dir, home, "kb", "prompt")
	if code != 0 {
		t.Fatalf("kb prompt exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "fs bootstrap") {
		t.Errorf("an unseeded knowledge center must say how to fill it:\n%s", stdout)
	}
}

// stepsOf returns one shipped playbook's steps, parsed by the binary itself —
// the same parse the dispatcher and the exit gate use.
func stepsOf(t *testing.T, bin, name string) []map[string]any {
	t.Helper()
	writeKBPlaybook(t, testHome(t), name, shippedDefault(t, name))
	resp, code := runFS(t, bin, t.TempDir(), "kb", "get", name)
	if code != 0 {
		t.Fatalf("kb get %s exit %d: %v", name, code, resp["error"])
	}
	var steps []map[string]any
	for _, raw := range resp["data"].(map[string]any)["steps"].([]any) {
		steps = append(steps, raw.(map[string]any))
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

	// The member's exit gate checks the process, not the outcome. The
	// merged-ness check it replaced asserted an outcome a hand merge satisfies,
	// so it is deleted rather than kept beside the stronger check. Everything
	// after the declared teardown is a postcondition about this member's own
	// tree or workspace, named by $FS_WORKTREE*, never about the batch.
	memberSteps := stepsOf(t, bin, parExitPlaybook)
	pre := preconditionsOf(memberSteps)
	if len(pre) != 1 || pre[0] != "fs integrated $FS_NODE" {
		t.Errorf("parallel-exit's pre-teardown checks = %v, want the integration link alone", pre)
	}
	for _, body := range postconditionsOf(t, parExitPlaybook, memberSteps) {
		if !strings.Contains(body, "FS_WORKTREE") {
			t.Errorf("parallel-exit postcondition %q is not about this member's own worktree/workspace", body)
		}
	}
	if asks := bodiesOf(memberSteps, "ask"); len(asks) != 1 ||
		!strings.Contains(asks[0], "its worker node") {
		t.Errorf("parallel-exit asks = %v, want the one question about this member's node", asks)
	}

	// The integration's exit gate keeps what the integration owns, and it is the
	// same teardown: a worktree or branch check is batch-global when it does not
	// name this dispatch's own tree — it belongs to no single member, and it
	// makes a per-member integration impossible while any other member is
	// unfinished. The suite check is the integration's own: it runs on the tree
	// the worker merged into.
	intSteps := stepsOf(t, bin, intExitPlaybook)
	if got := preconditionsOf(intSteps); len(got) != 1 || !strings.Contains(got[0], "tests/run.sh") {
		t.Errorf("integration-exit's pre-teardown checks = %v, want the repo suite alone", got)
	}
	for _, body := range postconditionsOf(t, intExitPlaybook, intSteps) {
		if !strings.Contains(body, "FS_WORKTREE") {
			t.Errorf("integration-exit postcondition %q is not about this dispatch's own worktree", body)
		}
	}
	for _, body := range bodiesOf(intSteps, "check") {
		if (strings.Contains(body, "worktree") || strings.Contains(body, "branch")) &&
			!strings.Contains(body, "FS_WORKTREE") {
			t.Errorf("integration-exit check %q is batch-global; it belongs to no single member", body)
		}
	}
	if asks := bodiesOf(intSteps, "ask"); len(asks) != 1 ||
		!strings.Contains(asks[0], "integration verdict") {
		t.Errorf("integration-exit asks = %v, want the one question this integration owns", asks)
	}

	// The entry gate is member-scoped, and its compound ask is split: a rubric
	// cannot partition two questions asked as one.
	entry := bodiesOf(stepsOf(t, bin, intEntryPlaybook), "check")
	if len(entry) != 1 || !strings.Contains(entry[0], "fs task get") || !strings.Contains(entry[0], "$FS_INTEGRATES") {
		t.Errorf("integration-entry checks = %v, want the member check on $FS_INTEGRATES", entry)
	}
	for _, body := range entry {
		if strings.Contains(body, "worktree list") {
			t.Errorf("integration-entry check %q is repo-wide; it cannot be run per member", body)
		}
	}
	if asks := bodiesOf(stepsOf(t, bin, intEntryPlaybook), "ask"); len(asks) != 2 {
		t.Errorf("integration-entry asks = %v, want the order and the staged-changes question apart", asks)
	}

	// Ordinary code work has an exit gate at all, and it declares its own
	// teardown rather than relying on machinery in fs close. The ask comes
	// first, because the teardown destroys the tree the answer is about: a
	// question asked after the worktree is gone is a question about a checkout
	// nobody can look at any more.
	dev := stepsOf(t, bin, codeExitPb)
	if len(dev) == 0 || dev[0]["kind"] != "ask" {
		t.Errorf("code-exit's first step = %v, want the ask before the teardown", dev)
	}
	asks := bodiesOf(dev, "ask")
	if len(asks) != 1 || !strings.Contains(asks[0], "nothing important died with its transcript") {
		t.Errorf("code-exit asks = %v, want the question about the worker's node", asks)
	}
	if len(bodiesOf(dev, "do")) == 0 {
		t.Error("code-exit declares no teardown: a dispatch that had a worktree would leak its checkout")
	}
	for _, body := range postconditionsOf(t, codeExitPb, dev) {
		if !strings.Contains(body, "FS_WORKTREE") {
			t.Errorf("code-exit postcondition %q is not about this dispatch's own worktree", body)
		}
	}
	if pre := preconditionsOf(dev); len(pre) != 0 {
		t.Errorf("code-exit pre-teardown checks = %v, want none: neither a clean tree nor a merged branch can be made true here", pre)
	}
}
