// Pending: which dispatches the cap has not closed out.
//
// The cap's failure mode is losing track of a worker that finished, so this is
// the single question that answers "what is waiting on me?". It reads the cap's
// scope, each delivery record, and the worker's node — and asks herdr for its
// agent states as a hint about when to look, never as the truth about whether
// the work is done.
package dispatch

import (
	"fmt"

	"github.com/flagship-dev/flagship/internal/command"
)

// The states fs pending reports for a dispatch the cap has not closed out.
const (
	// StateReady: the worker's node is done. Read the node, then close.
	StateReady = "ready"
	// StateRunning: the worker's node is not done and herdr still has the agent.
	StateRunning = "running"
	// StateUnseen: the worker's node is not done but herdr reports the agent
	// done — work that finished and nobody looked at.
	StateUnseen = "unseen"
	// StateGone: the worker's node is not done and herdr has no such agent. The
	// worker died; this one needs the user.
	StateGone = "gone"
	// StateUnlinked: no delivery record, or one written before it carried the
	// worker's node. There is nothing to read, and closing needs --worker.
	StateUnlinked = "unlinked"
)

// pendingStates is every state, in the order a cap acts on them.
var pendingStates = []string{StateReady, StateRunning, StateUnseen, StateGone, StateUnlinked}

// PendingEntry is one dispatch node the cap has not closed out.
type PendingEntry struct {
	CapNode    string `json:"cap_node"`
	WorkerNode string `json:"worker_node,omitempty"`
	Project    string `json:"project,omitempty"`
	Tab        string `json:"tab,omitempty"`
	Agent      string `json:"agent,omitempty"`
	State      string `json:"state"`
}

// PendingResult is the data payload of fs pending: one entry per dispatch, a
// count per state so the cap can see at a glance what needs it, and the size of
// the backlog — the kind=gap nodes nobody has picked up, so it is never only
// remembered.
type PendingResult struct {
	Pending          []PendingEntry `json:"pending"`
	Counts           map[string]int `json:"counts"`
	UndispatchedGaps int            `json:"undispatched_gaps"`
	Warning          string         `json:"warning,omitempty"`
}

// Pending lists every dispatch in the cap's scope that is not done, with what
// the cap should do about each. It appends no events and touches no pane.
func Pending(h *command.Handler, hc HerdrCLI) command.Response {
	unresolved, err := unresolvedDispatches(h)
	if err != nil {
		return errResp(fmt.Sprintf("pending: %v", err))
	}

	// herdr says when to look, not whether the work is done. When it cannot be
	// asked, the states that depend on it are reported as still running rather
	// than guessed at: an unreadable herdr is not an agent that has gone.
	agents, herdrErr := hc.Agents()

	entries := make([]PendingEntry, 0, len(unresolved))
	counts := make(map[string]int, len(pendingStates))
	for _, state := range pendingStates {
		counts[state] = 0
	}
	for _, node := range unresolved {
		entry, err := pendingEntry(h, node.NodeID, agents, herdrErr)
		if err != nil {
			return errResp("pending: " + err.Error())
		}
		counts[entry.State]++
		entries = append(entries, entry)
	}

	result := PendingResult{Pending: entries, Counts: counts}
	gaps, err := h.OpenGaps()
	if err != nil {
		return errResp(fmt.Sprintf("pending: %v", err))
	}
	result.UndispatchedGaps = gaps
	if herdrErr != nil {
		result.Warning = herdrWarning(herdrErr)
	}
	return command.Response{OK: true, Data: result}
}

// herdrWarning says why a state that depends on herdr is reported as running
// rather than guessed at. fs pending and fs sweep both read herdr as a hint, so
// both report its absence the same way.
func herdrWarning(err error) string {
	return fmt.Sprintf(
		"herdr is unavailable (%v): an unfinished worker's state cannot be read, and is reported as %q",
		err, StateRunning)
}

// pendingEntry reads one dispatch node's delivery record and the worker node it
// names, and classifies it.
func pendingEntry(h *command.Handler, capNodeID string, agents map[string]string, herdrErr error) (PendingEntry, error) {
	entry := PendingEntry{CapNode: capNodeID}

	delivery, recorded, err := h.DeliveryFor(capScope, capNodeID)
	if err != nil {
		return entry, fmt.Errorf("read delivery record for %s: %w", capNodeID, err)
	}
	if recorded {
		entry.Project = delivery.Project
		entry.Tab = delivery.TabID
		entry.Agent = delivery.Agent
		entry.WorkerNode = delivery.Node
	}

	entry.State = pendingState(h, delivery, recorded, agents, herdrErr)
	return entry, nil
}

// pendingState classifies one dispatch. The worker's node is the truth about
// whether the work is done, so a done node is ready however herdr reads — a
// worker whose pane was looked at reads idle, not done, and is still finished.
//
// A record carrying no worker node is unlinked before anything else: there is no
// node to read, and herdr reporting no such agent would otherwise be mistaken
// for a worker that died.
func pendingState(h *command.Handler, delivery command.DeliveryRecord, recorded bool, agents map[string]string, herdrErr error) string {
	if !recorded || delivery.Project == "" || delivery.Node == "" {
		return StateUnlinked
	}

	if worker, found := scopeNode(h, delivery.Project, delivery.Node); found && worker.Status == "done" {
		return StateReady
	}

	if herdrErr != nil {
		return StateRunning
	}
	state, found := agents[delivery.Agent]
	switch {
	case !found:
		return StateGone
	case state == "done":
		return StateUnseen
	default:
		return StateRunning
	}
}
