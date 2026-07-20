package ws

import "github.com/google/uuid"

// newID generates a UUID v4 string.
// UUID format is required because Go passes run_id, session_id, and context_id to
// the Python Temporal worker which converts them via uuid.UUID(); a plain hex string
// would raise ValueError there.
func newID() string {
	return uuid.New().String()
}
