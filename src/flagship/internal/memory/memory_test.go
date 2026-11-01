package memory_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/flagship-dev/flagship/internal/memory"
)

// open returns a store on a scratch memstore file. Every test gets its own: they
// are cheap, and a shared one would make a duplicate-key refusal ambiguous.
func open(t *testing.T) *memory.Store {
	t.Helper()
	s, err := memory.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestKeysRoundTrip(t *testing.T) {
	cases := []struct {
		key     string
		task    string
		session string
	}{
		{memory.TaskKey("t-1"), "t-1", ""},
		{memory.SessionKey("s-1"), "", "s-1"},
		{memory.ClaimKey("t-1", 3), "t-1", ""},
		{memory.EventKey("t-1", "s-1", 2), "t-1", "s-1"},
	}
	for _, c := range cases {
		if got, ok := memory.ParseTask(c.key); ok != (c.task != "") || got != c.task {
			t.Errorf("%s: task = %q/%v, want %q", c.key, got, ok, c.task)
		}
		if got, ok := memory.ParseSession(c.key); ok != (c.session != "") || got != c.session {
			t.Errorf("%s: session = %q/%v, want %q", c.key, got, ok, c.session)
		}
	}
	if round, ok := memory.ParseRound(memory.ClaimKey("t-1", 3)); !ok || round != 3 {
		t.Errorf("round = %d/%v, want 3", round, ok)
	}
	// A claim key must not name a session: every racer has to build the same one.
	if sid, ok := memory.ParseSession(memory.ClaimKey("t-1", 1)); ok || sid != "" {
		t.Errorf("a claim key named session %q; two racers would then build different keys", sid)
	}
}

func TestPutThenRows(t *testing.T) {
	s := open(t)
	scope := memory.Scope("p")
	if err := s.PutTask(scope, "t-1", "the goal", `{"goal":"the goal"}`); err != nil {
		t.Fatalf("put task: %v", err)
	}
	rows, err := s.Rows(scope)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(rows) != 1 || rows[0].Task() != "t-1" || rows[0].Kind != memory.KindTask || rows[0].Closed {
		t.Fatalf("rows = %+v, want one open task row for t-1", rows)
	}
}

// TestDuplicateKeyIsRefused pins the primitive both invariants rest on. If a
// memstore release stops refusing a duplicate (origin, origin_id) inside its own
// transaction, this fails and fs must stop assigning work.
func TestDuplicateKeyIsRefused(t *testing.T) {
	s := open(t)
	scope := memory.Scope("p")
	if err := s.PutBind(scope, "s-1", "t-1"); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	err := s.PutBind(scope, "s-1", "t-2")
	if !errors.Is(err, memory.ErrDuplicate) {
		t.Fatalf("second bind of one session = %v, want ErrDuplicate", err)
	}
}

func TestRowsOfAnotherScopeAreNotReturned(t *testing.T) {
	s := open(t)
	if err := s.PutTask(memory.Scope("p"), "t-1", "goal", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutTask("someone-else", "t-2", "their goal", "{}"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.AllRows()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Scope != memory.Scope("p") {
			t.Fatalf("AllRows returned a row outside fs's namespace: %+v", r)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want only the fs row", len(rows))
	}
	// A non-fs scope is refused outright, not silently listed.
	if _, err := s.Rows("someone-else"); err == nil {
		t.Fatal("Rows read a scope fs does not own")
	}
}

func TestRetireClosesTheRowAndNothingElse(t *testing.T) {
	s := open(t)
	scope := memory.Scope("p")
	if err := s.PutTask(scope, "t-1", "goal", "{}"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Rows(scope)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows: %v %d", err, len(rows))
	}
	if err := s.Retire(rows[0].ID); err != nil {
		t.Fatalf("retire: %v", err)
	}
	rows, err = s.Rows(scope)
	if err != nil || len(rows) != 1 || !rows[0].Closed {
		t.Fatalf("after retire rows = %+v (%v), want one closed row still present", rows, err)
	}
}

func TestLivenessIsDerivedFromRowTimestamps(t *testing.T) {
	s := open(t)
	scope := memory.Scope("p")
	start := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	now := start
	s.Clock = func() time.Time { return now }

	if err := s.PutBind(scope, "s-1", "t-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutClaim(scope, "t-1", "s-1", 1); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Rows(scope)
	if err != nil {
		t.Fatal(err)
	}
	held, ok := memory.NewIndex(rows).Claim("t-1")
	if !ok {
		t.Fatal("no claim decoded")
	}
	if !held.Live(now) {
		t.Fatalf("a fresh claim is not live at its own timestamp: %+v", held)
	}
	// One second inside the lease: still live.
	edge := now.Add(memory.DefaultTTL - time.Second)
	if !held.Live(edge) {
		t.Fatal("a claim inside its ttl is not live")
	}
	// Past it: not live, and nothing had to be written for that to be true.
	if held.Live(now.Add(memory.DefaultTTL + time.Second)) {
		t.Fatal("a claim past its ttl is live; expiry must be derived, not written")
	}

	// Any write by the holder is the keepalive. A report moves the lease.
	now = start.Add(60 * time.Second)
	if err := s.PutEvent(scope, "t-1", "s-1", 1, "progress", `{"state":"progress","role":"dev"}`); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Rows(scope)
	if err != nil {
		t.Fatal(err)
	}
	held, _ = memory.NewIndex(rows).Claim("t-1")
	if !held.Live(start.Add(150 * time.Second)) {
		t.Fatalf("the lease did not follow the holder's newest write: expires %s", held.Expires())
	}
}

func TestBindingNamesTheTaskASessionIsSpentOn(t *testing.T) {
	s := open(t)
	scope := memory.Scope("p")
	if _, ok := memory.NewIndex(mustRows(t, s, scope)).Binding("s-1"); ok {
		t.Fatal("an unbound session reported a binding")
	}
	if err := s.PutBind(scope, "s-1", "t-7"); err != nil {
		t.Fatal(err)
	}
	got, ok := memory.NewIndex(mustRows(t, s, scope)).Binding("s-1")
	if !ok || got != "t-7" {
		t.Fatalf("binding = %q/%v, want t-7", got, ok)
	}
}

func TestProjectsAndIdsAreGuarded(t *testing.T) {
	for _, project := range []string{"", " ", "a:b", "a/b", " a", "a b"} {
		if err := memory.ValidProject(project); err == nil {
			t.Errorf("project %q was accepted; a scope component holding ':' or space would read another service's rows", project)
		}
	}
	if err := memory.ValidProject("skills"); err != nil {
		t.Errorf("ValidProject(skills) = %v", err)
	}
	for _, id := range []string{"", " ", "a:b", "a b"} {
		if err := memory.ValidID("session", id); err == nil {
			t.Errorf("session %q was accepted; a key component holding ':' would parse as another row's key", id)
		}
	}
}

func mustRows(t *testing.T, s *memory.Store, scope string) []memory.Row {
	t.Helper()
	rows, err := s.Rows(scope)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	return rows
}
