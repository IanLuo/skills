package dispatch_test

// A fake engine stands in for the herdr CLI: it records what was asked of it and
// returns canned results, so a delivery and a close run without a live terminal.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/dispatch"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/store"
)

// fakeHerdr stands in for the herdr CLI: it records what was asked of it and
// returns canned results, so the gates run without a live terminal.
type fakeHerdr struct {
	tabID     string
	paneID    string
	splitErr  error
	startErr  error
	promptErr error
	closeErr  error
	agents    map[string]string
	agentsErr error

	// startErrs is the race the split leaves behind, made deterministic: herdr
	// answers agent_pane_busy for a pane that is not yet a shell, and this is one
	// canned answer per start until it runs out and startErr takes over.
	startErrs []error

	// agentForPane stands in for the pane→name read a claim makes: herdr names
	// the agent it hosts in a pane, and names none for a pane hosting none.
	agentForPane    map[string]string
	agentForPaneErr error

	// rootPaneID/rootPaneTab/rootPaneErr stand in for the root pane a worktree
	// workspace already has — the pane a worktree dispatch must start in.
	rootPaneID  string
	rootPaneTab string
	rootPaneErr error

	// checkoutPath/checkoutErr stand in for the checkout path a worktree
	// workspace resolves to — the tree the exit checks must run in.
	checkoutPath string
	checkoutErr  error

	// paneExists/paneExistsErr stand in for the read-back a close does after
	// closing the pane: an empty fake reports the pane gone, which is the goal
	// state, and paneExists: true is how a test constructs a pane that survived.
	paneExists    bool
	paneExistsErr error

	// worktreeNewID/worktreeNewPath/worktreeNewBranch/worktreeNewErr stand in
	// for --worktree-new: CreateWorktree returns the workspace id, checkout path
	// and branch a real `herdr worktree create` would, and Deliver records all
	// three on the delivery. An empty worktreeNewBranch is the detached checkout
	// a real herdr reports no branch for, and the zero value of this fake — so a
	// test that says nothing about the branch gets a delivery that records none,
	// which is what the branch postcondition stands down on.
	worktreeNewID     string
	worktreeNewPath   string
	worktreeNewBranch string
	worktreeNewErr    error

	created          []string
	worktreesCreated []string
	started          []string
	startKinds       []string
	startArgs        [][]string
	prompts          []promptCall
	closed           []string
	rooted           []string
	checkouts        []string
	verified         []string
}

// promptCall is one brief handed to a pane, so a test can read the text the
// worker would have received.
type promptCall struct{ pane, text string }

func (f *fakeHerdr) SplitPane(cwd string) (string, string, error) {
	if f.splitErr != nil {
		return "", "", f.splitErr
	}
	f.created = append(f.created, cwd)
	return f.paneID, f.tabID, nil
}

func (f *fakeHerdr) WorktreeRootPane(wsID string) (string, string, string, error) {
	f.rooted = append(f.rooted, wsID)
	if f.rootPaneErr != nil {
		return "", "", "", f.rootPaneErr
	}
	return f.rootPaneID, f.rootPaneTab, f.checkoutPath, nil
}

func (f *fakeHerdr) WorktreeCheckout(wsID string) (string, error) {
	f.checkouts = append(f.checkouts, wsID)
	if f.checkoutErr != nil {
		return "", f.checkoutErr
	}
	return f.checkoutPath, nil
}

func (f *fakeHerdr) CreateWorktree(cwd string) (string, string, string, error) {
	f.worktreesCreated = append(f.worktreesCreated, cwd)
	if f.worktreeNewErr != nil {
		return "", "", "", f.worktreeNewErr
	}
	return f.worktreeNewID, f.worktreeNewPath, f.worktreeNewBranch, nil
}

func (f *fakeHerdr) StartAgent(paneID, name, kind string, args []string) error {
	f.started = append(f.started, paneID+"/"+name)
	f.startKinds = append(f.startKinds, kind)
	f.startArgs = append(f.startArgs, args)
	if len(f.startErrs) > 0 {
		err := f.startErrs[0]
		f.startErrs = f.startErrs[1:]
		return err
	}
	return f.startErr
}

func (f *fakeHerdr) Prompt(paneID, text string) error {
	f.prompts = append(f.prompts, promptCall{pane: paneID, text: text})
	return f.promptErr
}

func (f *fakeHerdr) Agents() (map[string]string, error) {
	if f.agentsErr != nil {
		return nil, f.agentsErr
	}
	return f.agents, nil
}

func (f *fakeHerdr) AgentForPane(paneID string) (string, error) {
	if f.agentForPaneErr != nil {
		return "", f.agentForPaneErr
	}
	return f.agentForPane[paneID], nil
}

func (f *fakeHerdr) ClosePane(paneID string) error {
	if f.closeErr != nil {
		return f.closeErr
	}
	f.closed = append(f.closed, paneID)
	return nil
}

func (f *fakeHerdr) PaneExists(paneID string) (bool, error) {
	f.verified = append(f.verified, paneID)
	if f.paneExistsErr != nil {
		return false, f.paneExistsErr
	}
	return f.paneExists, nil
}

// addCapNode records a dispatch node in the cap scope the way Prepare does: the
// kind carries what a goal prefix once did.
func addCapNode(t *testing.T, f *fixture, goal string) string {
	t.Helper()
	return addCapKindNode(t, f, goal, query.KindDispatch)
}

// prepareOnly runs a real dispatch but hands nothing to a worker.
func prepareOnly(t *testing.T, f *fixture, req dispatch.PrepareRequest) *dispatch.Brief {
	t.Helper()
	resp := f.prepare(req)
	if !resp.OK {
		t.Fatalf("Prepare: %s", resp.Error)
	}
	return resp.Data.(*dispatch.Brief)
}

// runDispatch prepares and delivers a dispatch for real — the node, the brief and
// the delivery record are all written by the code under test — then marks the
// worker's node done, which is what a close reads.
func runDispatch(t *testing.T, f *fixture, req dispatch.PrepareRequest) *dispatch.Brief {
	t.Helper()
	brief := prepareOnly(t, f, req)

	herdr := &fakeHerdr{tabID: "w1:t9", paneID: "w1:p9"}
	if resp := dispatch.Deliver(f.h, herdr, brief); !resp.OK {
		t.Fatalf("Deliver: %s", resp.Error)
	}

	workerProject, workerID, ok := strings.Cut(brief.WorkerNode, ":")
	if !ok {
		t.Fatalf("worker node %q is not PROJECT:NODE", brief.WorkerNode)
	}
	if resp := f.h.TaskUpdate(workerProject, workerID, "done", "the work is finished", nil); !resp.OK {
		t.Fatalf("finish the worker: %s", resp.Error)
	}
	return brief
}

// recordLegacyDelivery writes a delivery record straight into the store, the
// way a binary older than the current DeliveryRecord did: pane, agent, and
// engine, and nothing else. RecordDelivery refuses a record with no node or no
// project, so this is the only way to construct the shape a close has to read
// after an upgrade — and the reason close reads those two fields defensively.
// project is what the caller wants the record to name; "" is the record written
// before deliveries carried one at all.
func recordLegacyDelivery(t *testing.T, f *fixture, capNode, paneID, project string) {
	t.Helper()
	s, err := store.Open(f.storeDB)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	payload, err := json.Marshal(map[string]string{
		"pane_id": paneID, "agent": "dispatch-dev-task", "engine": "herdr",
		"project": project, "type": "dev-task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(store.Event{
		Type: store.DeliveryRecorded, ProjectID: "cap", NodeID: &capNode, Payload: payload,
	}); err != nil {
		t.Fatalf("append legacy delivery-recorded: %v", err)
	}
}
