package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// invoke runs one command line against a scratch memstore and returns its exit
// code and decoded envelope. It is the whole CLI surface under test: flags in,
// JSON out, exit code out.
func invoke(t *testing.T, args ...string) (int, map[string]any) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("fs %s: stdout is not JSON: %v\n%s", strings.Join(args, " "), err, stdout.String())
	}
	return code, env
}

// scratch points the CLI at a throwaway store for the duration of one test.
func scratch(t *testing.T) {
	t.Helper()
	t.Setenv("MEMSTORE_DB", filepath.Join(t.TempDir(), "memory.db"))
	t.Setenv("HOME", t.TempDir()) // the log sink writes under ~/.fs/logs
}

// okData asserts a successful envelope and returns its data.
func okData(t *testing.T, code int, env map[string]any) map[string]any {
	t.Helper()
	if code != 0 {
		t.Fatalf("exit %d, want 0: %v", code, env)
	}
	if env["ok"] != true {
		t.Fatalf("ok = %v, want true: %v", env["ok"], env)
	}
	data, _ := env["data"].(map[string]any)
	if data == nil {
		t.Fatalf("no data in %v", env)
	}
	return data
}

// TestUsageExitsTwo: a malformed command line is a different answer from a refusal,
// and a script can tell them apart by the exit code alone.
func TestUsageExitsTwo(t *testing.T) {
	scratch(t)
	cases := [][]string{
		{},
		{"nonsense"},
		{"add", "--project"},        // a flag with no value
		{"claim", "--unknown-flag"}, // a flag fs does not have
		{"status", "extra"},
	}
	for _, args := range cases {
		code, env := invoke(t, args...)
		if code != 2 {
			t.Errorf("fs %v: exit %d, want 2", args, code)
		}
		if env["ok"] != false || env["error"] == "" {
			t.Errorf("fs %v: envelope = %v, want ok:false with an error", args, env)
		}
	}
}

// TestRefusalsExitOne: fs understood and refused. Zero writes, one JSON answer.
func TestRefusalsExitOne(t *testing.T) {
	scratch(t)
	cases := [][]string{
		{"add", "--project", "p", "--goal", "", "--check", "true"}, // no goal
		{"add", "--project", "", "--goal", "g", "--check", "true"}, // no scope
		{"add", "--project", "p", "--goal", "g"},                   // no declared check
		{"claim", "--project", "p"},                                // no session identity
		{"report", "--project", "p", "--session", "s", "--task", "t-1", "--state", "invented"},
		{"report", "--project", "p", "--session", "s", "--task", "t-1", "--state", "pass", "--command", "true"}, // no exit
		{"done", "--project", "p", "--session", "s", "--task", "t-1"},
	}
	for _, args := range cases {
		code, env := invoke(t, args...)
		if code != 1 {
			t.Errorf("fs %v: exit %d, want 1", args, code)
		}
		if env["ok"] != false {
			t.Errorf("fs %v: ok = %v, want false", args, env["ok"])
		}
	}
	// Nothing was written by any of them.
	code, env := invoke(t, "status")
	data := okData(t, code, env)
	if projects, _ := data["projects"].([]any); len(projects) != 0 {
		t.Fatalf("%d projects exist after every add was refused: %v", len(projects), projects)
	}
}

// TestFiveVerbsEndToOneCompletion walks the whole product once, through the CLI, and
// checks that only the reviewer's passing run closes anything.
func TestFiveVerbsEndToOneCompletion(t *testing.T) {
	scratch(t)

	add := mustOK(t, "add", "--project", "p", "--goal", "ship it", "--check", "go test ./...")
	dev, _ := add["task"].(string)
	if dev == "" {
		t.Fatalf("add returned no task id: %v", add)
	}

	claimed := mustOK(t, "claim", "--project", "p", "--session", "s-dev", "--task", dev)
	if claimed["session"] != "s-dev" || claimed["round"] != float64(1) {
		t.Fatalf("claim = %v, want s-dev at round 1", claimed)
	}
	// A second session finds nothing eligible: the only task is held.
	if code, env := invoke(t, "claim", "--project", "p", "--session", "s-other"); code != 1 {
		t.Fatalf("a second session claimed held work: %d %v", code, env)
	}

	mustOK(t, "report", "--project", "p", "--session", "s-dev", "--task", dev,
		"--state", "dev-done", "--note", "ready for review")

	// The developer cannot complete its own work.
	if code, env := invoke(t, "done", "--project", "p", "--session", "s-dev", "--task", dev); code != 1 {
		t.Fatalf("the developer completed its own task: %d %v", code, env)
	}

	review := mustOK(t, "add", "--project", "p", "--goal", "review "+dev, "--review-of", dev)
	rev, _ := review["task"].(string)
	if checks, _ := review["checks"].([]any); len(checks) != 1 || checks[0] != "go test ./..." {
		t.Fatalf("the review did not inherit the declared checks: %v", review)
	}
	mustOK(t, "claim", "--project", "p", "--session", "s-rev", "--task", rev)

	// No recorded run yet: refused.
	if code, env := invoke(t, "done", "--project", "p", "--session", "s-rev", "--task", rev); code != 1 {
		t.Fatalf("completion passed with no reviewer run: %d %v", code, env)
	}
	// A failing run: refused, and the exit code is the reason.
	if code, _ := invoke(t, "report", "--project", "p", "--session", "s-rev", "--task", rev,
		"--state", "fail", "--role", "review", "--command", "go test ./...", "--exit", "1"); code != 0 {
		t.Fatalf("a failing reviewer run was not recorded: %d", code)
	}
	if code, env := invoke(t, "done", "--project", "p", "--session", "s-rev", "--task", rev); code != 1 {
		t.Fatalf("completion passed on a failing run: %d %v", code, env)
	}
	// The passing run: completion.
	mustOK(t, "report", "--project", "p", "--session", "s-rev", "--task", rev,
		"--state", "pass", "--role", "review", "--command", "go test ./...", "--exit", "0")
	done := mustOK(t, "done", "--project", "p", "--session", "s-rev", "--task", rev)
	if done["dev"] != dev || done["review"] != rev {
		t.Fatalf("done = %v, want both tasks named", done)
	}
	if checks, _ := done["checks"].([]any); len(checks) != 1 {
		t.Fatalf("done recorded %d checks, want the one that closed it: %v", len(checks), done)
	}

	// Status agrees, from rows alone.
	status := mustOK(t, "status", "--project", "p")
	projects, _ := status["projects"].([]any)
	if len(projects) != 1 {
		t.Fatalf("status = %v, want one project", status)
	}
	states := map[string]string{}
	for _, raw := range projects[0].(map[string]any)["tasks"].([]any) {
		task := raw.(map[string]any)
		states[task["task"].(string)] = task["state"].(string)
	}
	if states[dev] != "done" || states[rev] != "done" {
		t.Fatalf("states = %v, want both done", states)
	}
}

// TestStorePathComesFromTheEnvironment: fs opens the shared file it is told to open,
// and nothing of its own.
func TestStorePathComesFromTheEnvironment(t *testing.T) {
	db := filepath.Join(t.TempDir(), "named.db")
	t.Setenv("MEMSTORE_DB", db)
	t.Setenv("HOME", t.TempDir())

	mustOK(t, "add", "--project", "p", "--goal", "g", "--check", "true")
	if _, err := os.Stat(db); err != nil {
		t.Fatalf("the named store was not created: %v", err)
	}
}

// mustOK invokes a command that has to succeed and returns its data.
func mustOK(t *testing.T, args ...string) map[string]any {
	t.Helper()
	code, env := invoke(t, args...)
	if code != 0 {
		t.Fatalf("fs %s: exit %d: %v", strings.Join(args, " "), code, env)
	}
	return okData(t, code, env)
}
