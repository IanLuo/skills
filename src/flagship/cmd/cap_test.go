package main

// One cap, reachable, end to end: the claim, the report pushed at the live cap,
// the reply, and the guard that keeps a worker from coordinating. Every cycle
// runs under a temp HOME against the fake herdr on PATH.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedPane puts a pane in the fake herdr's state, so a claim over it reads live.
func seedPane(t *testing.T, state, pane string) {
	t.Helper()
	path := filepath.Join(state, "pane_"+strings.ReplaceAll(pane, ":", "_"))
	if err := os.WriteFile(path, []byte("/tmp"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// killPane removes a pane from the fake herdr's state, so a claim over it is a
// take-over.
func killPane(t *testing.T, state, pane string) {
	t.Helper()
	path := filepath.Join(state, "pane_"+strings.ReplaceAll(pane, ":", "_"))
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// seedAgent puts an agent in a pane, so a claim reads the pane's agent name.
func seedAgent(t *testing.T, state, name, pane string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(state, "agent_"+name), []byte(pane+" working\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// claimEnv is the environment of a session running in a herdr pane.
func claimEnv(env []string, pane string) []string {
	return append(append([]string{}, env...),
		"HERDR_PANE_ID="+pane, "HERDR_ENV=1", "HERDR_TAB_ID="+strings.Split(pane, ":")[0]+":t1")
}

// capWho runs fs cap who and returns the parsed data and the exit code.
func capWho(t *testing.T, bin, root string, env []string) (map[string]any, int) {
	t.Helper()
	resp, code := runFSEnv(t, bin, root, env, "cap", "who")
	if code != 0 && code != 1 {
		t.Fatalf("cap who exit %d: %v", code, resp["error"])
	}
	if resp["ok"] == false {
		t.Fatalf("cap who refused: %v", resp["error"])
	}
	return resp["data"].(map[string]any), code
}

// promptedPanes returns the pane the fake herdr's last prompt went to.
func promptedPane(t *testing.T, state string) string {
	t.Helper()
	pane, err := os.ReadFile(filepath.Join(state, "prompt_pane"))
	if err != nil {
		t.Fatalf("reading the prompted pane: %v", err)
	}
	return string(pane)
}

func TestCLICapClaimAndWho(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	env, state, _ := fakeHerdrOnPath(t)
	seedPane(t, state, "wTEST:p1")
	seedAgent(t, state, "cap-agent", "wTEST:p1")

	// With no claim at all, fs cap who says so and exits 1: the scope is free,
	// and that is not the same as a cap nobody can reach.
	data, code := capWho(t, bin, root, env)
	if code != 1 || data["state"] != "none" {
		t.Fatalf("unclaimed: state=%v exit=%d, want none/1", data["state"], code)
	}

	resp, code := runFSEnv(t, bin, root, claimEnv(env, "wTEST:p1"), "cap", "claim")
	if code != 0 {
		t.Fatalf("cap claim exit %d: %v", code, resp["error"])
	}

	data, code = capWho(t, bin, root, env)
	if code != 0 {
		t.Fatalf("cap who exit %d with a live cap", code)
	}
	if data["state"] != "live" || data["pane_id"] != "wTEST:p1" || data["agent"] != "cap-agent" {
		t.Fatalf("cap who: %v", data)
	}
	if data["claimed_at"] == "" {
		t.Error("the address records no time")
	}

	// A second claim while the pane is alive is refused, naming it and when it
	// was seen.
	seedPane(t, state, "wTEST:p2")
	resp, code = runFSEnv(t, bin, root, claimEnv(env, "wTEST:p2"), "cap", "claim")
	if code != 1 {
		t.Fatalf("a claim over a live cap must be refused, exit %d", code)
	}
	refusal := resp["error"].(string)
	if !strings.Contains(refusal, "wTEST:p1") || !strings.Contains(refusal, data["claimed_at"].(string)) {
		t.Errorf("the refusal must name the live pane and when it was seen: %s", refusal)
	}
}

func TestCLICapClaimTakeOver(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	env, state, _ := fakeHerdrOnPath(t)
	seedPane(t, state, "wTEST:p1")
	seedPane(t, state, "wTEST:p2")
	if _, code := runFSEnv(t, bin, root, claimEnv(env, "wTEST:p1"), "cap", "claim"); code != 0 {
		t.Fatal("the first claim failed")
	}

	// The cap's pane is gone from herdr: the address is provably dead.
	killPane(t, state, "wTEST:p1")
	data, code := capWho(t, bin, root, env)
	if code != 1 || data["state"] != "dead" {
		t.Fatalf("a dead cap reads state=%v exit=%d, want dead/1", data["state"], code)
	}
	if data["pane_id"] != "wTEST:p1" {
		t.Fatalf("a dead cap still names its address: %v", data)
	}

	// A take-over without the reconstruction is refused.
	resp, code := runFSEnv(t, bin, root, claimEnv(env, "wTEST:p2"), "cap", "claim")
	if code != 1 {
		t.Fatal("a take-over without --reconstructed must be refused")
	}
	if !strings.Contains(resp["error"].(string), "--reconstructed") {
		t.Errorf("the refusal must ask for the reconstruction: %v", resp["error"])
	}

	// With it, the claim succeeds and the handover is recorded.
	_, code = runFSEnv(t, bin, root, claimEnv(env, "wTEST:p2"), "cap", "claim",
		"--reconstructed", "read fs pending: two dispatches open, nothing blocked")
	if code != 0 {
		t.Fatalf("take-over exit %d", code)
	}
	data, code = capWho(t, bin, root, env)
	if code != 0 || data["pane_id"] != "wTEST:p2" {
		t.Fatalf("after the take-over: %v exit=%d", data, code)
	}

	// The handover survives in the store, on the claim itself.
	logged, _ := runFSEnv(t, bin, root, env, "log", "--project", "cap", "--type", "cap-claimed")
	events := logged["data"].(map[string]any)["events"].([]any)
	if len(events) != 2 {
		t.Fatalf("expected both claims in the store, got %d", len(events))
	}
	payload, _ := events[1].(map[string]any)["payload"].(map[string]any)
	if payload["taken_over_from"] != "wTEST:p1" || payload["taken_over_at"] == nil {
		t.Fatalf("the handover was not recorded: %v", payload)
	}
	if !strings.Contains(payload["reconstructed"].(string), "two dispatches open") {
		t.Fatalf("the reconstruction was not recorded: %v", payload)
	}
}

func TestCLICapClaimOutsideHerdrIsRefused(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	// A session that is not in a herdr pane at all. Cleared explicitly rather
	// than left to the ambient environment: the suite may itself be running
	// inside one, and the test is about the session that is not.
	env, _, _ := fakeHerdrOnPath(t)
	env = append(env, "HERDR_PANE_ID=")

	resp, code := runFSEnv(t, bin, root, env, "cap", "claim")
	if code != 1 {
		t.Fatal("a session with no HERDR_PANE_ID must be refused a claim")
	}
	if !strings.Contains(resp["error"].(string), "HERDR_PANE_ID") {
		t.Errorf("the refusal must say why: %v", resp["error"])
	}
}

// reportArgs is fs report for a worker node.
func reportArgs(workerRef, line string, kind string) []string {
	args := []string{"report", workerRef, "--line", line}
	if kind != "" {
		args = append(args, kind)
	}
	return args
}

func TestCLIReportRecordsAndNotifiesTheLiveCap(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	env, state, _ := fakeHerdrOnPath(t)

	// The cap claims its own pane, then dispatches a worker.
	seedPane(t, state, "wTEST:p9")
	if _, code := runFSEnv(t, bin, root, claimEnv(env, "wTEST:p9"), "cap", "claim"); code != 0 {
		t.Fatal("the cap's claim failed")
	}
	_, _, workerPane, workerRef := deliverProbe(t, bin, root, env)

	// The worker reports a block: the event lands first, and the live cap is
	// notified through herdr with the node named and the line carried.
	resp, code := runFSEnv(t, bin, root, claimEnv(env, workerPane),
		reportArgs(workerRef, "the fixture has no cap", "--blocked")...)
	if code != 0 {
		t.Fatalf("report exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	if data["notified"] != true {
		t.Fatalf("a block must reach the live cap: %v", data)
	}

	// The exact argv herdr was handed: the cap's pane, and a text naming the
	// node rather than carrying the work.
	if pane := promptedPane(t, state); pane != "wTEST:p9" {
		t.Errorf("the report went to %q, want the cap's pane wTEST:p9", pane)
	}
	text := herdrPrompt(t, state)
	if !strings.Contains(text, "skills:"+strings.TrimPrefix(workerRef, "skills:")) || !strings.Contains(text, "the fixture has no cap") {
		t.Errorf("the notification: %q", text)
	}

	// Both events are in the store: the report, and the block every existing
	// reader already handles.
	logged, _ := runFSEnv(t, bin, root, env, "log", "--project", "skills", "--node", strings.TrimPrefix(workerRef, "skills:"))
	types := map[string]bool{}
	for _, e := range logged["data"].(map[string]any)["events"].([]any) {
		types[e.(map[string]any)["type"].(string)] = true
	}
	for _, want := range []string{"report-recorded", "task-blocked", "status-changed"} {
		if !types[want] {
			t.Errorf("a blocked report must write %s; the store holds %v", want, types)
		}
	}
}

func TestCLIReportWithNoLiveCapSaysRecordedNotNotified(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	env, state, _ := fakeHerdrOnPath(t)
	workerNode := addWorkerNode(t, bin, root, "active")

	// No claim, and the worker's own pane names it: the report is written and
	// the command succeeds, saying nobody was pulled.
	resp, code := runFSEnv(t, bin, root, claimEnv(env, "wTEST:p9"),
		reportArgs("skills:"+workerNode, "the fixture has no cap", "--blocked")...)
	if code != 0 {
		t.Fatalf("report exit %d: %v", code, resp["error"])
	}
	data := resp["data"].(map[string]any)
	if data["notified"] != false || !strings.Contains(data["note"].(string), "recorded, not notified") {
		t.Fatalf("data: %v", data)
	}
	if _, err := os.Stat(filepath.Join(state, "prompt")); !os.IsNotExist(err) {
		t.Error("nothing was there to notify, so nothing may be sent")
	}

	// Readable afterwards by fs pending, from the cap's own read.
	pending, code := runFSEnv(t, bin, root, env, "pending")
	if code != 0 {
		t.Fatalf("pending exit %d: %v", code, pending["error"])
	}
	if state := pending["data"].(map[string]any)["cap"].(map[string]any)["state"]; state != "none" {
		t.Errorf("fs pending must say no cap has claimed: %v", state)
	}

	// And the block still reads as a block for every reader that already
	// handles one.
	got, code := runFSEnv(t, bin, root, env, "task", "get", workerNode, "--project", "skills")
	if code != 0 {
		t.Fatalf("task get exit %d", code)
	}
	if status := got["data"].(map[string]any)["status"]; status != "blocked" {
		t.Errorf("the blocked report left the node %v", status)
	}
}

func TestCLIReportKindlessSendsNothing(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	env, state, _ := fakeHerdrOnPath(t)
	seedPane(t, state, "wTEST:p9")
	if _, code := runFSEnv(t, bin, root, claimEnv(env, "wTEST:p9"), "cap", "claim"); code != 0 {
		t.Fatal("the cap's claim failed")
	}
	workerNode := addWorkerNode(t, bin, root, "active")

	resp, code := runFSEnv(t, bin, root, claimEnv(env, "wTEST:p1"),
		reportArgs("skills:"+workerNode, "halfway through the parser", "")...)
	if code != 0 {
		t.Fatalf("report exit %d: %v", code, resp["error"])
	}
	if data := resp["data"].(map[string]any); data["notified"] != false {
		t.Fatalf("a report with no kind must not interrupt the cap: %v", data)
	}
	if _, err := os.Stat(filepath.Join(state, "prompt")); !os.IsNotExist(err) {
		t.Error("a kindless report must send nothing")
	}
}

// AC6, the sharpest: one test, one environment, two acts. The pane fs holds a
// delivery record for is refused a coordinating act, naming the node it worked,
// and its own report from that same pane succeeds — the guard is neither
// toothless nor a muzzle on the worker who needs to speak.
func TestCLIReportingIsAllowedWhereCoordinatingIsRefused(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	env, state, _ := fakeHerdrOnPath(t)

	seedPane(t, state, "wTEST:p9")
	if _, code := runFSEnv(t, bin, root, claimEnv(env, "wTEST:p9"), "cap", "claim"); code != 0 {
		t.Fatal("the cap's claim failed")
	}
	// The cap dispatches, and the delivery record binds the worker's pane.
	_, _, workerPane, workerRef := deliverProbe(t, bin, root, env)
	workerEnv := claimEnv(env, workerPane)

	// Act one: the worker's pane coordinates, and is refused, naming the node it
	// worked.
	for _, args := range [][]string{
		{"dispatch", "--project", "skills", "--type", "dev-task", "--goal", "a worker coordinating"},
		{"close", "--node", "t-nothing"},
		{"cap", "claim"},
		{"task", "add", "--goal", "a worker writing the cap's scope", "--project", "cap"},
		{"tell", "skills:" + strings.TrimPrefix(workerRef, "skills:"), "a worker answering as the cap"},
	} {
		resp, code := runFSEnv(t, bin, root, workerEnv, args...)
		if code == 0 {
			t.Errorf("fs %s succeeded from the worker's pane: %v", strings.Join(args, " "), resp)
			continue
		}
		refusal, _ := resp["error"].(string)
		if !strings.Contains(refusal, workerRef) {
			t.Errorf("fs %s refusal must name the node that pane worked: %s", strings.Join(args, " "), refusal)
		}
	}

	// Act two: from that same environment, the worker's report succeeds.
	resp, code := runFSEnv(t, bin, root, workerEnv,
		reportArgs(workerRef, "blocked: the fixture has no cap", "--blocked")...)
	if code != 0 {
		t.Fatalf("fs report from the worker's pane must succeed, exit %d: %v", code, resp["error"])
	}
	if data := resp["data"].(map[string]any); data["notified"] != true {
		t.Fatalf("the worker's report must reach the cap: %v", data)
	}
}

func TestCLIReportFromACapPaneIsAllowed(t *testing.T) {
	// The guard refuses a pane with a delivery record, not every pane: the cap's
	// own pane holds none, and a session that has claimed coordinates.
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	env, state, _ := fakeHerdrOnPath(t)
	seedPane(t, state, "wTEST:p9")
	capEnv := claimEnv(env, "wTEST:p9")
	if _, code := runFSEnv(t, bin, root, capEnv, "cap", "claim"); code != 0 {
		t.Fatal("the cap's claim failed")
	}

	if _, code := runFSEnv(t, bin, root, capEnv, "task", "add", "--goal", "cap work", "--project", "cap"); code != 0 {
		t.Fatal("the cap must be able to write its own scope")
	}
}

func TestCLITellResolvesTheDeliveryRecordAndSends(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	runFS(t, bin, root, "project", "create", "--name", "skills", "--root", root)
	env, state, _ := fakeHerdrOnPath(t)
	seedPane(t, state, "wTEST:p9")
	if _, code := runFSEnv(t, bin, root, claimEnv(env, "wTEST:p9"), "cap", "claim"); code != 0 {
		t.Fatal("the cap's claim failed")
	}
	_, _, workerPane, workerRef := deliverProbe(t, bin, root, env)

	resp, code := runFSEnv(t, bin, root, claimEnv(env, "wTEST:p9"), "tell", workerRef, "use the sqlite store")
	if code != 0 {
		t.Fatalf("tell exit %d: %v", code, resp["error"])
	}
	if pane := promptedPane(t, state); pane != workerPane {
		t.Errorf("the reply went to %q, want the worker's pane %s", pane, workerPane)
	}
	if text := herdrPrompt(t, state); text != "use the sqlite store" {
		t.Errorf("the reply text: %q", text)
	}

	// An unknown node, and a node with no delivery record, are each refused
	// naming which.
	workerNode := addWorkerNode(t, bin, root, "active")
	for _, node := range []string{"t-nope", workerNode} {
		resp, code := runFSEnv(t, bin, root, claimEnv(env, "wTEST:p9"), "tell", "skills:"+node, "hello")
		if code != 1 {
			t.Errorf("tell %s must be refused, exit %d", node, code)
			continue
		}
		if !strings.Contains(resp["error"].(string), node) {
			t.Errorf("the refusal must name the node: %v", resp["error"])
		}
	}
}
