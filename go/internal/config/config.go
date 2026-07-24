// Package config loads and validates all runtime configuration from environment
// variables. It panics at startup when required values are absent or insecure,
// preventing a misconfigured binary from serving traffic.
package config

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// Config holds every configuration value used by the application.
// All fields are exported so downstream packages can read them directly.
type Config struct {
	// App-level settings
	AppEnv        string
	AppHost       string
	AppPort       int
	InstanceID    string
	LogLevel      string
	LogFormat     string
	OtelEnabled   bool

	// PostgreSQL
	DBHost     string
	DBPort     int
	DBName     string
	DBUser     string
	DBPassword string
	DBPoolSize int

	// Redis
	RedisHost     string
	RedisPort     int
	RedisPassword string
	RedisDB       int

	// Security — never log these values
	SecretKey string
	// JWTSecret is the HMAC secret used by the auth service to sign HS256 tokens.
	// Read from JWT_SECRET env var (same name as the auth service uses).
	// Takes priority over SecretKey for JWT validation.
	JWTSecret string

	// JWT (RS256 local validation)
	// JWT_PUBLIC_KEY_PEM is a PEM-encoded RSA public key. When set, JWT
	// middleware is enabled and tokens are validated locally without any
	// HTTP call to the auth service. When empty, bearer-only mode is used.
	JWTPublicKeyPEM string
	// JWTPublicKey is parsed from JWTPublicKeyPEM at startup. Nil when
	// JWTPublicKeyPEM is empty.
	JWTPublicKey *rsa.PublicKey

	// LLM providers
	AnthropicAPIKey string

	// Temporal
	TemporalEnabled  bool
	TemporalHostPort string

	// Reconciler
	// ReconcilerDryRun controls whether the run reconciler writes to the DB.
	// Default is true (safe). Set RECONCILER_DRY_RUN=false to enable writes.
	// Any invalid or missing value falls back to true.
	ReconcilerDryRun bool

	// RunEventsMode selects the run-event delivery transport (Phase 11c-B).
	// Parsed from RUN_EVENTS_MODE: "dual" | "streams" | anything-else→"pubsub".
	RunEventsMode RunEventsMode
}

// RunEventsMode selects how run events are delivered to WS/SSE clients.
// It is set from the RUN_EVENTS_MODE environment variable (Phase 11c-B).
type RunEventsMode string

const (
	// RunEventsModePublish is legacy Pub/Sub-only delivery (default). New runs
	// get events_transport='pubsub' and the Go bridge reads from the Pub/Sub
	// channel only. This is the safe default for anything but 'dual'/'streams'.
	RunEventsModePublish RunEventsMode = "pubsub"
	// RunEventsModeDual writes to Redis Streams AND Pub/Sub (via the Python Lua
	// script). New runs get events_transport='streams'; legacy rows keep 'pubsub'.
	RunEventsModeDual RunEventsMode = "dual"
	// RunEventsModeStreams writes to Redis Streams only. New runs get
	// events_transport='streams'; the Go bridge reads exclusively from the stream.
	RunEventsModeStreams RunEventsMode = "streams"
)

// DefaultSecretKey is the insecure placeholder that must never reach production.
const DefaultSecretKey = "change-this-in-production"

// Load reads environment variables, applies defaults, and validates required
// fields. It calls os.Exit(1) (via a fatal log to stderr) when SECRET_KEY is
// absent or still set to the insecure default.
func Load() (*Config, error) {
	cfg := &Config{
		AppEnv:      getEnv("APP_ENV", "development"),
		AppHost:     getEnv("APP_HOST", "0.0.0.0"),
		AppPort:     getEnvInt("APP_PORT", 8002),
		InstanceID:  getEnv("THE_M_INSTANCE_ID", "go-bridge-1"),
		LogLevel:    strings.ToUpper(getEnv("LOG_LEVEL", "INFO")),
		LogFormat:   strings.ToLower(getEnv("LOG_FORMAT", "json")),
		OtelEnabled: getEnvBool("OTEL_ENABLED", false),

		DBHost:     getEnv("DATABASE_HOST", ""),
		DBPort:     getEnvInt("DATABASE_PORT", 5432),
		DBName:     getEnv("DATABASE_NAME", "them"),
		DBUser:     getEnv("DATABASE_USER", "them"),
		DBPassword: getEnv("DATABASE_PASSWORD", ""),
		DBPoolSize: getEnvInt("DATABASE_POOL_SIZE", 20),

		RedisHost:     getEnv("REDIS_HOST", "localhost"),
		RedisPort:     getEnvInt("REDIS_PORT", 6379),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       getEnvInt("REDIS_DB", 0),

		SecretKey: getEnv("SECRET_KEY", ""),
		JWTSecret: getEnv("JWT_SECRET", ""),

		JWTPublicKeyPEM: getEnv("JWT_PUBLIC_KEY_PEM", ""),

		AnthropicAPIKey: getEnv("ANTHROPIC_API_KEY", ""),

		TemporalEnabled:  getEnvBool("TEMPORAL_ENABLED", false),
		TemporalHostPort: getEnv("TEMPORAL_HOST_PORT", "localhost:7233"),

		ReconcilerDryRun: getEnvBoolSafe("RECONCILER_DRY_RUN", true),

		RunEventsMode: parseRunEventsMode(os.Getenv("RUN_EVENTS_MODE")),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	// Parse and validate JWT public key if provided.
	// Fail fast on malformed PEM — better to crash at startup than to silently
	// fall back to unauthenticated access at runtime.
	if cfg.JWTPublicKeyPEM != "" {
		key, err := parseRSAPublicKey([]byte(cfg.JWTPublicKeyPEM))
		if err != nil {
			return nil, fmt.Errorf("JWT_PUBLIC_KEY_PEM is malformed: %w", err)
		}
		cfg.JWTPublicKey = key
		slog.Info("auth: JWT middleware enabled (RS256 local validation)")
	} else {
		slog.Info("auth: JWT middleware disabled — bearer-only mode (JWT_PUBLIC_KEY_PEM not set)")
	}

	return cfg, nil
}

// validate returns an error for any invalid or insecure configuration values.
func (c *Config) validate() error {
	if c.SecretKey == "" {
		return fmt.Errorf("SECRET_KEY is required but was not set")
	}
	if c.SecretKey == DefaultSecretKey {
		return fmt.Errorf("SECRET_KEY must not use the default value %q", DefaultSecretKey)
	}
	if c.DBPassword == "" {
		return fmt.Errorf("DATABASE_PASSWORD is required but was not set")
	}
	if c.DBHost == "" {
		return fmt.Errorf("DATABASE_HOST is required but was not set")
	}
	return nil
}

// DSN returns a PostgreSQL connection string suitable for pgx.
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s pool_max_conns=%d sslmode=disable",
		c.DBHost, c.DBPort, c.DBName, c.DBUser, c.DBPassword, c.DBPoolSize,
	)
}

// RedisAddr returns the Redis address in "host:port" form.
func (c *Config) RedisAddr() string {
	return fmt.Sprintf("%s:%d", c.RedisHost, c.RedisPort)
}

// SafeString returns a log-safe representation of the config — all secret
// fields replaced with "***".
func (c *Config) SafeString() string {
	jwtMode := "disabled"
	if c.JWTPublicKey != nil {
		jwtMode = "enabled (RS256)"
	} else if c.JWTSecret != "" {
		jwtMode = "enabled (HS256/JWT_SECRET)"
	} else if c.SecretKey != "" {
		jwtMode = "enabled (HS256/SECRET_KEY)"
	}
	anthropicMode := "not-set"
	if c.AnthropicAPIKey != "" {
		anthropicMode = "set"
	}
	return fmt.Sprintf(
		"app_env=%s app_host=%s app_port=%d instance_id=%s "+
			"db_host=%s db_port=%d db_name=%s db_user=%s db_password=*** "+
			"db_pool_size=%d redis_host=%s redis_port=%d redis_db=%d "+
			"log_level=%s log_format=%s otel_enabled=%v secret_key=*** "+
			"jwt_middleware=%s anthropic_api_key=%s "+
			"temporal_enabled=%v temporal_host_port=%s "+
			"reconciler_dry_run=%v run_events_mode=%s",
		c.AppEnv, c.AppHost, c.AppPort, c.InstanceID,
		c.DBHost, c.DBPort, c.DBName, c.DBUser,
		c.DBPoolSize, c.RedisHost, c.RedisPort, c.RedisDB,
		c.LogLevel, c.LogFormat, c.OtelEnabled,
		jwtMode, anthropicMode,
		c.TemporalEnabled, c.TemporalHostPort,
		c.ReconcilerDryRun, c.RunEventsMode,
	)
}

// parseRSAPublicKey decodes a PEM block and parses it as an RSA public key.
// It accepts both PKIX (BEGIN PUBLIC KEY) and PKCS#1 (BEGIN RSA PUBLIC KEY)
// formats. This is the config-layer copy; the canonical implementation lives
// in internal/auth where the jwt.go validation logic is also kept.
func parseRSAPublicKey(pemBytes []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("failed to decode PEM block — ensure the key uses standard PEM encoding")
	}
	switch block.Type {
	case "PUBLIC KEY":
		pub, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKIX public key: %w", err)
		}
		rsaKey, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("PEM does not contain an RSA public key")
		}
		return rsaKey, nil
	case "RSA PUBLIC KEY":
		pub, err := x509.ParsePKCS1PublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS1 public key: %w", err)
		}
		return pub, nil
	default:
		return nil, fmt.Errorf("unsupported PEM block type %q; expected \"PUBLIC KEY\" or \"RSA PUBLIC KEY\"", block.Type)
	}
}

// parseRunEventsMode maps the RUN_EVENTS_MODE env value to a RunEventsMode.
// Only "dual" and "streams" (case-insensitive) select non-default transports;
// every other value — including "pubsub", unset, or garbage — falls back to
// pubsub. This mirrors getEnvBoolSafe intent: default to the safe legacy path.
func parseRunEventsMode(v string) RunEventsMode {
	switch strings.ToLower(v) {
	case "dual":
		return RunEventsModeDual
	case "streams":
		return RunEventsModeStreams
	default:
		return RunEventsModePublish
	}
}

// getEnv returns the value of the named environment variable, or fallback when
// the variable is unset or empty.
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getEnvInt parses an environment variable as an integer, returning fallback on
// parse errors or when the variable is absent.
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

// getEnvBool parses an environment variable as a boolean, returning fallback on
// parse errors or when the variable is absent.
func getEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

// getEnvBoolSafe parses an environment variable as a boolean, always returning
// fallback on parse errors or when the variable is absent. Unlike getEnvBool it
// is named explicitly to signal the intent: invalid values must fail to the safe
// default rather than the opposite. Used for security-sensitive feature flags
// such as RECONCILER_DRY_RUN where false enables destructive writes.
func getEnvBoolSafe(key string, safeDefault bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return safeDefault
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return safeDefault
	}
	return b
}
