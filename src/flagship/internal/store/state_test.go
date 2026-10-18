package store_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// AC10/T10 — a fold killed between the insert and the close leaves a hole, and
// a re-fold closes it.
//
// This is the state the live store was in: a node's older row still open while a
// newer row already existed, so every reader saw the wrong current row, and a
// re-fold skipped at the same place and changed nothing.
//
// A fold cannot be killed on purpose, so the hole is constructed from the
// outside: the newer event's row is written exactly as the fold's insert half
// writes it, and the older row is left open, which is the state the death
// leaves. The re-fold that follows is the real fold.
func TestRefoldClosesAHoleLeftByAKilledFold(t *testing.T) {
	const node = "t-hole"
	older := store.Event{
		ID: "01HOLEA", Type: store.TaskCreated, ProjectID: "proj-hole",
		NodeID: strPtr(node), Timestamp: time.Unix(1700000000, 0).UTC(),
		Payload: json.RawMessage(`{"goal":"the older event","kind":"work"}`),
	}
	newer := store.Event{
		ID: "01HOLEB", Type: store.StatusChanged, ProjectID: "proj-hole",
		NodeID: strPtr(node), Timestamp: time.Unix(1700000100, 0).UTC(),
		Payload: json.RawMessage(`{"to":"done"}`),
	}

	ms, err := memstore.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("memstore open: %v", err)
	}
	defer ms.Close()

	// The older event folds, and its row stays open — nothing newer exists yet.
	if err := store.FoldEvent(ms, older); err != nil {
		t.Fatalf("fold older: %v", err)
	}
	// The newer event's INSERT half: its row is written and the older row is not
	// retired, which is where a fold killed between the two leaves the node.
	insertHalfOf(t, ms, newer, node, "done")

	rows := nodeRowsOf(t, ms, "proj-hole", node)
	if len(rows) != 2 {
		t.Fatalf("node has %d rows, want 2", len(rows))
	}
	if closed(rowByOrigin(t, rows, older.ID)) {
		t.Fatalf("the hole is not there: the older row is already closed (%v)", rowStates(rows))
	}

	// The re-fold: the newer event is already projected, so this is the skip
	// path — and the skip path must still retire what the insert half left open,
	// or the hole can never be closed.
	if err := store.FoldEvent(ms, newer); err != nil {
		t.Fatalf("re-fold newer: %v", err)
	}
	rows = nodeRowsOf(t, ms, "proj-hole", node)
	if !closed(rowByOrigin(t, rows, older.ID)) {
		t.Errorf("after the re-fold the older row is open — the skip path retired nothing (%v)", rowStates(rows))
	}
	if closed(rowByOrigin(t, rows, newer.ID)) {
		t.Errorf("after the re-fold the newer row is closed; a re-fold must not retire the current row")
	}

	// And the state a reader derives is the event store's: done, from the newer
	// event, with the older event's goal.
	scope, err := store.ReadScope(ms, "proj-hole")
	if err != nil {
		t.Fatalf("read scope: %v", err)
	}
	got := scope.Nodes[node]
	if got == nil {
		t.Fatal("no node in the derived state")
	}
	if got.Status != "done" || got.Goal != "the older event" {
		t.Errorf("derived state = status %q goal %q, want done / the older event", got.Status, got.Goal)
	}
}

// insertHalfOf writes one event's node row exactly as the fold's insert half
// writes it: the same envelope, the same provenance, and no retire.
func insertHalfOf(t *testing.T, ms *memstore.Store, evt store.Event, node, status string) {
	t.Helper()
	extra := fmt.Sprintf(`{"event_id":%q,"project_id":%q,"event_type":%q,"fs_node":%q,"payload":%s}`,
		evt.ID, evt.ProjectID, evt.Type, node, evt.Payload)
	if _, err := ms.Put(&memstore.Memory{
		Ts: evt.Timestamp, Kind: "work", Scope: store.ProjectionScope(evt.ProjectID),
		Text: node, Status: status, Extra: extra, FsNode: node,
		Origin: "fs", OriginId: evt.ID,
	}); err != nil {
		t.Fatalf("insert half: %v", err)
	}
}

// The same hole reached by fold ORDER rather than by a death: fold the newer
// event first, then fill the lag, then re-fold — the sequence the live store
// went through.
func TestOutOfOrderFoldsConvergeOnARefold(t *testing.T) {
	const node = "t-order"
	events := []store.Event{
		{ID: "01ORDA", Type: store.TaskCreated, ProjectID: "proj-order", NodeID: strPtr(node),
			Timestamp: time.Unix(1700000000, 0).UTC(), Payload: json.RawMessage(`{"goal":"the goal","kind":"dispatch"}`)},
		{ID: "01ORDB", Type: store.StatusChanged, ProjectID: "proj-order", NodeID: strPtr(node),
			Timestamp: time.Unix(1700000100, 0).UTC(), Payload: json.RawMessage(`{"to":"done"}`)},
	}
	ms, err := memstore.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("memstore open: %v", err)
	}
	defer ms.Close()

	for _, i := range []int{1, 0, 1} { // newer, then the lag, then the re-fold
		if err := store.FoldEvent(ms, events[i]); err != nil {
			t.Fatalf("fold %d: %v", i, err)
		}
	}
	rows := nodeRowsOf(t, ms, "proj-order", node)
	if len(rows) != 2 {
		t.Fatalf("node has %d rows, want 2", len(rows))
	}
	olderRow, newerRow := rowByOrigin(t, rows, "01ORDA"), rowByOrigin(t, rows, "01ORDB")
	if !closed(olderRow) || closed(newerRow) {
		t.Errorf("after the re-fold rows are %v, want the older closed and the newer open", rowStates(rows))
	}
	scope, err := store.ReadScope(ms, "proj-order")
	if err != nil {
		t.Fatalf("read scope: %v", err)
	}
	got := scope.Nodes[node]
	if got == nil || got.Status != "done" || got.Goal != "the goal" || got.Kind != "dispatch" {
		t.Errorf("derived state = %+v, want done / the goal / dispatch", got)
	}
}

func nodeRowsOf(t *testing.T, ms *memstore.Store, project, node string) []memstore.Memory {
	t.Helper()
	rows, err := store.ScopeRows(ms, project)
	if err != nil {
		t.Fatalf("scope rows: %v", err)
	}
	var out []memstore.Memory
	for _, m := range rows {
		if m.FsNode == node && !isSideKind(m.Kind) {
			out = append(out, m)
		}
	}
	return out
}

func isSideKind(kind string) bool {
	switch kind {
	case "work", "dispatch", "gap", "batch":
		return false
	}
	return true
}

func closed(m memstore.Memory) bool { return m.Status == "closed" }

func rowByOrigin(t *testing.T, rows []memstore.Memory, origin string) memstore.Memory {
	t.Helper()
	for _, m := range rows {
		if m.OriginId == origin {
			return m
		}
	}
	t.Fatalf("no row for %s among %v", origin, rowStates(rows))
	return memstore.Memory{}
}

func rowStates(rows []memstore.Memory) []string {
	out := make([]string, 0, len(rows))
	for _, m := range rows {
		out = append(out, m.OriginId+"="+m.Status)
	}
	return out
}
