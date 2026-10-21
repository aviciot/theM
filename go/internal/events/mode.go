package events

import (
	"fmt"
	"os"
)

// EventsMode represents the valid event logging modes.
type EventsMode string

const (
	// ModePostgres writes events to Postgres only (default, fully supported).
	ModePostgres EventsMode = "postgres"

	// ModeDual writes events to both Postgres and ClickHouse (migration/verification mode).
	// Postgres remains authoritative; ClickHouse mirror failures are logged but don't block requests.
	ModeDual EventsMode = "dual"

	// ModeClickhouse writes events to ClickHouse only (BLOCKED until Phase 2 complete).
	// Missing features: budget accounting queries, request body storage.
	ModeClickhouse EventsMode = "clickhouse"
)

// ValidateEventsMode validates the events mode from environment variable.
// Returns the normalized mode or an error if invalid/unsupported.
//
// Safety rules:
// 1. Unknown values are rejected (prevents silent misconfiguration)
// 2. "clickhouse" is allowed by default (Auth/Tools have no PG tables yet)
// 3. Empty defaults to "postgres" (safe default)
//
// For Gateway-specific validation (blocks clickhouse until budget/body support),
// use ValidateGatewayEventsMode instead.
func ValidateEventsMode(envVar string) (EventsMode, error) {
	mode := os.Getenv(envVar)

	// Empty defaults to postgres (safe, fully supported)
	if mode == "" {
		return ModePostgres, nil
	}

	switch EventsMode(mode) {
	case ModePostgres:
		return ModePostgres, nil

	case ModeDual:
		return ModeDual, nil

	case ModeClickhouse:
		return ModeClickhouse, nil

	default:
		return "", fmt.Errorf(
			"invalid events mode %q in %s: must be 'postgres' (default), 'dual', 'clickhouse', or empty",
			mode, envVar)
	}
}

// ValidateGatewayEventsMode validates events mode specifically for the Gateway service.
// Blocks clickhouse-only mode until budget accounting queries and request body storage
// are implemented. Auth and Tools services should use ValidateEventsMode instead.
func ValidateGatewayEventsMode(envVar string) (EventsMode, error) {
	mode := os.Getenv(envVar)

	// Empty defaults to postgres (safe, fully supported)
	if mode == "" {
		return ModePostgres, nil
	}

	switch EventsMode(mode) {
	case ModePostgres:
		return ModePostgres, nil

	case ModeDual:
		return ModeDual, nil

	case ModeClickhouse:
		// Block clickhouse-only mode until Phase 2 features are complete
		return "", fmt.Errorf(
			"gateway events mode 'clickhouse' is not yet supported: missing budget accounting queries and request body storage. "+
			"Use 'postgres' (default) or 'dual' (migration verification mode) instead. "+
			"Set %s=postgres or %s=dual", envVar, envVar)

	default:
		return "", fmt.Errorf(
			"invalid events mode %q in %s: must be 'postgres' (default), 'dual', or empty. "+
			"'clickhouse' is blocked for gateway until Phase 2 complete", mode, envVar)
	}
}

// RequireRedisForMode checks if the given mode requires Redis Events to be configured.
// Returns an error if Redis is required but redisHost is empty.
func RequireRedisForMode(mode EventsMode, redisHost string) error {
	if mode == ModeDual || mode == ModeClickhouse {
		if redisHost == "" {
			return fmt.Errorf(
				"events mode %q requires REDIS_EVENTS_HOST to be set for event queue", mode)
		}
	}
	return nil
}
