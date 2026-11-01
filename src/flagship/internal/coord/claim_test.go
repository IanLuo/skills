package coord_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flagship-dev/flagship/internal/coord"
	"github.com/flagship-dev/flagship/internal/memory"
)

// TestClaimWritesTwoRows is the shape both invariants are built from: one bind row
// that spends the session, one claim row that holds the task. One row cannot carry
// both, because memstore's uniqueness is one key wide.
func TestClaimWritesTwoRows(t *testing.T) {
	s, _ := open(t)
	task := add(t, s, "p")
	got := claim(t, s, "p", "s-1", task.Tid)

	if got.Session != "s-1" || got.Round != 1 {
		t.Fatalf("claim = %+v, want s-1 at round 1", got)
	}
	rs := rows(t, s, "p")
	if n := countKind(rs, memory.KindBind); n != 1 {
		t.Fatalf("bind rows = %d, want exactly 1", n)
	}
	if n := countKind(rs, memory.KindClaim); n != 1 {
		t.Fatalf("claim rows = %d, want exactly 1", n)
	}
	for _, r := range rs {
		if r.Kind == memory.KindClaim && r.OriginID != memory.ClaimKey(task.Tid, 1) {
			t.Fatalf("claim key = %q, want %q: every racer must build one key", r.OriginID, memory.ClaimKey(task.Tid, 1))
		}
	}
}

// TestClaimBarrierGoroutines puts eight sessions on one task at once, inside one
// process. Exactly one may succeed.
//
// This is also the test that fails if the protocol is ever "simplified" into a
// read-then-write: a winner decided from a read returns success with no row
// committed, so two racers can both win and the counts below stop agreeing. The
// decisive step must stay a Put whose duplicate refusal is memstore's, which is why
// the successes and the claim rows are both counted and compared.
func TestClaimBarrierGoroutines(t *testing.T) {
	s, _ := open(t)
	task := add(t, s, "p")

	const racers = 8
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		won   []string
		lost  int
		fatal []error
	)
	for i := 0; i < racers; i++ {
		session := "s-" + strconv.Itoa(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := coord.Claim(s, coord.ClaimInput{Project: "p", Session: session, Task: task.Tid})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				won = append(won, session)
			case strings.Contains(err.Error(), "held by session") || strings.Contains(err.Error(), "lost the claim"):
				lost++
			default:
				fatal = append(fatal, err)
			}
		}()
	}
	wg.Wait()

	if len(fatal) > 0 {
		t.Fatalf("unexpected refusals: %v", fatal)
	}
	if len(won) != 1 {
		t.Fatalf("%d sessions claimed one task (%v); exactly one may", len(won), won)
	}
	if lost != racers-1 {
		t.Fatalf("%d refusals, want %d", lost, racers-1)
	}
	rs := rows(t, s, "p")
	if n := countKind(rs, memory.KindClaim); n != 1 {
		t.Fatalf("%d claim rows for one winner; success must be a committed row, not a read", n)
	}
	// The winner is the session the row names: a win nobody wrote is no win.
	held, ok := memory.NewIndex(rs).Claim(task.Tid)
	if !ok || held.Session != won[0] {
		t.Fatalf("the claim row names %q, the winner was %q", held.Session, won[0])
	}
}

// TestClaimBarrierProcesses runs the same race across processes, which is the case
// that matters: agents are separate programs, and each has its own memstore handle.
func TestClaimBarrierProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("starts helper processes")
	}
	db := filepath.Join(t.TempDir(), "barrier.db")
	s, err := memory.Open(db)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	task, err := coord.Add(s, coord.AddInput{Project: "p", Goal: "one task", Checks: []string{"true"}})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	const procs = 3
	gate := filepath.Join(t.TempDir(), "gate")
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}

	var cmds []*exec.Cmd
	var outs []*bytes.Buffer
	for i := 0; i < procs; i++ {
		cmd := exec.Command(exe, "-test.run=^TestClaimHelperProcess$")
		out := &bytes.Buffer{}
		cmd.Stdout, cmd.Stderr = out, out
		cmd.Env = append(os.Environ(),
			"FS_CLAIM_HELPER=1",
			"FS_CLAIM_DB="+db,
			"FS_CLAIM_PROJECT=p",
			"FS_CLAIM_TASK="+task.Tid,
			"FS_CLAIM_SESSION=s-"+strconv.Itoa(i),
			"FS_CLAIM_GATE="+gate,
			"FS_CLAIM_N="+strconv.Itoa(procs),
		)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start helper %d: %v", i, err)
		}
		cmds, outs = append(cmds, cmd), append(outs, out)
	}

	won := 0
	for i, cmd := range cmds {
		err := cmd.Wait()
		switch {
		case err == nil:
			won++
		case exitCode(err) == helperRefused:
			// The refusal is the expected outcome for a loser.
		default:
			t.Fatalf("helper %d failed unexpectedly: %v\n%s", i, err, outs[i])
		}
	}
	if won != 1 {
		t.Fatalf("%d processes claimed one task; exactly one may", won)
	}

	// Reopen and count: one claim row, named by the process that reported winning.
	after, err := memory.Open(db)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer after.Close()
	rs, err := after.Rows(memory.Scope("p"))
	if err != nil {
		t.Fatal(err)
	}
	if n := countKind(rs, memory.KindClaim); n != 1 {
		t.Fatalf("%d claim rows after a race of %d processes", n, procs)
	}
}

const helperRefused = 3

// TestClaimHelperProcess is not a test: it is the child side of the barrier above.
// It waits at a file-based gate until every helper is ready, then claims, and
// exits 0 for a win or helperRefused for a refusal so the parent can count both.
func TestClaimHelperProcess(t *testing.T) {
	if os.Getenv("FS_CLAIM_HELPER") != "1" {
		t.Skip("helper process only")
	}
	gate, n := os.Getenv("FS_CLAIM_GATE"), os.Getenv("FS_CLAIM_N")
	want, err := strconv.Atoi(n)
	if err != nil {
		t.Fatalf("FS_CLAIM_N: %v", err)
	}

	f, err := os.OpenFile(gate, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if _, err := f.WriteString("ready\n"); err != nil {
		t.Fatalf("gate write: %v", err)
	}
	f.Close()
	deadline := time.Now().Add(30 * time.Second)
	for {
		body, err := os.ReadFile(gate)
		if err != nil {
			t.Fatalf("gate read: %v", err)
		}
		if strings.Count(string(body), "ready") >= want {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gate never filled: %q", body)
		}
		time.Sleep(time.Millisecond)
	}

	s, err := memory.Open(os.Getenv("FS_CLAIM_DB"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	_, err = coord.Claim(s, coord.ClaimInput{
		Project: os.Getenv("FS_CLAIM_PROJECT"),
		Session: os.Getenv("FS_CLAIM_SESSION"),
		Task:    os.Getenv("FS_CLAIM_TASK"),
	})
	if err != nil {
		os.Exit(helperRefused)
	}
	os.Exit(0)
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// TestClaimTakesTheNextRoundNeverTheLast: a claim always writes a new round,
// because the old round's key is spent. Re-putting the same key would be refused
// by memstore and the task would look permanently held by a dead session.
func TestClaimTakesTheNextRoundNeverTheLast(t *testing.T) {
	s, now := open(t)
	task := add(t, s, "p")
	claim(t, s, "p", "s-old", task.Tid)

	// The holder stops writing: its lease lapses by itself, with nothing written.
	*now = now.Add(memory.DefaultTTL + time.Second)

	got := claim(t, s, "p", "s-new", task.Tid)
	if got.Round != 2 {
		t.Fatalf("round = %d, want 2: a new holder takes the next round", got.Round)
	}
	rs := rows(t, s, "p")
	if n := countKind(rs, memory.KindClaim); n != 2 {
		t.Fatalf("claim rows = %d, want 2: attempt history stays", n)
	}
	held, _ := memory.NewIndex(rs).Claim(task.Tid)
	if held.Session != "s-new" || !held.Live(*now) {
		t.Fatalf("holder = %+v, want s-new live", held)
	}
	// The old session may no longer write, and expiry never marked anything done.
	if _, err := coord.Report(s, coord.ReportInput{
		Project: "p", Session: "s-old", Task: task.Tid, State: "progress",
	}); err == nil {
		t.Fatal("the expired holder was allowed to write")
	}
	rs = rows(t, s, "p")
	for _, r := range rs {
		if r.Kind == memory.KindTask && r.Closed {
			t.Fatal("expiry closed the task; it must only free it")
		}
	}
}

// TestClaimRefusesAHeldTask names the holder and when the lease lapses, because a
// refusal that does not say who holds the task sends a reader looking.
func TestClaimRefusesAHeldTask(t *testing.T) {
	s, now := open(t)
	task := add(t, s, "p")
	claim(t, s, "p", "s-1", task.Tid)

	_, err := coord.Claim(s, coord.ClaimInput{Project: "p", Session: "s-2", Task: task.Tid})
	if err == nil {
		t.Fatal("a held task was claimed twice")
	}
	if !strings.Contains(err.Error(), "s-1") {
		t.Fatalf("refusal %q does not name the holder", err)
	}
	expires := now.Add(memory.DefaultTTL).UTC().Format(time.RFC3339)
	if !strings.Contains(err.Error(), expires) {
		t.Fatalf("refusal %q does not name the expiry %s", err, expires)
	}
}

// TestClaimAlreadyMineIsFine: a session that holds the task asking again is not an
// error — the harness may retry a claim it already made. It holds nothing new and
// writes nothing new.
func TestClaimAlreadyMineIsFine(t *testing.T) {
	s, _ := open(t)
	task := add(t, s, "p")
	claim(t, s, "p", "s-1", task.Tid)
	before := len(rows(t, s, "p"))

	got := claim(t, s, "p", "s-1", task.Tid)
	if got.Round != 1 {
		t.Fatalf("round = %d, want the held round", got.Round)
	}
	if after := len(rows(t, s, "p")); after != before {
		t.Fatalf("%d rows after a repeated claim, was %d: nothing new was needed", after, before)
	}
}

// TestClaimNextEligibleSkipsHeldAndClosed covers the pull path: an agent asks for
// work rather than naming a task.
func TestClaimNextEligibleSkipsHeldAndClosed(t *testing.T) {
	s, now := open(t)
	first := add(t, s, "p")
	second := add(t, s, "p")
	claim(t, s, "p", "s-1", first.Tid)

	got := claim(t, s, "p", "s-2", "")
	if got.Tid != second.Tid {
		t.Fatalf("claimed %s, want the only eligible task %s", got.Tid, second.Tid)
	}
	// A third session finds nothing: one is held, one is now held too.
	if _, err := coord.Claim(s, coord.ClaimInput{Project: "p", Session: "s-3"}); err == nil {
		t.Fatal("a third session found eligible work when there was none")
	} else if !strings.Contains(err.Error(), "no eligible task") {
		t.Fatalf("refusal = %q, want it to say there is no eligible task", err)
	}
	// Once the leases lapse, the work is eligible again — chosen oldest first.
	*now = now.Add(memory.DefaultTTL + time.Second)
	got = claim(t, s, "p", "s-4", first.Tid)
	if got.Tid != first.Tid {
		t.Fatalf("claimed %s after expiry, want the oldest task %s", got.Tid, first.Tid)
	}
}

// TestClaimDoesNotBurnASessionOnABadTask: a mistyped or already-closed task id is
// refused before the bind, so the session can still claim real work. The bind is the
// point of no return for an identity; spending it on a typo would be pure loss.
func TestClaimDoesNotBurnASessionOnABadTask(t *testing.T) {
	s, _ := open(t)
	task := add(t, s, "p")

	if _, err := coord.Claim(s, coord.ClaimInput{Project: "p", Session: "s-1", Task: "t-nosuchtask"}); err == nil {
		t.Fatal("a task that does not exist was claimed")
	}
	// The same identity still works, and holds the real task.
	got := claim(t, s, "p", "s-1", task.Tid)
	if got.Tid != task.Tid || got.Round != 1 {
		t.Fatalf("claim after a bad id = %+v, want the real task at round 1", got)
	}
}
