// Drift: comparing the projection against the event store, and repairing it.
//
// The event store is the truth and the projection is derived, so a divergence
// is a defect in the fold, the mapping or the read — never a preference. Two
// commands keep that visible and repairable (D4):
//
//   - StateVerify re-folds every scope into a SCRATCH projection file and
//     compares it to the live one. It writes nothing to the live file and never
//     repairs.
//   - StateRebuild re-folds every scope into the LIVE file: it forgets that
//     scope's rows and folds its events again, touching no non-fs: row.
//
// Both compare the same canonical form — every row of a scope as a document
// keyed on its provenance, so a deleted, added or changed row is one named
// difference rather than a length someone has to diff by hand.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/IanLuo/memstore"
)

// JSONDiff is the first field at which two canonical encodings differ, with
// both values. The field is a path — "dispatches[0].node.status",
// "rows[7].status" — because a difference named without its place is a
// difference a reader has to find again.
type JSONDiff struct {
	Field string
	A     string
	B     string
}

// DiffJSON compares two values by their canonical JSON encodings: encoding/json
// orders map keys, so two equal values encode identically and the comparison is
// byte-for-byte. It reports the first difference, or nil when they agree.
func DiffJSON(a, b any) (*JSONDiff, error) {
	av, err := canonical(a)
	if err != nil {
		return nil, err
	}
	bv, err := canonical(b)
	if err != nil {
		return nil, err
	}
	return diffValue("", av, bv), nil
}

func canonical(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func diffValue(path string, a, b any) *JSONDiff {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			return &JSONDiff{Field: path, A: encode(a), B: encode(b)}
		}
		for _, k := range unionKeys(av, bv) {
			child := path + "." + k
			if path == "" {
				child = k
			}
			if d := diffValue(child, av[k], bv[k]); d != nil {
				return d
			}
		}
		return nil
	case []any:
		bv, ok := b.([]any)
		if !ok {
			return &JSONDiff{Field: path, A: encode(a), B: encode(b)}
		}
		for i := 0; i < len(av) || i < len(bv); i++ {
			child := fmt.Sprintf("%s[%d]", path, i)
			if i >= len(av) || i >= len(bv) {
				return &JSONDiff{Field: child, A: encode(index(av, i)), B: encode(index(bv, i))}
			}
			if d := diffValue(child, av[i], bv[i]); d != nil {
				return d
			}
		}
		return nil
	}
	if !reflect.DeepEqual(a, b) {
		return &JSONDiff{Field: path, A: encode(a), B: encode(b)}
	}
	return nil
}

func index(s []any, i int) any {
	if i >= len(s) {
		return nil
	}
	return s[i]
}

func unionKeys(a, b map[string]any) []string {
	seen := make(map[string]bool, len(a)+len(b))
	keys := make([]string, 0, len(a)+len(b))
	for _, m := range []map[string]any{a, b} {
		for k := range m {
			if seen[k] {
				continue
			}
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// encode renders a value for a difference message: its JSON, or "<missing>"
// when the field is absent from one side.
func encode(v any) string {
	if v == nil {
		return "<missing>"
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}

// StateIssue is one scope's divergence between the projection and the event
// store, as StateVerify reports it.
type StateIssue struct {
	Scope      string
	Field      string
	EventStore string
	Projection string
	// Cause is a sentence naming the divergence itself, for the differences that
	// are not one field with two values: rows one side holds and the other does
	// not, a row retired on one side only, a node present on one side only. It
	// leads the message because a positional field path answers "which index"
	// when the question is "what is wrong": the live red of skills:t-55266895
	// read "side_rows[68].fs_node: t-abf680c9 vs t-beec6a55" and named a node that
	// was never wrong, while the cause was five rows the projection never
	// received and the index had shifted under them.
	Cause string
}

func (i StateIssue) String() string {
	if i.Cause != "" {
		return fmt.Sprintf("scope %s: %s", i.Scope, i.Cause)
	}
	return fmt.Sprintf("scope %s differs at %s: the event store says %s, the projection says %s",
		i.Scope, i.Field, i.EventStore, i.Projection)
}

// scopeState is a scope's derived state as both sides compare it: what a reader
// gets, with no row id and nothing else that is local to one file. It is the
// comparison D4 asks for — "each scope's derived state" — and it is deliberately
// not a row-by-row comparison: a row written out of order carries a snapshot
// that could not inherit from a predecessor it never saw, while the state
// derived from its payload is the same either way.
type scopeState struct {
	ProjectID string      `json:"project_id"`
	RootPath  string      `json:"root_path,omitempty"`
	Nodes     []nodeState `json:"nodes"`
	SideRows  []rowState  `json:"side_rows"`
}

// nodeState is one node's derived state, without the file-local row id.
type nodeState struct {
	NodeID     string   `json:"node_id"`
	Goal       string   `json:"goal"`
	Kind       string   `json:"kind"`
	Status     string   `json:"status"`
	ParentID   string   `json:"parent_id,omitempty"`
	FoundBy    string   `json:"found_by,omitempty"`
	Check      string   `json:"check,omitempty"`
	BlockCheck string   `json:"block_check,omitempty"`
	Severity   string   `json:"severity,omitempty"`
	Integrates string   `json:"integrates,omitempty"`
	Reviews    string   `json:"reviews,omitempty"`
	Absorbed   []string `json:"absorbed,omitempty"`
}

// rowState is one side row as both sides compare it: the event it came from,
// its node, and the payload the event carried — which is the row's whole content
// and never inherited.
type rowState struct {
	Kind     string `json:"kind"`
	FsNode   string `json:"fs_node,omitempty"`
	OriginID string `json:"origin_id"`
	Ts       string `json:"ts"`
	Payload  any    `json:"payload,omitempty"`
}

// derivedScope renders a scope's derived state canonically.
func derivedScope(ms *memstore.Store, projectID string) (*scopeState, error) {
	scope, err := ReadScope(ms, projectID)
	if err != nil {
		return nil, err
	}
	out := &scopeState{ProjectID: projectID, RootPath: scope.RootPath, Nodes: []nodeState{}, SideRows: []rowState{}}
	for _, nid := range scope.NodeIDs {
		n := scope.Nodes[nid]
		out.Nodes = append(out.Nodes, nodeState{
			NodeID: n.NodeID, Goal: n.Goal, Kind: n.Kind, Status: n.Status,
			ParentID: n.ParentID, FoundBy: n.FoundBy, Check: n.Check,
			BlockCheck: n.BlockCheck, Severity: n.Severity,
			Integrates: n.Integrates, Reviews: n.Reviews, Absorbed: n.Absorbed,
		})
	}
	for _, group := range [][]memstore.Memory{scope.CapRows, scope.Deliveries, scope.Reports, scope.Blocks} {
		for _, m := range group {
			var payload any
			if env, err := decodeExtra(m.Extra); err == nil {
				_ = json.Unmarshal(env.Payload, &payload)
			}
			out.SideRows = append(out.SideRows, rowState{
				Kind: m.Kind, FsNode: m.FsNode, OriginID: m.OriginId,
				Ts: m.Ts.UTC().Format(time.RFC3339Nano), Payload: payload,
			})
		}
	}
	sort.Slice(out.SideRows, func(i, j int) bool {
		a, b := out.SideRows[i], out.SideRows[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.FsNode != b.FsNode {
			return a.FsNode < b.FsNode
		}
		return a.OriginID < b.OriginID
	})
	return out, nil
}

// stateRow is one projected row as presence is compared: the event it names, the
// kind and node it belongs to, and whether it is retired. Presence is compared
// before content and keyed on this identity rather than on a position, so a row
// the projection never received names itself instead of shifting a list.
type stateRow struct {
	OriginID string `json:"origin_id"`
	Kind     string `json:"kind,omitempty"`
	FsNode   string `json:"fs_node,omitempty"`
	Closed   bool   `json:"closed"`
}

// rowsByProvenance lists one scope's rows by the event each names, and whether it
// is retired, sorted by that identity. It is how verify sees a row that should
// not be there and a row that should be and is not, without comparing content a
// re-fold is allowed to differ on.
func rowsByProvenance(ms *memstore.Store, projectID string) ([]stateRow, error) {
	rows, err := scopeRows(ms, ProjectionScope(projectID))
	if err != nil {
		return nil, err
	}
	out := make([]stateRow, 0, len(rows))
	for _, m := range rows {
		out = append(out, stateRow{
			OriginID: m.OriginId, Kind: m.Kind, FsNode: m.FsNode,
			Closed: m.Status == closedStatus,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OriginID < out[j].OriginID })
	return out, nil
}

// StateVerify compares the live projection with a scratch re-fold of the event
// store, scope by scope. It writes nothing to the live file: the scratch store
// is a temp file, removed before it returns.
//
// Three things must hold, and each is what a reader actually relies on:
//
//  1. the derived state is equal — the answers a read gives are the event
//     store's answers;
//  2. every row names an event of the scope and every event of the scope has a
//     row, and each row's retired state is the one a fresh fold gives it — so
//     "verify clean" is what a rebuild would produce, and a stray row or a row
//     the fold never closed is named;
//  3. no fs: scope exists in the projection that the event store does not name,
//     because a rebuild removes such a scope and a verify that missed it would
//     be certifying something a repair would change.
//
// A difference is reported by what it IS — rows one side holds and the other
// does not, then a field keyed on the row's identity — never as the first index
// that differs, because an index answers a question nobody asked and hides the
// one they did (skills:t-55266895).

// VerifyReport says where StateVerify looked: the live projection it read and the
// scratch re-fold it compared it with, plus the scopes it walked. A "clean"
// answer names both files, so it is never ambiguous which projection is clean.
type VerifyReport struct {
	Live    string
	Scratch string
	Scopes  []string
}

func (s *Store) StateVerify(projPath string) ([]StateIssue, VerifyReport, error) {
	scopes, err := s.Scopes()
	if err != nil {
		return nil, VerifyReport{}, err
	}
	live, closeLive, err := s.openProjection(projPath)
	if err != nil {
		return nil, VerifyReport{}, err
	}
	defer closeLive()
	scratch, scratchPath, cleanup, err := scratchProjection()
	if err != nil {
		return nil, VerifyReport{}, err
	}
	defer cleanup()
	report := VerifyReport{Live: projPath, Scratch: scratchPath, Scopes: scopes}

	var issues []StateIssue
	known := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		known[ProjectionScope(scope)] = true
		events, err := s.Replay(scope, nil)
		if err != nil {
			return nil, report, fmt.Errorf("state verify: replay %s: %w", scope, err)
		}
		for i := range events {
			if err := FoldEvent(scratch, events[i]); err != nil {
				return nil, report, fmt.Errorf("state verify: fold %s: %w", scope, err)
			}
		}
		if issue := compareScope(scratch, live, scope); issue != nil {
			issues = append(issues, *issue)
		}
	}

	stray, err := projectionScopes(live)
	if err != nil {
		return nil, report, err
	}
	for _, scope := range stray {
		if known[scope] {
			continue
		}
		issues = append(issues, StateIssue{
			Scope: scope, Field: "scope",
			EventStore: "<missing>", Projection: "the scope holds rows the event store does not name",
		})
	}
	return issues, report, nil
}

// compareScope compares one scope's rows and derived state, and returns the
// first difference, or nil when the scope agrees.
//
// Presence is compared first, and by the event each row names rather than by its
// position. A row the projection never received is the cause a repair fixes;
// walked by position it surfaces as the first index that differs and names a
// field that was never wrong — the live red of skills:t-55266895 said
// "side_rows[68].fs_node: t-abf680c9 vs t-beec6a55", while five events had no row
// at all and the index had shifted under them. Content is compared only once the
// row sets agree, and then keyed on the same identity: `nodes[t-abf680c9].status`
// is a place a reader can go back to, `nodes[66].status` is not.
func compareScope(scratch, live *memstore.Store, scope string) *StateIssue {
	wantRows, err := rowsByProvenance(scratch, scope)
	if err != nil {
		return &StateIssue{Scope: scope, Field: "rows", EventStore: err.Error()}
	}
	gotRows, err := rowsByProvenance(live, scope)
	if err != nil {
		return &StateIssue{Scope: scope, Field: "rows", Projection: err.Error()}
	}
	if issue := comparePresence(scope, wantRows, gotRows); issue != nil {
		return issue
	}

	want, err := derivedScope(scratch, scope)
	if err != nil {
		return &StateIssue{Scope: scope, Field: "read", EventStore: err.Error()}
	}
	got, err := derivedScope(live, scope)
	if err != nil {
		return &StateIssue{Scope: scope, Field: "read", Projection: err.Error()}
	}
	if want.RootPath != got.RootPath {
		return &StateIssue{Scope: scope, Field: "root_path",
			EventStore: encode(want.RootPath), Projection: encode(got.RootPath)}
	}
	if issue := compareNodes(scope, want.Nodes, got.Nodes); issue != nil {
		return issue
	}
	return compareSideRows(scope, want.SideRows, got.SideRows)
}

// comparePresence names the rows one side holds and the other does not, and the
// shared rows whose retired state differs. These are the differences a repair
// fixes, and the ones a positional walk misreports.
func comparePresence(scope string, want, got []stateRow) *StateIssue {
	wantBy := indexRows(want)
	gotBy := indexRows(got)

	var missing, extra []stateRow
	for _, r := range want {
		if _, ok := gotBy[r.OriginID]; !ok {
			missing = append(missing, r)
		}
	}
	for _, r := range got {
		if _, ok := wantBy[r.OriginID]; !ok {
			extra = append(extra, r)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		return &StateIssue{Scope: scope, Field: "rows", Cause: presenceCause(missing, extra)}
	}

	for _, r := range want {
		other := gotBy[r.OriginID]
		if r.Closed == other.Closed {
			continue
		}
		return &StateIssue{Scope: scope, Field: "rows", Cause: fmt.Sprintf(
			"row %s is %s in the event store and %s in the projection",
			rowLabel(r), retired(r.Closed), retired(other.Closed))}
	}
	return nil
}

func indexRows(rows []stateRow) map[string]stateRow {
	out := make(map[string]stateRow, len(rows))
	for _, r := range rows {
		out[r.OriginID] = r
	}
	return out
}

func retired(closed bool) string {
	if closed {
		return "closed"
	}
	return "open"
}

// presenceCause says which side holds rows the other does not, and names them.
func presenceCause(missing, extra []stateRow) string {
	var a, b string
	if len(missing) > 0 {
		a = absentPhrase(len(missing), "row", "event store", "projection", describeRows(missing))
	}
	if len(extra) > 0 {
		b = absentPhrase(len(extra), "row", "projection", "event store", describeRows(extra))
	}
	return joinCauses(a, b)
}

func joinCauses(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + ", and " + b
}

// absentPhrase reads "1 row present in the event store is absent from the
// projection (kind/id on node)": the count decides the noun and the verb, so a
// one-row difference reads as one row.
func absentPhrase(count int, noun, side, other, items string) string {
	subject, verb := fmt.Sprintf("%d %ss", count, noun), "are"
	if count == 1 {
		subject, verb = "1 "+noun, "is"
	}
	return fmt.Sprintf("%s present in the %s %s absent from the %s (%s)", subject, side, verb, other, items)
}

// listLimit bounds how many rows or nodes one message names: enough to identify
// the drift, never a wall of ids.
const listLimit = 5

// describeRows renders rows as "kind/id on node", bounded by listLimit.
func describeRows(rows []stateRow) string {
	labels := make([]string, 0, listLimit+1)
	for i, r := range rows {
		if i == listLimit {
			labels = append(labels, fmt.Sprintf("and %d more", len(rows)-listLimit))
			break
		}
		labels = append(labels, rowLabel(r))
	}
	return strings.Join(labels, ", ")
}

func rowLabel(r stateRow) string {
	switch {
	case r.Kind == "" && r.FsNode == "":
		return r.OriginID
	case r.FsNode == "":
		return r.Kind + "/" + r.OriginID
	}
	return fmt.Sprintf("%s/%s on %s", r.Kind, r.OriginID, r.FsNode)
}

// compareNodes compares the derived nodes by node id: which nodes each side
// holds, then each shared node's fields, named by the node they belong to.
func compareNodes(scope string, want, got []nodeState) *StateIssue {
	wantBy := make(map[string]nodeState, len(want))
	for _, n := range want {
		wantBy[n.NodeID] = n
	}
	gotBy := make(map[string]nodeState, len(got))
	for _, n := range got {
		gotBy[n.NodeID] = n
	}

	var missing, extra []string
	for _, n := range want {
		if _, ok := gotBy[n.NodeID]; !ok {
			missing = append(missing, n.NodeID)
		}
	}
	for _, n := range got {
		if _, ok := wantBy[n.NodeID]; !ok {
			extra = append(extra, n.NodeID)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		return &StateIssue{Scope: scope, Field: "nodes", Cause: nodePresenceCause(missing, extra)}
	}

	for _, n := range want {
		diff, err := DiffJSON(n, gotBy[n.NodeID])
		if err != nil {
			return &StateIssue{Scope: scope, Field: "nodes[" + n.NodeID + "]", EventStore: err.Error()}
		}
		if diff != nil {
			return &StateIssue{Scope: scope,
				Field:      fieldPath("nodes["+n.NodeID+"]", diff.Field),
				EventStore: diff.A, Projection: diff.B}
		}
	}
	return nil
}

// nodePresenceCause says which side holds nodes the other does not.
func nodePresenceCause(missing, extra []string) string {
	var a, b string
	if len(missing) > 0 {
		a = absentPhrase(len(missing), "node", "event store", "projection", describeNames(missing))
	}
	if len(extra) > 0 {
		b = absentPhrase(len(extra), "node", "projection", "event store", describeNames(extra))
	}
	return joinCauses(a, b)
}

// describeNames renders node ids, bounded by listLimit.
func describeNames(names []string) string {
	if len(names) > listLimit {
		body := strings.Join(names[:listLimit], ", ")
		return fmt.Sprintf("%s, and %d more", body, len(names)-listLimit)
	}
	return strings.Join(names, ", ")
}

// compareSideRows compares the side rows (cap claims, deliveries, reports,
// blocks) by the event each names, so a difference names the row.
func compareSideRows(scope string, want, got []rowState) *StateIssue {
	wantBy := make(map[string]rowState, len(want))
	for _, r := range want {
		wantBy[sideKey(r)] = r
	}
	gotBy := make(map[string]rowState, len(got))
	for _, r := range got {
		gotBy[sideKey(r)] = r
	}

	for _, r := range want {
		key := sideKey(r)
		other, ok := gotBy[key]
		if !ok {
			return &StateIssue{Scope: scope, Field: "side_rows[" + key + "]",
				EventStore: encode(r), Projection: "<missing>"}
		}
		diff, err := DiffJSON(r, other)
		if err != nil {
			return &StateIssue{Scope: scope, Field: "side_rows[" + key + "]", EventStore: err.Error()}
		}
		if diff != nil {
			return &StateIssue{Scope: scope,
				Field:      fieldPath("side_rows["+key+"]", diff.Field),
				EventStore: diff.A, Projection: diff.B}
		}
	}
	for _, r := range got {
		if _, ok := wantBy[sideKey(r)]; !ok {
			return &StateIssue{Scope: scope, Field: "side_rows[" + sideKey(r) + "]",
				EventStore: "<missing>", Projection: encode(r)}
		}
	}
	return nil
}

// sideKey is a side row's identity: its kind and the event it names, because one
// event can write rows of different kinds.
func sideKey(r rowState) string { return r.Kind + "/" + r.OriginID }

// fieldPath joins a row's identity path to the field that differs.
func fieldPath(base, field string) string {
	if field == "" {
		return base
	}
	return base + "." + field
}

// StateRebuild re-folds the event store into the live projection file, and
// removes what does not belong there.
//
// The order is load-bearing, and it is not the order the spec's D4 sketched.
// D4 said "delete that scope's rows and re-fold" — but memstore's only deletion
// verb is Forget, a soft delete, and a row's (origin, origin_id) stays taken
// after it (Put's duplicate check and the partial unique index both ignore
// deleted rows). Forgetting first would therefore make the re-fold skip every
// event it was meant to restore, and a rebuild of a populated scope would empty
// it. So a rebuild:
//
//  1. folds the scope's events again — every row already there is a skip, every
//     missing one is written, which is the heal for a fold that failed or never
//     ran (the lagging direction the ordering NFR guarantees);
//  2. forgets the rows in that scope whose provenance names no event of the
//     stream — a row left by an older mapping, or one nobody can explain;
//  3. forgets fs: scopes the event store no longer names.
//
// What it cannot repair, and why: a row whose CONTENT is wrong for its event
// keeps its key, so the re-fold skips it and the wrong row stands; and a row
// that has been forgotten cannot be restored, because the key is taken. Both
// need a memstore verb fs does not have — a hard delete or an update — and this
// is named on the node rather than worked around. A projection that lags is
// fully repairable, which is the failure the ordering creates.
//
// It touches no non-fs: row: every row it forgets came from a scope read, and
// every scope it reads is built by ProjectionScope.
func (s *Store) StateRebuild(projPath string) (int, error) {
	scopes, err := s.Scopes()
	if err != nil {
		return 0, err
	}
	live, closeLive, err := s.openProjection(projPath)
	if err != nil {
		return 0, err
	}
	defer closeLive()
	known := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		known[ProjectionScope(scope)] = true
		events, err := s.Replay(scope, nil)
		if err != nil {
			return 0, fmt.Errorf("state rebuild: replay %s: %w", scope, err)
		}
		expected := make(map[string]bool, len(events))
		for i := range events {
			expected[events[i].ID] = true
			expected[events[i].ID+blockOriginSuffix] = true
			if err := FoldEvent(live, events[i]); err != nil {
				return 0, fmt.Errorf("state rebuild: fold %s: %w", scope, err)
			}
		}
		if err := forgetStrays(live, scope, expected); err != nil {
			return 0, err
		}
	}

	// A scope in the projection that the event store does not name is inert
	// data fs left behind; rebuild removes it rather than leaving rows nothing
	// can explain.
	stray, err := projectionScopes(live)
	if err != nil {
		return 0, err
	}
	for _, scope := range stray {
		if known[scope] {
			continue
		}
		rows, err := live.Recent(scope, recentLimit)
		if err != nil {
			return 0, fmt.Errorf("state rebuild: read %s: %w", scope, err)
		}
		for _, m := range rows {
			if err := live.Forget(m.Id); err != nil {
				return 0, fmt.Errorf("state rebuild: remove %s row %d: %w", scope, m.Id, err)
			}
		}
	}
	return len(scopes), nil
}

// blockOriginSuffix is the role suffix a block's own row carries: one event can
// write a node version and a record, and (origin, origin_id) is unique per row,
// so the second row's provenance names the event plus this role.
const blockOriginSuffix = "#block"

// forgetStrays forgets the rows of one scope whose provenance names no event of
// that scope's stream.
func forgetStrays(ms *memstore.Store, projectID string, expected map[string]bool) error {
	rows, err := scopeRows(ms, ProjectionScope(projectID))
	if err != nil {
		return err
	}
	for _, m := range rows {
		if expected[m.OriginId] {
			continue
		}
		if err := ms.Forget(m.Id); err != nil {
			return fmt.Errorf("state rebuild: forget stray %s row %d: %w", projectID, m.Id, err)
		}
	}
	return nil
}

// projectionScopes lists the fs: scopes the projection holds. It is the only way
// to enumerate them — memstore's Recent is the only scope-listing verb (A5) —
// and every row it reads that is not fs's is discarded here and used for
// nothing: fs reads only its own rows (D5).
func projectionScopes(ms *memstore.Store) ([]string, error) {
	rows, err := ms.Recent("", recentLimit)
	if err != nil {
		return nil, fmt.Errorf("state rebuild: list projection scopes: %w", err)
	}
	seen := map[string]bool{}
	var scopes []string
	for _, m := range rows {
		if !isProjectionScope(m.Scope) || seen[m.Scope] {
			continue
		}
		seen[m.Scope] = true
		scopes = append(scopes, m.Scope)
	}
	sort.Strings(scopes)
	return scopes, nil
}

// isProjectionScope reports whether a scope is one fs writes: the prefix, and
// nothing else, is what makes it fs's (D5).
func isProjectionScope(scope string) bool {
	return len(scope) > len(projectionScopePrefix) && scope[:len(projectionScopePrefix)] == projectionScopePrefix
}

// openProjection opens the live projection, or returns the attached one when
// the store already holds it, so a rebuild and the read after it in the same
// process see one file.
func (s *Store) openProjection(projPath string) (*memstore.Store, func(), error) {
	if s.proj != nil {
		return s.proj, func() {}, nil
	}
	ms, err := OpenProjection(projPath)
	if err != nil {
		return nil, nil, fmt.Errorf("state: open projection: %w", err)
	}
	return ms, func() { ms.Close() }, nil
}

// scratchProjection opens a throwaway projection file, with its path and the
// function that removes it.
func scratchProjection() (*memstore.Store, string, func(), error) {
	dir, err := os.MkdirTemp("", "fs-state-verify-")
	if err != nil {
		return nil, "", nil, fmt.Errorf("state verify: scratch dir: %w", err)
	}
	path := filepath.Join(dir, "memory.db")
	ms, err := memstore.Open(path)
	if err != nil {
		os.RemoveAll(dir)
		return nil, "", nil, fmt.Errorf("state verify: scratch projection: %w", err)
	}
	return ms, path, func() { ms.Close(); os.RemoveAll(dir) }, nil
}
