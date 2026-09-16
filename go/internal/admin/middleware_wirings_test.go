package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/tenantctx"
)

// mwDB is a minimal DBQuerier stub for middleware wiring handler tests.
type mwDB struct {
	execSQL  string
	queryErr error
	// rows returned by Query (list/defs)
	listRows [][]any
	// row returned by QueryRow (get single wiring)
	getRow []any
}

func (d *mwDB) Exec(_ context.Context, sql string, _ ...any) error {
	d.execSQL = sql
	return d.queryErr
}

func (d *mwDB) Query(_ context.Context, _ string, _ ...any) (admin.RowScanner, error) {
	return newFakeRows(d.listRows), d.queryErr
}

func (d *mwDB) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	if d.queryErr != nil {
		return &fakeRow{err: d.queryErr}
	}
	return &mwRow{vals: d.getRow}
}

func (d *mwDB) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	return &fakeRow{}
}

// mwRow scans a variable-length row.
type mwRow struct {
	vals []any
	pos  int
}

func (r *mwRow) Scan(dest ...any) error {
	for i, d := range dest {
		if i >= len(r.vals) {
			break
		}
		switch v := d.(type) {
		case *string:
			if s, ok := r.vals[i].(string); ok {
				*v = s
			}
		case *bool:
			if b, ok := r.vals[i].(bool); ok {
				*v = b
			}
		case *int:
			if n, ok := r.vals[i].(int); ok {
				*v = n
			}
		case *[]byte:
			switch val := r.vals[i].(type) {
			case []byte:
				*v = val
			case string:
				*v = []byte(val)
			}
		}
	}
	return nil
}

// wiringRow builds a fake scan row for a MiddlewareWiring.
func wiringRow() []any {
	return []any{
		"wiring-uuid-1",           // id
		"app-uuid-1",              // application_id
		"agent-uuid-1",            // agent_id
		"crm-agent",               // agent_slug
		"def-uuid-1",              // def_id
		"file-guard",              // def_slug
		0,                         // position
		[]byte(`{"enabled":true}`), // config_override
		true,                      // enabled
		"node-1",                  // node_id
		"2026-09-15T00:00:00Z",    // created_at
		"2026-09-15T00:00:00Z",    // updated_at
	}
}

func buildMWHandler(db admin.DBQuerier) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := tenantctx.WithTenantID(r.Context(), "00000000-0000-0000-0000-000000000001")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	h := admin.NewMiddlewareWiringsHandler(db, nil)
	r.Route("/applications/{id}", func(r chi.Router) {
		h.MountOn(r)
	})
	return r
}

// TestMiddlewareWirings_ListDefs returns 200 with emoji/color/bg_color fields.
func TestMiddlewareWirings_ListDefs(t *testing.T) {
	// Simulate a middleware_defs row with all three new visual columns.
	db := &mwDB{listRows: [][]any{
		{
			"def-uuid-1",             // id
			"file-guard",             // slug
			"guard",                  // kind
			"File Guard",             // display_name
			"Scans uploaded files",   // description
			[]byte(`{}`),             // config
			true,                     // is_builtin
			"builtin",                // scope
			"🛡️",                      // emoji
			"#f59e0b",                // color
			"rgba(245,158,11,0.08)", // bg_color
		},
	}}

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := tenantctx.WithTenantID(r.Context(), "00000000-0000-0000-0000-000000000001")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	h := admin.NewMiddlewareWiringsHandler(db, nil)
	r.Get("/middleware-defs", h.ListDefs)

	req := httptest.NewRequest(http.MethodGet, "/middleware-defs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var out []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 def, got %d", len(out))
	}
	d := out[0]
	if d["emoji"] != "🛡️" {
		t.Errorf("expected emoji '🛡️', got %q", d["emoji"])
	}
	if d["color"] != "#f59e0b" {
		t.Errorf("expected color '#f59e0b', got %q", d["color"])
	}
	if d["bg_color"] != "rgba(245,158,11,0.08)" {
		t.Errorf("expected bg_color 'rgba(245,158,11,0.08)', got %q", d["bg_color"])
	}
}

// TestMiddlewareWirings_List returns 200 with empty array when no wirings.
func TestMiddlewareWirings_List(t *testing.T) {
	db := &mwDB{listRows: nil}
	h := buildMWHandler(db)

	req := httptest.NewRequest(http.MethodGet, "/applications/app-1/middleware-wirings", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var out []any
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty array, got %d items", len(out))
	}
}

// TestMiddlewareWirings_Create_MissingAgentID returns 400.
func TestMiddlewareWirings_Create_MissingAgentID(t *testing.T) {
	db := &mwDB{}
	h := buildMWHandler(db)

	body, _ := json.Marshal(map[string]any{"def_slug": "file-guard"})
	req := httptest.NewRequest(http.MethodPost, "/applications/app-1/middleware-wirings",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestMiddlewareWirings_Create_InvalidJSON returns 400.
func TestMiddlewareWirings_Create_InvalidJSON(t *testing.T) {
	db := &mwDB{}
	h := buildMWHandler(db)

	req := httptest.NewRequest(http.MethodPost, "/applications/app-1/middleware-wirings",
		bytes.NewBufferString("not-json"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestMiddlewareWirings_Delete returns 204.
func TestMiddlewareWirings_Delete(t *testing.T) {
	db := &mwDB{}
	h := buildMWHandler(db)

	req := httptest.NewRequest(http.MethodDelete,
		"/applications/app-1/middleware-wirings/wiring-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if db.execSQL == "" {
		t.Error("expected DELETE SQL to be executed")
	}
}
