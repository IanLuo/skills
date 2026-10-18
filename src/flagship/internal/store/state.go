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
}

func (i StateIssue) String() string {
	return fmt.Sprintf("scope %s differs at %s: the event store says %s, the projection says %s",
		i.Scope, i.Field, i.EventStore, i.Projection)
}

// canonRow is one projected row as both sides compare it: everything the row
// asserts, with the parent resolved to the node it names (row ids differ between
// two files, so an id would compare unequal for equal state) and without the
// row's own id.
type canonRow struct {
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Status string `json:"status"`
	FsNode string `json:"fs_node,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Parent string `json:"parent,omitempty"`
	Extra  string `json:"extra"`
}

// canonicalScope is one scope as a map keyed on provenance: one entry per
// projected event, so a difference is named by the event it belongs to rather
// than by a position in a list.
func canonicalScope(ms *memstore.Store, projectID string) (map[string]canonRow, error) {
	rows, err := scopeRows(ms, ProjectionScope(projectID))
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]string, len(rows))
	for _, m := range rows {
		byID[m.Id] = m.FsNode
	}
	out := make(map[string]canonRow, len(rows))
	for _, m := range rows {
		key := m.OriginId
		if key == "" {
			// A row with no provenance names no event: it is named by what it
			// says instead, so a stray row still appears as a difference.
			key = "#" + m.Kind + "#" + m.Text
		}
		if _, taken := out[key]; taken {
			key = key + "+" + m.Kind
		}
		parent := ""
		if m.Parent != nil {
			parent = byID[*m.Parent]
		}
		out[key] = canonRow{
			Kind: m.Kind, Text: m.Text, Status: m.Status,
			FsNode: m.FsNode, Ref: m.Ref, Parent: parent, Extra: m.Extra,
		}
	}
	return out, nil
}

// StateVerify compares the live projection with a scratch re-fold of the event
// store, scope by scope. It writes nothing to the live file: the scratch store
// is a temp file, removed before it returns. A scope present in the event store
// but absent from the projection is a divergence with an empty projection.
func (s *Store) StateVerify(projPath string) ([]StateIssue, error) {
	scopes, err := s.Scopes()
	if err != nil {
		return nil, err
	}
	live, closeLive, err := s.openProjection(projPath)
	if err != nil {
		return nil, err
	}
	defer closeLive()
	scratch, cleanup, err := scratchProjection()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	var issues []StateIssue
	for _, scope := range scopes {
		events, err := s.Replay(scope, nil)
		if err != nil {
			return nil, fmt.Errorf("state verify: replay %s: %w", scope, err)
		}
		for i := range events {
			if err := FoldEvent(scratch, events[i]); err != nil {
				return nil, fmt.Errorf("state verify: fold %s: %w", scope, err)
			}
		}
		want, err := canonicalScope(scratch, scope)
		if err != nil {
			return nil, err
		}
		got, err := canonicalScope(live, scope)
		if err != nil {
			return nil, err
		}
		diff, err := DiffJSON(want, got)
		if err != nil {
			return nil, err
		}
		if diff != nil {
			issues = append(issues, StateIssue{
				Scope: scope, Field: diff.Field,
				EventStore: diff.A, Projection: diff.B,
			})
		}
	}
	return issues, nil
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

// scratchProjection opens a throwaway projection file, with the function that
// removes it.
func scratchProjection() (*memstore.Store, func(), error) {
	dir, err := os.MkdirTemp("", "fs-state-verify-")
	if err != nil {
		return nil, nil, fmt.Errorf("state verify: scratch dir: %w", err)
	}
	ms, err := memstore.Open(filepath.Join(dir, "memory.db"))
	if err != nil {
		os.RemoveAll(dir)
		return nil, nil, fmt.Errorf("state verify: scratch projection: %w", err)
	}
	return ms, func() { ms.Close(); os.RemoveAll(dir) }, nil
}
