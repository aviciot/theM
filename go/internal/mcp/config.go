// Package mcp provides the MCP Store service: registry cache, health-check
// loop, tool discovery, and tool execution for them-mcp-service.
package mcp

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime configuration for them-mcp-service.
type Config struct {
	AppPort    int
	InstanceID string
	LogLevel   string
	LogFormat  string

	DBHost     string
	DBPort     int
	DBName     string
	DBUser     string
	DBPassword string

	RedisHost     string
	RedisPort     int
	RedisPassword string
	RedisDB       int

	// SecretKey is used for Fernet credential decryption (same key as them-go-bridge).
	SecretKey string

	// HealthIntervalSeconds is how often the health loop probes each MCP server.
	HealthIntervalSeconds int

	// AllowHTTP permits non-HTTPS MCP server URLs (dev only).
	AllowHTTP bool

	// AllowStdio permits stdio transport (disabled by default).
	AllowStdio bool
}

// LoadConfig reads environment variables and validates required fields.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		AppPort:               getEnvInt("APP_PORT", 8010),
		InstanceID:            getEnv("THE_M_INSTANCE_ID", "mcp-service-1"),
		LogLevel:              strings.ToUpper(getEnv("LOG_LEVEL", "INFO")),
		LogFormat:             strings.ToLower(getEnv("LOG_FORMAT", "json")),
		DBHost:                getEnv("DATABASE_HOST", ""),
		DBPort:                getEnvInt("DATABASE_PORT", 5432),
		DBName:                getEnv("DATABASE_NAME", "them"),
		DBUser:                getEnv("DATABASE_USER", "them"),
		DBPassword:            getEnv("DATABASE_PASSWORD", ""),
		RedisHost:             getEnv("REDIS_HOST", "localhost"),
		RedisPort:             getEnvInt("REDIS_PORT", 6379),
		RedisPassword:         getEnv("REDIS_PASSWORD", ""),
		RedisDB:               getEnvInt("REDIS_DB", 0),
		SecretKey:             getEnv("SECRET_KEY", ""),
		HealthIntervalSeconds: getEnvInt("MCP_HEALTH_INTERVAL_SECONDS", 30),
		AllowHTTP:             getEnvBool("MCP_ALLOW_HTTP", false),
		AllowStdio:            getEnvBool("MCP_ALLOW_STDIO", false),
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.DBHost == "" {
		return fmt.Errorf("DATABASE_HOST is required")
	}
	if c.DBPassword == "" {
		return fmt.Errorf("DATABASE_PASSWORD is required")
	}
	if c.SecretKey == "" {
		return fmt.Errorf("SECRET_KEY is required")
	}
	if c.SecretKey == "change-this-in-production" {
		return fmt.Errorf("SECRET_KEY must not use the default value")
	}
	return nil
}

// DSN returns a pgx-compatible connection string.
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s pool_max_conns=10 sslmode=disable",
		c.DBHost, c.DBPort, c.DBName, c.DBUser, c.DBPassword,
	)
}

// RedisAddr returns "host:port".
func (c *Config) RedisAddr() string {
	return fmt.Sprintf("%s:%d", c.RedisHost, c.RedisPort)
}

// Addr returns the HTTP listen address.
func (c *Config) Addr() string {
	return fmt.Sprintf("0.0.0.0:%d", c.AppPort)
}

// SafeString returns a log-safe config representation with secrets redacted.
func (c *Config) SafeString() string {
	return fmt.Sprintf(
		"port=%d instance=%s db_host=%s db_name=%s redis_host=%s health_interval=%ds allow_http=%v allow_stdio=%v",
		c.AppPort, c.InstanceID, c.DBHost, c.DBName, c.RedisHost,
		c.HealthIntervalSeconds, c.AllowHTTP, c.AllowStdio,
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
