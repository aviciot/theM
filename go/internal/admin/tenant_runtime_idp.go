package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/tenantctx"
)

// runtimeIDPRequest is the JSON body for PUT /tenant/runtime-idp.
type runtimeIDPRequest struct {
	JWKSUri  string `json:"jwks_uri"`
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"` // optional
	SubClaim string `json:"sub_claim"` // optional; default "sub"
}

// runtimeIDPResponse is the JSON response for GET and PUT /tenant/runtime-idp.
type runtimeIDPResponse struct {
	Configured bool   `json:"configured"`
	JWKSUri    string `json:"jwks_uri,omitempty"`
	Issuer     string `json:"issuer,omitempty"`
	Audience   string `json:"audience,omitempty"`
	SubClaim   string `json:"sub_claim,omitempty"`
}

// withRIDPTx opens a tenant-scoped transaction with the app.tenant_id GUC set,
// required because tenant_runtime_config has FORCE ROW LEVEL SECURITY targeting them_app.
// Falls back to the handler's plain DBQuerier when pools is nil (tests, legacy callers).
func (h *TenantSelfServiceHandler) withRIDPTx(ctx context.Context, tenantID string, fn func(*dal.DB) error) error {
	if h.pools == nil {
		return fn(h.db)
	}
	tid, err := uuid.Parse(tenantID)
	if err != nil {
		return err
	}
	tx, err := h.pools.BeginTenantTx(ctx, tid)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	if err := fn(dal.NewDBFromTenantQuerier(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GetRuntimeIDP handles GET /api/v1/tenant/runtime-idp.
// Returns the current runtime IDP config for the caller's tenant,
// or {"configured":false} when no config exists.
func (h *TenantSelfServiceHandler) GetRuntimeIDP(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	var resp runtimeIDPResponse
	err := h.withRIDPTx(r.Context(), tenantID, func(db *dal.DB) error {
		row, err := db.GetTenantRuntimeIDP(r.Context(), tenantID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				resp = runtimeIDPResponse{Configured: false}
				return nil
			}
			return err
		}
		resp = runtimeIDPResponse{
			Configured: true,
			JWKSUri:    row.JWKSUri,
			Issuer:     row.Issuer,
			Audience:   row.Audience,
			SubClaim:   row.SubClaim,
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// PutRuntimeIDP handles PUT /api/v1/tenant/runtime-idp.
// Creates or replaces the runtime IDP config for the caller's tenant.
// jwks_uri must be HTTPS and issuer must be non-empty.
func (h *TenantSelfServiceHandler) PutRuntimeIDP(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	var in runtimeIDPRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// Require HTTPS in production. Allow plain HTTP only for local dev endpoints
	// (localhost and Docker-internal hostnames starting with http://them-).
	if !strings.HasPrefix(in.JWKSUri, "https://") &&
		!strings.HasPrefix(in.JWKSUri, "http://localhost") &&
		!strings.HasPrefix(in.JWKSUri, "http://them-") {
		writeError(w, http.StatusBadRequest, "jwks_uri must be an HTTPS URL")
		return
	}
	if strings.TrimSpace(in.Issuer) == "" {
		writeError(w, http.StatusBadRequest, "issuer is required")
		return
	}
	subClaim := in.SubClaim
	if subClaim == "" {
		subClaim = "sub"
	}
	var resp runtimeIDPResponse
	err := h.withRIDPTx(r.Context(), tenantID, func(db *dal.DB) error {
		row, err := db.UpsertTenantRuntimeIDP(r.Context(), tenantID, dal.TenantRuntimeIDP{
			JWKSUri:  in.JWKSUri,
			Issuer:   in.Issuer,
			Audience: in.Audience,
			SubClaim: subClaim,
		})
		if err != nil {
			return err
		}
		resp = runtimeIDPResponse{
			Configured: true,
			JWKSUri:    row.JWKSUri,
			Issuer:     row.Issuer,
			Audience:   row.Audience,
			SubClaim:   row.SubClaim,
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// DeleteRuntimeIDP handles DELETE /api/v1/tenant/runtime-idp.
// Removes the runtime IDP config for the caller's tenant.
// No-op if no config exists.
func (h *TenantSelfServiceHandler) DeleteRuntimeIDP(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	err := h.withRIDPTx(r.Context(), tenantID, func(db *dal.DB) error {
		return db.DeleteTenantRuntimeIDP(r.Context(), tenantID)
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
