package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

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

// GetRuntimeIDP handles GET /api/v1/tenant/runtime-idp.
// Returns the current runtime IDP config for the caller's tenant,
// or {"configured":false} when no config exists.
func (h *TenantSelfServiceHandler) GetRuntimeIDP(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	row, err := h.db.GetTenantRuntimeIDP(r.Context(), tenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, runtimeIDPResponse{Configured: false})
			return
		}
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, runtimeIDPResponse{
		Configured: true,
		JWKSUri:    row.JWKSUri,
		Issuer:     row.Issuer,
		Audience:   row.Audience,
		SubClaim:   row.SubClaim,
	})
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
	if !strings.HasPrefix(in.JWKSUri, "https://") {
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
	row, err := h.db.UpsertTenantRuntimeIDP(r.Context(), tenantID, dal.TenantRuntimeIDP{
		JWKSUri:  in.JWKSUri,
		Issuer:   in.Issuer,
		Audience: in.Audience,
		SubClaim: subClaim,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, runtimeIDPResponse{
		Configured: true,
		JWKSUri:    row.JWKSUri,
		Issuer:     row.Issuer,
		Audience:   row.Audience,
		SubClaim:   row.SubClaim,
	})
}

// DeleteRuntimeIDP handles DELETE /api/v1/tenant/runtime-idp.
// Removes the runtime IDP config for the caller's tenant.
// No-op if no config exists.
func (h *TenantSelfServiceHandler) DeleteRuntimeIDP(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.db.DeleteTenantRuntimeIDP(r.Context(), tenantID); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
