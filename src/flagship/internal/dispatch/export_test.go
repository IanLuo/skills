package dispatch

// Exported seams for the tests in package dispatch_test.
//
// The parity gate is reached from the CLI only by a switched read, and slice 2's
// switch is off: the projection arm measured 1.71x the event store and the gated
// read 2.29x, over AC8's ceiling, so the declaration is empty (see
// internal/command/state_source.go). The gate is still the contract this slice
// delivers, so the tests drive it directly here rather than through a read that
// is deliberately not switched.
// Answer is the read's store-derived answer, exported so the tests can compare
// the two derivations field by field.
type Answer = pendingAnswer

var (
	AnswerFromLog        = answerFromLog
	AnswerFromProjection = answerFromProjection
	GatedAnswer          = gatedAnswer
	DeliveryOf           = deliveryOf
	ReportsOf            = reportsOf
	CapOf                = capOf
	UnfinishedOf         = unfinishedOf
)
