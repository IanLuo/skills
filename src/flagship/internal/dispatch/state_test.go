package dispatch_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/IanLuo/memstore"
	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/dispatch"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/store"
)

// AC1 — exactly one read is switched, and it is `pending`.
//
// The AC's acceptance is enforced as behaviour rather than asserted as a pass:
// this test FAILS if any read is declared switched while AC8's ceiling is unmet,
// and FAILS if none is declared once the ceiling is met. The measured reason and
// number are recorded on the running test's output and on the node. Before the
// memstore `b5be5fd` pin the projection-alone arm measured 1.26-1.40x against a
// 1.25x ceiling and the declaration was empty; the covering index
// `idx_memory_scope_recent` puts it inside the ceiling and the declaration names
// `pending`.
// TestSwitchedReads (AC1/AC8 of the superseded spec) is retired: the contract
// .agents/specs/fs-contract.md R2 = one source, the event log. The projection and
// its gate are scheduled for deletion, so there is no source to switch between.

// pendingFixture builds a stream containing every event type fs pending
// consumes, so the parity gate compares the whole answer and not a subset:
// task-created with and without a kind, status-changed, delivery-recorded,
// report-recorded, cap-claimed, and a kind=gap node carrying severity, check and
// found_by.
type pendingFixture struct {
	*fixture
	capNode    string
	workerNode string
	gapNode    string
	foundBy    string
}

func newPendingFixture(t *testing.T) *pendingFixture {
	return newPendingFixtureGaps(t, true)
}

// newPendingFixtureGaps is the same stream with or without a kind=gap node, so a
// test can build the shape where one side's open-gap map is empty.
func newPendingFixtureGaps(t *testing.T, withGap bool) *pendingFixture {
	t.Helper()
	p := &pendingFixture{fixture: newFixture(t, t.TempDir())}

	// A work node that can be a gap's found_by: a gap's provenance names a node
	// that exists.
	p.foundBy = addCapKindNode(t, p.fixture, "the finding node", query.KindWork)

	// task-created with a kind.
	p.capNode = addCapNode(t, p.fixture, "dispatch code: sample")
	p.workerNode = addWorkerNode(t, p.fixture, "active")

	// task-created without a kind: an event written before nodes carried one.
	// It reads as work, and the projection must materialise it the same way —
	// with a row, and with the goal the event's payload carries.
	appendRaw(t, p.fixture, store.Event{
		Type: store.TaskCreated, ProjectID: "cap", NodeID: strPtr("t-legacy"),
		Payload: json.RawMessage(`{"goal":"a node from before kinds"}`),
	})

	// delivery-recorded, report-recorded, cap-claimed. (The status-changed is the
	// one addWorkerNode wrote; a second would leave the node with two, and a test
	// that removes one status row would not see the state move.)
	recordDeliveryAs(t, p.fixture, p.capNode, "agent-1", p.workerNode)
	if resp := p.h.ReportAdd("skills", p.workerNode, "question", "one line about the work"); !resp.OK {
		t.Fatalf("report: %s", resp.Error)
	}
	if resp := p.h.ClaimCap("cap", command.CapClaim{PaneID: "w9:p54", TabID: "w9:t1", Agent: "pi"}); !resp.OK {
		t.Fatalf("cap claim: %s", resp.Error)
	}

	if !withGap {
		return p
	}
	// A gap with every field memstore has no column for.
	added := p.h.TaskAddKind("skills", "a gap with a weight", query.KindGap,
		"cap:"+p.foundBy, "", "", "true", query.SeveritySevere, nil)
	if !added.OK {
		t.Fatalf("gap add: %s", added.Error)
	}
	p.gapNode = added.Data.(command.EventData).NodeID
	return p
}

// gapScopesRefusal drives the gate and returns its refusal message, failing the
// test when it answers.
func gapScopesRefusal(t *testing.T, p *pendingFixture) string {
	t.Helper()
	answer, err := dispatch.GatedAnswer("pending", command.SourceProjection, p.h,
		func() (dispatch.Answer, error) { return dispatch.AnswerFromLog(p.h) },
		func() (dispatch.Answer, error) { return dispatch.AnswerFromProjection(p.h) })
	if err == nil {
		t.Fatalf("the gate answered %+v; a per-scope divergence must refuse", answer)
	}
	return err.Error()
}

// A2 — the per-scope breakdown is what makes a cancelling divergence visible and
// what names the scope a count moved in. Each case below is one the total-only
// comparison passes, so each is a gap this closes.
func TestPendingParityRefusesOnThePerScopeGapBreakdown(t *testing.T) {
	t.Run("the totals cancel out", func(t *testing.T) {
		p := newPendingFixture(t)
		// A gap in cap as well, so one scope can gain while another loses: cap
		// 1 -> 0 and skills 1 -> 2 leaves the total at 2 either way.
		added := p.h.TaskAddKind("cap", "a cap gap", query.KindGap, "", "", "", "", "", nil)
		if !added.OK {
			t.Fatalf("cap gap: %s", added.Error)
		}
		capGap := added.Data.(command.EventData).NodeID

		ms := p.h.Projection()
		capScopeState, err := store.ReadScope(ms, "cap")
		if err != nil {
			t.Fatalf("read cap: %v", err)
		}
		if err := ms.Forget(capScopeState.Nodes[capGap].RowID); err != nil {
			t.Fatalf("forget the cap gap's row: %v", err)
		}
		injectNodeRow(t, p, "skills", "t-injected-gap", "gap",
			`{"goal":"a gap nobody wrote","kind":"gap"}`, "task-created", "injected-gap", 0)

		// The totals are equal; only the parts differ.
		fromLog, err := dispatch.AnswerFromLog(p.h)
		if err != nil {
			t.Fatalf("log answer: %v", err)
		}
		fromProj, err := dispatch.AnswerFromProjection(p.h)
		if err != nil {
			t.Fatalf("projection answer: %v", err)
		}
		if fromLog.Gaps != fromProj.Gaps {
			t.Fatalf("the totals differ (%d vs %d); this case is about a cancelling difference", fromLog.Gaps, fromProj.Gaps)
		}

		msg := gapScopesRefusal(t, p)
		if !strings.Contains(msg, "gap_scopes.cap") || !strings.Contains(msg, "on scope cap") {
			t.Errorf("refusal = %q, want it to refuse on the per-scope breakdown at gap_scopes.cap", msg)
		}
		if strings.Contains(msg, "undispatched_gaps") {
			t.Errorf("refusal = %q, want the per-scope field, not the total", msg)
		}
	})

	t.Run("a scope is added to the map", func(t *testing.T) {
		// The projection holds no gap at all, so its map is empty and the whole
		// field differs: a scope-spanning divergence, said to be one.
		p := newPendingFixture(t)
		ms := p.h.Projection()
		state, err := store.ReadScope(ms, "skills")
		if err != nil {
			t.Fatalf("read skills: %v", err)
		}
		if err := ms.Forget(state.Nodes[p.gapNode].RowID); err != nil {
			t.Fatalf("forget the gap's row: %v", err)
		}

		msg := gapScopesRefusal(t, p)
		if !strings.Contains(msg, "across scopes") || !strings.Contains(msg, "skills") {
			t.Errorf("refusal = %q, want a scope-spanning refusal naming skills", msg)
		}
		if strings.Contains(msg, "on scope cap") {
			t.Errorf("refusal = %q, want no single scope for a spanning count", msg)
		}
	})

	t.Run("a scope is removed from the map", func(t *testing.T) {
		// The event store holds no gap, so ITS map is empty and the projection's
		// is not: the same whole-field difference, from the other side.
		p := newPendingFixtureGaps(t, false)
		injectNodeRow(t, p, "skills", "t-injected-gap", "gap",
			`{"goal":"a gap nobody wrote","kind":"gap"}`, "task-created", "injected-gap", 0)

		msg := gapScopesRefusal(t, p)
		if !strings.Contains(msg, "across scopes") || !strings.Contains(msg, "skills") {
			t.Errorf("refusal = %q, want a scope-spanning refusal naming skills", msg)
		}
		if strings.Contains(msg, "on scope cap") {
			t.Errorf("refusal = %q, want no single scope for a spanning count", msg)
		}
	})
}

func strPtr(s string) *string { return &s }

// appendRaw writes an event past the append path and folds it into the
// projection, which is the fixture's way of writing a stream shape the CLI's
// validators refuse today.
func appendRaw(t *testing.T, f *fixture, evt store.Event) store.Event {
	t.Helper()
	s, err := store.Open(f.storeDB)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	id, err := s.Append(evt)
	if err != nil {
		t.Fatalf("append raw: %v", err)
	}
	events, err := s.Replay(evt.ProjectID, nil)
	if err != nil {
		t.Fatalf("replay raw: %v", err)
	}
	for _, got := range events {
		if got.ID != id {
			continue
		}
		if err := store.FoldEvent(f.h.Projection(), got); err != nil {
			t.Fatalf("fold raw: %v", err)
		}
		return got
	}
	t.Fatalf("event %s not replayed", id)
	return store.Event{}
}

// answers derives the read's two store-derived answers and the gated one.
// AC2 — the projection answers every state question pending asks, and the gate
// compares them. The fixture carries every event type the read consumes, the two
// derivations are canonically equal, the gated read returns the projection's
// answer, and the read itself answers.
func TestPendingParity(t *testing.T) {
	p := newPendingFixture(t)

	// The fixture is what it says it is: every type pending consumes is there.
	types := eventTypes(t, p.fixture)
	for _, want := range []store.EventType{
		store.TaskCreated, store.StatusChanged, store.DeliveryRecorded,
		store.ReportRecorded, store.CapClaimed,
	} {
		if !types[want] {
			t.Errorf("fixture has no %s event, so the parity gate is comparing less than the read asks", want)
		}
	}

	fromLog, err := dispatch.AnswerFromLog(p.h)
	if err != nil {
		t.Fatalf("event-store answer: %v", err)
	}
	fromProj, err := dispatch.AnswerFromProjection(p.h)
	if err != nil {
		t.Fatalf("projection answer: %v", err)
	}
	if fromLog.Gaps != fromProj.Gaps {
		t.Fatalf("undispatched gaps: event store %d, projection %d", fromLog.Gaps, fromProj.Gaps)
	}
	if len(fromProj.Dispatches) != 1 {
		t.Fatalf("projection found %d unresolved dispatches, want 1: %+v", len(fromProj.Dispatches), fromProj.Dispatches)
	}
	if diff, err := store.DiffJSON(fromLog, fromProj); err != nil {
		t.Fatalf("compare: %v", err)
	} else if diff != nil {
		t.Fatalf("the two sources disagree at %s: event store %s, projection %s", diff.Field, diff.A, diff.B)
	}

	// The gate returns the projection's answer, not the event store's. The two
	// are equal here — that is the gate's precondition — so identity is proven by
	// TestPendingParityRefuses, where only the projection's answer changes.
	gated, err := dispatch.GatedAnswer("pending", command.SourceProjection, p.h,
		func() (dispatch.Answer, error) { return fromLog, nil },
		func() (dispatch.Answer, error) { return fromProj, nil })
	if err != nil {
		t.Fatalf("gated answer: %v", err)
	}
	if !reflect.DeepEqual(gated, fromProj) {
		t.Fatalf("gated answer = %+v, want the projection's %+v", gated, fromProj)
	}

	// And the read answers, with the entry the stream describes.
	entry := pendingEntryFor(t, pendingOf(t, dispatch.Pending(p.h, &fakeHerdr{agents: map[string]string{"agent-1": "working"}})), p.capNode)
	if entry.WorkerNode != p.workerNode {
		t.Errorf("worker_node = %q, want %q", entry.WorkerNode, p.workerNode)
	}
	if len(entry.Reports) != 1 || entry.Reports[0].Line != "one line about the work" {
		t.Errorf("reports = %+v, want the one recorded report", entry.Reports)
	}
}

// eventTypes is the set of event types a fixture's store holds, across scopes.
func eventTypes(t *testing.T, f *fixture) map[store.EventType]bool {
	t.Helper()
	s, err := store.Open(f.storeDB)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	scopes, err := s.Scopes()
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	out := map[store.EventType]bool{}
	for _, scope := range scopes {
		events, err := s.Replay(scope, nil)
		if err != nil {
			t.Fatalf("replay %s: %v", scope, err)
		}
		for _, evt := range events {
			out[evt.Type] = true
		}
	}
	return out
}

// AC3 — a difference refuses, and there is no fallback. Three injected
// divergences: a row's status changed, a row deleted, a row added. Each one
// must exit the read with an error naming the scope and the differing field with
// both values, and return no answer at all.
//
// Each injection changes the state the read derives — a wrong payload, a missing
// row, an extra node — because that is what a fold bug or a stray writer
// produces, and it is what the gate must see.
func TestPendingParityRefuses(t *testing.T) {
	cases := []struct {
		name    string
		diverge func(t *testing.T, p *pendingFixture)
		field   string
		scope   string
	}{
		{
			name: "a row's status changed",
			diverge: func(t *testing.T, p *pendingFixture) {
				// The worker's status row says done instead of active: the same
				// event projected wrong, which is exactly what a fold bug is.
				ms := p.h.Projection()
				scope, err := store.ReadScope(ms, "skills")
				if err != nil {
					t.Fatalf("read scope: %v", err)
				}
				node := scope.Nodes[p.workerNode]
				if node == nil {
					t.Fatalf("no projected worker node %s", p.workerNode)
				}
				injectNodeRow(t, p, "skills", p.workerNode, "dispatch",
					`{"to":"done"}`, "status-changed", "injected-status", node.RowID)
			},
			field: "worker.status",
			scope: "skills",
		},
		{
			name: "a row deleted",
			diverge: func(t *testing.T, p *pendingFixture) {
				// The status row is gone, so the node falls back to pending: the
				// state the read derives is not the event store's.
				ms := p.h.Projection()
				scope, err := store.ReadScope(ms, "skills")
				if err != nil {
					t.Fatalf("read scope: %v", err)
				}
				node := scope.Nodes[p.workerNode]
				if node == nil {
					t.Fatalf("no projected worker node %s", p.workerNode)
				}
				if err := ms.Forget(node.RowID); err != nil {
					t.Fatalf("forget: %v", err)
				}
			},
			field: "worker.status",
			scope: "skills",
		},
		{
			name: "a row added",
			diverge: func(t *testing.T, p *pendingFixture) {
				// An extra open gap nobody recorded, in skills: the backlog
				// moves, and the per-scope breakdown says WHICH scope moved —
				// the total alone is a count across scopes and cannot name one.
				injectNodeRow(t, p, "skills", "t-injected-gap", "gap",
					`{"goal":"a gap nobody wrote","kind":"gap"}`, "task-created", "injected-gap", 0)
			},
			field: "gap_scopes.skills",
			scope: "skills",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newPendingFixture(t)
			logs := captureLogs(t, p.fixture)
			tc.diverge(t, p)

			answer, err := dispatch.GatedAnswer("pending", command.SourceProjection, p.h,
				func() (dispatch.Answer, error) { return dispatch.AnswerFromLog(p.h) },
				func() (dispatch.Answer, error) { return dispatch.AnswerFromProjection(p.h) })
			if err == nil {
				t.Fatalf("the gate answered %+v on a diverged projection; a divergence must refuse", answer)
			}
			msg := err.Error()
			for _, want := range []string{"refused", tc.field, tc.scope, "fs state rebuild"} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal %q does not name %q", msg, want)
				}
			}
			if !reflect.DeepEqual(answer, dispatch.Answer{}) {
				t.Errorf("refusal returned %+v; nothing may be returned from the event store", answer)
			}

			// The running log keeps the difference too: the cap reads the log,
			// and a silent refusal would be a refusal nobody can find.
			if !logHasRefusal(logs, tc.field) {
				t.Errorf("the running log has no refusal naming %q", tc.field)
			}
		})
	}
}

// injectNodeRow writes one node-version row the way a diverged fold would: a
// real envelope, so the read derives state from it, and (when retiring is asked
// for) it retires the row it replaces.
func injectNodeRow(t *testing.T, p *pendingFixture, project, node, kind, payloadJSON, eventType, originID string, retire int64) {
	t.Helper()
	ms := p.h.Projection()
	extra := fmt.Sprintf(`{"event_id":%q,"project_id":%q,"event_type":%q,"fs_node":%q,"payload":%s}`,
		originID, project, eventType, node, payloadJSON)
	id, err := ms.Put(&memstore.Memory{
		Ts: time.Now().UTC(), Kind: kind, Scope: "fs:" + project, Text: node,
		Status: "pending", Extra: extra, FsNode: node,
		Origin: "test", OriginId: originID,
	})
	if err != nil {
		t.Fatalf("inject row: %v", err)
	}
	if retire != 0 {
		if err := ms.CloseMemory(retire, &id); err != nil {
			t.Fatalf("retire injected-into row: %v", err)
		}
	}
}

// captureLogs points the handler at a captured log stream and returns it, so a
// test can read what the running log recorded.
func captureLogs(t *testing.T, f *fixture) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	f.h.SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	return &buf
}

// logHasRefusal reports whether the captured running log recorded a refusal
// naming field — the live half of AC3, which requires the refusal to leave a
// line naming the read and the difference.
func logHasRefusal(log *bytes.Buffer, field string) bool {
	return strings.Contains(log.String(), field) && strings.Contains(log.String(), "refused")
}

// AC9 — no pending answer loses information to a missing memstore column.
func TestPendingGapFields(t *testing.T) {
	p := newPendingFixture(t)

	ms := p.h.Projection()
	scope, err := store.ReadScope(ms, "skills")
	if err != nil {
		t.Fatalf("read scope: %v", err)
	}
	gap := scope.Nodes[p.gapNode]
	if gap == nil {
		t.Fatalf("no projected gap node %s", p.gapNode)
	}
	if gap.Severity != string(query.SeveritySevere) {
		t.Errorf("projected severity = %q, want %q", gap.Severity, query.SeveritySevere)
	}
	if gap.Check != "true" {
		t.Errorf("projected check = %q, want %q", gap.Check, "true")
	}
	if gap.FoundBy != "cap:"+p.foundBy {
		t.Errorf("projected found_by = %q, want %q", gap.FoundBy, "cap:"+p.foundBy)
	}

	// The gap count, field by field: both sources count the same backlog.
	fromLog, err := dispatch.AnswerFromLog(p.h)
	if err != nil {
		t.Fatalf("event-store answer: %v", err)
	}
	fromProj, err := dispatch.AnswerFromProjection(p.h)
	if err != nil {
		t.Fatalf("projection answer: %v", err)
	}
	if fromLog.Gaps != 1 || fromProj.Gaps != 1 {
		t.Errorf("undispatched gaps = %d (event store) / %d (projection), want 1 and 1", fromLog.Gaps, fromProj.Gaps)
	}

	// The check-derived decoration: the check exits 0, so the claim is over and
	// every reader says so — the projection included, from the check parsed out
	// of its row rather than replayed from an event.
	info, found, err := p.h.NodeIn("skills", p.gapNode)
	if err != nil || !found {
		t.Fatalf("event-store node: found=%v err=%v", found, err)
	}
	fromStore := command.UnfinishedNode{
		ProjectID: "skills", NodeID: info.NodeID, Status: info.Status, Goal: info.Goal,
		Kind: info.Kind, FoundBy: info.FoundBy, Check: info.Check, BlockCheck: info.BlockCheck,
	}
	fromRow := dispatch.UnfinishedOf("skills", gap)
	if got, want := command.NodeStatus(fromRow, ""), command.NodeStatus(fromStore, ""); got != want {
		t.Errorf("check-derived status = %q from the projection, %q from the event store", got, want)
	}
	if !strings.Contains(command.NodeStatus(fromRow, ""), "claim no longer holds") {
		t.Errorf("gap check was not re-run: status = %q", command.NodeStatus(fromRow, ""))
	}
}

// AC5, runtime half — a representative write set leaves the projection at
// parity with the event store. The set is the one the spec names, and it ends
// with a REAL dispatch through the playbook machinery (prepare, the entry gate,
// the brief, the delivery record, the worker's node, the worker finishing), so
// `fs dispatch` is covered by running it and not only by the static check of its
// append sites.
func TestWriteSetStaysAtParity(t *testing.T) {
	f := newFixture(t, t.TempDir())
	h := f.h

	mustOK := func(what string, resp command.Response) command.EventData {
		t.Helper()
		if !resp.OK {
			t.Fatalf("%s: %s", what, resp.Error)
		}
		if data, ok := resp.Data.(command.EventData); ok {
			return data
		}
		return command.EventData{}
	}

	mustOK("project create", h.ProjectCreate("skills", t.TempDir()))

	// A real dispatch first, through the playbook machinery: prepared, gated,
	// delivered, and finished. The gate refuses while an unresolved dispatch sits
	// in the cap scope, so it runs before the hand-written cap nodes below.
	f.writePlaybook(t, "code-entry", passingEntry)
	runDispatch(t, f, devReq("a dispatched task"))

	work := mustOK("task add", h.TaskAddKind("skills", "a work node", query.KindWork, "", "", "", "", "", nil))
	mustOK("task update", h.TaskUpdate("skills", work.NodeID, "active", "a decision", nil))
	mustOK("task edit", h.TaskEdit("skills", work.NodeID, "an edited goal", query.KindWork, "", nil))
	mustOK("task block", h.TaskBlockWithCheck("skills", work.NodeID, "blocked on a probe", "true"))
	mustOK("task unblock", h.TaskUnblock("skills", work.NodeID))
	mustOK("task knowledge", h.KnowledgeAdd("skills", work.NodeID, "something learned"))

	survivor := mustOK("survivor add", h.TaskAddKind("skills", "a survivor", query.KindWork, "", "", "", "", "", nil))
	absorbed := mustOK("absorbed add", h.TaskAddKind("skills", "an absorbed node", query.KindWork, "", "", "", "", "", nil))
	if resp := h.TaskMerge("skills", absorbed.NodeID, survivor.NodeID, "folded in", nil); !resp.OK {
		t.Fatalf("task merge: %s", resp.Error)
	}

	mustOK("report", h.ReportAdd("skills", work.NodeID, "done", "finished the work"))
	mustOK("cap claim", h.ClaimCap("cap", command.CapClaim{PaneID: "w9:p54", TabID: "w9:t1"}))
	capNode := mustOK("cap dispatch node", h.TaskAddKind("cap", "dispatch code: sample", query.KindDispatch, "", "", "", "", "", nil))
	mustOK("record delivery", h.RecordDelivery("cap", capNode.NodeID, command.DeliveryRecord{
		PaneID: "w1:p1", Agent: "agent-1", Engine: "herdr", Project: "skills", Node: work.NodeID,
	}))
	if resp := h.RecordPlan("cap", capNode.NodeID, []byte(`{"playbooks":["code"]}`)); !resp.OK {
		t.Fatalf("record plan: %s", resp.Error)
	}

	// The projection is at parity with the event store after the whole set.
	issues, _, err := h.StateVerify("")
	if err != nil {
		t.Fatalf("state verify: %v", err)
	}
	if len(issues) != 0 {
		for _, issue := range issues {
			t.Errorf("after the write set: %s", issue)
		}
	}

	// And the read's own answer agrees, which is what the gate would compare.
	fromLog, err := dispatch.AnswerFromLog(h)
	if err != nil {
		t.Fatalf("event-store answer: %v", err)
	}
	fromProj, err := dispatch.AnswerFromProjection(h)
	if err != nil {
		t.Fatalf("projection answer: %v", err)
	}
	if diff, err := store.DiffJSON(fromLog, fromProj); err != nil {
		t.Fatalf("compare: %v", err)
	} else if diff != nil {
		t.Fatalf("the write set left the sources disagreeing at %s: event store %s, projection %s", diff.Field, diff.A, diff.B)
	}
}
