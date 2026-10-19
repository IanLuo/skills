// Which source a state read answers from.
//
// Slice 2's rule (spec D2): the source is a property of the read, never a flag
// and never a selector in the JSON envelope. The declaration below names the
// reads that answer from the projection — exactly one, `pending` — and every
// other state read answers from the event store until a later card switches it
// with its own gate.
//
// A switched read always runs its parity gate, and the gate's refusal is the
// only response to a divergence. The one override is FS_STATE_SOURCE=log, a
// person's explicit per-process rollback: it answers from the event store, skips
// the gate, and records source=log. Nothing sets it on a divergence.
package command

import (
	"fmt"
	"os"
)

// The two sources a state read can answer from, as the running log records them.
const (
	// SourceProjection is a switched read's answer: derived from the memstore
	// projection, and only returned when the parity gate found it equal to the
	// event store's.
	SourceProjection = "projection"
	// SourceLog is the event store's answer: a read that is not switched, or
	// any read while FS_STATE_SOURCE=log.
	SourceLog = "log"
)

// StateSourceEnv is the per-process rollback. Its only recognised value is
// "log"; anything else leaves every read on its declared source, because a
// typo must not silently switch the whole store layer.
const StateSourceEnv = "FS_STATE_SOURCE"

// switchedReads is the declaration AC1 asks for, and it is the whole of what
// "switched" means mechanically: a read named here answers from the projection.
// There is no flag and no env var that switches a read on — the rollback above
// only turns the switch off.
//
// It contains `pending` because AC8's ceiling is met now that memstore's
// `Recent(scope, k)` has a covering index (`b5be5fd`, schema 6,
// `idx_memory_scope_recent`): before that pin the projection-alone arm scanned
// and sorted the whole memory file and measured 1.26-1.40x, over the 1.25x
// ceiling, so the declaration was empty. See MeasuredSwitch below for the
// numbers and the protocol. TestSwitchedReads enforces the rule in both
// directions, so the declaration and the measurement cannot drift apart: this
// line is the only thing that turns `fs pending` on.
var switchedReads = []string{"pending"}

// SwitchMeasurement is AC8's measurement on the live store, recorded as data so
// the declaration and the measurement cannot drift apart: TestSwitchedReads
// enforces the rule (spec D6) that a read is switched only while the measurement
// is inside the ceiling.
type SwitchMeasurement struct {
	// Read is the read the measurement is about.
	Read string
	// ProjectionRatio is the projection-derived answer against the event store's,
	// and GateRatio is the switched read with its gate on against the event
	// store's. CeilingProjection and CeilingGate are AC8's two ceilings.
	ProjectionRatio   float64
	GateRatio         float64
	CeilingProjection float64
	CeilingGate       float64
	// The raw medians the ratios came from, in milliseconds, so a reader can
	// check the arithmetic and see how far apart the two arms are.
	ProjectionMillis float64
	LogMillis        float64
	GateMillis       float64
	LogOnlyMillis    float64
	// Protocol is how the numbers were produced, so they can be reproduced or
	// refuted rather than taken on trust.
	Protocol string
}

// WithinCeiling reports whether both of AC8's arms are inside their ceilings.
func (m SwitchMeasurement) WithinCeiling() bool {
	return m.ProjectionRatio <= m.CeilingProjection && m.GateRatio <= m.CeilingGate
}

func (m SwitchMeasurement) String() string {
	return fmt.Sprintf("%s: projection-alone %.2fx (ceiling %.2fx), gated %.2fx (ceiling %.2fx)",
		m.Read, m.ProjectionRatio, m.CeilingProjection, m.GateRatio, m.CeilingGate)
}

// MeasuredSwitch is AC8's measurement as taken on the live store, and it is
// what justifies the declaration above: both arms are inside their ceilings
// after the memstore re-vendor, so D6 keeps the read switched.
func MeasuredSwitch() SwitchMeasurement {
	return SwitchMeasurement{
		Read:              "pending",
		ProjectionRatio:   0.54,
		GateRatio:         1.36,
		CeilingProjection: 1.25,
		CeilingGate:       2.00,
		ProjectionMillis:  28.5,
		LogMillis:         52.5,
		GateMillis:        108.6,
		LogOnlyMillis:     80.0,
		Protocol: "in-process arms: go test ./internal/dispatch -run '^$' " +
			"-bench BenchmarkPendingProjectionCost|BenchmarkPendingLogCost -benchtime=20x -count=5 " +
			"with FS_PROJECTION_REAL_DB=a copy of ~/.fs/store.db, one store and one projection opened " +
			"per process and rebuilt once before the loop, 3 untimed warm iterations, 5 samples of 20, " +
			"medians reported; two independent runs: projection 28.7ms vs log 53.1ms (0.54x) and " +
			"projection 28.2ms vs log 51.9ms (0.54x); CLI arms: the same commit built with " +
			"switchedReads={pending}, 3 untimed warm runs then 20 timed runs per arm against a copy of " +
			"the live store under an isolated HOME, wall time via python time.perf_counter around the " +
			"process, medians and spread reported (gate 108.6ms, spread 3.0ms; log-only 80.0ms, spread " +
			"8.8ms). Both ratios are load- and machine-sensitive; the projection arm is inside its " +
			"ceiling in every sample taken here, where before the memstore b5be5fd pin every sample was " +
			"over it",
	}
}

// stateReads is every read whose source the running log records (D2). It is the
// set the log's `source` field makes comparable: a switched read says
// "projection", the rest say "log", so AC1 can tell which reads are switched
// from the log alone.
var stateReads = []string{"pending", "status", "gaps", "unfinished", "task get"}

// SwitchedReads returns the switched-read declaration. It returns a copy so a
// caller cannot widen it.
func SwitchedReads() []string {
	out := make([]string, len(switchedReads))
	copy(out, switchedReads)
	return out
}

// Switched reports whether a read answers from the projection by declaration.
func Switched(read string) bool {
	for _, r := range switchedReads {
		if r == read {
			return true
		}
	}
	return false
}

// IsStateRead reports whether a read records its source on the running log.
func IsStateRead(read string) bool {
	for _, r := range stateReads {
		if r == read {
			return true
		}
	}
	return false
}

// StateSource names where a read answers from: SourceProjection for a switched
// read, SourceLog for every other — and SourceLog for every read while
// FS_STATE_SOURCE=log, which is the field rollback.
func StateSource(read string) string {
	if os.Getenv(StateSourceEnv) == SourceLog {
		return SourceLog
	}
	if Switched(read) {
		return SourceProjection
	}
	return SourceLog
}
