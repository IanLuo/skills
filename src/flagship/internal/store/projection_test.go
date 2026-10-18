package store_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/IanLuo/memstore"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/store"
	_ "modernc.org/sqlite"
)

// The projection is exercised here (package store_test) so it can import both
// store and query: store's own package cannot, query imports store.

func fixtureEvents(t *testing.T) []store.Event {
	t.Helper()
	ts := func(day int, hh, mm int) time.Time {
		return time.Date(2026, 10, day, hh, mm, 0, 0, time.UTC)
	}
	pay := func(t *testing.T, v any) json.RawMessage {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("fixture payload: %v", err)
		}
		return b
	}
	commit := func(s string) *string { return &s }
	ev := func(id string, typ store.EventType, nodeID *string, parent *string, ts time.Time, payload any) store.Event {
		return store.Event{
			ID: id, Type: typ, ProjectID: "proj-a",
			NodeID: nodeID, ParentNodeID: parent, CommitSHA: commit("c" + id),
			Timestamp: ts, Payload: pay(t, payload),
		}
	}

	var out []store.Event
	out = append(out,
		ev("e01-proj", store.ProjectCreated, nil, nil, ts(1, 0, 0), map[string]string{"name": "proj-a", "root_path": "/tmp/proj-a"}),
		ev("e02-t1", store.TaskCreated, strPtr("t1"), nil, ts(1, 0, 1), map[string]string{"goal": "root goal", "kind": "work"}),
		ev("e03-t2", store.TaskCreated, strPtr("t2"), strPtr("t1"), ts(1, 0, 2), map[string]string{"goal": "child goal"}),
		ev("e04-t1a", store.StatusChanged, strPtr("t1"), nil, ts(1, 0, 3), map[string]string{"from": "pending", "to": "active"}),
		ev("e05-t1d", store.DecisionRecorded, strPtr("t1"), nil, ts(1, 0, 4), map[string]string{"summary": "decided X"}),
		ev("e06-t1k", store.KnowledgeAdded, strPtr("t1"), nil, ts(1, 0, 5), map[string]string{"summary": "learned Y"}),
		ev("e07-t1r", store.ReportRecorded, strPtr("t1"), nil, ts(1, 0, 6), map[string]string{"kind": "done", "line": "done line"}),
		ev("e08-t2b", store.TaskBlocked, strPtr("t2"), nil, ts(1, 0, 7), map[string]string{"reason": "waiting on probe", "check": "test -f /tmp/x"}),
		ev("e09-t2s", store.StatusChanged, strPtr("t2"), nil, ts(1, 0, 8), map[string]string{"from": "pending", "to": "blocked"}),
		ev("e10-t1m", store.MetadataChanged, strPtr("t1"), nil, ts(1, 1, 0), map[string]string{"field": "goal", "new_value": "root goal v2"}),
		ev("e11-t2u", store.TaskUnblocked, strPtr("t2"), nil, ts(1, 1, 1), map[string]any{}),
		ev("e12-t2p", store.StatusChanged, strPtr("t2"), nil, ts(1, 1, 2), map[string]string{"from": "blocked", "to": "pending"}),
		ev("e13-t3", store.TaskCreated, strPtr("t3"), strPtr("t1"), ts(1, 1, 3), map[string]string{"goal": "gap goal", "kind": "gap", "severity": "severe", "check": "grep -q x /tmp/file"}),
		ev("e14-cap", store.CapClaimed, nil, nil, ts(1, 1, 4), map[string]string{"pane_id": "w9:p1", "tab_id": "w9:t1", "agent": "pi", "claimed_at": "2026-10-01T00:00:00Z"}),
		ev("e15-t2m", store.StatusChanged, strPtr("t2"), nil, ts(1, 1, 5), map[string]string{"from": "pending", "to": "done"}),
		ev("e16-t2d", store.DecisionRecorded, strPtr("t2"), nil, ts(1, 1, 6), map[string]string{"summary": "absorbed into t1"}),
		ev("e17-t1abs", store.MetadataChanged, strPtr("t1"), nil, ts(1, 1, 7), map[string]string{"field": "absorbed", "new_value": "t2"}),
	)
	return out
}

// edgeRows reads the edge table directly: memstore's public surface surfaces
// only superseded-by and found-by on Search hits, so the projection's own rels
// cannot be read through it (a finding slice 2 must answer).
func edgeRows(t *testing.T, dbPath string, from int64) map[string][]int64 {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open memstore for edges: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT rel, to_id FROM edge WHERE from_id = ?`, from)
	if err != nil {
		t.Fatalf("query edges: %v", err)
	}
	defer rows.Close()
	out := map[string][]int64{}
	for rows.Next() {
		var rel string
		var to int64
		if err := rows.Scan(&rel, &to); err != nil {
			t.Fatalf("scan edge: %v", err)
		}
		out[rel] = append(out[rel], to)
	}
	return out
}

func envelopeOf(m memstore.Memory) map[string]any {
	var env map[string]any
	_ = json.Unmarshal([]byte(m.Extra), &env)
	return env
}

// payloadOf reads a row's raw event payload out of its envelope.
func payloadOf(env map[string]any) map[string]any {
	p, _ := env["payload"].(map[string]any)
	if p == nil {
		return map[string]any{}
	}
	return p
}

func fsNodeOf(m memstore.Memory) string {
	if v, ok := envelopeOf(m)["fs_node"].(string); ok {
		return v
	}
	return ""
}

// projectionDiff compares a memstore projection against query.BuildTree for
// one project, matching rows the way the card's acceptance does: by node (the
// Extra envelope's fs_node), kind and text. Merged nodes are their own case —
// BuildTree leaves an absorbed node done, the card closes its row — and a node
// with no task-created event (materialized by BuildTree from whatever named
// it) is expected to carry its id as text, the projection's documented rule.
func projectionDiff(t *testing.T, project string, events []store.Event, tree *query.Tree, ms *memstore.Store, proj *store.Projection) []string {
	t.Helper()
	var diffs []string
	add := func(format string, args ...any) {
		diffs = append(diffs, fmt.Sprintf(format, args...))
	}
	scope := "fs:" + project
	path := ms.Path()

	rows, err := ms.Recent(scope, 1<<20)
	if err != nil {
		t.Fatalf("recent %s: %v", scope, err)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Id < rows[j].Id })

	// Rows by node: node rows are the node kinds, side rows anything else.
	byNode := map[string][]memstore.Memory{}
	for _, m := range rows {
		node := fsNodeOf(m)
		if node == "" {
			continue
		}
		byNode[node] = append(byNode[node], m)
	}
	// Side rows must also match their node's envelope count, so they are kept
	// apart from node rows by kind.
	nodeKinds := map[string]bool{"work": true, "dispatch": true, "gap": true, "batch": true}

	// Expected merged set: bare ids named in any metadata-changed(absorbed)
	// event of this stream — the only absorbees whose rows this store holds.
	merged := map[string]bool{}
	for _, evt := range events {
		if evt.Type != store.MetadataChanged {
			continue
		}
		var pl struct {
			Field    string `json:"field"`
			NewValue string `json:"new_value"`
		}
		_ = json.Unmarshal(evt.Payload, &pl)
		if pl.Field != "absorbed" {
			continue
		}
		for _, id := range strings.Split(pl.NewValue, ",") {
			if id = strings.TrimSpace(id); id != "" && !strings.Contains(id, ":") {
				merged[id] = true
			}
		}
	}

	// Expected counts from events, for the reads BuildTree does not derive.
	eventCounts := map[string]map[string]int{} // node -> kind -> count
	created := map[string]bool{}
	for _, evt := range events {
		if evt.NodeID == nil {
			continue
		}
		n := *evt.NodeID
		if eventCounts[n] == nil {
			eventCounts[n] = map[string]int{}
		}
		switch evt.Type {
		case store.TaskCreated:
			created[n] = true
		case store.DecisionRecorded:
			eventCounts[n]["decision"]++
		case store.KnowledgeAdded:
			eventCounts[n]["knowledge"]++
		case store.ReportRecorded:
			eventCounts[n]["report"]++
		case store.TaskBlocked:
			eventCounts[n]["block"]++
		}
	}

	// Node set.
	var wantNodes []string
	for nid := range tree.Nodes {
		wantNodes = append(wantNodes, nid)
	}
	sort.Strings(wantNodes)
	var gotNodes []string
	for nid := range byNode {
		gotNodes = append(gotNodes, nid)
	}
	sort.Strings(gotNodes)
	if strings.Join(wantNodes, " ") != strings.Join(gotNodes, " ") {
		add("node set differs: tree %v vs rows %v", wantNodes, gotNodes)
	}

	for _, nid := range wantNodes {
		nodeRows := byNode[nid]
		// The node's row(s) are the node-kind rows; side rows ride the same
		// envelope but different kinds, so separate them by kind here.
		var nodeMem []memstore.Memory
		for _, m := range nodeRows {
			if nodeKinds[m.Kind] {
				nodeMem = append(nodeMem, m)
			}
		}
		if len(nodeMem) == 0 {
			add("node %s: no node-kind row", nid)
			continue
		}
		want := tree.Nodes[nid]
		// Current row: the latest one still open. Superseded and absorbed rows
		// are closed; exactly the current row stays open unless the node was
		// merged, which closes all of them.
		var current memstore.Memory
		var open int
		for _, m := range nodeMem {
			if m.Status != "closed" {
				current = m
				open++
			}
		}
		tre := tree.Nodes[nid].Status
		if tre == "" {
			tre = "pending"
		}
		if merged[nid] {
			tre = "closed"
		}
		if open == 0 {
			if !merged[nid] {
				add("node %s: no open row (all %d rows closed)", nid, len(nodeMem))
			}
		} else if current.Status != tre {
			add("node %s: status %q, want %q", nid, current.Status, tre)
		}
		if !merged[nid] {
			wantText := want.Goal
			if !created[nid] {
				wantText = nid
			}
			if current.Text != wantText {
				add("node %s: text %q, want %q", nid, current.Text, wantText)
			}
			if string(current.Kind) != string(want.Kind) {
				add("node %s: kind %q, want %q", nid, current.Kind, want.Kind)
			}
			// Parent: the first row carries it; it must name the parent node's
			// first row. An orphan — a parent id that names no node in the
			// stream (the "damage the old path left behind" BuildTree marks) —
			// has no row to point at: the tree keeps the id, the projection
			// keeps no parent, and the two answer the same.
			first := nodeMem[0]
			wantParent := want.ParentID
			if _, ok := tree.Nodes[wantParent]; !ok {
				wantParent = ""
			}
			if first.Parent == nil {
				if wantParent != "" {
					add("node %s: no parent row, want parent %s", nid, wantParent)
				}
			} else {
				pid := *first.Parent
				parentMem, err := ms.Get(pid)
				if err != nil {
					add("node %s: parent row %d unreadable: %v", nid, pid, err)
				} else if got := fsNodeOf(parentMem); got != wantParent {
					add("node %s: parent points at %s, want %s", nid, got, wantParent)
				}
			}
		}
		// Counts: expected from the tree (decision, knowledge) or from events
		// (report — BuildTree derives none).
		for _, k := range []string{"decision", "knowledge", "report", "block"} {
			wantN := 0
			switch k {
			case "decision":
				wantN = len(want.Decisions)
			case "knowledge":
				wantN = len(want.Knowledge)
			default:
				wantN = eventCounts[nid][k]
			}
			gotN := 0
			for _, m := range nodeRows {
				if m.Kind == k {
					gotN++
				}
			}
			if gotN != wantN {
				add("node %s: %s rows %d, want %d", nid, k, gotN, wantN)
			}
			// The Link edges must exist too — the rows may, with the edge
			// missing. Expected from the same sources.
			wantE := wantN
			if m, ok := eventCounts[nid][k]; ok && (k == "report" || k == "block") {
				wantE = m
			}
			gotE := len(edgeRows(t, path, nodeMem[0].Id)[k])
			if gotE != wantE {
				add("node %s: %s edges %d, want %d", nid, k, gotE, wantE)
			}
		}
		if merged[nid] {
			// Every row closed, and the absorbed-into edge names the survivor.
			for _, m := range nodeMem {
				if m.Status != "closed" {
					add("node %s: absorbed but row %d open", nid, m.Id)
				}
			}
			found := false
			for _, to := range edgeRows(t, path, nodeMem[0].Id)["absorbed-into"] {
				m, err := ms.Get(to)
				if err == nil && fsNodeOf(m) != "" {
					found = true
				}
			}
			if !found {
				add("node %s: no absorbed-into edge to a node row", nid)
			}
		}
	}

	// Project and cap rows, against the stream (BuildTree has neither).
	var createdEvts, capEvts int
	var projectRoot string
	for _, evt := range events {
		switch evt.Type {
		case store.ProjectCreated:
			createdEvts++
			if createdEvts == 1 {
				var pl map[string]any
				_ = json.Unmarshal(evt.Payload, &pl)
				projectRoot, _ = pl["root_path"].(string)
			}
		case store.CapClaimed:
			capEvts++
		}
	}
	if createdEvts > 0 {
		if proj.ProjectMemory == 0 {
			add("project row missing")
		} else {
			m, err := ms.Get(proj.ProjectMemory)
			if err != nil {
				add("project row: %v", err)
			} else {
				if m.Kind != "project" || m.Text != projectRoot || m.Scope != scope {
					add("project row kind/text/scope = %s/%q/%q", m.Kind, m.Text, m.Scope)
				}
			}
		}
	} else if proj.ProjectMemory != 0 {
		add("project row written with no project-created event")
	}
	gotCap := 0
	for _, m := range rows {
		if m.Kind == "cap" {
			gotCap++
		}
	}
	if gotCap != capEvts {
		add("cap rows %d, want %d", gotCap, capEvts)
	}
	return diffs
}

func mustProject(t *testing.T, events []store.Event) (*memstore.Store, *store.Projection) {
	t.Helper()
	dir := t.TempDir()
	ms, err := memstore.Open(filepath.Join(dir, "mem.db"))
	if err != nil {
		t.Fatalf("memstore open: %v", err)
	}
	proj, err := store.ProjectEvents(ms, "proj-a", events)
	if err != nil {
		ms.Close()
		t.Fatalf("ProjectEvents: %v", err)
	}
	return ms, proj
}

// TestProjectionFixture is the card's fixture gate: a fixed event stream into
// a throwaway memstore, asserting rows, statuses, parent links, the supersede
// chain for a goal edit, the cap-claimed scope row — and (as the deliverable's
// own test) that the folded state answers the same as query.BuildTree.
func TestProjectionFixture(t *testing.T) {
	events := fixtureEvents(t)
	ms, proj := mustProject(t, events)
	defer ms.Close()

	// The BuildTree equivalence, first: any diff here is a mapping bug.
	tree := query.BuildTree("proj-a", events)
	if diffs := projectionDiff(t, "proj-a", events, tree, ms, proj); len(diffs) > 0 {
		for _, d := range diffs {
			t.Errorf("fixture projection vs BuildTree: %s", d)
		}
	}

	// Rows per node, oldest first: one original row, then the goal-edit row.
	if got := len(proj.NodeRows); got != 3 {
		t.Fatalf("NodeRows has %d nodes, want 3", got)
	}
	// One row per change to the node's displayed state: created, set active, goal
	// edited, and the merge's absorbed list (D3 — content is immutable, so a
	// change is a new version and the row it replaces is closed).
	if rows := proj.NodeRows["t1"]; len(rows) != 4 {
		t.Fatalf("t1 has %d rows, want 4 (create, active, goal edit, absorbed)", len(rows))
	} else {
		old, err := ms.Get(rows[0])
		if err != nil {
			t.Fatalf("get t1 row0: %v", err)
		}
		cur, err := ms.Get(rows[len(rows)-1])
		if err != nil {
			t.Fatalf("get t1 current row: %v", err)
		}
		if old.Text != "root goal" || cur.Text != "root goal v2" {
			t.Errorf("t1 texts = %q/%q, want root goal / root goal v2", old.Text, cur.Text)
		}
		// The supersede chain: each retired row names the row that replaced it,
		// and only the newest stays open.
		for i := 0; i < len(rows)-1; i++ {
			retired, err := ms.Get(rows[i])
			if err != nil {
				t.Fatalf("get t1 row %d: %v", i, err)
			}
			if retired.Status != "closed" {
				t.Errorf("t1 row %d status %q, want closed", i, retired.Status)
			}
			if retired.Supersedes == nil || *retired.Supersedes != rows[i+1] {
				t.Errorf("t1 row %d supersedes %v, want %d", i, retired.Supersedes, rows[i+1])
			}
		}
		if cur.Status != "active" {
			t.Errorf("t1 current status %q, want active", cur.Status)
		}
		if cur.Text != "root goal v2" {
			t.Errorf("t1 current text %q, want root goal v2", cur.Text)
		}
	}
	// t2: created, blocked, status blocked, unblocked, status pending, done — six
	// versions, every one of them closed by the merge that absorbed it.
	if rows := proj.NodeRows["t2"]; len(rows) != 6 {
		t.Fatalf("t2 has %d rows, want 6 (create, block, blocked, unblock, pending, done)", len(rows))
	} else {
		m, err := ms.Get(rows[0])
		if err != nil {
			t.Fatalf("get t2: %v", err)
		}
		// Absorbed: closed, with the absorbed-into link to the survivor.
		if m.Status != "closed" {
			t.Errorf("t2 status %q, want closed (absorbed)", m.Status)
		}
		if m.Parent == nil || *m.Parent != proj.NodeRows["t1"][0] {
			t.Errorf("t2 parent %v, want t1 first row %d", m.Parent, proj.NodeRows["t1"][0])
		}
		got := edgeRows(t, ms.Path(), rows[0])["absorbed-into"]
		if len(got) != 1 {
			t.Errorf("t2 absorbed-into edges %v, want one edge to the survivor", got)
		} else if survivor, err := ms.Get(got[0]); err != nil || fsNodeOf(survivor) != "t1" {
			t.Errorf("t2 absorbed-into edge points at %v, want the survivor t1", survivor.Id)
		}
		// The block link (task-blocked), with the reason riding the block row.
		blk := edgeRows(t, ms.Path(), rows[0])["block"]
		if len(blk) != 1 {
			t.Fatalf("t2 block edges %v, want 1", blk)
		}
		b, err := ms.Get(blk[0])
		if err != nil {
			t.Fatalf("get t2 block row: %v", err)
		}
		if b.Kind != "block" || b.Text != "waiting on probe" {
			t.Errorf("t2 block row kind/text = %s/%q", b.Kind, b.Text)
		}
		if got := payloadOf(envelopeOf(b))["reason"]; got != "waiting on probe" {
			t.Errorf("t2 block envelope reason %v", got)
		}
	}
	if rows := proj.NodeRows["t3"]; len(rows) != 1 {
		t.Fatalf("t3 has %d rows, want 1", len(rows))
	} else {
		m, err := ms.Get(rows[0])
		if err != nil {
			t.Fatalf("get t3: %v", err)
		}
		if m.Kind != "gap" || m.Status != "pending" {
			t.Errorf("t3 kind/status = %s/%q, want gap/pending", m.Kind, m.Status)
		}
		env := envelopeOf(m)
		nodeDocOf := env["node"].(map[string]any)
		if nodeDocOf["severity"] != "severe" || nodeDocOf["check"] != "grep -q x /tmp/file" {
			t.Errorf("t3 node document severity/check = %v/%v", nodeDocOf["severity"], nodeDocOf["check"])
		}
		if env["event_id"] != "e13-t3" || env["fs_node"] != "t3" || env["commit_sha"] != "ce13-t3" {
			t.Errorf("t3 envelope provenance = %v", env)
		}
		// The row names the event it came from in its provenance columns, which is
		// what makes a re-fold safe (D3, A1).
		if m.Origin != "fs" || m.OriginId != "e13-t3" || m.FsNode != "t3" || m.Ref != "ce13-t3" {
			t.Errorf("t3 structured provenance = %s/%q/%q/%q, want fs/e13-t3/t3/ce-t3",
				m.Origin, m.OriginId, m.FsNode, m.Ref)
		}
	}

	// Project row: Scope, Kind and Text per the card.
	if proj.ProjectMemory == 0 {
		t.Fatal("no project row")
	}
	pm, err := ms.Get(proj.ProjectMemory)
	if err != nil {
		t.Fatalf("get project row: %v", err)
	}
	if pm.Kind != "project" || pm.Text != "/tmp/proj-a" || pm.Scope != "fs:proj-a" {
		t.Errorf("project row = %s/%q/%q, want project//tmp/proj-a/fs:proj-a", pm.Kind, pm.Text, pm.Scope)
	}
	if envelopeOf(pm)["event_id"] != "e01-proj" {
		t.Errorf("project envelope event_id = %v", envelopeOf(pm)["event_id"])
	}

	// Cap row: scope-level, one per cap-claimed event.
	if len(proj.CapRows) != 1 {
		t.Fatalf("CapRows = %v, want 1", proj.CapRows)
	}
	cm, err := ms.Get(proj.CapRows[0])
	if err != nil {
		t.Fatalf("get cap row: %v", err)
	}
	if cm.Kind != "cap" || cm.Text != "w9:p1" || cm.Scope != "fs:proj-a" {
		t.Errorf("cap row = %s/%q/%q", cm.Kind, cm.Text, cm.Scope)
	}

	// Side links on t1: decision, knowledge, report each one edge and one row.
	for rel, wantText := range map[string]string{"decision": "decided X", "knowledge": "learned Y", "report": "done line"} {
		ids := edgeRows(t, ms.Path(), proj.NodeRows["t1"][0])[rel]
		if len(ids) != 1 {
			t.Errorf("t1 %s edges = %v, want 1", rel, ids)
			continue
		}
		m, err := ms.Get(ids[0])
		if err != nil {
			t.Fatalf("get t1 %s row: %v", rel, err)
		}
		if m.Kind != rel || m.Text != wantText {
			t.Errorf("t1 %s row = %s/%q, want %s/%q", rel, m.Kind, m.Text, rel, wantText)
		}
	}
}

// TestProjectionRealStoreCopy is the card's real validation, on a COPY of
// ~/.fs/store.db — never the original. It runs only when FS_PROJECTION_REAL_DB
// names the copy; the suite stays hermetic without it. For every scope it
// replays the events, folds them into a throwaway memstore, and requires the
// memstore state to answer the same as query.BuildTree.
func TestProjectionRealStoreCopy(t *testing.T) {
	copyPath := os.Getenv("FS_PROJECTION_REAL_DB")
	if copyPath == "" {
		t.Skip("set FS_PROJECTION_REAL_DB to a copy of ~/.fs/store.db to run the real-store projection validation")
	}
	s, err := store.Open(copyPath)
	if err != nil {
		t.Fatalf("open store copy: %v", err)
	}
	defer s.Close()
	scopes, err := s.Scopes()
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	var totalNodes, totalDiffs int
	for _, scope := range scopes {
		events, err := s.Replay(scope, nil)
		if err != nil {
			t.Fatalf("replay %s: %v", scope, err)
		}
		dir := t.TempDir()
		ms, err := memstore.Open(filepath.Join(dir, "mem.db"))
		if err != nil {
			t.Fatalf("memstore open: %v", err)
		}
		proj, err := store.ProjectEvents(ms, scope, events)
		if err != nil {
			ms.Close()
			t.Fatalf("project %s: %v", scope, err)
		}
		tree := query.BuildTree(scope, events)
		totalNodes += len(tree.Nodes)
		diffs := projectionDiff(t, scope, events, tree, ms, proj)
		ms.Close()
		totalDiffs += len(diffs)
		t.Logf("proj=%-24s events=%-5d nodes=%-4d diffs=%d", scope, len(events), len(tree.Nodes), len(diffs))
		for _, d := range diffs {
			t.Logf("  diff %s: %s", scope, d)
		}
	}
	t.Logf("TOTAL projects=%d nodes_derived=%d diffs=%d", len(scopes), totalNodes, totalDiffs)
	if totalDiffs > 0 {
		t.Fatalf("%d projection diffs across %d scopes", totalDiffs, len(scopes))
	}
}
