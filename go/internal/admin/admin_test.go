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
	return &fakeRow{err: f.queryRowErr}
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
	deletedKeys []string
}

func (c *fakeCache) Del(_ context.Context, key string) error {
	c.deletedKeys = append(c.deletedKeys, key)
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

// 5. Signal run — calls Temporal client.
func TestSignalRun(t *testing.T) {
	db := &fakeDB{}
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
	assert.Contains(t, temporal.signaled, "run-abc",
		"Temporal should have been signaled for run-abc")
}
