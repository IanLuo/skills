// Package command validates input, produces typed events, and manages the
// response envelope. It is the gatekeeper between CLI and Event Store.
// (SYSTEM-DESIGN R1, ARCHITECTURE R4)
package command

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"

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
// binding of a cap dispatch node to the pane and agent carrying its brief.
type DeliveryRecord struct {
	PaneID string `json:"pane_id"`
	Agent  string `json:"agent"`
	Engine string `json:"engine"`
}

// TaskInfo represents derived task state for status output.
type TaskInfo struct {
	NodeID       string   `json:"node_id"`
	Goal         string   `json:"goal"`
	Status       string   `json:"status"`
	ParentNodeID string   `json:"parent_node_id,omitempty"`
	Decisions    []string `json:"decisions,omitempty"`
}

// StatusResult is the data payload for fs status.
type StatusResult struct {
	ProjectID string     `json:"project_id"`
	Tasks     []TaskInfo `json:"tasks"`
}

// UnfinishedNode is one task that is not done, tagged with the scope it lives
// in so fs unfinished can report every scope at once.
type UnfinishedNode struct {
	ProjectID string `json:"project_id"`
	NodeID    string `json:"node_id"`
	Status    string `json:"status"`
	Goal      string `json:"goal"`
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

// TaskAdd appends a task-created event. AC2.
func (h *Handler) TaskAdd(projectID, goal string, parentNodeID *string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if goal == "" {
		return errResp("goal is required")
	}

	nodeID := newNodeID()
	payload, _ := json.Marshal(map[string]string{"goal": goal})

	id, err := h.store.Append(store.Event{
		Type:         store.TaskCreated,
		ProjectID:    projectID,
		NodeID:       &nodeID,
		ParentNodeID: parentNodeID,
		CommitSHA:    h.commitSHA,
		Payload:      payload,
	})
	if err != nil {
		h.logger.Error("store append failed", "command", "task-add", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "task-add", "project", projectID, "node_id", nodeID, "event_id", id)
	return okResp(EventData{
		EventID:   id,
		EventType: store.TaskCreated,
		NodeID:    nodeID,
		CommitSHA: h.commitSHA,
	})
}

// TaskUpdate appends status-changed and optionally decision-recorded events. AC4.
func (h *Handler) TaskUpdate(projectID, nodeID, status, decision string, commitSHA *string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
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
// binding it to the pane and agent that carry its brief. Recorded by the
// dispatcher at delivery time, so close-out reads the binding rather than
// whatever a decision happened to say.
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

// TaskEdit appends a metadata-changed event. AC11.
func (h *Handler) TaskEdit(projectID, nodeID, newGoal string, commitSHA *string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}

	sha := h.commitSHA
	if commitSHA != nil {
		sha = commitSHA
	}

	// Derive current goal for old_value.
	oldGoal := h.deriveGoal(projectID, nodeID)

	if newGoal != "" {
		payload, _ := json.Marshal(map[string]string{
			"field":     "goal",
			"old_value": oldGoal,
			"new_value": newGoal,
		})
		id, err := h.store.Append(store.Event{
			Type:      store.MetadataChanged,
			ProjectID: projectID,
			NodeID:    &nodeID,
			CommitSHA: sha,
			Payload:   payload,
		})
		if err != nil {
			h.logger.Error("store append failed", "command", "task-edit", "error", err)
			return errResp(err.Error())
		}
		h.logger.Info("command executed", "command", "task-edit", "project", projectID, "node_id", nodeID, "event_id", id)
		return okResp(EventData{
			EventID:   id,
			EventType: store.MetadataChanged,
			NodeID:    nodeID,
			CommitSHA: sha,
		})
	}

	return errResp("nothing to edit: provide --goal")
}

// Status derives current state for all tasks in a project. AC3.
// Delegates tree-building to the query engine.
func (h *Handler) Status(projectID string) Response {
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

	return okResp(result)
}

// collectTasks flattens the tree into the TaskInfo slice (pre-order).
func collectTasks(node *query.Node, out *[]TaskInfo) {
	*out = append(*out, TaskInfo{
		NodeID:       node.NodeID,
		Goal:         node.Goal,
		Status:       node.Status,
		ParentNodeID: node.ParentID,
		Decisions:    node.Decisions,
	})
	for _, child := range node.Children {
		collectTasks(child, out)
	}
}

// Unfinished lists every node that is not done, across every scope in the
// store. Scopes come from the events themselves, not the registry: a node in a
// scope that was never registered, or whose registry row was lost, still
// reports here.
func (h *Handler) Unfinished() Response {
	scopes, err := h.store.Scopes()
	if err != nil {
		h.logger.Error("store scopes failed", "command", "unfinished", "error", err)
		return errResp(err.Error())
	}

	unfinished := []UnfinishedNode{}
	for _, scope := range scopes {
		nodes, err := h.UnfinishedIn(scope)
		if err != nil {
			h.logger.Error("store replay failed", "command", "unfinished", "scope", scope, "error", err)
			return errResp(err.Error())
		}
		unfinished = append(unfinished, nodes...)
	}

	h.logger.Info("command executed", "command", "unfinished", "scopes", len(scopes), "unfinished", len(unfinished))
	return okResp(map[string]any{"unfinished": unfinished})
}

// UnfinishedIn lists the nodes in one scope that are not done, ordered by node
// id. Status is derived by replaying the scope's events, the same way fs status
// derives it.
func (h *Handler) UnfinishedIn(scope string) ([]UnfinishedNode, error) {
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

	unfinished := make([]UnfinishedNode, 0, len(nodes))
	for _, node := range nodes {
		if node.Status == "done" {
			continue
		}
		unfinished = append(unfinished, UnfinishedNode{
			ProjectID: scope,
			NodeID:    node.NodeID,
			Status:    node.Status,
			Goal:      node.Goal,
		})
	}
	return unfinished, nil
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
func (h *Handler) TaskBlock(projectID, nodeID, reason string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}
	if reason == "" {
		return errResp("--reason is required")
	}

	from := h.deriveStatus(projectID, nodeID)

	blockedPayload, _ := json.Marshal(map[string]string{"reason": reason})
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
