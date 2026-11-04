package dal

import (
	"context"
	"strings"
	"testing"
)

// captureQuerier records the SQL of QueryRow calls; Scan returns a nil error.
type captureQuerier struct{ sql string }

func (c *captureQuerier) Query(context.Context, string, ...any) (RowScanner, error) {
	return nil, nil
}
func (c *captureQuerier) QueryRow(_ context.Context, sql string, _ ...any) SingleRowScanner {
	c.sql = sql
	return okRow{}
}
func (c *captureQuerier) Exec(context.Context, string, ...any) error { return nil }
func (c *captureQuerier) ExecReturning(context.Context, string, ...any) SingleRowScanner {
	return okRow{}
}

type okRow struct{}

func (okRow) Scan(...any) error { return nil }

// GW-CLIENT-01: a new gateway client is created 'valid' so the gateway accepts it (F-138).
func TestCreateGatewayClient_InsertsValidStatus(t *testing.T) {
	q := &captureQuerier{}
	_, err := NewDB(q).CreateGatewayClient(context.Background(), "00000000-0000-0000-0000-000000000001", "hash", "label")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(q.sql, "config_status") || !strings.Contains(q.sql, "'valid'") {
		t.Fatalf("insert must set config_status='valid', got: %s", q.sql)
	}
}
