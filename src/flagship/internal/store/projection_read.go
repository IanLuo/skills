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
	// Rows is how many versions the node has, and AllRetired says every one of
	// them is closed — the node was absorbed by a merge.
	Rows       int
	AllRetired bool
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
		node := &ProjectedNode{NodeID: nid, Kind: "work", Status: "pending", Rows: len(vs)}
		// The current row is the newest one still open. A node whose rows are all
		// closed was absorbed by a merge: the last row is its state and its
		// status is done, which is what the merge wrote before the close retired
		// the row.
		cur := vs[len(vs)-1]
		node.AllRetired = true
		for i := len(vs) - 1; i >= 0; i-- {
			if vs[i].Status != closedStatus {
				cur, node.AllRetired = vs[i], false
				break
			}
		}
		node.RowID = cur.Id
		node.Status = cur.Status
		if node.AllRetired {
			node.Status = "done"
		}
		doc := nodeDoc{Kind: cur.Kind}
		if got := docOf(cur.Extra); got != nil {
			doc = *got
		}
		node.Goal, node.Kind = doc.Goal, doc.Kind
		if !isNodeKind(node.Kind) {
			node.Kind = "work"
		}
		node.ParentID = doc.ParentNodeID
		node.FoundBy = doc.FoundBy
		node.Check = doc.Check
		node.BlockCheck = doc.BlockCheck
		node.Severity = doc.Severity
		node.Integrates = doc.Integrates
		node.Reviews = doc.Reviews
		node.Absorbed = doc.Absorbed
		out.Nodes[nid] = node
		out.NodeIDs = append(out.NodeIDs, nid)
	}
	sort.Strings(out.NodeIDs)
	return out, nil
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
