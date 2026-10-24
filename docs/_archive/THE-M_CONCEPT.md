# the-M — Concept & Vision
# Last updated: 2026-09-08

---

## What is the-M?

**the-M is an AI agentic operating system** — a multi-tenant platform that lets organizations build, deploy, and operate AI agents at scale.

It is infrastructure, not an application. Think of it the way AWS is infrastructure for web apps — the-M is infrastructure for agentic AI workloads.

---

## Core Capabilities

- **Agent orchestration** — run single agents or multi-agent DAGs (via Temporal)
- **Multi-transport** — agents exposed over WebSocket, SSE, or A2A (agent-to-agent protocol)
- **LLM abstraction** — pluggable providers (Anthropic, OpenAI, etc.) with per-tenant config
- **Run recording** — every agent run is stored: steps, messages, token usage, artifacts
- **MCP support** — agents can call external tools via the Model Context Protocol
- **Rate limiting & quotas** — per-tenant caps on runs, tokens, users, agents
- **Audit trail** — all admin actions and agent runs are logged per tenant
- **RBAC** — super_admin / tenant admin / member / viewer roles

---

## Multi-Tenancy

Each **tenant** is a fully isolated unit:
- Its own agents, applications, entry points, LLM providers
- Its own quota and rate limits
- Its own identity provider (SSO) or local user accounts
- Its own membership and roles

Tenants share the same platform infrastructure (Postgres, Redis, Go services) but are logically separated — one tenant cannot see or affect another.

---

## Identity & SSO

the-M supports both login methods on a single login page:

| Method | How it works |
|---|---|
| **Local** | Username + password stored in the-M |
| **SSO (OIDC)** | User types email → the-M resolves tenant by email domain → redirects to tenant's IdP (Okta, Keycloak, Azure AD, etc.) → issues internal JWT on return |

Each tenant configures its own OIDC IdP (discovery URL, client ID, secret, redirect URI).
Group mappings translate IdP groups to the-M roles automatically on login.

### How We Test SSO Locally

We run **Keycloak** as a local IdP inside Docker (`them-keycloak`), exposed via Traefik at `/auth/keycloak`.

- One Keycloak realm (`them`) serves all test tenants
- Each tenant maps to a Keycloak client (e.g. `them-m`)
- Test users (`avi`, `avi1`, `avi2`, `avi3`, `testuser`) are created in the realm
- The full OIDC flow (start → Keycloak login → callback → JWT) runs exactly as it would in production against a real IdP

This lets us validate the entire SSO flow end-to-end without external dependencies.

---

## The Tenant's Perspective: Internal Team

A tenant like "Bank Corp" has an **internal team** — engineers and admins who:
- Log into the-M UI (via SSO or local account)
- Build and configure AI agents
- Set up entry points (WebSocket/SSE doors into their agents)
- Monitor runs, usage, costs

These are **tenant members** with admin/member roles in the-M.

---

## The Tenant's Perspective: End Users (Customers)

The bank's **retail customers** never touch the-M UI. They use the bank's own application (website, mobile app) which calls the-M under the hood:

```
Customer → Bank App → the-M API (service token) → Agent → Response → Bank App → Customer
```

The bank authenticates to the-M with a **service API token**. The customer authenticates to the bank's app using whatever the bank uses. The-M is invisible to the end user.

---

## The Gap: End-User Visibility

Today the-M sees the bank's service token but not the individual customer behind it. For the-M to be a true AI operating system for tenants, it needs to surface:

- **Who** ran the agent (end-user ID passed by the tenant app)
- **Per-customer usage** — runs, tokens, cost
- **Per-customer rate limiting** — prevent one user flooding the system
- **Per-customer audit trail** — what did this user's agent do
- **Per-customer history** — agent memory across sessions

### Proposed Approach

The tenant app passes an `external_user` context in the session/run payload:

```json
{
  "external_user_id": "customer-alice-123",
  "external_user_role": "premium",
  "metadata": {}
}
```

The-M stores this on the run, enforces per-user rate limits, and exposes per-user analytics to the tenant admin — without the-M ever managing those users' authentication.

This keeps the separation clean:
- **Tenant manages their customers** (auth, identity, roles)
- **the-M tracks and governs their usage** (runs, tokens, limits, audit)

---

## Open Questions

- Should `external_user_id` be opaque (hash) or meaningful (email)?
- Should per-user rate limits be configured by the tenant or enforced by the-M defaults?
- Should end-user context flow into the agent's system prompt automatically?
- How does a tenant expose per-user history to their customers without giving them direct the-M access?
