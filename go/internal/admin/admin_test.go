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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
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

func (r *fakeRows) Next() bool  { return r.pos < len(r.data) }
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
	case *bool:
		switch v := src.(type) {
		case bool:
			*d = v
		default:
			return fmt.Errorf("scanInto: cannot assign %T to *bool", src)
		}
	default:
		return fmt.Errorf("scanInto: unsupported dest type %T", dest)
	}
	return nil
}

// fakeDB satisfies admin.DBQuerier.
type fakeDB struct {
	queryRows    *fakeRows // returned by Query
	queryRowErr  error     // error returned by QueryRow's Scan
	queryRowStr  string    // string value scanned by QueryRow (e.g. slug lookup)
	execErr      error     // returned by Exec
	execRetID    int64     // id returned by ExecReturning
	execRetErr   error     // error returned by ExecReturning's Scan
	querySQLLog  []string  // log of executed SQL
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

// stringRow scans a single string value (used for slug lookups).
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
	return &idRow{id: f.execRetID}
}

// idRow scans a single int64 id.
type idRow struct{ id int64 }

func (r *idRow) Scan(dest ...any) error {
	if len(dest) == 0 {
		return nil
	}
	if d, ok := dest[0].(*int64); ok {
		*d = r.id
		return nil
	}
	return fmt.Errorf("idRow: cannot scan into %T", dest[0])
}

// fakeCache satisfies admin.CacheInvalidator.
type fakeCache struct {
	deletedKeys    []string
	publishedMsgs  []string // channel:message pairs stored as "channel:message"
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

// ── Tests ─────────────────────────────────────────────────────────────────────

// 1. List agents — returns empty array not null.
func TestListAgentsEmptyArray(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	h := admin.NewAgentsHandler(db, nil)

	r := chi.NewRouter()
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
	db := &fakeDB{execRetID: 42}
	cache := &fakeCache{}
	h := admin.NewAgentsHandler(db, cache)

	r := chi.NewRouter()
	h.Routes(r)

	body, _ := json.Marshal(map[string]any{
		"slug":        "test-agent",
		"name":        "Test Agent",
		"adapter_type": "mock",
	})
	req := httptest.NewRequest(http.MethodPost, "/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	assert.True(t, strings.Contains(w.Header().Get("Location"), "42"),
		"Location header should contain the new agent id")
	assert.Contains(t, cache.deletedKeys, "them:agents:registry",
		"cache should be invalidated")
}

// 3. Get nonexistent agent — 404.
func TestGetNonexistentAgent(t *testing.T) {
	db := &fakeDB{queryRowErr: errors.New("no rows")}
	h := admin.NewAgentsHandler(db, nil)

	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/agents/99999", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
}

// 4. List runs with context_id filter — correct SQL fragment used.
func TestListRunsContextIDFilter(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	h := admin.NewRunsHandler(db, nil)

	r := chi.NewRouter()
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
func serveApps(t *testing.T, db *fakeDB, cache admin.CacheInvalidator, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	h := admin.NewApplicationsHandler(db, cache)
	r := chi.NewRouter()
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
		"slug":    "my-ep", // unchanged
		"name":    "My EP",
		"ep_type": "websocket",
	})
	w := serveApps(t, db, cache, http.MethodPut, "/applications/1/entry-points/2", body)
	require.Equal(t, http.StatusOK, w.Code)
	// Both old and new slugs are published — when they're the same value it
	// appears twice. The subscriber calls Invalidate once per message; idempotent.
	assert.Contains(t, cache.publishedMsgs, "them:ep:config:changed:my-ep")
}

// AI-1a: UpdateEntryPoint with slug rename — publishes BOTH old and new slugs.
func TestUpdateEntryPoint_SlugRename_PublishesBothSlugs(t *testing.T) {
	db := &fakeDB{queryRowStr: "old-slug"} // old slug from DB
	cache := &fakeCache{}
	body, _ := json.Marshal(map[string]any{
		"slug":    "new-slug", // renamed
		"name":    "My EP",
		"ep_type": "websocket",
	})
	w := serveApps(t, db, cache, http.MethodPut, "/applications/1/entry-points/2", body)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, cache.publishedMsgs, "them:ep:config:changed:old-slug",
		"old slug must be evicted so stale cache entry is invalidated")
	assert.Contains(t, cache.publishedMsgs, "them:ep:config:changed:new-slug",
		"new slug must be evicted in case it was previously cached under a different EP")
}

// AI-1b: UpdateEntryPoint — old slug cache entry is evicted (slug rename scenario).
// Verifies the cache eviction side: old slug is published first, new slug second.
func TestUpdateEntryPoint_SlugRename_OldSlugPublishedFirst(t *testing.T) {
	db := &fakeDB{queryRowStr: "original-ep"}
	cache := &fakeCache{}
	body, _ := json.Marshal(map[string]any{
		"slug":    "renamed-ep",
		"ep_type": "websocket",
	})
	serveApps(t, db, cache, http.MethodPut, "/applications/1/entry-points/9", body)

	require.Len(t, cache.publishedMsgs, 2, "exactly two invalidation messages")
	assert.Equal(t, "them:ep:config:changed:original-ep", cache.publishedMsgs[0],
		"old slug published first")
	assert.Equal(t, "them:ep:config:changed:renamed-ep", cache.publishedMsgs[1],
		"new slug published second")
}

// AI-1c: UpdateEntryPoint — old slug lookup fails (row not found) → only new slug published.
// Ensures handler does not error when slug pre-fetch returns nothing.
func TestUpdateEntryPoint_OldSlugLookupFails_OnlyNewSlugPublished(t *testing.T) {
	// queryRowStr="" and queryRowErr set → Scan returns error → oldSlug stays ""
	db := &fakeDB{queryRowErr: errors.New("no rows")}
	cache := &fakeCache{}
	body, _ := json.Marshal(map[string]any{
		"slug":    "only-new-slug",
		"ep_type": "websocket",
	})
	w := serveApps(t, db, cache, http.MethodPut, "/applications/1/entry-points/3", body)
	require.Equal(t, http.StatusOK, w.Code)
	// Empty old slug is skipped by invalidateEP guard; only new slug published.
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
	body, _ := json.Marshal(map[string]any{"name": "MyApp", "slug": "my-app"})
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
		"slug":    "safe-ep",
		"ep_type": "websocket",
	})
	assert.NotPanics(t, func() {
		serveApps(t, &fakeDB{}, nil /* nil cache */, http.MethodPut, "/applications/1/entry-points/3", body)
	})
}

// AI-6: CreateEntryPoint does NOT publish (no cached entry to evict for new EP).
func TestCreateEntryPoint_DoesNotPublish(t *testing.T) {
	cache := &fakeCache{}
	db := &fakeDB{execRetID: 99}
	body, _ := json.Marshal(map[string]any{
		"slug":    "brand-new-ep",
		"ep_type": "websocket",
	})
	w := serveApps(t, db, cache, http.MethodPost, "/applications/1/entry-points", body)
	require.Equal(t, http.StatusCreated, w.Code)
	assert.Empty(t, cache.publishedMsgs,
		"no invalidation needed for a freshly created EP")
}

// AZ-1: Anonymous request to admin endpoint returns 401 — RequireSuperAdmin middleware
// rejects requests with no JWT claims in context (e.g., public EP anonymous sessions).
func TestAdminRequiresSuperAdmin_AnonymousRejected(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	h := admin.NewAgentsHandler(db, nil)

	r := chi.NewRouter()
	// Wire RequireSuperAdmin the same way main.go does.
	r.Use(admin.RequireSuperAdmin(nil))
	h.Routes(r)

	// No Authorization header, no JWT claims in context — anonymous request.
	req := httptest.NewRequest(http.MethodGet, "/agents", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"admin endpoints must reject requests with no JWT claims (anonymous sessions)")
}

// ── EP type validation tests ──────────────────────────────────────────────────

// EPT-1: CreateEntryPoint with invalid ep_type → 422 Unprocessable Entity.
func TestCreateEntryPoint_InvalidEPType_Returns422(t *testing.T) {
	cache := &fakeCache{}
	db := &fakeDB{execRetID: 1}
	body, _ := json.Marshal(map[string]any{
		"slug":    "bad-ep",
		"ep_type": "grpc", // not a valid type
	})
	w := serveApps(t, db, cache, http.MethodPost, "/applications/1/entry-points", body)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"invalid ep_type must return 422")
	assert.Empty(t, cache.publishedMsgs, "no cache invalidation for rejected create")
}

// EPT-2: UpdateEntryPoint with invalid ep_type → 422 Unprocessable Entity.
func TestUpdateEntryPoint_InvalidEPType_Returns422(t *testing.T) {
	cache := &fakeCache{}
	db := &fakeDB{queryRowStr: "existing-ep"}
	body, _ := json.Marshal(map[string]any{
		"slug":    "existing-ep",
		"ep_type": "tcp", // not a valid type
	})
	w := serveApps(t, db, cache, http.MethodPut, "/applications/1/entry-points/2", body)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"invalid ep_type on update must return 422")
	assert.Empty(t, cache.publishedMsgs, "no cache invalidation for rejected update")
}

// EPT-3: CreateEntryPoint accepts all valid ep_type values.
func TestCreateEntryPoint_ValidEPTypes_Accepted(t *testing.T) {
	for _, epType := range []string{"websocket", "sse", "voice"} {
		t.Run(epType, func(t *testing.T) {
			db := &fakeDB{execRetID: 1}
			body, _ := json.Marshal(map[string]any{
				"slug":    "my-ep",
				"ep_type": epType,
			})
			w := serveApps(t, db, nil, http.MethodPost, "/applications/1/entry-points", body)
			assert.Equal(t, http.StatusCreated, w.Code,
				"valid ep_type %q must be accepted", epType)
		})
	}
}

// EPT-4: UpdateEntryPoint with empty ep_type is allowed (partial update — keeps existing).
func TestUpdateEntryPoint_EmptyEPType_Allowed(t *testing.T) {
	db := &fakeDB{queryRowStr: "my-ep"}
	body, _ := json.Marshal(map[string]any{
		"slug":    "my-ep",
		"ep_type": "", // omitted / empty — not a rename, just updating other fields
	})
	w := serveApps(t, db, nil, http.MethodPut, "/applications/1/entry-points/2", body)
	// Empty ep_type on update is allowed (the DB keeps the existing value).
	assert.Equal(t, http.StatusOK, w.Code,
		"empty ep_type on update must not be rejected")
}

// 5. Signal run — calls Temporal client with "ctx-{context_id}" workflow ID.
func TestSignalRun(t *testing.T) {
	// queryRowStr is returned by fakeDB.QueryRow — simulates context_id lookup.
	db := &fakeDB{queryRowStr: "ctx-xyz-123"}
	temporal := &fakeTemporal{}
	h := admin.NewRunsHandler(db, temporal)

	r := chi.NewRouter()
	h.Routes(r)

	body, _ := json.Marshal(map[string]any{
		"payload": map[string]string{"response": "yes"},
	})
	req := httptest.NewRequest(http.MethodPost, "/runs/run-abc/signal", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// Signal must target "ctx-{context_id}" — the Temporal workflow ID scheme
	// used by Python's OrchestrationWorkflow.
	assert.Contains(t, temporal.signaled, "ctx-ctx-xyz-123",
		"Temporal must be signaled with 'ctx-{context_id}' workflow ID, not run_id")
}
