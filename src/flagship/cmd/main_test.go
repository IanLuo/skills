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
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+testHome(t))
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

func TestCLIHelpSubcommands(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	for _, sub := range []string{"project", "task", "kb"} {
		cmd := exec.Command(bin, sub, "--help")
		cmd.Dir = dir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		_ = cmd.Run()

		out := stderr.String()
		if !strings.Contains(out, "Subcommands:") {
			t.Errorf("%s --help missing 'Subcommands:': %s", sub, out)
		}
	}
}

func TestCLIKBAddGetListEditRemove(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	playbook := `name: dev-prereqs
type: prerequisite
trigger: dev-task
steps:
  - check spec
  - check arch
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
  - check spec
  - check arch
  - run linter
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
