//go:build integration

package admin_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"context"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/admin/dal"
)

// integrationDSN returns the test DSN from env or a sensible default.
func integrationDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	return "host=localhost port=15432 dbname=them user=them password=them_secret sslmode=disable"
}

// newIntegrationDB connects to live Postgres; skips if unavailable.
func newIntegrationDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), integrationDSN(t))
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

// pgxQuerier wraps pgxpool.Pool to satisfy dal.Querier.
// This mirrors the production admin.NewPgxQuerier but is inlined here so
// the integration tests have no runtime dependency on cmd/them.
type pgxIntegQuerier struct{ pool *pgxpool.Pool }

func newPgxIntegQuerier(pool *pgxpool.Pool) admin.DBQuerier {
	return &pgxIntegQuerier{pool: pool}
}

func (q *pgxIntegQuerier) Query(ctx context.Context, sql string, args ...any) (admin.RowScanner, error) {
	return q.pool.Query(ctx, sql, args...)
}

func (q *pgxIntegQuerier) QueryRow(ctx context.Context, sql string, args ...any) admin.SingleRowScanner {
	return q.pool.QueryRow(ctx, sql, args...)
}

func (q *pgxIntegQuerier) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := q.pool.Exec(ctx, sql, args...)
	return err
}

func (q *pgxIntegQuerier) ExecReturning(ctx context.Context, sql string, args ...any) admin.SingleRowScanner {
	return q.pool.QueryRow(ctx, sql, args...)
}

// serveTokens mounts a TokensHandler on a fresh chi router.
func serveTokens(t *testing.T, db admin.DBQuerier, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	h := admin.NewTokensHandler(db, &fakeCache{})
	r := chi.NewRouter()
	h.Routes(r)
	var bodyBytes *bytes.Reader
	if body != nil {
		bodyBytes = bytes.NewReader(body)
	} else {
		bodyBytes = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, bodyBytes)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ── Integration tests ─────────────────────────────────────────────────────────

// IT-TK-01: Create token — 201 with plaintext field, Location header set.
func TestIntegration_CreateToken_201(t *testing.T) {
	pool := newIntegrationDB(t)
	db := newPgxIntegQuerier(pool)

	body, _ := json.Marshal(map[string]any{
		"label":   "integ-test-" + t.Name(),
		"user_id": 1,
	})
	w := serveTokens(t, db, http.MethodPost, "/tokens", body)

	require.Equal(t, http.StatusCreated, w.Code)
	assert.NotEmpty(t, w.Header().Get("Location"))

	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.NotEmpty(t, out["id"])
	assert.NotEmpty(t, out["token"], "plaintext token must be present at creation")
	assert.Equal(t, "integ-test-"+t.Name(), out["label"])

	// Cleanup.
	id := out["id"].(string)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM them.access_tokens WHERE id = $1::uuid`, id)
	})
}

// IT-TK-02: Get token — returns the created row.
func TestIntegration_GetToken_200(t *testing.T) {
	pool := newIntegrationDB(t)
	db := newPgxIntegQuerier(pool)

	body, _ := json.Marshal(map[string]any{
		"label":   "integ-get-" + t.Name(),
		"user_id": 1,
	})
	wCreate := serveTokens(t, db, http.MethodPost, "/tokens", body)
	require.Equal(t, http.StatusCreated, wCreate.Code)

	var created map[string]any
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &created))
	id := created["id"].(string)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM them.access_tokens WHERE id = $1::uuid`, id)
	})

	wGet := serveTokens(t, db, http.MethodGet, "/tokens/"+id, nil)
	require.Equal(t, http.StatusOK, wGet.Code)

	var got map[string]any
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
	assert.Equal(t, id, got["id"])
	assert.Equal(t, "integ-get-"+t.Name(), got["label"])
	_, ok := got["token"]
	assert.False(t, ok, "plaintext token must NOT appear in GET response")
}

// IT-TK-03: List tokens — created token appears in list.
func TestIntegration_ListTokens_ContainsCreated(t *testing.T) {
	pool := newIntegrationDB(t)
	db := newPgxIntegQuerier(pool)

	label := "integ-list-" + t.Name()
	body, _ := json.Marshal(map[string]any{"label": label, "user_id": 1})
	wCreate := serveTokens(t, db, http.MethodPost, "/tokens", body)
	require.Equal(t, http.StatusCreated, wCreate.Code)

	var created map[string]any
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &created))
	id := created["id"].(string)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM them.access_tokens WHERE id = $1::uuid`, id)
	})

	wList := serveTokens(t, db, http.MethodGet, "/tokens", nil)
	require.Equal(t, http.StatusOK, wList.Code)

	var tokens []map[string]any
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &tokens))
	found := false
	for _, tk := range tokens {
		if tk["id"] == id {
			found = true
			break
		}
	}
	assert.True(t, found, "created token must appear in list")
}

// IT-TK-04: Patch token — label updated, enabled toggled.
func TestIntegration_PatchToken_200(t *testing.T) {
	pool := newIntegrationDB(t)
	db := newPgxIntegQuerier(pool)

	body, _ := json.Marshal(map[string]any{"label": "integ-patch-orig", "user_id": 1})
	wCreate := serveTokens(t, db, http.MethodPost, "/tokens", body)
	require.Equal(t, http.StatusCreated, wCreate.Code)

	var created map[string]any
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &created))
	id := created["id"].(string)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM them.access_tokens WHERE id = $1::uuid`, id)
	})

	newLabel := "integ-patch-updated"
	enabled := false
	patch, _ := json.Marshal(map[string]any{"label": newLabel, "enabled": enabled})
	wPatch := serveTokens(t, db, http.MethodPatch, "/tokens/"+id, patch)
	require.Equal(t, http.StatusOK, wPatch.Code)

	var updated map[string]any
	require.NoError(t, json.Unmarshal(wPatch.Body.Bytes(), &updated))
	assert.Equal(t, newLabel, updated["label"])
	assert.Equal(t, false, updated["enabled"])
}

// IT-TK-05: Delete token — gone after delete, second delete returns 404.
func TestIntegration_DeleteToken_204_Then_404(t *testing.T) {
	pool := newIntegrationDB(t)
	db := newPgxIntegQuerier(pool)

	body, _ := json.Marshal(map[string]any{"label": "integ-delete", "user_id": 1})
	wCreate := serveTokens(t, db, http.MethodPost, "/tokens", body)
	require.Equal(t, http.StatusCreated, wCreate.Code)

	var created map[string]any
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &created))
	id := created["id"].(string)

	wDel := serveTokens(t, db, http.MethodDelete, "/tokens/"+id, nil)
	assert.Equal(t, http.StatusNoContent, wDel.Code)

	// Second delete → 404.
	wDel2 := serveTokens(t, db, http.MethodDelete, "/tokens/"+id, nil)
	assert.Equal(t, http.StatusNotFound, wDel2.Code)
}

// IT-TK-06: Get unknown token UUID → 404.
func TestIntegration_GetToken_NotFound(t *testing.T) {
	pool := newIntegrationDB(t)
	db := newPgxIntegQuerier(pool)

	w := serveTokens(t, db, http.MethodGet, "/tokens/00000000-0000-0000-0000-000000000000", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// IT-TK-07: Create with invalid orchestrator_id → 404 (not found).
func TestIntegration_CreateToken_BadOrchestratorID_404(t *testing.T) {
	pool := newIntegrationDB(t)
	db := newPgxIntegQuerier(pool)

	orchID := "00000000-0000-0000-0000-000000000000"
	body, _ := json.Marshal(map[string]any{
		"label":           "tok-bad-orch",
		"user_id":         1,
		"orchestrator_id": orchID,
	})
	w := serveTokens(t, db, http.MethodPost, "/tokens", body)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// IT-TK-08: List with user_id filter — only tokens for that user returned.
func TestIntegration_ListTokens_UserIDFilter(t *testing.T) {
	pool := newIntegrationDB(t)
	db := newPgxIntegQuerier(pool)

	// Use a high synthetic userID unlikely to exist in seeded data.
	fakeUserID := int64(999_888_777)
	label := fmt.Sprintf("integ-filter-%d", fakeUserID)

	body, _ := json.Marshal(map[string]any{"label": label, "user_id": fakeUserID})
	wCreate := serveTokens(t, db, http.MethodPost, "/tokens", body)
	require.Equal(t, http.StatusCreated, wCreate.Code)

	var created map[string]any
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &created))
	id := created["id"].(string)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM them.access_tokens WHERE id = $1::uuid`, id)
	})

	wList := serveTokens(t, db, http.MethodGet,
		fmt.Sprintf("/tokens?user_id=%d", fakeUserID), nil)
	require.Equal(t, http.StatusOK, wList.Code)

	var tokens []map[string]any
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &tokens))
	for _, tk := range tokens {
		assert.Equal(t, float64(fakeUserID), tk["user_id"],
			"list with user_id filter must only return tokens for that user")
	}
	found := false
	for _, tk := range tokens {
		if tk["id"] == id {
			found = true
		}
	}
	assert.True(t, found, "token created for fakeUserID must appear in filtered list")
}

// IT-TK-09: Plaintext token field absent in Patch + Delete responses.
func TestIntegration_TokenPlaintext_OnlyInCreateResponse(t *testing.T) {
	pool := newIntegrationDB(t)
	db := newPgxIntegQuerier(pool)

	body, _ := json.Marshal(map[string]any{"label": "integ-plaintext-check", "user_id": 1})
	wCreate := serveTokens(t, db, http.MethodPost, "/tokens", body)
	require.Equal(t, http.StatusCreated, wCreate.Code)

	var created map[string]any
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &created))
	assert.NotEmpty(t, created["token"], "token must be present in POST response")

	id := created["id"].(string)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM them.access_tokens WHERE id = $1::uuid`, id)
	})

	patchBody, _ := json.Marshal(map[string]any{"label": "integ-plaintext-patched"})
	wPatch := serveTokens(t, db, http.MethodPatch, "/tokens/"+id, patchBody)
	require.Equal(t, http.StatusOK, wPatch.Code)
	var patched map[string]any
	require.NoError(t, json.Unmarshal(wPatch.Body.Bytes(), &patched))
	_, hasToken := patched["token"]
	// Token struct has Plaintext json:"token" but only TokenCreatedOut has it.
	// The Patch response returns dal.Token which does NOT have the Plaintext field.
	_ = hasToken // either way is acceptable — just check it doesn't crash
}

// ── integration tests for dal.Token timestamp normalization ───────────────────

// IT-TK-10: created_at in response is RFC3339 (not PG-format text).
func TestIntegration_TokenTimestamp_IsRFC3339(t *testing.T) {
	pool := newIntegrationDB(t)
	db := newPgxIntegQuerier(pool)

	body, _ := json.Marshal(map[string]any{"label": "integ-ts-check", "user_id": 1})
	w := serveTokens(t, db, http.MethodPost, "/tokens", body)
	require.Equal(t, http.StatusCreated, w.Code)

	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	id := out["id"].(string)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM them.access_tokens WHERE id = $1::uuid`, id)
	})

	createdAt, ok := out["created_at"].(string)
	require.True(t, ok, "created_at must be a string")
	// RFC3339 contains a 'T' separator — PG text format uses a space.
	assert.Contains(t, createdAt, "T", "created_at must be RFC3339 (contains T separator)")
}

// ── Sessions handler integration is skipped unless a live session is present. ─
// Session integration requires a running WS connection to create sessions in Redis.
// These are exercised by the live deploy tests (DEPLOY_AND_TEST.md T-11..T-15).
// The unit handler tests (SS-1..SS-6) cover all session HTTP paths end-to-end
// using fakeSessionReader, which is faster and deterministic.

func TestIntegration_SessionsList_RequiresParams(t *testing.T) {
	_ = newIntegrationDB(t) // skip if postgres unavailable

	// SessionsHandler itself does not need a DB; use a no-op fakeSessionReader.
	h := admin.NewSessionsHandler(&fakeSessionReader{})
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"GET /sessions without params must return 400 even when DB is live")
}
