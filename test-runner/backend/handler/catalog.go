package handler

import (
	"net/http"

	"github.com/aviciot/them-test-runner/config"
	"github.com/aviciot/them-test-runner/them"
	"github.com/go-chi/chi/v5"
)

func newClient() (*them.Client, error) {
	cfg := config.Get()
	return them.NewClient(cfg.ThemURL, cfg.AdminUser, cfg.AdminPass)
}

func ListTenants(w http.ResponseWriter, r *http.Request) {
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
	client, err := newClient()
	if err != nil {
		writeError(w, 502, "cannot connect to the-M: "+err.Error())
		return
	}
	apps, err := client.ListApps(tenantSlug)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, apps)
}

func ListEPs(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	client, err := newClient()
	if err != nil {
		writeError(w, 502, "cannot connect to the-M: "+err.Error())
		return
	}
	eps, err := client.ListEPs(appID)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, eps)
}
