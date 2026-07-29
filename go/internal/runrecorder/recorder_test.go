package runrecorder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Mock DB ───────────────────────────────────────────────────────────────────

// execCall records one call to Exec.
type execCall struct {
	sql  string
	args []any
}

// queryRowCall records one call to QueryRow.
type queryRowCall struct {
	sql  string
	args []any
}

// mockRow is a fake SingleRowScanner returned by mockDB.QueryRow.
type mockRow struct {
	scanFn func(dest ...any) error
}

func (m *mockRow) Scan(dest ...any) error {
	if m.scanFn != nil {
		return m.scanFn(dest...)
	}
	return nil
}

// mockDB implements DBQuerier for tests. It records all Exec and QueryRow calls
// and can be configured to return specific errors or scan results.
type mockDB struct {
	calls        []execCall
	queryRows    []queryRowCall
	errOnce      error         // if set, returned on the next Exec and cleared
	queryRowFn   func(sql string, args ...any) SingleRowScanner
}

func (m *mockDB) Exec(_ context.Context, sql string, args ...any) error {
	m.calls = append(m.calls, execCall{sql: sql, args: args})
	if m.errOnce != nil {
		err := m.errOnce
		m.errOnce = nil
		return err
	}
	return nil
}

func (m *mockDB) QueryRow(_ context.Context, sql string, args ...any) SingleRowScanner {
	m.queryRows = append(m.queryRows, queryRowCall{sql: sql, args: args})
	if m.queryRowFn != nil {
		return m.queryRowFn(sql, args...)
	}
	// Default: return a row that scans a fake UUID into the first dest.
	return &mockRow{
		scanFn: func(dest ...any) error {
			if len(dest) > 0 {
				if sp, ok := dest[0].(*string); ok {
					*sp = "00000000-0000-0000-0000-000000000001"
				}
			}
			return nil
		},
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestCreateRun_callsCorrectSQL verifies that CreateRun issues the expected
// INSERT with the correct argument ordering.
func TestCreateRun_callsCorrectSQL(t *testing.T) {
	db := &mockDB{}
	rec := New(db)

	now := time.Now().UTC().Truncate(time.Second)
	run := domain.Run{
		ID:             "run-abc",
		ContextID:      "ctx-1",
		ApplicationID:  42,
		EntryPointSlug: "ws-chat",
		Status:         domain.RunRunning,
		StartedAt:      now,
	}

	err := rec.CreateRun(context.Background(), run)
	require.NoError(t, err)

	require.Len(t, db.calls, 1)
	call := db.calls[0]

	// SQL must contain INSERT INTO them.runs and ON CONFLICT DO NOTHING.
	assert.Contains(t, call.sql, "INSERT INTO them.runs")
	assert.Contains(t, call.sql, "ON CONFLICT (id) DO NOTHING")
	assert.Contains(t, call.sql, "events_transport")

	// Arguments: id, context_id, application_id, entry_point_slug, status,
	// started_at, events_transport
	require.Len(t, call.args, 7)
	assert.Equal(t, "run-abc", call.args[0])
	assert.Equal(t, "ctx-1", call.args[1])
	assert.Equal(t, int64(42), call.args[2])
	assert.Equal(t, "ws-chat", call.args[3])
	assert.Equal(t, "running", call.args[4])
	assert.Equal(t, now, call.args[5])
	// Default mode (pubsub) → events_transport "pubsub".
	assert.Equal(t, "pubsub", call.args[6])
}

// TestCreateRun_eventsTransportByMode verifies that the events_transport column
// value is derived from the configured RunEventsMode (Phase 11c-B):
// pubsub → "pubsub"; dual → "streams"; streams → "streams".
func TestCreateRun_eventsTransportByMode(t *testing.T) {
	cases := []struct {
		mode config.RunEventsMode
		want string
	}{
		{config.RunEventsModePublish, "pubsub"},
		{config.RunEventsModeDual, "streams"},
		{config.RunEventsModeStreams, "streams"},
	}
	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			db := &mockDB{}
			rec := New(db).WithRunEventsMode(tc.mode)
			err := rec.CreateRun(context.Background(), domain.Run{ID: "r", StartedAt: time.Now()})
			require.NoError(t, err)
			require.Len(t, db.calls, 1)
			assert.Equal(t, tc.want, db.calls[0].args[6])
		})
	}
}

// TestCreateRun_explicitTransportOverridesMode verifies that a non-empty
// run.EventsTransport takes precedence over the configured mode.
func TestCreateRun_explicitTransportOverridesMode(t *testing.T) {
	db := &mockDB{}
	rec := New(db).WithRunEventsMode(config.RunEventsModePublish)
	err := rec.CreateRun(context.Background(), domain.Run{
		ID:              "r",
		StartedAt:       time.Now(),
		EventsTransport: "streams",
	})
	require.NoError(t, err)
	require.Len(t, db.calls, 1)
	assert.Equal(t, "streams", db.calls[0].args[6])
}

// TestUpdateRunStatus_withErrorMessage verifies that UpdateRunStatus sends the
// run ID, status, and non-empty error message.
func TestUpdateRunStatus_withErrorMessage(t *testing.T) {
	db := &mockDB{}
	rec := New(db)

	err := rec.UpdateRunStatus(context.Background(), "run-xyz", domain.RunFailed, "context deadline exceeded")
	require.NoError(t, err)

	require.Len(t, db.calls, 1)
	call := db.calls[0]

	assert.Contains(t, call.sql, "UPDATE them.runs")
	require.Len(t, call.args, 3)
	assert.Equal(t, "run-xyz", call.args[0])
	assert.Equal(t, "failed", call.args[1])
	assert.Equal(t, "context deadline exceeded", call.args[2])
}

// TestUpdateRunStatus_completed verifies that UpdateRunStatus works with an
// empty error message (normal completion).
func TestUpdateRunStatus_completed(t *testing.T) {
	db := &mockDB{}
	rec := New(db)

	err := rec.UpdateRunStatus(context.Background(), "run-ok", domain.RunCompleted, "")
	require.NoError(t, err)

	require.Len(t, db.calls, 1)
	call := db.calls[0]

	require.Len(t, call.args, 3)
	assert.Equal(t, "run-ok", call.args[0])
	assert.Equal(t, "completed", call.args[1])
	assert.Equal(t, "", call.args[2])
}

// TestRecordUsage_insertsCorrectly verifies the INSERT … ON CONFLICT UPDATE
// SQL and argument ordering for RecordUsage.
func TestRecordUsage_insertsCorrectly(t *testing.T) {
	db := &mockDB{}
	rec := New(db)

	err := rec.RecordUsage(context.Background(), "run-tok", 150, 320)
	require.NoError(t, err)

	require.Len(t, db.calls, 1)
	call := db.calls[0]

	assert.Contains(t, call.sql, "INSERT INTO them.run_usage")
	assert.Contains(t, call.sql, "ON CONFLICT (run_id) DO UPDATE")

	// args: run_id, input_tokens, output_tokens, recorded_at
	require.Len(t, call.args, 4)
	assert.Equal(t, "run-tok", call.args[0])
	assert.Equal(t, 150, call.args[1])
	assert.Equal(t, 320, call.args[2])
	_, ok := call.args[3].(time.Time)
	assert.True(t, ok, "4th arg should be a time.Time")
}

// TestRecordStep_insertsCorrectly verifies the INSERT into them.run_steps.
func TestRecordStep_insertsCorrectly(t *testing.T) {
	db := &mockDB{}
	rec := New(db)

	err := rec.RecordStep(context.Background(), "run-step", "llm_response", `{"text":"hello"}`)
	require.NoError(t, err)

	require.Len(t, db.calls, 1)
	call := db.calls[0]

	assert.Contains(t, call.sql, "INSERT INTO them.run_steps")
	require.Len(t, call.args, 3)
	assert.Equal(t, "run-step", call.args[0])
	assert.Equal(t, "llm_response", call.args[1])
	assert.Equal(t, `{"text":"hello"}`, call.args[2])
}

// TestDBError_propagates verifies that a database error is wrapped and
// returned by each Recorder method.
func TestDBError_propagates(t *testing.T) {
	sentinel := errors.New("db: connection refused")

	tests := []struct {
		name string
		fn   func(*Recorder) error
	}{
		{
			name: "CreateRun",
			fn: func(r *Recorder) error {
				return r.CreateRun(context.Background(), domain.Run{ID: "x", StartedAt: time.Now()})
			},
		},
		{
			name: "UpdateRunStatus",
			fn: func(r *Recorder) error {
				return r.UpdateRunStatus(context.Background(), "x", domain.RunFailed, "")
			},
		},
		{
			name: "RecordUsage",
			fn: func(r *Recorder) error {
				return r.RecordUsage(context.Background(), "x", 0, 0)
			},
		},
		{
			name: "RecordStep",
			fn: func(r *Recorder) error {
				return r.RecordStep(context.Background(), "x", "t", "c")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &mockDB{errOnce: sentinel}
			rec := New(db)

			err := tc.fn(rec)
			require.Error(t, err)
			assert.True(t, errors.Is(err, sentinel),
				"expected sentinel to be in error chain, got: %v", err)
		})
	}
}

// ── Artifact tests ────────────────────────────────────────────────────────────

// TestRecordArtifact_Success verifies that a valid artifact is persisted and
// returns a non-empty artifact ID.
func TestRecordArtifact_Success(t *testing.T) {
	db := &mockDB{}
	rec := New(db)

	id, err := rec.RecordArtifact(context.Background(), ArtifactInput{
		RunID:       "run-art-1",
		Filename:    "report.pdf",
		ContentType: "application/pdf",
		Data:        []byte("PDF content here"),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, id, "artifact ID must be non-empty on success")
	require.Len(t, db.queryRows, 1, "must issue exactly one QueryRow call")
	assert.Contains(t, db.queryRows[0].sql, "INSERT INTO them.run_artifacts")
}

// TestRecordArtifact_ExactlyOneMB verifies that data exactly at the limit succeeds.
func TestRecordArtifact_ExactlyOneMB(t *testing.T) {
	db := &mockDB{}
	rec := New(db)

	data := make([]byte, ArtifactMaxBytes) // exactly 1 MiB
	id, err := rec.RecordArtifact(context.Background(), ArtifactInput{
		RunID:    "run-boundary",
		Filename: "big.bin",
		Data:     data,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, id)
}

// TestRecordArtifact_OverLimit verifies that data exceeding 1 MiB returns
// ErrArtifactTooLarge and makes no DB call.
func TestRecordArtifact_OverLimit(t *testing.T) {
	db := &mockDB{}
	rec := New(db)

	data := make([]byte, ArtifactMaxBytes+1) // 1 MiB + 1 byte
	id, err := rec.RecordArtifact(context.Background(), ArtifactInput{
		RunID:    "run-over",
		Filename: "too-big.bin",
		Data:     data,
	})
	require.ErrorIs(t, err, ErrArtifactTooLarge)
	assert.Empty(t, id, "no ID should be returned on over-limit error")
	assert.Empty(t, db.queryRows, "no DB call must be made when limit is exceeded")
}

// TestRecordArtifact_SanitizesFilename verifies that path traversal characters
// in filenames are stripped before the DB insert.
func TestRecordArtifact_SanitizesFilename(t *testing.T) {
	var capturedFilename string
	db := &mockDB{
		queryRowFn: func(sql string, args ...any) SingleRowScanner {
			// args: run_id, app_id, session_id, filename, content_type, size, data
			if len(args) >= 4 {
				if s, ok := args[3].(string); ok {
					capturedFilename = s
				}
			}
			return &mockRow{
				scanFn: func(dest ...any) error {
					if len(dest) > 0 {
						if sp, ok := dest[0].(*string); ok {
							*sp = "00000000-0000-0000-0000-000000000002"
						}
					}
					return nil
				},
			}
		},
	}
	rec := New(db)

	_, err := rec.RecordArtifact(context.Background(), ArtifactInput{
		RunID:    "run-sanity",
		Filename: "../../etc/passwd",
		Data:     []byte("data"),
	})
	require.NoError(t, err)
	// After sanitization, only the base name should remain.
	assert.Equal(t, "passwd", capturedFilename,
		"path traversal must be stripped: got %q", capturedFilename)
}

// TestGetArtifact_WrongRun verifies that cross-run access is denied (the query
// requires artifact.run_id == runID).
func TestGetArtifact_WrongRun(t *testing.T) {
	db := &mockDB{
		queryRowFn: func(sql string, args ...any) SingleRowScanner {
			return &mockRow{
				scanFn: func(dest ...any) error {
					return errors.New("no rows in result set")
				},
			}
		},
	}
	rec := New(db)

	_, err := rec.GetArtifact(context.Background(), "run-A", "artifact-from-run-B")
	require.Error(t, err, "cross-run access must return an error")
}

// TestGetArtifact_WrongArtifact verifies that a non-existent artifact returns
// an error.
func TestGetArtifact_WrongArtifact(t *testing.T) {
	db := &mockDB{
		queryRowFn: func(sql string, args ...any) SingleRowScanner {
			return &mockRow{
				scanFn: func(dest ...any) error {
					return errors.New("no rows in result set")
				},
			}
		},
	}
	rec := New(db)

	_, err := rec.GetArtifact(context.Background(), "run-ok", "00000000-0000-0000-0000-000000000099")
	require.Error(t, err)
}

// ── sanitizeFilename unit tests ───────────────────────────────────────────────

// TestSanitizeFilename_PathTraversal verifies that directory components are
// stripped from malicious filenames.
func TestSanitizeFilename_PathTraversal(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"../../etc/passwd", "passwd"},
		{"../secret.txt", "secret.txt"},
		{"/absolute/path/file.pdf", "file.pdf"},
		{"normal.txt", "normal.txt"},
	}
	for _, tc := range cases {
		got := sanitizeFilename(tc.input)
		assert.Equal(t, tc.want, got, "sanitizeFilename(%q)", tc.input)
	}
}

// TestSanitizeFilename_Safe verifies that normal filenames pass through unchanged.
func TestSanitizeFilename_Safe(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"report.pdf", "report.pdf"},
		{"my-file_v2.csv", "my-file_v2.csv"},
		{"image (1).png", "image (1).png"},
		{"日本語.txt", "日本語.txt"},
	}
	for _, tc := range cases {
		got := sanitizeFilename(tc.input)
		assert.Equal(t, tc.want, got, "sanitizeFilename(%q)", tc.input)
	}
}

// TestSanitizeFilename_HiddenFile verifies that filenames starting with "."
// are prefixed with "file" to prevent hidden file names being served directly.
func TestSanitizeFilename_HiddenFile(t *testing.T) {
	got := sanitizeFilename(".htaccess")
	assert.Equal(t, "file.htaccess", got)
}

// TestMetadataEvent_HasNoFilePayload verifies that the artifact ID returned by
// RecordArtifact is a UUID string and not the raw data payload.
func TestMetadataEvent_HasNoFilePayload(t *testing.T) {
	db := &mockDB{}
	rec := New(db)

	secretData := []byte("TOP SECRET BINARY CONTENT")
	id, err := rec.RecordArtifact(context.Background(), ArtifactInput{
		RunID:    "run-meta",
		Filename: "secret.bin",
		Data:     secretData,
	})
	require.NoError(t, err)

	// The returned ID should not contain the file data.
	assert.NotContains(t, id, string(secretData),
		"artifact ID must not contain the file data")
	// The returned ID should look like a UUID (non-empty, reasonable length).
	assert.NotEmpty(t, id)
	assert.Less(t, len(id), 64, "ID should be a UUID, not file data")
}
