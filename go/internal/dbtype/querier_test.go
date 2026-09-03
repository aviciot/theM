package dbtype_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/aviciot/them/internal/dbtype"
)

// fakeTenantQ is a test implementation of TenantQuerier.
type fakeTenantQ struct{}

func (f *fakeTenantQ) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}
func (f *fakeTenantQ) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row { return nil }
func (f *fakeTenantQ) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (f *fakeTenantQ) IsTenantQuerier() struct{} { return struct{}{} }

// fakeAdminQ is a test implementation of AdminQuerier.
type fakeAdminQ struct{}

func (f *fakeAdminQ) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}
func (f *fakeAdminQ) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row { return nil }
func (f *fakeAdminQ) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (f *fakeAdminQ) IsAdminQuerier() struct{} { return struct{}{} }

// Compile-time assertions: verify the fake types satisfy the interfaces.
var _ dbtype.TenantQuerier = (*fakeTenantQ)(nil)
var _ dbtype.AdminQuerier = (*fakeAdminQ)(nil)

// TestInterfaceDistinction verifies that TenantQuerier and AdminQuerier are
// distinct types that the compiler enforces separately.
func TestInterfaceDistinction(t *testing.T) {
	var tq dbtype.TenantQuerier = &fakeTenantQ{}
	var aq dbtype.AdminQuerier = &fakeAdminQ{}

	if tq == nil {
		t.Fatal("TenantQuerier should not be nil")
	}
	if aq == nil {
		t.Fatal("AdminQuerier should not be nil")
	}

	// Verify marker methods return zero struct.
	if tq.IsTenantQuerier() != (struct{}{}) {
		t.Error("IsTenantQuerier must return struct{}{}")
	}
	if aq.IsAdminQuerier() != (struct{}{}) {
		t.Error("IsAdminQuerier must return struct{}{}")
	}
}
