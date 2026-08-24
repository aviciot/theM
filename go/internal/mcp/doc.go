// Package mcp implements the them-mcp-service: a standalone service that
// manages MCP (Model Context Protocol) server lifecycle for the-M platform.
//
// # File responsibilities — do not merge these concerns
//
//   - config.go    Config struct and LoadConfig; env parsing only; no I/O
//   - dal.go       DB access for them.mcp_servers and them.app_mcp_credentials only;
//                  no business logic; SQL stays here, never in callers
//   - client.go    MCP JSON-RPC HTTP client (initialize / tools/list / tools/call);
//                  stateless; no DB or Redis access
//   - registry.go  In-process server cache + Redis manifest/health keys + pub/sub;
//                  cache logic only; no MCP protocol
//   - leader.go    Redis SET NX leader election; no health or MCP logic
//   - health.go    Background health+discovery loop; orchestrates client + dal + registry;
//                  split into a new file if probe logic exceeds ~200 lines
//   - executor.go  POST /internal/execute handler logic: credential resolution and
//                  tool dispatch; when OAuth2 is added, split resolver into resolver.go
//   - server.go    chi router wiring and HTTP handler shims only; no business logic
//
// # Scaling model
//
// Multiple replicas may run simultaneously. Only the replica that holds the
// Redis leader lock (them:mcp:leader, SET NX PX 30s) executes the health loop.
// All replicas handle POST /internal/execute as stateless HTTP servers.
//
// # Security invariants
//
//   - Credentials are never logged, never returned in API responses
//   - Tenant isolation is enforced in executor.go before any credential lookup:
//     mcp_server.tenant_id must equal the application's tenant_id
//   - Test credentials (canvas session only) are used for one request and discarded
package mcp
