package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/IanLuo/memstore"
)

// DefaultTTL is how long a claim stays live without any write by its holder.
// The keepalive is any write: there is no renew verb and no heartbeat row.
const DefaultTTL = 120 * time.Second

// listLimit bounds one scope listing. memstore's Recent requires a limit and
// offers no scope-prefix listing, so every read asks for more rows than the
// design's sizing case (10,000 tasks per project) can hold and filters in
// process. A listing must never silently drop a row: a dropped claim row reads
// as a free task.
const listLimit = 1 << 20

// ErrDuplicate is memstore's refusal of a duplicate (origin, origin_id),
// re-exported so the claim protocol can branch on it without importing
// memstore. If memstore ever stops refusing this inside its own transaction,
// both fs invariants lose their arbiter: that is the release that stops fs
// assigning work.
var ErrDuplicate = memstore.ErrDuplicateOrigin

// Store is fs's handle on shared memstore.
//
// Clock is the single clock read in the product: every row fs writes is stamped
// by it and every liveness decision compares against it. One host, one clock —
// two hosts would need a different rule, which is stated as an assumption rather
// than discovered later. It is a field so a test can place fs in controlled time.
type Store struct {
	db    *memstore.Store
	Clock func() time.Time
}

// Open opens the shared memstore file: path if given, else $MEMSTORE_DB, else the
// home default memstore itself uses. fs is a client of that file, never its
// owner.
func Open(path string) (*Store, error) {
	if path == "" {
		p, err := defaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	db, err := memstore.Open(path)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, Clock: time.Now}, nil
}

// defaultPath is memstore's own resolution: $MEMSTORE_DB, else
// ~/.memstore/memory.db. fs must open the file the other services open, so the
// rule is theirs, not a second one invented here.
func defaultPath() (string, error) {
	if p := os.Getenv("MEMSTORE_DB"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no memstore path: MEMSTORE_DB is unset and the home directory is unknown: %w", err)
	}
	return filepath.Join(home, ".memstore", "memory.db"), nil
}

// Close releases the file.
func (s *Store) Close() error { return s.db.Close() }

// Now is the one clock read.
func (s *Store) Now() time.Time { return s.Clock() }

// Row is one fs row as memstore returned it. Extra is the caller's JSON: memory
// carries it and reads only the two fields liveness needs, because knowing what
// a task is belongs to coord.
type Row struct {
	ID       int64
	Kind     string
	Scope    string
	OriginID string
	Text     string
	Extra    string
	Ts       time.Time
	Closed   bool
}

// Session is the session a row belongs to, from its key. A claim row names no
// session in its key — it must not, or two racers would build different keys —
// so Claim and Binding read that one field from the row's extra.
func (r Row) Session() string {
	sid, _ := ParseSession(r.OriginID)
	return sid
}

// Task is the task a row belongs to, from its key.
func (r Row) Task() string {
	tid, _ := ParseTask(r.OriginID)
	return tid
}

// PutTask appends one task row. text is the task's own words; the caller's
// fields travel in extra.
func (s *Store) PutTask(scope, tid, text, extra string) error {
	return s.put(scope, KindTask, TaskKey(tid), text, text, extra)
}

// PutBind appends the row that spends a session. It is written once per session,
// before any claim, so a second task for that session fails on memstore's own
// unique index rather than on a check fs could skip.
func (s *Store) PutBind(scope, sid, tid string) error {
	extra, err := json.Marshal(bindExtra{Task: tid, Session: sid})
	if err != nil {
		return err
	}
	line := fmt.Sprintf("session %s bound to task %s", sid, tid)
	return s.put(scope, KindBind, SessionKey(sid), line, line, string(extra))
}

// PutClaim appends round round of task tid's claim, held by sid. A refusal here
// is how the loser of a race learns it lost.
func (s *Store) PutClaim(scope, tid, sid string, round int) error {
	extra, err := json.Marshal(claimExtra{
		Task: tid, Session: sid, Round: round, TTL: int(DefaultTTL / time.Second),
	})
	if err != nil {
		return err
	}
	line := fmt.Sprintf("task %s held by session %s at round %d", tid, sid, round)
	return s.put(scope, KindClaim, ClaimKey(tid, round), line, line, string(extra))
}

// PutEvent appends one report: index n for (tid, sid), with the caller's
// evidence in extra. This write is also the holder's keepalive.
func (s *Store) PutEvent(scope, tid, sid string, n int, text, extra string) error {
	if text == "" {
		text = "report"
	}
	return s.put(scope, KindEvent, EventKey(tid, sid, n), text, text, extra)
}

// put writes one row through memstore's supported API. It carries no direct SQL,
// no second key and no fallback: a store that cannot take the write leaves the
// product with nothing to report, by design.
func (s *Store) put(scope, kind, key, title, text, extra string) error {
	_, err := s.db.Put(&memstore.Memory{
		Ts:       s.Now(),
		Kind:     kind,
		Scope:    scope,
		Title:    title,
		Text:     text,
		Extra:    extra,
		Origin:   Origin,
		OriginId: key,
	})
	return err
}

// Retire closes one row through memstore's lifecycle operation. Completion is
// terminal: that operation is the only writer of the closed state, and a closed
// row is never reopened.
func (s *Store) Retire(id int64) error {
	return s.db.CloseMemory(id, nil)
}

// Rows lists one scope, newest first, decoded. Rows of another scope are
// dropped rather than returned: the scope guard is here, once, so no caller can
// forget it.
func (s *Store) Rows(scope string) ([]Row, error) {
	if _, ok := Project(scope); !ok {
		return nil, fmt.Errorf("refusing to read scope %q: fs reads only its own %s namespace", scope, ScopePrefix)
	}
	rows, err := s.db.Recent(scope, listLimit)
	if err != nil {
		return nil, err
	}
	return decode(rows, scope), nil
}

// AllRows lists every scope there is and keeps only fs's. The aggregate user
// query needs to cross projects and memstore has no scope listing, so the
// listing is filtered here, in the one place the scope guard lives. Nothing
// outside the fs namespace is returned or written: fs owns no index of projects
// and will not grow one.
func (s *Store) AllRows() ([]Row, error) {
	rows, err := s.db.Recent("", listLimit)
	if err != nil {
		return nil, err
	}
	return decode(rows, ""), nil
}

// decode maps memstore rows to fs rows, dropping any scope but one fs scope owns.
func decode(mems []memstore.Memory, want string) []Row {
	out := make([]Row, 0, len(mems))
	for _, m := range mems {
		if _, ok := Project(m.Scope); !ok {
			continue
		}
		if want != "" && m.Scope != want {
			continue
		}
		out = append(out, Row{
			ID:       m.Id,
			Kind:     m.Kind,
			Scope:    m.Scope,
			OriginID: m.OriginId,
			Text:     m.Text,
			Extra:    m.Extra,
			Ts:       m.Ts,
			Closed:   m.Status == "closed",
		})
	}
	return out
}
