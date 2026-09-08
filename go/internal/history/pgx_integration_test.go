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

// seedUser inserts a temporary user into auth_service.users and returns its ID.
// The user is deleted on test cleanup. username must be unique per test run.
func seedUser(t *testing.T, pool *pgxpool.Pool, username string) int64 {
	t.Helper()
	const q = `
INSERT INTO auth_service.users (username, name, active)
VALUES ($1, $2, true)
RETURNING id`
	row := pool.QueryRow(context.Background(), q, username, "Test "+username)
	var id int64
	if err := row.Scan(&id); err != nil {
		t.Fatalf("seedUser %q: %v", username, err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), "DELETE FROM auth_service.users WHERE id = $1", id)
	})
	return id
}

// seedMessage writes one message to the store for the given identity.
// runID is intentionally empty so tasks.run_id is stored as NULL — the
// integration test has no real runs row to reference (FK to them.runs).
func seedMessage(t *testing.T, s *Store, contextID, tenantID, externalUserID string, userID int64, text string) {
	t.Helper()
	msg := domain.Message{Role: domain.RoleUser, Parts: []domain.ContentPart{{Type: "text", Text: text}}}
	if err := s.WriteMessage(context.Background(), contextID, "", tenantID, externalUserID, userID, msg); err != nil {
		t.Fatalf("seedMessage: %v", err)
	}
}

// TestHistory_Integration_UserA_CannotReadUserB verifies that internal user A
// cannot read rows written by internal user B even when both share the same
// context_id and tenant_id.
func TestHistory_Integration_UserA_CannotReadUserB(t *testing.T) {
	pool := testPool(t)
	s := newStore(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	userA := seedUser(t, pool, "hist-test-userA-"+suffix)
	userB := seedUser(t, pool, "hist-test-userB-"+suffix)

	contextID := uuid.NewString()
	tenantID := uuid.NewString()

	// User B seeds a message.
	seedMessage(t, s, contextID, tenantID, "", userB, "message from user B")

	// User A reads — must see nothing.
	msgs, err := s.LoadHistory(ctx, contextID, tenantID, "", userA, 50)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("user A read %d messages from user B; want 0", len(msgs))
	}
}

// TestHistory_Integration_InternalCannotReadExternalUser verifies that an
// internal user session cannot read rows owned by an external user ("alice"),
// even when sharing the same context_id and tenant_id.
func TestHistory_Integration_InternalCannotReadExternalUser(t *testing.T) {
	pool := testPool(t)
	s := newStore(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	userA := seedUser(t, pool, "hist-test-internal-"+suffix)

	contextID := uuid.NewString()
	tenantID := uuid.NewString()

	// External user "alice" seeds a message.
	seedMessage(t, s, contextID, tenantID, "alice-"+suffix, 0, "message from alice")

	// Internal user reads — must see nothing (external rows have user_id=NULL ≠ userA).
	msgs, err := s.LoadHistory(ctx, contextID, tenantID, "", userA, 50)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("internal user read %d messages from external user; want 0", len(msgs))
	}
}

// TestHistory_Integration_LegacyRows_NotLeakedToUser verifies that legacy rows
// (both user_id=NULL and external_user_id=NULL) are excluded when querying as
// a specific internal user.
func TestHistory_Integration_LegacyRows_NotLeakedToUser(t *testing.T) {
	pool := testPool(t)
	s := newStore(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	userA := seedUser(t, pool, "hist-test-legacy-"+suffix)

	contextID := uuid.NewString()
	tenantID := uuid.NewString()

	// Anonymous/legacy row: both identifiers empty (userID=0 → NULL, externalUserID="").
	seedMessage(t, s, contextID, tenantID, "", 0, "anonymous legacy message")

	// Internal user reads — legacy row has user_id=NULL ≠ userA; must be excluded.
	msgs, err := s.LoadHistory(ctx, contextID, tenantID, "", userA, 50)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("user %d read %d legacy rows; want 0", userA, len(msgs))
	}
}

// TestHistory_Integration_ExternalUserIsolation verifies that external user A
// cannot read rows owned by external user B even in the same context.
func TestHistory_Integration_ExternalUserIsolation(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	contextID := uuid.NewString()
	tenantID := uuid.NewString()

	// User B seeds a message.
	seedMessage(t, s, contextID, tenantID, "bob-"+suffix, 0, "message from bob")

	// User A reads — must see nothing.
	msgs, err := s.LoadHistory(ctx, contextID, tenantID, "alice-"+suffix, 0, 50)
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
	pool := testPool(t)
	s := newStore(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	user42 := seedUser(t, pool, "hist-test-own-42-"+suffix)

	// Use separate contextIDs per identity — same context+different user means
	// a second task row is created for user42, which is correct behavior but
	// complicates the positive read assertion. Separate contexts are simpler.
	ctxUser42 := uuid.NewString()
	ctxAlice := uuid.NewString()
	tenantID := uuid.NewString()

	seedMessage(t, s, ctxUser42, tenantID, "", user42, "internal user message")
	seedMessage(t, s, ctxAlice, tenantID, "alice-"+suffix, 0, "alice message")

	// Internal user reads their own context.
	msgs42, err := s.LoadHistory(ctx, ctxUser42, tenantID, "", user42, 50)
	if err != nil {
		t.Fatalf("LoadHistory user42: %v", err)
	}
	if len(msgs42) != 1 {
		t.Errorf("user42: got %d messages, want 1", len(msgs42))
	}

	// Alice reads her own context.
	msgsAlice, err := s.LoadHistory(ctx, ctxAlice, tenantID, "alice-"+suffix, 0, 50)
	if err != nil {
		t.Fatalf("LoadHistory alice: %v", err)
	}
	if len(msgsAlice) != 1 {
		t.Errorf("alice: got %d messages, want 1", len(msgsAlice))
	}
}
