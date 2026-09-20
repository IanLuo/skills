// Package command validates input, produces typed events, and manages the
// response envelope. It is the gatekeeper between CLI and Event Store.
// (SYSTEM-DESIGN R1, ARCHITECTURE R4)
package command

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/store"
)

// Valid task statuses (SYSTEM-DESIGN R2).
var validStatuses = map[string]bool{
	"pending": true,
	"active":  true,
	"done":    true,
	"blocked": true,
}

// Response is the structured JSON envelope returned by every command.
// {ok, error?, data?} + exit code (ARCHITECTURE R4).
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}

// EventData is the data payload for single-event responses.
type EventData struct {
	EventID   string          `json:"event_id"`
	EventType store.EventType `json:"event_type"`
	NodeID    string          `json:"node_id,omitempty"`
	CommitSHA *string         `json:"commit_sha,omitempty"`
}

// UpdateResult is the data payload when a command produces multiple events.
type UpdateResult struct {
	Events []EventData `json:"events"`
}

// DeliveryRecord is the payload of a delivery-recorded event: the structural
// binding of a cap dispatch node to the pane, the tab that pane sits in, and the
// agent carrying its brief, and to the node created for the worker in the target
// project.
//
// Project and Node are what let close-out name the worker without the cap
// guessing. They are absent only on records written before the link existed;
// TabID likewise, on records written before it was recorded at all — it names
// the cap's own tab, which the worker shares and which is never closed;
// Type, Worktree, and WorktreePath, on records written before the cleanup gate
// and worktree teardown existed. Type is what close-out reads to find the exit
// gate, and it is structural — never recovered from the goal string, which is
// prose.
//
// Worktree names the herdr workspace; WorktreePath names the checkout it was
// made from. Both are recorded because they can die separately: herdr can
// forget a workspace — its agent having ended — while the checkout survives on
// disk, and then the workspace id alone names nothing that can be removed.
type DeliveryRecord struct {
	PaneID       string `json:"pane_id"`
	TabID        string `json:"tab_id,omitempty"`
	Agent        string `json:"agent"`
	Engine       string `json:"engine"`
	Project      string `json:"project"`
	Node         string `json:"node"`
	Type         string `json:"type,omitempty"`
	Worktree     string `json:"worktree,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
}

// TaskInfo represents derived task state for status output.
type TaskInfo struct {
	NodeID       string     `json:"node_id"`
	Goal         string     `json:"goal"`
	Kind         query.Kind `json:"kind"`
	FoundBy      string     `json:"found_by,omitempty"`
	Status       string     `json:"status"`
	ParentNodeID string     `json:"parent_node_id,omitempty"`
	Orphan       bool       `json:"orphan,omitempty"`
	Decisions    []string   `json:"decisions,omitempty"`
	Knowledge    []string   `json:"knowledge,omitempty"`
	// BlockCheck is the check the node's last block recorded, when one did. fs
	// status re-runs it the same way fs pending and fs unfinished do, so all
	// three readers report a block whose condition is over as such.
	BlockCheck string `json:"block_check,omitempty"`
	// Integrates names the cap-scope dispatch node an integration dispatch
	// merges. It is the structural link, read from the task-created payload.
	Integrates string `json:"integrates,omitempty"`
}

// StatusResult is the data payload for fs status.
type StatusResult struct {
	ProjectID string     `json:"project_id"`
	Tasks     []TaskInfo `json:"tasks"`
}

// UnfinishedNode is one task that is not done, tagged with the scope it lives
// in and its structural kind, so fs unfinished can group every scope's work.
type UnfinishedNode struct {
	ProjectID string     `json:"project_id"`
	NodeID    string     `json:"node_id"`
	Status    string     `json:"status"`
	Goal      string     `json:"goal"`
	Kind      query.Kind `json:"kind"`
	FoundBy   string     `json:"found_by,omitempty"`
	// BlockCheck is the command the node's last block recorded, when one did:
	// what fs pending and fs unfinished re-run to tell a live block from one
	// whose condition is over. Empty for a block with no check.
	BlockCheck string `json:"block_check,omitempty"`
}

// GapNode is one kind=gap node: something noticed and not dispatched, with
// found_by naming the node that noticed it when one was recorded.
type GapNode struct {
	ProjectID string `json:"project_id"`
	NodeID    string `json:"node_id"`
	Status    string `json:"status"`
	Goal      string `json:"goal"`
	FoundBy   string `json:"found_by,omitempty"`
}

// Handler executes commands against a store.
type Handler struct {
	store     *store.Store
	commitSHA *string
	logger    *slog.Logger
}

// NewHandler opens a store at path and returns a handler.
func NewHandler(dbPath string) (*Handler, error) {
	s, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	return &Handler{store: s, logger: slog.Default()}, nil
}

// SetLogger sets the structured logger for this handler.
func (h *Handler) SetLogger(l *slog.Logger) {
	h.logger = l
}

// Close releases the underlying store.
func (h *Handler) Close() error {
	return h.store.Close()
}

// SetCommitSHA sets the git HEAD sha to embed in events (ARCHITECTURE R4).
func (h *Handler) SetCommitSHA(sha *string) {
	h.commitSHA = sha
}

// ProjectCreate appends a project-created event. AC1.
func (h *Handler) ProjectCreate(name, rootPath string) Response {
	if name == "" {
		return errResp("project name is required")
	}
	if rootPath == "" {
		return errResp("project root path is required")
	}

	payload, _ := json.Marshal(map[string]string{
		"name":      name,
		"root_path": rootPath,
	})

	id, err := h.store.Append(store.Event{
		Type:      store.ProjectCreated,
		ProjectID: name,
		CommitSHA: h.commitSHA,
		Payload:   payload,
	})
	if err != nil {
		h.logger.Error("store append failed", "command", "project-create", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "project-create", "project", name, "event_id", id)
	return okResp(EventData{
		EventID:   id,
		EventType: store.ProjectCreated,
		CommitSHA: h.commitSHA,
	})
}

// TaskAdd appends a task-created event of the default kind, work. AC2.
func (h *Handler) TaskAdd(projectID, goal string, parentNodeID *string) Response {
	return h.TaskAddKind(projectID, goal, query.KindWork, "", "", parentNodeID)
}

// TaskAddKind appends a task-created event carrying its structural kind — and,
// for a gap, the node that noticed it; for an integration, the member it
// integrates. It is the write side of the kind the dispatch gates and fs gaps
// read, and of the integrates link the member's exit gate reads.
func (h *Handler) TaskAddKind(projectID, goal string, kind query.Kind, foundBy, integrates string, parentNodeID *string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if goal == "" {
		return errResp("goal is required")
	}
	if !query.ValidKind(kind) {
		return errResp(fmt.Sprintf("invalid kind: %q (valid: work, dispatch, gap)", kind))
	}
	if foundBy != "" {
		if err := validNodeRef(foundBy); err != nil {
			return errResp(err.Error())
		}
	}
	if integrates != "" {
		if err := validIntegrates(integrates); err != nil {
			return errResp(err.Error())
		}
	}

	// A parent that is not in the project would leave the node unreachable in
	// the tree: not a root, never a child, invisible to fs status — and events
	// are immutable, so the mistake could never be corrected. Refuse it here.
	// Existence is a task-created event, not tree membership: derived state
	// materializes a node from any event written to it, so a conjured node (see
	// nodeExists) would otherwise be an acceptable parent.
	if parentNodeID != nil && *parentNodeID != "" {
		exists, err := h.nodeExists(projectID, *parentNodeID)
		if err != nil {
			h.logger.Error("store replay failed", "command", "task-add", "error", err)
			return errResp(err.Error())
		}
		if !exists {
			return errResp(fmt.Sprintf("parent %s does not exist in project %s", *parentNodeID, projectID))
		}
	}

	nodeID := newNodeID()
	payload := map[string]string{"goal": goal, "kind": string(kind)}
	if foundBy != "" {
		payload["found_by"] = foundBy
	}
	if integrates != "" {
		payload["integrates"] = integrates
	}
	data, _ := json.Marshal(payload)

	id, err := h.store.Append(store.Event{
		Type:         store.TaskCreated,
		ProjectID:    projectID,
		NodeID:       &nodeID,
		ParentNodeID: parentNodeID,
		CommitSHA:    h.commitSHA,
		Payload:      data,
	})
	if err != nil {
		h.logger.Error("store append failed", "command", "task-add", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "task-add", "project", projectID, "node_id", nodeID, "kind", kind, "event_id", id)
	return okResp(EventData{
		EventID:   id,
		EventType: store.TaskCreated,
		NodeID:    nodeID,
		CommitSHA: h.commitSHA,
	})
}

// validNodeRef refuses a found_by that is not "<project>:<node>". It is the only
// thing naming what a gap was found by, so a typo must fail at write time.
func validNodeRef(ref string) error {
	project, node, ok := strings.Cut(ref, ":")
	if !ok || project == "" || node == "" || strings.Contains(node, ":") {
		return fmt.Errorf("found_by must be \"<project>:<node>\", got %q", ref)
	}
	return nil
}

// validIntegrates refuses an integrates link that is not a bare node id. The
// link is the one structural statement of which member an integration merges,
// and it names a node in the integration's own scope — so a qualified or
// malformed value must fail at write time, where it can still be corrected.
func validIntegrates(ref string) error {
	if strings.ContainsAny(ref, ": \t") {
		return fmt.Errorf("integrates must be a bare node id (e.g. t-1a2b3c4d), got %q", ref)
	}
	return nil
}

// nodeExists reports whether a node was created in the project. The test is a
// task-created event, not tree membership: derived state materializes a node
// from any event that names it, so a node that only ever had a status or
// decision written to it — the damage the old path left behind — is visible in
// fs status while never having been created. A node whose task-created parent is
// missing is an orphan, which is a node that exists.
func (h *Handler) nodeExists(projectID, nodeID string) (bool, error) {
	filter := &store.ReplayFilter{
		Types:  []store.EventType{store.TaskCreated},
		NodeID: &nodeID,
	}
	events, err := h.store.Replay(projectID, filter)
	if err != nil {
		return false, err
	}
	return len(events) > 0, nil
}

// resolveWriteNode resolves the node id a write names and confirms it exists in
// projectID. A writer that appends an event for a node that was never created
// conjures a work item with no task-created event: a status and decisions for
// something that does not exist. Refusing here, before any append, is what makes
// "a write lands on the node it names, or fails" true.
//
// A "<project>:<node>" id is qualified, never literal: a colon never appears in
// a node id (they are "t-<hex>"), so reading one as qualified is safe. The
// qualifier must name projectID — a different project is an accidental
// cross-scope write, refused naming both.
func (h *Handler) resolveWriteNode(projectID, nodeID string) (string, error) {
	if prefix, rest, ok := strings.Cut(nodeID, ":"); ok {
		if prefix != projectID {
			return "", fmt.Errorf("id %q names project %s but the write targets project %s", nodeID, prefix, projectID)
		}
		if rest == "" {
			return "", fmt.Errorf("id %q names no node", nodeID)
		}
		nodeID = rest
	}

	exists, err := h.nodeExists(projectID, nodeID)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("node %s does not exist in project %s", nodeID, projectID)
	}
	return nodeID, nil
}

// TaskUpdate appends status-changed and optionally decision-recorded events. AC4.
func (h *Handler) TaskUpdate(projectID, nodeID, status, decision string, commitSHA *string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}

	nodeID, err := h.resolveWriteNode(projectID, nodeID)
	if err != nil {
		return errResp(err.Error())
	}

	sha := h.commitSHA
	if commitSHA != nil {
		sha = commitSHA
	}

	var events []EventData

	if status != "" {
		if !validStatuses[status] {
			return errResp(fmt.Sprintf("invalid status: %s (valid: pending, active, done, blocked)", status))
		}

		// Derive current status for the from field.
		from := h.deriveStatus(projectID, nodeID)

		payload, _ := json.Marshal(map[string]string{"from": from, "to": status})
		id, err := h.store.Append(store.Event{
			Type:      store.StatusChanged,
			ProjectID: projectID,
			NodeID:    &nodeID,
			CommitSHA: sha,
			Payload:   payload,
		})
		if err != nil {
			return errResp(err.Error())
		}
		events = append(events, EventData{
			EventID:   id,
			EventType: store.StatusChanged,
			NodeID:    nodeID,
			CommitSHA: sha,
		})
	}

	if decision != "" {
		payload, _ := json.Marshal(map[string]string{"summary": decision})
		id, err := h.store.Append(store.Event{
			Type:      store.DecisionRecorded,
			ProjectID: projectID,
			NodeID:    &nodeID,
			CommitSHA: sha,
			Payload:   payload,
		})
		if err != nil {
			return errResp(err.Error())
		}
		events = append(events, EventData{
			EventID:   id,
			EventType: store.DecisionRecorded,
			NodeID:    nodeID,
			CommitSHA: sha,
		})
	}

	if len(events) == 0 {
		return errResp("nothing to update: provide --status and/or --decision")
	}

	h.logger.Info("command executed", "command", "task-update", "project", projectID, "node_id", nodeID, "events", len(events))
	return okResp(UpdateResult{Events: events})
}

// RecordDelivery appends a delivery-recorded event on a cap dispatch node,
// binding it to the pane and agent that carry its brief and to the node created
// for the worker in the target project. Recorded by the dispatcher at delivery
// time, so close-out reads the binding rather than whatever a decision happened
// to say.
func (h *Handler) RecordDelivery(projectID, nodeID string, d DeliveryRecord) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}
	if d.PaneID == "" {
		return errResp("delivery pane_id is required")
	}
	if d.Project == "" {
		return errResp("delivery project is required: it names the worker node's scope")
	}
	if d.Node == "" {
		return errResp("delivery node is required: it names the node the worker was given")
	}

	nodeID, err := h.resolveWriteNode(projectID, nodeID)
	if err != nil {
		return errResp(err.Error())
	}

	payload, _ := json.Marshal(d)
	id, err := h.store.Append(store.Event{
		Type:      store.DeliveryRecorded,
		ProjectID: projectID,
		NodeID:    &nodeID,
		CommitSHA: h.commitSHA,
		Payload:   payload,
	})
	if err != nil {
		h.logger.Error("store append failed", "command", "record-delivery", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "record-delivery", "project", projectID, "node_id", nodeID, "pane_id", d.PaneID, "event_id", id)
	return okResp(EventData{
		EventID:   id,
		EventType: store.DeliveryRecorded,
		NodeID:    nodeID,
		CommitSHA: h.commitSHA,
	})
}

// DeliveryFor returns the pane binding recorded for a node, and ok=false when
// the node has no delivery-recorded event. It reads the event payload, never
// the prose decision recorded beside it.
func (h *Handler) DeliveryFor(projectID, nodeID string) (DeliveryRecord, bool, error) {
	filter := &store.ReplayFilter{
		Types:  []store.EventType{store.DeliveryRecorded},
		NodeID: &nodeID,
	}
	events, err := h.store.Replay(projectID, filter)
	if err != nil {
		return DeliveryRecord{}, false, err
	}
	if len(events) == 0 {
		return DeliveryRecord{}, false, nil
	}

	var d DeliveryRecord
	if err := json.Unmarshal(events[len(events)-1].Payload, &d); err != nil {
		return DeliveryRecord{}, false, fmt.Errorf("delivery-recorded payload: %w", err)
	}
	return d, true, nil
}

// TaskEdit appends a metadata-changed event for each field it is given: the
// goal, the kind, or both. AC11.
func (h *Handler) TaskEdit(projectID, nodeID, newGoal string, newKind query.Kind, commitSHA *string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}

	nodeID, err := h.resolveWriteNode(projectID, nodeID)
	if err != nil {
		return errResp(err.Error())
	}

	sha := h.commitSHA
	if commitSHA != nil {
		sha = commitSHA
	}

	var events []EventData

	if newGoal != "" {
		data, err := h.appendMetadata(projectID, nodeID, "goal", h.deriveGoal(projectID, nodeID), newGoal, sha)
		if err != nil {
			h.logger.Error("store append failed", "command", "task-edit", "error", err)
			return errResp(err.Error())
		}
		events = append(events, data)
	}

	// An empty kind means "leave it alone": the CLI cannot distinguish an
	// absent --kind from an empty one, and neither should be stored.
	if newKind != "" {
		if !query.ValidKind(newKind) {
			return errResp(fmt.Sprintf("invalid kind: %q (valid: work, dispatch, gap)", newKind))
		}
		data, err := h.appendMetadata(projectID, nodeID, "kind", string(h.deriveKind(projectID, nodeID)), string(newKind), sha)
		if err != nil {
			h.logger.Error("store append failed", "command", "task-edit", "error", err)
			return errResp(err.Error())
		}
		events = append(events, data)
	}

	if len(events) == 0 {
		return errResp("nothing to edit: provide --goal and/or --kind")
	}

	h.logger.Info("command executed", "command", "task-edit", "project", projectID, "node_id", nodeID, "events", len(events))
	return okResp(UpdateResult{Events: events})
}

// appendMetadata appends one metadata-changed event and returns its envelope
// entry. A kind change is a new event, never a rewrite.
func (h *Handler) appendMetadata(projectID, nodeID, field, oldValue, newValue string, sha *string) (EventData, error) {
	payload, _ := json.Marshal(map[string]string{
		"field":     field,
		"old_value": oldValue,
		"new_value": newValue,
	})
	id, err := h.store.Append(store.Event{
		Type:      store.MetadataChanged,
		ProjectID: projectID,
		NodeID:    &nodeID,
		CommitSHA: sha,
		Payload:   payload,
	})
	if err != nil {
		return EventData{}, err
	}
	return EventData{EventID: id, EventType: store.MetadataChanged, NodeID: nodeID, CommitSHA: sha}, nil
}

// Status derives current state for all tasks in a project. AC3.
// Delegates tree-building to the query engine. It runs a blocked node's
// recorded check in this process's own directory; StatusIn names the project
// root the check belongs in.
func (h *Handler) Status(projectID string) Response {
	return h.StatusIn(projectID, "")
}

// StatusIn is Status with the directory a blocked node's recorded check runs in:
// the project root the node belongs to. The check is about that project, not
// about wherever the terminal happens to be, and re-verifying it here is what
// makes fs status agree with fs pending and fs unfinished — a block whose
// condition is over must not read as current truth in one of the three readers
// only. Nothing is unblocked: only the reported status changes.
func (h *Handler) StatusIn(projectID, blockDir string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}

	events, err := h.store.Replay(projectID, nil)
	if err != nil {
		h.logger.Error("store replay failed", "command", "status", "error", err)
		return errResp(err.Error())
	}

	tree := query.BuildTree(projectID, events)

	result := StatusResult{ProjectID: projectID}
	for _, root := range tree.Roots {
		collectTasks(root, &result.Tasks)
	}
	for i := range result.Tasks {
		task := &result.Tasks[i]
		task.Status = blockStatus(task.Status, task.BlockCheck, blockDir)
	}

	return okResp(result)
}

// collectTasks flattens the tree into the TaskInfo slice (pre-order).
func collectTasks(node *query.Node, out *[]TaskInfo) {
	*out = append(*out, TaskInfo{
		NodeID:       node.NodeID,
		Goal:         node.Goal,
		Kind:         node.Kind,
		FoundBy:      node.FoundBy,
		Status:       node.Status,
		ParentNodeID: node.ParentID,
		Orphan:       node.Orphan,
		Decisions:    node.Decisions,
		Knowledge:    node.Knowledge,
		BlockCheck:   node.BlockCheck,
		Integrates:   node.Integrates,
	})
	for _, child := range node.Children {
		collectTasks(child, out)
	}
}

// Unfinished lists every node that is not done, across every scope in the
// store, grouped by structural kind so the backlog and the dispatch status are
// visible without reading goals. Scopes come from the events themselves, not
// the registry: a node in a scope that was never registered, or whose registry
// row was lost, still reports here.
//
// rootOf resolves a scope to the directory a recorded block check runs in — the
// project root — so a block whose condition is over reads as such instead of as
// current truth. A nil rootOf, or a scope it cannot resolve, runs the check in
// this process's own directory. Nothing is unblocked: the node's record still
// says blocked, and only the report notes that the condition no longer holds.
func (h *Handler) Unfinished(rootOf func(scope string) string) Response {
	scopes, err := h.store.Scopes()
	if err != nil {
		h.logger.Error("store scopes failed", "command", "unfinished", "error", err)
		return errResp(err.Error())
	}

	// Every kind is present even when empty, so a reader sees the groups it
	// should have looked in rather than inferring them from what happens to be
	// there.
	grouped := map[string][]UnfinishedNode{
		string(query.KindWork):     {},
		string(query.KindDispatch): {},
		string(query.KindGap):      {},
	}
	total := 0
	for _, scope := range scopes {
		nodes, err := h.UnfinishedIn(scope)
		if err != nil {
			h.logger.Error("store replay failed", "command", "unfinished", "scope", scope, "error", err)
			return errResp(err.Error())
		}
		refreshBlockChecks(nodes, rootOf)
		for _, node := range nodes {
			grouped[string(node.Kind)] = append(grouped[string(node.Kind)], node)
			total++
		}
	}

	h.logger.Info("command executed", "command", "unfinished", "scopes", len(scopes), "unfinished", total)
	return okResp(map[string]any{"unfinished": grouped})
}

// Gaps lists every kind=gap node across every scope, open ones first, so a cap
// reads its backlog in one place instead of remembering it. A gap that has been
// dispatched or resolved is done and sorts after the open ones; it is not
// dropped, so the record of what was noticed survives.
func (h *Handler) Gaps() Response {
	scopes, err := h.store.Scopes()
	if err != nil {
		h.logger.Error("store scopes failed", "command", "gaps", "error", err)
		return errResp(err.Error())
	}

	gaps := []GapNode{}
	for _, scope := range scopes {
		nodes, err := h.nodesIn(scope)
		if err != nil {
			h.logger.Error("store replay failed", "command", "gaps", "scope", scope, "error", err)
			return errResp(err.Error())
		}
		for _, node := range nodes {
			if node.Kind != query.KindGap {
				continue
			}
			gaps = append(gaps, GapNode{
				ProjectID: scope,
				NodeID:    node.NodeID,
				Status:    node.Status,
				Goal:      node.Goal,
				FoundBy:   node.FoundBy,
			})
		}
	}

	// Open first, then scope, then node id: deterministic, and the things a cap
	// still has to act on are at the top.
	sort.SliceStable(gaps, func(i, j int) bool {
		iOpen, jOpen := gaps[i].Status != "done", gaps[j].Status != "done"
		if iOpen != jOpen {
			return iOpen
		}
		if gaps[i].ProjectID != gaps[j].ProjectID {
			return gaps[i].ProjectID < gaps[j].ProjectID
		}
		return gaps[i].NodeID < gaps[j].NodeID
	})

	h.logger.Info("command executed", "command", "gaps", "scopes", len(scopes), "gaps", len(gaps))
	return okResp(map[string]any{"gaps": gaps})
}

// OpenGaps counts the kind=gap nodes that are not done, across every scope.
// A gap is open until it is dispatched or resolved — done is the only way it
// stops counting — so this is the backlog nobody has picked up. fs pending
// reports it, so the backlog arrives with the turn's first read.
func (h *Handler) OpenGaps() (int, error) {
	scopes, err := h.store.Scopes()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, scope := range scopes {
		nodes, err := h.UnfinishedIn(scope)
		if err != nil {
			return 0, err
		}
		for _, node := range nodes {
			if node.Kind == query.KindGap {
				count++
			}
		}
	}
	return count, nil
}

// UnfinishedIn lists the nodes in one scope that are not done, ordered by node
// id. Status is derived by replaying the scope's events, the same way fs status
// derives it.
func (h *Handler) UnfinishedIn(scope string) ([]UnfinishedNode, error) {
	nodes, err := h.nodesIn(scope)
	if err != nil {
		return nil, err
	}

	unfinished := make([]UnfinishedNode, 0, len(nodes))
	for _, node := range nodes {
		if node.Status == "done" {
			continue
		}
		unfinished = append(unfinished, UnfinishedNode{
			ProjectID:  scope,
			NodeID:     node.NodeID,
			Status:     node.Status,
			Goal:       node.Goal,
			Kind:       node.Kind,
			FoundBy:    node.FoundBy,
			BlockCheck: node.BlockCheck,
		})
	}
	return unfinished, nil
}

// nodesIn derives every node in one scope, ordered by node id. It is the walk
// fs status, fs unfinished, and fs gaps share.
func (h *Handler) nodesIn(scope string) ([]*query.Node, error) {
	events, err := h.store.Replay(scope, nil)
	if err != nil {
		return nil, err
	}

	tree := query.BuildTree(scope, events)
	nodes := make([]*query.Node, 0, len(tree.Nodes))
	for _, node := range tree.Nodes {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	return nodes, nil
}

// deriveStatus replays events to find the current status of a node.
func (h *Handler) deriveStatus(projectID, nodeID string) string {
	filter := &store.ReplayFilter{
		Types:  []store.EventType{store.StatusChanged},
		NodeID: &nodeID,
	}
	events, err := h.store.Replay(projectID, filter)
	if err != nil || len(events) == 0 {
		return "pending"
	}
	var p struct{ To string }
	json.Unmarshal(events[len(events)-1].Payload, &p)
	if p.To == "" {
		return "pending"
	}
	return p.To
}

// deriveKind replays events to find the current kind of a node, using the same
// derivation fs status reports. A node that does not exist reads as the default.
func (h *Handler) deriveKind(projectID, nodeID string) query.Kind {
	events, err := h.store.Replay(projectID, nil)
	if err != nil {
		return query.KindWork
	}
	if node, ok := query.BuildTree(projectID, events).Nodes[nodeID]; ok {
		return node.Kind
	}
	return query.KindWork
}

// deriveGoal replays events to find the current goal of a node.
func (h *Handler) deriveGoal(projectID, nodeID string) string {
	// First check for metadata-changed events.
	filter := &store.ReplayFilter{
		Types:  []store.EventType{store.MetadataChanged},
		NodeID: &nodeID,
	}
	events, _ := h.store.Replay(projectID, filter)
	for i := len(events) - 1; i >= 0; i-- {
		var p struct {
			Field    string `json:"field"`
			NewValue string `json:"new_value"`
		}
		json.Unmarshal(events[i].Payload, &p)
		if p.Field == "goal" {
			return p.NewValue
		}
	}
	// Fall back to task-created.
	createFilter := &store.ReplayFilter{
		Types:  []store.EventType{store.TaskCreated},
		NodeID: &nodeID,
	}
	created, _ := h.store.Replay(projectID, createFilter)
	if len(created) > 0 {
		var p struct{ Goal string }
		json.Unmarshal(created[0].Payload, &p)
		return p.Goal
	}
	return ""
}

// TaskBlock appends a task-blocked event and a status-changed event atomically.
// It records no check: the reason stands as prose. TaskBlockWithCheck is the
// same block with a condition a reader can re-run.
func (h *Handler) TaskBlock(projectID, nodeID, reason string) Response {
	return h.TaskBlockWithCheck(projectID, nodeID, reason, "")
}

// TaskBlockWithCheck appends a task-blocked event and a status-changed event
// atomically, recording the optional check: the shell command that would show
// the blocking condition is over. Recording it is what lets fs pending and fs
// unfinished re-verify the block instead of repeating stale prose.
func (h *Handler) TaskBlockWithCheck(projectID, nodeID, reason, check string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}
	if reason == "" {
		return errResp("--reason is required")
	}

	nodeID, err := h.resolveWriteNode(projectID, nodeID)
	if err != nil {
		return errResp(err.Error())
	}

	from := h.deriveStatus(projectID, nodeID)

	blockedPayload, _ := json.Marshal(map[string]string{"reason": reason, "check": check})
	statusPayload, _ := json.Marshal(map[string]string{"from": from, "to": "blocked"})

	batch := []store.Event{
		{
			Type:      store.TaskBlocked,
			ProjectID: projectID,
			NodeID:    &nodeID,
			CommitSHA: h.commitSHA,
			Payload:   blockedPayload,
		},
		{
			Type:      store.StatusChanged,
			ProjectID: projectID,
			NodeID:    &nodeID,
			CommitSHA: h.commitSHA,
			Payload:   statusPayload,
		},
	}

	ids, err := h.store.AppendBatch(batch)
	if err != nil {
		h.logger.Error("store append failed", "command", "task-block", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "task-block", "project", projectID, "node_id", nodeID)
	events := []EventData{
		{EventID: ids[0], EventType: store.TaskBlocked, NodeID: nodeID, CommitSHA: h.commitSHA},
		{EventID: ids[1], EventType: store.StatusChanged, NodeID: nodeID, CommitSHA: h.commitSHA},
	}

	return okResp(UpdateResult{Events: events})
}

// TaskUnblock appends a task-unblocked event and a status-changed event atomically.
func (h *Handler) TaskUnblock(projectID, nodeID string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}

	nodeID, err := h.resolveWriteNode(projectID, nodeID)
	if err != nil {
		return errResp(err.Error())
	}

	unblockedPayload, _ := json.Marshal(map[string]string{})
	statusPayload, _ := json.Marshal(map[string]string{"from": "blocked", "to": "pending"})

	batch := []store.Event{
		{
			Type:      store.TaskUnblocked,
			ProjectID: projectID,
			NodeID:    &nodeID,
			CommitSHA: h.commitSHA,
			Payload:   unblockedPayload,
		},
		{
			Type:      store.StatusChanged,
			ProjectID: projectID,
			NodeID:    &nodeID,
			CommitSHA: h.commitSHA,
			Payload:   statusPayload,
		},
	}

	ids, err := h.store.AppendBatch(batch)
	if err != nil {
		h.logger.Error("store append failed", "command", "task-unblock", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "task-unblock", "project", projectID, "node_id", nodeID)
	events := []EventData{
		{EventID: ids[0], EventType: store.TaskUnblocked, NodeID: nodeID, CommitSHA: h.commitSHA},
		{EventID: ids[1], EventType: store.StatusChanged, NodeID: nodeID, CommitSHA: h.commitSHA},
	}

	return okResp(UpdateResult{Events: events})
}

// KnowledgeAdd appends a knowledge-added event.
func (h *Handler) KnowledgeAdd(projectID, nodeID, summary string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}
	if summary == "" {
		return errResp("--summary is required")
	}

	nodeID, err := h.resolveWriteNode(projectID, nodeID)
	if err != nil {
		return errResp(err.Error())
	}

	payload, _ := json.Marshal(map[string]string{"summary": summary})
	id, err := h.store.Append(store.Event{
		Type:      store.KnowledgeAdded,
		ProjectID: projectID,
		NodeID:    &nodeID,
		CommitSHA: h.commitSHA,
		Payload:   payload,
	})
	if err != nil {
		h.logger.Error("store append failed", "command", "knowledge-add", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "knowledge-add", "project", projectID, "node_id", nodeID, "event_id", id)
	return okResp(EventData{
		EventID:   id,
		EventType: store.KnowledgeAdded,
		NodeID:    nodeID,
		CommitSHA: h.commitSHA,
	})
}

// QueryResult is the data payload for fs query.
type QueryResult struct {
	ProjectID string        `json:"project_id"`
	Query     string        `json:"query"`
	Events    []store.Event `json:"events"`
}

// Query performs FTS5 search on event payloads. AC5.
func (h *Handler) Query(projectID, searchTerm string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if searchTerm == "" {
		return errResp("search term is required")
	}

	events, err := h.store.Search(projectID, searchTerm)
	if err != nil {
		h.logger.Error("store search failed", "command", "query", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "query", "project", projectID, "query", searchTerm, "results", len(events))
	return okResp(QueryResult{
		ProjectID: projectID,
		Query:     searchTerm,
		Events:    events,
	})
}

// LogResult is the data payload for fs log.
type LogResult struct {
	ProjectID string        `json:"project_id"`
	Events    []store.Event `json:"events"`
}

// Log replays events for a project with optional filters.
func (h *Handler) Log(projectID string, nodeID *string, eventType *store.EventType) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}

	var filter *store.ReplayFilter
	if nodeID != nil || eventType != nil {
		filter = &store.ReplayFilter{}
		if nodeID != nil {
			filter.NodeID = nodeID
		}
		if eventType != nil {
			filter.Types = []store.EventType{*eventType}
		}
	}

	events, err := h.store.Replay(projectID, filter)
	if err != nil {
		h.logger.Error("store replay failed", "command", "log", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "log", "project", projectID, "events", len(events))
	return okResp(LogResult{
		ProjectID: projectID,
		Events:    events,
	})
}

func errResp(msg string) Response {
	return Response{OK: false, Error: msg}
}

func okResp(data any) Response {
	return Response{OK: true, Data: data}
}
