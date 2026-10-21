package events

import (
	"os"
	"strings"
	"testing"
)

// TestValidateEventsMode_ValidModes verifies that valid modes are accepted.
func TestValidateEventsMode_ValidModes(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     EventsMode
	}{
		{"postgres explicit", "postgres", ModePostgres},
		{"dual", "dual", ModeDual},
		{"clickhouse", "clickhouse", ModeClickhouse},
		{"empty defaults to postgres", "", ModePostgres},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("TEST_EVENTS_MODE", tt.envValue)
			defer os.Unsetenv("TEST_EVENTS_MODE")

			got, err := ValidateEventsMode("TEST_EVENTS_MODE")
			if err != nil {
				t.Errorf("ValidateEventsMode() error = %v, want nil", err)
				return
			}
			if got != tt.want {
				t.Errorf("ValidateEventsMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestValidateEventsMode_InvalidModes verifies that invalid modes are rejected.
func TestValidateEventsMode_InvalidModes(t *testing.T) {
	invalidModes := []string{
		"invalid",
		"redis",
		"kafka",
		"POSTGRES", // case sensitive
		"Dual",
	}

	for _, mode := range invalidModes {
		t.Run("rejects_"+mode, func(t *testing.T) {
			os.Setenv("TEST_EVENTS_MODE", mode)
			defer os.Unsetenv("TEST_EVENTS_MODE")

			_, err := ValidateEventsMode("TEST_EVENTS_MODE")
			if err == nil {
				t.Errorf("ValidateEventsMode() expected error for mode %q, got nil", mode)
			}
			if !strings.Contains(err.Error(), "invalid events mode") {
				t.Errorf("ValidateEventsMode() error = %v, want 'invalid events mode'", err)
			}
		})
	}
}

// TestValidateGatewayEventsMode_BlocksClickhouse verifies Gateway-specific restriction.
func TestValidateGatewayEventsMode_BlocksClickhouse(t *testing.T) {
	os.Setenv("TEST_GATEWAY_MODE", "clickhouse")
	defer os.Unsetenv("TEST_GATEWAY_MODE")

	_, err := ValidateGatewayEventsMode("TEST_GATEWAY_MODE")
	if err == nil {
		t.Fatal("ValidateGatewayEventsMode() expected error for clickhouse mode, got nil")
	}
	if !strings.Contains(err.Error(), "not yet supported") {
		t.Errorf("ValidateGatewayEventsMode() error = %v, want 'not yet supported'", err)
	}
	if !strings.Contains(err.Error(), "budget accounting") {
		t.Errorf("ValidateGatewayEventsMode() error = %v, want mention of 'budget accounting'", err)
	}
}

// TestValidateGatewayEventsMode_AllowsPostgresAndDual verifies Gateway allows postgres/dual.
func TestValidateGatewayEventsMode_AllowsPostgresAndDual(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     EventsMode
	}{
		{"postgres", "postgres", ModePostgres},
		{"dual", "dual", ModeDual},
		{"empty defaults to postgres", "", ModePostgres},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("TEST_GATEWAY_MODE", tt.envValue)
			defer os.Unsetenv("TEST_GATEWAY_MODE")

			got, err := ValidateGatewayEventsMode("TEST_GATEWAY_MODE")
			if err != nil {
				t.Errorf("ValidateGatewayEventsMode() error = %v, want nil", err)
				return
			}
			if got != tt.want {
				t.Errorf("ValidateGatewayEventsMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRequireRedisForMode_PostgresNoRedis verifies postgres mode doesn't require Redis.
func TestRequireRedisForMode_PostgresNoRedis(t *testing.T) {
	err := RequireRedisForMode(ModePostgres, "")
	if err != nil {
		t.Errorf("RequireRedisForMode(ModePostgres, empty) = %v, want nil", err)
	}
}

// TestRequireRedisForMode_DualRequiresRedis verifies dual mode requires Redis.
func TestRequireRedisForMode_DualRequiresRedis(t *testing.T) {
	err := RequireRedisForMode(ModeDual, "")
	if err == nil {
		t.Fatal("RequireRedisForMode(ModeDual, empty) expected error, got nil")
	}
	if !strings.Contains(err.Error(), "requires REDIS_EVENTS_HOST") {
		t.Errorf("RequireRedisForMode() error = %v, want 'requires REDIS_EVENTS_HOST'", err)
	}

	// With Redis configured, should succeed
	err = RequireRedisForMode(ModeDual, "them-redis-events:6379")
	if err != nil {
		t.Errorf("RequireRedisForMode(ModeDual, with-host) = %v, want nil", err)
	}
}

// TestRequireRedisForMode_ClickhouseRequiresRedis verifies clickhouse mode requires Redis.
func TestRequireRedisForMode_ClickhouseRequiresRedis(t *testing.T) {
	err := RequireRedisForMode(ModeClickhouse, "")
	if err == nil {
		t.Fatal("RequireRedisForMode(ModeClickhouse, empty) expected error, got nil")
	}
	if !strings.Contains(err.Error(), "requires REDIS_EVENTS_HOST") {
		t.Errorf("RequireRedisForMode() error = %v, want 'requires REDIS_EVENTS_HOST'", err)
	}

	// With Redis configured, should succeed
	err = RequireRedisForMode(ModeClickhouse, "them-redis-events:6379")
	if err != nil {
		t.Errorf("RequireRedisForMode(ModeClickhouse, with-host) = %v, want nil", err)
	}
}
