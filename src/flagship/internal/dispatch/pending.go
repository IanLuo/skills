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
	CapNode string `json:"cap_node"`
	// CapProject is the scope the dispatch node lives in. It is `cap` for a
	// dispatch the cap prepared in its own scope, and a project's id for one
	// written there — a dispatch is a dispatch by its kind, not by its scope. It
	// is always present because a reader that has to address the node needs it:
	// a bare node id resolves in the cap's scope, so an id without this field is
	// a dispatch the reader can name and cannot reach.
	CapProject string `json:"cap_project"`
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
// The store-derived answer comes from the event log, and nothing else: the log is
// the only source a state read may answer from (`.agents/specs/fs-contract.md`
// R2). Everything herdr contributes — agent and pane states, the re-run checks,
// the unavailable warning — is applied after that answer is derived, because the
// store has none of it.
func Pending(h *command.Handler, hc engine.Engine) command.Response {
	answer, err := answerFromLog(h)
	if err != nil {
		return errResp("pending: " + err.Error())
	}

	result := decoratePending(hc, answer)
	h.Logger().Info("command executed", "command", "pending",
		"pending", len(result.Pending), "undispatched_gaps", result.UndispatchedGaps)
	return command.Response{OK: true, Data: result}
}

// decoratePending applies what the store does not hold — herdr's agent states
// and the re-run checks — to the derived answer, and counts the states a cap acts
// on. It reads nothing from the store.
func decoratePending(hc engine.Engine, answer pendingAnswer) PendingResult {
	agents, herdrErr := hc.Agents()

	entries := make([]PendingEntry, 0, len(answer.Dispatches))
	counts := make(map[string]int, len(pendingStates))
	for _, state := range pendingStates {
		counts[state] = 0
	}
	for _, d := range answer.Dispatches {
		entry := PendingEntry{CapNode: d.Node.NodeID, CapProject: d.Node.ProjectID}
		if entry.CapProject == "" {
			entry.CapProject = capScope
		}
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

// capStatusOf adds herdr's answer about the claim: the claim itself is a store
// value, and whether its pane still exists is the environment read that goes
// beside it.
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

// pendingStateOf classifies one dispatch from the derived answer. The worker's
// node is the truth about whether the work is done, so a done node
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

// pendingAnswer is every value fs pending asks the store, before the environment
// is read: the unresolved dispatch nodes, each dispatch's delivery record, its
// worker's node and reports, the cap's claim, and the open-gap count.
//
// Everything derived from herdr is applied after this, because the store has it
// not.
type pendingAnswer struct {
	Dispatches []pendingDispatch `json:"dispatches"`
	Cap        *command.CapClaim `json:"cap,omitempty"`
	// Gaps is the open-gap count across every scope.
	Gaps int `json:"undispatched_gaps"`
}

// pendingDispatch is one unresolved dispatch and the state its record leads to.
type pendingDispatch struct {
	Node     command.UnfinishedNode  `json:"node"`
	Delivery *command.DeliveryRecord `json:"delivery,omitempty"`
	Worker   *command.UnfinishedNode `json:"worker,omitempty"`
	Reports  []command.Report        `json:"reports,omitempty"`
}

// answerFromLog derives the pending answer from the event store: every scope's
// unresolved dispatches, each one's delivery record read in the scope that holds
// the dispatch, and the node and reports of the worker that record names.
//
// The scopes are every scope the events name, not the cap's alone. A dispatch is
// a dispatch by its kind, wherever it was written, and a read filtered to the
// cap's scope reported nothing in flight while four dispatch nodes stood in project
// scopes — the read the cap is told to trust at the start of every turn. The
// open-gap count is taken in the same walk, which it already made over every
// scope.
func answerFromLog(h *command.Handler) (pendingAnswer, error) {
	scopes, err := h.Scopes()
	if err != nil {
		return pendingAnswer{}, fmt.Errorf("list scopes: %w", err)
	}

	answer := pendingAnswer{Dispatches: []pendingDispatch{}}
	gaps := make(map[string]int, len(scopes))
	for _, scope := range scopes {
		nodes, err := h.UnfinishedIn(scope)
		if err != nil {
			return pendingAnswer{}, fmt.Errorf("replay %s: %w", scope, err)
		}
		for _, node := range nodes {
			if node.Kind == "gap" {
				gaps[scope]++
				continue
			}
			if node.Kind != "dispatch" {
				continue
			}

			d := pendingDispatch{Node: node}
			delivery, recorded, err := h.DeliveryFor(scope, node.NodeID)
			if err != nil {
				return pendingAnswer{}, fmt.Errorf("read delivery record for %s:%s: %w", scope, node.NodeID, err)
			}
			if recorded {
				d.Delivery = &delivery
				if delivery.Project != "" && delivery.Node != "" {
					worker, found, err := workerOf(h, delivery.Project, delivery.Node)
					if err != nil {
						return pendingAnswer{}, fmt.Errorf("read node %s:%s: %w", delivery.Project, delivery.Node, err)
					}
					if found {
						d.Worker = &worker
					}
					reports, err := h.ReportsFor(delivery.Project, delivery.Node)
					if err != nil {
						return pendingAnswer{}, fmt.Errorf("read reports for %s:%s: %w", delivery.Project, delivery.Node, err)
					}
					d.Reports = reports
				}
			}
			answer.Dispatches = append(answer.Dispatches, d)
		}
	}

	answer.Dispatches = onePerDispatch(answer.Dispatches)

	claim, claimed, err := h.CapFor(capScope)
	if err != nil {
		return pendingAnswer{}, fmt.Errorf("read the claim: %w", err)
	}
	if claimed {
		answer.Cap = &claim
	}
	answer.Gaps = totalGaps(gaps)
	return answer, nil
}

// onePerDispatch drops the WORKER node of a dispatch that is still open: one
// dispatch is one entry, and its entry is the node the cap prepared. Both ends of
// the link are kind=dispatch in their own scope, so a read that spanned scopes
// without this would report every running dispatch twice — once as the dispatch
// and once as an `unlinked` node that is in fact the work being waited on, which
// is how a state the cap is told to act on becomes noise it learns to ignore.
//
// The test is what the record names, never the goal text. A dispatch node whose
// own scope holds no delivery record, and which no OPEN dispatch names as its
// worker, is kept: either nothing was ever dispatched from it, or the dispatch
// that owned it was closed while the node was left open, and it is waiting on the
// cap in the scope it stands in either way.
func onePerDispatch(dispatches []pendingDispatch) []pendingDispatch {
	covered := make(map[string]bool, len(dispatches))
	for _, d := range dispatches {
		if d.Delivery == nil || d.Delivery.Project == "" || d.Delivery.Node == "" {
			continue
		}
		covered[workerRef(d.Delivery.Project, d.Delivery.Node)] = true
	}

	kept := make([]pendingDispatch, 0, len(dispatches))
	for _, d := range dispatches {
		if covered[workerRef(d.Node.ProjectID, d.Node.NodeID)] {
			continue
		}
		kept = append(kept, d)
	}
	return kept
}

// workerRef names a worker node the way a delivery record does: the scope it
// lives in and its bare id, together, because the same id can stand in two
// scopes.
func workerRef(scope, node string) string { return scope + ":" + node }

// totalGaps sums a per-scope count.
func totalGaps(counts map[string]int) int {
	total := 0
	for _, n := range counts {
		total += n
	}
	return total
}

// workerOf reads one worker node's derived state from the event store, the way
// fs status reports it.
func workerOf(h *command.Handler, project, nodeID string) (command.UnfinishedNode, bool, error) {
	info, found, err := h.NodeIn(project, nodeID)
	if err != nil || !found {
		return command.UnfinishedNode{}, false, err
	}
	return command.UnfinishedNode{
		ProjectID: project, NodeID: info.NodeID, Status: info.Status, Goal: info.Goal,
		Kind: info.Kind, FoundBy: info.FoundBy, ParentNodeID: info.ParentNodeID,
		BlockCheck: info.BlockCheck, Check: info.Check,
	}, true, nil
}
