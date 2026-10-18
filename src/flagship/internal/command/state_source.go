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
// It is EMPTY, and that is slice 2's measured outcome, not an oversight. The
// read is built and its parity gate is proven (TestPendingParity,
// TestPendingParityRefuses, TestPendingGapFields all drive it), but AC8's
// ceiling is not met on the live store — see MeasuredSwitch below for the
// numbers and the protocol — and the spec's D6 says a read that regresses is not
// kept switched past the gate. TestSwitchedReads enforces that rule in both
// directions, so the declaration and the measurement cannot drift apart.
//
// The cause is memstore's read surface, not this code: Recent(scope, k) is the
// only scope-listing verb, it carries no index on scope, and it sorts what it
// returns — so every projection read scans and sorts the whole memory file,
// while the event store's Replay walks a primary key. Ending the whole-history
// replay is the switch's whole purchase, and it has not been bought.
//
// Turning it back on is this one line — `[]string{"pending"}` — once the read
// surface can answer a scope without scanning the file. Everything else the
// read needs is in place.
var switchedReads []string

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

// MeasuredSwitch is AC8's measurement as taken on the live store, and it is the
// reason the declaration above is empty: the projection-alone arm is over its
// ceiling, so D6 reverts the read. The gate-on arm is inside its ceiling — the
// switch-off conclusion rests on the 1.25x arm alone, by a few percent.
func MeasuredSwitch() SwitchMeasurement {
	return SwitchMeasurement{
		Read:              "pending",
		ProjectionRatio:   1.40,
		GateRatio:         1.89,
		CeilingProjection: 1.25,
		CeilingGate:       2.00,
		ProjectionMillis:  87.0,
		LogMillis:         62.2,
		GateMillis:        170,
		LogOnlyMillis:     90,
		Protocol: "in-process arms: go test ./internal/dispatch -run '^$' " +
			"-bench BenchmarkPendingProjectionCost|BenchmarkPendingLogCost -benchtime=20x -count=5 " +
			"with FS_PROJECTION_REAL_DB=a copy of ~/.fs/store.db, one store and one projection opened " +
			"per process and rebuilt once before the loop, 3 untimed warm iterations, 5 samples of 20, " +
			"medians reported (projection 86.3-89.8ms, log 61.8-63.0ms); " +
			"CLI arms: the same commit built with switchedReads={pending}, 20 warm runs per arm at the " +
			"live store, /usr/bin/time -p, medians reported (gate 170ms, log-only 90ms; one gate " +
			"outlier at 1310ms). Both ratios are load- and machine-sensitive: an independent run of " +
			"the same benchmark measured 1.26-1.28x on the projection arm, so the switch-off " +
			"conclusion rests on that arm being over its ceiling in every measurement taken, not on " +
			"one number",
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
