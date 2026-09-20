// Package query derives current project state from event replay.
// It owns the tree-building logic and summary generation.
// (SYSTEM-DESIGN R1: Query Engine — reads derived state from events)
package query

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flagship-dev/flagship/internal/store"
)

// Kind is a node's structural kind: what the node is, independent of the prose
// in its goal. It replaces the conventions that used to be words in the goal
// string — a node stays what it is when its goal is rewritten.
type Kind string

const (
	// KindWork is an ordinary task. It is the default, so a node created
	// without a kind — including every node created before kinds existed —
	// reads as work.
	KindWork Kind = "work"
	// KindDispatch is a node fs dispatch wrote, on the cap's scope and on the
	// worker's scope in the target project. The dispatch gates select on it.
	KindDispatch Kind = "dispatch"
	// KindGap is something noticed and not dispatched: the backlog.
	KindGap Kind = "gap"
)

// ValidKind reports whether k names a kind. The empty string is not a kind:
// absence means the default, and callers resolve it rather than storing it.
func ValidKind(k Kind) bool {
	switch k {
	case KindWork, KindDispatch, KindGap:
		return true
	}
	return false
}

// Node represents a single task in the project tree.
type Node struct {
	NodeID    string   `json:"node_id"`
	Goal      string   `json:"goal"`
	Kind      Kind     `json:"kind"`
	FoundBy   string   `json:"found_by,omitempty"`
	Status    string   `json:"status"`
	ParentID  string   `json:"parent_id,omitempty"`
	Orphan    bool     `json:"orphan,omitempty"`
	Children  []*Node  `json:"children,omitempty"`
	Decisions []string `json:"decisions,omitempty"`
	Knowledge []string `json:"knowledge,omitempty"`

	// BlockCheck is the shell command the node's last block recorded, when one
	// did. A block reason is prose, so nothing re-verifies it; this is what lets
	// a reader re-run the condition and see that it has expired. It is empty for
	// a block with no check — prose alone, with no claim of freshness.
	BlockCheck string `json:"block_check,omitempty"`
}

// Tree is the derived project state.
type Tree struct {
	ProjectID string           `json:"project_id"`
	Roots     []*Node          `json:"roots"`
	Nodes     map[string]*Node `json:"-"` // lookup by node_id
}

// Engine reads the event store and derives state.
type Engine struct {
	store *store.Store
}

// NewEngine creates a query engine backed by the given store.
func NewEngine(s *store.Store) *Engine {
	return &Engine{store: s}
}

// DeriveTree replays all events for a project and builds the task tree.
func (e *Engine) DeriveTree(projectID string) (*Tree, error) {
	events, err := e.store.Replay(projectID, nil)
	if err != nil {
		return nil, fmt.Errorf("query: replay: %w", err)
	}

	return BuildTree(projectID, events), nil
}

// BuildTree constructs a Tree from a slice of events. Exported for use
// by the command layer (to avoid re-querying the store).
func BuildTree(projectID string, events []store.Event) *Tree {
	tree := &Tree{
		ProjectID: projectID,
		Nodes:     make(map[string]*Node),
	}

	// Maintain insertion order for roots.
	var rootOrder []string

	for _, evt := range events {
		if evt.NodeID == nil {
			continue
		}
		nid := *evt.NodeID

		switch evt.Type {
		case store.TaskCreated:
			var p struct {
				Goal    string `json:"goal"`
				Kind    string `json:"kind"`
				FoundBy string `json:"found_by"`
			}
			_ = json.Unmarshal(evt.Payload, &p)
			parentID := ""
			if evt.ParentNodeID != nil {
				parentID = *evt.ParentNodeID
			}
			node := &Node{
				NodeID:   nid,
				Goal:     p.Goal,
				Kind:     nodeKind(p.Kind, p.Goal),
				FoundBy:  p.FoundBy,
				Status:   "pending",
				ParentID: parentID,
			}
			tree.Nodes[nid] = node
			if parentID == "" {
				rootOrder = append(rootOrder, nid)
			}

		case store.StatusChanged:
			var p struct{ To string }
			_ = json.Unmarshal(evt.Payload, &p)
			node := tree.ensureNode(nid)
			node.Status = p.To

		case store.DecisionRecorded:
			var p struct{ Summary string }
			_ = json.Unmarshal(evt.Payload, &p)
			node := tree.ensureNode(nid)
			node.Decisions = append(node.Decisions, p.Summary)

		case store.KnowledgeAdded:
			var p struct{ Summary string }
			_ = json.Unmarshal(evt.Payload, &p)
			node := tree.ensureNode(nid)
			node.Knowledge = append(node.Knowledge, p.Summary)

		case store.TaskBlocked:
			var p struct {
				Check string `json:"check"`
			}
			_ = json.Unmarshal(evt.Payload, &p)
			tree.ensureNode(nid).BlockCheck = p.Check

		case store.TaskUnblocked:
			tree.ensureNode(nid).BlockCheck = ""

		case store.MetadataChanged:
			var p struct {
				Field    string `json:"field"`
				NewValue string `json:"new_value"`
			}
			_ = json.Unmarshal(evt.Payload, &p)
			switch p.Field {
			case "goal":
				node := tree.ensureNode(nid)
				node.Goal = p.NewValue
			case "kind":
				node := tree.ensureNode(nid)
				node.Kind = parseKind(p.NewValue)
			}

		default:
			// Any other event — status derived via StatusChanged. Ensure node
			// exists for orphan events.
			tree.ensureNode(nid)
		}
	}

	// Link children to parents. A node whose parent id is not present in the
	// project is an orphan: no root reaches it, so it would vanish from any
	// walk from the roots. Mark it and let it stand as a root of its own, so
	// fs status agrees with fs unfinished about which nodes exist.
	children := make(map[string]bool)
	for _, node := range tree.Nodes {
		if node.ParentID == "" {
			continue
		}
		if parent, ok := tree.Nodes[node.ParentID]; ok {
			parent.Children = append(parent.Children, node)
			children[node.NodeID] = true
			continue
		}
		node.Orphan = true
	}

	// Build roots list: nodes without a parent, preserving insertion order.
	// Nodes in rootOrder were explicitly created without a parent.
	seen := make(map[string]bool)
	for _, nid := range rootOrder {
		if node, ok := tree.Nodes[nid]; ok {
			tree.Roots = append(tree.Roots, node)
			seen[nid] = true
		}
	}
	// Add every node that is not a child: orphan-event nodes (no task-created)
	// and orphaned nodes (a parent id with no matching node).
	for nid, node := range tree.Nodes {
		if !seen[nid] && !children[nid] {
			tree.Roots = append(tree.Roots, node)
		}
	}

	return tree
}

// ensureNode returns the node for nid, creating it if needed (orphan events).
func (t *Tree) ensureNode(nid string) *Node {
	if n, ok := t.Nodes[nid]; ok {
		return n
	}
	n := &Node{
		NodeID: nid,
		Kind:   KindWork,
		Status: "pending",
	}
	t.Nodes[nid] = n
	return n
}

// nodeKind resolves the kind of a task-created event. An event written before
// nodes carried a kind carries none: it reads as the default, except that a
// goal fs dispatch wrote was a dispatch then and still is. That legacy read is
// against the creation goal, never the current one, so editing a goal cannot
// change what a node is — which is the failure the prefix convention had.
func nodeKind(raw, createdGoal string) Kind {
	if k := Kind(raw); ValidKind(k) {
		return k
	}
	if legacyDispatchGoal(createdGoal) {
		return KindDispatch
	}
	return KindWork
}

// parseKind reads a kind from an event payload, defaulting to work when it is
// absent or unrecognised: an unknown kind is not a fourth kind, it is a default.
func parseKind(raw string) Kind {
	if k := Kind(raw); ValidKind(k) {
		return k
	}
	return KindWork
}

// legacyDispatchGoal reports whether a goal was written by fs dispatch before
// nodes carried a kind. It always labelled its node "dispatch <type>: <goal>".
//
// This is the only prose read of a goal left in the codebase. It exists solely
// so events written before kind existed keep behaving as they did, and it is
// read once, against the creation goal. Every node written since carries its
// kind, and a node whose goal is edited does not change kind.
func legacyDispatchGoal(goal string) bool {
	rest, ok := strings.CutPrefix(goal, "dispatch ")
	if !ok {
		return false
	}
	typ, _, ok := strings.Cut(rest, ": ")
	return ok && typ != ""
}

// Summary returns a concise text summary of the project tree, suitable for
// inclusion in an LLM context prompt.
func (e *Engine) Summary(projectID string) (string, error) {
	tree, err := e.DeriveTree(projectID)
	if err != nil {
		return "", err
	}
	return FormatSummary(tree), nil
}

// FormatSummary renders a Tree as a concise text summary.
func FormatSummary(tree *Tree) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Project: %s\n", tree.ProjectID)

	if len(tree.Roots) == 0 {
		b.WriteString("No tasks.\n")
		return b.String()
	}

	for _, root := range tree.Roots {
		formatNode(&b, root, 0)
	}
	return b.String()
}

func formatNode(b *strings.Builder, node *Node, depth int) {
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s- [%s] %s (%s)\n", indent, node.NodeID, node.Goal, node.Status)

	for _, d := range node.Decisions {
		fmt.Fprintf(b, "%s  decision: %s\n", indent, d)
	}
	for _, k := range node.Knowledge {
		fmt.Fprintf(b, "%s  knowledge: %s\n", indent, k)
	}
	for _, child := range node.Children {
		formatNode(b, child, depth+1)
	}
}
