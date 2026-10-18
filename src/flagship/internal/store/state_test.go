package store_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IanLuo/memstore"
	"github.com/flagship-dev/flagship/internal/store"
)

// AC6 — scope safety is enforced, not intended.
//
// A scratch memstore seeded with non-fs: rows (the personal scope "" and a
// literal "personal" scope) must be byte-identical in those rows after a fold,
// after a read, after verify and after rebuild; no projection read may return a
// non-fs: row; and no projection write may construct a scope that does not start
// "fs:".
func TestProjectionScopeSafety(t *testing.T) {
	dir := t.TempDir()
	ms, err := memstore.Open(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatalf("memstore open: %v", err)
	}
	defer ms.Close()

	// Personal memories: the rows fs must never touch, read or count.
	for i, scope := range []string{"", "personal"} {
		if _, err := ms.Put(&memstore.Memory{
			Kind: "note", Scope: scope, Text: "a personal memory " + scope,
			Extra: `{"private":true}`, Origin: "native",
		}); err != nil {
			t.Fatalf("seed personal row %d: %v", i, err)
		}
	}
	before, seeded := nonFSRows(t, ms)
	if seeded != 2 {
		t.Fatalf("seeded %d non-fs rows, want 2", seeded)
	}

	// The event store and its scopes. Every append folds, so the fold under
	// test is the append path's own.
	dbPath := filepath.Join(dir, "store.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer s.Close()
	s.AttachProjection(ms)

	events := fixtureEvents(t)
	for _, scopeName := range []string{"proj-a", "proj-b"} {
		for _, evt := range events {
			evt.ProjectID = scopeName
			if _, err := s.Append(evt); err != nil {
				t.Fatalf("append %s: %v", scopeName, err)
			}
		}
	}
	if after, _ := nonFSRows(t, ms); after != before {
		t.Errorf("a fold changed the non-fs rows:\n before %s\n after  %s", before, after)
	}

	// A read returns fs: rows only, for the scope it was asked about.
	scope, err := store.ReadScope(ms, "proj-a")
	if err != nil {
		t.Fatalf("ReadScope: %v", err)
	}
	if len(scope.NodeIDs) == 0 {
		t.Fatal("the fold wrote no nodes")
	}
	rows, err := store.ScopeRows(ms, "proj-a")
	if err != nil {
		t.Fatalf("ScopeRows: %v", err)
	}
	for _, m := range rows {
		if !strings.HasPrefix(m.Scope, "fs:") {
			t.Errorf("a projection read returned a %q row", m.Scope)
		}
	}

	issues, err := s.StateVerify("")
	if err != nil {
		t.Fatalf("StateVerify: %v", err)
	}
	if len(issues) != 0 {
		for _, issue := range issues {
			t.Errorf("verify after the fold: %s", issue)
		}
	}
	if after, _ := nonFSRows(t, ms); after != before {
		t.Errorf("verify changed the non-fs rows:\n before %s\n after  %s", before, after)
	}

	if _, err := s.StateRebuild(""); err != nil {
		t.Fatalf("StateRebuild: %v", err)
	}
	if after, _ := nonFSRows(t, ms); after != before {
		t.Errorf("rebuild changed the non-fs rows:\n before %s\n after  %s", before, after)
	}

	// Every row the projection wrote names a scope fs owns. This is the write
	// half of the same rule: a row that is not fs's would be a scope fs
	// constructed, and no other writer's row may carry fs's provenance.
	all, err := ms.Recent("", 1<<20)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	for _, m := range all {
		if m.Origin != "fs" {
			continue
		}
		if !strings.HasPrefix(m.Scope, "fs:") {
			t.Errorf("an fs-provenance row carries scope %q", m.Scope)
		}
	}
}

// nonFSRows renders every non-fs row in a file, with how many there are, so a
// test can prove a byte-identical non-fs set rather than an unchanged count.
func nonFSRows(t *testing.T, ms *memstore.Store) (string, int) {
	t.Helper()
	rows, err := ms.Recent("", 1<<20)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	var kept []memstore.Memory
	for _, m := range rows {
		if strings.HasPrefix(m.Scope, "fs:") {
			continue
		}
		kept = append(kept, m)
	}
	raw, err := json.Marshal(kept)
	if err != nil {
		t.Fatalf("marshal non-fs rows: %v", err)
	}
	return string(raw), len(kept)
}

// AC10 — re-folding over an already-projected prefix skips those rows via
// ErrDuplicateOrigin instead of erroring, which is what makes the heal a re-fold
// rather than a hand repair.
func TestRefoldSkipsDuplicates(t *testing.T) {
	events := fixtureEvents(t)
	dir := t.TempDir()
	ms, err := memstore.Open(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatalf("memstore open: %v", err)
	}
	defer ms.Close()

	if _, err := store.ProjectEvents(ms, "proj-a", events); err != nil {
		t.Fatalf("first fold: %v", err)
	}
	first := rowCount(t, ms)

	// The whole stream again: every Put is refused as a duplicate, and every
	// refusal is a skip.
	if _, err := store.ProjectEvents(ms, "proj-a", events); err != nil {
		t.Fatalf("re-fold over a fully projected stream: %v", err)
	}
	if got := rowCount(t, ms); got != first {
		t.Errorf("re-fold changed the row count: %d -> %d", first, got)
	}

	// A partly folded stream: the prefix is skipped, the tail is written, and
	// the heal completes without a hand repair.
	half := len(events) / 2
	dir2 := t.TempDir()
	ms2, err := memstore.Open(filepath.Join(dir2, "memory.db"))
	if err != nil {
		t.Fatalf("memstore open 2: %v", err)
	}
	defer ms2.Close()
	if _, err := store.ProjectEvents(ms2, "proj-a", events[:half]); err != nil {
		t.Fatalf("partial fold: %v", err)
	}
	if _, err := store.ProjectEvents(ms2, "proj-a", events); err != nil {
		t.Fatalf("re-fold over a partly projected stream: %v", err)
	}
	if got, want := rowCount(t, ms2), first; got != want {
		t.Errorf("healed row count = %d, want %d (the same as a single full fold)", got, want)
	}
}

func rowCount(t *testing.T, ms *memstore.Store) int {
	t.Helper()
	rows, err := ms.Recent("", 1<<20)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	return len(rows)
}
