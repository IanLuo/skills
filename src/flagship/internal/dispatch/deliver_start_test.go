package dispatch

import "testing"

// startHerdr is a HerdrCLI whose StartAgent answers err for its first `busy`
// calls and succeeds after that — herdr refusing a pane that is not yet a shell,
// with the refusal made deterministic. Only StartAgent is ever called: the
// embedded interface is nil, so a retry that reached any other method would panic
// the test rather than quietly pass it.
type startHerdr struct {
	HerdrCLI
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

// A pane split a moment ago is not yet at a shell prompt, and herdr starts an
// agent nowhere else — so a delivery that follows a split loses a race that a
// person copying the printed line loses too. The start is asked again until the
// pane is ready.
func TestStartAgentRetriesWhileThePaneIsNotYetAShell(t *testing.T) {
	noBackoff(t)

	hc := &startHerdr{err: &HerdrError{Code: "agent_pane_busy"}, busy: 2}
	if err := startAgent(hc, "w1:p9", "dispatch-dev-task", "claude", nil); err != nil {
		t.Fatalf("startAgent: %v", err)
	}
	if hc.calls != 3 {
		t.Errorf("start attempts = %d, want 3: two refusals and the one that took", hc.calls)
	}
}

// The retry is bounded, and when it runs out it reports what herdr said rather
// than a failure of its own: a pane that never becomes a shell is herdr's
// answer to give, and it is the one a reader can act on.
func TestStartAgentGivesUpAndReportsHerdrsOwnError(t *testing.T) {
	noBackoff(t)

	hc := &startHerdr{err: &HerdrError{Code: "agent_pane_busy"}, busy: agentStartAttempts + 5}
	err := startAgent(hc, "w1:p9", "dispatch-dev-task", "claude", nil)
	if herdrErrorCode(err) != "agent_pane_busy" {
		t.Errorf("err = %v, want herdr's agent_pane_busy", err)
	}
	if hc.calls != agentStartAttempts {
		t.Errorf("start attempts = %d, want the bound %d", hc.calls, agentStartAttempts)
	}
}

// A name herdr will never accept is not a pane that is not ready yet. Asking
// again would turn one refusal into twenty of the same, slower one.
func TestStartAgentDoesNotRetryWhatIsNotARace(t *testing.T) {
	noBackoff(t)

	hc := &startHerdr{err: &HerdrError{Code: "invalid_agent_name"}, busy: 1}
	err := startAgent(hc, "w1:p9", "dispatch-dev-task-", "claude", nil)
	if herdrErrorCode(err) != "invalid_agent_name" {
		t.Errorf("err = %v, want herdr's invalid_agent_name", err)
	}
	if hc.calls != 1 {
		t.Errorf("start attempts = %d, want 1: an invalid name does not become valid", hc.calls)
	}
}
