// Package authserver implements the-M's user-facing authentication service in
// Go. It reads users and roles from the existing auth_service schema in the them
// database, verifies bcrypt passwords, and issues HS256 JWTs whose claim shape is
// byte-compatible with the tokens the Go bridge already validates
// (internal/auth.ValidateHS256JWT) and the tokens the legacy Python auth service
// issued (auth_service/services/token_service.py).
//
// It deliberately implements ONLY the UI-facing contract (login/me/refresh/logout
// plus verify/validate for service-to-service callers). User/role/team admin CRUD
// is out of scope and remains in the Python service source.
package authserver

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds every runtime value used by the auth server. Secret fields must
// never be logged; use SafeString.
type Config struct {
	Host string
	Port int

	// DB connection to the them database (which contains the auth_service schema).
	DBHost     string
	DBPort     int
	DBName     string
	DBUser     string
	DBPassword string
	DBPoolSize int

	// JWTSecret is the HMAC-SHA256 signing key. It MUST equal the secret the Go
	// bridge validates with (JWT_SECRET / ${THE_M_JWT_SECRET}); otherwise tokens
	// this service issues will be rejected by the bridge.
	JWTSecret string

	// AccessTokenExpiry / RefreshTokenExpiry are fallbacks in seconds when a role
	// carries no token_expiry. They mirror the Python settings defaults.
	AccessTokenExpiry  int
	RefreshTokenExpiry int

	LogLevel  string
	LogFormat string

	InstanceID string
}

// LoadConfig reads configuration from the environment, applies defaults, and
// validates required fields. It returns an error (never os.Exit) so the caller
// controls the failure path.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		Host: getEnv("HOST", "0.0.0.0"),
		Port: getEnvInt("PORT", 8703),

		DBHost:     getEnv("DATABASE_HOST", ""),
		DBPort:     getEnvInt("DATABASE_PORT", 5432),
		DBName:     getEnv("DATABASE_NAME", "them"),
		DBUser:     getEnv("DATABASE_USER", "them"),
		DBPassword: getEnv("DATABASE_PASSWORD", ""),
		DBPoolSize: getEnvInt("DATABASE_POOL_SIZE", 10),

		JWTSecret: getEnv("JWT_SECRET", ""),

		AccessTokenExpiry:  getEnvInt("ACCESS_TOKEN_EXPIRY", 3600),
		RefreshTokenExpiry: getEnvInt("REFRESH_TOKEN_EXPIRY", 604800),

		LogLevel:  getEnv("LOG_LEVEL", "INFO"),
		LogFormat: getEnv("LOG_FORMAT", "json"),

		InstanceID: getEnv("THE_M_INSTANCE_ID", "auth-go-1"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.JWTSecret == "" {
		return fmt.Errorf("JWT_SECRET is required but was not set")
	}
	if c.DBHost == "" {
		return fmt.Errorf("DATABASE_HOST is required but was not set")
	}
	if c.DBPassword == "" {
		return fmt.Errorf("DATABASE_PASSWORD is required but was not set")
	}
	if c.AccessTokenExpiry <= 0 {
		return fmt.Errorf("ACCESS_TOKEN_EXPIRY must be positive")
	}
	if c.RefreshTokenExpiry <= 0 {
		return fmt.Errorf("REFRESH_TOKEN_EXPIRY must be positive")
	}
	return nil
}

// DSN returns a pgx connection string for the them database.
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s pool_max_conns=%d sslmode=disable",
		c.DBHost, c.DBPort, c.DBName, c.DBUser, c.DBPassword, c.DBPoolSize,
	)
}

// Addr returns the listen address in host:port form.
func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// SafeString returns a log-safe one-line representation. All secrets are masked.
func (c *Config) SafeString() string {
	return fmt.Sprintf(
		"host=%s port=%d db_host=%s db_port=%d db_name=%s db_user=%s db_password=*** "+
			"jwt_secret=*** access_expiry=%d refresh_expiry=%d log_level=%s log_format=%s instance_id=%s",
		c.Host, c.Port, c.DBHost, c.DBPort, c.DBName, c.DBUser,
		c.AccessTokenExpiry, c.RefreshTokenExpiry, c.LogLevel, c.LogFormat, c.InstanceID,
	)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
