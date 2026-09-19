// Delivery: handing a prepared brief to a worker and recording the binding.
//
// herdr is the declared dispatcher (engine_scope: herdr in the playbook
// schema), so shelling out to its CLI is a deliberate coupling, not an
// accident. The HerdrCLI interface exists only so the gates can be exercised
// without a live terminal.
package dispatch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/flagship-dev/flagship/internal/command"
)

// agentKind is the agent the dispatcher launches in the worker pane. It matches
// the herdr line the brief carries as next_command.
const agentKind = "pi"

// herdrEngine names the dispatcher in the delivery record.
const herdrEngine = "herdr"

// ErrPaneGone reports that a pane no longer exists. Closing an already-closed
// pane is a success, not a failure: the dispatch is simply already torn down.
var ErrPaneGone = errors.New("pane is already gone")

// HerdrCLI is the slice of the herdr CLI the dispatch lifecycle uses: split a
// sibling pane, start an agent in it, send the brief, close it again.
type HerdrCLI interface {
	// SplitPane creates a sibling pane with cwd and returns its pane id.
	SplitPane(cwd string) (string, error)
	// StartAgent starts an interactive agent named name in an existing pane.
	StartAgent(paneID, name string) error
	// Prompt submits text to the agent hosted by paneID.
	Prompt(paneID, text string) error
	// ClosePane closes paneID, returning ErrPaneGone if it no longer exists.
	ClosePane(paneID string) error
}

// Deliver hands the prepared brief to a worker: it splits a sibling pane at the
// project root, starts the agent, creates the worker's node in the target
// project, sends the brief naming that node, then records the binding as a
// delivery-recorded event and marks the cap node active.
//
// The worker's node is created before the brief is sent because the brief has to
// name it — the worker must not invent its own. That ordering is the only one
// that works, so a failure after the node exists rolls it back (see
// rollbackWorker): a failed delivery neither leaves outstanding work behind nor
// claims a delivery that did not happen.
func Deliver(h *command.Handler, hc HerdrCLI, brief *Brief) command.Response {
	agent := "dispatch-" + brief.TaskType

	paneID, err := hc.SplitPane(brief.RootPath)
	if err != nil {
		return deliverErr(brief, fmt.Errorf("split pane: %w", err))
	}

	if err := hc.StartAgent(paneID, agent); err != nil {
		abandonPane(hc, paneID)
		return deliverErr(brief, fmt.Errorf("start agent %s: %w", agent, err))
	}

	workerID, err := createWorkerNode(h, brief)
	if err != nil {
		abandonPane(hc, paneID)
		return deliverErr(brief, err)
	}

	if err := hc.Prompt(paneID, renderBrief(brief)); err != nil {
		abandonPane(hc, paneID)
		rollbackWorker(h, brief.Project, workerID, fmt.Sprintf("send brief: %v", err))
		return deliverErr(brief, fmt.Errorf("send brief: %w", err))
	}

	delivery := command.DeliveryRecord{
		PaneID:  paneID,
		Agent:   agent,
		Engine:  herdrEngine,
		Project: brief.Project,
		Node:    workerID,
	}
	if resp := h.RecordDelivery(capScope, brief.CapNodeID, delivery); !resp.OK {
		abandonPane(hc, paneID)
		rollbackWorker(h, brief.Project, workerID, fmt.Sprintf("record delivery: %s", resp.Error))
		return deliverErr(brief, fmt.Errorf("record delivery: %s", resp.Error))
	}
	if err := recordDecision(h, brief.CapNodeID, fmt.Sprintf("delivered to pane %s, agent %s", paneID, agent)); err != nil {
		return deliverErr(brief, err)
	}
	if resp := h.TaskUpdate(capScope, brief.CapNodeID, "active", "", nil); !resp.OK {
		return deliverErr(brief, fmt.Errorf("mark node active: %s", resp.Error))
	}

	brief.Delivery = &delivery
	return command.Response{OK: true, Data: brief}
}

// createWorkerNode creates the worker's node in the target project and points
// the brief at it, so the brief the worker receives names the node it owns
// rather than leaving it to invent one.
func createWorkerNode(h *command.Handler, brief *Brief) (string, error) {
	added := h.TaskAdd(brief.Project, dispatchGoal(brief.TaskType, brief.Goal), nil)
	if !added.OK {
		return "", fmt.Errorf("create worker node in %s: %s", brief.Project, added.Error)
	}
	data, ok := added.Data.(command.EventData)
	if !ok || data.NodeID == "" {
		return "", fmt.Errorf("create worker node in %s: task-created response carried no node id", brief.Project)
	}
	brief.WorkerNode = brief.Project + ":" + data.NodeID
	return data.NodeID, nil
}

// rollbackWorker resolves a worker node whose delivery did not complete. The
// store is append-only, so the node cannot be deleted; marking it done with the
// reason is the rollback — nothing is left outstanding, and the log says why the
// node exists. Best-effort: the caller is already reporting a failure.
func rollbackWorker(h *command.Handler, project, workerID, reason string) {
	_ = h.TaskUpdate(project, workerID, "done", "dispatch never delivered: "+reason, nil)
}

// deliverErr reports a delivery that did not happen, naming the cap node it
// left unresolved so the cap can find and close it instead of guessing.
func deliverErr(brief *Brief, err error) command.Response {
	return errResp(fmt.Sprintf(
		"deliver: %v; cap node %s is unresolved — fix it and re-dispatch, or close it with fs close --abandoned",
		err, brief.CapNodeID))
}

// abandonPane best-effort closes a pane whose dispatch is not being recorded.
func abandonPane(hc HerdrCLI, paneID string) {
	_ = hc.ClosePane(paneID)
}

// herdrCLI shells out to the herdr binary on PATH.
type herdrCLI struct{}

// NewHerdrCLI returns the herdr CLI adapter used by the fs commands.
func NewHerdrCLI() HerdrCLI { return herdrCLI{} }

func (herdrCLI) SplitPane(cwd string) (string, error) {
	out, err := runHerdr("pane", "split", "--current", "--direction", "right", "--cwd", cwd, "--no-focus")
	if err != nil {
		return "", err
	}

	var resp struct {
		Result struct {
			Pane struct {
				PaneID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", fmt.Errorf("herdr pane split: parse response: %w", err)
	}
	if resp.Result.Pane.PaneID == "" {
		return "", errors.New("herdr pane split: response carried no pane id")
	}
	return resp.Result.Pane.PaneID, nil
}

func (herdrCLI) StartAgent(paneID, name string) error {
	_, err := runHerdr("agent", "start", name, "--kind", agentKind, "--pane", paneID)
	return err
}

func (herdrCLI) Prompt(paneID, text string) error {
	_, err := runHerdr("agent", "prompt", paneID, text)
	return err
}

func (herdrCLI) ClosePane(paneID string) error {
	_, err := runHerdr("pane", "close", paneID)
	if herdrErrorCode(err) == "pane_not_found" {
		return ErrPaneGone
	}
	return err
}

// HerdrError is a failure the herdr CLI reported with a machine-readable code
// in its JSON error envelope.
type HerdrError struct {
	Code    string
	Message string
}

func (e *HerdrError) Error() string {
	if e.Message == "" {
		return "herdr: " + e.Code
	}
	return fmt.Sprintf("herdr %s: %s", e.Code, e.Message)
}

// runHerdr runs the herdr CLI and returns its stdout. herdr reports failures as
// JSON on stderr ({"error":{"code":...}}) and still exits non-zero, so the code
// is recovered from the output rather than from the exit status alone. Stdout
// is checked too: the envelope's destination is not contractual.
func runHerdr(args ...string) (string, error) {
	cmd := exec.Command("herdr", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if e := parseHerdrError(stderr.Bytes()); e != nil {
			return "", e
		}
		if e := parseHerdrError(stdout.Bytes()); e != nil {
			return "", e
		}
		return "", fmt.Errorf("herdr %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// herdrErrorCode returns the error code herdr reported, or "" for a failure
// that never reached herdr (a missing binary, a dead server).
func herdrErrorCode(err error) string {
	var e *HerdrError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func parseHerdrError(out []byte) error {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(out, &envelope) != nil || envelope.Error.Code == "" {
		return nil
	}
	return &HerdrError{Code: envelope.Error.Code, Message: envelope.Error.Message}
}
