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

const (
	capPlaybook      = "cap"
	devPlaybook      = "dev-task-prerequisites"
	herdrPlaybook    = "herdr"
	parPrePlaybook   = "parallel-prerequisites"
	parCleanPlaybook = "parallel-cleanup"
	intPrePlaybook   = "integrate-prerequisites"
	intCleanPlaybook = "integrate-cleanup"
)

// shippedPlaybooks is every playbook the binary ships. fs bootstrap seeds all
// of them; the cap's standing rules are built from the procedure ones.
var shippedPlaybooks = []string{
	capPlaybook, devPlaybook, herdrPlaybook, parPrePlaybook, parCleanPlaybook,
	intPrePlaybook, intCleanPlaybook,
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

	mine := "# mine: keep this line\nname: cap\ntype: procedure\nsteps:\n  - say: mine\n"
	writeKBPlaybook(t, home, capPlaybook, mine)

	seeded := playbooksOf(t, runFSOK(t, bin, dir, "bootstrap"))
	if seeded[capPlaybook]["action"] != "kept" {
		t.Errorf("%s action = %v, want kept", capPlaybook, seeded[capPlaybook]["action"])
	}
	if got := readRaw(t, filepath.Join(home, ".fs", "kb", capPlaybook+".yaml")); got != mine {
		t.Errorf("bootstrap modified an existing playbook:\ngot  %q\nwant %q", got, mine)
	}
	if seeded[devPlaybook]["action"] != "created" {
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
	devPath := filepath.Join(kbDir, devPlaybook+".yaml")
	writeRaw(t, devPath, readRaw(t, devPath)+"  - say: an added step\n")
	if state := playbooksOf(t, runFSOK(t, bin, dir, "kb", "list"))[devPlaybook]; state["state"] != "edited" {
		t.Errorf("an edited playbook = %v, want state edited", state)
	}

	// A playbook whose stamp matches its own body but not the shipped default
	// was seeded from a default that has since moved on: stale, not edited.
	older := "name: cap\ntype: procedure\nsteps:\n  - say: older\n"
	writeKBPlaybook(t, home, capPlaybook, fileStamp(older)+older)
	if state := playbooksOf(t, runFSOK(t, bin, dir, "kb", "list"))[capPlaybook]; state["state"] != "stale" {
		t.Errorf("a stale playbook = %v, want state stale", state)
	}

	// A hand-written playbook with no shipped default is local.
	writeKBPlaybook(t, home, "hand-written", "name: hand-written\ntype: procedure\nsteps:\n  - say: mine\n")
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
	edited := fileStamp(body) + strings.Replace(body, "close only with fs close, never by hand",
		"close however you like", 1)
	writeRaw(t, capPath, edited)

	diff := diffOf(capPlaybook)
	if diff == "" {
		t.Fatal("an edited playbook must diff non-empty")
	}
	for _, want := range []string{
		"-  - say: close only with fs close, never by hand",
		"+  - say: close however you like",
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

// fs kb prompt is the delivery path for the playbooks: the cap's standing rules
// come out as plain text on stdout, so a session starts from the rules the
// binary ships rather than from a prompt retyped by hand.
// The parallel gate must read the batch's cards from the dispatch that runs it,
// not from a list someone edits per batch: parallel-prerequisites.yaml runs
// bin/check-parallel.sh with $FS_CARDS, the variable fs dispatch --cards exports.
// This pins that wiring wherever the module is built, including where the repo's
// bin/ is out of reach; tests/check-parallel covers the script's own behaviour.
func TestShippedParallelGateReadsTheDispatchedCards(t *testing.T) {
	bin := getFS(t)
	writeKBPlaybook(t, testHome(t), parPrePlaybook, shippedDefault(t, parPrePlaybook))

	resp, code := runFS(t, bin, t.TempDir(), "kb", "get", parPrePlaybook)
	if code != 0 {
		t.Fatalf("kb get exit %d: %v", code, resp["error"])
	}

	const want = "bin/check-parallel.sh $FS_CARDS"
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

	// The standing-rules line, one stamped header per cap-triggered procedure,
	// and the rules themselves.
	for _, want := range []string{
		"Standing rules for this session",
		"not suggestions",
		"# cap — procedure, trigger cap; default, stamp sha256:",
		"# herdr — procedure, trigger cap; default, stamp sha256:",
		"pick up finished workers at the top of every turn",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("kb prompt must contain %q; got:\n%s", want, stdout)
		}
	}
	// A prerequisite playbook is a gate the dispatcher runs, not a standing rule.
	if strings.Contains(stdout, devPlaybook) {
		t.Errorf("a prerequisite playbook must not be part of the prompt:\n%s", stdout)
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

	older := "name: cap\ntype: procedure\ntrigger: cap\nsteps:\n  - say: older\n"
	writeKBPlaybook(t, home, capPlaybook, fileStamp(older)+older)

	stdout, _, code := rawFS(t, bin, dir, home, "kb", "prompt")
	if code != 0 {
		t.Fatalf("kb prompt exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "# cap — procedure, trigger cap; stale, stamp sha256:") {
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
