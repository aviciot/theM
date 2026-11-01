//go:build integration

package authserver

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func retentionTestDSN() string {
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://them:them_secret@localhost:15432/them?sslmode=disable"
}

// PurgeExpired against the real tables: only sessions past their grace and expired blacklist rows go.
//
// NOTE: PurgeExpired is global by design, so this test removes ALL expired rows of the database it runs
// against (not only the ones it seeds). Run it against a TEST database.
func TestPurgeExpired_RealDB(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, retentionTestDSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	var userID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM auth_service.users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("need one existing user: %v", err)
	}
	tag := "retention-it-" + uuid.NewString()
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM auth_service.user_sessions WHERE access_token_hash LIKE $1`, tag+"%")
		pool.Exec(context.Background(), `DELETE FROM auth_service.blacklisted_tokens WHERE token_hash LIKE $1`, tag+"%")
	})

	session := func(name, expiresExpr string) {
		q := `INSERT INTO auth_service.user_sessions (user_id, access_token_hash, refresh_token_hash, expires_at)
		      VALUES ($1, $2, $3, ` + expiresExpr + `)`
		if _, err := pool.Exec(ctx, q, userID, tag+name, "pending:"+tag+name); err != nil {
			t.Fatalf("seed session %s: %v", name, err)
		}
	}
	session("-old", `CURRENT_TIMESTAMP - interval '30 days'`)  // expired long ago: purged
	session("-recent", `CURRENT_TIMESTAMP - interval '1 day'`) // expired, inside the 7 day grace: kept
	session("-valid", `CURRENT_TIMESTAMP + interval '1 hour'`) // still valid: kept
	blacklist := func(name, expiresExpr string) {
		if _, err := pool.Exec(ctx, `INSERT INTO auth_service.blacklisted_tokens (token_hash, expires_at) VALUES ($1, `+expiresExpr+`)`, tag+name); err != nil {
			t.Fatalf("seed blacklist %s: %v", name, err)
		}
	}
	blacklist("-expired", `CURRENT_TIMESTAMP - interval '1 hour'`) // token already expired: purged
	blacklist("-active", `CURRENT_TIMESTAMP + interval '1 hour'`)  // still revokes a live token: kept

	if _, _, err := PurgeExpired(ctx, PoolRetentionDB{Pool: pool}, RetentionConfig{SessionGrace: 7 * 24 * time.Hour, Batch: 100}); err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}

	exists := func(table, col, name string) bool {
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE `+col+` = $1`, tag+name).Scan(&n)
		return n == 1
	}
	if exists("auth_service.user_sessions", "access_token_hash", "-old") {
		t.Error("session expired 30 days ago must be purged")
	}
	if !exists("auth_service.user_sessions", "access_token_hash", "-recent") {
		t.Error("session expired 1 day ago is inside the grace period and must stay")
	}
	if !exists("auth_service.user_sessions", "access_token_hash", "-valid") {
		t.Error("a valid session must stay")
	}
	if exists("auth_service.blacklisted_tokens", "token_hash", "-expired") {
		t.Error("an expired blacklist entry must be purged")
	}
	if !exists("auth_service.blacklisted_tokens", "token_hash", "-active") {
		t.Error("an active blacklist entry must stay (it still revokes a live token)")
	}
}
