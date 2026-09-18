package command

import (
	"crypto/rand"
	"fmt"
)

// newNodeID generates a short random node ID for tasks.
// Format: "t-" + 8 hex chars.
func newNodeID() string {
	var buf [4]byte
	rand.Read(buf[:])
	return fmt.Sprintf("t-%x", buf)
}
