package dispatch_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/dispatch"
	"github.com/flagship-dev/flagship/internal/engine"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/registry"
)

// fixture wires a handler and a registry isolated in temp dirs. fsRoot is the
// temp HOME's `~/.fs`: every scope hangs off it, so nothing a test does can
// reach the operator's real store or logs.
type fixture struct {
	h       *command.Handler
	reg     *registry.Registry
	storeDB string
	fsRoot  string
}

func newFixture(t *testing.T, root string) *fixture {
	t.Helper()
	fsRoot := t.TempDir()

	dbPath := filepath.Join(fsRoot, "store.db")
	h, err := command.NewHandler(dbPath)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	t.Cleanup(func() { h.Close() })

	// The projection every append folds into, in the same temp dir as the event
	// store: the fixture carries both files the way the CLI's process does.
	if err := h.AttachProjection(filepath.Join(fsRoot, "mem.db")); err != nil {
		t.Fatalf("AttachProjection: %v", err)
	}

	reg, err := registry.Open(filepath.Join(fsRoot, "registry.db"))
	if err != nil {
		t.Fatalf("registry.Open: %v", err)
	}
	t.Cleanup(func() { reg.Close() })
	if err := reg.Register("skills", root); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Tests create cap-scope dispatch nodes, and one left unresolved makes later
	// dispatches refuse. Close them, so the suite never becomes order-dependent.
	t.Cleanup(func() { resolveCapScope(h) })

	return &fixture{h: h, reg: reg, storeDB: dbPath, fsRoot: fsRoot}
}

// resolveCapScope marks every node in the cap scope done. Best-effort: cleanup
// must not fail a test that already passed.
func resolveCapScope(h *command.Handler) {
	nodes, err := h.UnfinishedIn("cap")
	if err != nil {
		return
	}
	for _, node := range nodes {
		h.TaskUpdate("cap", node.NodeID, "done", "test cleanup", nil)
	}
}

// devReq is the ordinary dispatch these tests prepare.
func devReq(goal string) dispatch.PrepareRequest {
	return dispatch.PrepareRequest{Project: "skills", TaskType: "dev-task", Goal: goal}
}

// prepare runs a dispatch against this fixture's store and registry, so tests
// exercise the real signature without repeating it.
func (f *fixture) prepare(req dispatch.PrepareRequest) command.Response {
	return dispatch.Prepare(f.h, f.reg, req)
}

// close runs the close-out the same way.
func (f *fixture) close(hc engine.Engine, req dispatch.CloseRequest) command.Response {
	return dispatch.Close(f.h, hc, f.reg, req)
}

// addCapKindNode records a cap-scope node of one kind and returns its id.
func addCapKindNode(t *testing.T, f *fixture, goal string, kind query.Kind) string {
	t.Helper()
	added := f.h.TaskAddKind("cap", goal, kind, "", "", "", "", "", nil)
	if !added.OK {
		t.Fatalf("task add: %s", added.Error)
	}
	return added.Data.(command.EventData).NodeID
}

// addIntegrationNode records an integration dispatch that names a member, the
// way fs dispatch --integrates does: one task-created event carrying the link.
func addIntegrationNode(t *testing.T, f *fixture, member string) string {
	t.Helper()
	added := f.h.TaskAddKind("cap", "dispatch integrate: merge the member", query.KindDispatch, "", member, "", "", "", nil)
	if !added.OK {
		t.Fatalf("task add: %s", added.Error)
	}
	return added.Data.(command.EventData).NodeID
}

// scopeTasks returns every node in a scope, via the same derived state fs status
// reports.
func scopeTasks(t *testing.T, f *fixture, scope string) []command.TaskInfo {
	t.Helper()
	resp := f.h.Status(scope)
	if !resp.OK {
		t.Fatalf("Status: %s", resp.Error)
	}
	return resp.Data.(command.StatusResult).Tasks
}

// scopeTask returns one node's derived state, or ok=false when the scope has no
// such node.
func scopeTask(t *testing.T, f *fixture, scope, nodeID string) (command.TaskInfo, bool) {
	t.Helper()
	for _, task := range scopeTasks(t, f, scope) {
		if task.NodeID == nodeID {
			return task, true
		}
	}
	return command.TaskInfo{}, false
}

// capDecisions returns the decisions recorded on one cap-scope node.
func capDecisions(t *testing.T, f *fixture, nodeID string) []string {
	t.Helper()
	task, ok := scopeTask(t, f, "cap", nodeID)
	if !ok {
		t.Fatalf("cap node %s not found", nodeID)
	}
	return task.Decisions
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
