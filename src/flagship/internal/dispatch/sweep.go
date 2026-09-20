// Sweep: close the pane the moment the work is done, without waiting on a cap
// turn.
//
// The pane and the node are two different things, and bundling them makes the
// resource wait on the journal: a cap can only close a pane when it has a turn,
// but a worker finishing is a herdr event and a node status change, and neither
// reaches the cap. So the pane gets its own mechanism. Sweep reads every
// dispatch the cap has not closed out, asks the same question fs pending asks —
// is the worker's node done? — and closes the pane of every worker that is.
//
// It never marks a cap node done. The node is the record, and only the cap
// closes it, after it has read it. It is not a gate either: nothing here blocks
// a dispatch or a close. And it starts nothing, so it is not a second
// dispatcher. Running it twice is the same as running it once: the decision it
// records on the node is what a second sweep finds, and skips.
package dispatch

import (
	"fmt"
	"slices"
	"strings"

	"github.com/flagship-dev/flagship/internal/command"
)

// SweepClosed is one pane the sweep closed, with the worker whose done node
// justified it.
type SweepClosed struct {
	CapNode    string `json:"cap_node"`
	PaneID     string `json:"pane_id"`
	WorkerNode string `json:"worker_node"`
}

// SweepKept is one pane the sweep left alone because its worker is not done.
// State is the classification fs pending reports for it.
type SweepKept struct {
	CapNode string `json:"cap_node"`
	PaneID  string `json:"pane_id,omitempty"`
	State   string `json:"state"`
}

// SweepResult is the data payload of fs sweep: what it closed, and what it left.
type SweepResult struct {
	Closed  []SweepClosed `json:"closed"`
	Kept    []SweepKept   `json:"kept"`
	Warning string        `json:"warning,omitempty"`
}

// Sweep closes the pane of every dispatch in the cap's scope whose worker node
// is done, and leaves every other one alone. It appends one decision per pane it
// closed and nothing else, and marks no node done.
//
// The worker's node decides, so herdr is asked once, only for the detail the
// kept states carry — never for whether the work is done. A done worker's pane
// is closed even when herdr's agent view cannot be read.
func Sweep(h *command.Handler, hc HerdrCLI) command.Response {
	unresolved, err := unresolvedDispatches(h)
	if err != nil {
		return errResp(fmt.Sprintf("sweep: %v", err))
	}

	agents, herdrErr := hc.Agents()

	result := SweepResult{Closed: []SweepClosed{}, Kept: []SweepKept{}}
	var warnings []string
	for _, node := range unresolved {
		delivery, recorded, err := h.DeliveryFor(capScope, node.NodeID)
		if err != nil {
			return errResp(fmt.Sprintf("sweep: read delivery record for %s: %v", node.NodeID, err))
		}

		// The same classification fs pending reports, so the two commands never
		// disagree about a dispatch the cap is looking at.
		state := pendingState(h, delivery, recorded, agents, herdrErr)
		if state != StateReady {
			// kept is the panes left because the worker is not done. A dispatch
			// whose record names no pane — one that was prepared and never
			// delivered — left no resource, so it is not reported here.
			if delivery.PaneID != "" {
				result.Kept = append(result.Kept, SweepKept{
					CapNode: node.NodeID, PaneID: delivery.PaneID, State: state,
				})
			}
			continue
		}
		if delivery.PaneID == "" {
			// A done worker whose record names no pane: there is no resource to
			// close, and nothing a sweep could do about it.
			continue
		}

		workerRef := delivery.Project + ":" + delivery.Node
		decision := sweepDecision(delivery.PaneID, workerRef)
		if alreadySwept(h, node.NodeID, decision) {
			// A previous sweep closed this pane. The node stays open until the
			// cap reads it, so the decision is the only trace to go on.
			continue
		}
		if warning := tearDownPane(hc, delivery.PaneID); warning != "" {
			// The pane is still there, so this is not handled: record nothing,
			// and let the next sweep try again. A pane already gone returns no
			// warning — that is the goal state.
			warnings = append(warnings, warning)
			continue
		}
		if err := recordDecision(h, node.NodeID, decision); err != nil {
			return errResp("sweep: " + err.Error())
		}
		result.Closed = append(result.Closed, SweepClosed{
			CapNode: node.NodeID, PaneID: delivery.PaneID, WorkerNode: workerRef,
		})
	}

	if herdrErr != nil {
		warnings = append(warnings, herdrWarning(herdrErr))
	}
	result.Warning = strings.Join(warnings, "; ")
	return command.Response{OK: true, Data: result}
}

// sweepDecision is the one decision a sweep records per pane it closed. It names
// the pane and the worker node the close rested on, so the cap can see why a
// pane it never touched is gone — and a second sweep can tell it has already
// been here.
func sweepDecision(paneID, workerRef string) string {
	return fmt.Sprintf("pane %s closed by sweep (worker node %s is done)", paneID, workerRef)
}

// alreadySwept reports whether this close is already on the cap's node. The node
// is never marked done by a sweep, so it is still there to ask.
func alreadySwept(h *command.Handler, capNodeID, decision string) bool {
	node, err := capNode(h, capNodeID)
	if err != nil {
		return false
	}
	return slices.Contains(node.Decisions, decision)
}
