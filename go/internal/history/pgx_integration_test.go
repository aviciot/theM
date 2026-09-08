//go:build integration

package history

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/domain"
)

// testPool opens a pgxpool using DATABASE_PASSWORD from the environment.
// The test is skipped when the password is absent (CI without a live DB).
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	host := os.Getenv("DATABASE_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("DATABASE_PORT")
	if port == "" {
		port = "5432"
	}
	user := os.Getenv("DATABASE_USER")
	if user == "" {
		user = "them"
	}
	password := os.Getenv("DATABASE_PASSWORD")
	dbname := os.Getenv("DATABASE_NAME")
	if dbname == "" {
		dbname = "them"
	}
	if password == "" {
		t.Skip("DATABASE_PASSWORD not set — skipping history integration test")
	}
	dsn := fmt.Sprintf("host=%s port=%s dbname=%s user=%s password=%s sslmode=disable",
		host, port, dbname, user, password)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newStore creates a Store backed by the integration pool.
func newStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(testPool(t), slog.Default())
}

// seedMessage writes one message to the store for the given identity, returning
// the context_id used. Pass externalUserID="" and userID=0 for anonymous/legacy rows.
func seedMessage(t *testing.T, s *Store, contextID, tenantID, externalUserID string, userID int64, text string) {
	t.Helper()
	runID := uuid.NewString()
	msg := domain.Message{Role: domain.RoleUser, Parts: []domain.ContentPart{{Type: "text", Text: text}}}
	if err := s.WriteMessage(context.Background(), contextID, runID, tenantID, externalUserID, userID, msg); err != nil {
		t.Fatalf("seedMessage: %v", err)
	}
}

// TestHistory_Integration_UserA_CannotReadUserB verifies that internal user A
// (userID=42) cannot read rows written by internal user B (userID=99) even when
// both share the same context_id and tenant_id.
func TestHistory_Integration_UserA_CannotReadUserB(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	contextID := uuid.NewString()
	tenantID := uuid.NewString()

	// User B seeds a message.
	seedMessage(t, s, contextID, tenantID, "", 99, "message from user B")

	// User A reads — must see nothing.
	msgs, err := s.LoadHistory(ctx, contextID, tenantID, "", 42, 50)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("user A read %d messages from user B; want 0", len(msgs))
	}
}

// TestHistory_Integration_InternalCannotReadExternalUser verifies that an
// internal user session (userID=42) cannot read rows owned by an external user
// ("alice"), even when sharing the same context_id and tenant_id.
func TestHistory_Integration_InternalCannotReadExternalUser(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	contextID := uuid.NewString()
	tenantID := uuid.NewString()

	// External user "alice" seeds a message.
	seedMessage(t, s, contextID, tenantID, "alice", 0, "message from alice")

	// Internal user 42 reads — must see nothing (external rows have user_id=NULL ≠ 42).
	msgs, err := s.LoadHistory(ctx, contextID, tenantID, "", 42, 50)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("internal user read %d messages from external user; want 0", len(msgs))
	}
}

// TestHistory_Integration_LegacyRows_NotLeakedToUser verifies that legacy rows
// (both user_id=NULL and external_user_id=NULL, written by anonymous sessions)
// are excluded when querying as a specific user (userID=42).
func TestHistory_Integration_LegacyRows_NotLeakedToUser(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	contextID := uuid.NewString()
	tenantID := uuid.NewString()

	// Anonymous/legacy row: both identifiers empty.
	seedMessage(t, s, contextID, tenantID, "", 0, "anonymous legacy message")

	// Internal user 42 reads — legacy row has user_id=NULL ≠ 42; must be excluded.
	msgs, err := s.LoadHistory(ctx, contextID, tenantID, "", 42, 50)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("user 42 read %d legacy rows; want 0", len(msgs))
	}
}

// TestHistory_Integration_ExternalUserIsolation verifies that external user A
// cannot read rows owned by external user B even in the same context.
func TestHistory_Integration_ExternalUserIsolation(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	contextID := uuid.NewString()
	tenantID := uuid.NewString()

	// User B seeds a message.
	seedMessage(t, s, contextID, tenantID, "bob", 0, "message from bob")

	// User A reads — must see nothing.
	msgs, err := s.LoadHistory(ctx, contextID, tenantID, "alice", 0, 50)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("alice read %d messages from bob; want 0", len(msgs))
	}
}

// TestHistory_Integration_OwnHistory verifies the positive case: each identity
// can read exactly the messages they wrote.
func TestHistory_Integration_OwnHistory(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	contextID := uuid.NewString()
	tenantID := uuid.NewString()

	// Three different identities write into the same context.
	seedMessage(t, s, contextID, tenantID, "", 42, "internal user 42 message")
	seedMessage(t, s, contextID, tenantID, "alice", 0, "alice message")
	seedMessage(t, s, contextID, tenantID, "", 0, "anonymous message")

	// Internal user 42 reads their own message.
	msgs42, err := s.LoadHistory(ctx, contextID, tenantID, "", 42, 50)
	if err != nil {
		t.Fatalf("LoadHistory user42: %v", err)
	}
	if len(msgs42) != 1 {
		t.Errorf("user 42: got %d messages, want 1", len(msgs42))
	}

	// Alice reads her own message.
	msgsAlice, err := s.LoadHistory(ctx, contextID, tenantID, "alice", 0, 50)
	if err != nil {
		t.Fatalf("LoadHistory alice: %v", err)
	}
	if len(msgsAlice) != 1 {
		t.Errorf("alice: got %d messages, want 1", len(msgsAlice))
	}
}
