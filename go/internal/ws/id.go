package ws

import (
	"crypto/rand"
	"encoding/hex"
)

// newID generates a random 16-byte hex string suitable for IDs.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
