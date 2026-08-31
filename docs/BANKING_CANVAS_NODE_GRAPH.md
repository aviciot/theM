# Banking Canvas Agent — Exact Node Graph
# Created: 2026-08-26

---

## Overview

One canvas agent. One skill. 16 nodes.
Handles: balance inquiry, transfer request, fraud detection, compliance check,
auto-approve, decline, and escalation path — all in a single visible pipeline.

---

## Node Graph

```
[input]
   │
   ▼
[parse-intent]          LLM — extract: intent, amount, destination, account_id
   │
   ▼
[extract-intent]        transform — json_path → intent, amount, destination_account, source_account
   │
   ▼
[route-intent]          branch — {{eq .intent "transfer"}} 
   │                            true  → [check-account]
   │                            false → [handle-inquiry]
   │
   ├──────────────────────────────────────────────────┐
   ▼                                                  ▼
[check-account]                              [handle-inquiry]
HTTP → GET /mock/banking/account/{{.source_account}}   LLM — answer balance/statement question
   │                                                  │
   ▼                                                  ▼
[extract-account]                            [inquiry-response]
transform → balance, daily_limit,            response → final answer
            daily_used, status
   │
   ▼
[check-balance]         branch — {{ge (float .balance) (float .amount)}}
   │                            true  → [check-compliance]
   │                            false → [decline-insufficient]
   │
   ├──────────────────────────────────────────────────┐
   ▼                                                  ▼
[check-compliance]                        [decline-insufficient]
HTTP → POST /mock/banking/compliance       LLM — friendly decline message
body: {amount, destination_account}                   │
   │                                                  ▼
   ▼                                       [decline-response]
[extract-compliance]                       response → "Insufficient funds..."
transform → compliant(bool), reason, daily_remaining
   │
   ▼
[route-compliance]      branch — {{.compliant}}
   │                            true  → [score-fraud]
   │                            false → [decline-compliance]
   │
   ├──────────────────────────────────────────────────┐
   ▼                                                  ▼
[score-fraud]                             [decline-compliance]
LLM — given amount, destination,          LLM — friendly decline message
      account history → output JSON:                  │
      {score: 0-100, level: LOW/MED/HIGH, ▼
       reasons: [...]}           [compliance-response]
   │                             response → "Exceeds daily limit..."
   ▼
[extract-fraud]
transform → fraud_score, fraud_level, fraud_reasons
   │
   ▼
[route-fraud]           branch — {{lt (int .fraud_score) 70}}
   │                            true  → [approve-transfer]
   │                            false → [escalate-transfer]
   │
   ├──────────────────────────────────────────────────┐
   ▼                                                  ▼
[approve-transfer]                        [escalate-transfer]
HTTP → POST /mock/banking/execute          LLM — compose escalation message
body: {source, destination, amount}        explaining why transfer is under review
   │                                                  │
   ▼                                                  ▼
[compose-approval]                         [escalation-response]
LLM — friendly confirmation with           response → "Transfer flagged for review..."
      confirmation number
   │
   ▼
[approval-response]
response → "Transfer of $X processed. Ref #TXN-XXXX"
```

---

## Node List (16 nodes)

| # | ID | Type | Purpose |
|---|---|---|---|
| 1 | `input` | input | Receive user message → `user_request` |
| 2 | `parse-intent` | llm | Extract intent + transfer details as JSON |
| 3 | `extract-intent` | transform | json_path → `intent`, `amount`, `source_account`, `destination_account` |
| 4 | `route-intent` | branch | `{{eq .intent "transfer"}}` → check-account / handle-inquiry |
| 5 | `handle-inquiry` | llm | Answer balance/statement questions conversationally |
| 6 | `inquiry-response` | response | Return inquiry answer |
| 7 | `check-account` | http | `GET /mock/banking/account/{{.source_account}}` |
| 8 | `extract-account` | transform | json_path → `balance`, `daily_limit`, `daily_used`, `status` |
| 9 | `check-balance` | branch | `{{ge (float .balance) (float .amount)}}` → check-compliance / decline-insufficient |
| 10 | `check-compliance` | http | `POST /mock/banking/compliance` → compliant, reason, daily_remaining |
| 11 | `extract-compliance` | transform | json_path → `compliant`, `reason`, `daily_remaining` |
| 12 | `route-compliance` | branch | `{{.compliant}}` → score-fraud / decline-compliance |
| 13 | `score-fraud` | llm | Fraud risk analysis → JSON `{score, level, reasons}` |
| 14 | `extract-fraud` | transform | json_path → `fraud_score`, `fraud_level`, `fraud_reasons` |
| 15 | `route-fraud` | branch | `{{lt (int .fraud_score) 70}}` → approve / escalate |
| 16 | `approve-transfer` | http | `POST /mock/banking/execute` → confirmation number |
| 17 | `compose-approval` | llm | Friendly confirmation message |
| 18 | `approval-response` | response | Final approval message |
| 19 | `decline-insufficient` | llm | Friendly insufficient funds message |
| 20 | `decline-response` | response | Decline output |
| 21 | `decline-compliance` | llm | Friendly limit exceeded message |
| 22 | `compliance-response` | response | Compliance decline output |
| 23 | `escalate-transfer` | llm | Explain transfer is under human review |
| 24 | `escalation-response` | response | Escalation output |

Total: **24 nodes** (3 branch, 3 http, 5 llm, 4 transform, 5 response, 1 input, 1 inquiry-llm + 1 inquiry-response already counted)

---

## Mock Banking API (3 endpoints)

Routes added to `them-go-bridge` — no new container needed.

### GET /mock/banking/account/:id
```json
{
  "account_id": "ACC-001",
  "owner": "Sarah Cohen",
  "status": "active",
  "balance": 12450.00,
  "daily_limit": 10000.00,
  "daily_used": 2300.00,
  "currency": "USD",
  "recent_transfers": [
    {"amount": 500, "destination": "ACC-UK-001", "days_ago": 3},
    {"amount": 1800, "destination": "ACC-US-002", "days_ago": 7}
  ]
}
```

### POST /mock/banking/compliance
Request: `{"amount": 8000, "destination_account": "RU71..."}`
```json
{
  "compliant": false,
  "reason": "Exceeds remaining daily limit",
  "daily_remaining": 7700.00,
  "destination_blocked": false
}
```

### POST /mock/banking/execute
Request: `{"source": "ACC-001", "destination": "ACC-UK-001", "amount": 500}`
```json
{
  "status": "executed",
  "confirmation": "TXN-88214",
  "timestamp": "2026-08-26T12:00:00Z"
}
```

---

## Seed Accounts (hardcoded)

| Account ID | Owner | Balance | Daily Limit | Risk Profile |
|---|---|---|---|---|
| ACC-001 | Sarah Cohen | $12,450 | $10,000 | Normal |
| ACC-002 | David Levy | $3,200 | $5,000 | Normal |
| ACC-003 | Test User | $50,000 | $25,000 | High-value |

---

## Three Demo Scenarios

### Scenario 1 — Auto-approve (low risk)
> "Transfer $500 to ACC-UK-001"

- Account: ✓ balance OK
- Compliance: ✓ within limit
- Fraud score: ~20 (small amount, familiar destination)
- **Result:** approved instantly, confirmation number returned

### Scenario 2 — Compliance decline
> "Transfer $9,000 to ACC-US-002"

- Account: ✓ balance OK
- Compliance: ✗ only $7,700 remaining today
- **Result:** declined immediately at compliance node — fraud check never runs

### Scenario 3 — Fraud escalation
> "Transfer $8,000 to RU71 0401 0645 0000 2320 0000"

- Account: ✓ balance OK
- Compliance: ✓ within limit (just)
- Fraud score: ~85 (large amount, unknown foreign account)
- **Result:** escalation message — "under review by our security team"

---

## What the Canvas Visualization Shows

Each node lights up as it executes. The audience sees:

1. Intent parsed → routed to transfer path
2. Account checked → green
3. Balance verified → green  
4. Compliance checked → green (scenario 1) or red (scenario 2)
5. Fraud scored → LLM reasoning visible
6. Routed to approve or escalate
7. Transfer executed → confirmation returned

**This is the demo.** The graph IS the product.

---

## Build Order

1. Mock banking API routes on `them-go-bridge` (~50 lines Go)
2. Canvas agent JSON definition (all 24 nodes)
3. Publish to `agent_runtime_specs`
4. Wire up as app with entry point
5. Test all 3 scenarios end-to-end
