package artifacts_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/artifacts"
	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/runrecorder"
)

// ── Fakes ─────────────────────────────────────────────────────────────────────

// fakeAuth implements artifacts.Authenticator.
type fakeAuth struct {
	valid bool
}

func (f *fakeAuth) Validate(_ context.Context, _ string) (*auth.TokenInfo, error) {
	if !f.valid {
		return nil, errors.New("invalid token")
	}
	return &auth.TokenInfo{TokenID: 1}, nil
}

// fakeStore implements artifacts.ArtifactGetter.
type fakeStore struct {
	artifacts   map[string]runrecorder.ArtifactMeta // key: runID+":"+artifactID
	scanStatus  map[string]string                   // key: artifactID → scan_status
	err         error
}

func (f *fakeStore) GetArtifact(_ context.Context, runID, artifactID string) (runrecorder.ArtifactMeta, error) {
	if f.err != nil {
		return runrecorder.ArtifactMeta{}, f.err
	}
	key := runID + ":" + artifactID
	if a, ok := f.artifacts[key]; ok {
		return a, nil
	}
	// Simulate "no rows" for unknown combos.
	return runrecorder.ArtifactMeta{}, errors.New("no rows in result set")
}

func (f *fakeStore) GetArtifactScanStatus(_ context.Context, artifactID string) (string, error) {
	if f.scanStatus != nil {
		if s, ok := f.scanStatus[artifactID]; ok {
			return s, nil
		}
	}
	return "disabled", nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func newHandler(authenticator artifacts.Authenticator, store artifacts.ArtifactGetter) http.Handler {
	return artifacts.New(authenticator, store, nil).Routes()
}

func bearerRequest(method, url, token string) *http.Request {
	r := httptest.NewRequest(method, url, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestArtifactDownload_Success verifies that an authenticated request for a
// valid run+artifact combination returns 200 with correct headers and body.
func TestArtifactDownload_Success(t *testing.T) {
	store := &fakeStore{
		artifacts: map[string]runrecorder.ArtifactMeta{
			"run-1:art-1": {
				ID:          "art-1",
				RunID:       "run-1",
				Filename:    "report.pdf",
				ContentType: "application/pdf",
				Size:        7,
				Data:        []byte("PDF data"),
			},
		},
	}
	h := newHandler(&fakeAuth{valid: true}, store)

	w := httptest.NewRecorder()
	r := bearerRequest(http.MethodGet, "/runs/run-1/artifacts/art-1", "valid-token")
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/pdf", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
	assert.Contains(t, w.Header().Get("Content-Disposition"), "report.pdf")
	assert.Equal(t, "8", w.Header().Get("Content-Length")) // len("PDF data") == 8
	assert.Equal(t, "PDF data", w.Body.String())
}

// TestArtifactDownload_Unauthorized verifies that a missing bearer token
// returns 401.
func TestArtifactDownload_Unauthorized(t *testing.T) {
	store := &fakeStore{}
	h := newHandler(&fakeAuth{valid: true}, store)

	w := httptest.NewRecorder()
	r := bearerRequest(http.MethodGet, "/runs/run-1/artifacts/art-1", "") // no token
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestArtifactDownload_InvalidToken verifies that an invalid bearer token
// returns 401.
func TestArtifactDownload_InvalidToken(t *testing.T) {
	store := &fakeStore{}
	h := newHandler(&fakeAuth{valid: false}, store)

	w := httptest.NewRecorder()
	r := bearerRequest(http.MethodGet, "/runs/run-1/artifacts/art-1", "bad-token")
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestArtifactDownload_WrongRun verifies that a valid token + valid artifact_id
// but mismatched run_id returns 404 (cross-run denied by DB query).
func TestArtifactDownload_WrongRun(t *testing.T) {
	store := &fakeStore{
		artifacts: map[string]runrecorder.ArtifactMeta{
			"run-A:art-1": {
				ID:          "art-1",
				RunID:       "run-A",
				Filename:    "file.txt",
				ContentType: "text/plain",
				Size:        5,
				Data:        []byte("hello"),
			},
		},
	}
	h := newHandler(&fakeAuth{valid: true}, store)

	// Request art-1 but via run-B's URL — must 404.
	w := httptest.NewRecorder()
	r := bearerRequest(http.MethodGet, "/runs/run-B/artifacts/art-1", "valid-token")
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestArtifactDownload_WrongArtifact verifies that a valid token + valid run_id
// but non-existent artifact_id returns 404.
func TestArtifactDownload_WrongArtifact(t *testing.T) {
	store := &fakeStore{} // empty store — no artifacts
	h := newHandler(&fakeAuth{valid: true}, store)

	w := httptest.NewRecorder()
	r := bearerRequest(http.MethodGet, "/runs/run-1/artifacts/does-not-exist", "valid-token")
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestArtifactDownload_CrossRun verifies that artifact_id from run A requested
// via run B's URL returns 404 (not the artifact from run A).
func TestArtifactDownload_CrossRun(t *testing.T) {
	store := &fakeStore{
		artifacts: map[string]runrecorder.ArtifactMeta{
			"run-A:art-shared": {
				ID:          "art-shared",
				RunID:       "run-A",
				Filename:    "secret.pdf",
				ContentType: "application/pdf",
				Size:        6,
				Data:        []byte("secret"),
			},
		},
	}
	h := newHandler(&fakeAuth{valid: true}, store)

	// Request the same artifact ID via run-B's URL.
	w := httptest.NewRecorder()
	r := bearerRequest(http.MethodGet, "/runs/run-B/artifacts/art-shared", "valid-token")
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusNotFound, w.Code,
		"cross-run access via wrong run_id must return 404")
	// Ensure secret data is not in the response body.
	assert.NotContains(t, w.Body.String(), "secret")
}

// TestArtifactDownload_SafeContentDisposition verifies that the response has a
// Content-Disposition attachment header.
func TestArtifactDownload_SafeContentDisposition(t *testing.T) {
	store := &fakeStore{
		artifacts: map[string]runrecorder.ArtifactMeta{
			"run-1:art-2": {
				ID:          "art-2",
				RunID:       "run-1",
				Filename:    "data.csv",
				ContentType: "text/csv",
				Size:        3,
				Data:        []byte("a,b"),
			},
		},
	}
	h := newHandler(&fakeAuth{valid: true}, store)

	w := httptest.NewRecorder()
	r := bearerRequest(http.MethodGet, "/runs/run-1/artifacts/art-2", "valid-token")
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	cd := w.Header().Get("Content-Disposition")
	assert.True(t, strings.HasPrefix(cd, "attachment"),
		"Content-Disposition must start with 'attachment', got: %q", cd)
}

// TestArtifactDownload_CorrectHeaders verifies that Content-Type and
// Content-Length are set correctly from artifact metadata.
func TestArtifactDownload_CorrectHeaders(t *testing.T) {
	store := &fakeStore{
		artifacts: map[string]runrecorder.ArtifactMeta{
			"run-1:art-3": {
				ID:          "art-3",
				RunID:       "run-1",
				Filename:    "image.png",
				ContentType: "image/png",
				Size:        4,
				Data:        []byte("\x89PNG"),
			},
		},
	}
	h := newHandler(&fakeAuth{valid: true}, store)

	w := httptest.NewRecorder()
	r := bearerRequest(http.MethodGet, "/runs/run-1/artifacts/art-3", "valid-token")
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
	assert.Equal(t, "4", w.Header().Get("Content-Length"))
}

// TestArtifactDownload_ResponseBodyEqualsArtifactData verifies that the
// response body exactly matches the stored artifact data.
func TestArtifactDownload_ResponseBodyEqualsArtifactData(t *testing.T) {
	expectedData := []byte("exact binary content \x00\x01\x02")
	store := &fakeStore{
		artifacts: map[string]runrecorder.ArtifactMeta{
			"run-1:art-4": {
				ID:          "art-4",
				RunID:       "run-1",
				Filename:    "binary.bin",
				ContentType: "application/octet-stream",
				Size:        int64(len(expectedData)),
				Data:        expectedData,
			},
		},
	}
	h := newHandler(&fakeAuth{valid: true}, store)

	w := httptest.NewRecorder()
	r := bearerRequest(http.MethodGet, "/runs/run-1/artifacts/art-4", "valid-token")
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, expectedData, w.Body.Bytes())
}

// TestArtifactDownload_ScanPending verifies that a pending artifact returns 202.
func TestArtifactDownload_ScanPending(t *testing.T) {
	store := &fakeStore{
		scanStatus: map[string]string{"art-5": "pending"},
		artifacts:  map[string]runrecorder.ArtifactMeta{},
	}
	h := newHandler(&fakeAuth{valid: true}, store)

	w := httptest.NewRecorder()
	r := bearerRequest(http.MethodGet, "/runs/run-1/artifacts/art-5", "valid-token")
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.Contains(t, w.Body.String(), "pending")
}

// TestArtifactDownload_ScanScanning verifies that a scanning artifact returns 202.
func TestArtifactDownload_ScanScanning(t *testing.T) {
	store := &fakeStore{
		scanStatus: map[string]string{"art-6": "scanning"},
	}
	h := newHandler(&fakeAuth{valid: true}, store)

	w := httptest.NewRecorder()
	r := bearerRequest(http.MethodGet, "/runs/run-1/artifacts/art-6", "valid-token")
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusAccepted, w.Code)
}

// TestArtifactDownload_ScanInfected verifies that an infected artifact returns 451.
func TestArtifactDownload_ScanInfected(t *testing.T) {
	store := &fakeStore{
		scanStatus: map[string]string{"art-7": "infected"},
	}
	h := newHandler(&fakeAuth{valid: true}, store)

	w := httptest.NewRecorder()
	r := bearerRequest(http.MethodGet, "/runs/run-1/artifacts/art-7", "valid-token")
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnavailableForLegalReasons, w.Code)
	assert.Contains(t, w.Body.String(), "blocked")
}

// TestArtifactDownload_ScanClean verifies that a clean artifact is served normally.
func TestArtifactDownload_ScanClean(t *testing.T) {
	store := &fakeStore{
		scanStatus: map[string]string{"art-8": "clean"},
		artifacts: map[string]runrecorder.ArtifactMeta{
			"run-1:art-8": {
				ID:          "art-8",
				RunID:       "run-1",
				Filename:    "clean.txt",
				ContentType: "text/plain",
				Size:        5,
				Data:        []byte("hello"),
			},
		},
	}
	h := newHandler(&fakeAuth{valid: true}, store)

	w := httptest.NewRecorder()
	r := bearerRequest(http.MethodGet, "/runs/run-1/artifacts/art-8", "valid-token")
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "hello", w.Body.String())
}

// ── MinIO / quarantine-first path tests ───────────────────────────────────────

// fakeFetcher implements artifacts.ByteFetcher.
type fakeFetcher struct {
	data map[string][]byte
	err  error
}

func (f *fakeFetcher) GetArtifact(_ context.Context, key string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	if d, ok := f.data[key]; ok {
		return d, nil
	}
	return nil, errors.New("not found")
}

func newHandlerWithFetcher(store artifacts.ArtifactGetter, fetcher artifacts.ByteFetcher) http.Handler {
	return artifacts.NewWithFetcher(&fakeAuth{valid: true}, store, fetcher, nil).Routes()
}

// TestArtifactDownload_MinIO verifies that when storage_key is set the handler
// fetches bytes from the MinIO fetcher instead of Postgres BYTEA.
func TestArtifactDownload_MinIO(t *testing.T) {
	store := &fakeStore{
		artifacts: map[string]runrecorder.ArtifactMeta{
			"run-minio:art-minio": {
				ID:          "art-minio",
				RunID:       "run-minio",
				Filename:    "report.pdf",
				ContentType: "application/pdf",
				Size:        7,
				Data:        nil,                         // bytes not in Postgres
				StorageKey:  "artifacts/art-minio",      // bytes in MinIO
			},
		},
	}
	fetcher := &fakeFetcher{data: map[string][]byte{
		"artifacts/art-minio": []byte("pdf-data"),
	}}
	h := newHandlerWithFetcher(store, fetcher)

	w := httptest.NewRecorder()
	r := bearerRequest(http.MethodGet, "/runs/run-minio/artifacts/art-minio", "tok")
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "pdf-data", w.Body.String())
	assert.Contains(t, w.Header().Get("Content-Disposition"), "report.pdf")
}

// TestArtifactDownload_InfectedGone verifies that an artifact with data=nil and
// no storage_key (infected, bytes scrubbed) returns 410 Gone.
func TestArtifactDownload_InfectedGone(t *testing.T) {
	store := &fakeStore{
		artifacts: map[string]runrecorder.ArtifactMeta{
			"run-inf:art-inf": {
				ID:          "art-inf",
				RunID:       "run-inf",
				Filename:    "virus.exe",
				ContentType: "application/octet-stream",
				Size:        100,
				Data:        nil,  // scrubbed
				StorageKey:  "",   // no storage key → infected
			},
		},
	}
	fetcher := &fakeFetcher{}
	h := newHandlerWithFetcher(store, fetcher)

	w := httptest.NewRecorder()
	r := bearerRequest(http.MethodGet, "/runs/run-inf/artifacts/art-inf", "tok")
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusGone, w.Code)
}

// TestArtifactDownload_MinIOFetchError verifies that a MinIO read failure returns 500.
func TestArtifactDownload_MinIOFetchError(t *testing.T) {
	store := &fakeStore{
		artifacts: map[string]runrecorder.ArtifactMeta{
			"run-err:art-err": {
				ID:         "art-err",
				RunID:      "run-err",
				StorageKey: "artifacts/art-err",
			},
		},
	}
	fetcher := &fakeFetcher{err: errors.New("minio unavailable")}
	h := newHandlerWithFetcher(store, fetcher)

	w := httptest.NewRecorder()
	r := bearerRequest(http.MethodGet, "/runs/run-err/artifacts/art-err", "tok")
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
