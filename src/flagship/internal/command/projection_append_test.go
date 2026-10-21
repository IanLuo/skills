package command_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IanLuo/memstore"
	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/store"
)

// AC5 — every append projects, in the same step, without ever rolling back the
// event.
//
// The static half: the append surface is the 14 event-store sites in
// internal/command/command.go — 10 store.Append and 4 store.AppendBatch — and
// every one of them hands its error to storeWriteOk, which is what clears a
// projection failure without failing the write. task edit is the fourth batch:
// it validates every field before it appends anything, so its accepted set is
// written in one step or not at all. A call site that skipped it
// would report a committed event as a failed write and invite the retry that
// doubles the state.
func TestEveryAppendProjects(t *testing.T) {
	t.Run("every call site wraps its error", func(t *testing.T) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "command.go", nil, 0)
		if err != nil {
			t.Fatalf("parse command.go: %v", err)
		}

		var sites int
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name != "Append" && sel.Sel.Name != "AppendBatch" {
				return true
			}
			inner, ok := sel.X.(*ast.SelectorExpr)
			if !ok || inner.Sel.Name != "store" {
				return true
			}
			sites++

			// The statement that FOLLOWS this one must pass the error to
			// storeWriteOk. Per call site, not per function: TaskUpdate and
			// TaskMerge each hold two sites, and a function-level check would
			// pass with one of the two unwrapped.
			next := statementAfter(file, call.Pos())
			if next == nil || !callsStoreWriteOk(next) {
				t.Errorf("%s: the %s call site is not followed by a storeWriteOk call, so a projection failure would be reported as a failed write",
					fset.Position(call.Pos()), sel.Sel.Name)
			}
			return true
		})

		if sites != 14 {
			t.Errorf("found %d event-store append sites, want the locked 14 (10 Append + 4 AppendBatch)", sites)
		}
	})

	t.Run("a batch folds every event it appends", func(t *testing.T) {
		// fs task unblock appends two events in one atomic batch: a
		// task-unblocked and its paired status-changed. A batch is not one fold,
		// so the projection must hold one row per event, each naming its own.
		h, _ := newProjectedHandler(t)
		node := addNode(t, h, "skills", "a node to unblock")
		if resp := h.TaskBlockWithCheck("skills", node, "blocked on a probe", ""); !resp.OK {
			t.Fatalf("block: %s", resp.Error)
		}
		before := rowOriginIDs(t, h, "skills")

		resp := h.TaskUnblock("skills", node)
		if !resp.OK {
			t.Fatalf("unblock: %s", resp.Error)
		}
		ids := eventIDs(resp)
		if len(ids) != 2 {
			t.Fatalf("task unblock wrote %d events, want 2", len(ids))
		}
		after := rowOriginIDs(t, h, "skills")
		for _, id := range ids {
			if !after[id] {
				t.Errorf("event %s has no projected row; a batch must fold every event it appends", id)
			}
		}
		if len(after)-len(before) < len(ids) {
			t.Errorf("the batch added %d rows for %d events; a batch is not one fold", len(after)-len(before), len(ids))
		}
	})
}

// statementAfter returns the statement that follows the one containing pos,
// within the innermost block that holds it. Overwriting as the walk descends
// leaves the innermost block's answer, which is the one the call site lives in.
func statementAfter(file *ast.File, pos token.Pos) ast.Stmt {
	var next ast.Stmt
	ast.Inspect(file, func(n ast.Node) bool {
		block, ok := n.(*ast.BlockStmt)
		if !ok {
			return true
		}
		for i, st := range block.List {
			if st.Pos() <= pos && pos <= st.End() && i+1 < len(block.List) {
				next = block.List[i+1]
			}
		}
		return true
	})
	return next
}

// callsStoreWriteOk reports whether a statement calls storeWriteOk.
func callsStoreWriteOk(st ast.Stmt) bool {
	found := false
	ast.Inspect(st, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == "storeWriteOk" {
			found = true
		}
		return true
	})
	return found
}

// AC10 — a failed fold heals by re-fold, and the failure tells the user not to
// retry the command. The event is durable before the fold, so the message says
// the action is recorded and names the heal, and the command reports its write
// as the write it was.
func TestFoldFailureMessage(t *testing.T) {
	failure := &store.ProjectionFailure{Scope: "fs:cap", Err: memstore.ErrDuplicateOrigin}
	got := failure.Error()
	want := `recorded; only the projection failed for fs:cap — run "fs state rebuild"`
	if !strings.HasPrefix(got, want) {
		t.Errorf("fold failure message = %q, want it to open %q", got, want)
	}
	for _, forbidden := range []string{"write failed", "failed to write", "append failed"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("fold failure message %q reads as a failed write (%q)", got, forbidden)
		}
	}

	// The same message on the live path: the projection is closed under the
	// handler, so every fold fails, and the write must still succeed.
	h, _ := newProjectedHandler(t)
	if err := h.Projection().Close(); err != nil {
		t.Fatalf("close projection: %v", err)
	}

	var logs bytes.Buffer
	h.SetLogger(slog.New(slog.NewTextHandler(&logs, nil)))

	node := addNode(t, h, "cap", "dispatch code: sample")
	resp := h.TaskUpdate("cap", node, "active", "", nil)
	if !resp.OK {
		t.Fatalf("a failed fold failed the write: %s", resp.Error)
	}
	if !strings.Contains(logs.String(), "only the projection failed for fs:cap") {
		t.Errorf("running log has no fold-failure message; got:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "event_committed=true") {
		t.Errorf("the log does not say the event was committed; got:\n%s", logs.String())
	}
}

// AC5 — a projection failure leaves the event present and the command's write
// result intact.
func TestProjectionFailureKeepsEvent(t *testing.T) {
	h, dbPath := newProjectedHandler(t)
	if err := h.Projection().Close(); err != nil {
		t.Fatalf("close projection: %v", err)
	}
	var logs bytes.Buffer
	h.SetLogger(slog.New(slog.NewTextHandler(&logs, nil)))

	node := addNode(t, h, "skills", "a node whose fold fails")
	resp := h.TaskUpdate("skills", node, "active", "a decision", nil)
	if !resp.OK {
		t.Fatalf("write reported failed: %s", resp.Error)
	}
	written := eventIDs(resp)
	if len(written) != 2 {
		t.Fatalf("write reported %d events, want 2", len(written))
	}

	// The events are in the store, and the node's derived state is what the
	// write said it would be.
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	events, err := s.Replay("skills", nil)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	got := map[string]bool{}
	for _, evt := range events {
		got[evt.ID] = true
	}
	for _, id := range written {
		if !got[id] {
			t.Errorf("event %s is not in the store after a fold failure", id)
		}
	}

	info, found, err := h.NodeIn("skills", node)
	if err != nil || !found {
		t.Fatalf("node after fold failure: found=%v err=%v", found, err)
	}
	if info.Status != "active" {
		t.Errorf("node status = %q, want active", info.Status)
	}

	// The heal: re-attach a projection at the same path and rebuild. The events
	// the failed fold left behind are folded now, and verify says the two agree —
	// which is what makes the failure message's "run fs state rebuild" a repair
	// rather than a hope (AC10).
	if err := h.AttachProjection(filepath.Join(filepath.Dir(dbPath), "mem.db")); err != nil {
		t.Fatalf("re-attach projection: %v", err)
	}
	if _, err := h.StateRebuild(""); err != nil {
		t.Fatalf("rebuild after the failed fold: %v", err)
	}
	issues, _, err := h.StateVerify("")
	if err != nil {
		t.Fatalf("verify after the rebuild: %v", err)
	}
	if len(issues) != 0 {
		for _, issue := range issues {
			t.Errorf("after the heal: %s", issue)
		}
	}
}

// newProjectedHandler opens a handler with a scratch projection attached, the
// way the CLI's process does, and returns the event store's path so a test can
// read the events back without the handler's own connection.
func newProjectedHandler(t *testing.T) (*command.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "store.db")
	h, err := command.NewHandler(dbPath)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	t.Cleanup(func() { h.Close() })
	if err := h.AttachProjection(filepath.Join(dir, "mem.db")); err != nil {
		t.Fatalf("AttachProjection: %v", err)
	}
	return h, dbPath
}

// addNode adds a node of the given kind and returns its id.
func addNode(t *testing.T, h *command.Handler, scope, goal string) string {
	t.Helper()
	resp := h.TaskAddKind(scope, goal, query.KindWork, "", "", "", "", "", nil)
	if !resp.OK {
		t.Fatalf("task add: %s", resp.Error)
	}
	return resp.Data.(command.EventData).NodeID
}

// eventIDs is the event ids a write reported.
func eventIDs(resp command.Response) []string {
	switch data := resp.Data.(type) {
	case command.EventData:
		return []string{data.EventID}
	case command.UpdateResult:
		out := make([]string, 0, len(data.Events))
		for _, evt := range data.Events {
			out = append(out, evt.EventID)
		}
		return out
	}
	return nil
}

// rowOriginIDs is every provenance id the projection holds for a scope.
func rowOriginIDs(t *testing.T, h *command.Handler, scope string) map[string]bool {
	t.Helper()
	rows, err := store.ScopeRows(h.Projection(), scope)
	if err != nil {
		t.Fatalf("scope rows: %v", err)
	}
	out := map[string]bool{}
	for _, m := range rows {
		out[m.OriginId] = true
	}
	return out
}
