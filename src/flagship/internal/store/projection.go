// Projection: folding a project's event log into memstore, so memstore's rows
// answer the same state questions query.BuildTree answers today. This is the
// slice-1 seam of the store-layer replacement (card .agents/dispatch/
// fs-store-memstore.md): additive, library only — nothing reads it yet, and it
// exercises memstore purely through its exported surface.
package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/IanLuo/memstore"
)

// Rel names of the Link edges the projection writes. They are the projection's
// choice — the card left the names open — and they start at a node's first row
// so a node's outgoing set never moves when a goal edit adds rows. memstore's
// Search walk surfaces only superseded-by and found-by; the rest are stored but
// not surfaced, which slice 2 must answer (a finding, not a choice).
const (
	relDecision     = "decision"
	relKnowledge    = "knowledge"
	relReport       = "report"
	relBlock        = "block"
	relAbsorbedInto = "absorbed-into"
)

// Projection is what one ProjectEvents call wrote, so a reader can walk the
// result without the provenance columns — which stay native and empty this
// slice because memstore's Put hardcodes them (the cap's ruling, recorded on
// skills:t-12cc0d54; a memstore change honouring them is carded on
// skills:t-b1a2e486 for before slice 3).
type Projection struct {
	ProjectID string
	// ProjectMemory is the project-created row, or 0 when the stream has none.
	ProjectMemory int64
	// NodeRows maps each node to its rows, oldest first. The last element is
	// the current row; superseded and absorbed rows sit before it, closed.
	NodeRows map[string][]int64
	// CapRows are the scope-level cap-claimed rows, in event order.
	CapRows []int64
}

// ProjectEvents folds one project's events — as Store.Replay returns them —
// into a memstore file, writing only through memstore's exported verbs.
//
// The mapping, verified against the card's table:
//
//   - project-created    one row per project: Kind=project, Text=root_path
//   - task-created       one row per node: Kind=<the node's kind>, Text=<goal>,
//     Parent=<the parent node's first row>; the first row carries the parent,
//     later rows are edits
//   - status-changed     the CURRENT (last, non-closed) row's Status column
//   - decision-recorded / knowledge-added / report-recorded
//     their own rows, linked from the node row (rel: decision | knowledge |
//     report)
//   - task-blocked       the node's status rides the paired status-changed; a
//     Kind=block row (Text=reason) linked from the node row (rel: block)
//   - task-unblocked     nothing — the paired status-changed carries the state
//   - metadata-changed   goal/kind/severity edits project as a NEW row with
//     the new content, the row they replace CLOSED with Supersedes — the
//     correction rule, applied to every content-bearing field because fs
//     allows the edit and memstore content is immutable; field=absorbed is a
//     merge (below), an unknown field folds to nothing, as in BuildTree
//   - task-merge         not an event: TaskMerge writes a decision-recorded
//     ("absorbed into …"), a status-changed(done) and, on the survivor, a
//     metadata-changed(absorbed). The absorbed node's rows are CLOSED and a
//     link names the survivor (rel: absorbed-into). A cross-scope absorbee —
//     a full address in the survivor's list — is not in this store and is not
//     closed here.
//   - cap-claimed        a scope-level row per event: Kind=cap, Text=pane_id
//   - delivery-recorded / plan-recorded
//     not in the card's table and not mapped — named, in the card's words, not
//     invented; BuildTree derives nothing from them either
//
// Every row's Extra is the origin envelope the cap ruled in: event_id,
// project_id, commit_sha and fs_node when the event carries them, plus the
// event's raw payload — which is also where every field memstore has no column
// for (severity, check, found_by, …) rides. A node set is every node the
// events name: BuildTree materializes a node from any event that names it, and
// the projection must answer the same set, so a node with no task-created
// event gets a row with its id as text — there is no goal to store.
//
// It folds a fixed stream into a single store, with no resume semantics: a
// second run against the same file duplicates rows (Import guards on
// origin_id; nothing here does, because Put cannot write it). Slice 3's
// migration must answer that, alongside the memstore provenance change.
func ProjectEvents(ms *memstore.Store, projectID string, events []Event) (*Projection, error) {
	scope := "fs:" + projectID
	p := &Projection{ProjectID: projectID, NodeRows: make(map[string][]int64)}

	// Phase 1: fold. State derives the way BuildTree derives it — last
	// status-changed wins, edits correct rather than update — so phase 2 can
	// write rows in an order memstore's verbs allow.
	type rowFold struct {
		text  string
		kind  string
		ts    time.Time
		extra string
	}
	type nodeFold struct {
		id        string
		parent    *string
		status    string
		rows      []rowFold
		decisions []rowFold
		knowledge []rowFold
		reports   []rowFold
		blocked   []rowFold
		absorbed  []string
		firstTs   time.Time
		firstE    string
	}
	current := func(n *nodeFold) rowFold {
		if len(n.rows) > 0 {
			return n.rows[len(n.rows)-1]
		}
		return rowFold{text: n.id, kind: "work"}
	}

	var (
		nodeOrder []string
		nodes     = make(map[string]*nodeFold)
		capEvents []Event
		created   *Event
	)
	seen := func(id string) *nodeFold {
		n := nodes[id]
		if n == nil {
			n = &nodeFold{id: id}
			nodes[id] = n
			nodeOrder = append(nodeOrder, id)
		}
		return n
	}

	for i := range events {
		evt := events[i]
		switch evt.Type {
		case ProjectCreated:
			if created == nil {
				e := evt
				created = &e
			}
			continue
		case CapClaimed:
			capEvents = append(capEvents, evt)
			continue
		}
		if evt.NodeID == nil {
			continue
		}
		n := seen(*evt.NodeID)
		if n.firstTs.IsZero() {
			n.firstTs = evt.Timestamp
			n.firstE = extraOf(evt, payloadMap(evt))
		}
		switch evt.Type {
		case TaskCreated:
			var pl struct {
				Goal string `json:"goal"`
				Kind string `json:"kind"`
			}
			_ = json.Unmarshal(evt.Payload, &pl)
			n.parent = cloneString(evt.ParentNodeID)
			n.rows = append(n.rows, rowFold{
				text: pl.Goal, kind: nodeKindOf(pl.Kind, pl.Goal),
				ts: evt.Timestamp, extra: extraOf(evt, payloadMap(evt)),
			})
		case StatusChanged:
			var pl struct {
				To string `json:"to"`
			}
			_ = json.Unmarshal(evt.Payload, &pl)
			n.status = pl.To
		case DecisionRecorded:
			var pl struct {
				Summary string `json:"summary"`
			}
			_ = json.Unmarshal(evt.Payload, &pl)
			n.decisions = append(n.decisions, rowFold{
				text: pl.Summary, kind: "decision",
				ts: evt.Timestamp, extra: extraOf(evt, payloadMap(evt)),
			})
		case KnowledgeAdded:
			var pl struct {
				Summary string `json:"summary"`
			}
			_ = json.Unmarshal(evt.Payload, &pl)
			n.knowledge = append(n.knowledge, rowFold{
				text: pl.Summary, kind: "knowledge",
				ts: evt.Timestamp, extra: extraOf(evt, payloadMap(evt)),
			})
		case ReportRecorded:
			var pl struct {
				Line string `json:"line"`
			}
			_ = json.Unmarshal(evt.Payload, &pl)
			n.reports = append(n.reports, rowFold{
				text: pl.Line, kind: "report",
				ts: evt.Timestamp, extra: extraOf(evt, payloadMap(evt)),
			})
		case TaskBlocked:
			var pl struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(evt.Payload, &pl)
			n.blocked = append(n.blocked, rowFold{
				text: pl.Reason, kind: "block",
				ts: evt.Timestamp, extra: extraOf(evt, payloadMap(evt)),
			})
		case TaskUnblocked:
			// The paired status-changed carries the state.
		case MetadataChanged:
			var pl struct {
				Field    string `json:"field"`
				NewValue string `json:"new_value"`
			}
			_ = json.Unmarshal(evt.Payload, &pl)
			switch pl.Field {
			case "absorbed":
				n.absorbed = splitAbsorbed(pl.NewValue)
			case "goal", "kind", "severity":
				cur := current(n)
				text, kind := cur.text, cur.kind
				switch pl.Field {
				case "goal":
					text = pl.NewValue
				case "kind":
					kind = parseKindOf(pl.NewValue)
				}
				n.rows = append(n.rows, rowFold{
					text: text, kind: kind,
					ts: evt.Timestamp, extra: extraOf(evt, payloadMap(evt)),
				})
			}
		default:
			// delivery-recorded / plan-recorded: named above, not mapped.
		}
	}

	// Phase 2: write. Parents first, so a child's Parent column can name a row
	// that exists — Put is the only writer of that column.
	order := make([]*nodeFold, 0, len(nodes))
	placed := make(map[string]bool, len(nodes))
	var visit func(n *nodeFold)
	visit = func(n *nodeFold) {
		if placed[n.id] {
			return
		}
		placed[n.id] = true
		if n.parent != nil {
			if par, ok := nodes[*n.parent]; ok {
				visit(par)
			}
		}
		order = append(order, n)
	}
	for _, id := range nodeOrder {
		visit(nodes[id])
	}

	if created != nil {
		pl := payloadMap(*created)
		root, _ := pl["root_path"].(string)
		if root == "" {
			// A project-created event with no root_path cannot name one; the
			// row still needs non-empty text (Put refuses empty).
			root = projectID
		}
		id, err := ms.Put(&memstore.Memory{
			Ts: created.Timestamp, Kind: "project", Scope: scope,
			Text: root, Extra: extraOf(*created, pl),
		})
		if err != nil {
			return nil, fmt.Errorf("projection: project row: %w", err)
		}
		p.ProjectMemory = id
	}

	for _, n := range order {
		rows := n.rows
		if len(rows) == 0 {
			rows = []rowFold{{text: n.id, kind: "work", ts: n.firstTs, extra: n.firstE}}
		}
		status := n.status
		if status == "" {
			status = "pending"
		}
		ids := make([]int64, len(rows))
		for i, r := range rows {
			var parent *int64
			if i == 0 && n.parent != nil {
				if pid, ok := p.NodeRows[*n.parent]; ok && len(pid) > 0 {
					v := pid[0]
					parent = &v
				}
			}
			id, err := ms.Put(&memstore.Memory{
				Ts: r.ts, Kind: r.kind, Scope: scope,
				Text: r.text, Extra: r.extra, Status: status, Parent: parent,
			})
			if err != nil {
				return nil, fmt.Errorf("projection: node %s row %d: %w", n.id, i, err)
			}
			ids[i] = id
		}
		p.NodeRows[n.id] = ids
		// Edits close the row they replace — the correction rule. Closing runs
		// newest first because CloseMemory is a no-op on an already-closed row:
		// the chain 0→1→2 must see 1 open when 2 claims it.
		for i := len(ids) - 1; i >= 1; i-- {
			if err := ms.CloseMemory(ids[i-1], &ids[i]); err != nil {
				return nil, fmt.Errorf("projection: node %s supersede: %w", n.id, err)
			}
		}
	}

	side := func(nodeID, rel string, egs []rowFold) error {
		first := p.NodeRows[nodeID][0]
		for _, r := range egs {
			id, err := ms.Put(&memstore.Memory{
				Ts: r.ts, Kind: r.kind, Scope: scope, Text: r.text, Extra: r.extra,
			})
			if err != nil {
				return fmt.Errorf("projection: %s on %s: %w", rel, nodeID, err)
			}
			if err := ms.Link(first, rel, id); err != nil {
				return fmt.Errorf("projection: link %s %s->%d: %w", rel, nodeID, id, err)
			}
		}
		return nil
	}
	for _, n := range order {
		for _, s := range []struct {
			rel string
			egs []rowFold
		}{{relDecision, n.decisions}, {relKnowledge, n.knowledge}, {relReport, n.reports}, {relBlock, n.blocked}} {
			if err := side(n.id, s.rel, s.egs); err != nil {
				return nil, err
			}
		}
	}

	// Merges close the absorbed node and link it to its survivor. The absorbed
	// list is the survivor's own record, whole list per event, last event wins
	// (the same shape BuildTree reads). A full address names an absorbee in
	// another scope, whose rows are not in this store; that half of a
	// cross-scope merge is not closed here (finding, named on the node).
	for _, n := range order {
		if len(n.absorbed) == 0 {
			continue
		}
		survivor := p.NodeRows[n.id][0]
		for _, id := range n.absorbed {
			if strings.Contains(id, ":") {
				continue
			}
			abs, ok := nodes[id]
			if !ok {
				continue
			}
			rows := p.NodeRows[abs.id]
			for _, r := range rows {
				if err := ms.CloseMemory(r, nil); err != nil {
					return nil, fmt.Errorf("projection: absorb %s into %s: %w", id, n.id, err)
				}
			}
			if err := ms.Link(rows[0], relAbsorbedInto, survivor); err != nil {
				return nil, fmt.Errorf("projection: link absorbed %s: %w", id, err)
			}
		}
	}

	for _, evt := range capEvents {
		pl := payloadMap(evt)
		pane, _ := pl["pane_id"].(string)
		if pane == "" {
			pane = evt.ID
		}
		id, err := ms.Put(&memstore.Memory{
			Ts: evt.Timestamp, Kind: "cap", Scope: scope,
			Text: pane, Extra: extraOf(evt, pl),
		})
		if err != nil {
			return nil, fmt.Errorf("projection: cap row: %w", err)
		}
		p.CapRows = append(p.CapRows, id)
	}

	return p, nil
}

// extraOf is the origin envelope the cap ruled in: the event's id, project and
// commit, the node it names, and the raw payload — which is where every field
// memstore has no column for rides.
func extraOf(evt Event, payload map[string]any) string {
	env := make(map[string]any, len(payload)+4)
	env["event_id"] = evt.ID
	env["project_id"] = evt.ProjectID
	if evt.CommitSHA != nil {
		env["commit_sha"] = *evt.CommitSHA
	}
	if evt.NodeID != nil {
		env["fs_node"] = *evt.NodeID
	}
	for k, v := range payload {
		env[k] = v
	}
	b, err := json.Marshal(env)
	if err != nil {
		b, _ = json.Marshal(map[string]any{"event_id": evt.ID})
	}
	return string(b)
}

func payloadMap(evt Event) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(evt.Payload, &m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

// nodeKindOf, parseKindOf, validKindOf and legacyDispatchGoalOf mirror the kind
// resolution in query.BuildTree (query.nodeKind, query.parseKind, and their
// helpers), because query owns the rule and package store cannot import query:
// the projection must resolve exactly the kind BuildTree resolves.
//
// A raw valid kind wins; a goal fs dispatch wrote before nodes carried a kind
// reads as a dispatch; anything else is work.
func nodeKindOf(raw, createdGoal string) string {
	if validKindOf(raw) {
		return raw
	}
	if legacyDispatchGoalOf(createdGoal) {
		return "dispatch"
	}
	return "work"
}

func parseKindOf(raw string) string {
	if validKindOf(raw) {
		return raw
	}
	return "work"
}

func validKindOf(k string) bool {
	switch k {
	case "work", "dispatch", "gap", "batch":
		return true
	}
	return false
}

func legacyDispatchGoalOf(goal string) bool {
	rest, ok := strings.CutPrefix(goal, "dispatch ")
	if !ok {
		return false
	}
	typ, _, ok := strings.Cut(rest, ": ")
	return ok && typ != ""
}

// splitAbsorbed mirrors query.splitAbsorbed: node ids joined by commas, in the
// order they were folded in; last event states the whole list, so replay needs
// no accumulation.
func splitAbsorbed(joined string) []string {
	var ids []string
	for _, id := range strings.Split(joined, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func cloneString(s *string) *string {
	if s == nil {
		return nil
	}
	v := *s
	return &v
}
