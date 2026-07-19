package config_test

import (
	"os"
	"testing"

	"github.com/aviciot/them/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setEnv is a helper that sets environment variables for a test and returns a
// cleanup function that restores the original values.
func setEnv(t *testing.T, pairs map[string]string) {
	t.Helper()
	original := make(map[string]string, len(pairs))
	for k, v := range pairs {
		original[k] = os.Getenv(k)
		os.Setenv(k, v)
	}
	t.Cleanup(func() {
		for k, orig := range original {
			if orig == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, orig)
			}
		}
	})
}

// validEnv returns the minimum set of env vars that produce a valid config.
func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_HOST":     "localhost",
		"DATABASE_PASSWORD": "supersecret",
		"SECRET_KEY":        "a-real-secret-key-that-is-long-enough",
	}
}

func TestLoad_ValidConfig(t *testing.T) {
	setEnv(t, validEnv())

	cfg, err := config.Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "development", cfg.AppEnv)
	assert.Equal(t, "0.0.0.0", cfg.AppHost)
	assert.Equal(t, 8002, cfg.AppPort)
	assert.Equal(t, "localhost", cfg.DBHost)
	assert.Equal(t, 5432, cfg.DBPort)
	assert.Equal(t, "them", cfg.DBName)
	assert.Equal(t, "them", cfg.DBUser)
	assert.Equal(t, "supersecret", cfg.DBPassword)
	assert.Equal(t, 20, cfg.DBPoolSize)
	assert.Equal(t, "localhost", cfg.RedisHost)
	assert.Equal(t, 6379, cfg.RedisPort)
	assert.Equal(t, 0, cfg.RedisDB)
	assert.Equal(t, "go-bridge-1", cfg.InstanceID)
	assert.Equal(t, "INFO", cfg.LogLevel)
	assert.Equal(t, "json", cfg.LogFormat)
}

func TestLoad_MissingDatabasePassword(t *testing.T) {
	env := validEnv()
	delete(env, "DATABASE_PASSWORD")
	setEnv(t, env)
	os.Unsetenv("DATABASE_PASSWORD")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_PASSWORD")
}

func TestLoad_EmptySecretKey(t *testing.T) {
	env := validEnv()
	env["SECRET_KEY"] = ""
	setEnv(t, env)

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SECRET_KEY")
}

func TestLoad_DefaultSecretKey(t *testing.T) {
	env := validEnv()
	env["SECRET_KEY"] = config.DefaultSecretKey
	setEnv(t, env)

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SECRET_KEY")
}

func TestLoad_MissingDatabaseHost(t *testing.T) {
	env := validEnv()
	delete(env, "DATABASE_HOST")
	setEnv(t, env)
	os.Unsetenv("DATABASE_HOST")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_HOST")
}

func TestLoad_CustomPort(t *testing.T) {
	env := validEnv()
	env["APP_PORT"] = "9999"
	setEnv(t, env)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, 9999, cfg.AppPort)
}

func TestConfig_DSN(t *testing.T) {
	setEnv(t, validEnv())

	cfg, err := config.Load()
	require.NoError(t, err)

	dsn := cfg.DSN()
	assert.Contains(t, dsn, "host=localhost")
	assert.Contains(t, dsn, "dbname=them")
	assert.Contains(t, dsn, "supersecret")
}

func TestConfig_RedisAddr(t *testing.T) {
	setEnv(t, validEnv())

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, "localhost:6379", cfg.RedisAddr())
}

func TestConfig_SafeString_MasksSecrets(t *testing.T) {
	setEnv(t, validEnv())

	cfg, err := config.Load()
	require.NoError(t, err)

	safe := cfg.SafeString()
	assert.NotContains(t, safe, "supersecret")
	assert.NotContains(t, safe, cfg.SecretKey)
	assert.Contains(t, safe, "db_password=***")
	assert.Contains(t, safe, "secret_key=***")
}
