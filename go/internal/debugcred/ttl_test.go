package debugcred

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/aviciot/them/internal/appflow"
)

// TTL must be derived FROM appflow.DebugRunMaxLifetime, never an
// independently guessed number — otherwise a credential could expire before
// (or long after) the workflow's own enforced WorkflowRunTimeout, breaking
// "the run is still legitimately active" vs "credential expired" agreement.
func TestTTL_DerivedFromDebugRunMaxLifetimePlusCleanupMargin(t *testing.T) {
	assert.Equal(t, appflow.DebugRunMaxLifetime+CleanupMargin, TTL)
	assert.Equal(t, 3*time.Hour+40*time.Minute, TTL, "3h30m DebugRunMaxLifetime + 10m CleanupMargin")
}
