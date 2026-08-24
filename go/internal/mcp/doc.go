// Package mcp implements the them-mcp-service: a standalone service that
// manages MCP (Model Context Protocol) server lifecycle for the-M platform.
//
// # File responsibilities — do not merge these concerns
//
//   - config.go     Config struct and LoadConfig; env parsing only; no I/O
//   - dal.go        DB access for them.mcp_servers + them.app_mcp_credentials only;
//                   all SQL lives here, never in callers
//   - client.go     Stateless MCP JSON-RPC HTTP client (initialize / tools/list / tools/call);
//                   no DB, no Redis, no state
//   - registry.go   In-process server cache + Redis manifest/health keys + pub/sub;
//                   cache logic only; no MCP protocol
//   - leader.go     Redis SET NX leader election; isolated — no health or MCP logic
//   - supervisor.go Supervisor: reconciles DB server list against running workers every 30s;
//                   spawns a worker goroutine per server, stops workers for removed servers
//   - health.go     worker: per-server goroutine with own ticker, exponential backoff,
//                   panic recovery, and independent health state machine
//   - executor.go   POST /internal/execute: credential resolution + MCP tool dispatch;
//                   split credential logic into resolver.go when OAuth2 is added
//   - server.go     chi router wiring and HTTP handler shims only; no business logic
//
// # Scaling model
//
// Multiple replicas may run simultaneously. Only the replica holding the Redis
// leader lock (them:mcp:leader, SET NX PX 30s) runs the Supervisor. The
// Supervisor owns one goroutine per MCP server — each with its own probe
// ticker and exponential backoff. Non-leader replicas are pure stateless HTTP
// servers handling POST /internal/execute.
//
// # Per-server independence
//
// Each MCP server is managed entirely independently:
//   - own goroutine — a slow/unreachable server never blocks others
//   - own ticker — probe intervals are not shared
//   - own backoff — unreachable servers back off to 10 min; healthy servers probe at base interval
//   - own panic recovery — a panic in one worker does not affect others
//   - dynamic lifecycle — new servers are discovered and started within 30s;
//     deleted/disabled servers are stopped within 30s
//
// # Security invariants
//
//   - Credentials are never logged, never returned in API responses
//   - Tenant isolation enforced in executor.go: mcp_server.tenant_id must equal
//     the calling application's tenant_id before any credential lookup
//   - Test credentials (canvas session only) are used for one request and discarded
package mcp
