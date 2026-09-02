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
	loader := workerconfig.NewPgxLoader(nil, nil)
	assert.NotNil(t, loader, "NewPgxLoader must return a non-nil *PgxLoader")

	// PgxLoader must satisfy the Loader interface at compile time.
	var _ workerconfig.Loader = loader
}

// TestManagedAppParams_ConfigSubstitution verifies that {{PARAMS.KEY}} placeholders
// in a system prompt are replaced with values from ManagedAppParams.Config.
func TestManagedAppParams_ConfigSubstitution(t *testing.T) {
	params := &workerconfig.ManagedAppParams{
		Config: map[string]any{
			"COMPANY_NAME": "Acme Corp",
			"TONE":         "formal",
			"MAX_RETRIES":  3,
		},
	}
	prompt := "You are an assistant for {{PARAMS.COMPANY_NAME}}. Use a {{PARAMS.TONE}} tone. Max retries: {{PARAMS.MAX_RETRIES}}. Unknown: {{PARAMS.MISSING}}."
	result := workerconfig.ApplyParamSubstitution(prompt, params)
	assert.Equal(t, "You are an assistant for Acme Corp. Use a formal tone. Max retries: 3. Unknown: {{PARAMS.MISSING}}.", result)
}

// TestManagedAppParams_NilSafe verifies that ApplyParamSubstitution is safe when params is nil.
func TestManagedAppParams_NilSafe(t *testing.T) {
	prompt := "Hello {{PARAMS.NAME}}"
	result := workerconfig.ApplyParamSubstitution(prompt, nil)
	assert.Equal(t, prompt, result)
}

// TestRunConfig_ManagedAppParams_ZeroNil verifies ManagedAppParams is nil for zero-value RunConfig.
func TestRunConfig_ManagedAppParams_ZeroNil(t *testing.T) {
	var cfg workerconfig.RunConfig
	assert.Nil(t, cfg.ManagedAppParams)
}
