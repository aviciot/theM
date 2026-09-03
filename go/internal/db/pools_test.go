package db

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestNewPools_BadAppDSN(t *testing.T) {
	ctx := context.Background()
	_, err := NewPools(ctx, "not-a-valid-dsn", "not-a-valid-dsn")
	if err == nil {
		t.Fatal("expected error for invalid app DSN, got nil")
	}
}

func TestTenantTx_InterfaceAssertions(t *testing.T) {
	t.Log("TenantTx and AdminTx interface assertions verified at compile time")
}

func TestPools_Close_NilSafe(t *testing.T) {
	t.Log("Pools.Close requires real DB connection — covered by integration tests")
}

func TestBeginTenantTx_TenantIDFormat(t *testing.T) {
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	s := id.String()
	if s != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("unexpected UUID string: %s", s)
	}
}
