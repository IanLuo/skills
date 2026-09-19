// Package dispatch owns the cap's dispatch lifecycle for a task type: prepare
// the brief, deliver it to a worker, and close it out.
//
// Prepare reads the task-type's prerequisite playbook, runs the mechanical
// prerequisites against the target project's root, records the cap's own node,
// and returns the brief. Deliver (fs dispatch --deliver) is the only step that
// spawns: it splits a pane, starts the agent, sends the brief, and records the
// pane binding structurally. Close (fs close) is the close-out gate: it refuses
// to close a dispatch whose worker node is not done, then closes the pane from
// the recorded binding.
//
// It also gates preparation: a failing prerequisite check or an earlier dispatch
// that was never delivered stops it, unless the cap passes --confirm.
package dispatch

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/knowledge"
	"github.com/flagship-dev/flagship/internal/registry"
)

// capScope is the project_id of the cap's own event table. It is implicit: a
// scope is just a string, so it is never registered and never created.
const capScope = "cap"

// Item is one gate step in the brief's checklist: a check the dispatcher ran,
// or an ask only the cap can confirm. Body is the shell command for a check.
type Item struct {
	Kind   string `json:"kind"`   // check | ask
	Body   string `json:"body"`   // the command to run, or the question
	Status string `json:"status"` // pass | fail for a check, ? for an ask
	Output string `json:"output,omitempty"`
}

// Brief is the data payload of fs dispatch.
type Brief struct {
	Goal        string   `json:"goal"`
	Project     string   `json:"project"`
	RootPath    string   `json:"root_path"`
	TaskType    string   `json:"task_type"`
	Playbook    string   `json:"playbook"`
	Checklist   []Item   `json:"checklist"`
	Context     []string `json:"context,omitempty"`
	LockedDocs  []string `json:"locked_docs"`
	Notes       []string `json:"notes"`
	CapNodeID   string   `json:"cap_node_id"`
	NextCommand string   `json:"next_command"`

	// Delivery is the pane binding, set only when fs dispatch --deliver handed
	// the brief to a worker. It is recorded as a delivery-recorded event; this
	// field is the same binding echoed back to the caller.
	Delivery *command.DeliveryRecord `json:"delivery,omitempty"`
}

// Prepare composes the brief for a task type and records the cap's node. It
// returns the CLI response envelope, so failures are reported the same way as
// every other command's.
//
// It refuses in two cases rather than preparing work in a bad state: an earlier
// dispatch is still unresolved, or a prerequisite check failed. confirm overrides
// both refusals and records the override as a decision on the cap's node.
func Prepare(h *command.Handler, reg *registry.Registry, kc *knowledge.Center, project, taskType, goal string, confirm bool) command.Response {
	if project == "" {
		return errResp("--project is required")
	}
	if taskType == "" {
		return errResp("--type is required")
	}
	if goal == "" {
		return errResp("--goal is required")
	}

	playbookName := taskType + "-prerequisites"
	pb, err := kc.Get(playbookName)
	if err != nil {
		return errResp(fmt.Sprintf(
			"dispatch: prerequisite playbook %s.yaml is missing or unreadable (%v); tell the user and stop — never improvise a procedure for a worker",
			playbookName, err))
	}

	proj, err := reg.Get(project)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}

	// Gate: a dispatch the cap prepared but never delivered is still open work.
	// Refuse to pile another one on top of it unless the cap says to proceed.
	unresolved, err := unresolvedDispatches(h)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}
	if len(unresolved) > 0 && !confirm {
		return errResp(unresolvedError(unresolved))
	}

	brief, err := buildBrief(pb, proj, taskType, goal, confirm)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}

	nodeID, err := recordCapNode(h, taskType, goal, project)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}
	brief.CapNodeID = nodeID
	brief.NextCommand = nextCommand(brief)

	if confirm {
		if err := recordOverrides(h, nodeID, brief, unresolved); err != nil {
			return errResp(fmt.Sprintf("dispatch: %v", err))
		}
	}

	return command.Response{OK: true, Data: brief}
}

// isDispatchGoal reports whether a goal was written by fs dispatch, which always
// labels its node "dispatch <type>: <goal>". The cap's own backlog is not
// dispatch work, so the gate must not treat it as such.
func isDispatchGoal(goal string) bool {
	rest, ok := strings.CutPrefix(goal, "dispatch ")
	if !ok {
		return false
	}
	typ, _, ok := strings.Cut(rest, ": ")
	return ok && typ != ""
}

// unresolvedDispatches returns the cap's dispatch nodes that are not done.
func unresolvedDispatches(h *command.Handler) ([]command.UnfinishedNode, error) {
	nodes, err := h.UnfinishedIn(capScope)
	if err != nil {
		return nil, err
	}

	var unresolved []command.UnfinishedNode
	for _, node := range nodes {
		if isDispatchGoal(node.Goal) {
			unresolved = append(unresolved, node)
		}
	}
	return unresolved, nil
}

// unresolvedError names each unresolved dispatch and says how to clear it.
func unresolvedError(unresolved []command.UnfinishedNode) string {
	named := make([]string, len(unresolved))
	for i, node := range unresolved {
		named[i] = fmt.Sprintf("%s (%q)", node.NodeID, node.Goal)
	}
	return fmt.Sprintf(
		"dispatch: unresolved dispatches in scope %s: %s; resolve them (fs task update ID --status done --decision ...) or re-run with --confirm to proceed",
		capScope, strings.Join(named, ", "))
}

// buildBrief runs the typed prerequisites against the project root: every check
// step is executed there, every ask becomes a "?" for the cap, and every say is
// carried as worker context rather than a gate item.
//
// Checks run in order and stop at the first failure unless confirm is set. The
// user fixes one thing at a time; a list of failures buries the decision.
func buildBrief(pb *knowledge.Playbook, proj registry.Project, taskType, goal string, confirm bool) (*Brief, error) {
	info, err := os.Stat(proj.RootPath)
	if err != nil {
		return nil, fmt.Errorf("project %q root %s is unreadable: %w", proj.Name, proj.RootPath, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("project %q root %s is not a directory", proj.Name, proj.RootPath)
	}

	lockedDocs := findLockedDocs(proj.RootPath)

	checklist := make([]Item, 0, len(pb.Steps))
	var context []string
	for _, step := range pb.Steps {
		switch step.Kind {
		case knowledge.KindCheck:
			item := runCheck(step.Body, proj.RootPath)
			checklist = append(checklist, item)
			if item.Status == "fail" && !confirm {
				return nil, fmt.Errorf(
					"prerequisite check failed: %q; output: %s; tell the user and fix it, or re-run with --confirm to proceed",
					item.Body, checkOutput(item))
			}
		case knowledge.KindAsk:
			checklist = append(checklist, Item{Kind: step.Kind, Body: step.Body, Status: "?"})
		default: // say — worker context, not part of the gate
			context = append(context, step.Body)
		}
	}

	return &Brief{
		Goal:       goal,
		Project:    proj.Name,
		RootPath:   proj.RootPath,
		TaskType:   taskType,
		Playbook:   pb.Name,
		Checklist:  checklist,
		Context:    context,
		LockedDocs: lockedDocs,
		Notes: []string{
			"the locked-doc grep matches the marker text anywhere, so it has a known false positive when a repo documents the marker syntax itself",
		},
	}, nil
}

// runCheck executes a check's body as a shell command with cwd = the project
// root, and reports pass/fail plus the command's combined output.
func runCheck(body, root string) Item {
	cmd := exec.Command("sh", "-c", body)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return Item{
		Kind:   knowledge.KindCheck,
		Body:   body,
		Status: passFail(err == nil),
		Output: strings.TrimRight(string(out), "\n"),
	}
}

func passFail(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

// checkOutput is the failing command's output, made explicit when it produced
// none so the refusal always says what happened.
func checkOutput(item Item) string {
	if strings.TrimSpace(item.Output) == "" {
		return "(no output)"
	}
	return item.Output
}

// findLockedDocs returns the *.md files under root carrying a specs:locked or
// design:locked marker, relative to root (as grep -rl would print them).
func findLockedDocs(root string) []string {
	var docs []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable entry does not abort the sweep
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if !bytes.Contains(data, []byte("<!-- specs:locked:")) &&
			!bytes.Contains(data, []byte("<!-- design:locked:")) {
			return nil
		}
		if rel, err := filepath.Rel(root, path); err == nil {
			path = rel
		}
		docs = append(docs, path)
		return nil
	})
	sort.Strings(docs)
	return docs
}

// recordCapNode writes the cap's two events for this dispatch: the node itself
// and the decision naming the project the work went to. It returns the node id
// so the cap can read its own dispatch back.
func recordCapNode(h *command.Handler, taskType, goal, project string) (string, error) {
	added := h.TaskAdd(capScope, fmt.Sprintf("dispatch %s: %s", taskType, goal), nil)
	if !added.OK {
		return "", fmt.Errorf("record cap node: %s", added.Error)
	}
	data, ok := added.Data.(command.EventData)
	if !ok || data.NodeID == "" {
		return "", fmt.Errorf("record cap node: task-created response carried no node id")
	}

	summary := fmt.Sprintf("dispatch %s: %s to project %s", taskType, goal, project)
	if err := recordDecision(h, data.NodeID, summary); err != nil {
		return "", err
	}
	return data.NodeID, nil
}

// recordOverrides records the cap's --confirm overrides on its own dispatch node:
// one decision per failing check, plus one naming the dispatches that were
// already unresolved. Without them the refusal leaves no trace in the log.
func recordOverrides(h *command.Handler, nodeID string, brief *Brief, unresolved []command.UnfinishedNode) error {
	for _, item := range brief.Checklist {
		if item.Status != "fail" {
			continue
		}
		summary := "user confirmed proceeding past a failing check: " + item.Body
		if err := recordDecision(h, nodeID, summary); err != nil {
			return err
		}
	}

	if len(unresolved) > 0 {
		ids := make([]string, len(unresolved))
		for i, node := range unresolved {
			ids[i] = node.NodeID
		}
		summary := "user confirmed proceeding with unresolved dispatches: " + strings.Join(ids, ", ")
		if err := recordDecision(h, nodeID, summary); err != nil {
			return err
		}
	}
	return nil
}

func recordDecision(h *command.Handler, nodeID, summary string) error {
	resp := h.TaskUpdate(capScope, nodeID, "", summary, nil)
	if !resp.OK {
		return fmt.Errorf("record cap decision: %s", resp.Error)
	}
	return nil
}

// nextCommand is the herdr line the cap runs to deliver the brief to a worker.
// dispatch does not run it.
func nextCommand(b *Brief) string {
	name := "dispatch-" + b.TaskType
	return fmt.Sprintf(
		`P=$(herdr pane split --current --direction right --cwd %s --no-focus | jq -r '.result.pane.pane_id') && `+
			`herdr agent start %s --kind pi --pane "$P" && `+
			`herdr agent prompt %s %s --wait --timeout 120000`,
		shellQuote(b.RootPath), name, name, shellQuote(renderBrief(b)))
}

// renderBrief is the text the cap hands the worker: goal, project root, the say
// steps as context, the gate checklist with each check's output, and the locked
// docs.
func renderBrief(b *Brief) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Goal: %s\n", b.Goal)
	fmt.Fprintf(&sb, "Project: %s (root %s)\n", b.Project, b.RootPath)
	if len(b.Context) > 0 {
		sb.WriteString("Context:\n")
		for _, say := range b.Context {
			fmt.Fprintf(&sb, "- %s\n", say)
		}
	}
	sb.WriteString("Checklist:\n")
	for _, item := range b.Checklist {
		fmt.Fprintf(&sb, "- [%s] %s\n", item.Status, item.Body)
		if item.Output != "" {
			sb.WriteString("    output:\n")
			for _, line := range strings.Split(item.Output, "\n") {
				fmt.Fprintf(&sb, "      %s\n", line)
			}
		}
	}
	if len(b.LockedDocs) > 0 {
		fmt.Fprintf(&sb, "Locked docs: %s\n", strings.Join(b.LockedDocs, ", "))
	} else {
		sb.WriteString("Locked docs: none\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// shellQuote wraps s in single quotes for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func errResp(msg string) command.Response {
	return command.Response{OK: false, Error: msg}
}
