package workerconfig_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aviciot/them/internal/temporal/workerconfig"
)

// TestRunConfig_ZeroValue verifies that the zero value of RunConfig is safe to
// use (empty provider/key falls back to global, empty orchestrator config is valid).
func TestRunConfig_ZeroValue(t *testing.T) {
	var cfg workerconfig.RunConfig
	assert.Empty(t, cfg.LLMProvider, "zero-value LLMProvider must be empty string (signals global fallback)")
	assert.Empty(t, cfg.LLMAPIKey, "zero-value LLMAPIKey must be empty string (signals global fallback)")
}

// TestPgxLoader_NewPgxLoader verifies that NewPgxLoader returns a non-nil *PgxLoader.
// The loader is not called here (no live DB in unit tests) — this is a construction guard.
func TestPgxLoader_NewPgxLoader(t *testing.T) {
	loader := workerconfig.NewPgxLoader(nil)
	assert.NotNil(t, loader, "NewPgxLoader must return a non-nil *PgxLoader")

	// PgxLoader must satisfy the Loader interface at compile time.
	var _ workerconfig.Loader = loader
}
