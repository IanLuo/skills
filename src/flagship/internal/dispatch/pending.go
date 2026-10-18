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
	"github.com/flagship-dev/flagship/internal/engine"
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
//
// Slice 2 switched this read: its store-derived answer comes from the memstore
// projection, and it is returned only when the parity gate found it equal to the
// event store's (spec D2, AC2). A divergence refuses the command — it never
// answers from the event store on the side (AC3). Everything herdr contributes
// is applied after the comparison, so the gate never compares an environment
// read against itself.
func Pending(h *command.Handler, hc engine.Engine) command.Response {
	source := command.StateSource("pending")
	answer, err := gatedAnswer("pending", source, h,
		func() (pendingAnswer, error) { return answerFromLog(h) },
		func() (pendingAnswer, error) { return answerFromProjection(h) },
	)
	if err != nil {
		return errResp("pending: " + err.Error())
	}

	result := decoratePending(hc, answer)
	h.Logger().Info("command executed", "command", "pending", "source", source,
		"pending", len(result.Pending), "undispatched_gaps", result.UndispatchedGaps)
	return command.Response{OK: true, Data: result}
}

// decoratePending applies what neither source holds — herdr's agent states and
// the re-run checks — to the answer the gate settled on, and counts the states a
// cap acts on. It reads nothing from the store.
func decoratePending(hc engine.Engine, answer pendingAnswer) PendingResult {
	agents, herdrErr := hc.Agents()

	entries := make([]PendingEntry, 0, len(answer.Dispatches))
	counts := make(map[string]int, len(pendingStates))
	for _, state := range pendingStates {
		counts[state] = 0
	}
	for _, d := range answer.Dispatches {
		entry := PendingEntry{CapNode: d.Node.NodeID}
		if d.Delivery != nil {
			entry.Project = d.Delivery.Project
			entry.Tab = d.Delivery.TabID
			entry.Agent = d.Delivery.Agent
			entry.WorkerNode = d.Delivery.Node
		}
		entry.State = pendingStateOf(d, agents, herdrErr)

		// An unfinished dispatch is still a dispatch the cap has not closed out,
		// so its recorded check is re-run here: it runs in the cap scope's
		// directory, which is this process's own — cap is never a registered
		// project. Nothing is unblocked or closed; the entry just says the claim
		// has expired instead of repeating it as current truth. A node whose
		// report says no more than its status keeps its status out of the read —
		// except a blocked one, whose block is the whole answer and is reported
		// whether or not it still holds.
		if reported := command.NodeStatus(d.Node, ""); reported != d.Node.Status || d.Node.Status == "blocked" {
			entry.Status = reported
		}
		entry.Reports = d.Reports
		counts[entry.State]++
		entries = append(entries, entry)
	}

	result := PendingResult{
		Pending: entries, Counts: counts, UndispatchedGaps: answer.Gaps,
		Cap: capStatusOf(answer, hc),
	}
	if herdrErr != nil {
		result.Warning = fmt.Sprintf(
			"herdr is unavailable (%v): an unfinished worker's state cannot be read, and is reported as %q",
			herdrErr, StateUnknown)
	}
	return result
}

// capStatusOf adds herdr's answer about the claim the gate compared: the claim
// itself is a store value, and whether its pane still exists is the environment
// read that goes beside it.
func capStatusOf(answer pendingAnswer, hc engine.Engine) CapStatus {
	if answer.Cap == nil {
		return CapStatus{State: CapNone}
	}
	claim := *answer.Cap
	status := CapStatus{
		PaneID: claim.PaneID, TabID: claim.TabID,
		Agent: claim.Agent, ClaimedAt: claim.ClaimedAt,
	}
	live, err := hc.PaneExists(claim.PaneID)
	switch {
	case err != nil:
		status.State = CapUnknown
	case live:
		status.State = CapLive
	default:
		status.State = CapDead
	}
	return status
}

// pendingStateOf classifies one dispatch from the answer the gate settled on.
// The worker's node is the truth about whether the work is done, so a done node
// is ready however herdr reads. A record carrying no worker node is unlinked
// before anything else. A herdr that could not be asked is its own state, never
// running.
func pendingStateOf(d pendingDispatch, agents map[string]string, herdrErr error) string {
	if d.Delivery == nil || d.Delivery.Project == "" || d.Delivery.Node == "" {
		return StateUnlinked
	}
	if d.Worker != nil && d.Worker.Status == "done" {
		return StateReady
	}
	if herdrErr != nil {
		return StateUnknown
	}
	state, found := agents[d.Delivery.Agent]
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
