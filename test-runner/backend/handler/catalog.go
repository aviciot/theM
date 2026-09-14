package handler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/aviciot/them-test-runner/config"
	"github.com/aviciot/them-test-runner/them"
	"github.com/go-chi/chi/v5"
	_ "github.com/lib/pq"
)

var (
	dbMu sync.RWMutex
	db   *sql.DB
)

func getDB() (*sql.DB, error) {
	dbMu.RLock()
	if db != nil {
		d := db
		dbMu.RUnlock()
		return d, nil
	}
	dbMu.RUnlock()

	cfg := config.Get()
	if cfg.DBURL == "" {
		return nil, fmt.Errorf("DB_URL not configured")
	}
	d, err := sql.Open("postgres", cfg.DBURL)
	if err != nil {
		return nil, err
	}
	if err = d.Ping(); err != nil {
		return nil, fmt.Errorf("db ping: %w", err)
	}
	dbMu.Lock()
	db = d
	dbMu.Unlock()
	return d, nil
}

func newClient() (*them.Client, error) {
	cfg := config.Get()
	return them.NewClient(cfg.ThemURL, cfg.AdminUser, cfg.AdminPass)
}

func ListTenants(w http.ResponseWriter, r *http.Request) {
	d, err := getDB()
	if err == nil {
		// Fast path: query DB directly.
		rows, qErr := d.QueryContext(context.Background(),
			`SELECT id::text, slug, display_name, enabled FROM them.tenants WHERE enabled=true ORDER BY slug`)
		if qErr == nil {
			defer rows.Close()
			type T struct {
				ID          string `json:"id"`
				Slug        string `json:"slug"`
				DisplayName string `json:"display_name"`
				Enabled     bool   `json:"enabled"`
			}
			var out []T
			for rows.Next() {
				var t T
				if rows.Scan(&t.ID, &t.Slug, &t.DisplayName, &t.Enabled) == nil {
					out = append(out, t)
				}
			}
			if out == nil {
				out = []T{}
			}
			writeJSON(w, 200, out)
			return
		}
	}
	// Fallback: use the-M admin API.
	client, err := newClient()
	if err != nil {
		writeError(w, 502, "cannot connect to the-M: "+err.Error())
		return
	}
	tenants, err := client.ListTenants()
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, tenants)
}

func ListApps(w http.ResponseWriter, r *http.Request) {
	tenantSlug := chi.URLParam(r, "slug")
	d, err := getDB()
	if err != nil {
		slog.Error("ListApps: getDB failed", "error", err)
		writeError(w, 502, "db unavailable: "+err.Error())
		return
	}
	rows, qErr := d.QueryContext(context.Background(),
		`SELECT a.id::text, a.slug, a.name, a.enabled
		 FROM them.applications a
		 JOIN them.tenants t ON t.id = a.tenant_id
		 WHERE t.slug = $1 AND a.enabled = true
		 ORDER BY a.name`, tenantSlug)
	if qErr != nil {
		slog.Error("ListApps: query failed", "tenant", tenantSlug, "error", qErr)
		writeError(w, 502, "query failed: "+qErr.Error())
		return
	}
	defer rows.Close()
	type A struct {
		ID      string `json:"id"`
		Slug    string `json:"slug"`
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	var out []A
	for rows.Next() {
		var a A
		if rows.Scan(&a.ID, &a.Slug, &a.Name, &a.Enabled) == nil {
			out = append(out, a)
		}
	}
	if out == nil {
		out = []A{}
	}
	writeJSON(w, 200, out)
}

func ListEPs(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	d, err := getDB()
	if err != nil {
		slog.Error("ListEPs: getDB failed", "error", err)
		writeError(w, 502, "db unavailable: "+err.Error())
		return
	}
	rows, qErr := d.QueryContext(context.Background(),
		`SELECT id::text, slug, entry_point_type,
		        access_policy::text, allowed_principals, enabled
		 FROM them.entry_points
		 WHERE application_id = $1::uuid AND enabled = true
		 ORDER BY slug`, appID)
	if qErr != nil {
		slog.Error("ListEPs: query failed", "app_id", appID, "error", qErr)
		writeError(w, 502, "query failed: "+qErr.Error())
		return
	}
	defer rows.Close()
	type E struct {
		ID                string `json:"id"`
		Slug              string `json:"slug"`
		EntryPointType    string `json:"entry_point_type"`
		AccessPolicy      string `json:"access_policy"`
		AllowedPrincipals string `json:"allowed_principals"`
		Enabled           bool   `json:"enabled"`
	}
	var out []E
	for rows.Next() {
		var e E
		if rows.Scan(&e.ID, &e.Slug, &e.EntryPointType, &e.AccessPolicy, &e.AllowedPrincipals, &e.Enabled) == nil {
			out = append(out, e)
		}
	}
	if out == nil {
		out = []E{}
	}
	writeJSON(w, 200, out)
}
