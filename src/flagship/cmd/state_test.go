package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/IanLuo/memstore"
)

// AC7 — rebuild and verify exist and do what they say. A scratch home, one
// project, one node; the projection starts empty (which is the first-run state),
// verify says so, rebuild seeds it, a removed row makes verify name the scope and
// the differing field, and rebuild restores it. No non-fs row is touched.
func TestStateVerifyRebuild(t *testing.T) {
	bin := getFS(t)
	home := testHome(t)
	dir := t.TempDir()

	// The writes run against a throwaway projection, so the live one is absent
	// when verify first looks — the first-run state AC4 names. (Every append
	// folds, so a write against the live file would seed it.)
	elsewhere := "FS_MEMSTORE=" + filepath.Join(t.TempDir(), "memory.db")
	if resp, code := runFSEnv(t, bin, dir, []string{elsewhere}, "project", "create", "--name", "statetest"); code != 0 || resp["ok"] != true {
		t.Fatalf("project create: code=%d resp=%v", code, resp)
	}
	if resp, code := runFSEnv(t, bin, dir, []string{elsewhere}, "task", "add", "--goal", "a node to project", "--project", "statetest"); code != 0 || resp["ok"] != true {
		t.Fatalf("task add: code=%d resp=%v", code, resp)
	}

	// A personal memory, so "touches no non-fs row" is a claim about a row that
	// exists rather than about an empty set.
	projPath := filepath.Join(home, ".memstore", "memory.db")
	seedPersonalMemory(t, projPath)
	before := nonFSBytes(t, projPath)

	// The live projection is empty: the first-run state. A switched read would
	// refuse here, and verify says what to run.
	resp, code := runFS(t, bin, dir, "state", "verify")
	if code != 1 {
		t.Fatalf("verify before the seed: code=%d resp=%v, want 1 (the projection is empty)", code, resp)
	}
	if err, _ := resp["error"].(string); !strings.Contains(err, "statetest") || !strings.Contains(err, "fs state rebuild") {
		t.Fatalf("verify refusal = %q, want it to name the scope and the remedy", resp["error"])
	}

	// The seed makes it clean.
	if resp, code := runFS(t, bin, dir, "state", "rebuild"); code != 0 || resp["ok"] != true {
		t.Fatalf("rebuild: code=%d resp=%v", code, resp)
	}
	if resp, code := runFS(t, bin, dir, "state", "verify"); code != 0 || resp["ok"] != true {
		t.Fatalf("verify after rebuild: code=%d resp=%v", code, resp)
	}

	// A row the projection does not have is a drift verify catches by name: a
	// write that ran without the fold (a failed fold, or a process whose
	// projection was not open) leaves the event store ahead of the projection,
	// which is the lagging direction the ordering NFR allows and the one a
	// re-fold heals.
	if resp, code := runFSEnv(t, bin, dir, []string{elsewhere}, "task", "add", "--goal", "a node added while the projection was closed", "--project", "statetest"); code != 0 || resp["ok"] != true {
		t.Fatalf("lagging write: code=%d resp=%v", code, resp)
	}
	resp, code = runFS(t, bin, dir, "state", "verify")
	if code != 1 {
		t.Fatalf("verify after removing a row: code=%d resp=%v, want 1", code, resp)
	}
	if err, _ := resp["error"].(string); !strings.Contains(err, "statetest") || !strings.Contains(err, "fs state rebuild") {
		t.Fatalf("verify refusal = %q, want the scope and the remedy", resp["error"])
	}

	// And rebuild restores it.
	if resp, code := runFS(t, bin, dir, "state", "rebuild"); code != 0 || resp["ok"] != true {
		t.Fatalf("rebuild after drift: code=%d resp=%v", code, resp)
	}
	if resp, code := runFS(t, bin, dir, "state", "verify"); code != 0 || resp["ok"] != true {
		t.Fatalf("verify after the repair: code=%d resp=%v", code, resp)
	}

	if after := nonFSBytes(t, projPath); after != before {
		t.Errorf("the non-fs rows changed across verify and rebuild:\n before %s\n after  %s", before, after)
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

// nonFSBytes renders every non-fs row of the projection file.
func nonFSBytes(t *testing.T, path string) string {
	t.Helper()
	ms, err := memstore.Open(path)
	if err != nil {
		t.Fatalf("open projection: %v", err)
	}
	defer ms.Close()
	rows, err := ms.Recent("", 1<<20)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	var b strings.Builder
	for _, m := range rows {
		if strings.HasPrefix(m.Scope, "fs:") {
			continue
		}
		b.WriteString(m.Kind)
		b.WriteString("|")
		b.WriteString(m.Scope)
		b.WriteString("|")
		b.WriteString(m.Text)
		b.WriteString("|")
		b.WriteString(m.Extra)
		b.WriteString("\n")
	}
	return b.String()
}
