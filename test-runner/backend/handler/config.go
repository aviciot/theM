package handler

import (
	"encoding/json"
	"net/http"

	"github.com/aviciot/them-test-runner/config"
	"github.com/aviciot/them-test-runner/them"
)

func GetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := config.Get()
	writeJSON(w, 200, map[string]any{
		"them_url":      cfg.ThemURL,
		"admin_user":    cfg.AdminUser,
		"admin_pass_set": cfg.AdminPass != "",
	})
}

func PutConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ThemURL   string `json:"them_url"`
		AdminUser string `json:"admin_user"`
		AdminPass string `json:"admin_pass"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if err := config.Save(body.ThemURL, body.AdminUser, body.AdminPass); err != nil {
		writeError(w, 500, "failed to save config")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func TestConfig(w http.ResponseWriter, r *http.Request) {
	cfg := config.Get()
	client, err := them.NewClient(cfg.ThemURL, cfg.AdminUser, cfg.AdminPass)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error(), "tenants_found": 0})
		return
	}
	tenants, err := client.ListTenants()
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error(), "tenants_found": 0})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "tenants_found": len(tenants)})
}
