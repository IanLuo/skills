// Reading the projection: the state a switched read answers with.
//
// A read reconstructs a scope's nodes, deliveries, reports and cap claim from
// the projection's rows alone — no event store replay, no query.BuildTree. That
// is the whole point of the switch: the rows are the derived state, and reading
// them is a scope-filtered query rather than a walk over the history.
//
// Every read here filters on a scope built by ProjectionScope, so no read can
// return a non-fs: row (D5, AC6).
package store

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/IanLuo/memstore"
)

// ScopeRows returns one scope's live rows, oldest first. It is the raw read
// behind ReadScope, exported for a caller that only needs a row's own columns —
// counting a kind, not reconstructing a node — so it pays no decode it does not
// use.
func ScopeRows(ms *memstore.Store, projectID string) ([]memstore.Memory, error) {
	return scopeRows(ms, ProjectionScope(projectID))
}

// ProjectedNode is one node's current state, reconstructed from its rows.
//
// The state is derived by folding the node's rows in EVENT order and applying
// each row's own event to the state, exactly as query.BuildTree applies the
// events — not by reading the newest row's snapshot. That is what makes the read
// independent of the order the rows were written in: a fold that arrives late
// (a lag filled by `fs state rebuild`, or a fold that failed and was re-run)
// writes a row whose snapshot could not inherit from a predecessor that was not
// there yet, and the state must still come out the same.
//
// RowID is the row of the node's newest event, and Rows is how many versions the
// node has; both are file-local and are for a caller that needs to point at a
// row, never part of the state a comparison uses.
type ProjectedNode struct {
	NodeID     string
	Goal       string
	Kind       string
	Status     string
	ParentID   string
	FoundBy    string
	Check      string
	BlockCheck string
	Severity   string
	Integrates string
	Reviews    string
	Absorbed   []string
	RowID      int64
	Rows       int
}

// ProjectedScope is one scope's state as the projection holds it. Side rows are
// returned whole: their payload is the event's own, and the caller decodes the
// shape it wrote, which is what lets the projection answer without a second
// mapping in this package.
type ProjectedScope struct {
	ProjectID  string
	RootPath   string
	Nodes      map[string]*ProjectedNode
	NodeIDs    []string
	CapRows    []memstore.Memory
	Deliveries []memstore.Memory
	Reports    []memstore.Memory
	Blocks     []memstore.Memory
}

// ReadScope reconstructs one scope from the projection. It reads only the rows
// of "fs:"+projectID, and a scope with no rows is an empty scope rather than an
// error: the divergence from the event store is the gate's to find, not this
// read's to invent.
func ReadScope(ms *memstore.Store, projectID string) (*ProjectedScope, error) {
	rows, err := scopeRows(ms, ProjectionScope(projectID))
	if err != nil {
		return nil, err
	}
	out := &ProjectedScope{ProjectID: projectID, Nodes: map[string]*ProjectedNode{}}

	versions := map[string][]memstore.Memory{}
	for _, m := range rows {
		switch {
		case m.Kind == rowProject:
			if out.RootPath == "" {
				out.RootPath = m.Text
			}
		case m.Kind == rowCap:
			out.CapRows = append(out.CapRows, m)
		case m.Kind == rowDelivery:
			out.Deliveries = append(out.Deliveries, m)
		case m.Kind == rowReport:
			out.Reports = append(out.Reports, m)
		case m.Kind == rowBlock:
			out.Blocks = append(out.Blocks, m)
		case isNodeKind(m.Kind) && m.FsNode != "":
			versions[m.FsNode] = append(versions[m.FsNode], m)
		}
	}

	for nid, vs := range versions {
		out.Nodes[nid] = deriveNode(nid, vs)
		out.NodeIDs = append(out.NodeIDs, nid)
	}
	sort.Strings(out.NodeIDs)
	return out, nil
}

// deriveNode folds a node's version rows in event order into the state the
// event store would derive, using each row's own event type and payload. A row
// whose snapshot is incomplete because it was written out of order contributes
// only what its own event establishes, which is all BuildTree takes from it too.
func deriveNode(nid string, vs []memstore.Memory) *ProjectedNode {
	ordered := make([]memstore.Memory, len(vs))
	copy(ordered, vs)
	sort.SliceStable(ordered, func(i, j int) bool {
		return eventOrderOf(ordered[i]) < eventOrderOf(ordered[j])
	})

	node := &ProjectedNode{NodeID: nid, Kind: "work", Status: "pending", Rows: len(vs)}
	for _, m := range ordered {
		env, err := decodeExtra(m.Extra)
		if err != nil {
			continue
		}
		node.RowID = m.Id
		payload := payloadFields(env)
		switch env.EventType {
		case string(TaskCreated):
			node.Goal = payload["goal"]
			node.Kind = nodeKindOf(payload["kind"], node.Goal)
			node.FoundBy = payload["found_by"]
			node.Check = payload["check"]
			node.Integrates = payload["integrates"]
			node.Reviews = payload["reviews"]
			node.Severity = parseSeverityOf(payload["severity"])
			// The parent is the event's own column, which the row's document
			// carries because a task-created's document is built from the event
			// alone and never inherited.
			if env.Node != nil {
				node.ParentID = env.Node.ParentNodeID
			}
		case string(StatusChanged):
			if to := payload["to"]; to != "" {
				node.Status = to
			}
		case string(MetadataChanged):
			switch payload["field"] {
			case "goal":
				node.Goal = payload["new_value"]
			case "kind":
				node.Kind = parseKindOf(payload["new_value"])
			case "severity":
				node.Severity = parseSeverityOf(payload["new_value"])
			case "absorbed":
				node.Absorbed = splitAbsorbed(payload["new_value"])
			}
		case string(TaskBlocked):
			node.BlockCheck = payload["check"]
		case string(TaskUnblocked):
			node.BlockCheck = ""
		}
	}
	if !isNodeKind(node.Kind) {
		node.Kind = "work"
	}
	return node
}

// eventOrderOf is a row's place in its node's event order: the provenance id,
// which is the event's own ULID and so sorts in time order. A row with no
// provenance keeps its insertion order after every row that has one.
func eventOrderOf(m memstore.Memory) string {
	if m.OriginId == "" {
		return "~"
	}
	return m.OriginId
}

// payloadFields decodes a row's raw event payload into strings, so the read
// applies the event rather than the snapshot beside it.
func payloadFields(env rowExtra) map[string]string {
	out := map[string]string{}
	var raw map[string]any
	if err := json.Unmarshal(env.Payload, &raw); err != nil {
		return out
	}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// RowPayload decodes a side row's raw event payload, which is what the row was
// projected from: the caller decodes the shape its own writer wrote.
func RowPayload(m memstore.Memory, into any) error {
	if err := rowExtraOf(m, into); err != nil {
		return fmt.Errorf("projection: row %d payload: %w", m.Id, err)
	}
	return nil
}

// rowExtraOf decodes the payload of a side row.
func rowExtraOf(m memstore.Memory, into any) error {
	env, err := decodeExtra(m.Extra)
	if err != nil {
		return err
	}
	return decodeInto(env.Payload, into)
}
