package dispatch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/query"
)

// The two arms AC8 bounds, measured in-process so the numbers are the reads and
// not the process start.
//
//	BenchmarkPendingProjectionCost  the projection-derived answer alone
//	BenchmarkPendingLogCost         the event-store answer alone
//
// The switched read's own arm is their sum plus the comparison, which is what
// the 2x ceiling in AC8 is about. The projection-alone ceiling (1.25x) is the
// first of these against the second.
//
// With FS_PROJECTION_REAL_DB naming a copy of ~/.fs/store.db the benchmark runs
// over the real stream — the shape the ceiling was set against. Without it the
// benchmark builds a small stream of its own, so it always runs and always
// compares like with like.
func benchHandler(b *testing.B) *command.Handler {
	b.Helper()

	dir := b.TempDir()
	dbPath := filepath.Join(dir, "store.db")
	src := os.Getenv("FS_PROJECTION_REAL_DB")
	if src != "" {
		dbPath = src
	}
	h, err := command.NewHandler(dbPath)
	if err != nil {
		b.Fatalf("NewHandler: %v", err)
	}
	b.Cleanup(func() { h.Close() })

	if err := h.AttachProjection(filepath.Join(dir, "mem.db")); err != nil {
		b.Fatalf("AttachProjection: %v", err)
	}
	if _, err := h.StateRebuild(""); err != nil {
		b.Fatalf("StateRebuild: %v", err)
	}
	if src == "" {
		seedPendingStream(b, h)
	}
	return h
}

// seedPendingStream writes a stream with the shapes fs pending reads: a cap
// dispatch, its delivery record, the worker's node, a report, and a gap.
func seedPendingStream(b *testing.B, h *command.Handler) {
	b.Helper()
	add := func(project, goal string, kind query.Kind) string {
		r := h.TaskAddKind(project, goal, kind, "", "", "", "", "", nil)
		if !r.OK {
			b.Fatalf("task add %s: %s", goal, r.Error)
		}
		return r.Data.(command.EventData).NodeID
	}
	capNode := add("cap", "dispatch code: sample", query.KindDispatch)
	if r := h.RecordDelivery("cap", capNode, command.DeliveryRecord{
		PaneID: "w1:p1", Agent: "agent", Engine: "herdr", Project: "skills", Node: "t-worker",
	}); !r.OK {
		b.Fatalf("record delivery: %s", r.Error)
	}
	add("skills", "worker goal", query.KindWork)
	if r := h.ReportAdd("skills", "t-worker", "question", "one line"); !r.OK {
		b.Fatalf("report: %s", r.Error)
	}
	add("skills", "a gap", query.KindGap)
}

// BenchmarkPendingProjectionCost is the projection arm alone: the read a
// switched fs pending makes of the memstore projection, with no event store
// replay in it.
func BenchmarkPendingProjectionCost(b *testing.B) {
	h := benchHandler(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := answerFromProjection(h); err != nil {
			b.Fatalf("projection answer: %v", err)
		}
	}
}

// BenchmarkPendingLogCost is the event-store arm alone: the answer every
// unswitched state read derives by replaying the store and building the tree.
func BenchmarkPendingLogCost(b *testing.B) {
	h := benchHandler(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := answerFromLog(h); err != nil {
			b.Fatalf("log answer: %v", err)
		}
	}
}
