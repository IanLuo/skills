// Projection: folding a project's events into memstore, so memstore's rows
// answer the same state questions query.BuildTree answers today.
//
// Slice 1 (card .agents/dispatch/fs-store-memstore.md) folded a whole stream
// into one store and nothing read it. Slice 2 (.agents/specs/fs-state-store.md)
// makes it a write path and a read path:
//
//   - The fold happens one event at a time, inside the append path
//     (Store.Append/AppendBatch), so every committed event is projected in the
//     same command step (D3, AC5).
//   - A row names the event it came from in its provenance columns —
//     Origin="fs", OriginId=<event id>, Ref=<commit>, FsNode=<node> — so a
//     re-fold is safe: Put refuses a duplicate (origin, origin_id) with
//     ErrDuplicateOrigin, and a fold treats that as already projected (D3,
//     AC10).
//   - A change to a node's displayed state writes a NEW row and closes the row
//     it replaces, because memstore's content is immutable and its only status
//     writer is CloseMemory (NFR Immutability, D3). A node's current row is its
//     newest row that is not "closed".
//
// The scope is built in exactly one place — ProjectionScope — as "fs:" plus the
// project id, so no projection write can construct a scope that is not fs's
// (D5, AC6).
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/IanLuo/memstore"
)

// projectionScopePrefix is the only scope literal fs writes or reads in the
// shared memory file. Every scope is this prefix plus a project id (D5).
const projectionScopePrefix = "fs:"

// projectionOrigin is the provenance origin of every projected row: the row
// names fs as its writer and its event as its origin id.
const projectionOrigin = "fs"

// rel* are the Link edges the projection writes. The names are the projection's
// own; memstore stores and walks them without interpreting them.
const (
	relDecision     = "decision"
	relKnowledge    = "knowledge"
	relReport       = "report"
	relBlock        = "block"
	relAbsorbedInto = "absorbed-into"
)

// Row kinds. The node kinds are memstore's own four (the row's Kind column is
// the node's structural kind, so a read filters nodes by kind without decoding
// anything); the rest are the projection's side rows.
const (
	rowProject   = "project"
	rowCap       = "cap"
	rowDelivery  = "delivery"
	rowReport    = "report"
	rowDecision  = "decision"
	rowKnowledge = "knowledge"
	rowBlock     = "block"
)

// closedStatus is memstore's one lifecycle state: CloseMemory writes it, and
// nothing else may.
const closedStatus = "closed"

// recentLimit is the row bound a scope read asks Recent for. memstore's Recent
// is the only scope-listing verb (A5) and takes a count, so a read asks for
// more rows than a scope can hold rather than paging.
const recentLimit = 1 << 20

// ProjectionScope is the only place a projection scope is built: "fs:" plus the
// project id, never a path and never a caller's string (D5, AC6).
func ProjectionScope(projectID string) string {
	return projectionScopePrefix + projectID
}

// ProjectionEnv is the variable that re-points the projection. It exists for
// tests and scratch files; production leaves it unset, so every line that says
// where a write's state went can also say whether this variable chose the file
// (skills:t-55266895).
const ProjectionEnv = "FS_MEMSTORE"

// ProjectionPathEnv is the value ProjectionSource reports when ProjectionEnv
// chose the path, and ProjectionSourceDefault when nothing did. They are the
// words the running log and `fs state verify` print, so both name the same fact.
const (
	ProjectionPathEnv       = ProjectionEnv
	ProjectionSourceDefault = "default"
)

// DefaultProjectionPath is where the projection lives when nothing overrides it:
// ~/.memstore/memory.db, the one file fs shares with personal memories.
func DefaultProjectionPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".memstore", "memory.db")
}

// ProjectionPath is where the projection lives for this process: $FS_MEMSTORE
// when set (tests, scratch files), otherwise DefaultProjectionPath.
func ProjectionPath() string {
	if p := os.Getenv(ProjectionEnv); p != "" {
		return p
	}
	return DefaultProjectionPath()
}

// ProjectionSource names what chose ProjectionPath: ProjectionPathEnv when the
// variable did, ProjectionSourceDefault otherwise.
func ProjectionSource() string {
	if os.Getenv(ProjectionEnv) != "" {
		return ProjectionPathEnv
	}
	return ProjectionSourceDefault
}

// OpenProjection opens the projection file at path, creating it and its parent
// directory if needed. An empty path is refused: a projection at "" is not a
// projection, and silently opening something else would be a different store.
func OpenProjection(path string) (*memstore.Store, error) {
	if path == "" {
		return nil, errors.New("projection: no path")
	}
	return memstore.Open(path)
}

// ProjectionFailure reports that an event was committed and its fold was not.
// The event is durable first and the fold second (NFR Ordering), so this is not
// a failed write: the action is recorded and only the projection is behind. The
// message says so and names the heal, because a user who retries the command
// appends a second event and doubles the state (AC10).
type ProjectionFailure struct {
	Scope string
	Err   error
}

func (e *ProjectionFailure) Error() string {
	return fmt.Sprintf("recorded; only the projection failed for %s — run \"fs state rebuild\": %v", e.Scope, e.Err)
}

func (e *ProjectionFailure) Unwrap() error { return e.Err }

// AttachProjection makes every later append fold into ms, in the same step as
// the append (D3). A nil store detaches it.
func (s *Store) AttachProjection(ms *memstore.Store) {
	s.proj = ms
}

// Projection returns the attached projection, or nil.
func (s *Store) Projection() *memstore.Store {
	return s.proj
}

// Projection is what one ProjectEvents call wrote, so a reader can walk the
// result. NodeRows maps each node to its rows, oldest first: the last element is
// the current row, and every earlier one is closed — superseded by a later
// version, or altogether when the node was absorbed by a merge.
type Projection struct {
	ProjectID string
	// ProjectMemory is the project-created row, or 0 when the stream has none.
	ProjectMemory int64
	// NodeRows maps each node to its rows, oldest first.
	NodeRows map[string][]int64
	// CapRows are the scope-level cap-claimed rows, in event order.
	CapRows []int64
}

// nodeDoc is a node version's resolved state, carried in its row's Extra. It is
// the whole of what a read needs from a node, so a read never replays the event
// store to answer: Goal, Kind and Status are the collected row's own columns,
// and the fields memstore has no column for ride here (D1).
type nodeDoc struct {
	Goal         string   `json:"goal"`
	Kind         string   `json:"kind"`
	ParentNodeID string   `json:"parent_node_id,omitempty"`
	FoundBy      string   `json:"found_by,omitempty"`
	Check        string   `json:"check,omitempty"`
	Integrates   string   `json:"integrates,omitempty"`
	Reviews      string   `json:"reviews,omitempty"`
	Severity     string   `json:"severity,omitempty"`
	BlockCheck   string   `json:"block_check,omitempty"`
	Absorbed     []string `json:"absorbed,omitempty"`
}

// rowExtra is the projection's envelope: the event's identity and provenance,
// the node document for a node row, and the event's raw payload unchanged. A
// field memstore has no column for (a block's check, a report's kind) is read
// from here (D1); the payload is what the event store already holds, nothing
// more.
type rowExtra struct {
	EventID   string          `json:"event_id"`
	ProjectID string          `json:"project_id"`
	CommitSHA string          `json:"commit_sha,omitempty"`
	FsNode    string          `json:"fs_node,omitempty"`
	EventType string          `json:"event_type"`
	Node      *nodeDoc        `json:"node,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// isNodeKind reports whether kind is one of the four structural node kinds —
// the row kinds that carry a node version.
func isNodeKind(kind string) bool {
	switch kind {
	case "work", "dispatch", "gap", "batch":
		return true
	}
	return false
}

// FoldEvent folds one event into the projection. It is the fold's only entry
// point: the append path calls it per event (a batch calls it N times, never
// once for the batch), and state rebuild calls it over a scope's stream.
//
// An event that is already projected — Put refused its (origin, origin_id) with
// ErrDuplicateOrigin — is a skip, not an error: that is what makes a re-fold
// over an already-folded prefix the heal (D3, AC10).
func FoldEvent(ms *memstore.Store, evt Event) error {
	return (&projector{ms: ms}).fold(evt)
}

// ProjectEvents folds a whole stream into one store, in event order. It is the
// scratch-store comparison's driver (D4) and the slice-1 entry point kept whole;
// the append path never calls it.
func ProjectEvents(ms *memstore.Store, projectID string, events []Event) (*Projection, error) {
	p := &projector{ms: ms}
	for i := range events {
		if err := p.fold(events[i]); err != nil {
			return nil, fmt.Errorf("projection: event %s: %w", events[i].ID, err)
		}
	}
	return summarise(ms, projectID)
}

// summarise reads back what a fold wrote, so a caller can walk the rows without
// the provenance columns.
func summarise(ms *memstore.Store, projectID string) (*Projection, error) {
	rows, err := scopeRows(ms, ProjectionScope(projectID))
	if err != nil {
		return nil, err
	}
	p := &Projection{ProjectID: projectID, NodeRows: make(map[string][]int64)}
	for _, m := range rows {
		switch {
		case m.Kind == rowProject && p.ProjectMemory == 0:
			p.ProjectMemory = m.Id
		case m.Kind == rowCap:
			p.CapRows = append(p.CapRows, m.Id)
		case isNodeKind(m.Kind) && m.FsNode != "":
			p.NodeRows[m.FsNode] = append(p.NodeRows[m.FsNode], m.Id)
		}
	}
	return p, nil
}

// scopeRows returns one scope's live rows, oldest first. It is the projection's
// one read primitive: memstore's Recent is the only scope-listing verb (A5), so
// every projection read filters on a scope the event store named (D5).
func scopeRows(ms *memstore.Store, scope string) ([]memstore.Memory, error) {
	rows, err := ms.Recent(scope, recentLimit)
	if err != nil {
		return nil, fmt.Errorf("projection: read scope %s: %w", scope, err)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Id < rows[j].Id })
	return rows, nil
}

// projector folds one event at a time into a projection file.
type projector struct {
	ms *memstore.Store
}

// fold is the mapping, one event in and the rows it implies out.
//
//   - project-created    one row per project: Kind=project, Text=root_path
//   - cap-claimed        a scope-level row per event: Kind=cap, Text=pane_id
//   - plan-recorded      nothing: no switched read asks it, and close's plan
//     read stays on the event store (D3)
//   - task-created       a node version row, Kind=<the node's kind>,
//     Text=goal, Parent=<the parent's current row>
//   - status-changed     a new node version row with the new status; the row it
//     replaces is closed
//   - metadata-changed   goal/kind/severity/absorbed: a new node version row
//     with the corrected field (memstore content is
//     immutable, so a correction is a new version); an
//     unknown field folds to nothing, as in BuildTree.
//     field=absorbed also closes the absorbed node's rows
//     and links it to the survivor — the cross-node effect
//     the one event carries
//   - task-blocked       a node version row carrying the block's check, plus the
//     block record linked from the node
//   - task-unblocked     a node version row with the block's check cleared (the
//     paired status-changed carries the state)
//   - decision-recorded / knowledge-added / report-recorded
//     their own rows, linked from the node
//   - delivery-recorded  its own row carrying the payload in Extra
//
// Every row writes the event's identity into its provenance columns, so a row
// names the event it came from instead of hiding that identity in Extra.
func (p *projector) fold(evt Event) error {
	scope := ProjectionScope(evt.ProjectID)

	switch evt.Type {
	case ProjectCreated:
		root := payloadString(evt, "root_path")
		if root == "" {
			root = evt.ProjectID
		}
		_, _, err := p.put(evt, scope, rowProject, root, "", nil, nil, evt.ID)
		return err
	case CapClaimed:
		pane := payloadString(evt, "pane_id")
		if pane == "" {
			pane = evt.ID
		}
		_, _, err := p.put(evt, scope, rowCap, pane, "", nil, nil, evt.ID)
		return err
	case PlanRecorded:
		return nil
	}

	if evt.NodeID == nil {
		return nil
	}
	nid := *evt.NodeID

	switch evt.Type {
	case TaskCreated:
		goal := payloadString(evt, "goal")
		doc := nodeDoc{
			Goal:       goal,
			Kind:       nodeKindOf(payloadString(evt, "kind"), goal),
			FoundBy:    payloadString(evt, "found_by"),
			Check:      payloadString(evt, "check"),
			Integrates: payloadString(evt, "integrates"),
			Reviews:    payloadString(evt, "reviews"),
			Severity:   payloadString(evt, "severity"),
		}
		if evt.ParentNodeID != nil {
			doc.ParentNodeID = *evt.ParentNodeID
		}
		// The Parent column is a row id: it names the parent's current row, so a
		// walk from the parent reaches the child.
		var parent *int64
		if doc.ParentNodeID != "" {
			if prow, _, _, err := p.state(scope, doc.ParentNodeID); err != nil {
				return err
			} else if prow != nil {
				v := prow.Id
				parent = &v
			}
		}
		return p.putVersion(evt, scope, nid, doc, "pending", parent, nil)

	case StatusChanged:
		prev, doc, status, err := p.state(scope, nid)
		if err != nil {
			return err
		}
		to := payloadString(evt, "to")
		if to == "" {
			to = status
		}
		return p.putVersion(evt, scope, nid, doc, to, nil, prev)

	case MetadataChanged:
		prev, doc, status, err := p.state(scope, nid)
		if err != nil {
			return err
		}
		field, value := payloadString(evt, "field"), payloadString(evt, "new_value")
		switch field {
		case "goal":
			doc.Goal = value
		case "kind":
			doc.Kind = parseKindOf(value)
			if !isNodeKind(doc.Kind) {
				doc.Kind, doc.Severity = "work", ""
			}
		case "severity":
			doc.Severity = parseSeverityOf(value)
		case "absorbed":
			doc.Absorbed = splitAbsorbed(value)
		default:
			return nil
		}
		if err := p.putVersion(evt, scope, nid, doc, status, nil, prev); err != nil {
			return err
		}
		if field == "absorbed" {
			return p.absorb(scope, nid, doc.Absorbed)
		}
		return nil

	case TaskBlocked:
		prev, doc, status, err := p.state(scope, nid)
		if err != nil {
			return err
		}
		doc.BlockCheck = payloadString(evt, "check")
		if err := p.putVersion(evt, scope, nid, doc, status, nil, prev); err != nil {
			return err
		}
		// The record of the block is its own row, linked from the node. Its
		// origin id carries a role suffix so both rows of this one event carry
		// provenance: (origin, origin_id) is unique per row, not per event.
		id, skip, err := p.put(evt, scope, rowBlock, payloadString(evt, "reason"), "", nil, nil, evt.ID+"#block")
		if err != nil || skip {
			return err
		}
		return p.link(scope, nid, relBlock, id)

	case TaskUnblocked:
		prev, doc, status, err := p.state(scope, nid)
		if err != nil {
			return err
		}
		doc.BlockCheck = ""
		return p.putVersion(evt, scope, nid, doc, status, nil, prev)

	case DecisionRecorded:
		id, skip, err := p.put(evt, scope, rowDecision, payloadString(evt, "summary"), "", nil, nil, evt.ID)
		if err != nil || skip {
			return err
		}
		return p.link(scope, nid, relDecision, id)

	case KnowledgeAdded:
		id, skip, err := p.put(evt, scope, rowKnowledge, payloadString(evt, "summary"), "", nil, nil, evt.ID)
		if err != nil || skip {
			return err
		}
		return p.link(scope, nid, relKnowledge, id)

	case ReportRecorded:
		line := payloadString(evt, "line")
		id, skip, err := p.put(evt, scope, rowReport, line, "", nil, nil, evt.ID)
		if err != nil || skip {
			return err
		}
		return p.link(scope, nid, relReport, id)

	case DeliveryRecorded:
		pane := payloadString(evt, "pane_id")
		if pane == "" {
			pane = evt.ID
		}
		_, _, err := p.put(evt, scope, rowDelivery, pane, "", nil, nil, evt.ID)
		return err
	}
	return nil
}

// put writes one row and reports whether it was already there. The second
// return is the re-fold skip: a row whose (origin, origin_id) is taken is
// already projected, and that is not an error (D3, AC10).
func (p *projector) put(evt Event, scope, kind, text, status string, parent *int64, doc *nodeDoc, originID string) (int64, bool, error) {
	if text == "" {
		// A row needs non-empty text. A node row an event names without a
		// task-created has no goal to carry, so it carries its id — which is what
		// slice 1's comparison expects — while the node document keeps the empty
		// goal BuildTree derives.
		if evt.NodeID != nil {
			text = *evt.NodeID
		} else {
			text = kind
		}
	}
	extra, err := json.Marshal(rowExtra{
		EventID:   evt.ID,
		ProjectID: evt.ProjectID,
		CommitSHA: derefString(evt.CommitSHA),
		FsNode:    derefString(evt.NodeID),
		EventType: string(evt.Type),
		Node:      doc,
		Payload:   evt.Payload,
	})
	if err != nil {
		return 0, false, fmt.Errorf("projection: encode row for %s: %w", originID, err)
	}
	id, err := p.ms.Put(&memstore.Memory{
		Ts:       evt.Timestamp,
		Kind:     kind,
		Scope:    scope,
		Text:     text,
		Extra:    string(extra),
		Status:   status,
		Parent:   parent,
		Origin:   projectionOrigin,
		OriginId: originID,
		Ref:      derefString(evt.CommitSHA),
		FsNode:   derefString(evt.NodeID),
	})
	if errors.Is(err, memstore.ErrDuplicateOrigin) {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("projection: %s row %s: %w", kind, originID, err)
	}
	return id, false, nil
}

// putVersion writes one node version row and retires the rows a fresh in-order
// fold would have retired (see rechain). The retire is memstore's correction
// rule: content is immutable, so a change is a new version and the old one
// retires (D3).
func (p *projector) putVersion(evt Event, scope, nid string, doc nodeDoc, status string, parent *int64, prev *memstore.Memory) error {
	if parent == nil && prev != nil {
		parent = prev.Parent
	}
	if _, _, err := p.put(evt, scope, doc.Kind, doc.Goal, status, parent, &doc, evt.ID); err != nil {
		return err
	}
	return p.rechain(scope, nid)
}

// rechain retires every row of a node that a fresh in-order fold would have
// retired: each row that has a successor in event order, plus every row of a
// node that was absorbed by a merge.
//
// It runs after EVERY node-version fold, insert or skip, and that is the whole
// point. Two holes are otherwise unhealable, and both were seen live:
//
//   - a fold killed between its insert and its close leaves the older row open
//     while the newer row exists; every later re-fold skips at the same place
//     (the row is already there) and retires nothing;
//   - a fold of an OLDER event — a lag being filled by `fs state rebuild` —
//     arrives when a newer row already exists, so the newer row cannot be the
//     thing it retires, and retiring only the current row leaves the row between
//     them open.
//
// Recomputing from the whole set fixes both, because the expected closed-ness is
// a function of the row set and not of the order the rows arrived in. Resolving
// rows needs no new memstore verb: nodeRows is the same scope-listing read
// link() already uses.
//
// The supersede pointer is set from the successor in event order, which is what
// an in-order fold writes; a row already closed keeps the pointer it has, since
// memstore's close is a no-op on a closed row. Verify compares closed-ness and
// never the pointer, for exactly that reason.
func (p *projector) rechain(scope, nid string) error {
	rows, err := p.nodeRows(scope, nid)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	ordered := make([]memstore.Memory, len(rows))
	copy(ordered, rows)
	sort.SliceStable(ordered, func(i, j int) bool {
		return eventOrderOf(ordered[i]) < eventOrderOf(ordered[j])
	})

	// A node whose rows were closed with no replacement was absorbed: the merge
	// closed every version of it, including the newest.
	absorbed := false
	for _, m := range ordered {
		if m.Status == closedStatus && m.Supersedes == nil {
			absorbed = true
		}
	}
	for i, m := range ordered {
		if m.Status == closedStatus {
			continue
		}
		switch {
		case i < len(ordered)-1:
			next := ordered[i+1].Id
			if err := p.ms.CloseMemory(m.Id, &next); err != nil {
				return fmt.Errorf("projection: close superseded row %d: %w", m.Id, err)
			}
		case absorbed:
			if err := p.ms.CloseMemory(m.Id, nil); err != nil {
				return fmt.Errorf("projection: close absorbed row %d: %w", m.Id, err)
			}
		}
	}
	return nil
}

// link hangs a side row off the node's first row, so a node's outgoing set does
// not move when a later version adds a row.
func (p *projector) link(scope, nid, rel string, to int64) error {
	rows, err := p.nodeRows(scope, nid)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	if err := p.ms.Link(rows[0].Id, rel, to); err != nil {
		return fmt.Errorf("projection: link %s %s->%d: %w", rel, nid, to, err)
	}
	return nil
}

// absorb applies a merge's cross-node effect: the absorbed node's rows are
// closed and its first row links to the survivor. A full address names an
// absorbee in another scope, whose rows are not in this scope's rows; that half
// of a cross-scope merge is not closed here.
func (p *projector) absorb(scope, survivor string, ids []string) error {
	srows, err := p.nodeRows(scope, survivor)
	if err != nil {
		return err
	}
	if len(srows) == 0 {
		return nil
	}
	survivorRow := srows[len(srows)-1].Id
	for _, id := range ids {
		if strings.Contains(id, ":") {
			continue
		}
		rows, err := p.nodeRows(scope, id)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if err := p.ms.CloseMemory(r.Id, nil); err != nil {
				return fmt.Errorf("projection: absorb %s into %s: %w", id, survivor, err)
			}
		}
		if len(rows) == 0 {
			continue
		}
		if err := p.ms.Link(rows[0].Id, relAbsorbedInto, survivorRow); err != nil {
			return fmt.Errorf("projection: link absorbed %s: %w", id, err)
		}
	}
	return nil
}

// nodeRows returns a node's version rows, oldest first.
func (p *projector) nodeRows(scope, nid string) ([]memstore.Memory, error) {
	rows, err := scopeRows(p.ms, scope)
	if err != nil {
		return nil, err
	}
	out := make([]memstore.Memory, 0, 4)
	for _, m := range rows {
		if m.Kind != rowProject && isNodeKind(m.Kind) && m.FsNode == nid {
			out = append(out, m)
		}
	}
	return out, nil
}

// state returns a node's current row and its resolved document, so a fold that
// changes one field carries the rest forward. A node with no row yet has no
// previous row and a document built from its id, which is how a node an event
// names without a task-created is materialized — the same node set BuildTree
// materializes. A node whose rows are all closed was absorbed: its last row is
// its state and its status is done, which is what the merge wrote before the
// close retired the row.
func (p *projector) state(scope, nid string) (*memstore.Memory, nodeDoc, string, error) {
	rows, err := p.nodeRows(scope, nid)
	if err != nil {
		return nil, nodeDoc{}, "", err
	}
	if len(rows) == 0 {
		// A node an event names without a task-created has no goal to carry:
		// BuildTree materializes it with an empty goal, and the projection must
		// answer the same, so the document says nothing rather than inventing the
		// id as prose (the row's Text has to be non-empty, and that is the
		// column's requirement, not a claim about the node).
		return nil, nodeDoc{Kind: "work"}, "pending", nil
	}
	cur, status := rows[len(rows)-1], "done"
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Status != closedStatus {
			cur, status = rows[i], rows[i].Status
			break
		}
	}
	doc := nodeDoc{Kind: "work"}
	if got := docOf(cur.Extra); got != nil {
		doc = *got
	}
	if !isNodeKind(doc.Kind) {
		doc.Kind = "work"
	}
	if status == "" || status == closedStatus {
		status = "pending"
	}
	return &cur, doc, status, nil
}

// docOf decodes a row's node document, or nil when the row is not a node.
func docOf(extra string) *nodeDoc {
	env, err := decodeExtra(extra)
	if err != nil {
		return nil
	}
	return env.Node
}

// derefString reads a nil pointer as the empty string.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// payloadString reads one string field of an event's payload.
func payloadString(evt Event, key string) string {
	var m map[string]any
	if err := json.Unmarshal(evt.Payload, &m); err != nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

// nodeKindOf, parseKindOf, parseSeverityOf, validKindOf and
// legacyDispatchGoalOf mirror the kind resolution in query.BuildTree
// (query.nodeKind, query.parseKind, query.parseSeverity), because query owns the
// rule and package store cannot import query: the projection must resolve
// exactly the kind BuildTree resolves.
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

// parseSeverityOf mirrors query.parseSeverity: an unknown or absent severity is
// not a fourth level, it is a gap nobody has weighed.
func parseSeverityOf(raw string) string {
	switch raw {
	case "severe", "defect", "prose":
		return raw
	}
	return ""
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
// order they were folded in; the last event states the whole list, so replay
// needs no accumulation.
func splitAbsorbed(joined string) []string {
	var ids []string
	for _, id := range strings.Split(joined, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// decodeExtra decodes a row's envelope.
func decodeExtra(extra string) (rowExtra, error) {
	var env rowExtra
	if err := json.Unmarshal([]byte(extra), &env); err != nil {
		return rowExtra{}, err
	}
	return env, nil
}

// decodeInto decodes an event payload into the shape the caller wrote.
func decodeInto(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, into)
}
