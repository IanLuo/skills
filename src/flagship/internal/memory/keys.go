package memory

import (
	"strconv"
	"strings"
)

// The four row kinds, and the four key prefixes. One prefix per kind because
// memstore's unique index is one key wide: `session:<sid>` is what makes a
// session spent, `claim:<tid>:<n>` is what makes a task held, and collapsing
// them into one row would silently drop one of the two invariants.
const (
	KindTask  = "fs.task"
	KindBind  = "fs.bind"
	KindClaim = "fs.claim"
	KindEvent = "fs.event"
)

const (
	prefixTask    = "task"
	prefixSession = "session"
	prefixClaim   = "claim"
	prefixEvent   = "ev"
)

// TaskKey is the key of a task row.
func TaskKey(tid string) string { return prefixTask + ":" + tid }

// SessionKey is the key of a session's binding row.
func SessionKey(sid string) string { return prefixSession + ":" + sid }

// ClaimKey is the key every racer of one round computes identically. That is
// the whole point: the loser of a race learns it lost from memstore's refusal of
// this key, never from a comparison of its own.
func ClaimKey(tid string, round int) string {
	return prefixClaim + ":" + tid + ":" + strconv.Itoa(round)
}

// EventKey is the key of one report. The trailing index is per (task, session)
// so a session's reports append instead of colliding.
func EventKey(tid, sid string, n int) string {
	return prefixEvent + ":" + tid + ":" + sid + ":" + strconv.Itoa(n)
}

// ParseTask returns the task a key belongs to: a task key names its task, a
// claim key names the task it holds, an event key names the task it reports on.
func ParseTask(key string) (string, bool) {
	p := strings.Split(key, ":")
	switch {
	case len(p) == 2 && p[0] == prefixTask:
		return p[1], p[1] != ""
	case len(p) == 3 && p[0] == prefixClaim:
		return p[1], p[1] != ""
	case len(p) == 4 && p[0] == prefixEvent:
		return p[1], p[1] != ""
	}
	return "", false
}

// ParseSession returns the session a key names. A claim key names none: the
// claim key must be identical for every racer, so the holder is carried in the
// row's extra instead.
func ParseSession(key string) (string, bool) {
	p := strings.Split(key, ":")
	switch {
	case len(p) == 2 && p[0] == prefixSession:
		return p[1], p[1] != ""
	case len(p) == 4 && p[0] == prefixEvent:
		return p[2], p[2] != ""
	}
	return "", false
}

// ParseRound returns the round a claim key names.
func ParseRound(key string) (int, bool) {
	p := strings.Split(key, ":")
	if len(p) != 3 || p[0] != prefixClaim {
		return 0, false
	}
	round, err := strconv.Atoi(p[2])
	if err != nil || round <= 0 {
		return 0, false
	}
	return round, true
}

// ParseEventIndex returns the per-session report index an event key carries.
func ParseEventIndex(key string) (int, bool) {
	p := strings.Split(key, ":")
	if len(p) != 4 || p[0] != prefixEvent {
		return 0, false
	}
	n, err := strconv.Atoi(p[3])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
