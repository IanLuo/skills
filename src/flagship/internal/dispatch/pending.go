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
	// StateBlocked: the worker's node is not done and herdr reports the agent
	// blocked — a pane parked on a prompt or a modal that nothing in the worker's
	// own session will dismiss. It is the one unfinished state that is not work in
	// progress: the harness stopped and only a person can move it, so it is
	// reported beside ready rather than left to look like thinking.
	StateBlocked = "blocked"
	// StateRunning: the worker's node is not done, herdr was asked, and the
	// state it reported is neither `blocked` nor `done` — working and idle among
	// them, and also a value herdr reports as `unknown`, an unrecognised value
	// and an empty one, because the switch below has `running` as its default
	// arm. (No agent at all is StateGone, not this; a herdr that could not be
	// asked at all is StateUnknown, not this.) Idle is included deliberately: an
	// agent between two tool calls is not blocked, and a state that fires on both
	// is a state the cap learns to ignore. The default errs the same way and for
	// the same reason: a state that could not be read is not a worker parked on a
	// prompt, and reporting it as one would spend the cap's attention on a claim
	// herdr never made.
	StateRunning = "running"
	// StateUnknown: the worker's node is not done and herdr could not be asked at
	// all, so nothing about this worker was read — it may be working, dead, or
	// there may be no herdr on this machine. It is not StateRunning: that one
	// says an agent exists and is not blocked or done, which is a claim about a
	// worker somebody measured, and every other route into it follows an actual
	// herdr read. It takes its word, and its fact, from the cap's own unknown,
	// because "herdr could not be asked" is one fact about one read.
	StateUnknown = "unknown"
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
var pendingStates = []string{StateReady, StateBlocked, StateRunning, StateUnknown, StateUnseen, StateGone, StateUnlinked}

// PendingEntry is one dispatch node the cap has not closed out.
type PendingEntry struct {
	CapNode    string `json:"cap_node"`
	WorkerNode string `json:"worker_node,omitempty"`
	Project    string `json:"project,omitempty"`
	Tab        string `json:"tab,omitempty"`
	Agent      string `json:"agent,omitempty"`
	State      string `json:"state"`
	// Status is the node's own status when it is blocked: plain "blocked", or
	// "blocked (condition no longer holds …)" when the check the block recorded
	// now exits 0. It also carries a node's claim once the check it recorded has
	// expired — "<status> (claim no longer holds …)". Both are resolved readings:
	// the recorded condition is over, which is the one thing a reader must not be
	// left to repeat as current truth.
	//
	// The third reading is the opposite one. A check the deadline stopped says
	// nothing about the claim — it was neither confirmed nor denied — and reads
	// "blocked [check did not finish …]" or "<status> [check did not finish …]",
	// so a reader is not told a condition is over that nobody finished asking
	// about. A resolved reading is parenthesised and an unresolved one bracketed:
	// the two senses are opposite, and the delimiter is what tells them apart, so
	// a reader keyed on "the status is decorated" cannot read a timeout as an
	// expiry.
	//
	// It is empty for any other node, where State is the whole answer.
	Status string `json:"status,omitempty"`
	// Reports is every report the worker recorded on its node, oldest first. A
	// worker waiting on a decision reads as running, whose documented meaning is
	// "let it work" — these are what make the wait visible from the read the cap
	// already makes.
	Reports []command.Report `json:"reports,omitempty"`
}

// PendingResult is the data payload of fs pending: one entry per dispatch, a
// count per state so the cap can see at a glance what needs it, and the size of
// the backlog — the kind=gap nodes nobody has picked up, so it is never only
// remembered.
type PendingResult struct {
	Pending []PendingEntry `json:"pending"`
	// Cap names the claimed address and whether herdr still has it, so the read
	// the cap already makes is also the read that tells it it has been
	// superseded — and the read that tells a fresh session the scope is free.
	Cap              CapStatus      `json:"cap"`
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
	// asked, the states that depend on it are reported as unread rather than
	// guessed at: an unreadable herdr is not an agent that has gone, and it is
	// not an agent that was seen working either.
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
		// An unfinished dispatch is still a dispatch the cap has not closed out,
		// so its recorded check is re-run here: it runs in the cap scope's
		// directory, which is this process's own — cap is never a registered
		// project. Nothing is unblocked or closed; the entry just says the claim
		// has expired instead of repeating it as current truth. A node whose
		// report says no more than its status keeps its status out of the read —
		// except a blocked one, whose block is the whole answer and is reported
		// whether or not it still holds.
		if reported := command.NodeStatus(node, ""); reported != node.Status || node.Status == "blocked" {
			entry.Status = reported
		}
		counts[entry.State]++
		entries = append(entries, entry)
	}

	result := PendingResult{Pending: entries, Counts: counts}
	capState, err := CapStatusOf(h, hc)
	if err != nil {
		return errResp(fmt.Sprintf("pending: %v", err))
	}
	result.Cap = capState
	gaps, err := h.OpenGaps()
	if err != nil {
		return errResp(fmt.Sprintf("pending: %v", err))
	}
	result.UndispatchedGaps = gaps
	if herdrErr != nil {
		result.Warning = fmt.Sprintf(
			"herdr is unavailable (%v): an unfinished worker's state cannot be read, and is reported as %q",
			herdrErr, StateUnknown)
	}
	return command.Response{OK: true, Data: result}
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

	// A report lives on the worker's own node, so a record that names no worker
	// node names no reports either: there is nothing to read and the entry is
	// already unlinked.
	if recorded && delivery.Project != "" && delivery.Node != "" {
		reports, err := h.ReportsFor(delivery.Project, delivery.Node)
		if err != nil {
			return entry, fmt.Errorf("read reports for %s:%s: %w", delivery.Project, delivery.Node, err)
		}
		entry.Reports = reports
	}
	return entry, nil
}

// pendingState classifies one dispatch. The worker's node is the truth about
// whether the work is done, so a done node is ready however herdr reads — a
// worker whose pane was looked at reads idle, not done, and is still finished.
//
// A record carrying no worker node is unlinked before anything else: there is no
// node to read, and herdr reporting no such agent would otherwise be mistaken
// for a worker that died.
//
// A herdr that could not be asked is its own state, never running: every other
// route into running follows a read that placed the agent, and reporting an
// unread worker as one that was read working is the state the cap acts on.
func pendingState(h *command.Handler, delivery command.DeliveryRecord, recorded bool, agents map[string]string, herdrErr error) string {
	if !recorded || delivery.Project == "" || delivery.Node == "" {
		return StateUnlinked
	}

	if worker, found := scopeNode(h, delivery.Project, delivery.Node); found && worker.Status == "done" {
		return StateReady
	}

	if herdrErr != nil {
		return StateUnknown
	}
	state, found := agents[delivery.Agent]
	switch {
	case !found:
		return StateGone
	case state == "done":
		return StateUnseen
	case state == "blocked":
		return StateBlocked
	default:
		return StateRunning
	}
}
