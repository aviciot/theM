# the-M Documentation

Welcome to **the-M** - a multi-agent orchestration platform.

## Quick Links

- **[Current State](CURRENT.md)** - Latest status, next tasks, blockers
- **[Architecture](INDEX.md)** - Full documentation index
- **[Code Findings Backlog](plans/code-findings-backlog.md)** - Known issues (121 items)

## Getting Started

### For Developers
1. **[Local Dev & Deploy](guides/local-dev-and-deploy.md)** - Setup and run locally
2. **[Database Migrations](guides/database-migrations.md)** - Schema changes
3. **[A2A Test Agents](guides/a2a-test-agents.md)** - Testing agent-to-agent calls

### Architecture Overview
- **[Platform Overview](architecture/platform-overview.md)** - All services, containers, routes
- **[Run Lifecycle](architecture/run-lifecycle.md)** - WS/SSE → admission → Temporal → agents
- **[Multi-tenancy & RLS](architecture/multi-tenancy-rls.md)** - Tenant isolation, database roles

## Key Features

### Multi-Agent System
- **Canvas agents** built visually, served by `them-agent-runtime`
- **A2A protocol** (Agent-to-Agent) with JSON-RPC 2.0
- **MCP integration** for tool execution

### Orchestration
- **Temporal workflows** for reliable execution
- **AppFlow** for visual workflow building
- **LLM Gateway** with governance and rate limiting

### Security & Isolation
- **Row-Level Security (RLS)** for tenant isolation
- **Role-based access** with external JWT support
- **Rate limiting** per token and per user

## Recent Updates

### Security Fixes (2026-10-31)
- ✅ **F-68**: A2A task store tenant isolation - keys now scoped by application ID
- ✅ **F-31**: Gate/session keys tenant isolation - includes application ID

See [Code Findings Backlog](plans/code-findings-backlog.md) for remaining issues.

## Technology Stack

- **Backend**: Go 1.25, Temporal, PostgreSQL, Redis
- **Frontend**: Next.js, React
- **Observability**: ClickHouse (events), Prometheus (metrics)
- **Infrastructure**: Docker Compose, Traefik

## Repository Structure

```
├── docs/              # Documentation (you are here)
│   ├── architecture/  # System design docs
│   ├── guides/       # How-to guides
│   ├── reference/    # Generated references
│   ├── plans/        # Implementation plans
│   └── lessons/      # Lessons learned
├── go/               # Go services
├── frontend/         # Next.js frontend
├── db/               # Database migrations
└── scripts/          # Utility scripts
```

## Contributing

- **Code changes** require doc updates (enforced by pre-commit hook)
- **Tests** must pass before commit (`go test ./...`)
- **Architecture docs** must be updated when changing owned files

See `CLAUDE.md` for full development guidelines.
