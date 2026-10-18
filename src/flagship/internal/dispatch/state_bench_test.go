package dispatch

import (
	"os"
	"path/filepath"
	"sync"
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
// Protocol, and it is the one MeasuredSwitch names:
//
//   - ONE load: the event store and the projection are opened once per process,
//     and the projection is rebuilt once before any measurement, so a sample is
//     never paying for the rebuild.
//   - N runs: -benchtime=20x, and -count=5 for a spread rather than one number.
//   - WARM: a few untimed iterations run before the timer starts, so the first
//     sample is not the page-cache fill.
//   - FS_PROJECTION_REAL_DB names a copy of ~/.fs/store.db; without it the
//     benchmark builds a small stream of its own, so it always runs.
func benchHandler(b *testing.B) *command.Handler {
	b.Helper()
	benchOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fs-bench-")
		if err != nil {
			benchErr = err
			return
		}
		benchDir = dir

		dbPath := filepath.Join(dir, "store.db")
		if src := os.Getenv("FS_PROJECTION_REAL_DB"); src != "" {
			dbPath = src
		}
		benchH, benchErr = command.NewHandler(dbPath)
		if benchErr != nil {
			return
		}
		if err := benchH.AttachProjection(filepath.Join(dir, "mem.db")); err != nil {
			benchErr = err
			return
		}
		if _, err := benchH.StateRebuild(""); err != nil {
			benchErr = err
			return
		}
		if os.Getenv("FS_PROJECTION_REAL_DB") == "" {
			seedPendingStream(b, benchH)
		}
	})
	if benchErr != nil {
		b.Fatalf("benchmark fixture: %v", benchErr)
	}
	return benchH
}

var (
	benchOnce sync.Once
	benchH    *command.Handler
	benchErr  error
	benchDir  string
)

// cleanupBenchFixture removes the shared fixture's temp dir and closes its
// handler. The fixture is built once per process and both benchmarks share it,
// so it cannot be cleaned up by either of them: TestMain does it, after the
// whole run.
func cleanupBenchFixture() {
	if benchH != nil {
		benchH.Close()
	}
	if benchDir != "" {
		os.RemoveAll(benchDir)
	}
}

// TestMain runs the package's tests and then cleans the benchmark fixture, which
// outlives every individual benchmark because it is shared between them.
func TestMain(m *testing.M) {
	code := m.Run()
	cleanupBenchFixture()
	os.Exit(code)
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
	// The worker's node is created first and its id is what the delivery record
	// and the report name. A literal id here would name a node that does not
	// exist, and the fixture would fail before it measured anything — which is
	// what made this benchmark unusable without FS_PROJECTION_REAL_DB.
	worker := add("skills", "worker goal", query.KindWork)
	if r := h.RecordDelivery("cap", capNode, command.DeliveryRecord{
		PaneID: "w1:p1", Agent: "agent", Engine: "herdr", Project: "skills", Node: worker,
	}); !r.OK {
		b.Fatalf("record delivery: %s", r.Error)
	}
	if r := h.ReportAdd("skills", worker, "question", "one line"); !r.OK {
		b.Fatalf("report: %s", r.Error)
	}
	add("skills", "a gap", query.KindGap)
}

// BenchmarkPendingProjectionCost is the projection arm alone: the read a
// switched fs pending makes of the memstore projection, with no event store
// replay in it.
func BenchmarkPendingProjectionCost(b *testing.B) {
	h := benchHandler(b)
	for i := 0; i < 3; i++ { // warm
		if _, err := answerFromProjection(h); err != nil {
			b.Fatalf("projection answer: %v", err)
		}
	}
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
	for i := 0; i < 3; i++ { // warm
		if _, err := answerFromLog(h); err != nil {
			b.Fatalf("log answer: %v", err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := answerFromLog(h); err != nil {
			b.Fatalf("log answer: %v", err)
		}
	}
}
