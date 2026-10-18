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

import "os"

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
// TestPendingParityRefuses, TestPendingGapFields all drive it), but the spec's
// performance ceiling is not met on the live store:
//
//	projection-derived answer   83ms   (1.71x the event store's 48ms)
//	gated read                  160ms  (2.29x the event store's 70ms)
//
// AC8 allows 2x with the gate on and 1.25x for the projection alone, and the
// spec's D6 says a read that regresses is not kept switched past the gate. The
// cause is memstore's read surface, not this code: Recent(scope, k) is the only
// scope-listing verb, it carries no index on scope, and it sorts what it
// returns — so every projection read scans and sorts the whole memory file,
// while the event store's Replay walks a primary key. Ending the whole-history
// replay is the switch's whole purchase, and it has not been bought.
//
// Turning it back on is this one line — `[]string{"pending"}` — once the read
// surface can answer a scope without scanning the file. Everything else the
// read needs is in place.
var switchedReads []string

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
