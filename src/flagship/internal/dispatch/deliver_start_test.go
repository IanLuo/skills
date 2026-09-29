package dispatch

import (
	"errors"
	"testing"

	"github.com/flagship-dev/flagship/internal/engine"
)

// startHerdr is an engine.Engine whose StartAgent answers err for its first
// `busy` calls and succeeds after that — the pane that is not yet a shell,
// with the refusal made deterministic. Only StartAgent is ever called: the
// embedded interface is nil, so a retry that reached any other method would panic
// the test rather than quietly pass it.
type startHerdr struct {
	engine.Engine
	err   error
	busy  int
	calls int
}

func (s *startHerdr) StartAgent(paneID, name, kind string, args []string) error {
	s.calls++
	if s.calls <= s.busy {
		return s.err
	}
	return nil
}

// noBackoff takes the wait out of the retry for one test: the attempt count is
// what is under test, not the wall clock.
func noBackoff(t *testing.T) {
	t.Helper()
	previous := agentStartBackoff
	agentStartBackoff = 0
	t.Cleanup(func() { agentStartBackoff = previous })
}

// A pane split a moment ago is not yet at a shell prompt, and the engine starts
// an agent nowhere else — so a delivery that follows a split is refused until
// the pane is ready. The start is asked again until it takes.
func TestStartAgentRetriesWhileThePaneIsNotYetAShell(t *testing.T) {
	noBackoff(t)

	hc := &startHerdr{err: engine.ErrPaneBusy, busy: 2}
	if err := startAgent(hc, "w1:p9", "dispatch-dev-task", "claude", nil); err != nil {
		t.Fatalf("startAgent: %v", err)
	}
	if hc.calls != 3 {
		t.Errorf("start attempts = %d, want 3: two refusals and the one that took", hc.calls)
	}
}

// The retry is bounded, and when it runs out it reports what the engine said
// rather than a failure of its own: a pane that never becomes a shell is the
// engine's answer to give, and it is the one a reader can act on.
func TestStartAgentGivesUpAndReportsEnginesOwnError(t *testing.T) {
	noBackoff(t)

	hc := &startHerdr{err: engine.ErrPaneBusy, busy: agentStartAttempts + 5}
	err := startAgent(hc, "w1:p9", "dispatch-dev-task", "claude", nil)
	if !errors.Is(err, engine.ErrPaneBusy) {
		t.Errorf("err = %v, want the engine's own ErrPaneBusy", err)
	}
	if hc.calls != agentStartAttempts {
		t.Errorf("start attempts = %d, want the bound %d", hc.calls, agentStartAttempts)
	}
}

// A name the engine will never accept is not a pane that is not ready yet.
// Asking again would turn one refusal into twenty of the same, slower one.
func TestStartAgentDoesNotRetryWhatIsNotARace(t *testing.T) {
	noBackoff(t)

	invalid := errors.New("the engine refused the name as ungrammatical")
	hc := &startHerdr{err: invalid, busy: 1}
	err := startAgent(hc, "w1:p9", "dispatch-dev-task-", "claude", nil)
	if !errors.Is(err, invalid) {
		t.Errorf("err = %v, want the engine's own refusal", err)
	}
	if hc.calls != 1 {
		t.Errorf("start attempts = %d, want 1: an invalid name does not become valid", hc.calls)
	}
}
