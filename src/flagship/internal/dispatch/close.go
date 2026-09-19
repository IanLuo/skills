// Close: the close-out gate for a dispatch.
//
// The gate exists because a cap that trusts its own in-context testing never
// reads the worker's node. It can check that the worker's node exists and is
// done, and that a verdict was recorded. It cannot check that the cap read that
// node. Do not pretend otherwise: the gate makes the omission visible and
// expensive, it does not make it impossible.
package dispatch

import (
	"errors"
	"fmt"
	"strings"

	"github.com/flagship-dev/flagship/internal/command"
)

// CloseRequest is the cap's close-out of one dispatch.
type CloseRequest struct {
	NodeID    string // the cap's dispatch node
	Worker    string // "<project>:<node>" — the worker's node, which must be done
	Decision  string // the cap's verdict, recorded on the cap node
	Abandoned bool   // the dispatch was never delivered
	Reason    string // why it was never delivered; required with Abandoned
}

// CloseResult is the close-out's data payload.
type CloseResult struct {
	NodeID   string `json:"node_id"`
	Decision string `json:"decision"`
	PaneID   string `json:"pane_id,omitempty"`
	Warning  string `json:"warning,omitempty"`
}

// Close closes out a dispatch node. It refuses — naming what is missing — when
// the node is not a dispatch, when no delivery was recorded (or, with
// --abandoned, no reason was given), when the worker's node is missing or not
// done, or when no verdict was given. On success it closes the pane from the
// delivery record and marks the cap node done with the verdict.
//
// --abandoned skips the delivery and worker gates and records
// "abandoned, never delivered: <reason>" instead of a verdict.
func Close(h *command.Handler, hc HerdrCLI, req CloseRequest) command.Response {
	if req.NodeID == "" {
		return errResp("--node is required: fs close --node <cap node> ...")
	}

	node, err := capNode(h, req.NodeID)
	if err != nil {
		return errResp(err.Error())
	}
	if !isDispatchGoal(node.Goal) {
		return errResp(fmt.Sprintf(
			"close: %s is not a dispatch node — its goal %q is not a dispatch goal (want the prefix \"dispatch TYPE: \")",
			req.NodeID, node.Goal))
	}

	decision := req.Decision
	var delivery command.DeliveryRecord

	if req.Abandoned {
		if req.Reason == "" {
			return errResp("--abandoned requires --reason: say why the dispatch was never delivered")
		}
		decision = "abandoned, never delivered: " + req.Reason
	} else {
		d, ok, err := h.DeliveryFor(capScope, req.NodeID)
		if err != nil {
			return errResp(fmt.Sprintf("close: read delivery record: %v", err))
		}
		if !ok {
			return errResp(fmt.Sprintf(
				"no delivery recorded for %s — was this dispatched, or prepared and abandoned? close it with --abandoned --reason \"<why>\" if it was never delivered",
				req.NodeID))
		}
		delivery = d

		if req.Worker == "" {
			return errResp("--worker is required: name the worker's node as PROJECT:NODE")
		}
		project, workerID, ok := strings.Cut(req.Worker, ":")
		if !ok || project == "" || workerID == "" {
			return errResp(fmt.Sprintf("--worker %q must be PROJECT:NODE", req.Worker))
		}
		worker, found := scopeNode(h, project, workerID)
		if !found {
			return errResp(fmt.Sprintf("worker %s not found in project %s", req.Worker, project))
		}
		if worker.Status != "done" {
			return errResp(fmt.Sprintf(
				"worker %s is %s — read its node and let it finish, or close with --abandoned",
				req.Worker, worker.Status))
		}
		if req.Decision == "" {
			return errResp("--decision is required: record the verdict this close-out rests on")
		}
	}

	result := CloseResult{NodeID: req.NodeID, Decision: decision, PaneID: delivery.PaneID}
	if !req.Abandoned {
		switch err := hc.ClosePane(delivery.PaneID); {
		case err == nil, errors.Is(err, ErrPaneGone):
			// Already gone is the goal state, not a failure.
		default:
			result.Warning = fmt.Sprintf("could not close pane %s: %v — close it by hand", delivery.PaneID, err)
		}
	}

	if resp := h.TaskUpdate(capScope, req.NodeID, "done", decision, nil); !resp.OK {
		return errResp("mark node done: " + resp.Error)
	}
	return command.Response{OK: true, Data: result}
}

// capNode returns the cap-scope node, or an error when no such node exists.
func capNode(h *command.Handler, nodeID string) (command.TaskInfo, error) {
	node, ok := scopeNode(h, capScope, nodeID)
	if !ok {
		return command.TaskInfo{}, fmt.Errorf("close: no node %s in scope %s", nodeID, capScope)
	}
	return node, nil
}

// scopeNode finds one node in a scope via derived state, the same way fs status
// reports it. A scope with no events simply has no such node.
func scopeNode(h *command.Handler, scope, nodeID string) (command.TaskInfo, bool) {
	resp := h.Status(scope)
	if !resp.OK {
		return command.TaskInfo{}, false
	}
	result, ok := resp.Data.(command.StatusResult)
	if !ok {
		return command.TaskInfo{}, false
	}
	for _, task := range result.Tasks {
		if task.NodeID == nodeID {
			return task, true
		}
	}
	return command.TaskInfo{}, false
}
