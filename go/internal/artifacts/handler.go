// Package artifacts provides the HTTP handler for downloading run file artifacts.
//
// Security model:
//   - Bearer token authentication (same as WS/SSE — NOT RequireSuperAdmin JWT)
//   - run_id + artifact_id pair verified in a single DB query (cross-run denied)
//   - Bytes served from MinIO (storage_key set) or Postgres BYTEA (legacy), never filesystem
//   - No os.Open, filepath.Join, or os.ReadFile in this path
//   - Client disconnect cancels the DB/MinIO fetch via r.Context()
//   - Binary data never appears in log output
//   - Content-Disposition uses RFC 5987 encoding for non-ASCII filenames
package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/runrecorder"
)

// ArtifactGetter retrieves a file artifact from storage.
type ArtifactGetter interface {
	GetArtifact(ctx context.Context, runID, artifactID string) (runrecorder.ArtifactMeta, error)
	GetArtifactScanStatus(ctx context.Context, artifactID string) (string, error)
}

// ByteFetcher retrieves raw bytes from the object store by storage key.
// Implemented by *storage.Client in production; nil disables MinIO serving
// (falls back to Postgres BYTEA for legacy artifacts).
type ByteFetcher interface {
	GetArtifact(ctx context.Context, key string) ([]byte, error)
}

// Authenticator validates a bearer token and returns its metadata.
type Authenticator interface {
	Validate(ctx context.Context, raw string) (*auth.TokenInfo, error)
}

// Handler serves artifact download requests.
// Route: GET /api/v1/runs/{run_id}/artifacts/{artifact_id}
type Handler struct {
	auth    Authenticator
	store   ArtifactGetter
	fetcher ByteFetcher // may be nil (legacy/no MinIO)
	logger  *slog.Logger
}

// New creates a Handler without a MinIO fetcher (legacy mode — bytes from Postgres).
func New(authenticator Authenticator, store ArtifactGetter, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{auth: authenticator, store: store, logger: logger}
}

// NewWithFetcher creates a Handler with a MinIO byte fetcher for the
// quarantine-first path. When artifact.StorageKey is set, bytes are fetched
// from MinIO instead of Postgres BYTEA.
func NewWithFetcher(authenticator Authenticator, store ArtifactGetter, fetcher ByteFetcher, logger *slog.Logger) *Handler {
	h := New(authenticator, store, logger)
	h.fetcher = fetcher
	return h
}

// Routes registers the artifact download endpoint on a chi sub-router.
// Tests call this directly so paths are relative to the sub-router root:
//
//	GET /runs/{run_id}/artifacts/{artifact_id}
//
// In production use Handler() instead — it returns a flat http.Handler
// suitable for direct route registration at a full path.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(h.requireBearer)
	r.Get("/runs/{run_id}/artifacts/{artifact_id}", h.Download)
	return r
}

// Handler returns an http.Handler that applies bearer token authentication
// and then calls Download. Unlike Routes(), it has no internal chi routing
// so it is safe to register at a fully-qualified path on the root router via
// server.MountArtifacts.
func (h *Handler) Handler() http.Handler {
	return h.requireBearer(http.HandlerFunc(h.Download))
}

// requireBearer is inline bearer token middleware so this handler does not
// depend on the admin JWT middleware and can be mounted independently.
func (h *Handler) requireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := extractBearer(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		_, err := h.auth.Validate(r.Context(), raw)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or revoked bearer token"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Download handles GET /runs/{run_id}/artifacts/{artifact_id}.
//
// Authorization: authenticated caller + run_id exists + artifact belongs to that run.
// Cross-run and cross-tenant access is denied by the DB query (WHERE id=$1 AND run_id=$2).
//
// Scan gate:
//   - 'pending' or 'scanning' → 202 Accepted (retry later)
//   - 'infected' → 451 Unavailable For Legal Reasons
//   - 'disabled', 'clean', 'error', 'failed' → serve normally
//
// SECURITY:
//   - Artifact data is fetched from the DB, not the filesystem
//   - Content-Disposition filename is sanitized at response time
//   - Client disconnect (r.Context()) cancels the DB fetch
//   - Binary data never appears in logs
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "run_id")
	artifactID := chi.URLParam(r, "artifact_id")

	if runID == "" || artifactID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run_id and artifact_id are required"})
		return
	}

	// Check scan gate before loading the full artifact bytes.
	scanStatus, err := h.store.GetArtifactScanStatus(r.Context(), artifactID)
	if err == nil {
		switch scanStatus {
		case "pending", "scanning":
			writeJSON(w, http.StatusAccepted, map[string]string{
				"status":      scanStatus,
				"artifact_id": artifactID,
				"message":     "artifact is being scanned, retry shortly",
			})
			return
		case "infected":
			writeJSON(w, http.StatusUnavailableForLegalReasons, map[string]string{
				"error":       "artifact blocked: malicious content detected",
				"artifact_id": artifactID,
			})
			return
		}
	}

	// Fetch artifact metadata — use r.Context() so client disconnect cancels the DB call.
	artifact, err := h.store.GetArtifact(r.Context(), runID, artifactID)
	if err != nil {
		if isNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "artifact not found"})
			return
		}
		h.logger.Warn("artifacts: get artifact failed",
			"run_id", runID, "artifact_id", artifactID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Resolve bytes: MinIO (quarantine-first) → Postgres BYTEA (legacy) → 410 (infected/scrubbed).
	// SECURITY: bytes must never appear in log output.
	var data []byte
	switch {
	case artifact.StorageKey != "" && h.fetcher != nil:
		// Clean quarantine-first artifact: bytes in MinIO artifacts bucket.
		data, err = h.fetcher.GetArtifact(r.Context(), artifact.StorageKey)
		if err != nil {
			h.logger.Warn("artifacts: MinIO fetch failed",
				"artifact_id", artifactID, "storage_key", artifact.StorageKey, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
	case artifact.StorageKey == "" && artifact.Data == nil:
		// Infected artifact: bytes were scrubbed, no storage key.
		writeJSON(w, http.StatusGone, map[string]string{
			"error":       "artifact unavailable: content was removed",
			"artifact_id": artifactID,
		})
		return
	default:
		// Legacy path: bytes stored in Postgres BYTEA.
		data = artifact.Data
	}

	// Validate and sanitize content type.
	ct := safeContentType(artifact.ContentType)

	// Sanitize filename at response time (defense in depth).
	filename := safeResponseFilename(artifact.Filename)

	// Set response headers.
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", contentDisposition(filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)

	// SECURITY: data is raw bytes — never log it.
	_, _ = io.Copy(w, bytes.NewReader(data))
}

// ── helpers ───────────────────────────────────────────────────────────────────

// extractBearer parses the Authorization: Bearer <token> header.
func extractBearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	tok := strings.TrimSpace(h[len(prefix):])
	if tok == "" {
		return "", false
	}
	return tok, true
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// safeContentType returns the content type if it is a known safe MIME type,
// or "application/octet-stream" otherwise. This prevents stored XSS via
// crafted content types.
func safeContentType(ct string) string {
	if ct == "" {
		return "application/octet-stream"
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return "application/octet-stream"
	}
	// Allow-list of safe MIME type prefixes.
	// All served as attachments so no inline rendering risk.
	switch {
	case strings.HasPrefix(mediaType, "application/"),
		strings.HasPrefix(mediaType, "image/"),
		strings.HasPrefix(mediaType, "audio/"),
		strings.HasPrefix(mediaType, "video/"),
		strings.HasPrefix(mediaType, "text/"),
		strings.HasPrefix(mediaType, "font/"):
		return mediaType
	}
	return "application/octet-stream"
}

// safeResponseFilename strips path traversal and control characters from a
// filename before writing it to the Content-Disposition header.
// This is a second sanitization pass (recorder.go sanitizes at write time).
func safeResponseFilename(name string) string {
	name = strings.ReplaceAll(name, "/", "")
	name = strings.ReplaceAll(name, "\\", "")
	name = strings.ReplaceAll(name, "..", "")
	var sb strings.Builder
	for _, ru := range name {
		if ru >= 32 && ru != 127 {
			sb.WriteRune(ru)
		}
	}
	result := strings.TrimSpace(sb.String())
	if result == "" {
		return "artifact"
	}
	return result
}

// contentDisposition builds a Content-Disposition header value.
// For ASCII filenames: attachment; filename="<name>"
// For non-ASCII filenames: RFC 5987 encoding is used.
func contentDisposition(filename string) string {
	if utf8.ValidString(filename) && isASCII(filename) {
		escaped := strings.ReplaceAll(filename, `"`, `\"`)
		return `attachment; filename="` + escaped + `"`
	}
	// RFC 5987: attachment; filename*=UTF-8''<percent-encoded>
	encoded := rfc5987Encode(filename)
	return `attachment; filename*=UTF-8''` + encoded
}

// rfc5987Encode percent-encodes a UTF-8 string per RFC 5987.
func rfc5987Encode(s string) string {
	var sb strings.Builder
	for _, b := range []byte(s) {
		// Unreserved characters per RFC 5987 §3.2.1
		if isAttrChar(b) {
			sb.WriteByte(b)
		} else {
			fmt.Fprintf(&sb, "%%%02X", b)
		}
	}
	return sb.String()
}

// isAttrChar returns true for characters that don't need percent-encoding in
// RFC 5987 extended values (attr-char = ALPHA / DIGIT / "!" / "#" / "$" /
// "%" / "&" / "+" / "-" / "." / "^" / "_" / "`" / "|" / "~").
func isAttrChar(b byte) bool {
	return (b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') ||
		b == '!' || b == '#' || b == '$' || b == '&' || b == '+' ||
		b == '-' || b == '.' || b == '^' || b == '_' || b == '`' ||
		b == '|' || b == '~'
}

// isASCII returns true if s contains only ASCII printable characters (< 128).
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 128 {
			return false
		}
	}
	return true
}

// isNotFound returns true if the error indicates a missing artifact row.
// Recognizes pgx "no rows" text and common wrapped error text.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no rows") ||
		strings.Contains(msg, "not found") ||
		errors.Is(err, errNotFound)
}

// errNotFound is a sentinel for tests that want to simulate a missing artifact.
var errNotFound = errors.New("artifact: not found")
