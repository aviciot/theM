//go:build integration

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/crypto"
)

// systemAgentsIntegrationPool opens a pgxpool for SystemAgentsHandler
// integration tests. Self-contained (does not reuse the broken
// tokens_sessions_integration_test.go helpers in this same package).
func systemAgentsIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = "host=localhost port=15432 dbname=them user=them password=them_secret sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping integration test", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("postgres ping failed (%v) — skipping integration test", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func cleanSystemAgentsConfig(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM them.config WHERE config_key='system_agents'`) //nolint:errcheck
	})
	// Start from a clean slate for each test.
	_, err := pool.Exec(context.Background(), `DELETE FROM them.config WHERE config_key='system_agents'`)
	require.NoError(t, err)
}

func buildSystemAgentsRouter(pool *pgxpool.Pool) http.Handler {
	h := admin.NewSystemAgentsHandler(admin.NewPgxQuerier(pool), crypto.DeriveKey("system-agents-integration-test-secret"))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /system-agents", h.Get)
	mux.HandleFunc("PUT /system-agents", h.Put)
	return mux
}

// TestSystemAgents_Put_GeneralMode_RoundTripsModeAndKeyID proves mode="general"
// + key_id survive a real Postgres JSONB round trip through them.config,
// which the in-memory fakes used elsewhere in this package can't verify.
func TestSystemAgents_Put_GeneralMode_RoundTripsModeAndKeyID(t *testing.T) {
	pool := systemAgentsIntegrationPool(t)
	cleanSystemAgentsConfig(t, pool)
	r := buildSystemAgentsRouter(pool)

	rr := doJSON(t, r, http.MethodPut, "/system-agents", map[string]any{
		"roles": map[string]any{
			"classifier": map[string]any{
				"enabled":   true,
				"mode":      "general",
				"provider":  "anthropic",
				"key_id":    float64(7),
			},
		},
	})
	require.Equal(t, http.StatusOK, rr.Code)

	rr2 := doJSON(t, r, http.MethodGet, "/system-agents", nil)
	require.Equal(t, http.StatusOK, rr2.Code)
	assert.Contains(t, rr2.Body.String(), `"mode":"general"`)
	assert.Contains(t, rr2.Body.String(), `"key_id":7`)
	assert.Contains(t, rr2.Body.String(), `"provider":"anthropic"`)
}

// TestSystemAgents_Put_CustomMode_EncryptsKeyAndMasksOnRead proves the
// existing custom-mode api_key encrypt-on-write / mask-on-read behavior is
// unaffected by adding mode/key_id.
func TestSystemAgents_Put_CustomMode_EncryptsKeyAndMasksOnRead(t *testing.T) {
	pool := systemAgentsIntegrationPool(t)
	cleanSystemAgentsConfig(t, pool)
	r := buildSystemAgentsRouter(pool)

	rr := doJSON(t, r, http.MethodPut, "/system-agents", map[string]any{
		"roles": map[string]any{
			"card_synthesizer": map[string]any{
				"enabled":  true,
				"mode":     "custom",
				"provider": "anthropic",
				"model":    "claude-haiku-4-5-20251001",
				"api_key":  "sk-integration-test-plaintext-98765",
			},
		},
	})
	require.Equal(t, http.StatusOK, rr.Code)
	assert.NotContains(t, rr.Body.String(), "sk-integration-test-plaintext-98765")

	rr2 := doJSON(t, r, http.MethodGet, "/system-agents", nil)
	require.Equal(t, http.StatusOK, rr2.Code)
	assert.NotContains(t, rr2.Body.String(), "sk-integration-test-plaintext-98765")
	assert.Contains(t, rr2.Body.String(), `"mode":"custom"`)
}

// TestSystemAgents_Put_GeneralMode_ModelOutsideAllowedList_Rejected proves the
// Put handler validates general_model against the selected platform
// provider's allowed_models instead of silently accepting anything.
func TestSystemAgents_Put_GeneralMode_ModelOutsideAllowedList_Rejected(t *testing.T) {
	pool := systemAgentsIntegrationPool(t)
	cleanSystemAgentsConfig(t, pool)
	r := buildSystemAgentsRouter(pool)

	ctx := context.Background()
	var prevAllowed []byte
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT allowed_models FROM them.llm_providers WHERE name='anthropic' AND tenant_id IS NULL`,
	).Scan(&prevAllowed))
	_, err := pool.Exec(ctx,
		`UPDATE them.llm_providers SET allowed_models='["claude-haiku-4-5-20251001"]' WHERE name='anthropic' AND tenant_id IS NULL`)
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Exec(ctx, //nolint:errcheck
			`UPDATE them.llm_providers SET allowed_models=$1 WHERE name='anthropic' AND tenant_id IS NULL`, prevAllowed)
	})

	rr := doJSON(t, r, http.MethodPut, "/system-agents", map[string]any{
		"roles": map[string]any{
			"classifier": map[string]any{
				"enabled":       true,
				"mode":          "general",
				"provider":      "anthropic",
				"general_model": "not-an-allowed-model",
			},
		},
	})
	require.Equal(t, http.StatusBadRequest, rr.Code)

	rrOK := doJSON(t, r, http.MethodPut, "/system-agents", map[string]any{
		"roles": map[string]any{
			"classifier": map[string]any{
				"enabled":       true,
				"mode":          "general",
				"provider":      "anthropic",
				"general_model": "claude-haiku-4-5-20251001",
			},
		},
	})
	require.Equal(t, http.StatusOK, rrOK.Code)

	rrGet := doJSON(t, r, http.MethodGet, "/system-agents", nil)
	require.Equal(t, http.StatusOK, rrGet.Code)
	assert.Contains(t, rrGet.Body.String(), `"general_model":"claude-haiku-4-5-20251001"`)
}

func doJSON(t *testing.T, r http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}
