# the-M Test Runner

A load and smoke test UI for the-M applications.
Simulates real end-users hitting a live app entry point in parallel and reports pass/fail per user in real time.

**UI:** `http://localhost:3210`
**Backend API:** `http://localhost:8093`

---

## Architecture

Two containers work together:

| Container | Role |
|---|---|
| `them-test-runner-frontend` | Next.js UI — browse, create, edit, run scenarios and view history |
| `them-test-runner-backend` | Go service — executes scenarios against the live the-M stack, streams results via SSE |

Scenarios and run history are persisted as JSON files in `test-runner/data/`.

---

## Concepts

### Scenario
A scenario defines one test configuration. Fields:

| Field | Description |
|---|---|
| `name` | Human label |
| `tenant_slug` | Target tenant (e.g. `payops_ai`) |
| `tenant_id` | UUID of the tenant (used for token creation) |
| `app_id` | UUID of the application |
| `app_slug` | Slug of the application |
| `ep_slug` | Entry point slug to hit |
| `ep_type` | `websocket` or `a2a` — determines the protocol used |
| `auth_mode` | How virtual users authenticate (see below) |
| `n_users` | Number of parallel virtual users |
| `messages` | Ordered list of messages each user sends |

### Entry point types

**`websocket`** — each virtual user opens a persistent WS connection to:
```
/{tenant_slug}/apps/{app_slug}/{ep_slug}/ws
```
Sends each message as `{"type":"message","content":"..."}` and waits for a `run.complete` event before moving to the next message.

**`a2a`** — each virtual user sends one HTTP POST per message to:
```
/{tenant_slug}/a2a/{app_slug}/{ep_slug}
```
Using the A2A JSON-RPC 2.0 `SendMessage` wire format (`returnImmediately: false`). No persistent connection.

### Auth modes

| Mode | How it works |
|---|---|
| `token` | Runner creates a temporary the-M access token per virtual user via the admin API, uses it as the bearer token, deletes it after the run |
| `external_jwt` | Runner fetches a JWT from Keycloak per virtual user using the resource-owner password grant; `keycloak_users` list cycles if there are fewer users than `n_users` |
| `public` | No auth header sent |

For `token` mode, `auth_user` / `auth_pass` are the the-M admin credentials used to create tokens. If left empty, the global config credentials are used.

---

## Running a Scenario

1. Open `http://localhost:3210`
2. Go to **Scenarios** — you'll see all saved scenarios
3. Click **▶ Run** on a scenario
4. The run page opens and streams live results per virtual user as they complete
5. Final summary (passed / failed / duration per user) is shown when all users finish
6. Every run is saved to **History** automatically

---

## Creating a Scenario

1. Click **+ New Scenario**
2. The UI lets you pick tenant → app → entry point from live dropdowns (pulled from the-M admin API)
3. Fill in `n_users` and the message list
4. Choose auth mode
5. Save — the scenario is stored as a JSON file in `test-runner/data/scenarios/`

---

## Configuration

Global config is at `test-runner/data/config.json`:

```json
{
  "them_url": "http://them-traefik:8088",
  "admin_user": "admin",
  "admin_pass": "admin123"
}
```

Editable from the UI at **Config**. Environment variables (`THEM_URL`, `THEM_ADMIN_USER`, `THEM_ADMIN_PASS`) take precedence over the file on startup, then the file overrides them for subsequent saves.

---

## Data Layout

```
test-runner/data/
  config.json          # global the-M connection config
  scenarios/
    <id>.json          # one file per saved scenario
  history/
    <run-id>.json      # one file per completed run (full results)
```

---

## Backend API (for reference / automation)

| Method | Path | Description |
|---|---|---|
| `GET` | `/config` | Get current config |
| `PUT` | `/config` | Update config |
| `POST` | `/config/test` | Test connection to the-M |
| `GET` | `/tenants` | List tenants (proxied from the-M) |
| `GET` | `/tenants/{slug}/apps` | List apps for a tenant |
| `GET` | `/apps/{id}/eps` | List entry points for an app |
| `GET` | `/scenarios` | List all scenarios |
| `POST` | `/scenarios` | Create a scenario |
| `PUT` | `/scenarios/{id}` | Update a scenario |
| `DELETE` | `/scenarios/{id}` | Delete a scenario |
| `POST` | `/run` | Start a run, returns `{"run_id":"..."}` |
| `GET` | `/run/{runId}/stream` | SSE stream of run events |
| `DELETE` | `/run/{runId}` | Cancel an active run |
| `GET` | `/history` | List all past runs (no per-user detail) |
| `GET` | `/history/{runId}` | Get full run result with per-user detail |
| `DELETE` | `/history/{runId}` | Delete a history entry |

The Next.js frontend proxies all backend calls through `/api/` — so from the browser the paths are `/api/scenarios`, `/api/run`, etc.

---

## Result Structure

Each run produces a summary:

```
RunSummary
  run_id, scenario_id, scenario_name
  n_users, passed, failed
  started_at, ended_at
  results[]        ← one UserResult per virtual user
    user_index
    connected        ← did the user connect / reach the EP?
    status           ← "passed" | "failed"
    duration_ms
    error            ← top-level error (connect failure, auth failure)
    steps[]          ← one StepResult per message
      sent
      received
      latency_ms
      ok
      error
```

A user is **passed** only if all steps succeeded and the user connected. A run is summarised as `N passed / M failed` across all virtual users.

---

## Adding a New Scenario (JSON directly)

Drop a `.json` file into `test-runner/data/scenarios/` and restart (or just use the UI — no restart needed):

```json
{
  "id": "my-scenario",
  "name": "My App — basic smoke",
  "tenant_slug": "my_tenant",
  "tenant_id": "...",
  "app_id": "...",
  "app_slug": "my-app-slug",
  "ep_slug": "ws",
  "ep_type": "websocket",
  "auth_mode": "token",
  "auth_user": "admin",
  "auth_pass": "admin123",
  "n_users": 3,
  "messages": ["hello", "what can you do?"]
}
```
