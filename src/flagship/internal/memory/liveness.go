package memory

import (
	"encoding/json"
	"time"
)

// Liveness is derived, never written. There is no "expired" flag, no cleanup
// job, and no state two readers can disagree about: a claim of round n is live
// while some row by its session is newer than now − ttl. Expiry frees an
// incomplete task for a new session; it never marks work done.

// bindExtra is the part of a binding row's extra that liveness needs: the task a
// session is spent on.
type bindExtra struct {
	Task    string `json:"task"`
	Session string `json:"session"`
}

// claimExtra is the part of a claim row's extra that liveness needs. The claim
// key cannot carry the session — every racer must compute it identically — so the
// holder is in the row.
type claimExtra struct {
	Task    string `json:"task"`
	Session string `json:"session"`
	Round   int    `json:"round"`
	TTL     int    `json:"ttl_s"`
}

// Claim is one task held at one round, with the facts liveness is computed from.
type Claim struct {
	Task      string
	Session   string
	Round     int
	TTL       time.Duration
	At        time.Time
	LastWrite time.Time
}

// Expires is when the lease lapses if the holder writes nothing more.
func (c Claim) Expires() time.Time { return c.LastWrite.Add(c.TTL) }

// Live reports whether the claim stands at now.
func (c Claim) Live(now time.Time) bool { return !now.After(c.Expires()) }

// Index is one pass over a listing.
//
// Every liveness question is answered from it in constant time, because the
// alternative — scanning the listing once per task — is quadratic, and the sizing
// case is 10,000 tasks in one project. Reads stay one listing plus in-process
// filters; this is the filter.
type Index struct {
	claims   map[string]Claim     // task → its highest round
	sessions map[string]time.Time // session → its newest write, whichever kind
	binds    map[string]string    // session → the task it is spent on
	events   map[string][]Row     // task → its report rows
	maxEvent map[string]int       // task\x00session → its highest report index
}

// NewIndex builds the index for a listing. An undecodable row is skipped rather
// than fatal: it cannot be a claim, a bind or a report fs wrote, and one bad row
// must not hide the rest of a project.
func NewIndex(rows []Row) *Index {
	ix := &Index{
		claims:   make(map[string]Claim),
		sessions: make(map[string]time.Time),
		binds:    make(map[string]string),
		events:   make(map[string][]Row),
		maxEvent: make(map[string]int),
	}
	for _, r := range rows {
		switch r.Kind {
		case KindBind:
			sid, ok := ParseSession(r.OriginID)
			if !ok {
				continue
			}
			var ex bindExtra
			if err := json.Unmarshal([]byte(r.Extra), &ex); err != nil || ex.Task == "" {
				continue
			}
			ix.binds[sid] = ex.Task
		case KindClaim:
			c, ok := claimOf(r)
			if !ok {
				continue
			}
			if best, seen := ix.claims[c.Task]; !seen || c.Round > best.Round {
				ix.claims[c.Task] = c
			}
		case KindEvent:
			tid, ok := ParseTask(r.OriginID)
			if !ok {
				continue
			}
			ix.events[tid] = append(ix.events[tid], r)
			sid, ok := ParseSession(r.OriginID)
			if !ok {
				continue
			}
			if n, ok := ParseEventIndex(r.OriginID); ok && n > ix.maxEvent[tid+"\x00"+sid] {
				ix.maxEvent[tid+"\x00"+sid] = n
			}
		}
	}
	// A claim has to be attributed to its holder's newest write, so the session
	// pass runs after the claim pass, over what both found.
	for _, r := range rows {
		sid := r.Session()
		if sid == "" {
			if c, ok := claimOf(r); ok {
				sid = c.Session
			}
		}
		if sid == "" {
			continue
		}
		if newest, seen := ix.sessions[sid]; !seen || r.Ts.After(newest) {
			ix.sessions[sid] = r.Ts
		}
	}
	for tid, c := range ix.claims {
		if newest, ok := ix.sessions[c.Session]; ok {
			c.LastWrite = newest
		} else {
			c.LastWrite = c.At
		}
		ix.claims[tid] = c
	}
	return ix
}

// Claim returns the current claim on a task: the highest round seen. Only the
// highest can be live — a lower round lost to it — so a caller asks liveness of
// this one claim and nothing else.
func (ix *Index) Claim(tid string) (Claim, bool) {
	c, ok := ix.claims[tid]
	return c, ok
}

// Newest returns the timestamp of the newest row written by session sid: its
// binding, its claim, or any report. Any write is the keepalive, so no row is
// privileged and no separate heartbeat kind exists.
func (ix *Index) Newest(sid string) (time.Time, bool) {
	t, ok := ix.sessions[sid]
	return t, ok
}

// Binding returns the task a session is spent on. A session that has never claimed
// is bound to nothing.
func (ix *Index) Binding(sid string) (string, bool) {
	tid, ok := ix.binds[sid]
	return tid, ok
}

// MaxEvent returns the highest report index a session has used on a task, so the
// next report appends rather than colliding with a key already spent.
func (ix *Index) MaxEvent(tid, sid string) int {
	return ix.maxEvent[tid+"\x00"+sid]
}

// Events returns the report rows on one task, in no particular order.
func (ix *Index) Events(tid string) []Row { return ix.events[tid] }

// claimOf decodes a claim row: its key names the task and the round, its extra
// names the session holding it.
func claimOf(r Row) (Claim, bool) {
	if r.Kind != KindClaim {
		return Claim{}, false
	}
	tid, ok := ParseTask(r.OriginID)
	if !ok {
		return Claim{}, false
	}
	round, ok := ParseRound(r.OriginID)
	if !ok {
		return Claim{}, false
	}
	var ex claimExtra
	if err := json.Unmarshal([]byte(r.Extra), &ex); err != nil || ex.Session == "" {
		return Claim{}, false
	}
	ttl := time.Duration(ex.TTL) * time.Second
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return Claim{Task: tid, Session: ex.Session, Round: max(round, ex.Round), TTL: ttl, At: r.Ts}, true
}
