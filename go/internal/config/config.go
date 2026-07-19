// Package config loads and validates all runtime configuration from environment
// variables. It panics at startup when required values are absent or insecure,
// preventing a misconfigured binary from serving traffic.
package config

import (
	"fmt"
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

	// Security — never log this value
	SecretKey string
}

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
	}

	if err := cfg.validate(); err != nil {
		return nil, err
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
	return fmt.Sprintf(
		"app_env=%s app_host=%s app_port=%d instance_id=%s "+
			"db_host=%s db_port=%d db_name=%s db_user=%s db_password=*** "+
			"db_pool_size=%d redis_host=%s redis_port=%d redis_db=%d "+
			"log_level=%s log_format=%s otel_enabled=%v secret_key=***",
		c.AppEnv, c.AppHost, c.AppPort, c.InstanceID,
		c.DBHost, c.DBPort, c.DBName, c.DBUser,
		c.DBPoolSize, c.RedisHost, c.RedisPort, c.RedisDB,
		c.LogLevel, c.LogFormat, c.OtelEnabled,
	)
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
