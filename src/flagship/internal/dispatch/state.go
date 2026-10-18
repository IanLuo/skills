// The parity gate: a switched read answers from the projection only when the
// projection and the event store agree on every state question the read asks.
//
// A divergence is a defect in the projection, the fold, or the read — never a
// preference. So the gate fails closed: the difference is named, the command
// exits non-zero, and nothing is returned from the event store (NFR, AC3). The
// only way to answer from the event store is to say so with FS_STATE_SOURCE=log,
// which is a person's decision and is recorded.
package dispatch

import (
	"fmt"

	"github.com/IanLuo/memstore"
	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/store"
)

// pendingAnswer is every value fs pending asks the store, before the
// environment is read: the unresolved dispatch nodes, each dispatch's delivery
// record, its worker's node and reports, the cap's claim, and the open-gap set.
//
// It is the gate's unit of comparison. Everything derived from herdr — agent and
// pane states, the re-run checks, the unavailable warning — is applied after the
// comparison, because neither source has it and comparing it would compare one
// herdr read against itself.
type pendingAnswer struct {
	Dispatches []pendingDispatch `json:"dispatches"`
	Cap        *command.CapClaim `json:"cap,omitempty"`
	Gaps       int               `json:"undispatched_gaps"`
}

// pendingDispatch is one unresolved dispatch and the state its record leads to.
type pendingDispatch struct {
	Node     command.UnfinishedNode  `json:"node"`
	Delivery *command.DeliveryRecord `json:"delivery,omitempty"`
	Worker   *command.UnfinishedNode `json:"worker,omitempty"`
	Reports  []command.Report        `json:"reports,omitempty"`
}

// stateRefusal is the gate's refusal, carried as an error so the read's one
// error path reports it.
type stateRefusal struct {
	read  string
	scope string
	diff  *store.JSONDiff
}

func (r *stateRefusal) Error() string {
	return fmt.Sprintf(
		"%s: refused — the projection and the event store disagree on scope %s at %s: the event store says %s, the projection says %s; run \"fs state rebuild\" (or FS_STATE_SOURCE=log %s to answer from the event store for this process)",
		r.read, r.scope, r.diff.Field, r.diff.A, r.diff.B, r.read)
}

// gatedAnswer derives the read's store-derived answer from the declared source,
// and runs the gate when the source is the projection.
//
// The event store is derived first in both cases: it is the oracle, so a
// switched read refuses on a difference rather than preferring either side.
func gatedAnswer(read, source string, h *command.Handler, fromLog func() (pendingAnswer, error), fromProjection func() (pendingAnswer, error)) (pendingAnswer, error) {
	logAnswer, err := fromLog()
	if err != nil {
		return pendingAnswer{}, err
	}
	if source == command.SourceLog {
		return logAnswer, nil
	}

	projAnswer, err := fromProjection()
	if err != nil {
		return pendingAnswer{}, err
	}
	diff, err := store.DiffJSON(logAnswer, projAnswer)
	if err != nil {
		return pendingAnswer{}, fmt.Errorf("%s: compare sources: %w", read, err)
	}
	if diff != nil {
		h.Logger().Error("state read refused: the projection and the event store disagree",
			"command", read, "scope", capScope, "field", diff.Field,
			"event_store", diff.A, "projection", diff.B, "source", command.SourceLog)
		return pendingAnswer{}, &stateRefusal{read: read, scope: capScope, diff: diff}
	}
	return projAnswer, nil
}

// answerFromLog derives the pending answer from the event store: the cap scope's
// unresolved dispatches, each one's delivery record, and the node and reports of
// the worker that record names.
func answerFromLog(h *command.Handler) (pendingAnswer, error) {
	nodes, err := h.UnfinishedIn(capScope)
	if err != nil {
		return pendingAnswer{}, fmt.Errorf("pending: replay %s: %w", capScope, err)
	}

	answer := pendingAnswer{Dispatches: []pendingDispatch{}}
	for _, node := range nodes {
		if node.Kind != "dispatch" {
			continue
		}
		d := pendingDispatch{Node: node}
		delivery, recorded, err := h.DeliveryFor(capScope, node.NodeID)
		if err != nil {
			return pendingAnswer{}, fmt.Errorf("pending: read delivery record for %s: %w", node.NodeID, err)
		}
		if recorded {
			d.Delivery = &delivery
			if delivery.Project != "" && delivery.Node != "" {
				worker, found, err := workerOf(h, delivery.Project, delivery.Node)
				if err != nil {
					return pendingAnswer{}, fmt.Errorf("pending: read node %s:%s: %w", delivery.Project, delivery.Node, err)
				}
				if found {
					d.Worker = &worker
				}
				reports, err := h.ReportsFor(delivery.Project, delivery.Node)
				if err != nil {
					return pendingAnswer{}, fmt.Errorf("pending: read reports for %s:%s: %w", delivery.Project, delivery.Node, err)
				}
				d.Reports = reports
			}
		}
		answer.Dispatches = append(answer.Dispatches, d)
	}

	claim, claimed, err := h.CapFor(capScope)
	if err != nil {
		return pendingAnswer{}, fmt.Errorf("pending: read the claim: %w", err)
	}
	if claimed {
		answer.Cap = &claim
	}
	gaps, err := h.OpenGaps()
	if err != nil {
		return pendingAnswer{}, fmt.Errorf("pending: count the backlog: %w", err)
	}
	answer.Gaps = gaps
	return answer, nil
}

// answerFromProjection derives the same answer from the projection: the rows are
// the derived state, so nothing here replays an event. A scope whose rows are
// absent is an empty scope, which is what the gate then reports as a difference
// against whatever the event store holds — an unseeded projection refuses the
// read and names `fs state rebuild` rather than seeding anything during a read.
func answerFromProjection(h *command.Handler) (pendingAnswer, error) {
	ms := h.Projection()
	if ms == nil {
		return pendingAnswer{}, fmt.Errorf(
			"pending: refused — no projection is open, so this read cannot answer from it; run \"fs state rebuild\" after attaching the projection, or FS_STATE_SOURCE=log pending")
	}

	// One read per scope: a scope is read at most once per answer, so the gate
	// costs the projection's own reads and nothing repeated.
	reads := map[string]*store.ProjectedScope{}
	read := func(project string) (*store.ProjectedScope, error) {
		if got, ok := reads[project]; ok {
			return got, nil
		}
		got, err := store.ReadScope(ms, project)
		if err != nil {
			return nil, err
		}
		reads[project] = got
		return got, nil
	}

	capState, err := read(capScope)
	if err != nil {
		return pendingAnswer{}, err
	}

	answer := pendingAnswer{Dispatches: []pendingDispatch{}}
	for _, nid := range capState.NodeIDs {
		node := capState.Nodes[nid]
		if node.Kind != "dispatch" || node.Status == "done" {
			continue
		}
		d := pendingDispatch{Node: unfinishedOf(capScope, node)}
		if delivery := deliveryOf(capState, nid); delivery != nil {
			d.Delivery = delivery
			if delivery.Project != "" && delivery.Node != "" {
				workerScope, err := read(delivery.Project)
				if err != nil {
					return pendingAnswer{}, err
				}
				if worker, ok := workerScope.Nodes[delivery.Node]; ok {
					u := unfinishedOf(delivery.Project, worker)
					d.Worker = &u
				}
				reports, err := reportsOf(workerScope, delivery.Node)
				if err != nil {
					return pendingAnswer{}, err
				}
				d.Reports = reports
			}
		}
		answer.Dispatches = append(answer.Dispatches, d)
	}

	if claim := capOf(capState); claim != nil {
		answer.Cap = claim
	}

	gaps, err := projectedGaps(h, ms, reads, read)
	if err != nil {
		return pendingAnswer{}, err
	}
	answer.Gaps = gaps
	return answer, nil
}

// projectedGaps counts the kind=gap nodes that are not done, across every scope
// the event store names — the same set the event store's own count walks. The
// scope list is a DISTINCT query, not a replay.
func projectedGaps(h *command.Handler, ms *memstore.Store, reads map[string]*store.ProjectedScope, read func(string) (*store.ProjectedScope, error)) (int, error) {
	scopes, err := h.Scopes()
	if err != nil {
		return 0, fmt.Errorf("pending: list scopes: %w", err)
	}
	count := 0
	for _, scope := range scopes {
		if state, ok := reads[scope]; ok {
			for _, nid := range state.NodeIDs {
				node := state.Nodes[nid]
				if node.Kind == "gap" && node.Status != "done" {
					count++
				}
			}
			continue
		}
		// A scope the answer has not otherwise read is counted from the rows'
		// own columns: a gap's openness is its kind and its newest row's status,
		// so nothing here decodes a node document it does not use.
		rows, err := store.ScopeRows(ms, scope)
		if err != nil {
			return 0, err
		}
		status := map[string]string{}
		kind := map[string]string{}
		for _, m := range rows {
			if m.FsNode == "" || !isGapRowKind(m.Kind) {
				continue
			}
			kind[m.FsNode] = m.Kind
			status[m.FsNode] = m.Status
		}
		for nid, st := range status {
			if kind[nid] != "gap" {
				continue
			}
			if st == "closed" || st == "done" {
				continue
			}
			count++
		}
	}
	return count, nil
}

// isGapRowKind reports whether a row is a node version that could be a gap. A
// node's kind is its row's kind, so the filter is the row's own column.
func isGapRowKind(kind string) bool {
	switch kind {
	case "work", "dispatch", "gap", "batch":
		return true
	}
	return false
}

// workerOf reads one worker node's derived state from the event store, the way
// fs status reports it.
func workerOf(h *command.Handler, project, nodeID string) (command.UnfinishedNode, bool, error) {
	info, found, err := h.NodeIn(project, nodeID)
	if err != nil || !found {
		return command.UnfinishedNode{}, false, err
	}
	return command.UnfinishedNode{
		ProjectID: project, NodeID: info.NodeID, Status: info.Status, Goal: info.Goal,
		Kind: info.Kind, FoundBy: info.FoundBy, ParentNodeID: info.ParentNodeID,
		BlockCheck: info.BlockCheck, Check: info.Check,
	}, true, nil
}

// unfinishedOf renders a projected node as the answer's node shape, so both
// sources are compared as the same document.
func unfinishedOf(scope string, node *store.ProjectedNode) command.UnfinishedNode {
	return command.UnfinishedNode{
		ProjectID: scope, NodeID: node.NodeID, Status: node.Status, Goal: node.Goal,
		Kind: queryKind(node.Kind), FoundBy: node.FoundBy, ParentNodeID: node.ParentID,
		BlockCheck: node.BlockCheck, Check: node.Check,
	}
}

// deliveryOf returns the delivery record a cap dispatch node's newest projected
// delivery row carries, or nil when it has none. The newest wins, so a pane
// reused for a later dispatch answers for the dispatch it holds now — the same
// rule the event store's read applies.
func deliveryOf(scope *store.ProjectedScope, capNode string) *command.DeliveryRecord {
	for i := len(scope.Deliveries) - 1; i >= 0; i-- {
		m := scope.Deliveries[i]
		if m.FsNode != capNode {
			continue
		}
		var d command.DeliveryRecord
		if err := store.RowPayload(m, &d); err != nil {
			return nil
		}
		return &d
	}
	return nil
}

// reportsOf returns a worker node's reports, oldest first.
func reportsOf(scope *store.ProjectedScope, node string) ([]command.Report, error) {
	var out []command.Report
	for _, m := range scope.Reports {
		if m.FsNode != node {
			continue
		}
		var p struct {
			Kind string `json:"kind"`
			Line string `json:"line"`
		}
		if err := store.RowPayload(m, &p); err != nil {
			return nil, err
		}
		out = append(out, command.Report{Kind: p.Kind, Line: p.Line, At: m.Ts})
	}
	return out, nil
}

// capOf returns the newest projected claim, or nil when nothing has claimed.
func capOf(scope *store.ProjectedScope) *command.CapClaim {
	if len(scope.CapRows) == 0 {
		return nil
	}
	var c command.CapClaim
	if err := store.RowPayload(scope.CapRows[len(scope.CapRows)-1], &c); err != nil {
		return nil
	}
	return &c
}

// queryKind is the projected node kind as the query package's Kind. The
// projection cannot import query (query imports store), so the boundary is
// where the two meet.
func queryKind(kind string) query.Kind {
	return query.Kind(kind)
}
