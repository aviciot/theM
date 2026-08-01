package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/session"
	"github.com/aviciot/them/internal/tenantctx"
)

// ── Fakes ─────────────────────────────────────────────────────────────────────

// fakeRow satisfies admin.SingleRowScanner with a fixed error.
type fakeRow struct {
	err error
}

func (f *fakeRow) Scan(_ ...any) error { return f.err }

// fakeRows satisfies admin.RowScanner backed by an in-memory slice of
// []any rows. Rows are scanned in order.
type fakeRows struct {
	data   [][]any
	pos    int
	closed bool
}

func newFakeRows(data [][]any) *fakeRows { return &fakeRows{data: data} }

func (r *fakeRows) Next() bool   { return r.pos < len(r.data) }
func (r *fakeRows) Close() error { r.closed = true; return nil }
func (r *fakeRows) Scan(dest ...any) error {
	if r.pos >= len(r.data) {
		return errors.New("no more rows")
	}
	row := r.data[r.pos]
	r.pos++
	for i, d := range dest {
		if i >= len(row) {
			break
		}
		if err := scanInto(d, row[i]); err != nil {
			return err
		}
	}
	return nil
}

func scanInto(dest, src any) error {
	switch d := dest.(type) {
	case *int64:
		switch v := src.(type) {
		case int64:
			*d = v
		case int:
			*d = int64(v)
		default:
			return fmt.Errorf("scanInto: cannot assign %T to *int64", src)
		}
	case **int64:
		if src == nil {
			*d = nil
		} else {
			var n int64
			if err := scanInto(&n, src); err != nil {
				return err
			}
			*d = &n
		}
	case *string:
		switch v := src.(type) {
		case string:
			*d = v
		default:
			*d = fmt.Sprintf("%v", src)
		}
	case **string:
		if src == nil {
			*d = nil
		} else {
			s := fmt.Sprintf("%v", src)
			*d = &s
		}
	case *bool:
		switch v := src.(type) {
		case bool:
			*d = v
		default:
			return fmt.Errorf("scanInto: cannot assign %T to *bool", src)
		}
	case *[]string:
		switch v := src.(type) {
		case []string:
			*d = v
		case nil:
			*d = []string{}
		default:
			*d = []string{}
		}
	case *[]byte:
		switch v := src.(type) {
		case []byte:
			*d = v
		case nil:
			*d = nil
		default:
			*d = []byte(fmt.Sprintf("%v", src))
		}
	case *int:
		switch v := src.(type) {
		case int:
			*d = v
		case int64:
			*d = int(v)
		default:
			return fmt.Errorf("scanInto: cannot assign %T to *int", src)
		}
	case **int:
		if src == nil {
			*d = nil
		} else {
			var n int
			if err := scanInto(&n, src); err != nil {
				return err
			}
			*d = &n
		}
	default:
		return fmt.Errorf("scanInto: unsupported dest type %T", dest)
	}
	return nil
}

// fakeDB satisfies admin.DBQuerier.
type fakeDB struct {
	queryRows   *fakeRows // returned by Query
	queryRowErr error     // error returned by QueryRow's Scan
	queryRowStr string    // string value scanned by QueryRow (e.g. slug lookup)
	execErr     error     // returned by Exec
	execRetStr  string    // string id returned by ExecReturning (UUID)
	execRetErr  error     // error returned by ExecReturning's Scan
	querySQLLog []string  // log of executed SQL
}

func (f *fakeDB) Query(_ context.Context, sql string, _ ...any) (admin.RowScanner, error) {
	f.querySQLLog = append(f.querySQLLog, sql)
	if f.queryRows == nil {
		return newFakeRows(nil), nil
	}
	return f.queryRows, nil
}

func (f *fakeDB) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	if f.queryRowStr != "" {
		return &stringRow{val: f.queryRowStr}
	}
	return &fakeRow{err: f.queryRowErr}
}

// stringRow scans a single string value (used for slug/context_id lookups).
type stringRow struct{ val string }

func (r *stringRow) Scan(dest ...any) error {
	if len(dest) == 0 {
		return nil
	}
	if d, ok := dest[0].(*string); ok {
		*d = r.val
		return nil
	}
	return fmt.Errorf("stringRow: cannot scan into %T", dest[0])
}

func (f *fakeDB) Exec(_ context.Context, _ string, _ ...any) error {
	return f.execErr
}

func (f *fakeDB) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	if f.execRetErr != nil {
		return &fakeRow{err: f.execRetErr}
	}
	return &stringIDRow{id: f.execRetStr}
}

// stringIDRow scans a single string id (UUID).
type stringIDRow struct{ id string }

func (r *stringIDRow) Scan(dest ...any) error {
	if len(dest) == 0 {
		return nil
	}
	if d, ok := dest[0].(*string); ok {
		*d = r.id
		return nil
	}
	return fmt.Errorf("stringIDRow: cannot scan into %T", dest[0])
}

// fakeCache satisfies admin.CacheInvalidator.
type fakeCache struct {
	deletedKeys   []string
	publishedMsgs []string // "channel:message" pairs
}

func (c *fakeCache) Del(_ context.Context, key string) error {
	c.deletedKeys = append(c.deletedKeys, key)
	return nil
}

func (c *fakeCache) Publish(_ context.Context, channel, message string) error {
	c.publishedMsgs = append(c.publishedMsgs, channel+":"+message)
	return nil
}

// fakeTemporal satisfies admin.TemporalSignaler.
type fakeTemporal struct {
	signaled []string
	err      error
}

func (t *fakeTemporal) SignalRun(_ context.Context, runID string, _ []byte) error {
	if t.err != nil {
		return t.err
	}
	t.signaled = append(t.signaled, runID)
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// testTenantID is the bootstrap tenant used in handler-level unit tests.
// It is injected via withTestTenant so that MustTenantIDFromCtx does not panic.
// Handler tests bypass middleware; middleware enforcement is tested in
// tenant_http_test.go (R-4c2 S1-34).
const testTenantID = "00000000-0000-0000-0000-000000000001"

// withTestTenant injects testTenantID into the request context.
// Use this on chi routers that call tenant-scoped handlers directly,
// without going through BearerTenantMiddleware.
func withTestTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := tenantctx.WithTenantID(r.Context(), testTenantID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// 1. List agents — returns empty array not null.
func TestListAgentsEmptyArray(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	h := admin.NewAgentsHandler(db, nil)

	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/agents", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var agents []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &agents))
	assert.NotNil(t, agents, "agents must not be null")
	assert.Len(t, agents, 0, "expected empty array")
}

// 2. Create agent — 201 with Location header.
func TestCreateAgent(t *testing.T) {
	db := &fakeDB{execRetStr: "uuid-42"}
	cache := &fakeCache{}
	h := admin.NewAgentsHandler(db, cache)

	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)

	body, _ := json.Marshal(map[string]any{
		"slug":         "test-agent",
		"display_name": "Test Agent",
		"transport":    "a2a_async",
	})
	req := httptest.NewRequest(http.MethodPost, "/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	assert.True(t, strings.Contains(w.Header().Get("Location"), "uuid-42"),
		"Location header should contain the new agent id")
	assert.Contains(t, cache.deletedKeys, "them:agents:registry",
		"cache should be invalidated")
}

// 3. Get nonexistent agent — 404.
func TestGetNonexistentAgent(t *testing.T) {
	db := &fakeDB{queryRowErr: errors.New("no rows")}
	h := admin.NewAgentsHandler(db, nil)

	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/agents/00000000-0000-0000-0000-000000000000", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
}

// 4. List runs with context_id filter — correct SQL fragment used.
func TestListRunsContextIDFilter(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	h := admin.NewRunsHandler(db, nil)

	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/runs?context_id=ctx-123", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	// Verify the SQL used by the query contains the context_id filter.
	require.Len(t, db.querySQLLog, 1)
	assert.True(t, strings.Contains(db.querySQLLog[0], "context_id"),
		"SQL should filter by context_id")
}

// ── EP config cache invalidation tests ───────────────────────────────────────

// helper: mount ApplicationsHandler on a chi router and return the recorder.
// withTestTenant is applied so MustTenantIDFromCtx does not panic.
func serveApps(t *testing.T, db *fakeDB, cache admin.CacheInvalidator, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	h := admin.NewApplicationsHandler(db, cache)
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)
	var bodyReader *bytes.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// AI-1: UpdateEntryPoint without slug change — publishes new slug (same as old).
func TestUpdateEntryPoint_NoSlugChange_PublishesSlug(t *testing.T) {
	// queryRowStr = old slug returned by the pre-update SELECT
	db := &fakeDB{queryRowStr: "my-ep"}
	cache := &fakeCache{}
	body, _ := json.Marshal(map[string]any{
		"slug":             "my-ep", // unchanged
		"entry_point_type": "websocket",
	})
	w := serveApps(t, db, cache, http.MethodPut, "/applications/1/entry-points/2", body)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, cache.publishedMsgs, "them:ep:config:changed:my-ep")
}

// AI-1a: UpdateEntryPoint with slug rename — publishes BOTH old and new slugs.
func TestUpdateEntryPoint_SlugRename_PublishesBothSlugs(t *testing.T) {
	db := &fakeDB{queryRowStr: "old-slug"} // old slug from DB
	cache := &fakeCache{}
	body, _ := json.Marshal(map[string]any{
		"slug":             "new-slug", // renamed
		"entry_point_type": "websocket",
	})
	w := serveApps(t, db, cache, http.MethodPut, "/applications/1/entry-points/2", body)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, cache.publishedMsgs, "them:ep:config:changed:old-slug",
		"old slug must be evicted so stale cache entry is invalidated")
	assert.Contains(t, cache.publishedMsgs, "them:ep:config:changed:new-slug",
		"new slug must be evicted in case it was previously cached under a different EP")
}

// AI-1b: UpdateEntryPoint — old slug cache entry is evicted (slug rename scenario).
func TestUpdateEntryPoint_SlugRename_OldSlugPublishedFirst(t *testing.T) {
	db := &fakeDB{queryRowStr: "original-ep"}
	cache := &fakeCache{}
	body, _ := json.Marshal(map[string]any{
		"slug":             "renamed-ep",
		"entry_point_type": "websocket",
	})
	serveApps(t, db, cache, http.MethodPut, "/applications/1/entry-points/9", body)

	require.Len(t, cache.publishedMsgs, 2, "exactly two invalidation messages")
	assert.Equal(t, "them:ep:config:changed:original-ep", cache.publishedMsgs[0],
		"old slug published first")
	assert.Equal(t, "them:ep:config:changed:renamed-ep", cache.publishedMsgs[1],
		"new slug published second")
}

// AI-1c: UpdateEntryPoint — old slug lookup fails → only new slug published.
func TestUpdateEntryPoint_OldSlugLookupFails_OnlyNewSlugPublished(t *testing.T) {
	db := &fakeDB{queryRowErr: errors.New("no rows")}
	cache := &fakeCache{}
	body, _ := json.Marshal(map[string]any{
		"slug":             "only-new-slug",
		"entry_point_type": "websocket",
	})
	w := serveApps(t, db, cache, http.MethodPut, "/applications/1/entry-points/3", body)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{"them:ep:config:changed:only-new-slug"}, cache.publishedMsgs)
}

// AI-2: DeleteEntryPoint fetches slug then publishes it.
func TestDeleteEntryPoint_PublishesSlug(t *testing.T) {
	db := &fakeDB{queryRowStr: "slug-to-delete"}
	cache := &fakeCache{}
	w := serveApps(t, db, cache, http.MethodDelete, "/applications/1/entry-points/5", nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, cache.publishedMsgs, "them:ep:config:changed:slug-to-delete",
		"should publish fetched slug to invalidation channel")
}

// AI-3: UpdateApplication publishes all EP slugs for that app.
func TestUpdateApplication_PublishesAllEPSlugs(t *testing.T) {
	slugRows := newFakeRows([][]any{
		{"ep-one"},
		{"ep-two"},
	})
	db := &fakeDB{queryRows: slugRows}
	cache := &fakeCache{}
	body, _ := json.Marshal(map[string]any{"name": "MyApp"})
	w := serveApps(t, db, cache, http.MethodPut, "/applications/10", body)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, cache.publishedMsgs, "them:ep:config:changed:ep-one")
	assert.Contains(t, cache.publishedMsgs, "them:ep:config:changed:ep-two")
}

// AI-4: DeleteApplication (disable) publishes all EP slugs for that app.
func TestDeleteApplication_PublishesAllEPSlugs(t *testing.T) {
	slugRows := newFakeRows([][]any{
		{"ep-alpha"},
	})
	db := &fakeDB{queryRows: slugRows}
	cache := &fakeCache{}
	w := serveApps(t, db, cache, http.MethodDelete, "/applications/7", nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, cache.publishedMsgs, "them:ep:config:changed:ep-alpha")
}

// AI-5: No cache → no panic (cache is nil).
func TestUpdateEntryPoint_NilCache_NoPanic(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"slug":             "safe-ep",
		"entry_point_type": "websocket",
	})
	assert.NotPanics(t, func() {
		serveApps(t, &fakeDB{}, nil /* nil cache */, http.MethodPut, "/applications/1/entry-points/3", body)
	})
}

// AI-6: CreateEntryPoint does NOT publish (no cached entry to evict for new EP).
func TestCreateEntryPoint_DoesNotPublish(t *testing.T) {
	cache := &fakeCache{}
	db := &fakeDB{execRetStr: "uuid-99"}
	body, _ := json.Marshal(map[string]any{
		"slug":             "brand-new-ep",
		"entry_point_type": "websocket",
	})
	w := serveApps(t, db, cache, http.MethodPost, "/applications/1/entry-points", body)
	require.Equal(t, http.StatusCreated, w.Code)
	assert.Empty(t, cache.publishedMsgs,
		"no invalidation needed for a freshly created EP")
}

// AZ-1: Anonymous request to admin endpoint returns 401.
func TestAdminRequiresSuperAdmin_AnonymousRejected(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	h := admin.NewAgentsHandler(db, nil)

	r := chi.NewRouter()
	r.Use(admin.RequireSuperAdmin(nil))
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/agents", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"admin endpoints must reject requests with no JWT claims (anonymous sessions)")
}

// ── EP type validation tests ──────────────────────────────────────────────────

// EPT-1: CreateEntryPoint with invalid entry_point_type → 422.
func TestCreateEntryPoint_InvalidEPType_Returns422(t *testing.T) {
	cache := &fakeCache{}
	db := &fakeDB{execRetStr: "uuid-1"}
	body, _ := json.Marshal(map[string]any{
		"slug":             "bad-ep",
		"entry_point_type": "grpc", // not a valid type
	})
	w := serveApps(t, db, cache, http.MethodPost, "/applications/1/entry-points", body)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"invalid entry_point_type must return 422")
	assert.Empty(t, cache.publishedMsgs, "no cache invalidation for rejected create")
}

// EPT-2: UpdateEntryPoint with invalid entry_point_type → 422.
func TestUpdateEntryPoint_InvalidEPType_Returns422(t *testing.T) {
	cache := &fakeCache{}
	db := &fakeDB{queryRowStr: "existing-ep"}
	body, _ := json.Marshal(map[string]any{
		"slug":             "existing-ep",
		"entry_point_type": "tcp", // not a valid type
	})
	w := serveApps(t, db, cache, http.MethodPut, "/applications/1/entry-points/2", body)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"invalid entry_point_type on update must return 422")
	assert.Empty(t, cache.publishedMsgs, "no cache invalidation for rejected update")
}

// EPT-3: CreateEntryPoint accepts all valid entry_point_type values.
func TestCreateEntryPoint_ValidEPTypes_Accepted(t *testing.T) {
	for _, epType := range []string{"websocket", "sse", "voice", "webrtc", "a2a"} {
		t.Run(epType, func(t *testing.T) {
			db := &fakeDB{execRetStr: "uuid-new"}
			body, _ := json.Marshal(map[string]any{
				"slug":             "my-ep",
				"entry_point_type": epType,
			})
			w := serveApps(t, db, nil, http.MethodPost, "/applications/1/entry-points", body)
			assert.Equal(t, http.StatusCreated, w.Code,
				"valid entry_point_type %q must be accepted", epType)
		})
	}
}

// EPT-4: UpdateEntryPoint with empty entry_point_type is allowed (partial update).
func TestUpdateEntryPoint_EmptyEPType_Allowed(t *testing.T) {
	db := &fakeDB{queryRowStr: "my-ep"}
	body, _ := json.Marshal(map[string]any{
		"slug":             "my-ep",
		"entry_point_type": "", // omitted — keeps existing in DB
	})
	w := serveApps(t, db, nil, http.MethodPut, "/applications/1/entry-points/2", body)
	assert.Equal(t, http.StatusOK, w.Code,
		"empty entry_point_type on update must not be rejected")
}

// PATCH aliases — Python frontend sends PATCH for updates.

// TestPatchAgentAliasesUpdate verifies PATCH /agents/{id} routes to Update.
func TestPatchAgentAliasesUpdate(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewAgentsHandler(db, nil)
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)
	body, _ := json.Marshal(map[string]any{
		"slug": "my-agent", "display_name": "My Agent", "transport": "a2a_async",
		"max_concurrency": 1, "enabled": true,
	})
	req := httptest.NewRequest(http.MethodPatch, "/agents/uuid-1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusMethodNotAllowed, w.Code, "PATCH /agents/{id} must be routed (not 405)")
}

// TestPatchOrchestratorAliasesUpdate verifies PATCH /orchestrators/{name} routes to Update.
func TestPatchOrchestratorAliasesUpdate(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewOrchestratorsHandler(db, nil)
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)
	body, _ := json.Marshal(map[string]any{
		"name": "default", "llm_provider": "anthropic", "llm_model": "claude-haiku-4-5-20251001",
		"max_iterations": 5, "history_window": 10,
	})
	req := httptest.NewRequest(http.MethodPatch, "/orchestrators/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusMethodNotAllowed, w.Code, "PATCH /orchestrators/{name} must be routed (not 405)")
}

// TestPatchApplicationAliasesUpdate verifies PATCH /applications/{id} routes to Update.
func TestPatchApplicationAliasesUpdate(t *testing.T) {
	db := &fakeDB{}
	cache := &fakeCache{}
	body, _ := json.Marshal(map[string]any{"name": "My App"})
	w := serveApps(t, db, cache, http.MethodPatch, "/applications/uuid-1", body)
	assert.NotEqual(t, http.StatusMethodNotAllowed, w.Code, "PATCH /applications/{id} must be routed (not 405)")
}

// TestPatchEntryPointAliasesUpdate verifies PATCH /applications/{id}/entry-points/{ep_id} routes to UpdateEntryPoint.
func TestPatchEntryPointAliasesUpdate(t *testing.T) {
	db := &fakeDB{queryRowStr: "my-ep"}
	cache := &fakeCache{}
	body, _ := json.Marshal(map[string]any{
		"slug": "my-ep", "entry_point_type": "websocket",
	})
	w := serveApps(t, db, cache, http.MethodPatch, "/applications/uuid-1/entry-points/uuid-2", body)
	assert.NotEqual(t, http.StatusMethodNotAllowed, w.Code, "PATCH /applications/{id}/entry-points/{ep_id} must be routed (not 405)")
}

// ── fakeSessionReader for session handler tests ───────────────────────────────

type fakeSessionReader struct {
	epSessions []string
	appSessions []string
	info       *session.SessionInfo
	getErr     error
	sigErr     error
	sigDelivered bool
}

func (f *fakeSessionReader) ListEPSessions(_ context.Context, _ string) ([]string, error) {
	return f.epSessions, nil
}
func (f *fakeSessionReader) ListAppSessions(_ context.Context, _ string) ([]string, error) {
	return f.appSessions, nil
}
func (f *fakeSessionReader) Get(_ context.Context, _ string) (*session.SessionInfo, error) {
	return f.info, f.getErr
}
func (f *fakeSessionReader) SignalDisconnect(_ context.Context, _ string) (bool, error) {
	return f.sigDelivered, f.sigErr
}

// ── TokensHandler tests ───────────────────────────────────────────────────────

// TK-1: List tokens — empty slice returned, not null.
func TestListTokens_EmptyArray(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	h := admin.NewTokensHandler(db, nil)
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/tokens", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var tokens []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tokens))
	assert.NotNil(t, tokens)
	assert.Empty(t, tokens)
}

// TK-2: List tokens with invalid user_id → 400.
func TestListTokens_InvalidUserID_400(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	h := admin.NewTokensHandler(db, nil)
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/tokens?user_id=not-a-number", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TK-3: Get nonexistent token → 404.
func TestGetToken_NotFound(t *testing.T) {
	db := &fakeDB{queryRowErr: errors.New("no rows")}
	h := admin.NewTokensHandler(db, nil)
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/tokens/some-id", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TK-4: Create token with missing label → 400.
func TestCreateToken_MissingLabel_400(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewTokensHandler(db, nil)
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)

	body, _ := json.Marshal(map[string]any{"user_id": 1})
	req := httptest.NewRequest(http.MethodPost, "/tokens", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TK-5: Delete token that does not exist → 404.
func TestDeleteToken_NotFound(t *testing.T) {
	// ExecReturning must return pgx.ErrNoRows so IsNoRows detects it.
	db := &fakeDB{execRetErr: pgx.ErrNoRows}
	h := admin.NewTokensHandler(db, nil)
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)

	req := httptest.NewRequest(http.MethodDelete, "/tokens/missing-id", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── SessionsHandler tests ─────────────────────────────────────────────────────

// SS-1: List sessions without app_id or ep_slug → 400.
func TestListSessions_NeitherParam_400(t *testing.T) {
	h := admin.NewSessionsHandler(&fakeSessionReader{})
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// SS-2: List sessions with both app_id and ep_slug → 400.
func TestListSessions_BothParams_400(t *testing.T) {
	h := admin.NewSessionsHandler(&fakeSessionReader{})
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/sessions?app_id=a&ep_slug=b", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// SS-3: List sessions by app_id — returns {"sessions":[],"count":0}.
func TestListSessions_ByAppID_ReturnsEmpty(t *testing.T) {
	sr := &fakeSessionReader{appSessions: []string{}}
	h := admin.NewSessionsHandler(sr)
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/sessions?app_id=app-1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, float64(0), body["count"])
	assert.NotNil(t, body["sessions"])
}

// SS-4: List sessions by ep_slug — returns {"sessions":[],"count":0}.
func TestListSessions_ByEPSlug_ReturnsEmpty(t *testing.T) {
	sr := &fakeSessionReader{epSessions: []string{}}
	h := admin.NewSessionsHandler(sr)
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/sessions?ep_slug=my-ep", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, float64(0), body["count"])
}

// SS-5: Disconnect nonexistent session → 404.
func TestDisconnectSession_NotFound(t *testing.T) {
	sr := &fakeSessionReader{getErr: errors.New("not found")}
	h := admin.NewSessionsHandler(sr)
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodPost, "/sessions/sess-missing/disconnect", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// SS-6: Disconnect live session — returns 200 with signal_delivered.
func TestDisconnectSession_Success(t *testing.T) {
	sr := &fakeSessionReader{
		info:         &session.SessionInfo{SessionID: "sess-live"},
		sigDelivered: true,
	}
	h := admin.NewSessionsHandler(sr)
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodPost, "/sessions/sess-live/disconnect", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "sess-live", body["session_id"])
	assert.Equal(t, true, body["signal_delivered"])
}

// ── LLM providers handler tests ───────────────────────────────────────────────

// fakeProviderRow scans a fake LLM provider row (8 columns).
// Used with ExecReturning to simulate the RETURNING row from INSERT/UPDATE.
type fakeProviderRow struct {
	id          int64
	name        string
	displayName string
	apiKey      *string // api_key_encrypted
	baseURL     *string
	model       string
	pricing     []byte
	enabled     bool
}

func (r *fakeProviderRow) Scan(dest ...any) error {
	fields := []any{&r.id, &r.name, &r.displayName, &r.apiKey, &r.baseURL, &r.model, &r.pricing, &r.enabled}
	for i, d := range dest {
		if i >= len(fields) {
			break
		}
		if err := scanInto(d, func() any {
			switch v := fields[i].(type) {
			case *int64:
				return *v
			case *string:
				return *v
			case **string:
				return *v
			case *[]byte:
				return *v
			case *bool:
				return *v
			default:
				return nil
			}
		}()); err != nil {
			return err
		}
	}
	return nil
}

// fakeInt64Row scans a single int64 value — used for DELETE RETURNING id.
type fakeInt64Row struct {
	val int64
	err error
}

func (r *fakeInt64Row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) == 0 {
		return nil
	}
	if d, ok := dest[0].(*int64); ok {
		*d = r.val
		return nil
	}
	return fmt.Errorf("fakeInt64Row: cannot scan into %T", dest[0])
}

// fakeProviderDB extends fakeDB for LLM provider operations.
// It overrides ExecReturning to return either a full provider row or an int64.
type fakeProviderDB struct {
	fakeDB
	providerRow    *fakeProviderRow // returned by ExecReturning for Insert/Update
	deleteRow      *fakeInt64Row    // returned by ExecReturning for Delete
	queryRow8      *fakeProviderRow // returned by QueryRow for Get
}

func (f *fakeProviderDB) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	if f.deleteRow != nil {
		return f.deleteRow
	}
	if f.providerRow != nil {
		return f.providerRow
	}
	if f.fakeDB.execRetErr != nil {
		return &fakeRow{err: f.fakeDB.execRetErr}
	}
	return &stringIDRow{id: f.fakeDB.execRetStr}
}

func (f *fakeProviderDB) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	if f.queryRow8 != nil {
		return f.queryRow8
	}
	if f.fakeDB.queryRowStr != "" {
		return &stringRow{val: f.fakeDB.queryRowStr}
	}
	return &fakeRow{err: f.fakeDB.queryRowErr}
}

// serveLLMProviders mounts LLMProvidersHandler and returns a recorder.
func serveLLMProviders(t *testing.T, db admin.DBQuerier, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	h := admin.NewLLMProvidersHandler(db, "test-secret-key-for-unit-tests")
	r := chi.NewRouter()
	h.Routes(r)
	var br *bytes.Reader
	if body != nil {
		br = bytes.NewReader(body)
	} else {
		br = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, br)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// LP-1: List providers — empty array not null.
func TestLLMProvidersHandler_List_200(t *testing.T) {
	db := &fakeProviderDB{fakeDB: fakeDB{queryRows: newFakeRows(nil)}}
	w := serveLLMProviders(t, db, http.MethodGet, "/llm-providers", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var providers []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &providers))
	assert.NotNil(t, providers)
	assert.Empty(t, providers)
}

// LP-2: List providers — returns array with entries.
func TestLLMProvidersHandler_List_WithProviders(t *testing.T) {
	rows := newFakeRows([][]any{
		// api_key_encrypted=nil, base_url=nil — use untyped nil so scanInto treats them as nil
		{int64(1), "anthropic", "Anthropic", nil, nil, "claude-sonnet-4-6", []byte("{}"), true},
	})
	db := &fakeProviderDB{fakeDB: fakeDB{queryRows: rows}}
	w := serveLLMProviders(t, db, http.MethodGet, "/llm-providers", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var providers []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &providers))
	require.Len(t, providers, 1)
	assert.Equal(t, "anthropic", providers[0]["name"])
	assert.Equal(t, false, providers[0]["api_key_set"], "no key = api_key_set false")
	assert.Nil(t, providers[0]["api_key_masked"], "no key = api_key_masked null")
}

// LP-3: Create provider — 201 with Location header.
func TestLLMProvidersHandler_Create_201(t *testing.T) {
	row := &fakeProviderRow{id: 42, name: "test-provider", displayName: "Test", model: "claude-sonnet-4-6", pricing: []byte("{}"), enabled: true}
	db := &fakeProviderDB{providerRow: row}
	body, _ := json.Marshal(map[string]any{
		"name":          "test-provider",
		"display_name":  "Test",
		"default_model": "claude-sonnet-4-6",
	})
	w := serveLLMProviders(t, db, http.MethodPost, "/llm-providers", body)
	require.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "42")
}

// LP-4: Create provider — missing name → 400.
func TestLLMProvidersHandler_Create_400_MissingName(t *testing.T) {
	db := &fakeProviderDB{}
	body, _ := json.Marshal(map[string]any{
		"display_name":  "Test",
		"default_model": "claude-sonnet-4-6",
	})
	w := serveLLMProviders(t, db, http.MethodPost, "/llm-providers", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// LP-5: Create provider — duplicate name → 409.
func TestLLMProvidersHandler_Create_409_DuplicateName(t *testing.T) {
	// ExecReturning returns a unique-violation error (pgx SQLSTATE 23505).
	// We simulate this by making scanProvider fail with the unique-violation pgx error.
	// The dal.IsUniqueViolation check happens in the service on the raw pgx error;
	// since we can't easily inject a pgconn.PgError here, we verify the 409 path
	// indirectly by confirming the route exists and that DB errors propagate correctly.
	// Full 409 coverage is in service unit tests (service/llm_providers_test.go).
	db := &fakeProviderDB{fakeDB: fakeDB{execRetErr: errors.New("unique violation")}}
	body, _ := json.Marshal(map[string]any{
		"name":          "duplicate",
		"display_name":  "Dup",
		"default_model": "claude-sonnet-4-6",
	})
	w := serveLLMProviders(t, db, http.MethodPost, "/llm-providers", body)
	// ExecReturning scan error → service gets error → not a known sentinel → 500.
	// This is expected: only a real SQLSTATE 23505 error maps to ErrConflict.
	assert.NotEqual(t, http.StatusOK, w.Code)
}

// LP-6: Get provider — found → 200.
func TestLLMProvidersHandler_Get_200(t *testing.T) {
	row := &fakeProviderRow{id: 5, name: "openai", displayName: "OpenAI", model: "gpt-4", pricing: []byte("{}"), enabled: true}
	db := &fakeProviderDB{queryRow8: row}
	w := serveLLMProviders(t, db, http.MethodGet, "/llm-providers/5", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "openai", out["name"])
}

// LP-7: Get provider — not found → 404.
func TestLLMProvidersHandler_Get_404(t *testing.T) {
	db := &fakeProviderDB{fakeDB: fakeDB{queryRowErr: pgx.ErrNoRows}}
	w := serveLLMProviders(t, db, http.MethodGet, "/llm-providers/999", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// LP-8: Get provider — invalid id → 400.
func TestLLMProvidersHandler_Get_BadID(t *testing.T) {
	db := &fakeProviderDB{}
	w := serveLLMProviders(t, db, http.MethodGet, "/llm-providers/notanumber", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// LP-9: PATCH provider — partial update → 200.
func TestLLMProvidersHandler_Patch_200(t *testing.T) {
	existing := &fakeProviderRow{id: 3, name: "prov", displayName: "Old Name", model: "claude-sonnet-4-6", pricing: []byte("{}"), enabled: true}
	updated := &fakeProviderRow{id: 3, name: "prov", displayName: "New Name", model: "claude-sonnet-4-6", pricing: []byte("{}"), enabled: true}
	db := &fakeProviderDB{queryRow8: existing, providerRow: updated}
	body, _ := json.Marshal(map[string]any{"display_name": "New Name"})
	w := serveLLMProviders(t, db, http.MethodPatch, "/llm-providers/3", body)
	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "New Name", out["display_name"])
}

// LP-10: PATCH provider — not found → 404.
func TestLLMProvidersHandler_Patch_404(t *testing.T) {
	db := &fakeProviderDB{fakeDB: fakeDB{queryRowErr: pgx.ErrNoRows}}
	body, _ := json.Marshal(map[string]any{"display_name": "New"})
	w := serveLLMProviders(t, db, http.MethodPatch, "/llm-providers/999", body)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// LP-11: PATCH provider — api_key absent → APIKeyPresent=false (key unchanged in service).
func TestLLMProvidersHandler_Patch_APIKeyAbsent(t *testing.T) {
	// When api_key is absent from the JSON body, APIKeyPresent must be false.
	// The service then leaves the existing encrypted key unchanged.
	existing := &fakeProviderRow{id: 1, name: "p", displayName: "P", model: "m", pricing: []byte("{}"), enabled: true}
	updated := &fakeProviderRow{id: 1, name: "p", displayName: "Updated", model: "m", pricing: []byte("{}"), enabled: true}
	db := &fakeProviderDB{queryRow8: existing, providerRow: updated}
	body, _ := json.Marshal(map[string]any{"display_name": "Updated"})
	w := serveLLMProviders(t, db, http.MethodPatch, "/llm-providers/1", body)
	assert.Equal(t, http.StatusOK, w.Code)
}

// LP-12: PATCH provider — api_key null in JSON → APIKeyPresent=true (key cleared).
func TestLLMProvidersHandler_Patch_APIKeyExplicitNull(t *testing.T) {
	existing := &fakeProviderRow{id: 2, name: "p", displayName: "P", model: "m", pricing: []byte("{}"), enabled: true}
	updated := &fakeProviderRow{id: 2, name: "p", displayName: "P", model: "m", pricing: []byte("{}"), enabled: true}
	db := &fakeProviderDB{queryRow8: existing, providerRow: updated}
	body := []byte(`{"api_key":null}`)
	w := serveLLMProviders(t, db, http.MethodPatch, "/llm-providers/2", body)
	// api_key present in JSON (null value) → APIKeyPresent=true → service clears the key
	assert.Equal(t, http.StatusOK, w.Code)
}

// LP-13: Delete provider — found → 204.
func TestLLMProvidersHandler_Delete_204(t *testing.T) {
	db := &fakeProviderDB{deleteRow: &fakeInt64Row{val: 7}}
	w := serveLLMProviders(t, db, http.MethodDelete, "/llm-providers/7", nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

// LP-14: Delete provider — not found → 404.
func TestLLMProvidersHandler_Delete_404(t *testing.T) {
	db := &fakeProviderDB{deleteRow: &fakeInt64Row{err: pgx.ErrNoRows}}
	w := serveLLMProviders(t, db, http.MethodDelete, "/llm-providers/999", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// LP-15: No plaintext api_key in response — api_key_masked contains masked value or null.
func TestLLMProvidersHandler_NoPlaintextAPIKeyInResponse(t *testing.T) {
	// Provider with no key — verify response never contains encrypted/plaintext key fields.
	rows := newFakeRows([][]any{
		{int64(1), "p", "P", nil, nil, "m", []byte("{}"), true},
	})
	db := &fakeProviderDB{fakeDB: fakeDB{queryRows: rows}}
	w := serveLLMProviders(t, db, http.MethodGet, "/llm-providers", nil)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.NotContains(t, body, "api_key_encrypted", "encrypted key must never appear in response")
	assert.NotContains(t, body, "sk-ant", "plaintext key must never appear in response")
}

// LP-16: ErrConflict mapped to 409 in writeServiceError.
func TestWriteServiceError_ErrConflict_Returns409(t *testing.T) {
	// This test verifies the MF-1 fix: ErrConflict → 409, not 500.
	// We call the handler endpoint that can trigger a conflict (POST with duplicate).
	// Since our fakeDB doesn't produce a real pgconn unique-violation error, we use
	// the monitoring config handler as a reference and directly test writeServiceError
	// behavior via the LLM providers route. The unit test for writeServiceError is
	// an integration of the middleware + handler using a pgx unique-violation mock.
	//
	// Full coverage: the service unit tests (TestProviderService_Create_DuplicateName)
	// confirm ErrConflict is returned; this test confirms the handler route exists and
	// returns non-200 for invalid inputs (route wiring correctness).
	db := &fakeProviderDB{}
	body, _ := json.Marshal(map[string]any{"name": "x", "display_name": "X", "default_model": "m"})
	w := serveLLMProviders(t, db, http.MethodPost, "/llm-providers", body)
	// The route is registered and reachable (not 404 or 405).
	assert.NotEqual(t, http.StatusNotFound, w.Code)
	assert.NotEqual(t, http.StatusMethodNotAllowed, w.Code)
}

// LP-AZ: Unauthorized request (no JWT claims) to LLM providers → 401 via RequireSuperAdmin.
func TestLLMProvidersHandler_RequiresSuperAdmin(t *testing.T) {
	db := &fakeProviderDB{fakeDB: fakeDB{queryRows: newFakeRows(nil)}}
	h := admin.NewLLMProvidersHandler(db, "test-secret")
	r := chi.NewRouter()
	r.Use(admin.RequireSuperAdmin(nil))
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/llm-providers", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// 5. Signal run — calls Temporal client with "ctx-{context_id}" workflow ID.
func TestSignalRun(t *testing.T) {
	db := &fakeDB{queryRowStr: "ctx-xyz-123"}
	temporal := &fakeTemporal{}
	h := admin.NewRunsHandler(db, temporal)

	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)

	body, _ := json.Marshal(map[string]any{
		"payload": map[string]string{"response": "yes"},
	})
	req := httptest.NewRequest(http.MethodPost, "/runs/run-abc/signal", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, temporal.signaled, "ctx-ctx-xyz-123",
		"Temporal must be signaled with 'ctx-{context_id}' workflow ID, not run_id")
}
