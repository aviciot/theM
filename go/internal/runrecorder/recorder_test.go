package runrecorder

import (
	"context"
	"errors"
	"testing"
	"time"

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

// mockDB implements DBQuerier for tests. It records all Exec calls and can be
// configured to return a specific error.
type mockDB struct {
	calls   []execCall
	errOnce error // if set, returned on the next Exec and cleared
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

	// Arguments: id, context_id, application_id, entry_point_slug, status, started_at
	require.Len(t, call.args, 6)
	assert.Equal(t, "run-abc", call.args[0])
	assert.Equal(t, "ctx-1", call.args[1])
	assert.Equal(t, int64(42), call.args[2])
	assert.Equal(t, "ws-chat", call.args[3])
	assert.Equal(t, "running", call.args[4])
	assert.Equal(t, now, call.args[5])
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
