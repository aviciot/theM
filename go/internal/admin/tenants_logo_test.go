package admin_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
)

// ── Logo upload tests ─────────────────────────────────────────────────────────

func newLogoRouter(t *testing.T, db admin.DBQuerier) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	r.Use(withTestTenant)
	admin.NewTenantsHandler(db, nil, nil, t.TempDir()).Routes(r)
	return r
}

// logoMultipart builds a multipart request with the given file bytes and content type.
func logoMultipart(t *testing.T, data []byte, filename string) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, err := w.CreateFormFile("logo", filename)
	require.NoError(t, err)
	_, err = fw.Write(data)
	require.NoError(t, err)
	w.Close()
	req := httptest.NewRequest(http.MethodPost, "/tenants/00000000-0000-0000-0000-000000000001/logo", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

// TL-01: non-image file is rejected with 415
func TestTenantLogo_RejectNonImage(t *testing.T) {
	db := &tenantDB{
		getRow: &tenantFakeRow{id: "00000000-0000-0000-0000-000000000001", slug: "corp", displayName: "Corp", enabled: true},
	}
	r := newLogoRouter(t, db)

	// Send a plain-text file — http.DetectContentType will detect text/plain
	req := logoMultipart(t, []byte("hello world, not an image"), "logo.txt")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
}

// TL-02: file over 200 KB is rejected with 413
func TestTenantLogo_RejectOversized(t *testing.T) {
	db := &tenantDB{
		getRow: &tenantFakeRow{id: "00000000-0000-0000-0000-000000000001", slug: "corp", displayName: "Corp", enabled: true},
	}
	r := newLogoRouter(t, db)

	oversized := bytes.Repeat([]byte("x"), 201*1024)
	req := logoMultipart(t, oversized, "big.png")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

// TL-03: unknown tenant returns 404
func TestTenantLogo_NotFound(t *testing.T) {
	db := &tenantDB{
		getRow: &tenantFakeRow{err: pgx.ErrNoRows},
	}
	r := newLogoRouter(t, db)

	// Minimal valid PNG header (8 bytes magic + IHDR)
	pngHeader := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x00\x00\x00\x00:~\x9bU")
	req := logoMultipart(t, pngHeader, "logo.png")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TL-04: SVG accepted (http.DetectContentType returns text/plain for SVG, isSVG fallback handles it)
func TestTenantLogo_SVGAccepted(t *testing.T) {
	db := &tenantDB{
		getRow: &tenantFakeRow{id: "00000000-0000-0000-0000-000000000001", slug: "corp", displayName: "Corp", enabled: true},
		// execErr is nil (default) — Exec succeeds for SetTenantLogoURL
	}
	r := newLogoRouter(t, db)

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><circle cx="50" cy="50" r="40"/></svg>`)
	req := logoMultipart(t, svg, "logo.svg")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}
