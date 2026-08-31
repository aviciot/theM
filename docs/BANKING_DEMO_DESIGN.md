# Banking Demo — Design Document
# Smart Transfer Assistant on the-M
# Created: 2026-08-26

---

## Goal

Build a compelling, realistic banking demo that showcases the-M's core strengths:
multi-agent orchestration, deterministic canvas sub-agents, conditional routing,
audit trail, and human-in-the-loop approval — all within a single coherent user flow.

---

## Scenario: "Smart Transfer Assistant"

A bank customer opens a chat and asks to make a transfer. The system:

1. Understands the natural language request
2. Verifies account status and available balance
3. Scores the transaction for fraud risk
4. Runs compliance rules (limits, blocked destinations)
5. Either auto-approves, declines, or escalates to a human reviewer
6. Returns a clear, friendly explanation to the customer

This maps directly to what the-M was built for — and nothing else on the market
does steps 2–5 with full audit trail, tenant isolation, and HITL out of the box.

---

## Architecture

```
User (WebSocket)
     │
     ▼
Go Orchestrator (agentic loop — existing)
     │
     ├── [Tool 1] Account Checker canvas agent
     │     └── HTTP → mock banking API (balance, status, limits)
     │
     ├── [Tool 2] Fraud Scorer canvas agent
     │     └── LLM + rules → risk score 0–100 + reasons
     │
     ├── [Tool 3] Compliance Checker canvas agent
     │     └── HTTP → rules API (daily limit, blocked countries, AML)
     │
     ├── [HITL gate — Temporal signal]  ← triggered when fraud score > 70
     │     └── Human reviewer approves or rejects via admin UI
     │
     └── [Tool 4] Response Composer
           └── LLM → friendly explanation to customer
```

**Two layers:**
- **Outer**: conversational orchestrator (existing Go WS handler + Temporal)
- **Inner**: 3 canvas agents as registered A2A tool agents — deterministic,
  auditable, independently testable

---

## Canvas Agents (Inner Layer)

### Agent 1: Account Checker

**Purpose:** Verify account exists, is active, has sufficient balance for the transfer.

**Steps:**
```
input → http(account-lookup) → transform(extract-fields) →
http(balance-check) → transform(build-summary) → response
```

**Mock API:** Simple Go HTTP endpoint (`/mock/banking/account/:id`) returning
realistic fake JSON — account status, balance, daily limit used.

**Output (pipeline var `account_summary`):**
```json
{
  "account_id": "ACC-001",
  "status": "active",
  "balance": 12450.00,
  "daily_limit": 10000.00,
  "daily_used": 2300.00,
  "currency": "USD"
}
```

**Uses today:** `input`, `http`, `transform`, `response` — all fully implemented.

---

### Agent 2: Fraud Scorer

**Purpose:** Score a transaction 0–100 for fraud risk based on amount, destination,
time of day, and account history pattern.

**Steps:**
```
input → transform(parse-transaction) → llm(score-transaction) →
transform(extract-score) → branch(high-risk?) →
  [true]  response(escalate)
  [false] response(approve)
```

**LLM prompt** gives the model a rubric and transaction details; it returns a
structured JSON score + top 3 risk reasons.

**Output (`fraud_result`):**
```json
{
  "score": 82,
  "level": "HIGH",
  "reasons": ["Unusual destination country", "Amount 4x daily average", "After hours"]
}
```

**Uses today:** `input`, `transform`, `llm`, `branch`, `response` — all implemented.

---

### Agent 3: Compliance Checker

**Purpose:** Apply hard rules — daily transfer limit, blocked destination list,
AML pattern check. Binary pass/fail with reason.

**Steps:**
```
input → http(check-limits) → transform(extract-result) →
branch(compliant?) →
  [true]  response(pass)
  [false] response(fail + reason)
```

**Mock API:** `/mock/banking/compliance` — deterministic rules, no LLM needed.
This is the purest canvas agent showcase: structured input → rules → structured output.

**Output (`compliance_result`):**
```json
{
  "pass": true,
  "flags": [],
  "daily_remaining": 7700.00
}
```

**Uses today:** `input`, `http`, `transform`, `branch`, `response` — all implemented.

---

## Orchestrator Flow (Outer Layer)

The main orchestrator uses the existing Go agentic loop. Pseudoflow:

```
1. Parse user intent (LLM) → extract: amount, destination, account_id

2. Call Account Checker agent → if insufficient balance → decline immediately

3. Call Compliance Checker agent → if fail → decline with specific reason

4. Call Fraud Scorer agent → get score

5. if score < 40  → auto-approve → call execution mock → confirm to user
   if score 40–70 → approve with note ("we flagged this for review but approved")
   if score > 70  → HITL gate (Temporal signal) → human reviews in admin UI
                     → human approves → execute
                     → human rejects  → decline with reason

6. Response Composer (LLM) → friendly, empathetic message to customer
```

**HITL** is the existing Temporal signal mechanism — already working. No new
canvas nodes needed. The `human_wait` canvas step is NOT used here; HITL happens
at the orchestrator level, which is the correct architectural layer for it.

---

## New Nodes Needed (Canvas)

| Node | Status | Needed for this demo? |
|---|---|---|
| `parallel` | Stub | Nice-to-have: run Account + Fraud + Compliance in parallel → saves ~3s |
| `human_wait` | Stub | Not needed — HITL at orchestrator level is better |
| `loop` | Stub | Not needed |
| `a2a_call` | Stub | Not needed — orchestrator calls agents via registry |
| `stream_out` | Stub | Not needed for v1 |
| `mcp_call` | Stub | Not needed for v1 |

**Conclusion:** the demo works 100% with today's implemented nodes. `parallel` is
the one node worth implementing to make the demo feel fast — running all 3 checks
concurrently reduces latency from ~9s to ~3s. This is also a strong visual showcase.

---

## Mock Banking API

A small Go HTTP service (or a few extra routes on `them-go-bridge`) that returns
realistic fake data. No real banking integration needed.

Endpoints:
```
GET  /mock/banking/account/{id}          → account details
GET  /mock/banking/balance/{id}          → current balance
POST /mock/banking/compliance            → compliance check
POST /mock/banking/execute-transfer      → record mock transfer
```

Alternatively: skip the mock API entirely and put all "data" in the LLM prompt
context. Simpler to build, slightly less impressive technically.

**Recommendation:** implement the mock API — it shows that canvas HTTP steps call
real endpoints, which is the whole point.

---

## Demo Script (What the User Sees)

**Happy path:**
> "Transfer $500 to account GB29NWBK60161331926819"

→ Account check: ✓ balance sufficient
→ Compliance: ✓ within limits
→ Fraud score: 22 (low risk — familiar amount, domestic destination)
→ Auto-approved
→ "Your transfer of $500 has been processed. Confirmation #TXN-8821."

**HITL path:**
> "Transfer $8,000 to account RU71 0401 0645 0000 2320 0000"

→ Account check: ✓ balance OK
→ Compliance: ✓ (just under daily limit)
→ Fraud score: 88 (HIGH — unusual destination, large amount)
→ Escalated to human reviewer
→ Admin UI shows pending approval with fraud reasons
→ Human approves/rejects → customer gets notified

**Decline path:**
> "Transfer $15,000 to John"

→ Compliance: ✗ exceeds daily limit ($10,000)
→ Immediately declined — no fraud check needed
→ "I can't process this transfer — it exceeds your $10,000 daily limit.
   Your remaining limit today is $7,700."

---

## What This Demonstrates

| the-M Capability | Where it shows |
|---|---|
| Multi-agent orchestration | 3 canvas agents called in sequence (or parallel) |
| Canvas agents as tools | Account, Fraud, Compliance as standalone auditable agents |
| Conditional routing | Branch nodes inside canvas agents |
| HITL | High-risk transfers stopped for human review |
| Audit trail | Every agent call, LLM response, decision logged to `run_steps` |
| Tenant isolation | Each bank "customer" is a separate application/session |
| Real LLM reasoning | Fraud scorer uses Claude to reason about risk, not hard rules |
| Structured + conversational | Mix of deterministic canvas checks + conversational orchestrator |

---

## Build Order

**Phase 1 — Core pipeline (buildable today):**
1. Mock banking API (3 endpoints, ~100 lines Go)
2. Account Checker canvas agent
3. Compliance Checker canvas agent
4. Fraud Scorer canvas agent
5. Orchestrator prompt + tool registration
6. Demo script + seed data

**Phase 2 — Polish:**
7. Implement `parallel` step executor → run 3 agents concurrently
8. Admin UI HITL review panel (already partially exists)
9. Clean up response formatting

**Phase 1 is a complete demo. Phase 2 makes it impressive.**

---

## Open Questions

- Should the mock banking API be a new container or routes on `them-go-bridge`?
  → Routes on `them-go-bridge` is faster, avoids a new Dockerfile
- Seed accounts: hardcode 3–5 test accounts with different risk profiles
- Fraud scorer: use Claude Haiku (fast, cheap) or Sonnet (more nuanced)?
  → Haiku for scoring, Sonnet for final response composition
