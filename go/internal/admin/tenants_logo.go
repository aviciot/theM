package admin

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
)

const maxLogoBytes = 200 << 10 // 200 KB

var allowedLogoTypes = map[string]string{
	"image/png":      ".png",
	"image/jpeg":     ".jpg",
	"image/webp":     ".webp",
	"image/svg+xml":  ".svg",
}

// UploadLogo handles POST /admin/tenants/{id}/logo.
// Accepts multipart field "logo", validates content type and size, stores the
// file under {logoDir}/{slug}/logo/logo.{ext}, and persists the URL in the DB.
func (h *TenantsHandler) UploadLogo(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing tenant id")
		return
	}
	if err := r.ParseMultipartForm(maxLogoBytes); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "logo exceeds 200 KB limit")
		return
	}
	file, _, err := r.FormFile("logo")
	if err != nil {
		writeError(w, http.StatusBadRequest, "logo field missing")
		return
	}
	defer file.Close()

	// Read up to maxLogoBytes+1 to detect oversized files.
	data, err := io.ReadAll(io.LimitReader(file, int64(maxLogoBytes)+1))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read upload")
		return
	}
	if len(data) > maxLogoBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "logo exceeds 200 KB limit")
		return
	}

	// Detect content type from first 512 bytes.
	ct := http.DetectContentType(bytes.NewBuffer(data).Bytes()[:min512(len(data))])
	ext, ok := allowedLogoTypes[ct]
	if !ok {
		// SVG is text/xml or text/plain when detected by sniffing; also allow explicit check.
		if isSVG(data) {
			ext = ".svg"
		} else {
			writeError(w, http.StatusUnsupportedMediaType, "logo must be PNG, JPEG, WebP, or SVG")
			return
		}
	}

	tenant, err := h.db.GetTenant(r.Context(), id)
	if dal.IsNoRows(err) {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	logoDir := filepath.Join(h.logoDir, tenant.Slug, "logo")
	if err := os.MkdirAll(logoDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}

	// Remove any existing logo files before writing the new one.
	existing, _ := filepath.Glob(filepath.Join(logoDir, "logo.*"))
	for _, f := range existing {
		_ = os.Remove(f)
	}

	dest := filepath.Join(logoDir, "logo"+ext)
	if err := os.WriteFile(dest, data, 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}

	logoURL := "/static/tenants/" + tenant.Slug + "/logo/logo" + ext
	if err := h.db.SetTenantLogoURL(r.Context(), id, logoURL); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"logo_url": logoURL})
}

// DeleteLogo handles DELETE /admin/tenants/{id}/logo.
func (h *TenantsHandler) DeleteLogo(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing tenant id")
		return
	}
	tenant, err := h.db.GetTenant(r.Context(), id)
	if dal.IsNoRows(err) {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	_ = os.RemoveAll(filepath.Join(h.logoDir, tenant.Slug, "logo"))
	if err := h.db.ClearTenantLogoURL(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func min512(n int) int {
	if n < 512 {
		return n
	}
	return 512
}

// isSVG does a minimal check for SVG content that http.DetectContentType misses.
func isSVG(data []byte) bool {
	s := bytes.TrimSpace(data)
	return bytes.Contains(s[:min512(len(s))], []byte("<svg"))
}
