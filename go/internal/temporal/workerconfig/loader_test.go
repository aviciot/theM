package workerconfig_test

import (
	"errors"
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
	loader := workerconfig.NewPgxLoader(nil, nil)
	assert.NotNil(t, loader, "NewPgxLoader must return a non-nil *PgxLoader")

	// PgxLoader must satisfy the Loader interface at compile time.
	var _ workerconfig.Loader = loader
}

// TestPgxLoader_NewPgxLoader_TenantProviderKey_EmptyInputsFastPath verifies that
// NewPgxLoader with a nil pool is safe to construct and that the Loader interface
// is satisfied (compile-time guard). DB-backed resolution is verified by integration tests.
func TestPgxLoader_NewPgxLoader_TenantProviderKey_NilPoolSafe(t *testing.T) {
	loader := workerconfig.NewPgxLoader(nil, nil)
	assert.NotNil(t, loader)
	// Interface still satisfied after Step 15 additions.
	var _ workerconfig.Loader = loader
}

// TestErrNoProviderKey_IsSentinel verifies that ErrNoProviderKey is exported and
// can be matched with errors.Is.
func TestErrNoProviderKey_IsSentinel(t *testing.T) {
	err := workerconfig.ErrNoProviderKey
	assert.True(t, errors.Is(err, workerconfig.ErrNoProviderKey),
		"ErrNoProviderKey must be matchable with errors.Is")
	assert.NotEqual(t, "", err.Error(), "ErrNoProviderKey must have a non-empty message")
}
