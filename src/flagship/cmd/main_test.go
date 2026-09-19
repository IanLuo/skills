package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	fsBin     string
	fsBinOnce sync.Once
	fsBinErr  error
)

// getFS builds the fs binary once for all tests.
func getFS(t *testing.T) string {
	t.Helper()
	fsBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fs-test-bin-*")
		if err != nil {
			fsBinErr = err
			return
		}
		fsBin = filepath.Join(dir, "fs")
		cmd := exec.Command("go", "build", "-o", fsBin, ".")
		cmd.Dir = "."
		out, err := cmd.CombinedOutput()
		if err != nil {
			fsBinErr = fmt.Errorf("build failed: %s\n%s", err, out)
		}
	})
	if fsBinErr != nil {
		t.Fatal(fsBinErr)
	}
	return fsBin
}

// testHomes holds one isolated home per test, created on first use.
var testHomes sync.Map

// testHome returns the isolated home for this test. Every runFS call in a test
// shares it whatever working directory it runs in, while the real ~/.fs is
// never touched.
func testHome(t *testing.T) string {
	t.Helper()
	if home, ok := testHomes.Load(t); ok {
		return home.(string)
	}
	home, _ := testHomes.LoadOrStore(t, t.TempDir())
	t.Cleanup(func() { testHomes.Delete(t) })
	return home.(string)
}

// runFS runs the fs binary in a directory.
func runFS(t *testing.T, bin, dir string, args ...string) (map[string]any, int) {
	t.Helper()
	return runFSEnv(t, bin, dir, nil, args...)
}

// runFSEnv is runFS with extra environment variables (later entries win), for
// tests that shadow the herdr binary on PATH.
func runFSEnv(t *testing.T, bin, dir string, extraEnv []string, args ...string) (map[string]any, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "HOME="+testHome(t)), extraEnv...)
	out, err := cmd.Output()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
			// Prefer stdout (our JSON output) over stderr
			if len(out) == 0 {
				out = ee.Stderr
			}
		} else {
			t.Fatalf("exec: %v", err)
		}
	}
	var resp map[string]any
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal output %q: %v", string(out), err)
	}
	return resp, exitCode
}

func TestLogsGoToStderrNotStdout(t *testing.T) {
	// R1: Structured logs go to stderr (JSON), stdout is reserved for command output only.
	bin := getFS(t)
	dir := t.TempDir()

	cmd := exec.Command(bin, "project", "create", "--name", "logtest", "--root", dir)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+testHome(t))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	// Stdout should be valid JSON command output.
	var resp map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout: %s", err, stdout.String())
	}
	if resp["ok"] != true {
		t.Fatalf("command failed: %v", resp["error"])
	}

	// Stdout must not contain log lines.
	stdoutStr := stdout.String()
	if strings.Contains(stdoutStr, "\"level\"") {
		t.Errorf("stdout contains log lines — logs must go to stderr only\nstdout: %s", stdoutStr)
	}

	// Stderr should contain structured log output (JSON with "level" and "msg").
	stderrStr := stderr.String()
	if stderrStr == "" {
		t.Error("stderr is empty — expected structured log output")
	}
	if !strings.Contains(stderrStr, "\"level\"") {
		t.Errorf("stderr missing structured log fields\nstderr: %s", stderrStr)
	}
	if !strings.Contains(stderrStr, "\"msg\"") {
		t.Errorf("stderr missing msg field\nstderr: %s", stderrStr)
	}
}

func TestCLIProjectCreate(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	resp, code := runFS(t, bin, dir, "project", "create", "--name", "testproj", "--root", dir)
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}
	if resp["ok"] != true {
		t.Fatalf("not ok: %v", resp["error"])
	}
	// Per-project data lives in the global store, never in the repo.
	if _, err := os.Stat(filepath.Join(dir, ".fs")); !os.IsNotExist(err) {
		t.Fatalf("project dir must not hold fs data: %v", err)
	}
	if _, err := os.Stat(filepath.Join(testHome(t), ".fs", "store.db")); err != nil {
		t.Fatalf("global store not created: %v", err)
	}
}

// The store is shared and partitioned by project_id, and the project a command
// targets is the registered project owning the working directory.
func TestCLIStoreSharedAcrossProjects(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	other := t.TempDir()

	runFS(t, bin, dir, "project", "create", "--name", "demo")
	runFS(t, bin, dir, "task", "add", "--goal", "Implement auth")

	// Acting in the registered root targets "demo", not dir's basename.
	resp, code := runFS(t, bin, dir, "status")
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	if data["project_id"] != "demo" {
		t.Errorf("project_id = %v, want demo", data["project_id"])
	}
	if tasks := data["tasks"].([]any); len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	// The basename partition stays empty.
	resp, code = runFS(t, bin, dir, "status", "--project", filepath.Base(dir))
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}
	if tasks := resp["data"].(map[string]any)["tasks"]; tasks != nil {
		t.Errorf("basename partition should be empty, got %v", tasks)
	}

	// A different working directory reaches the same data by project name.
	resp, code = runFS(t, bin, other, "status", "--project", "demo")
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}
	tasks := resp["data"].(map[string]any)["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task from %s, got %d", other, len(tasks))
	}
}

// Parallel agents write to one store; no writer may fail on the write lock.
func TestCLIConcurrentWriters(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	runFS(t, bin, dir, "project", "create", "--name", "demo")

	const writers = 16
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(bin, "task", "add", "--goal", fmt.Sprintf("task %d", i))
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "HOME="+testHome(t))
			if out, err := cmd.CombinedOutput(); err != nil {
				errs[i] = fmt.Errorf("writer %d: %v: %s", i, err, out)
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Error(err)
		}
	}

	resp, code := runFS(t, bin, dir, "status")
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}
	if tasks := resp["data"].(map[string]any)["tasks"].([]any); len(tasks) != writers {
		t.Errorf("got %d tasks, want %d", len(tasks), writers)
	}
}

func TestCLITaskAdd(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)
	resp, code := runFS(t, bin, dir, "task", "add", "--goal", "implement auth", "--project", "p")
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	if data["node_id"] == "" {
		t.Error("empty node_id")
	}
}

func TestCLITaskUpdateAndStatus(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)
	addResp, _ := runFS(t, bin, dir, "task", "add", "--goal", "my task", "--project", "p")
	nodeID := addResp["data"].(map[string]any)["node_id"].(string)

	resp, code := runFS(t, bin, dir, "task", "update", nodeID, "--status", "active", "--project", "p")
	if code != 0 {
		t.Fatalf("update exit %d: %v", code, resp["error"])
	}

	statusResp, _ := runFS(t, bin, dir, "status", "--project", "p")
	data := statusResp["data"].(map[string]any)
	tasks := data["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	task := tasks[0].(map[string]any)
	if task["status"] != "active" {
		t.Errorf("status: %v", task["status"])
	}
}

func TestCLITaskEdit(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)
	addResp, _ := runFS(t, bin, dir, "task", "add", "--goal", "original goal", "--project", "p")
	nodeID := addResp["data"].(map[string]any)["node_id"].(string)

	resp, code := runFS(t, bin, dir, "task", "edit", nodeID, "--goal", "revised goal", "--project", "p")
	if code != 0 {
		t.Fatalf("edit exit %d: %v", code, resp["error"])
	}

	statusResp, _ := runFS(t, bin, dir, "status", "--project", "p")
	tasks := statusResp["data"].(map[string]any)["tasks"].([]any)
	task := tasks[0].(map[string]any)
	if task["goal"] != "revised goal" {
		t.Errorf("goal: %v", task["goal"])
	}
}

func TestCLIStatusDecision(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)
	addResp, _ := runFS(t, bin, dir, "task", "add", "--goal", "task", "--project", "p")
	nodeID := addResp["data"].(map[string]any)["node_id"].(string)

	runFS(t, bin, dir, "task", "update", nodeID, "--status", "done", "--decision", "chose JWT", "--project", "p")

	statusResp, _ := runFS(t, bin, dir, "status", "--project", "p")
	tasks := statusResp["data"].(map[string]any)["tasks"].([]any)
	task := tasks[0].(map[string]any)
	decisions := task["decisions"].([]any)
	if len(decisions) != 1 || decisions[0] != "chose JWT" {
		t.Errorf("decisions: %v", decisions)
	}
}

func TestCLIInvalidStatus(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)
	addResp, _ := runFS(t, bin, dir, "task", "add", "--goal", "task", "--project", "p")
	nodeID := addResp["data"].(map[string]any)["node_id"].(string)

	resp, code := runFS(t, bin, dir, "task", "update", nodeID, "--status", "invalid", "--project", "p")
	if code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
	if resp["ok"] != false {
		t.Error("should be not ok")
	}
}

func TestCLIQuery(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)
	runFS(t, bin, dir, "task", "add", "--goal", "implement authentication with JWT", "--project", "p")
	runFS(t, bin, dir, "task", "add", "--goal", "fix database migration", "--project", "p")

	resp, code := runFS(t, bin, dir, "query", "auth", "--project", "p")
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	events := data["events"].([]any)
	if len(events) < 1 {
		t.Fatal("expected at least 1 match for 'auth'")
	}
}

func TestCLIQueryNoTerm(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	_, code := runFS(t, bin, dir, "query", "--project", "p")
	if code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
}

func TestCLIQuery50EventSeed(t *testing.T) {
	// AC5: 50+ events, query returns accurate results.
	bin := getFS(t)
	dir := t.TempDir()

	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)
	// Seed 50+ events.
	for i := 0; i < 30; i++ {
		goal := fmt.Sprintf("database migration task %d", i)
		if i%6 == 0 {
			goal = fmt.Sprintf("authentication feature %d with OAuth tokens", i)
		}
		runFS(t, bin, dir, "task", "add", "--goal", goal, "--project", "p")
	}
	// Add some decisions.
	for i := 0; i < 21; i++ {
		addResp, _ := runFS(t, bin, dir, "task", "add", "--goal", fmt.Sprintf("extra task %d", i), "--project", "p")
		nid := addResp["data"].(map[string]any)["node_id"].(string)
		if i%7 == 0 {
			runFS(t, bin, dir, "task", "update", nid, "--decision", "chose authentication via JWT", "--status", "done", "--project", "p")
		}
	}

	resp, code := runFS(t, bin, dir, "query", "auth", "--project", "p")
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	events := data["events"].([]any)
	if len(events) < 5 {
		t.Errorf("expected at least 5 auth results, got %d", len(events))
	}
}

func TestCLILog(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)
	runFS(t, bin, dir, "task", "add", "--goal", "task one", "--project", "p")

	resp, code := runFS(t, bin, dir, "log", "--project", "p")
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	events := data["events"].([]any)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

func TestCLILogFilterByNode(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)
	addResp, _ := runFS(t, bin, dir, "task", "add", "--goal", "task one", "--project", "p")
	nodeID := addResp["data"].(map[string]any)["node_id"].(string)
	runFS(t, bin, dir, "task", "add", "--goal", "task two", "--project", "p")

	resp, code := runFS(t, bin, dir, "log", "--project", "p", "--node", nodeID)
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	events := data["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("expected 1 event for node, got %d", len(events))
	}
}

func TestCLILogFilterByType(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)
	runFS(t, bin, dir, "task", "add", "--goal", "task one", "--project", "p")

	resp, code := runFS(t, bin, dir, "log", "--project", "p", "--type", "task-created")
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	events := data["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("expected 1 task-created event, got %d", len(events))
	}
}

func TestCLITaskBlock(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)
	addResp, _ := runFS(t, bin, dir, "task", "add", "--goal", "task", "--project", "p")
	nodeID := addResp["data"].(map[string]any)["node_id"].(string)

	resp, code := runFS(t, bin, dir, "task", "block", nodeID, "--reason", "waiting on API", "--project", "p")
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}

	statusResp, _ := runFS(t, bin, dir, "status", "--project", "p")
	tasks := statusResp["data"].(map[string]any)["tasks"].([]any)
	task := tasks[0].(map[string]any)
	if task["status"] != "blocked" {
		t.Errorf("expected blocked, got %v", task["status"])
	}
}

func TestCLITaskUnblock(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)
	addResp, _ := runFS(t, bin, dir, "task", "add", "--goal", "task", "--project", "p")
	nodeID := addResp["data"].(map[string]any)["node_id"].(string)

	runFS(t, bin, dir, "task", "block", nodeID, "--reason", "waiting", "--project", "p")
	resp, code := runFS(t, bin, dir, "task", "unblock", nodeID, "--project", "p")
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}

	statusResp, _ := runFS(t, bin, dir, "status", "--project", "p")
	tasks := statusResp["data"].(map[string]any)["tasks"].([]any)
	task := tasks[0].(map[string]any)
	if task["status"] != "pending" {
		t.Errorf("expected pending, got %v", task["status"])
	}
}

func TestCLITaskKnowledge(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)
	addResp, _ := runFS(t, bin, dir, "task", "add", "--goal", "task", "--project", "p")
	nodeID := addResp["data"].(map[string]any)["node_id"].(string)

	resp, code := runFS(t, bin, dir, "task", "knowledge", nodeID, "--summary", "JWT expires in 1h", "--project", "p")
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	if data["event_type"] != "knowledge-added" {
		t.Errorf("event_type: %v", data["event_type"])
	}
}

func TestCLIMultipleTasksStatus(t *testing.T) {
	// AC13: multiple tasks active simultaneously
	bin := getFS(t)
	dir := t.TempDir()

	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)
	r1, _ := runFS(t, bin, dir, "task", "add", "--goal", "task one", "--project", "p")
	r2, _ := runFS(t, bin, dir, "task", "add", "--goal", "task two", "--project", "p")
	n1 := r1["data"].(map[string]any)["node_id"].(string)
	n2 := r2["data"].(map[string]any)["node_id"].(string)

	runFS(t, bin, dir, "task", "update", n1, "--status", "active", "--project", "p")
	runFS(t, bin, dir, "task", "update", n2, "--status", "active", "--project", "p")

	statusResp, _ := runFS(t, bin, dir, "status", "--project", "p")
	tasks := statusResp["data"].(map[string]any)["tasks"].([]any)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	for _, raw := range tasks {
		task := raw.(map[string]any)
		if task["status"] != "active" {
			t.Errorf("task %v not active: %v", task["node_id"], task["status"])
		}
	}
}

func writePlaybook(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCLIProjectList(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	runWithHome := func(args ...string) (map[string]any, int) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+testHome(t))
		out, err := cmd.Output()
		exitCode := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				exitCode = ee.ExitCode()
				if len(out) == 0 {
					out = ee.Stderr
				}
			} else {
				t.Fatalf("exec: %v", err)
			}
		}
		var resp map[string]any
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("unmarshal %q: %v", string(out), err)
		}
		return resp, exitCode
	}

	// List with no projects.
	resp, code := runWithHome("project", "list")
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	if data["projects"] != nil {
		if arr, ok := data["projects"].([]any); ok && len(arr) != 0 {
			t.Errorf("expected empty list, got %v", data["projects"])
		}
	}

	// Create two projects.
	dir2 := t.TempDir()
	runWithHome("project", "create", "--name", "alpha", "--root", dir)
	runWithHome("project", "create", "--name", "beta", "--root", dir2)

	// List returns both.
	resp, code = runWithHome("project", "list")
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}
	projects := resp["data"].(map[string]any)["projects"].([]any)
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}

	// Verify names present.
	names := map[string]bool{}
	for _, p := range projects {
		names[p.(map[string]any)["name"].(string)] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("missing project: %v", names)
	}
}

func TestCLIProjectGet(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	runWithHome := func(args ...string) (map[string]any, int) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+testHome(t))
		out, err := cmd.Output()
		exitCode := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				exitCode = ee.ExitCode()
				if len(out) == 0 {
					out = ee.Stderr
				}
			} else {
				t.Fatalf("exec: %v", err)
			}
		}
		var resp map[string]any
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("unmarshal %q: %v", string(out), err)
		}
		return resp, exitCode
	}

	// Create a project.
	runWithHome("project", "create", "--name", "myproj", "--root", dir)

	// Get it.
	resp, code := runWithHome("project", "get", "myproj")
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	if data["name"] != "myproj" {
		t.Errorf("name: %v", data["name"])
	}
	if data["root_path"] != dir {
		t.Errorf("root_path: %v", data["root_path"])
	}

	// Get nonexistent.
	resp, code = runWithHome("project", "get", "nope")
	if code != 1 {
		t.Errorf("expected exit 1 for nonexistent, got %d", code)
	}
}

func TestCLIProjectCreateRegisters(t *testing.T) {
	// fs project create should register in the global registry.
	bin := getFS(t)
	dir := t.TempDir()

	runWithHome := func(args ...string) (map[string]any, int) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+testHome(t))
		out, err := cmd.Output()
		exitCode := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				exitCode = ee.ExitCode()
				if len(out) == 0 {
					out = ee.Stderr
				}
			} else {
				t.Fatalf("exec: %v", err)
			}
		}
		var resp map[string]any
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("unmarshal %q: %v", string(out), err)
		}
		return resp, exitCode
	}

	// Create project.
	resp, code := runWithHome("project", "create", "--name", "registered", "--root", dir)
	if code != 0 {
		t.Fatalf("create exit %d: %v", code, resp["error"])
	}

	// Verify in registry via project get.
	resp, code = runWithHome("project", "get", "registered")
	if code != 0 {
		t.Fatalf("get exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	if data["name"] != "registered" {
		t.Errorf("name: %v", data["name"])
	}
}

func TestCLILastActivityUpdates(t *testing.T) {
	// Commands that target a project should update last_activity.
	bin := getFS(t)
	dir := t.TempDir()

	runWithHome := func(args ...string) (map[string]any, int) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+testHome(t))
		out, err := cmd.Output()
		exitCode := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				exitCode = ee.ExitCode()
				if len(out) == 0 {
					out = ee.Stderr
				}
			} else {
				t.Fatalf("exec: %v", err)
			}
		}
		var resp map[string]any
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("unmarshal %q: %v", string(out), err)
		}
		return resp, exitCode
	}

	// Create project.
	runWithHome("project", "create", "--name", "activity", "--root", dir)

	// Get initial activity.
	resp, _ := runWithHome("project", "get", "activity")
	initialActivity := resp["data"].(map[string]any)["last_activity"].(string)

	// Run a task add (which updates activity).
	addResp, _ := runWithHome("task", "add", "--goal", "test", "--project", "activity")
	nodeID := addResp["data"].(map[string]any)["node_id"].(string)
	_ = nodeID

	// Check activity updated.
	resp, _ = runWithHome("project", "get", "activity")
	newActivity := resp["data"].(map[string]any)["last_activity"].(string)
	if newActivity <= initialActivity {
		t.Errorf("last_activity not updated: %s <= %s", newActivity, initialActivity)
	}
}

func TestCLIHelpTopLevel(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	cmd := exec.Command(bin, "--help")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run() // exit 0 for help

	out := stderr.String()
	if !strings.Contains(out, "project") {
		t.Errorf("--help missing 'project': %s", out)
	}
	if !strings.Contains(out, "task") {
		t.Errorf("--help missing 'task': %s", out)
	}
	if !strings.Contains(out, "kb") {
		t.Errorf("--help missing 'kb': %s", out)
	}
}

// Every subcommand answers --help / -h / help with its usage on stderr, exit 0,
// and no state touched at all — not even the ~/.fs directory.
func TestCLIHelpSubcommands(t *testing.T) {
	bin := getFS(t)

	for _, sub := range []string{"project", "task", "status", "query", "log", "kb", "dispatch", "close", "unfinished", "bootstrap"} {
		t.Run(sub, func(t *testing.T) {
			for _, form := range []string{"--help", "-h", "help"} {
				home := t.TempDir()
				dir := t.TempDir()
				stdout, stderr, code := rawFS(t, bin, dir, home, sub, form)
				if code != 0 {
					t.Fatalf("%s %s exit = %d, want 0 (stderr: %s)", sub, form, code, stderr)
				}
				if !strings.Contains(stderr, sub) {
					t.Errorf("%s %s usage must name it; stderr = %q", sub, form, stderr)
				}
				if stdout != "" {
					t.Errorf("%s %s wrote to stdout: %q", sub, form, stdout)
				}
				if _, err := os.Stat(filepath.Join(home, ".fs")); !os.IsNotExist(err) {
					t.Errorf("%s %s must not create ~/.fs: %v", sub, form, err)
				}
			}
		})
	}
}

// rawFS runs the fs binary and returns stdout, stderr, and the exit code, for
// probes whose output is not the JSON envelope (--help writes usage to stderr).
func rawFS(t *testing.T, bin, dir, home string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+home)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("exec: %v", err)
		}
	}
	return stdout.String(), stderr.String(), code
}

// gitRepo initializes a git repo at dir with one commit and returns its HEAD
// sha, so a probe can tell one project's HEAD from another's.
func gitRepo(t *testing.T, dir, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "seed.txt"},
		{"-c", "user.email=t@example.com", "-c", "user.name=Test", "commit", "-q", "-m", msg},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return gitHead(t, dir)
}

// gitHead returns the HEAD sha of the repo at dir.
func gitHead(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// A typo'd flag must not be read as success: it is refused with exit 2, naming
// the flag, and records nothing.
func TestCLIRejectsUnknownFlags(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)

	resp, code := runFS(t, bin, dir, "dispatch", "--project", "p", "--type", "dev-task", "--goal", "x", "--deliverr")
	if code != 2 {
		t.Fatalf("exit = %d, want 2: %v", code, resp)
	}
	if resp["ok"] != false {
		t.Errorf("ok = %v, want false", resp["ok"])
	}
	if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "--deliverr") {
		t.Errorf("error %q must name the offending flag", errMsg)
	}

	// The refused dispatch recorded nothing (and split no pane).
	capResp, code := runFS(t, bin, dir, "log", "--project", "cap")
	if code != 0 {
		t.Fatalf("log cap exit %d: %v", code, capResp["error"])
	}
	if events := capResp["data"].(map[string]any)["events"]; events != nil {
		t.Errorf("an unknown flag must record nothing, got %v", events)
	}

	probes := map[string][]string{
		"status":         {"status", "--bogus"},
		"query":          {"query", "term", "--bogus"},
		"log":            {"log", "--bogus"},
		"task add":       {"task", "add", "--goal", "g", "--bogus"},
		"project create": {"project", "create", "--name", "q", "--bogus"},
		"kb add":         {"kb", "add", "--name", "n", "--file", "f", "--bogus"},
		"kb diff":        {"kb", "diff", "n", "--bogus"},
		"kb prompt":      {"kb", "prompt", "--bogus"},
		"kb reset":       {"kb", "reset", "n", "--bogus"},
		"bootstrap":      {"bootstrap", "--bogus"},
		"close":          {"close", "--node", "n", "--bogus"},
		"unfinished":     {"unfinished", "--bogus"},
	}
	for name, args := range probes {
		resp, code := runFS(t, bin, dir, args...)
		if code != 2 {
			t.Errorf("%s: exit = %d, want 2: %v", name, code, resp)
		}
		if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "--bogus") {
			t.Errorf("%s: error %q must name --bogus", name, errMsg)
		}
	}
}

// A stray positional is refused with exit 2 where the subcommand accepts none,
// and the subcommands that do take positionals keep working.
func TestCLIRejectsStrayPositionals(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)

	resp, code := runFS(t, bin, dir, "status", "bogus", "--project", "p")
	if code != 2 {
		t.Fatalf("exit = %d, want 2: %v", code, resp)
	}
	if resp["ok"] != false {
		t.Errorf("ok = %v, want false", resp["ok"])
	}
	if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "bogus") {
		t.Errorf("error %q must name the stray argument", errMsg)
	}

	// Positional-taking subcommands keep working unchanged.
	addResp, code := runFS(t, bin, dir, "task", "add", "--goal", "positional probe", "--project", "p")
	if code != 0 {
		t.Fatalf("task add exit %d: %v", code, addResp["error"])
	}
	nodeID := addResp["data"].(map[string]any)["node_id"].(string)
	if _, code := runFS(t, bin, dir, "task", "update", nodeID, "--status", "active", "--project", "p"); code != 0 {
		t.Errorf("task update exit %d, want 0", code)
	}
	if _, code := runFS(t, bin, dir, "query", "positional", "--project", "p"); code != 0 {
		t.Errorf("query exit %d, want 0", code)
	}
	if _, code := runFS(t, bin, dir, "project", "get", "p"); code != 0 {
		t.Errorf("project get exit %d, want 0", code)
	}
}

// An unregistered working directory must not silently invent a scope: the
// derived scope is refused with exit 1, and an explicit --project still works —
// including the unregistered cap scope.
func TestCLIUnregisteredDirectoryRefusesDerivedScope(t *testing.T) {
	bin := getFS(t)
	registered := t.TempDir()
	runFS(t, bin, registered, "project", "create", "--name", "p", "--root", registered)

	nowhere := t.TempDir()
	resp, code := runFS(t, bin, nowhere, "status")
	if code != 1 {
		t.Fatalf("exit = %d, want 1: %v", code, resp)
	}
	if resp["ok"] != false {
		t.Errorf("ok = %v, want false", resp["ok"])
	}
	errMsg, _ := resp["error"].(string)
	for _, want := range []string{nowhere, "--project", "fs project create"} {
		if !strings.Contains(errMsg, want) {
			t.Errorf("error %q must mention %q", errMsg, want)
		}
	}

	// No partition named after the directory was invented.
	logResp, code := runFS(t, bin, nowhere, "log", "--project", filepath.Base(nowhere))
	if code != 0 {
		t.Fatalf("log exit %d: %v", code, logResp["error"])
	}
	if events := logResp["data"].(map[string]any)["events"]; events != nil {
		t.Errorf("derived-scope refusal must not invent a partition, got %v", events)
	}

	// An explicit --project is always allowed, registered or not: cap is
	// unregistered by design and must not be locked out of its own table.
	for _, project := range []string{"p", "cap"} {
		resp, code := runFS(t, bin, nowhere, "status", "--project", project)
		if code != 0 {
			t.Errorf("status --project %s exit %d: %v", project, code, resp["error"])
		}
	}
}

// commit_sha describes the project the event is about, not the terminal's
// location: it is read from the resolved project's root_path.
func TestCLICommitSHAFromProjectRoot(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	want := gitRepo(t, root, "root commit")
	runFS(t, bin, root, "project", "create", "--name", "p", "--root", root)

	other := t.TempDir()
	otherSHA := gitRepo(t, other, "other commit")
	if otherSHA == want {
		t.Fatalf("probe repos must differ; both are %s", want)
	}

	addResp, code := runFS(t, bin, other, "task", "add", "--goal", "sha probe", "--project", "p")
	if code != 0 {
		t.Fatalf("task add exit %d: %v", code, addResp["error"])
	}
	data := addResp["data"].(map[string]any)
	if sha, _ := data["commit_sha"].(string); sha != want {
		t.Errorf("task-created commit_sha = %q, want the project root's %q", sha, want)
	}
	nodeID := data["node_id"].(string)

	logResp, code := runFS(t, bin, other, "log", "--project", "p", "--node", nodeID)
	if code != 0 {
		t.Fatalf("log exit %d: %v", code, logResp["error"])
	}
	events := logResp["data"].(map[string]any)["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("log returned %d events, want 1", len(events))
	}
	if sha, _ := events[0].(map[string]any)["commit_sha"].(string); sha != want {
		t.Errorf("event commit_sha = %q, want %q (not the cwd repo's %q)", sha, want, otherSHA)
	}
}

// fs status must carry the knowledge recorded on a node; a node without
// knowledge must not gain an empty key.
func TestCLIStatusCarriesKnowledge(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()
	runFS(t, bin, dir, "project", "create", "--name", "p", "--root", dir)

	addResp, _ := runFS(t, bin, dir, "task", "add", "--goal", "k probe", "--project", "p")
	nodeID := addResp["data"].(map[string]any)["node_id"].(string)
	plainResp, _ := runFS(t, bin, dir, "task", "add", "--goal", "plain probe", "--project", "p")
	plainID := plainResp["data"].(map[string]any)["node_id"].(string)

	want := "JWT refresh tokens expire after 7 days"
	if _, code := runFS(t, bin, dir, "task", "knowledge", nodeID, "--summary", want, "--project", "p"); code != 0 {
		t.Fatalf("task knowledge exit %d", code)
	}

	resp, code := runFS(t, bin, dir, "status", "--project", "p")
	if code != 0 {
		t.Fatalf("status exit %d: %v", code, resp["error"])
	}
	byID := map[string]map[string]any{}
	for _, raw := range resp["data"].(map[string]any)["tasks"].([]any) {
		task := raw.(map[string]any)
		byID[task["node_id"].(string)] = task
	}
	knowledge, ok := byID[nodeID]["knowledge"].([]any)
	if !ok || len(knowledge) != 1 || knowledge[0] != want {
		t.Errorf("node knowledge = %v, want [%q]", byID[nodeID]["knowledge"], want)
	}
	if _, ok := byID[plainID]["knowledge"]; ok {
		t.Errorf("a node with no knowledge must not carry a knowledge key: %v", byID[plainID])
	}
}

func TestCLIKBAddGetListEditRemove(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	playbook := `name: dev-prereqs
type: prerequisite
trigger: dev-task
steps:
  - check: test -f docs/prd.md
  - check: test -f docs/arch.md
`
	pbFile := writePlaybook(t, dir, "dev-prereqs", playbook)

	// Helper to run with HOME override.
	runKB := func(args ...string) (map[string]any, int) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+testHome(t))
		out, err := cmd.Output()
		exitCode := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				exitCode = ee.ExitCode()
				if len(out) == 0 {
					out = ee.Stderr
				}
			} else {
				t.Fatalf("exec: %v", err)
			}
		}
		var resp map[string]any
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("unmarshal %q: %v", string(out), err)
		}
		return resp, exitCode
	}

	// kb list — empty.
	resp, code := runKB("kb", "list")
	if code != 0 {
		t.Fatalf("list exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	if pbs := data["playbooks"]; pbs != nil {
		if arr, ok := pbs.([]any); ok && len(arr) != 0 {
			t.Errorf("expected empty list, got %v", pbs)
		}
	}

	// kb add.
	resp, code = runKB("kb", "add", "--name", "dev-prereqs", "--file", pbFile)
	if code != 0 {
		t.Fatalf("add exit %d: %v", code, resp["error"])
	}

	// kb get.
	resp, code = runKB("kb", "get", "dev-prereqs")
	if code != 0 {
		t.Fatalf("get exit %d: %v", code, resp["error"])
	}
	pbData := resp["data"].(map[string]any)
	if pbData["name"] != "dev-prereqs" {
		t.Errorf("name: %v", pbData["name"])
	}
	if pbData["trigger"] != "dev-task" {
		t.Errorf("trigger: %v", pbData["trigger"])
	}

	// kb list — one item.
	resp, code = runKB("kb", "list")
	if code != 0 {
		t.Fatalf("list exit %d: %v", code, resp["error"])
	}
	pbs := resp["data"].(map[string]any)["playbooks"].([]any)
	if len(pbs) != 1 {
		t.Fatalf("expected 1 playbook, got %d", len(pbs))
	}

	// kb edit.
	updated := `name: dev-prereqs
type: prerequisite
trigger: dev-task
steps:
  - check: test -f docs/prd.md
  - check: test -f docs/arch.md
  - check: go vet ./...
`
	updatedFile := writePlaybook(t, dir, "dev-prereqs-updated", updated)
	resp, code = runKB("kb", "edit", "--name", "dev-prereqs", "--file", updatedFile)
	if code != 0 {
		t.Fatalf("edit exit %d: %v", code, resp["error"])
	}

	// Verify edit.
	resp, _ = runKB("kb", "get", "dev-prereqs")
	steps := resp["data"].(map[string]any)["steps"].([]any)
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps after edit, got %d", len(steps))
	}

	// kb remove.
	resp, code = runKB("kb", "remove", "dev-prereqs")
	if code != 0 {
		t.Fatalf("remove exit %d: %v", code, resp["error"])
	}

	// Verify removed.
	resp, code = runKB("kb", "get", "dev-prereqs")
	if code != 1 {
		t.Errorf("expected exit 1 after remove, got %d", code)
	}
}

// fs kb add refuses an invalid playbook, naming the offending step and the
// kinds its type allows.
func TestCLIKBAddRefusesInvalidPlaybooks(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	cases := []struct {
		name string
		pb   string
		want string
	}{
		{
			name: "prereq-say",
			pb: `name: prereq-say
type: prerequisite
trigger: t
steps:
  - check: test -f AGENTS.md
  - say: read AGENTS.md before editing
`,
			want: "read AGENTS.md before editing",
		},
		{
			name: "proc-ask",
			pb: `name: proc-ask
type: procedure
trigger: t
steps:
  - ask: is the acceptance check stated?
`,
			want: "allowed kinds: say, check",
		},
		{
			name: "route-two",
			pb: `name: route-two
type: routing
trigger: t
steps:
  - say: dev-task
  - say: review-task
`,
			want: "review-task",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pbFile := writePlaybook(t, dir, tc.name, tc.pb)
			resp, code := runFS(t, bin, dir, "kb", "add", "--name", tc.name, "--file", pbFile)
			if code != 1 {
				t.Fatalf("add exit = %d, want 1: %v", code, resp)
			}
			if resp["ok"] != false {
				t.Errorf("ok = %v, want false", resp["ok"])
			}
			errMsg, _ := resp["error"].(string)
			if !strings.Contains(errMsg, tc.want) {
				t.Errorf("error %q must name %q", errMsg, tc.want)
			}
		})
	}
}

const dispatchPlaybook = `name: dev-task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - check: test -f AGENTS.md
  - check: grep -rl -e 'specs:locked' -e 'design:locked' --include='*.md' .
  - ask: a locked PRD is present, and it names this one deliverable
  - ask: is the acceptance check for this deliverable stated
`

// fs dispatch prepares the brief from the playbook and the project root, and
// records the cap's node in the cap scope without registering "cap".
func TestCLIDispatchPreparesBrief(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# agents\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "prd.md"), []byte("<!-- specs:locked: prd -->\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)

	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", dispatchPlaybook)

	resp, code := runFS(t, bin, root, "dispatch", "--project", "skills", "--type", "dev-task", "--goal", "sample")
	if code != 0 {
		t.Fatalf("exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)

	checklist := data["checklist"].([]any)
	if len(checklist) != 4 {
		t.Fatalf("checklist has %d items, want the 2 checks + 2 asks", len(checklist))
	}
	byBody := map[string]map[string]any{}
	for _, raw := range checklist {
		item := raw.(map[string]any)
		byBody[item["body"].(string)] = item
	}
	agentsCheck, ok := byBody["test -f AGENTS.md"]
	if !ok {
		t.Fatalf("AGENTS.md check missing from the checklist: %v", byBody)
	}
	if agentsCheck["kind"] != "check" || agentsCheck["status"] != "pass" {
		t.Errorf("AGENTS.md check = %v, want a passing check", agentsCheck)
	}
	grepBody := "grep -rl -e 'specs:locked' -e 'design:locked' --include='*.md' ."
	if grepCheck, ok := byBody[grepBody]; !ok {
		t.Fatalf("locked-doc check missing from the checklist: %v", byBody)
	} else if grepCheck["status"] != "pass" {
		t.Errorf("locked-doc check = %v, want pass", grepCheck)
	} else if out, _ := grepCheck["output"].(string); !strings.Contains(out, filepath.Join("docs", "prd.md")) {
		t.Errorf("locked-doc check output = %q, want the matched doc", out)
	}

	locked := data["locked_docs"].([]any)
	if len(locked) != 1 || locked[0] != filepath.Join("docs", "prd.md") {
		t.Errorf("locked_docs = %v, want [docs/prd.md]", locked)
	}

	if cmd := data["next_command"].(string); !strings.Contains(cmd, "herdr agent prompt") {
		t.Errorf("next_command missing the herdr prompt step: %q", cmd)
	}

	// The cap node lives in the cap scope, and cap is not a registered project.
	statusResp, code := runFS(t, bin, root, "status", "--project", "cap")
	if code != 0 {
		t.Fatalf("status cap exit %d: %v", code, statusResp["error"])
	}
	capData := statusResp["data"].(map[string]any)
	if capData["project_id"] != "cap" {
		t.Errorf("cap scope project_id = %v", capData["project_id"])
	}
	tasks := capData["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("cap scope has %d tasks, want 1", len(tasks))
	}
	task := tasks[0].(map[string]any)
	if task["goal"] != "dispatch dev-task: sample" {
		t.Errorf("cap task goal = %v", task["goal"])
	}
	decisions := task["decisions"].([]any)
	if len(decisions) != 1 || !strings.Contains(decisions[0].(string), "skills") {
		t.Errorf("cap task decisions = %v, want the target project named", decisions)
	}

	resp, code = runFS(t, bin, root, "project", "get", "cap")
	if code == 0 {
		t.Errorf("cap must never be registered as a project, got %v", resp)
	}
}

// The parallel gate is what --cards exists for. The shipped
// parallel-prerequisites playbook runs the repo's real check-parallel.sh with
// $FS_CARDS, so the batch's disjointness is checked mechanically against *this*
// dispatch's cards — the one thing a static shell string could not do.
//
// The shipped playbook is used as shipped, so a gate that stopped reading
// $FS_CARDS would fail here rather than pass on a test-local stand-in.
func TestCLIDispatchCardsReachTheParallelGate(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	goGit(t, root, "init", "-q")
	installRepoFile(t, root, filepath.Join("..", "..", "..", "bin", "check-parallel.sh"), "bin/check-parallel.sh", 0o755)
	writeFileIn(t, root, "a.md", "# A\n\n## Files\nsrc/a.go\n")
	writeFileIn(t, root, "b.md", "# B\n\n## Files\nsrc/b.go\n")
	writeFileIn(t, root, "c.md", "# C\n\n## Files\nsrc/a.go\n")

	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), parPrePlaybook, shippedDefault(t, parPrePlaybook))
	gateBody := shippedStep(t, shippedDefault(t, parPrePlaybook), "check", "$FS_CARDS")
	env, _, _ := fakeHerdrOnPath(t)

	// Disjoint cards pass, and the gate's output says what it intersected.
	resp, code := runFSEnv(t, bin, root, env, "dispatch", "--project", "skills", "--type", "parallel", "--goal", "batch", "--cards", "a.md,b.md")
	if code != 0 {
		t.Fatalf("disjoint cards must pass the gate, exit %d: %v", code, resp["error"])
	}
	if out := checkOutputOf(t, resp, gateBody); !strings.Contains(out, "2 cards, no overlapping paths") {
		t.Errorf("gate output = %q, want the two cards it checked", out)
	}

	// --cards is repeatable, and the two forms name the same batch.
	resolveCapScope(t, bin, root)
	repeated, code := runFSEnv(t, bin, root, env, "dispatch", "--project", "skills", "--type", "parallel", "--goal", "batch", "--cards", "a.md", "--cards", "b.md")
	if code != 0 {
		t.Fatalf("--cards twice must name the same batch, exit %d: %v", code, repeated["error"])
	}
	if out := checkOutputOf(t, repeated, gateBody); !strings.Contains(out, "2 cards") {
		t.Errorf("gate output = %q, want two cards", out)
	}

	// Overlapping cards are refused, and the refusal names the shared path.
	resolveCapScope(t, bin, root)
	overlap, code := runFSEnv(t, bin, root, env, "dispatch", "--project", "skills", "--type", "parallel", "--goal", "batch", "--cards", "a.md,c.md")
	if code != 1 {
		t.Fatalf("overlapping cards must be refused, exit %d: %v", code, overlap)
	}
	errMsg, _ := overlap["error"].(string)
	for _, want := range []string{"src/a.go", "a.md", "c.md"} {
		if !strings.Contains(errMsg, want) {
			t.Errorf("refusal %q must name %q", errMsg, want)
		}
	}

	// With no cards the gate refuses instead of passing vacuously, and the
	// message names the flag the caller forgot.
	resolveCapScope(t, bin, root)
	noCards, code := runFSEnv(t, bin, root, env, "dispatch", "--project", "skills", "--type", "parallel", "--goal", "batch")
	if code != 1 {
		t.Fatalf("a gate with no cards must refuse, exit %d: %v", code, noCards)
	}
	errMsg, _ = noCards["error"].(string)
	if !strings.Contains(errMsg, "--cards") {
		t.Errorf("refusal %q must name --cards", errMsg)
	}
}

// checkOutputOf returns the output of the checklist item with the given body.
func checkOutputOf(t *testing.T, resp map[string]any, body string) string {
	t.Helper()
	for _, raw := range resp["data"].(map[string]any)["checklist"].([]any) {
		if item := raw.(map[string]any); item["body"] == body {
			out, _ := item["output"].(string)
			return out
		}
	}
	t.Fatalf("checklist has no item %q: %v", body, resp["data"])
	return ""
}

// resolveCapScope marks every open cap node done, so the next dispatch is not
// gated by the previous one — tests here dispatch several times into one store.
func resolveCapScope(t *testing.T, bin, root string) {
	t.Helper()
	for _, raw := range scopeTasks(t, bin, root, "cap") {
		task := raw.(map[string]any)
		if task["status"] == "done" {
			continue
		}
		if _, code := runFS(t, bin, root, "task", "update", task["node_id"].(string), "--status", "done", "--decision", "test cleanup", "--project", "cap"); code != 0 {
			t.Fatalf("resolving cap node %v failed", task["node_id"])
		}
	}
}

// installRepoFile copies a file out of the repo into root at rel, so a check
// that runs from root runs the artifact the repo ships rather than a stand-in.
//
// The repo's bin/ is outside this module, so it is absent when the module is
// built on its own — the nix check phase runs the tests from a copy of
// src/flagship. There the test skips rather than failing on the sandbox's shape;
// TestShippedParallelGateReadsTheDispatchedCards covers the wiring wherever the
// module is built, and tests/check-parallel covers the script itself.
func installRepoFile(t *testing.T, root, src, rel string, mode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("the repo's %s is not reachable from this module copy (%v)", rel, err)
	}
	dst := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, mode); err != nil {
		t.Fatal(err)
	}
}

// writeFileIn writes a file under root, creating its parent directories.
func writeFileIn(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// goGit runs git in dir. The prerequisite gate asks whether the root is a repo,
// so a dispatch test needs one.
func goGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// gitOut runs git in dir and returns its trimmed stdout — for `status
// --porcelain`, where an empty answer is the assertion.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// A missing playbook fails cleanly and names the file the cap must supply.
func TestCLIDispatchMissingPlaybook(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)

	resp, code := runFS(t, bin, root, "dispatch", "--project", "skills", "--type", "no-such-type", "--goal", "x")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if resp["ok"] != false {
		t.Errorf("ok = %v, want false", resp["ok"])
	}
	errMsg, _ := resp["error"].(string)
	if !strings.Contains(errMsg, "no-such-type-prerequisites") {
		t.Errorf("error must name the missing playbook, got %q", errMsg)
	}
}

// writeKBPlaybook installs a playbook in the isolated home's knowledge center.
func writeKBPlaybook(t *testing.T, home, name, content string) {
	t.Helper()
	kbDir := filepath.Join(home, ".fs", "kb")
	if err := os.MkdirAll(kbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kbDir, name+".yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// scopeTasks returns every task in a scope, via fs status.
func scopeTasks(t *testing.T, bin, dir, project string) []any {
	t.Helper()
	resp, code := runFS(t, bin, dir, "status", "--project", project)
	if code != 0 {
		t.Fatalf("status %s exit %d: %v", project, code, resp["error"])
	}
	tasks, _ := resp["data"].(map[string]any)["tasks"].([]any)
	return tasks
}

// scopeTaskByID returns one node from a scope, via fs status.
func scopeTaskByID(t *testing.T, bin, dir, project, nodeID string) map[string]any {
	t.Helper()
	for _, raw := range scopeTasks(t, bin, dir, project) {
		if task := raw.(map[string]any); task["node_id"] == nodeID {
			return task
		}
	}
	t.Fatalf("node %s not found in scope %s", nodeID, project)
	return nil
}

// capTask returns the cap-scope task with the given goal, via fs status.
func capTask(t *testing.T, bin, dir, goal string) map[string]any {
	t.Helper()
	tasks := scopeTasks(t, bin, dir, "cap")
	for _, raw := range tasks {
		if task := raw.(map[string]any); task["goal"] == goal {
			return task
		}
	}
	t.Fatalf("cap node with goal %q not found in %v", goal, tasks)
	return nil
}

func containsString(values []any, want string) bool {
	for _, v := range values {
		if s, ok := v.(string); ok && s == want {
			return true
		}
	}
	return false
}

// fs unfinished reads scopes from the store: the implicit cap scope and a scope
// that was never registered both report, and done nodes are omitted.
func TestCLIUnfinishedListsEveryScopeFromTheStore(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)

	doneResp, code := runFS(t, bin, root, "task", "add", "--goal", "finished thing", "--project", "skills")
	if code != 0 {
		t.Fatalf("task add exit %d: %v", code, doneResp["error"])
	}
	doneID := doneResp["data"].(map[string]any)["node_id"].(string)
	runFS(t, bin, root, "task", "update", doneID, "--status", "done", "--decision", "closed", "--project", "skills")

	runFS(t, bin, root, "task", "add", "--goal", "open thing", "--project", "skills")
	runFS(t, bin, root, "task", "add", "--goal", "cap backlog", "--project", "cap")
	runFS(t, bin, root, "task", "add", "--goal", "orphan thing", "--project", "never-registered")

	resp, code := runFS(t, bin, root, "unfinished")
	if code != 0 {
		t.Fatalf("unfinished exit %d: %v", code, resp["error"])
	}
	unfinished := resp["data"].(map[string]any)["unfinished"].([]any)

	byGoal := map[string]map[string]any{}
	for _, raw := range unfinished {
		node := raw.(map[string]any)
		byGoal[node["goal"].(string)] = node
	}
	if _, ok := byGoal["finished thing"]; ok {
		t.Errorf("a done node must not be reported as unfinished: %v", unfinished)
	}
	for goal, project := range map[string]string{
		"open thing":   "skills",
		"cap backlog":  "cap",
		"orphan thing": "never-registered",
	} {
		node, ok := byGoal[goal]
		if !ok {
			t.Errorf("unfinished %q missing from %v", goal, unfinished)
			continue
		}
		if node["project_id"] != project {
			t.Errorf("%q project_id = %v, want %q", goal, node["project_id"], project)
		}
	}

	// The unregistered scope proves the registry is not the source: it lists in
	// fs unfinished but project get cannot find it.
	if _, code := runFS(t, bin, root, "project", "get", "never-registered"); code == 0 {
		t.Error("never-registered must not be in the registry")
	}
}

// A failing check stops the run at that check, and --confirm overrides it while
// recording the override on the cap node.
func TestCLIDispatchFailsFastThenConfirm(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	sentinel := filepath.Join(t.TempDir(), "dispatch-check3-ran")
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", fmt.Sprintf(`name: dev-task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - check: true
  - check: false
  - check: touch %s
`, sentinel))

	resp, code := runFS(t, bin, root, "dispatch", "--project", "skills", "--type", "dev-task", "--goal", "sample")
	if code != 1 {
		t.Fatalf("exit = %d, want 1: %v", code, resp)
	}
	if resp["ok"] != false {
		t.Errorf("ok = %v, want false", resp["ok"])
	}
	errMsg, _ := resp["error"].(string)
	for _, want := range []string{"prerequisite check failed", `"false"`, "tell the user", "--confirm"} {
		if !strings.Contains(errMsg, want) {
			t.Errorf("error %q must mention %q", errMsg, want)
		}
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("check 3 ran despite check 2 failing: %v", err)
	}
	// A refusal records nothing, so the confirm below is not gated by it.
	if capResp, _ := runFS(t, bin, root, "status", "--project", "cap"); capResp["data"].(map[string]any)["tasks"] != nil {
		t.Errorf("a refusal must not record a cap node: %v", capResp)
	}

	resp, code = runFS(t, bin, root, "dispatch", "--confirm", "--project", "skills", "--type", "dev-task", "--goal", "sample")
	if code != 0 {
		t.Fatalf("--confirm exit = %d: %v", code, resp["error"])
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("--confirm must run every check; check 3 did not run: %v", err)
	}

	task := capTask(t, bin, root, "dispatch dev-task: sample")
	want := "user confirmed proceeding past a failing check: false"
	if !containsString(task["decisions"].([]any), want) {
		t.Errorf("cap decisions = %v, want %q", task["decisions"], want)
	}

	// Clean up: an unresolved dispatch node makes later dispatches refuse.
	nodeID := resp["data"].(map[string]any)["cap_node_id"].(string)
	runFS(t, bin, root, "task", "update", nodeID, "--status", "done", "--decision", "test cleanup", "--project", "cap")
}

// An unresolved dispatch stops the next one, and --confirm overrides it while
// naming the dispatch it left open.
func TestCLIDispatchRefusesWhileUnresolvedDispatchExists(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", `name: dev-task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - check: true
`)

	first, code := runFS(t, bin, root, "dispatch", "--project", "skills", "--type", "dev-task", "--goal", "first")
	if code != 0 {
		t.Fatalf("first dispatch exit %d: %v", code, first["error"])
	}
	firstID := first["data"].(map[string]any)["cap_node_id"].(string)

	resp, code := runFS(t, bin, root, "dispatch", "--project", "skills", "--type", "dev-task", "--goal", "second")
	if code != 1 {
		t.Fatalf("exit = %d, want 1: %v", code, resp)
	}
	if resp["ok"] != false {
		t.Errorf("ok = %v, want false", resp["ok"])
	}
	errMsg, _ := resp["error"].(string)
	for _, want := range []string{"unresolved dispatches", firstID, "--confirm"} {
		if !strings.Contains(errMsg, want) {
			t.Errorf("error %q must mention %q", errMsg, want)
		}
	}

	confirmed, code := runFS(t, bin, root, "dispatch", "--confirm", "--project", "skills", "--type", "dev-task", "--goal", "second")
	if code != 0 {
		t.Fatalf("--confirm exit = %d: %v", code, confirmed["error"])
	}
	task := capTask(t, bin, root, "dispatch dev-task: second")
	want := "user confirmed proceeding with unresolved dispatches: " + firstID
	if !containsString(task["decisions"].([]any), want) {
		t.Errorf("cap decisions = %v, want %q", task["decisions"], want)
	}

	// Clean up both dispatch nodes.
	secondID := confirmed["data"].(map[string]any)["cap_node_id"].(string)
	for _, id := range []string{firstID, secondID} {
		runFS(t, bin, root, "task", "update", id, "--status", "done", "--decision", "test cleanup", "--project", "cap")
	}
}

// --- fs dispatch --deliver and fs close -----------------------------------
//
// The tests below shadow the herdr binary on PATH with fakeHerdrScript, so the
// dispatch lifecycle runs end to end — split, start, prompt, record, close —
// without a live terminal. Pane state lives in $HERDR_TEST_STATE.

const herdrTestPlaybook = `name: dev-task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - check: true
`

// noCheckPlaybook has no check step, so dispatch needs nothing from PATH. Used
// to reach --deliver with herdr deliberately missing.
const noCheckPlaybook = `name: dev-task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - ask: is this one deliverable
`

const fakeHerdrScript = `#!/usr/bin/env bash
# A fake herdr for CLI tests: models pane and agent lifecycle in
# $HERDR_TEST_STATE so the deliver/close paths can be exercised without a
# terminal.
#
# A delivery splits a sibling pane of the cap's own, so the fake anchors every
# split to the cap's workspace and tab ($HERDR_TEST_WORKSPACE / $HERDR_TEST_TAB)
# and models no tab creation at all: a delivery that tried to create one would
# hit the unknown-command branch and fail.
set -euo pipefail
state="${HERDR_TEST_STATE:?HERDR_TEST_STATE is required}"
mkdir -p "$state"

cmd="${1:-}"; shift || true
sub="${1:-}"; shift || true

id_file() { printf '%s/%s_%s' "$state" "$1" "${2//:/_}"; }
not_found() { printf '{"error":{"code":"%s_not_found","message":"%s %s not found"}}\n' "$1" "$1" "$2" >&2; exit 1; }

case "$cmd $sub" in
  "pane split")
    n=$(( $(cat "$state/seq" 2>/dev/null || echo 0) + 1 ))
    printf '%s' "$n" > "$state/seq"
    # Keep the split's arguments: which pane it was anchored to is the point.
    printf '%s' "$*" > "$state/split_args"
    ws="${HERDR_TEST_WORKSPACE:-wTEST}"; tab="${HERDR_TEST_TAB:-wTEST:t0}"
    pane="$ws:p$n"
    # The pane's directory is kept, so a probe can read back where a worker
    # actually starts rather than inferring it from the arguments.
    cwd=""
    while [ $# -gt 0 ]; do
      if [ "$1" = "--cwd" ]; then cwd="${2:-}"; shift 2 || true; else shift; fi
    done
    printf '%s' "$cwd" > "$(id_file pane "$pane")"
    printf '{"result":{"pane":{"pane_id":"%s","tab_id":"%s","workspace_id":"%s"}}}\n' "$pane" "$tab" "$ws"
    ;;
  "pane get")
    id="${1:-}"
    [ -f "$(id_file pane "$id")" ] || not_found pane "$id"
    cwd="$(cat "$(id_file pane "$id")" 2>/dev/null || true)"
    printf '{"result":{"pane":{"pane_id":"%s","cwd":"%s","foreground_cwd":"%s"}}}\n' "$id" "$cwd" "$cwd"
    ;;
  "workspace get")
    # A worktree workspace is named by HERDR_TEST_WORKTREE_WS; anything else is
    # simply not there, the way a typo'd workspace id is not.
    id="${1:-}"
    [ "$id" = "${HERDR_TEST_WORKTREE_WS:-}" ] || not_found workspace "$id"
    printf '{"result":{"workspace":{"workspace_id":"%s","worktree":{"checkout_path":"%s","is_linked_worktree":true}}}}\n' "$id" "$HERDR_TEST_WORKTREE_CWD"
    ;;
  "pane list")
    ws=""
    while [ $# -gt 0 ]; do
      if [ "$1" = "--workspace" ]; then ws="${2:-}"; shift 2 || true; else shift; fi
    done
    [ "$ws" = "${HERDR_TEST_WORKTREE_WS:-}" ] || not_found workspace "$ws"
    # A worktree workspace has its root pane from the moment herdr made it, at
    # the worktree's own checkout — no split needed to run a worker in it.
    pane="$ws:p1"
    printf '%s' "$HERDR_TEST_WORKTREE_CWD" > "$(id_file pane "$pane")"
    printf '{"result":{"panes":[{"pane_id":"%s","tab_id":"%s:t1","workspace_id":"%s","cwd":"%s","foreground_cwd":"%s"}]}}\n' "$pane" "$ws" "$ws" "$HERDR_TEST_WORKTREE_CWD" "$HERDR_TEST_WORKTREE_CWD"
    ;;
  "worktree list")
    printf '{"result":{"worktrees":[]}}\n'
    ;;
  "pane close")
    id="${1:-}"
    [ -f "$(id_file pane "$id")" ] || not_found pane "$id"
    # Closing a pane kills the agents in it. The worker's node is then the only
    # trace left, which is exactly what fs pending has to report as gone.
    for f in "$state"/agent_*; do
      [ -e "$f" ] || continue
      read -r pane _ < "$f"
      if [ "$pane" = "$id" ]; then rm -f "$f"; fi
    done
    rm -f "$(id_file pane "$id")"
    printf '{"result":{"closed":true}}\n'
    ;;
  "worktree remove")
    # Close-out teardown removes the worktree the record names; which one it
    # removed is kept, so a test can assert it.
    ws=""
    while [ $# -gt 0 ]; do
      if [ "$1" = "--workspace" ]; then ws="${2:-}"; shift 2 || true; else shift; fi
    done
    # herdr's failure codes are modelled on request, so a gate that reads one as
    # proof the worktree is gone can be exercised. not_git_worktree says the
    # *caller* is not inside a git work tree, which is a statement about this
    # environment and not about the worktree the record names.
    if [ -n "${HERDR_TEST_WORKTREE_REMOVE_CODE:-}" ]; then
      printf '{"error":{"code":"%s","message":"%s"}}\n' "$HERDR_TEST_WORKTREE_REMOVE_CODE" "$HERDR_TEST_WORKTREE_REMOVE_CODE" >&2
      exit 1
    fi
    [ "$ws" = "${HERDR_TEST_WORKTREE_WS:-}" ] || not_found workspace "$ws"
    printf '%s' "$ws" > "$state/worktree_removed"
    printf '{"result":{"removed":true}}\n'
    ;;
  "agent start")
    # args: <name> --kind <kind> --pane <pane>
    name="${1:-}"; shift || true
    pane=""
    while [ $# -gt 0 ]; do
      if [ "$1" = "--pane" ]; then pane="${2:-}"; shift 2 || true; else shift; fi
    done
    [ -f "$(id_file pane "$pane")" ] || not_found pane "$pane"
    # The status is recorded at start and reported by agent list later; a test
    # pins it with HERDR_TEST_AGENT_STATE (working by default).
    printf '%s %s\n' "$pane" "${HERDR_TEST_AGENT_STATE:-working}" > "$state/agent_$name"
    printf '{"result":{"agent":{"name":"%s","pane_id":"%s"}}}\n' "$name" "$pane"
    ;;
  "agent get")
    name="${1:-}"
    f="$state/agent_$name"
    [ -f "$f" ] || not_found agent "$name"
    read -r pane status < "$f"
    printf '{"result":{"agent":{"name":"%s","pane_id":"%s","agent_status":"%s"}}}\n' "$name" "$pane" "$status"
    ;;
  "agent list")
    printf '{"result":{"agents":['
    first=1
    for f in "$state"/agent_*; do
      [ -e "$f" ] || continue
      name="${f##*/agent_}"
      read -r pane status < "$f"
      [ "$first" -eq 1 ] || printf ','
      first=0
      printf '{"name":"%s","pane_id":"%s","agent_status":"%s"}' "$name" "$pane" "$status"
    done
    printf ']}}\n'
    ;;
  "agent prompt")
    # Keep the brief, so a probe can read what the worker was handed.
    printf '%s' "${2:-}" > "$state/prompt"
    printf '{"result":{}}\n'
    ;;
  *)
    printf '{"error":{"code":"unknown_command","message":"%s %s"}}\n' "$cmd" "$sub" >&2
    exit 2
    ;;
esac
`

// fakeHerdrOnPath installs the fake herdr first on PATH and returns the
// environment, its pane-state directory, and the script path.
func fakeHerdrOnPath(t *testing.T) (env []string, state, script string) {
	t.Helper()
	binDir := t.TempDir()
	script = filepath.Join(binDir, "herdr")
	if err := os.WriteFile(script, []byte(fakeHerdrScript), 0o755); err != nil {
		t.Fatal(err)
	}
	state = t.TempDir()
	env = []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HERDR_TEST_STATE=" + state,
	}
	return env, state, script
}

// herdrGet runs the fake herdr's get for one object, standing in for the real
// `herdr pane get` / `herdr tab get` the acceptance check runs. A non-nil error
// means the object no longer exists.
func herdrGet(t *testing.T, script string, env []string, kind, id string) error {
	t.Helper()
	_, err := herdrRun(t, script, env, kind, "get", id)
	return err
}

// herdrRun runs the fake herdr and returns its stdout.
func herdrRun(t *testing.T, script string, env []string, args ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command(script, args...)
	cmd.Env = append(os.Environ(), env...)
	return cmd.Output()
}

// herdrAgentPane returns the pane herdr says an agent is in.
func herdrAgentPane(t *testing.T, script string, env []string, name string) string {
	t.Helper()
	out, err := herdrRun(t, script, env, "agent", "get", name)
	if err != nil {
		t.Fatalf("herdr agent get %s: %v", name, err)
	}
	var resp struct {
		Result struct {
			Agent struct {
				PaneID string `json:"pane_id"`
			} `json:"agent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("herdr agent get %s: %v", name, err)
	}
	return resp.Result.Agent.PaneID
}

// herdrPrompt returns the brief the fake herdr was handed, standing in for what
// the worker pane received.
func herdrPrompt(t *testing.T, state string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join(state, "prompt"))
	if err != nil {
		t.Fatalf("reading the prompted brief: %v", err)
	}
	return string(text)
}

// herdrPaneProbe returns the cwd herdr reports for a pane, standing in for the
// acceptance check's `herdr pane get <pane> → foreground_cwd`. The cwd is read
// back from herdr rather than inferred from the arguments fs passed, because
// the directory the worker actually starts in is the whole point.
func herdrPaneProbe(t *testing.T, script string, env []string, paneID string) (cwd, foregroundCWD string) {
	t.Helper()
	out, err := herdrRun(t, script, env, "pane", "get", paneID)
	if err != nil {
		t.Fatalf("herdr pane get %s: %v", paneID, err)
	}
	var resp struct {
		Result struct {
			Pane struct {
				CWD           string `json:"cwd"`
				ForegroundCWD string `json:"foreground_cwd"`
			} `json:"pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("herdr pane get %s: %v", paneID, err)
	}
	return resp.Result.Pane.CWD, resp.Result.Pane.ForegroundCWD
}

// herdrSplitArgs returns the arguments the fake herdr's last pane split was
// called with, so a test can assert which pane it was anchored to.
func herdrSplitArgs(t *testing.T, state string) string {
	t.Helper()
	args, err := os.ReadFile(filepath.Join(state, "split_args"))
	if err != nil {
		t.Fatalf("reading the split arguments: %v", err)
	}
	return string(args)
}

// deliverProbe runs fs dispatch --deliver against the fake herdr and returns the
// cap node, the tab and pane it was bound to, and the worker node it created.
func deliverProbe(t *testing.T, bin, root string, env []string) (nodeID, tabID, paneID, workerRef string) {
	t.Helper()
	return deliverProbeGoal(t, bin, root, env, "sample")
}

// deliverProbeGoal is deliverProbe with the goal and extra dispatch flags named,
// for a second concurrent dispatch (which needs --confirm to get past the
// unresolved-dispatch gate).
func deliverProbeGoal(t *testing.T, bin, root string, env []string, goal string, extra ...string) (nodeID, tabID, paneID, workerRef string) {
	t.Helper()
	args := append([]string{"dispatch", "--deliver", "--project", "skills", "--type", "dev-task", "--goal", goal}, extra...)
	resp, code := runFSEnv(t, bin, root, env, args...)
	if code != 0 {
		t.Fatalf("dispatch --deliver exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	delivery, ok := data["delivery"].(map[string]any)
	if !ok {
		t.Fatalf("dispatch --deliver carried no delivery: %v", data)
	}
	worker, ok := data["worker_node"].(string)
	if !ok || worker == "" {
		t.Fatalf("dispatch --deliver carried no worker_node: %v", data)
	}
	tab, _ := delivery["tab_id"].(string)
	return data["cap_node_id"].(string), tab, delivery["pane_id"].(string), worker
}

// finishWorker marks the delivered worker's node done, so the close-out gate has
// something to sign off.
func finishWorker(t *testing.T, bin, root, workerRef string) {
	t.Helper()
	_, nodeID, ok := strings.Cut(workerRef, ":")
	if !ok {
		t.Fatalf("worker %q is not PROJECT:NODE", workerRef)
	}
	if _, code := runFS(t, bin, root, "task", "update", nodeID, "--status", "done", "--decision", "worker finished", "--project", "skills"); code != 0 {
		t.Fatalf("marking worker %s done failed", workerRef)
	}
}

// addWorkerNode adds a skills-scope node with the given status and returns it.
func addWorkerNode(t *testing.T, bin, root, status string) string {
	t.Helper()
	resp, code := runFS(t, bin, root, "task", "add", "--goal", "worker node", "--project", "skills")
	if code != 0 {
		t.Fatalf("task add exit %d: %v", code, resp["error"])
	}
	nodeID := resp["data"].(map[string]any)["node_id"].(string)
	if status != "pending" {
		if _, code := runFS(t, bin, root, "task", "update", nodeID, "--status", status, "--project", "skills"); code != 0 {
			t.Fatalf("task update to %s failed", status)
		}
	}
	return nodeID
}

// fs dispatch --deliver splits a sibling pane of the cap's own, creates the
// worker's node in the target project, names it in the brief, and binds pane and
// node in the delivery record; fs close reads that binding, so the cap never has
// to say which node it is closing.
func TestCLIDeliverSplitsASiblingPaneAndCloseTearsItDown(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", herdrTestPlaybook)
	env, state, script := fakeHerdrOnPath(t)

	capNode, tabID, paneID, workerRef := deliverProbe(t, bin, root, env)
	if paneID == "" {
		t.Fatal("delivery carried no pane id")
	}
	// The pane is a split of the cap's own, so it lands in the cap's tab: the
	// delivery creates no tab at all. The split is anchored with --current, not
	// with a pane id, which is what keeps it inside the cap's workspace.
	if tabID != "wTEST:t0" {
		t.Errorf("delivery tab = %q, want the cap's own wTEST:t0", tabID)
	}
	if args := herdrSplitArgs(t, state); !strings.Contains(args, "--current") ||
		!strings.Contains(args, "--direction right") || !strings.Contains(args, "--no-focus") {
		t.Errorf("split was called with %q, want a --current right split with no focus", args)
	}
	if err := herdrGet(t, script, env, "pane", paneID); err != nil {
		t.Errorf("pane %s does not exist after --deliver: %v", paneID, err)
	}

	// The worker's node exists in the target project, pending, for the worker to
	// pick up — the cap does not create it, and neither does the worker.
	workerID := strings.TrimPrefix(workerRef, "skills:")
	if task := scopeTaskByID(t, bin, root, "skills", workerID); task["status"] != "pending" {
		t.Errorf("worker node status = %v, want pending", task["status"])
	}

	// The brief handed to the worker names that node and says not to invent one.
	if prompt := herdrPrompt(t, state); !strings.Contains(prompt, "Note your work on "+workerRef) {
		t.Errorf("the brief must name %s; got:\n%s", workerRef, prompt)
	}

	// The binding is structural: fs log returns the pane and the worker node as
	// parseable JSON.
	logResp, code := runFS(t, bin, root, "log", "--node", capNode, "--type", "delivery-recorded", "--project", "cap")
	if code != 0 {
		t.Fatalf("log exit %d: %v", code, logResp["error"])
	}
	events := logResp["data"].(map[string]any)["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("delivery-recorded events = %v, want exactly 1", events)
	}
	payload := events[0].(map[string]any)["payload"].(map[string]any)
	for field, want := range map[string]any{
		"pane_id": paneID, "tab_id": tabID, "agent": "dispatch-dev-task-" + workerID,
		"engine": "herdr", "project": "skills", "node": workerID,
	} {
		if payload[field] != want {
			t.Errorf("delivery payload %s = %v, want %v", field, payload[field], want)
		}
	}

	if task := capTask(t, bin, root, "dispatch dev-task: sample"); task["status"] != "active" {
		t.Errorf("cap node status = %v, want active once delivered", task["status"])
	}

	// Closing needs no --worker: the recorded node is what the gate checks.
	finishWorker(t, bin, root, workerRef)
	closed, code := runFSEnv(t, bin, root, env, "close", "--node", capNode, "--decision", "read the worker node; verified")
	if code != 0 {
		t.Fatalf("close exit %d: %v", code, closed["error"])
	}
	data := closed["data"].(map[string]any)
	if data["pane_id"] != paneID {
		t.Errorf("close data = %v, want the closed pane %s", data, paneID)
	}
	if data["worker"] != workerRef {
		t.Errorf("close worker = %v, want the recorded %s", data["worker"], workerRef)
	}

	task := capTask(t, bin, root, "dispatch dev-task: sample")
	if task["status"] != "done" {
		t.Errorf("cap node status = %v, want done", task["status"])
	}
	if !containsString(task["decisions"].([]any), "read the worker node; verified") {
		t.Errorf("cap decisions = %v, want the verdict", task["decisions"])
	}

	// The worker's pane is gone. Its tab is the cap's own and is untouched —
	// nothing in fs can close a tab, which is the strongest form of that.
	if err := herdrGet(t, script, env, "pane", paneID); err == nil {
		t.Errorf("pane %s still exists after fs close", paneID)
	}

	// A pane that is already closed is not an error: close out again, with no
	// warning — herdr reporting not_found is success, not a failure.
	again, code := runFSEnv(t, bin, root, env, "close", "--node", capNode, "--decision", "read the worker node; verified")
	if code != 0 {
		t.Fatalf("closing an already-closed dispatch must succeed, exit %d: %v", code, again["error"])
	}
	if warning, ok := again["data"].(map[string]any)["warning"]; ok {
		t.Errorf("an already-closed pane must not warn, got %v", warning)
	}
}

// A worktree dispatch starts the worker inside the worktree, not the main
// checkout. The worktree workspace already has a root pane at the worktree's own
// checkout, so fs binds the worker to that pane and splits nothing: a split of
// the cap's pane would inherit the cap's directory, run the worker in the main
// checkout, and still be recorded as isolated.
func TestCLIDeliverInAWorktreeStartsTheWorkerInTheWorktreeRootPane(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", herdrTestPlaybook)
	worktree := t.TempDir()
	env, state, script := fakeHerdrOnPath(t)
	env = append(env, "HERDR_TEST_WORKTREE_WS=wWT", "HERDR_TEST_WORKTREE_CWD="+worktree)

	capNode, tabID, paneID, workerRef := deliverProbeGoal(t, bin, root, env, "sample", "--worktree", "wWT")

	// The binding is the worktree's own root pane, not a new one.
	if paneID != "wWT:p1" || tabID != "wWT:t1" {
		t.Errorf("delivery bound pane %s tab %s, want the worktree's own wWT:p1 in wWT:t1", paneID, tabID)
	}
	if _, err := os.Stat(filepath.Join(state, "split_args")); !os.IsNotExist(err) {
		t.Errorf("a worktree dispatch split a pane: %s", herdrSplitArgs(t, state))
	}

	// The worker is in that pane, and that pane's cwd is the worktree — asserted
	// from herdr, not inferred.
	agent := "dispatch-dev-task-" + strings.TrimPrefix(workerRef, "skills:")
	if got := herdrAgentPane(t, script, env, agent); got != paneID {
		t.Errorf("agent %s is in pane %s, want %s", agent, got, paneID)
	}
	cwd, foreground := herdrPaneProbe(t, script, env, paneID)
	if cwd != worktree || foreground != worktree {
		t.Errorf("worker pane cwd = %q (foreground %q), want the worktree %s", cwd, foreground, worktree)
	}
	if foreground == root {
		t.Errorf("the worker started in the main checkout %s; that is the bug this fixes", root)
	}

	// The record still carries the worktree, which is what close removes.
	logResp, code := runFS(t, bin, root, "log", "--node", capNode, "--type", "delivery-recorded", "--project", "cap")
	if code != 0 {
		t.Fatalf("log exit %d: %v", code, logResp["error"])
	}
	events := logResp["data"].(map[string]any)["events"].([]any)
	payload := events[0].(map[string]any)["payload"].(map[string]any)
	if payload["worktree"] != "wWT" || payload["pane_id"] != paneID {
		t.Errorf("delivery payload = %v, want the worktree wWT bound to %s", payload, paneID)
	}
}

// The cleanup gate's checks run in the worktree the dispatch worked in, not the
// main checkout. This is the live failure in miniature: the project root is
// dirty and the worktree is clean and merged, so the shipped checks pass in the
// worktree and would fail in the root. The dirtiness is asserted at the time, so
// the test proves what it claims rather than assuming it.
func TestCLICloseRunsCleanupChecksInTheWorktreeNotTheMainCheckout(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", herdrTestPlaybook)
	// The shipped parallel-cleanup checks, verbatim: no uncommitted files, and
	// the branch merged into main.
	writeKBPlaybook(t, testHome(t), "dev-task-cleanup", `name: dev-task-cleanup
type: cleanup
trigger: dev-task
steps:
  - check: test -z "$(git status --porcelain)"
  - check: git merge-base --is-ancestor HEAD main
`)

	// A real repository with a real linked worktree, so the checks shell out to
	// git exactly as they do live.
	goGit(t, root, "init", "-q", "-b", "main")
	writeFileIn(t, root, "seed.txt", "seed\n")
	goGit(t, root, "add", "seed.txt")
	goGit(t, root, "-c", "user.email=t@example.com", "-c", "user.name=Test", "commit", "-q", "-m", "seed")
	worktree := filepath.Join(t.TempDir(), "wt")
	goGit(t, root, "worktree", "add", "-q", worktree, "-b", "batch-test-X")

	writeFileIn(t, root, "uncommitted.txt", "another agent's work\n")
	if dirty := gitOut(t, root, "status", "--porcelain"); !strings.Contains(dirty, "uncommitted.txt") {
		t.Fatalf("the main checkout is not dirty (%q); the test would prove nothing", dirty)
	}
	if dirty := gitOut(t, worktree, "status", "--porcelain"); dirty != "" {
		t.Fatalf("the worktree is not clean (%q); the test would prove nothing", dirty)
	}

	env, state, _ := fakeHerdrOnPath(t)
	env = append(env, "HERDR_TEST_WORKTREE_WS=wWT", "HERDR_TEST_WORKTREE_CWD="+worktree)
	capNode, _, _, workerRef := deliverProbeGoal(t, bin, root, env, "sample", "--worktree", "wWT")
	finishWorker(t, bin, root, workerRef)

	closed, code := runFSEnv(t, bin, root, env, "close", "--node", capNode, "--decision", "verified")
	if code != 0 {
		t.Fatalf("close exit %d: %v — the checks pass in the worktree even though the main checkout is dirty", code, closed["error"])
	}
	data := closed["data"].(map[string]any)
	if data["cleanup"] != "cleanup gate dev-task-cleanup: 2 checks passed in worktree wWT" {
		t.Errorf("cleanup = %v, want the checks reported and the tree they ran in", data["cleanup"])
	}
	// Teardown still removes the worktree the record names, so the fix leaves
	// the exit path exactly as it was.
	if removed, err := os.ReadFile(filepath.Join(state, "worktree_removed")); err != nil || string(removed) != "wWT" {
		t.Errorf("worktree removed = %q (err %v), want wWT", removed, err)
	}
}

// realWorktree turns root into a real git repository with a real linked worktree
// and returns the checkout path plus the path git itself reports for it: git
// resolves symlinks, the delivery record holds what herdr reported.
func realWorktree(t *testing.T, root string) (worktree, resolved string) {
	t.Helper()
	goGit(t, root, "init", "-q", "-b", "main")
	writeFileIn(t, root, "seed.txt", "seed\n")
	goGit(t, root, "add", "seed.txt")
	goGit(t, root, "-c", "user.email=t@example.com", "-c", "user.name=Test", "commit", "-q", "-m", "seed")
	worktree = filepath.Join(t.TempDir(), "wt")
	goGit(t, root, "worktree", "add", "-q", worktree, "-b", "batch-test-a")

	var err error
	if resolved, err = filepath.EvalSymlinks(worktree); err != nil {
		t.Fatalf("resolve worktree %s: %v", worktree, err)
	}
	return worktree, resolved
}

// assertWorktreeListed is the precondition the removal tests assert at the time,
// so a close that passes proves something rather than assuming the leak.
func assertWorktreeListed(t *testing.T, root, resolved string) {
	t.Helper()
	if listed := gitOut(t, root, "worktree", "list", "--porcelain"); !strings.Contains(listed, resolved) {
		t.Fatalf("git does not list %s before close; the test would prove nothing:\n%s", resolved, listed)
	}
}

// assertWorktreeGone is what "the worktree is gone" means: the checkout is gone
// from disk, and git no longer lists it.
func assertWorktreeGone(t *testing.T, root, worktree, resolved string) {
	t.Helper()
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Errorf("worktree %s is still on disk (stat err = %v)", worktree, err)
	}
	if listed := gitOut(t, root, "worktree", "list", "--porcelain"); strings.Contains(listed, resolved) {
		t.Errorf("git still lists the worktree %s:\n%s", resolved, listed)
	}
}

// The acceptance check this whole path exists for: a dispatch runs in a real
// worktree, close tears it down, and the checkout is gone afterwards. The fake
// herdr reports the workspace removed and touches nothing on disk, so a close
// that trusted that report would leave the worktree exactly where it was.
func TestCLICloseRemovesTheWorktreeHerdrReportsRemoved(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", herdrTestPlaybook)
	worktree, resolved := realWorktree(t, root)
	assertWorktreeListed(t, root, resolved)

	env, _, _ := fakeHerdrOnPath(t)
	env = append(env, "HERDR_TEST_WORKTREE_WS=wWT", "HERDR_TEST_WORKTREE_CWD="+worktree)

	delivered, code := runFSEnv(t, bin, root, env,
		"dispatch", "--deliver", "--project", "skills", "--type", "dev-task", "--goal", "sample", "--worktree", "wWT")
	if code != 0 {
		t.Fatalf("dispatch --deliver exit %d: %v", code, delivered["error"])
	}
	data := delivered["data"].(map[string]any)
	// The record keeps the checkout path, not only the workspace id: the id is
	// what herdr can forget before close, and the path is what close needs then.
	if path, _ := data["delivery"].(map[string]any)["worktree_path"].(string); path != worktree {
		t.Errorf("delivery worktree_path = %q, want the worktree %s", path, worktree)
	}
	finishWorker(t, bin, root, data["worker_node"].(string))

	closed, code := runFSEnv(t, bin, root, env, "close", "--node", data["cap_node_id"].(string), "--decision", "verified")
	if code != 0 {
		t.Fatalf("close exit %d: %v", code, closed["error"])
	}
	if warning := closed["data"].(map[string]any)["warning"]; warning != nil {
		t.Errorf("warning = %v, want none: the worktree is verified gone", warning)
	}
	assertWorktreeGone(t, root, worktree, resolved)
}

// The state that leaked two checkouts: herdr no longer knows the workspace at
// all — its agent having ended — while the git worktree is still there. Every
// herdr call about it reports workspace_not_found, so the close runs without the
// worktree's environment variables: the recorded path is what keeps the teardown
// possible, and the git worktree is removed anyway.
func TestCLICloseRemovesTheWorktreeWhenHerdrForgotTheWorkspace(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", herdrTestPlaybook)
	worktree, resolved := realWorktree(t, root)
	assertWorktreeListed(t, root, resolved)

	env, _, _ := fakeHerdrOnPath(t)
	deliverEnv := append(append([]string{}, env...), "HERDR_TEST_WORKTREE_WS=wWT", "HERDR_TEST_WORKTREE_CWD="+worktree)
	capNode, _, _, workerRef := deliverProbeGoal(t, bin, root, deliverEnv, "sample", "--worktree", "wWT")
	finishWorker(t, bin, root, workerRef)

	closed, code := runFSEnv(t, bin, root, env, "close", "--node", capNode, "--decision", "verified")
	if code != 0 {
		t.Fatalf("close exit %d: %v — a forgotten workspace must not cost the close its footing", code, closed["error"])
	}
	if warning := closed["data"].(map[string]any)["warning"]; warning != nil {
		t.Errorf("warning = %v, want none: the worktree is verified gone", warning)
	}
	assertWorktreeGone(t, root, worktree, resolved)
}

// A worktree git will not remove — here an untracked file — is a warning naming
// the path, and the close still succeeds: the cap can act on what it is told,
// and a leak is never silent.
func TestCLICloseWarnsWhenTheWorktreeCannotBeRemoved(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", herdrTestPlaybook)
	worktree, resolved := realWorktree(t, root)
	// Untracked files make `git worktree remove` refuse without --force, which is
	// the whole point: the teardown reports it rather than forcing past it.
	writeFileIn(t, worktree, "untracked.txt", "left behind\n")

	env, _, _ := fakeHerdrOnPath(t)
	env = append(env, "HERDR_TEST_WORKTREE_WS=wWT", "HERDR_TEST_WORKTREE_CWD="+worktree)
	capNode, _, _, workerRef := deliverProbeGoal(t, bin, root, env, "sample", "--worktree", "wWT")
	finishWorker(t, bin, root, workerRef)

	closed, code := runFSEnv(t, bin, root, env, "close", "--node", capNode, "--decision", "verified")
	if code != 0 {
		t.Fatalf("close exit %d: %v — a worktree that cannot be removed is a warning, not a refusal", code, closed["error"])
	}
	warning, _ := closed["data"].(map[string]any)["warning"].(string)
	for _, want := range []string{worktree, "could not remove"} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning %q must mention %q", warning, want)
		}
	}
	// The warning is the truth: the checkout is still registered with git.
	if listed := gitOut(t, root, "worktree", "list", "--porcelain"); !strings.Contains(listed, resolved) {
		t.Errorf("git no longer lists %s, so the warning claims a leak that is not there:\n%s", resolved, listed)
	}
}

// `not_git_worktree` says the caller's environment is not a git work tree. It is
// not evidence that the worktree is gone, and it must not be read as one: the
// close still verifies with git and removes the checkout, and the herdr failure
// is reported rather than swallowed.
func TestCLICloseDoesNotReadNotGitWorktreeAsTheWorktreeBeingGone(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", herdrTestPlaybook)
	worktree, resolved := realWorktree(t, root)
	assertWorktreeListed(t, root, resolved)

	env, _, _ := fakeHerdrOnPath(t)
	env = append(env,
		"HERDR_TEST_WORKTREE_WS=wWT", "HERDR_TEST_WORKTREE_CWD="+worktree,
		"HERDR_TEST_WORKTREE_REMOVE_CODE=not_git_worktree")
	capNode, _, _, workerRef := deliverProbeGoal(t, bin, root, env, "sample", "--worktree", "wWT")
	finishWorker(t, bin, root, workerRef)

	closed, code := runFSEnv(t, bin, root, env, "close", "--node", capNode, "--decision", "verified")
	if code != 0 {
		t.Fatalf("close exit %d: %v", code, closed["error"])
	}
	warning, _ := closed["data"].(map[string]any)["warning"].(string)
	if !strings.Contains(warning, "not_git_worktree") {
		t.Errorf("warning %q must report the herdr failure", warning)
	}
	assertWorktreeGone(t, root, worktree, resolved)
}

// A worktree workspace herdr does not know is refused, naming it, before any
// worker node exists and without splitting the cap's pane. The refusal is the
// point: a fallback split would put a worker in the main checkout under a record
// that claimed otherwise.
func TestCLIDeliverRefusesAnUnknownWorktreeWorkspace(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", herdrTestPlaybook)
	env, state, _ := fakeHerdrOnPath(t)

	resp, code := runFSEnv(t, bin, root, env, "dispatch", "--deliver", "--project", "skills", "--type", "dev-task", "--goal", "sample", "--worktree", "wNOPE")
	if code != 1 {
		t.Fatalf("exit = %d, want 1: %v", code, resp)
	}
	errMsg, _ := resp["error"].(string)
	for _, want := range []string{"wNOPE", "cannot be resolved", "unresolved"} {
		if !strings.Contains(errMsg, want) {
			t.Errorf("error %q must mention %q", errMsg, want)
		}
	}
	if _, err := os.Stat(filepath.Join(state, "split_args")); !os.IsNotExist(err) {
		t.Errorf("the refusal split the cap's pane: %s", herdrSplitArgs(t, state))
	}
	if tasks := scopeTasks(t, bin, root, "skills"); len(tasks) != 0 {
		t.Errorf("skills nodes = %v, want none: a refused dispatch creates no worker node", tasks)
	}
}

// With two dispatches into one project, --worker naming the wrong node is
// refused against the recorded one rather than closing the wrong work.
func TestCLICloseRefusesAMismatchedWorker(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", herdrTestPlaybook)
	env, _, _ := fakeHerdrOnPath(t)

	capNode, _, _, workerRef := deliverProbe(t, bin, root, env)
	finishWorker(t, bin, root, workerRef)
	other := addWorkerNode(t, bin, root, "done")

	resp, code := runFSEnv(t, bin, root, env, "close", "--node", capNode, "--worker", "skills:"+other, "--decision", "x")
	if code != 1 {
		t.Fatalf("exit = %d, want 1: %v", code, resp)
	}
	errMsg, _ := resp["error"].(string)
	for _, want := range []string{"skills:" + other, "does not match", workerRef} {
		if !strings.Contains(errMsg, want) {
			t.Errorf("error %q must mention %q", errMsg, want)
		}
	}
	if task := capTask(t, bin, root, "dispatch dev-task: sample"); task["status"] == "done" {
		t.Errorf("cap node status = %v, a refused close must not close it", task["status"])
	}
}

// Without a delivery record the close-out refuses and says what to do instead.
func TestCLICloseRefusesWithoutADeliveryRecord(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", herdrTestPlaybook)
	env, _, _ := fakeHerdrOnPath(t)

	prep, code := runFS(t, bin, root, "dispatch", "--project", "skills", "--type", "dev-task", "--goal", "undelivered")
	if code != 0 {
		t.Fatalf("dispatch exit %d: %v", code, prep["error"])
	}
	capNode := prep["data"].(map[string]any)["cap_node_id"].(string)

	resp, code := runFSEnv(t, bin, root, env, "close", "--node", capNode, "--worker", "skills:x", "--decision", "x")
	if code != 1 {
		t.Fatalf("exit = %d, want 1: %v", code, resp)
	}
	if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "no delivery recorded") {
		t.Errorf("error %q must say no delivery was recorded", errMsg)
	}
}

// A worker node that is not done blocks the close-out, which names its status.
func TestCLICloseRefusesWhileTheWorkerIsActive(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", herdrTestPlaybook)
	env, _, _ := fakeHerdrOnPath(t)

	capNode, _, _, workerRef := deliverProbe(t, bin, root, env)
	_, worker, _ := strings.Cut(workerRef, ":")
	runFS(t, bin, root, "task", "update", worker, "--status", "active", "--project", "skills")

	resp, code := runFSEnv(t, bin, root, env, "close", "--node", capNode, "--decision", "x")
	if code != 1 {
		t.Fatalf("exit = %d, want 1: %v", code, resp)
	}
	errMsg, _ := resp["error"].(string)
	for _, want := range []string{"worker " + workerRef, "is active", "let it finish", "--abandoned"} {
		if !strings.Contains(errMsg, want) {
			t.Errorf("error %q must mention %q", errMsg, want)
		}
	}
}

// A done worker is not enough: the cap must record a verdict.
func TestCLICloseRefusesWithoutAVerdict(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", herdrTestPlaybook)
	env, _, _ := fakeHerdrOnPath(t)

	capNode, _, _, workerRef := deliverProbe(t, bin, root, env)
	finishWorker(t, bin, root, workerRef)

	resp, code := runFSEnv(t, bin, root, env, "close", "--node", capNode)
	if code != 1 {
		t.Fatalf("exit = %d, want 1: %v", code, resp)
	}
	if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "--decision is required") {
		t.Errorf("error %q must require --decision", errMsg)
	}
}

// Abandoning skips the delivery and worker gates, but demands a reason.
func TestCLICloseAbandoned(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", herdrTestPlaybook)
	env, _, _ := fakeHerdrOnPath(t)

	prep, code := runFS(t, bin, root, "dispatch", "--project", "skills", "--type", "dev-task", "--goal", "undelivered")
	if code != 0 {
		t.Fatalf("dispatch exit %d: %v", code, prep["error"])
	}
	capNode := prep["data"].(map[string]any)["cap_node_id"].(string)

	resp, code := runFSEnv(t, bin, root, env, "close", "--node", capNode, "--abandoned")
	if code != 1 {
		t.Fatalf("--abandoned without --reason must fail, exit %d: %v", code, resp)
	}
	if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "--reason") {
		t.Errorf("error %q must require --reason", errMsg)
	}

	resp, code = runFSEnv(t, bin, root, env, "close", "--node", capNode, "--abandoned", "--reason", "the user withdrew the request")
	if code != 0 {
		t.Fatalf("close --abandoned exit %d: %v", code, resp["error"])
	}
	task := capTask(t, bin, root, "dispatch dev-task: undelivered")
	if task["status"] != "done" {
		t.Errorf("cap node status = %v, want done", task["status"])
	}
	if !containsString(task["decisions"].([]any), "abandoned, never delivered: the user withdrew the request") {
		t.Errorf("cap decisions = %v, want the never-delivered record", task["decisions"])
	}
}

// With herdr missing, --deliver fails and leaves the node pending.
func TestCLIDeliverHerdrUnavailable(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", noCheckPlaybook)

	resp, code := runFSEnv(t, bin, root, []string{"PATH=/nonexistent"}, "dispatch", "--deliver", "--project", "skills", "--type", "dev-task", "--goal", "sample")
	if code != 1 {
		t.Fatalf("exit = %d, want 1: %v", code, resp)
	}
	errMsg, _ := resp["error"].(string)
	if !strings.Contains(errMsg, "herdr") || !strings.Contains(errMsg, "unresolved") {
		t.Errorf("error %q must say herdr failed and name the unresolved node", errMsg)
	}

	// The node is left pending — visible to fs unfinished, never claimed done —
	// and no worker node was created in the target project: the failure preceded it.
	task := capTask(t, bin, root, "dispatch dev-task: sample")
	if task["status"] != "pending" {
		t.Errorf("cap node status = %v, want pending", task["status"])
	}
	if tasks := scopeTasks(t, bin, root, "skills"); len(tasks) != 0 {
		t.Errorf("skills tasks = %v, want none: no node may be created for a delivery that never happened", tasks)
	}
	runFS(t, bin, root, "task", "update", task["node_id"].(string), "--status", "done", "--decision", "test cleanup", "--project", "cap")
}

// --- fs pending -------------------------------------------------------------
//
// fs pending is the one command that answers "what is waiting on me?". It reads
// the cap's nodes, each delivery record, and each worker's node — herdr only
// says when to look.

// pendingEntry finds one dispatch in fs pending's data by cap node.
func pendingEntry(t *testing.T, data map[string]any, capNode string) map[string]any {
	t.Helper()
	entries, ok := data["pending"].([]any)
	if !ok {
		t.Fatalf("pending data = %v, want a pending list", data)
	}
	for _, e := range entries {
		entry := e.(map[string]any)
		if entry["cap_node"] == capNode {
			return entry
		}
	}
	t.Fatalf("pending %v has no entry for %s", entries, capNode)
	return nil
}

// Two dispatches of one type running at once are two agents with two names, and
// each name resolves in herdr to its own pane. A colliding name is what made a
// dispatch unresolvable in the first place.
func TestCLIDeliverNamesEachWorkerAgentUniquely(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", herdrTestPlaybook)
	env, _, script := fakeHerdrOnPath(t)

	_, _, firstPane, firstWorker := deliverProbe(t, bin, root, env)
	_, _, secondPane, secondWorker := deliverProbeGoal(t, bin, root, env, "second", "--confirm")

	first := "dispatch-dev-task-" + strings.TrimPrefix(firstWorker, "skills:")
	second := "dispatch-dev-task-" + strings.TrimPrefix(secondWorker, "skills:")
	if first == second {
		t.Fatalf("both dispatches named the same agent %s", first)
	}
	for agent, pane := range map[string]string{first: firstPane, second: secondPane} {
		if err := herdrGet(t, script, env, "agent", agent); err != nil {
			t.Errorf("herdr has no agent %s: %v", agent, err)
		}
		if got := herdrAgentPane(t, script, env, agent); got != pane {
			t.Errorf("agent %s is in pane %s, want %s", agent, got, pane)
		}
	}
}

// fs pending walks a dispatch's whole life: running while the worker works,
// ready once its node is done whatever herdr reads, and gone once the pane is
// killed without the node being touched.
func TestCLIPendingReportsWhatIsWaiting(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", herdrTestPlaybook)
	env, _, script := fakeHerdrOnPath(t)

	firstNode, _, _, firstWorker := deliverProbe(t, bin, root, env)
	secondNode, _, secondPane, secondWorker := deliverProbeGoal(t, bin, root, env, "second", "--confirm")
	if firstWorker == secondWorker {
		t.Fatalf("both dispatches created worker %s", firstWorker)
	}

	pending, code := runFSEnv(t, bin, root, env, "pending")
	if code != 0 {
		t.Fatalf("pending exit %d: %v", code, pending["error"])
	}
	data := pending["data"].(map[string]any)
	if counts := data["counts"].(map[string]any); counts["running"] != float64(2) {
		t.Errorf("counts = %v, want two running", counts)
	}
	for _, node := range []string{firstNode, secondNode} {
		entry := pendingEntry(t, data, node)
		if entry["state"] != "running" || entry["project"] != "skills" {
			t.Errorf("dispatch %s = %v, want a running dispatch in skills", node, entry)
		}
	}

	// The worker's node is the truth: a done node reads ready even though herdr
	// still calls the agent working — herdr only says when to look.
	finishWorker(t, bin, root, firstWorker)
	pending, _ = runFSEnv(t, bin, root, env, "pending")
	data = pending["data"].(map[string]any)
	if entry := pendingEntry(t, data, firstNode); entry["state"] != "ready" {
		t.Errorf("finished dispatch = %v, want ready", entry)
	}
	if entry := pendingEntry(t, data, secondNode); entry["state"] != "running" {
		t.Errorf("unfinished dispatch = %v, want running", entry)
	}

	// Close the first out; kill the second's pane without marking its node done.
	if _, code := runFSEnv(t, bin, root, env, "close", "--node", firstNode, "--decision", "read the worker node; verified"); code != 0 {
		t.Fatalf("close exit %d", code)
	}
	if _, err := herdrRun(t, script, env, "pane", "close", secondPane); err != nil {
		t.Fatalf("closing the second pane: %v", err)
	}

	pending, _ = runFSEnv(t, bin, root, env, "pending")
	data = pending["data"].(map[string]any)
	if entry := pendingEntry(t, data, secondNode); entry["state"] != "gone" {
		t.Errorf("dispatch whose pane died = %v, want gone", entry)
	}
	if counts := data["counts"].(map[string]any); counts["ready"] != float64(0) {
		t.Errorf("counts = %v, want nothing ready once the worker is closed", counts)
	}
}

// A dispatch prepared but never delivered has no record to read, so it is
// unlinked rather than a worker that looks gone. Reading it appends no event.
func TestCLIPendingReportsAnUndeliveredDispatchUnlinked(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	writeKBPlaybook(t, testHome(t), "dev-task-prerequisites", herdrTestPlaybook)
	env, _, _ := fakeHerdrOnPath(t)

	prep, code := runFSEnv(t, bin, root, env, "dispatch", "--project", "skills", "--type", "dev-task", "--goal", "never delivered")
	if code != 0 {
		t.Fatalf("dispatch exit %d: %v", code, prep["error"])
	}
	capNode := prep["data"].(map[string]any)["cap_node_id"].(string)

	before, _ := runFSEnv(t, bin, root, env, "log", "--project", "cap")
	beforeCount := len(before["data"].(map[string]any)["events"].([]any))

	pending, code := runFSEnv(t, bin, root, env, "pending")
	if code != 0 {
		t.Fatalf("pending exit %d: %v", code, pending["error"])
	}
	entry := pendingEntry(t, pending["data"].(map[string]any), capNode)
	if entry["state"] != "unlinked" {
		t.Errorf("never-delivered dispatch = %v, want unlinked", entry)
	}

	after, _ := runFSEnv(t, bin, root, env, "log", "--project", "cap")
	afterCount := len(after["data"].(map[string]any)["events"].([]any))
	if afterCount != beforeCount {
		t.Errorf("cap events = %d, want %d: fs pending must write nothing", afterCount, beforeCount)
	}
}

// fs pending takes no arguments: a flag is a usage error, refused before any
// work begins.
func TestCLIPendingRefusesArguments(t *testing.T) {
	bin := getFS(t)

	resp, code := runFS(t, bin, t.TempDir(), "pending", "--project", "skills")
	if code != 2 {
		t.Fatalf("exit = %d, want 2: %v", code, resp)
	}
	if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "--project") {
		t.Errorf("error %q must name the rejected flag", errMsg)
	}
}
