// One cap, reachable.
//
// cap is a scope name in the store and nothing more, so the scope had no
// identity: any session that read the cap's rules was a cap, two could hold it
// with neither able to tell, a worker could dispatch, and a dead cap read
// exactly like a quiet one. A claim gives the scope an address — the pane herdr
// routes to — and everything here reads that one record: the guard refuses
// coordinating acts from a pane fs holds a delivery record for, a report
// notifies whoever holds the claim, and the cap's own read says whether the
// address is still there.
//
// The store is the channel and herdr is the accelerant. Every write here lands
// first; a notification is attempted only after it, and never fails the write.
package dispatch

import (
	"fmt"

	"github.com/flagship-dev/flagship/internal/command"
)

// The states a claim can be in, as herdr answers for it. CapNone and CapDead
// must not read alike: an unclaimed scope is free to take, and a dead cap is
// work nobody holds.
const (
	// CapNone: nothing has claimed the scope.
	CapNone = "none"
	// CapLive: the registered pane is still in herdr, so a cap holds the scope.
	CapLive = "live"
	// CapDead: herdr no longer has the registered pane. A claim now is a
	// take-over.
	CapDead = "dead"
	// CapUnknown: herdr could not be asked. Not a dead cap — reporting one
	// would invite a take-over of a scope that may still be held.
	CapUnknown = "unknown"
)

// CapStatus is the cap scope's address and whether herdr still has it: what
// fs cap who prints and what fs pending carries, so a cap discovers its own
// supersession and a fresh session sees that coordination is available.
type CapStatus struct {
	PaneID    string `json:"pane_id,omitempty"`
	TabID     string `json:"tab_id,omitempty"`
	Agent     string `json:"agent,omitempty"`
	ClaimedAt string `json:"claimed_at,omitempty"`
	State     string `json:"state"`
}

// CapStatusOf reads the claim and asks herdr whether its address is still
// there. It writes nothing and touches no pane.
func CapStatusOf(h *command.Handler, hc HerdrCLI) (CapStatus, error) {
	claim, claimed, err := h.CapFor(capScope)
	if err != nil {
		return CapStatus{}, err
	}
	if !claimed {
		return CapStatus{State: CapNone}, nil
	}

	status := CapStatus{
		PaneID:    claim.PaneID,
		TabID:     claim.TabID,
		Agent:     claim.Agent,
		ClaimedAt: claim.ClaimedAt,
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
	return status, nil
}

// ClaimRequest is what a session offers to become the cap. PaneID and TabID are
// read from the environment of the session claiming; the agent is not offered —
// herdr is asked which agent sits in the pane, because the address is what fs
// recorded and never what a caller says it is.
type ClaimRequest struct {
	PaneID string
	TabID  string
	// Reconstructed is what the claimant read before taking over a scope whose
	// address is gone. Required for a take-over, refused without it, recorded
	// on the claim.
	Reconstructed string
}

// Claim makes the calling session the cap: refused while a live cap holds the
// scope, taken over when herdr proves the address gone, simply granted when
// nothing has claimed.
//
// A claim is also how a cap that lost its claim recovers: with no claim present
// there is nothing to refuse it. What cannot happen is a claim over a live
// address, because that is two sessions coordinating at once with neither able
// to tell.
func Claim(h *command.Handler, hc HerdrCLI, req ClaimRequest) command.Response {
	if req.PaneID == "" {
		return errResp("cap claim: HERDR_PANE_ID is not set, and the cap is a pane herdr can route to")
	}

	current, claimed, err := h.CapFor(capScope)
	if err != nil {
		return errResp("cap claim: " + err.Error())
	}

	claim := command.CapClaim{PaneID: req.PaneID, TabID: req.TabID}

	if claimed {
		live, err := hc.PaneExists(current.PaneID)
		if err != nil {
			return errResp(fmt.Sprintf(
				"cap claim: herdr could not be asked whether %s is still there (%v); a take-over needs the address proven dead",
				current.PaneID, err))
		}
		if live {
			return errResp(fmt.Sprintf(
				"cap claim: %s is live, claimed at %s; the scope is held — read it with fs pending, or wait for the cap to go",
				current.PaneID, current.ClaimedAt))
		}
		if req.Reconstructed == "" {
			return errResp(fmt.Sprintf(
				"cap claim: %s is gone (last heard %s); taking the scope over requires --reconstructed \"<what you read>\"",
				current.PaneID, current.ClaimedAt))
		}
		claim.TakenOverFrom = current.PaneID
		claim.TakenOverAt = current.ClaimedAt
		claim.Reconstructed = req.Reconstructed
	}

	// Best-effort: the pane is the address and it is already known. A herdr that
	// cannot name the agent leaves the field empty rather than refusing a claim
	// the store could hold — the agent is what a person recognises, not what a
	// reply is routed by.
	if agent, err := hc.AgentForPane(req.PaneID); err == nil {
		claim.Agent = agent
	}

	return h.ClaimCap(capScope, claim)
}

// Guard refuses a coordinating act from a pane fs holds a delivery record for,
// naming the node that pane worked.
//
// It is a discipline guard and not a security boundary, and says so:
// HERDR_PANE_ID is an environment variable any process can unset. It exists so
// a worker cannot accidentally coordinate. Reporting is not a coordinating act
// — a report is addressed to a node and never to a cap — so nothing here
// refuses it.
//
// A pane with no delivery record is unclaimed, not a worker: fs cannot tell a
// session that was never dispatched from one whose record was lost, and it
// refuses on what it knows.
func Guard(h *command.Handler, paneID string) error {
	if paneID == "" {
		return nil
	}

	delivery, capNode, found, err := h.DeliveryForPane(capScope, paneID)
	if err != nil {
		return fmt.Errorf(
			"refused: fs cannot read the delivery records (%v), so it cannot tell whether this pane carries a worker", err)
	}
	if !found {
		return nil
	}

	// The refusal names the route that works, because the act it refuses is the
	// one a worker's own playbook told it to take: worker.yaml instructed the
	// worker to file its finding as a gap node naming the scope it belongs to,
	// and a finding from a dispatched investigation belongs to the cap's scope,
	// which is the write this function refuses. The worker was left holding a
	// finding with no permitted way to place it — the guard is a discipline
	// guard and says so, so the fix is the obligation and not the guard: the
	// worker reports, and the cap files. This text is where the worker learns
	// that, so it names the command rather than the prohibition alone.
	return fmt.Errorf(
		"refused: pane %s holds the delivery record for %s:%s (dispatch %s) — a worker does not coordinate; a finding goes to the cap as a report (fs report %s:%s --question), and the cap files it; or finish the work and let the cap close it",
		paneID, delivery.Project, delivery.Node, capNode, delivery.Project, delivery.Node)
}

// ReportRequest is a worker's report: the node it is about, the kind, and one
// line. It never names a cap — a worker cannot know which session is reading,
// and must not need to.
type ReportRequest struct {
	Project string
	Node    string
	Kind    string
	Line    string
}

// ReportResult is the data payload of fs report: the events it wrote, whether
// the live cap was notified, and why not when it was not.
type ReportResult struct {
	command.UpdateResult
	Notified bool   `json:"notified"`
	Note     string `json:"note,omitempty"`
}

// Report records a worker's report and then, best-effort, pushes it at whoever
// holds the cap. The two halves are one command because the order is a property
// rather than a discipline: an event written after a notification would let a
// cap learn something the store does not hold, and a notification that could be
// skipped would leave a block sitting silently in a store nobody looked at.
//
// The notification is never required. With no cap, with a dead one, or with a
// herdr that refuses, the report is still written and the command still
// succeeds, saying recorded, not notified.
func Report(h *command.Handler, hc HerdrCLI, req ReportRequest) command.Response {
	resp := h.ReportAdd(req.Project, req.Node, req.Kind, req.Line)
	if !resp.OK {
		return resp
	}

	written, _ := resp.Data.(command.UpdateResult)
	notified, note := notify(h, hc, req)
	return command.Response{OK: true, Data: ReportResult{
		UpdateResult: written,
		Notified:     notified,
		Note:         note,
	}}
}

// notify pushes a report at the live cap and says why when it could not. Every
// failure here is a missed promptness, never a lost report: the caller has
// already written it.
func notify(h *command.Handler, hc HerdrCLI, req ReportRequest) (bool, string) {
	if !command.ReportNotifies(req.Kind) {
		return false, "recorded, not notified: a report with no kind does not interrupt the cap"
	}

	status, err := CapStatusOf(h, hc)
	if err != nil {
		return false, fmt.Sprintf("recorded, not notified: the claim could not be read (%v)", err)
	}
	switch status.State {
	case CapNone:
		return false, "recorded, not notified: no cap has claimed the scope"
	case CapDead:
		return false, fmt.Sprintf("recorded, not notified: the cap's pane %s is gone", status.PaneID)
	case CapUnknown:
		return false, fmt.Sprintf("recorded, not notified: herdr could not be asked for the cap's pane %s", status.PaneID)
	}

	// The text is a pointer: the node id and one line. The node is the record
	// and the cap still reads it; this only says which node to read.
	text := fmt.Sprintf("[fs report] %s:%s %s — %s", req.Project, req.Node, req.Kind, req.Line)
	if err := hc.Prompt(status.PaneID, text); err != nil {
		return false, fmt.Sprintf("recorded, not notified: herdr refused the prompt to %s (%v)", status.PaneID, err)
	}
	return true, ""
}

// TellRequest is the cap's reply: the node it answers and the text.
type TellRequest struct {
	Project string
	Node    string
	Text    string
}

// Tell sends the cap's reply to the worker that holds a dispatch, resolved
// through that dispatch's delivery record — the same record the guard reads.
//
// It records nothing: a reply is a message, and what the cap decided lands on
// the node as a decision, where it outlives the delivery. So unlike a report,
// a tell herdr refuses is a failure: there is nothing behind it.
func Tell(h *command.Handler, hc HerdrCLI, req TellRequest) command.Response {
	if req.Text == "" {
		return errResp("fs tell: a reply needs text")
	}
	if _, found := scopeNode(h, req.Project, req.Node); !found {
		return errResp(fmt.Sprintf("fs tell: node %s does not exist in project %s", req.Node, req.Project))
	}

	delivery, capNode, found, err := h.DispatchFor(capScope, req.Project, req.Node)
	if err != nil {
		return errResp("fs tell: " + err.Error())
	}
	if !found {
		return errResp(fmt.Sprintf(
			"fs tell: no delivery record names %s:%s — no dispatch to reply through", req.Project, req.Node))
	}
	if delivery.PaneID == "" {
		return errResp(fmt.Sprintf(
			"fs tell: the delivery record for %s:%s (dispatch %s) names no pane to reply through",
			req.Project, req.Node, capNode))
	}

	if err := hc.Prompt(delivery.PaneID, req.Text); err != nil {
		return errResp(fmt.Sprintf("fs tell: herdr refused the prompt to %s (%v)", delivery.PaneID, err))
	}
	return command.Response{OK: true, Data: TellResult{PaneID: delivery.PaneID, CapNode: capNode}}
}

// TellResult is the data payload of fs tell: where the reply went.
type TellResult struct {
	PaneID  string `json:"pane_id"`
	CapNode string `json:"cap_node"`
}
