package store

import (
	"crypto/rand"
	"sync"
	"time"
)

// ULIDGenerator produces monotonically increasing ULIDs.
// Owned by the store package (ARCHITECTURE R4).
type ULIDGenerator struct {
	mu   sync.Mutex
	last string
}

// NewULIDGenerator returns a new generator.
func NewULIDGenerator() *ULIDGenerator {
	return &ULIDGenerator{}
}

// crockford is the Crockford Base32 encoding alphabet.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// New generates a ULID: 10 chars timestamp (ms) + 16 chars randomness.
// Guarantees monotonic ordering within the same millisecond.
func (g *ULIDGenerator) New() string {
	g.mu.Lock()
	defer g.mu.Unlock()

	ms := uint64(time.Now().UnixMilli())
	var buf [26]byte

	// Encode 48-bit timestamp in first 10 characters (big-endian).
	for i := 9; i >= 0; i-- {
		buf[i] = crockford[ms&0x1f]
		ms >>= 5
	}

	// Fill 16 random characters.
	var rnd [10]byte
	rand.Read(rnd[:])
	for i := 0; i < 16; i++ {
		// Map random bytes to base32. Use 5 bits per character.
		byteIdx := (i * 5) / 8
		bitIdx := uint((i * 5) % 8)
		var val byte
		if byteIdx < len(rnd) {
			val = rnd[byteIdx] >> bitIdx
			if byteIdx+1 < len(rnd) && bitIdx > 3 {
				val |= rnd[byteIdx+1] << (8 - bitIdx)
			}
		}
		buf[10+i] = crockford[val&0x1f]
	}

	id := string(buf[:])

	// Ensure monotonicity: if same or earlier than last, increment.
	if id <= g.last {
		id = increment(g.last)
	}
	g.last = id
	return id
}

// increment returns the next ULID lexicographically (overflow panics, but
// that requires exhausting 80 bits of randomness in one millisecond).
func increment(id string) string {
	buf := []byte(id)
	for i := len(buf) - 1; i >= 0; i-- {
		pos := indexOf(crockford, buf[i])
		if pos < 31 {
			buf[i] = crockford[pos+1]
			return string(buf)
		}
		buf[i] = crockford[0]
	}
	panic("store: ULID overflow")
}

func indexOf(alphabet string, c byte) int {
	for i := 0; i < len(alphabet); i++ {
		if alphabet[i] == c {
			return i
		}
	}
	return 0
}
