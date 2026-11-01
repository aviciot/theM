//go:build integration

package llmgateway

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/events"
)

type recordingSink struct{ got []events.Event }

func (r *recordingSink) Emit(_ context.Context, e events.Event) error {
	r.got = append(r.got, e)
	return nil
}
func (r *recordingSink) Close(context.Context) error { return nil }

// WriteRequest against the real table: the event is emitted only after the row is stored, and it carries
// the application and user. A failed insert emits nothing (no phantom event in ClickHouse).
func TestWriteRequest_RealDB_EmitsAfterInsertOnly(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://them:them_secret@localhost:15432/them?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	t.Setenv("THE_M_EVENTS_GATEWAY", "dual")

	var userID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM auth_service.users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("need an existing user: %v", err)
	}
	const tenant = "00000000-0000-0000-0000-000000000001"
	provider := "inttest-" + generateUUID()[:8]
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM them.gateway_requests WHERE provider = $1`, provider)
	})

	sink := &recordingSink{}
	d := NewDAL(pool).WithEventSink(sink)

	id, err := d.WriteRequest(ctx, RequestRecord{TenantID: tenant, ApplicationID: "11111111-1111-1111-1111-111111111111", UserID: userID, Provider: provider, Status: "ok"})
	if err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM them.gateway_requests WHERE id = $1::uuid AND provider = $2`, id, provider).Scan(&n)
	if n != 1 {
		t.Fatalf("row not stored (%d)", n)
	}
	if len(sink.got) != 1 {
		t.Fatalf("events after a successful insert = %d, want 1", len(sink.got))
	}
	g := sink.got[0].GatewayRequest
	if g.RequestID != id || g.ApplicationID != "11111111-1111-1111-1111-111111111111" || g.UserID == "" {
		t.Errorf("event not attributable / not correlated with the row: %+v (row id %s)", g, id)
	}

	// An insert that fails (invalid tenant id) must not produce an event.
	sink.got = nil
	if _, err := d.WriteRequest(ctx, RequestRecord{TenantID: "not-a-uuid", Provider: provider, Status: "ok"}); err == nil {
		t.Fatal("expected the insert to fail")
	}
	if len(sink.got) != 0 {
		t.Errorf("a failed insert must not emit an event, got %d", len(sink.got))
	}
}
