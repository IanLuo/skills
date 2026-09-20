// Package dispatch owns the cap's dispatch lifecycle for a task type: prepare
// the brief, deliver it to a worker, and close it out.
//
// Prepare reads the task-type's prerequisite playbook, runs the mechanical
// prerequisites against the target project's root, records the cap's own node,
// and returns the brief. Every check step sees the dispatch's own inputs as
// FS_* (see checkEnv), so a gate can ask about this dispatch — which cards —
// and not only about the world. Deliver (fs dispatch --deliver) is the only
// step that spawns: it splits a sibling pane of the cap's own, starts the agent
// in it, sends the brief, and records the pane binding structurally. Close
// (fs close) is the close-out gate: it refuses to close a dispatch whose worker
// node is not done, then closes the pane from the recorded binding.
//
// It also gates preparation: a failing prerequisite check or an earlier dispatch
// that was never delivered stops it. Each gate has its own override — --confirm
// for a failing check, --allow-unresolved for an unresolved dispatch — so saying
// yes to one can never wave the other through.
package dispatch

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/knowledge"
	"github.com/flagship-dev/flagship/internal/query"
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

	// WorkerNode is the node created for the worker in the target project, as
	// "<project>:<node>". Deliver sets it before the brief is sent, because the
	// brief has to name the node the worker owns. Without --deliver no such node
	// exists and it stays empty.
	WorkerNode string `json:"worker_node,omitempty"`

	// Worktree is the herdr worktree workspace this dispatch runs in, when it
	// runs in one. Deliver records it so close-out can remove it: a worktree
	// must not be able to outlive a closed node. Empty for a dispatch that runs
	// in the project root.
	Worktree string `json:"worktree,omitempty"`

	// Delivery is the pane binding, set only when fs dispatch --deliver handed
	// the brief to a worker. It is recorded as a delivery-recorded event; this
	// field is the same binding echoed back to the caller.
	Delivery *command.DeliveryRecord `json:"delivery,omitempty"`
}

// Prepare composes the brief for a task type and records the cap's node. It
// returns the CLI response envelope, so failures are reported the same way as
// every other command's.
//
// integrates is the member's cap node an integration merges, or empty for a
// dispatch that integrates nothing. A non-empty value must name a cap-scope
// dispatch node: the link is recorded on this dispatch's own task-created
// payload, so it exists from creation and never depends on parsing a goal.
//
// It refuses in two cases rather than preparing work in a bad state: an earlier
// dispatch is still unresolved, or a prerequisite check failed. The two gates
// have one override each: allowUnresolved for the unresolved dispatch, confirm
// for the failing check. Neither overrides the other — a cap answering "go ahead
// with the other dispatch open" has not seen, and so cannot have accepted, a
// failing prerequisite the other flag would have short-circuited.
func Prepare(h *command.Handler, reg *registry.Registry, kc *knowledge.Center, project, taskType, goal string, cards []string, integrates string, confirm, allowUnresolved bool) command.Response {
	if project == "" {
		return errResp("--project is required")
	}
	if taskType == "" {
		return errResp("--type is required")
	}
	if goal == "" {
		return errResp("--goal is required")
	}

	member, err := integrateMember(h, integrates)
	if err != nil {
		return errResp("dispatch: " + err.Error())
	}

	playbookName := knowledge.PrerequisiteName(taskType)
	pb, err := kc.Get(playbookName)
	if err != nil {
		return errResp(fmt.Sprintf(
			"dispatch: prerequisite playbook %s.yaml is missing or unreadable (%v); tell the user and stop — never improvise a procedure for a worker",
			playbookName, err))
	}
	if err := triggerError("prerequisite", playbookName, pb, taskType); err != nil {
		return errResp("dispatch: " + err.Error())
	}

	proj, err := reg.Get(project)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}

	// Gate: a dispatch the cap prepared but never delivered is still open work.
	// Refuse to pile another one on top of it unless the cap says to proceed.
	// --allow-unresolved is this gate's only override: --confirm is about a
	// failing check and must not pass this one.
	//
	// The member this integration names is expected to be unresolved: a member's
	// dispatch is closed only after the integration it waits on is done, so it is
	// not the other open work this gate protects — it is what this dispatch is
	// about. Every other open dispatch still refuses.
	unresolved, err := unresolvedDispatches(h)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}
	unresolved = withoutNode(unresolved, member)
	if len(unresolved) > 0 && !allowUnresolved {
		return errResp(unresolvedError(unresolved))
	}

	brief, err := buildBrief(pb, proj, taskType, goal, cards, integrates, confirm)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}

	nodeID, err := recordCapNode(h, taskType, goal, project, member)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}
	brief.CapNodeID = nodeID
	brief.NextCommand = nextCommand(brief)

	// Each override is recorded by the flag that granted it, on the node it
	// granted it for. A refusal above leaves no node and no override: nothing
	// proceeded, so there is nothing to record.
	if confirm {
		if err := recordCheckOverrides(h, nodeID, brief); err != nil {
			return errResp(fmt.Sprintf("dispatch: %v", err))
		}
	}
	if allowUnresolved && len(unresolved) > 0 {
		if err := recordUnresolvedOverride(h, nodeID, unresolved); err != nil {
			return errResp(fmt.Sprintf("dispatch: %v", err))
		}
	}

	return command.Response{OK: true, Data: brief}
}

// triggerError refuses a playbook whose trigger contradicts the task type its
// name selects it for. An empty trigger is not a contradiction: the name is
// what selects a playbook, and the shipped playbooks predate the field.
//
// Entry and exit are held to the same rule, because both select by the same
// kind of name. --confirm never overrides it: a contradiction is a
// configuration mistake, not a failed gate.
func triggerError(kind, name string, pb *knowledge.Playbook, taskType string) error {
	if pb.TriggerAgrees(taskType) {
		return nil
	}
	return fmt.Errorf(
		"%s playbook %s.yaml carries trigger %q, which does not match the task type %q its name selects it for; set trigger to %q, or leave it empty — an empty trigger is valid and the shipped playbooks rely on it",
		kind, name, pb.Trigger, taskType, taskType)
}

// integrateMember resolves a --integrates value to the bare id of the cap-scope
// dispatch node it names, or to "" when the flag was not given.
//
// It refuses a node that does not exist, and one that is not a dispatch node,
// naming it: the link is written into the integration's task-created payload, and
// events are immutable, so a wrong link could never be corrected. "Is this a
// dispatch node" is the same question the dispatch gates ask, so it is asked the
// same way — from the node's structural kind, never from its goal text.
func integrateMember(h *command.Handler, integrates string) (string, error) {
	if integrates == "" {
		return "", nil
	}

	member, err := bareNodeID(integrates)
	if err != nil {
		return "", err
	}
	node, err := capNode(h, member)
	if err != nil {
		return "", fmt.Errorf("--integrates %s: %v", integrates, err)
	}
	if node.Kind != query.KindDispatch {
		return "", fmt.Errorf(
			"--integrates %s names a %q node, not a %s node — an integration integrates a member's dispatch node",
			integrates, node.Kind, query.KindDispatch)
	}
	return member, nil
}

// bareNodeID strips an optional "<scope>:" qualifier from a node reference. A
// node id is "t-<hex>" — a colon never appears in one — so a qualified id is
// unambiguously qualified. Only capScope is accepted: an integration's member
// lives in the cap's own scope.
func bareNodeID(ref string) (string, error) {
	if ref == "" {
		return "", errors.New("a member cap node is required")
	}
	scope, node, qualified := strings.Cut(ref, ":")
	if qualified {
		if scope != capScope {
			return "", fmt.Errorf("%s names scope %s; a member lives in scope %s", ref, scope, capScope)
		}
		if node == "" {
			return "", fmt.Errorf("%s names no node", ref)
		}
		return node, nil
	}
	return ref, nil
}

// Integrated answers the member's question: is there a done integration
// dispatch in the cap scope that names this member? It returns that dispatch's
// node id.
//
// It is the member's exit gate. An outcome — "HEAD is an ancestor of main" — is
// satisfied by a hand merge, so it cannot tell a dispatched integration from a
// bypass; a done integration node naming this member can. A pending one does
// not count: the member is not integrated until the integration's own gate has
// passed and its node is done.
func Integrated(h *command.Handler, member string) command.Response {
	ref, err := bareNodeID(member)
	if err != nil {
		return errResp("integrated: " + err.Error())
	}

	// cap has no registered root, and nothing here needs one: the read is of the
	// cap's own scope.
	resp := h.StatusIn(capScope, "")
	if !resp.OK {
		return errResp("integrated: " + resp.Error)
	}
	result, ok := resp.Data.(command.StatusResult)
	if !ok {
		return errResp("integrated: read scope " + capScope + ": no status payload")
	}

	for _, task := range result.Tasks {
		if task.Integrates == ref && task.Kind == query.KindDispatch && task.Status == "done" {
			return command.Response{OK: true, Data: IntegratedResult{Member: ref, IntegratedBy: task.NodeID}}
		}
	}
	return errResp(fmt.Sprintf(
		"integrated: no done integration dispatch in scope %s names member %s; merge it with a dispatched integration (fs dispatch --type integrate --integrates %s), never by hand",
		capScope, ref, ref))
}

// IntegratedResult is the data payload of fs integrated: which member was asked
// about, and the done integration dispatch that names it.
type IntegratedResult struct {
	Member       string `json:"member"`
	IntegratedBy string `json:"integrated_by"`
}

// withoutNode drops one node id from a list. It is how the member an
// integration names is kept out of the unresolved-dispatch gate it would
// otherwise fail: that gate is about other open work.
func withoutNode(nodes []command.UnfinishedNode, nodeID string) []command.UnfinishedNode {
	if nodeID == "" {
		return nodes
	}
	kept := make([]command.UnfinishedNode, 0, len(nodes))
	for _, node := range nodes {
		if node.NodeID != nodeID {
			kept = append(kept, node)
		}
	}
	return kept
}

// dispatchGoal is the goal text fs dispatch writes — on the cap's node, and on
// the worker's node in the target project. Both ends of the link carry the same
// text, so the two nodes read as one piece of work. It is prose for a reader:
// what makes a node a dispatch is its kind, never this text.
func dispatchGoal(taskType, goal string) string {
	return fmt.Sprintf("dispatch %s: %s", taskType, goal)
}

// unresolvedDispatches returns the cap's dispatch nodes that are not done. A
// node is a dispatch because it was written as one, so a dispatch whose goal
// was later edited is still recognised — the failure the goal prefix had.
func unresolvedDispatches(h *command.Handler) ([]command.UnfinishedNode, error) {
	nodes, err := h.UnfinishedIn(capScope)
	if err != nil {
		return nil, err
	}

	var unresolved []command.UnfinishedNode
	for _, node := range nodes {
		if node.Kind == query.KindDispatch {
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
		"dispatch: unresolved dispatches in scope %s: %s; resolve them (fs task update ID --status done --decision ...) or re-run with --allow-unresolved to proceed",
		capScope, strings.Join(named, ", "))
}

// buildBrief runs the typed prerequisites against the project root: every check
// step is executed there, every ask becomes a "?" for the cap, and every say is
// carried as worker context rather than a gate item.
//
// Checks run in order and stop at the first failure unless confirm is set. The
// user fixes one thing at a time; a list of failures buries the decision.
//
// Each check runs with env, so a prerequisite over the batch's own inputs —
// which cards, which type, which member — is a mechanical gate rather than a
// judgement call.
func buildBrief(pb *knowledge.Playbook, proj registry.Project, taskType, goal string, cards []string, integrates string, confirm bool) (*Brief, error) {
	info, err := os.Stat(proj.RootPath)
	if err != nil {
		return nil, fmt.Errorf("project %q root %s is unreadable: %w", proj.Name, proj.RootPath, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("project %q root %s is not a directory", proj.Name, proj.RootPath)
	}

	lockedDocs := findLockedDocs(proj.RootPath)
	env := checkEnv(proj.Name, taskType, goal, cards, integrates)

	checklist := make([]Item, 0, len(pb.Steps))
	var context []string
	for _, step := range pb.Steps {
		switch step.Kind {
		case knowledge.KindCheck:
			item := runCheck(step.Body, proj.RootPath, env)
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

// checkEnv is the dispatch's own inputs, exposed to every check step as FS_*.
//
// It exists because a prerequisite is otherwise a static shell string: it could
// ask about the world (is this a git repo?) but never about *this* dispatch
// (which cards? which member does it integrate?). A gate over the batch's cards
// cannot be written without it.
//
// FS_CARDS is the --cards value, comma-separated, and FS_INTEGRATES is the
// --integrates value. Both are exported empty rather than left absent when they
// were not given, so a check can tell "this dispatch named no cards/member" from
// "this step has no env" — and one that needs them must refuse on empty rather
// than pass vacuously.
func checkEnv(project, taskType, goal string, cards []string, integrates string) []string {
	return []string{
		"FS_PROJECT=" + project,
		"FS_TYPE=" + taskType,
		"FS_GOAL=" + goal,
		"FS_CARDS=" + strings.Join(cards, ","),
		"FS_INTEGRATES=" + integrates,
	}
}

// runCheck runs a check's body through the shared shell runner and reports
// pass/fail plus the command's combined output.
func runCheck(body, root string, env []string) Item {
	out, err := command.RunShell(body, root, env)
	return Item{
		Kind:   knowledge.KindCheck,
		Body:   body,
		Status: passFail(err == nil),
		Output: strings.TrimRight(out, "\n"),
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

// recordCapNode writes the cap's two events for this dispatch: the node itself —
// carrying the member it integrates, when it integrates one — and the decision
// naming the project the work went to. It returns the node id so the cap can
// read its own dispatch back.
func recordCapNode(h *command.Handler, taskType, goal, project, integrates string) (string, error) {
	added := h.TaskAddKind(capScope, dispatchGoal(taskType, goal), query.KindDispatch, "", integrates, nil)
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

// recordCheckOverrides records the cap's --confirm override: one decision per
// failing check, naming the check and the output it failed with. Without it the
// refusal leaves no trace in the log, and a bare "user confirmed" would not say
// what was overridden or the value it was over.
func recordCheckOverrides(h *command.Handler, nodeID string, brief *Brief) error {
	for _, item := range brief.Checklist {
		if item.Status != "fail" {
			continue
		}
		summary := fmt.Sprintf(
			"--confirm overrode the failing check %q (output: %s)", item.Body, checkOutput(item))
		if err := recordDecision(h, nodeID, summary); err != nil {
			return err
		}
	}
	return nil
}

// recordUnresolvedOverride records the cap's --allow-unresolved override, naming
// the dispatches it proceeded past — the value the flag was over.
func recordUnresolvedOverride(h *command.Handler, nodeID string, unresolved []command.UnfinishedNode) error {
	named := make([]string, len(unresolved))
	for i, node := range unresolved {
		named[i] = node.NodeID
	}
	summary := "--allow-unresolved proceeded with unresolved dispatches: " + strings.Join(named, ", ")
	return recordDecision(h, nodeID, summary)
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
//
// It splits a sibling pane of the cap's own rather than creating a tab: the
// split is anchored to the cap's pane, and therefore to the cap's workspace, so
// there is no workspace to get wrong. It is the same line Deliver runs.
func nextCommand(b *Brief) string {
	name := "dispatch-" + b.TaskType
	return fmt.Sprintf(
		`P=$(herdr pane split --current --direction right --no-focus --cwd %s | jq -r '.result.pane.pane_id') && `+
			`herdr agent start %s --kind pi --pane "$P" && `+
			`herdr agent prompt %s %s --wait --timeout 120000`,
		shellQuote(b.RootPath), name, name, shellQuote(renderBrief(b)))
}

// renderBrief is the text the cap hands the worker: goal, project root, the
// node the worker owns, the say steps as context, the gate checklist with each
// check's output, and the locked docs. It also carries the standing worker
// obligation, so every worker is told how a gap it notices outlives it.
func renderBrief(b *Brief) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Goal: %s\n", b.Goal)
	fmt.Fprintf(&sb, "Project: %s (root %s)\n", b.Project, b.RootPath)
	if b.WorkerNode != "" {
		fmt.Fprintf(&sb, "Note your work on %s — that node already exists and is yours; do not create another.\n", b.WorkerNode)
	}
	// The standing worker obligation. A gap a worker notices must outlive it,
	// and the only channel that survives is a node: the final message is not
	// read and closing destroys the transcript.
	foundBy := b.WorkerNode
	if foundBy == "" {
		foundBy = "<your node, PROJECT:NODE>"
	}
	fmt.Fprintf(&sb, "Gaps: a gap you flag and do not fix must become a node — "+
		"`fs task add --kind gap --found-by %s --goal \"...\"` — because your final message is not read and closing destroys your transcript.\n", foundBy)
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
