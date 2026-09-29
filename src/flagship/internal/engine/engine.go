// Package engine defines the distribution engine interface: the dispatcher
// surface a playbook selects with its `engine:` field. Engine adapters translate
// dispatch's intent — split a pane, start a named agent in it, send the brief,
// tear it down — into engine-specific calls. (ARCHITECTURE R2: Engine Adapters
// layer; SYSTEM-DESIGN R3)
package engine

import "errors"

// ErrPaneGone reports that a pane no longer exists. Closing an already-closed
// pane is a success, not a failure: the dispatch is simply already torn down.
var ErrPaneGone = errors.New("pane is already gone")

// ErrPaneBusy reports that a pane cannot host an agent yet — a split a moment
// ago is not at a shell prompt. It is a race and not a refusal, so the caller
// retries it; an adapter maps its own engine's way of saying this onto the
// sentinel, so no caller has to know the engine's error codes.
var ErrPaneBusy = errors.New("pane is not ready to host an agent")

// ErrNoRootPane reports that a worktree workspace has no pane a worker can be
// started in: none of its panes sits at the worktree's checkout. A worktree
// dispatch is refused rather than falling back to splitting the cap's pane,
// which would run the worker in the main checkout while the record claimed
// isolation.
var ErrNoRootPane = errors.New("worktree workspace has no root pane")

// ErrNoWorktree reports that a worktree workspace cannot be resolved to a
// checkout path: the engine does not know the workspace, or it is not a
// worktree. Every gate that needs the tree refuses on it rather than falling
// back to the project root — running a check against a tree the dispatch never
// touched, and reporting success, is worse than refusing.
var ErrNoWorktree = errors.New("worktree workspace cannot be resolved")

// Engine is the dispatcher surface: the slice of the dispatcher's CLI the
// dispatch lifecycle uses — split a pane for the worker, start an agent in it,
// send the brief, read what the agents are doing, and tear the pane down again.
// v1 is herdr, which implements it in internal/engine/herdr; a playbook that
// declares `engine: herdr` resolves to that adapter.
//
// A dispatch in the project root gets a sibling pane of the cap's own, not a
// tab. A tab created by id carried no workspace, so a worker could land in
// another session's workspace and the engine has no command to move it back; a
// split is anchored to the cap's pane and so to the cap's workspace.
//
// A worktree dispatch gets no pane at all: the worktree workspace already has
// one, and splitting the cap's pane would put the worker in the main checkout.
type Engine interface {
	// SplitPane splits the pane the caller runs in — the cap's own — to the
	// right, without moving focus, and returns the new pane's id along with the
	// tab it landed in. cwd is where the worker starts, which is the project
	// root: the split would inherit the cap's directory otherwise.
	SplitPane(cwd string) (paneID, tabID string, err error)
	// WorktreeRootPane returns the pane a worktree dispatch starts its worker
	// in: the root pane the engine created with the workspace, whose cwd is the
	// worktree's own checkout. It returns that checkout too, because resolving
	// the pane means resolving the path — and the path is what the delivery
	// record must keep: the engine can forget the workspace before close, and
	// then the workspace id names nothing that can be removed. It returns
	// ErrNoWorktree when the workspace cannot be resolved and ErrNoRootPane when
	// it has no pane at the checkout — both naming wsID.
	WorktreeRootPane(wsID string) (paneID, tabID, checkout string, err error)
	// WorktreeCheckout returns the checkout path of the worktree workspace wsID.
	// It is how the cleanup gate finds the tree the dispatch worked in when the
	// record names no path of its own — a record written before the path was
	// recorded. ErrNoWorktree means the workspace could not be resolved, which a
	// gate refuses on rather than guessing.
	WorktreeCheckout(wsID string) (string, error)
	// CreateWorktree creates a new worktree workspace rooted at cwd and returns
	// its workspace id, its checkout path, and the branch the engine put that
	// checkout on. It is the mechanism behind --worktree-new: the workspace's
	// root pane sits at the checkout, which is what the delivery record must keep
	// for the teardown to remove it, and the branch is what it must keep for the
	// close to assert the removal happened. The engine names the branch when
	// --branch is not given, so the create response is the only place this
	// delivery can learn which branch it caused to exist; a detached checkout
	// reports none, and that is an empty string rather than a guess.
	CreateWorktree(cwd string) (workspaceID, checkout, branch string, err error)
	// StartAgent starts an interactive agent named name in an existing pane, of
	// the kind the plan's `agent:` field resolved to and with the arguments its
	// `agent_args:` field named — one argv element per entry, in order. A pane
	// that cannot host an agent yet returns ErrPaneBusy.
	StartAgent(paneID, name, kind string, args []string) error
	// Prompt submits text to the agent hosted by paneID.
	Prompt(paneID, text string) error
	// Agents reports every agent the engine knows, by name, with the state it
	// reports for it. fs pending reads it to tell a worker that is still going
	// from one that finished unseen or died; an error means the engine could not
	// be asked, which is not the same as no agent being there.
	Agents() (map[string]string, error)
	// AgentForPane returns the name the engine reports for the agent it hosts in
	// paneID, and "" when it names none there. It is how a claim records the
	// agent beside the pane: the engine routes to the pane, and the name is what
	// a person recognises. An error means the engine could not be asked, which is
	// not the same as there being no agent.
	AgentForPane(paneID string) (string, error)
	// ClosePane closes paneID, returning ErrPaneGone if it no longer exists.
	ClosePane(paneID string) error
	// PaneExists reports whether the engine still knows paneID. It is how
	// close-out confirms a pane it closed is really gone: a close that returns
	// nil is a claim, and the error-mapped "already gone" it treats as success is
	// the same mapping that once reported a worktree gone while its checkout
	// stayed on disk. An error means the question could not be answered, which is
	// not the same as the pane being absent.
	PaneExists(paneID string) (bool, error)
}
