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

// shippedPlaybooks is every playbook the binary ships. fs bootstrap seeds all
// of them; the cap's standing rules are built from the cap-phase ones.
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

	mine := "# mine: keep this line\nname: cap\ncap:\n  - say: mine\n"
	writeKBPlaybook(t, home, capPlaybook, mine)

	seeded := playbooksOf(t, runFSOK(t, bin, dir, "bootstrap"))
	if seeded[capPlaybook]["action"] != "kept" {
		t.Errorf("%s action = %v, want kept", capPlaybook, seeded[capPlaybook]["action"])
	}
	if got := readRaw(t, filepath.Join(home, ".fs", "kb", capPlaybook+".yaml")); got != mine {
		t.Errorf("bootstrap modified an existing playbook:\ngot  %q\nwant %q", got, mine)
	}
	if seeded[codePb]["action"] != "created" {
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
	devPath := filepath.Join(kbDir, codePb+".yaml")
	writeRaw(t, devPath, readRaw(t, devPath)+"  - say: an added step\n")
	if state := playbooksOf(t, runFSOK(t, bin, dir, "kb", "list"))[codePb]; state["state"] != "edited" {
		t.Errorf("an edited playbook = %v, want state edited", state)
	}

	// A playbook whose stamp matches its own body but not the shipped default
	// was seeded from a default that has since moved on: stale, not edited.
	older := "name: cap\ncap:\n  - say: older\n"
	writeKBPlaybook(t, home, capPlaybook, fileStamp(older)+older)
	if state := playbooksOf(t, runFSOK(t, bin, dir, "kb", "list"))[capPlaybook]; state["state"] != "stale" {
		t.Errorf("a stale playbook = %v, want state stale", state)
	}

	// A hand-written playbook with no shipped default is local.
	writeKBPlaybook(t, home, "hand-written", "name: hand-written\nwork:\n  - say: mine\n")
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
// not from a list someone edits per batch: batch.yaml runs check-parallel.sh
// with $FS_CARDS, the variable fs dispatch --cards exports. The script is the
// binary's own, addressed as $FS_SCRIPTS/check-parallel.sh, so this pins the
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
		"# cap — moments cap; default, stamp sha256:",
		"# herdr — moments cap; default, stamp sha256:",
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

// A prompt built from a playbook whose shipped default has moved on says so, so
// the cap can tell its rules are stale without a second command.
func TestCLIKBPromptMarksAStalePlaybook(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	home := testHome(t)
	runFSOK(t, bin, dir, "bootstrap")

	older := "name: cap\ncap:\n  - say: older\n"
	writeKBPlaybook(t, home, capPlaybook, fileStamp(older)+older)

	stdout, _, code := rawFS(t, bin, dir, home, "kb", "prompt")
	if code != 0 {
		t.Fatalf("kb prompt exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "# cap — moments cap; stale, stamp sha256:") {
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
	if pre := preconditionsOf(memberSteps); len(pre) != 1 || pre[0] != "fs integrated $FS_NODE" {
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
	if pre := preconditionsOf(batchSteps); len(pre) != 1 || !strings.Contains(pre[0], "fs batch $FS_NODE") {
		t.Errorf("batch-exit's gate = %v, want the fs batch read alone", pre)
	}
	if actions := bodiesOf(batchSteps, "do"); len(actions) != 3 {
		t.Errorf("batch-exit declares %d teardown actions, want 3 (branch, worktree, workspace)", len(actions))
	}
	for _, body := range postconditionsOf(t, batchPb, batchSteps) {
		if !strings.Contains(body, "fs batch") {
			t.Errorf("batch-exit postcondition %q is not scoped to the batch's members", body)
		}
	}

	// The entry gate is member-scoped, and its compound ask is split: a rubric
	// cannot partition two questions asked as one.
	entry := bodiesOf(stepsOf(t, bin, integrationPb, "entry"), "check")
	if len(entry) != 1 || !strings.Contains(entry[0], "fs task get") || !strings.Contains(entry[0], "$FS_INTEGRATES") {
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
	for _, body := range postconditionsOf(t, codePb, dev) {
		if !strings.Contains(body, "FS_WORKTREE") {
			t.Errorf("code's exit postcondition %q is not about this dispatch's own worktree", body)
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
	if len(pre) != 1 || !strings.Contains(pre[0], `fs reviews "$FS_NODE"`) {
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
