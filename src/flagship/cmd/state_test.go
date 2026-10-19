package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IanLuo/memstore"
	"github.com/flagship-dev/flagship/internal/store"
	_ "modernc.org/sqlite"
)

// AC7 — rebuild and verify exist and do what they say, with the limit the errata
// names.
//
// Three drifts, three outcomes, and each is asserted exactly so the test can go
// red in either direction:
//
//   - a row that is GONE is restored: verify exits 1, rebuild restores it, and
//     verify exits 0 again;
//   - a lag — an event written with no projection open — is filled by the same
//     re-fold, which is the failure the append-first ordering creates;
//   - a row removed with memstore's Forget is NOT resurrected. Forget is
//     memstore's only removal verb and it is one-way: the row's
//     (origin, origin_id) stays taken, so Put refuses the re-insert with
//     ErrDuplicateOrigin and the re-fold skips it. That is the limit D4's errata
//     records (skills:t-222f53e7): rebuild repairs lag, not content, and it
//     cannot undo a forget.
//
// `rebuild` touches no non-fs: row in any of them.
func TestStateVerifyRebuild(t *testing.T) {
	bin := getFS(t)

	t.Run("an empty projection refuses and rebuild seeds it", func(t *testing.T) {
		f := newStateFixture(t, bin, false)

		// The live projection holds the personal row and nothing else: the
		// first-run state AC4 names. Verify says what to run.
		resp, code := f.run("state", "verify")
		if code != 1 {
			t.Fatalf("verify before the seed: code=%d resp=%v, want 1 (the projection has no fs: rows)", code, resp)
		}
		if err, _ := resp["error"].(string); !strings.Contains(err, f.project) || !strings.Contains(err, "fs state rebuild") {
			t.Fatalf("verify refusal = %q, want it to name the scope and the remedy", resp["error"])
		}

		if resp, code := f.run("state", "rebuild"); code != 0 || resp["ok"] != true {
			t.Fatalf("rebuild: code=%d resp=%v", code, resp)
		}
		if resp, code := f.run("state", "verify"); code != 0 || resp["ok"] != true {
			t.Fatalf("verify after rebuild: code=%d resp=%v", code, resp)
		}
		f.assertPersonalRowIntact(t)
	})

	t.Run("a row that is gone is restored by rebuild", func(t *testing.T) {
		f := newStateFixture(t, bin, true)
		if resp, code := f.run("state", "verify"); code != 0 {
			t.Fatalf("verify on a fresh projection: code=%d resp=%v", code, resp)
		}

		// A row that is GONE: physically deleted, so the event's provenance key
		// is free again. This is the loss AC7's wording is about, and the only
		// way to construct it with memstore's exported verbs absent is to take
		// the row out of the file — which is what a lost row is.
		hardDeleteOneRow(t, f.projPath(), f.project)

		resp, code := f.run("state", "verify")
		if code != 1 {
			t.Fatalf("verify after losing a row: code=%d resp=%v, want 1", code, resp)
		}
		if err, _ := resp["error"].(string); !strings.Contains(err, f.project) || !strings.Contains(err, "fs state rebuild") {
			t.Fatalf("verify refusal = %q, want the scope and the remedy", resp["error"])
		}

		if resp, code := f.run("state", "rebuild"); code != 0 || resp["ok"] != true {
			t.Fatalf("rebuild: code=%d resp=%v", code, resp)
		}
		if resp, code := f.run("state", "verify"); code != 0 || resp["ok"] != true {
			t.Fatalf("verify after the repair: code=%d resp=%v, want 0 — a gone row is restored", code, resp)
		}
		f.assertPersonalRowIntact(t)
	})

	t.Run("a lag is filled by the same re-fold", func(t *testing.T) {
		f := newStateFixture(t, bin, true)
		// An event written while no projection was open: the row is missing and
		// its key is free, which is the drift a failed fold leaves.
		if resp, code := f.runUnfolded("task", "add", "--goal", "a node added while the projection was closed", "--project", f.project); code != 0 || resp["ok"] != true {
			t.Fatalf("lagging write: code=%d resp=%v", code, resp)
		}

		if resp, code := f.run("state", "verify"); code != 1 {
			t.Fatalf("verify on a lagging projection: code=%d resp=%v, want 1", code, resp)
		}
		if resp, code := f.run("state", "rebuild"); code != 0 || resp["ok"] != true {
			t.Fatalf("rebuild: code=%d resp=%v", code, resp)
		}
		if resp, code := f.run("state", "verify"); code != 0 || resp["ok"] != true {
			t.Fatalf("verify after the repair: code=%d resp=%v, want 0", code, resp)
		}
	})

	// The live red of skills:t-55266895, reproduced at will, and the three things
	// the ruling on it asked for: every write names the projection it folded into,
	// a non-default projection warns (loud, never a refusal), and a refusal names
	// the CAUSE — rows one side holds and the other does not — rather than the
	// first index that differs.
	//
	// The write here is a REPORT folded into another file: a real, committed
	// event whose row the live projection never receives. That is what the red
	// was, and it is a lag, so the re-fold heals it.
	t.Run("a write names its projection, warns when re-pointed, and the refusal names the cause", func(t *testing.T) {
		f := newStateFixture(t, bin, true)

		// A clean answer names both files it compared, so "clean" is never
		// ambiguous about which projection is clean.
		resp, code := f.run("state", "verify")
		if code != 0 {
			t.Fatalf("verify on a fresh projection: code=%d resp=%v", code, resp)
		}
		clean, _ := resp["data"].(map[string]any)
		if got, _ := clean["projection"].(string); got != f.projPath() {
			t.Fatalf("verify's projection = %q, want the live file %q", got, f.projPath())
		}
		if got, _ := clean["projection_env"].(string); got != store.ProjectionSourceDefault {
			t.Fatalf("verify's projection_env = %q, want %q", got, store.ProjectionSourceDefault)
		}
		if got, _ := clean["folded_against"].(string); got == "" {
			t.Fatalf("verify's folded_against is empty: a clean answer must name the fold it compared against")
		}

		// A write on the default path records where it folded, and is quiet.
		resp, stderr, code := runFSEnvStderr(t, f.bin, f.dir,
			[]string{"HOME=" + f.home}, "report", f.project+":"+f.node, "--line", "a report on the default path")
		if code != 0 || resp["ok"] != true {
			t.Fatalf("report on the default path: code=%d resp=%v stderr=%s", code, resp, stderr)
		}
		mustNotContain(t, stderr, "projection re-pointed")
		mustContain(t, stderr, `"projection":"`+f.projPath()+`"`)
		mustContain(t, stderr, `"projection_env":"`+store.ProjectionSourceDefault+`"`)

		// The write that produced the live red: FS_MEMSTORE sends the fold
		// elsewhere. The command still succeeds — a refusal would break the suite,
		// which sets this variable on purpose — and the run says where it went.
		resp, stderr, code = runFSEnvStderr(t, f.bin, f.dir,
			[]string{"HOME=" + f.home, store.ProjectionEnv + "=" + f.unfolded},
			"report", f.project+":"+f.node, "--line", "a report folded into another file")
		if code != 0 || resp["ok"] != true {
			t.Fatalf("report under a re-pointed projection: code=%d resp=%v stderr=%s", code, resp, stderr)
		}
		eventID := firstEventID(t, resp)
		mustContain(t, stderr, `"level":"WARN"`)
		mustContain(t, stderr, "projection re-pointed")
		mustContain(t, stderr, `"variable":"`+store.ProjectionEnv+`"`)
		mustContain(t, stderr, `"value":"`+f.unfolded+`"`)
		mustContain(t, stderr, `"default":"`+f.projPath()+`"`)
		mustContain(t, stderr, `"projection":"`+f.unfolded+`"`)
		mustContain(t, stderr, `"projection_env":"`+store.ProjectionPathEnv+`"`)

		// The refusal names the cause: the row that is absent, by the event it
		// names, and the file that lacks it. No index path, because no index is
		// what is wrong.
		resp, code = f.run("state", "verify")
		if code != 1 {
			t.Fatalf("verify after a side row landed outside the live projection: code=%d resp=%v, want 1", code, resp)
		}
		refusal, _ := resp["error"].(string)
		mustContain(t, refusal, "1 row present in the event store is absent from the projection")
		mustContain(t, refusal, "report/"+eventID)
		mustContain(t, refusal, "the projection is "+f.projPath())
		mustContain(t, refusal, "fs state rebuild")
		mustNotContain(t, refusal, "side_rows[")

		if resp, code := f.run("state", "rebuild"); code != 0 || resp["ok"] != true {
			t.Fatalf("rebuild: code=%d resp=%v", code, resp)
		}
		if resp, code := f.run("state", "verify"); code != 0 || resp["ok"] != true {
			t.Fatalf("verify after the repair: code=%d resp=%v, want 0 — a row that never reached the live file is a lag, not lost content", code, resp)
		}
		f.assertPersonalRowIntact(t)
	})

	// The other direction of the same cause: a row the projection holds and the
	// event store does not. It is named as an extra, not as a shifted index.
	t.Run("a row the event store does not hold is named as an extra", func(t *testing.T) {
		f := newStateFixture(t, bin, true)
		seedStrayRow(t, f.projPath(), f.project)

		resp, code := f.run("state", "verify")
		if code != 1 {
			t.Fatalf("verify with an extra row: code=%d resp=%v, want 1", code, resp)
		}
		refusal, _ := resp["error"].(string)
		mustContain(t, refusal, "1 row present in the projection is absent from the event store")
		mustContain(t, refusal, "report/stray-1")
		mustNotContain(t, refusal, "side_rows[")
	})

	t.Run("a forgotten row is not resurrected", func(t *testing.T) {
		f := newStateFixture(t, bin, true)
		forgetOneRow(t, f.projPath(), f.project)

		resp, code := f.run("state", "verify")
		if code != 1 {
			t.Fatalf("verify after forgetting a row: code=%d resp=%v, want 1", code, resp)
		}
		if resp, code := f.run("state", "rebuild"); code != 0 || resp["ok"] != true {
			t.Fatalf("rebuild: code=%d resp=%v", code, resp)
		}

		// Still 1: memstore's Forget is one-way. The row is hidden, its
		// provenance key is still taken, and the re-fold's Put is refused as a
		// duplicate — so rebuild reports success and the state stays behind.
		// This is the limit, asserted rather than hoped away.
		resp, code = f.run("state", "verify")
		if code != 1 {
			t.Fatalf("verify after rebuild = code=%d resp=%v, want 1: a forgotten row must NOT be resurrected (memstore's forget is one-way, spec D4 errata skills:t-222f53e7)", code, resp)
		}
		if err, _ := resp["error"].(string); !strings.Contains(err, f.project) {
			t.Fatalf("verify refusal = %q, want it to name the scope", resp["error"])
		}
		f.assertPersonalRowIntact(t)
	})
}

// stateFixture is one isolated home holding a project, one node, and one
// personal memory in the shared projection file.
type stateFixture struct {
	t       *testing.T
	bin     string
	home    string
	dir     string
	project string
	// node is the id the fixture's task-add created, so a test can address it in
	// a report — the write whose row lands in a named projection file.
	node string
	// unfolded is where writes go when they must not fold, so a projection that
	// never saw them can be built.
	unfolded string
}

// newStateFixture creates the home and the project. foldWrites=false writes the
// project and its node with FS_MEMSTORE pointed elsewhere, so the live
// projection stays empty — the first-run state.
func newStateFixture(t *testing.T, bin string, foldWrites bool) *stateFixture {
	t.Helper()
	f := &stateFixture{
		t:        t,
		bin:      bin,
		home:     t.TempDir(),
		dir:      t.TempDir(),
		project:  "statetest",
		unfolded: filepath.Join(t.TempDir(), "memory.db"),
	}
	write := f.run
	if !foldWrites {
		write = f.runUnfolded
	}
	if resp, code := write("project", "create", "--name", f.project); code != 0 || resp["ok"] != true {
		t.Fatalf("project create: code=%d resp=%v", code, resp)
	}
	if resp, code := write("task", "add", "--goal", "a node to project", "--project", f.project); code != 0 || resp["ok"] != true {
		t.Fatalf("task add: code=%d resp=%v", code, resp)
	} else if data, _ := resp["data"].(map[string]any); data != nil {
		f.node, _ = data["node_id"].(string)
	}
	if f.node == "" {
		t.Fatalf("the fixture's task-add returned no node id")
	}

	// A personal memory, so "touches no non-fs row" is a claim about a row that
	// exists rather than about an empty set.
	seedPersonalMemory(t, f.projPath())
	return f
}

func (f *stateFixture) projPath() string {
	return filepath.Join(f.home, ".memstore", "memory.db")
}

// run runs fs in the fixture's directory and home.
func (f *stateFixture) run(args ...string) (map[string]any, int) {
	return runFSEnv(f.t, f.bin, f.dir, []string{"HOME=" + f.home}, args...)
}

// runUnfolded runs fs with the projection pointed at a scratch file, so the
// write happens while the live projection is not open.
func (f *stateFixture) runUnfolded(args ...string) (map[string]any, int) {
	return runFSEnv(f.t, f.bin, f.dir,
		[]string{"HOME=" + f.home, "FS_MEMSTORE=" + f.unfolded}, args...)
}

// assertPersonalRowIntact proves verify and rebuild changed no non-fs row.
func (f *stateFixture) assertPersonalRowIntact(t *testing.T) {
	t.Helper()
	if got := personalRow(t, f.projPath()); got != "a personal memory" {
		t.Errorf("the personal row reads %q after verify and rebuild, want it untouched", got)
	}
}

// seedPersonalMemory writes one row in the personal scope, the way another
// writer of the shared file would.
func seedPersonalMemory(t *testing.T, path string) {
	t.Helper()
	ms, err := memstore.Open(path)
	if err != nil {
		t.Fatalf("open projection: %v", err)
	}
	defer ms.Close()
	if _, err := ms.Put(&memstore.Memory{
		Kind: "note", Scope: "", Text: "a personal memory", Origin: "native",
	}); err != nil {
		t.Fatalf("seed personal memory: %v", err)
	}
}

// personalRow reads the personal row's text back.
func personalRow(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open projection for reading: %v", err)
	}
	defer db.Close()
	var text string
	if err := db.QueryRow(`SELECT text FROM memory WHERE scope = ''`).Scan(&text); err != nil {
		t.Fatalf("read personal row: %v", err)
	}
	return text
}

// seedStrayRow writes a row the event store names no event for, in the fixture's
// scope: a row the projection holds and the fold does not produce.
func seedStrayRow(t *testing.T, path, project string) {
	t.Helper()
	ms, err := memstore.Open(path)
	if err != nil {
		t.Fatalf("open projection: %v", err)
	}
	defer ms.Close()
	if _, err := ms.Put(&memstore.Memory{
		Kind: "report", Scope: "fs:" + project, Text: "a row nobody folded",
		Origin: "fs", OriginId: "stray-1",
	}); err != nil {
		t.Fatalf("seed stray row: %v", err)
	}
}

// oneProjectRowID is the newest projected row of a scope, which is the row the
// drift cases remove.
func oneProjectRowID(t *testing.T, path, project string) int64 {
	t.Helper()
	ms, err := memstore.Open(path)
	if err != nil {
		t.Fatalf("open projection: %v", err)
	}
	defer ms.Close()
	rows, err := ms.Recent("fs:"+project, 1<<20)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("no projected rows for %s", project)
	}
	return rows[0].Id
}

// hardDeleteOneRow takes a row out of the file, so the event's provenance key is
// free again: a row that is GONE, which is the loss AC7 is about.
func hardDeleteOneRow(t *testing.T, path, project string) {
	t.Helper()
	id := oneProjectRowID(t, path, project)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open projection: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM memory WHERE id = ?`, id); err != nil {
		t.Fatalf("hard delete row %d: %v", id, err)
	}
}

// forgetOneRow removes a row with memstore's own removal verb, which is a soft
// delete: the row is hidden and its provenance key stays taken.
func forgetOneRow(t *testing.T, path, project string) {
	t.Helper()
	id := oneProjectRowID(t, path, project)
	ms, err := memstore.Open(path)
	if err != nil {
		t.Fatalf("open projection: %v", err)
	}
	defer ms.Close()
	if err := ms.Forget(id); err != nil {
		t.Fatalf("forget row %d: %v", id, err)
	}
}

// runFSEnvStderr is runFSEnv with the process's stderr, which is where the
// running log — and the projection warning — goes. A test that asserts a WARN
// has to read it.
func runFSEnvStderr(t *testing.T, bin, dir string, extraEnv []string, args ...string) (map[string]any, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "HOME="+testHome(t)), extraEnv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
			if len(out) == 0 {
				out = stderr.Bytes()
			}
		} else {
			t.Fatalf("exec: %v", err)
		}
	}
	var resp map[string]any
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal output %q: %v", string(out), err)
	}
	return resp, stderr.String(), exitCode
}

// mustContain asserts a substring, with the whole string in the failure so a
// reader sees what was there instead.
func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("missing %q in:\n%s", needle, haystack)
	}
}

// mustNotContain is the other half: the thing that must not be said.
func mustNotContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("unexpected %q in:\n%s", needle, haystack)
	}
}

// firstEventID reads the event id out of a write's response.
func firstEventID(t *testing.T, resp map[string]any) string {
	t.Helper()
	data, _ := resp["data"].(map[string]any)
	if id, _ := data["event_id"].(string); id != "" {
		return id
	}
	if events, _ := data["events"].([]any); len(events) > 0 {
		if first, _ := events[0].(map[string]any); first != nil {
			if id, _ := first["event_id"].(string); id != "" {
				return id
			}
		}
	}
	t.Fatalf("no event id in %v", resp)
	return ""
}
