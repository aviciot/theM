---
title: Home
type: guide
status: current
last_verified: 2026-10-31
---

# the-M Documentation

**the-M** is a multi-agent orchestration platform that lets you build, deploy, and run AI agent workflows.

---

## What is the-M?

Think of the-M as a **platform for building AI applications**:

- 🎨 **Visual Agent Builder** - Draw workflows in a canvas instead of writing code
- 🤖 **Multi-Agent System** - Agents can call other agents (Agent-to-Agent / A2A)
- 🔗 **LLM Gateway** - Unified access to Claude, GPT, and other LLMs
- 🔐 **Multi-Tenant** - Isolated workspaces for different teams/customers
- 📊 **Built-in Observability** - Track costs, usage, and performance

---

## Quick Start

### I want to...

**Build an AI agent workflow**
→ Read [App Canvas / AppFlow](architecture/app-canvas-appflow.md) - Visual workflow builder

**Run the platform locally**
→ Read [Local Dev & Deploy](guides/local-dev-and-deploy.md) - Docker setup guide

**Understand the architecture**
→ Read [Platform Overview](architecture/platform-overview.md) - All services explained

**See what's broken**
→ Read [Code Findings Backlog](plans/code-findings-backlog.md) - 121 known issues

**Check current status**
→ Read [Current State](CURRENT.md) - Latest work, next tasks

---

## Core Concepts

### 1. **Applications & Entry Points**
- **Application** = One AI workflow/product (e.g., "Customer Support Bot")
- **Entry Point** = How users access it (WebSocket, SSE, API, Voice)

### 2. **Agents**
Three types:
- **Canvas Agents** - Built visually, no code required
- **Orchestrator Agents** - Traditional LLM + tools loop
- **External Agents** - Call via A2A protocol (Agent-to-Agent)

### 3. **AppFlow (Visual Workflows)**
Drag-and-drop nodes:
- **LLM** - Call Claude/GPT
- **Condition** - If/else branching
- **HTTP** - Call external APIs
- **MCP Tool** - Execute tools (file ops, database, etc.)
- **Human-in-Loop** - Wait for human approval
- **Agent Call** - Call another agent

### 4. **Multi-Tenancy**
Every resource is isolated by tenant:
- ✅ Row-Level Security (RLS) enforced in database
- ✅ Separate Redis keys per application
- ✅ Rate limits per tenant
- ✅ Cost tracking per tenant

---

## How It Works (Simple Flow)

```
User → WebSocket/API → Authentication → Rate Limit Check
  ↓
Entry Point Config Loaded
  ↓
Workflow Starts (Temporal/Local)
  ↓
Agent Executes (LLM calls, tool use, sub-agents)
  ↓
Events Stream Back → User sees results
```

**Real example:**
1. User sends "Book me a flight to NYC"
2. Entry point authenticates, checks quota
3. Workflow starts: LLM node → understands intent
4. Condition node → checks if dates provided
5. HTTP node → calls airline API
6. Human-in-Loop → waits for approval
7. Response sent back to user

---

## Key Features

### 🔒 **Security & Governance**
- **PII Detection** - Scan for credit cards, SSNs
- **Prompt Injection Guards** - Block jailbreak attempts
- **File Scanning** - ClamAV antivirus for uploaded files
- **Rate Limiting** - Per token, per user, per tenant

### 🎯 **Production Ready**
- **Temporal Workflows** - Reliable execution, survives crashes
- **Event Streaming** - Real-time updates via WebSocket/SSE
- **Cost Tracking** - LLM usage per request
- **Observability** - ClickHouse events, Prometheus metrics

### 🔌 **Extensible**
- **MCP (Model Context Protocol)** - Add tools via MCP servers
- **A2A Protocol** - Agents can call each other
- **Custom LLM Providers** - Bring your own API keys

---

## Documentation Structure

| Section | What's Inside |
|---------|--------------|
| **[Architecture](INDEX.md#architecture)** | How the system works (15 docs) |
| **[Guides](INDEX.md#guides)** | Step-by-step how-tos (4 docs) |
| **[Reference](INDEX.md#reference)** | Generated API/schema docs |
| **[Plans](INDEX.md#plans)** | Implementation plans, backlog |
| **[Lessons](INDEX.md#lessons)** | Lessons learned per area |

---

## Common Questions

**Q: Is this production-ready?**  
A: Partially. Core features work, but [121 known issues](plans/code-findings-backlog.md) exist (including security gaps).

**Q: What LLMs does it support?**  
A: Claude (Anthropic), GPT (OpenAI), and any OpenAI-compatible API.

**Q: Can I self-host?**  
A: Yes. Docker Compose setup in [Local Dev & Deploy](guides/local-dev-and-deploy.md).

**Q: How do I add a new agent?**  
A: Use the Agent Builder UI (canvas) or call A2A agents via HTTP.

**Q: Where's the API documentation?**  
A: See [Routes Reference](reference/routes-generated.md) (auto-generated from code).

---

## Technology Stack

**Backend:**
- Go 1.25 (main services)
- Temporal (workflow orchestration)
- PostgreSQL (primary database)
- Redis (sessions, rate limits, queues)

**Frontend:**
- Next.js + React
- Agent Canvas (visual builder)

**Infrastructure:**
- Docker Compose
- Traefik (reverse proxy)
- ClickHouse (events)
- MinIO (file storage)

---

## Recent Updates

**2026-10-31:** Security fixes
- ✅ F-68: A2A task store now isolated by application
- ✅ F-31: Gate/session keys scoped by tenant

**2026-10-25:** Documentation reboot
- 15 architecture docs written
- Pre-commit hook enforces doc updates
- Generated references (schema, routes, Redis keys)

See [Current State](CURRENT.md) for latest.

---

## Need Help?

- **Bug/Question:** Check [Code Findings](plans/code-findings-backlog.md) first
- **Architecture question:** See [Architecture docs](INDEX.md#architecture)
- **Setup issue:** See [Local Dev Guide](guides/local-dev-and-deploy.md)
- **GitHub:** [aviciot/theM](https://github.com/aviciot/theM)

---

## Contributing

1. Read `CLAUDE.md` in repo root (development rules)
2. Code changes **require** doc updates (enforced by git hook)
3. Tests must pass: `go test ./...`
4. Every doc lists the code it `owns:` - update both together
