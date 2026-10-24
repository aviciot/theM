# Multi-Tenant End-to-End Test Guide

**Stack URL:** `http://localhost:8088`  
**Super-admin credentials:** `admin` / `admin123`

---

## Prerequisites

Stack must be running:

```bash
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml up -d
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml ps
```

All containers should show `healthy` or `running`.

---

## Part 1 — Super-Admin: Create a New Tenant

### Step 1.1 — Log in as super-admin

1. Open `http://localhost:8088` in a browser.
2. Log in with `admin` / `admin123`.
3. **Expected:** You land on the platform dashboard. The left nav shows **Admin** section with: Tenants, Users, LLM Providers, Observability.

### Step 1.2 — Open the Tenants list

1. Click **Admin → Tenants** in the left nav.
2. **Expected:** One existing tenant: `default` (ID `00000000-0000-0000-0000-000000000001`).

### Step 1.3 — Provision a new tenant

1. Click **New Tenant** (top-right button).
2. A 4-step wizard opens:
   - **Step 1 — Tenant identity:** Enter slug `acme`, display name `Acme Corp`. Click **Create Tenant →**.
   - **Step 2 — Admin user:** Enter username, display name, and password for the tenant admin (e.g., `acme-admin` / `Acme Admin` / `changeme123`). Click **Create User →** (or Skip).
   - **Step 3 — Quota:** Leave all fields blank (unlimited) or set limits. Click **Save Quota →** (or Skip).
   - **Step 4 — Done:** Green checkmark shown. Click **Done**.
3. **Expected:** Tenant `acme` appears in the list. Clicking it opens the tenant detail panel (General / Identity Provider / Quotas tabs).

### Step 1.4 — Verify tenant isolation (DB check, optional)

```bash
docker exec -it them-postgres psql -U them -d them \
  -c "SELECT id, slug, name FROM them.tenants ORDER BY created_at;"
```

**Expected:** Two rows — `default` and `acme`.

---

### Step 1.5 — Delete a tenant (cleanup / re-run)

When you want to re-run the test from scratch, delete the `acme` tenant:

1. Click the `acme` card to open its detail panel.
2. In the **General** tab, scroll down to **Danger Zone**.
3. Click **Delete Tenant**, then confirm with **Yes, delete**.
4. **Expected:** Tenant disappears from the list. Panel closes.

> **Note:** Delete fails if the tenant still has applications, agents, or users. Remove them first (or skip to re-provision a fresh tenant with a different slug).

---

## Part 2 — Tenant Admin: Configure and Use the Tenant

### Step 2.1 — Log in as the tenant-admin user

1. Log out of the `admin` account (top-right menu → Sign out).
2. Log in with the tenant-admin credentials created in Step 1.3.
3. **Expected:** Dashboard loads. Left nav shows **only** tenant-scoped items: Applications, Agents, Orchestrators, Runs. No Admin section visible.

### Step 2.2 — Create an Application (entry point)

1. Navigate to **Applications**.
2. **Expected:** Empty state — onboarding banner prompts you to create the first application.
3. Click **Create Application**.
4. Fill in:
   - **Name:** `Test App`
   - **Slug:** `test-app`
5. Submit.
6. **Expected:** `Test App` appears in the list. Click into it to see the entry point (WebSocket door).

### Step 2.3 — Configure an LLM provider (if not inherited from platform)

1. Inside `Test App`, go to **Settings → LLM**.
2. Select a provider (e.g., `openai`) and enter an API key.
3. Click **Test** to verify connectivity.
4. **Expected:** Green checkmark / success response.

> **Note:** If the tenant inherits the platform-level LLM config, this step is optional.

### Step 2.4 — Create an Agent

1. Navigate to **Agents**.
2. Click **Create Agent**.
3. Fill in:
   - **Name:** `Echo Agent`
   - **Type:** Select an available transport (e.g., `a2a`, `openai`, etc.)
   - Configure the required fields for the chosen transport.
4. Click **Save**.
5. **Expected:** Agent appears in the list. Status shows as `active` or `configured`.

### Step 2.5 — Create an Orchestrator

1. Navigate to **Orchestrators**.
2. Click **Create Orchestrator**.
3. Fill in:
   - **Name:** `Test Orchestrator`
   - Assign `Echo Agent` as the primary agent.
4. Save.
5. **Expected:** Orchestrator appears, linked to the agent.

### Step 2.6 — Assign orchestrator to the entry point

1. Go to **Applications → Test App**.
2. Find the entry point (WebSocket endpoint).
3. Click **Edit** and assign `Test Orchestrator` as the orchestrator.
4. Save.
5. **Expected:** Entry point shows `Test Orchestrator` as active.

### Step 2.7 — Run a session in the Playground

1. Inside `Test App`, click **Playground** (or open the entry point).
2. Type a message (e.g., `Hello`).
3. Send.
4. **Expected:** The orchestrator processes the message and returns a response. The run appears in **Runs** with status `completed`.

### Step 2.8 — Verify run isolation (DB check, optional)

```bash
docker exec -it them-postgres psql -U them -d them \
  -c "SELECT id, status, application_id FROM them.runs ORDER BY created_at DESC LIMIT 5;"
```

**Expected:** Runs are tied to the `acme` tenant's application ID — not visible to or mixed with `default` tenant runs.

---

## Part 3 — Tenant Isolation Check

### Step 3.1 — Confirm tenant admin cannot see other tenants

While logged in as the `acme` tenant admin:

1. Try navigating to `/admin/tenants/` directly.
2. **Expected:** 403 / redirect to dashboard. Tenant admins cannot access the platform admin section.

### Step 3.2 — Confirm super-admin can see all tenants

Log back in as `admin` and go to **Admin → Tenants**.

**Expected:** Both `default` and `acme` appear.

---

## Part 4 — SSO Login (Optional — requires Keycloak)

Only do this if you have completed the SSO setup from `docs/LOCAL_TEST_ENVIRONMENT_RUNBOOK.md` (Section 9).

### Step 4.1 — Start Keycloak

```bash
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml --profile sso up -d them-keycloak
```

Wait ~20 seconds for Keycloak to start. Check: `http://localhost:8088/auth/keycloak/realms/them/.well-known/openid-configuration` should return JSON.

### Step 4.2 — Configure SSO for the `acme` tenant

1. Log in as the `acme` tenant admin.
2. Go to **Settings → SSO**.
3. Fill in:
   - **Issuer URL:** `http://localhost:8088/auth/keycloak/realms/them`
   - **Client ID:** `them-m`
   - **Client Secret:** `them-m-secret`
4. Save.
5. **Expected:** SSO enabled indicator on the settings page.

### Step 4.3 — Test SSO login

1. Log out of the tenant-admin account.
2. On the login page, click **Sign in with SSO** (or similar) and enter tenant slug `acme`.
3. You are redirected to Keycloak. Log in with `testuser@example.com` / `testpass`.
4. **Expected:** Redirected back to the platform, logged in as the SSO user. Session is scoped to the `acme` tenant.

---

## Quick Sanity Checks

| Check | Command |
|---|---|
| All containers healthy | `docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml ps` |
| Tenant list (DB) | `docker exec -it them-postgres psql -U them -d them -c "SELECT slug, name FROM them.tenants;"` |
| Run count per tenant | `docker exec -it them-postgres psql -U them -d them -c "SELECT a.tenant_id, count(*) FROM them.runs r JOIN them.applications a ON a.id = r.application_id GROUP BY 1;"` |
| Auth-go logs | `docker logs them-auth-go --tail 20` |
| Go bridge logs | `docker logs them-go-bridge --tail 20` |

---

## Known Gaps / Not Yet Testable

- **WebRTC entry points** — not yet wired in the UI.
- **Tenant quota enforcement UI** — quota is stored but the UI warning for over-quota is not yet implemented.
- **SSO group→role mapping** — OIDC group claims are received but role mapping UI is not yet in tenant settings.
