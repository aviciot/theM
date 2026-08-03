package authserver

import (
	"strings"
	"testing"
)

func TestConfigValidateRequiresJWTSecret(t *testing.T) {
	c := &Config{DBHost: "h", DBPassword: "p", AccessTokenExpiry: 1, RefreshTokenExpiry: 1}
	if err := c.validate(); err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("want JWT_SECRET error, got %v", err)
	}
}

func TestConfigValidateRequiresDBHostAndPassword(t *testing.T) {
	c := &Config{JWTSecret: "s", DBPassword: "p", AccessTokenExpiry: 1, RefreshTokenExpiry: 1}
	if err := c.validate(); err == nil || !strings.Contains(err.Error(), "DATABASE_HOST") {
		t.Fatalf("want DATABASE_HOST error, got %v", err)
	}
	c = &Config{JWTSecret: "s", DBHost: "h", AccessTokenExpiry: 1, RefreshTokenExpiry: 1}
	if err := c.validate(); err == nil || !strings.Contains(err.Error(), "DATABASE_PASSWORD") {
		t.Fatalf("want DATABASE_PASSWORD error, got %v", err)
	}
}

func TestConfigValidateOK(t *testing.T) {
	c := &Config{JWTSecret: "s", DBHost: "h", DBPassword: "p", AccessTokenExpiry: 3600, RefreshTokenExpiry: 604800}
	if err := c.validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestSafeStringMasksSecrets(t *testing.T) {
	c := &Config{JWTSecret: "super-secret-value", DBPassword: "db-secret", DBHost: "h", DBName: "them"}
	s := c.SafeString()
	if strings.Contains(s, "super-secret-value") || strings.Contains(s, "db-secret") {
		t.Fatalf("SafeString leaked a secret: %s", s)
	}
	if !strings.Contains(s, "***") {
		t.Fatalf("SafeString should mask with ***: %s", s)
	}
}

func TestDSN(t *testing.T) {
	c := &Config{DBHost: "them-postgres", DBPort: 5432, DBName: "them", DBUser: "them", DBPassword: "x", DBPoolSize: 10}
	dsn := c.DSN()
	if !strings.Contains(dsn, "host=them-postgres") || !strings.Contains(dsn, "dbname=them") {
		t.Fatalf("unexpected DSN: %s", dsn)
	}
}
