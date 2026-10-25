---
title: Frontend (Next.js UI and its proxy layer)
type: architecture
status: current
owns:
  - frontend/next.config.ts
  - frontend/Dockerfile
  - frontend/src/app/api/
  - frontend/src/lib/apiClient.ts
  - frontend/src/lib/api.ts
  - frontend/src/stores/authStore.ts
  - frontend/src/components/AuthGuard.tsx
last_verified: 2026-10-25   # code read; compose env checked; UI not clicked through
---

# Frontend

## Purpose
`them-frontend` is the admin/tenant UI: manage agents, apps (App Canvas builder), runtime settings, tokens, users, tenants,
gateway clients, observability, a playground for chatting with entry points, and a live monitor. It is a **Next.js 16 / React 19**
app (`output: 'standalone'`, Node 20, port 3200, behind Traefik at `/`). Tailwind, `zustand` (one store: auth), `@xyflow/react` +
`dagre` (canvas), `livekit-*` (voice page). ~190 files, ~39k lines of TS/TSX.

## How it works

```
browser ──► Next.js pages (client components)
   │  fetch('/api/…')  same origin, httpOnly cookies
   ▼
Next.js route handlers  frontend/src/app/api/*   (a "backend for frontend")
   ├─ /api/auth/{login,logout,me,refresh,tenant-lookup,token}  → them-auth-go  (THE_M_AUTH_URL)
   ├─ /api/auth-admin/[...path]  → auth service /api/v1/admin/…  (user management; needs cookie)
   ├─ /api/them/[...path]        → bridge /api/v1/…  (everything else; tenant routes /{tenant}/apps|a2a/… go to bridge root)
   ├─ /api/apps/[...path]        → bridge /apps/…
   └─ /api/debug/llm             → server-side relay to Anthropic/OpenAI/Groq/Gemini for canvas debug mode (caller supplies the API key in a header; no cookie/auth check)
live updates: browser opens WebSocket straight to the bridge: /ws/dashboard?token=… and /{tenant}/apps/{app}/{ep}/ws
```
In compose the handlers call the bridge **through Traefik** (`THE_M_API_URL=http://them-traefik:8088`) and the auth service directly
(`THE_M_AUTH_URL=http://them-auth-go:8703`). `NEXT_PUBLIC_BRIDGE_WS_URL` is empty, so WebSocket URLs are derived from the page origin.

### Auth in the UI
- Login: `authStore.login` → `/api/auth/login` (sends `{username: email, password}`) → auth service; the handler sets cookies `them_access_token` and `them_refresh_token` (`httpOnly`, `sameSite=strict`, `secure` only when `NODE_ENV=production` and `COOKIE_SECURE != 'false'`), then `/api/auth/me`.
- Every `/api/them/*` call carries `Authorization: Bearer <cookie token>`; on `401` `lib/apiClient.ts` calls `/api/auth/refresh` once (single-flight) and retries.
- `AuthGuard` (client component) calls `/api/auth/me` and redirects to `/login` when it fails. It is applied **per page** (not in a layout) and several pages skip it (`/`, the `applications/[id]/*` pages, the voice page). There is **no Next middleware**: protection of data is entirely server-side in the bridge/auth service; the guard only avoids showing an empty shell. Role-gated pages use `hooks/useRequireSuperAdmin.ts`.
- `/api/auth/token` returns the current access token (refreshing it if near expiry) so JS can open WebSockets (the playground, dashboard monitor, debug panel use `?token=`).
- `X-Playground-Token` (browser-supplied) overrides the cookie token on `/api/them/*` so the playground can test an entry point with a specific opaque token.

### Pages (`src/app/`)
`/login`, `/` and `/dashboard`, `/runs`, `/agents`; admin (`/admin/…`): `agents` (+ `builder`), `applications` (+ `[id]/builder` canvas, `monitor`, `runtime`, `spec`, `mcp-credentials`), `orchestrators`, `tokens`, `users`, `tenants`, `gateway`, `observability`, `services`, `temporal`, `audit-logs`, `playground`, `settings`; tenant self-service (`/tenant/members`, `/tenant/settings`); voice (`/apps/[slug]/voice`). The canvas editor lives under `admin/applications/components/` (`CanvasBuilderView`, `cbv/` panels and hooks); it mirrors the node rules served by `GET /admin/node-types` (`lib/nodeRegistry.ts`, `docs/reference/node-types-generated.md`).

### API client and types
`lib/apiClient.ts` (`request`, `api.get/post/…`, base `/api/them`), `lib/api.ts` (typed endpoint functions, ~900 lines), `lib/apiTypes.ts` (~1350 lines of response types, hand-maintained — they must follow the Go handlers). Streaming: the handlers pass `text/event-stream` and audio through unbuffered.

## Key invariants
- The browser never talks to the bridge or auth service directly for REST; it uses the BFF handlers, which attach the cookie token. WebSockets are the exception.
- Tenant identity comes from the token the BFF forwards, never from client-chosen headers.
- UI authorisation is cosmetic; the bridge enforces roles (`auth-and-roles.md`).
- Cookie names are fixed by the auth service (`them_access_token`, `them_refresh_token`); changing them breaks login.

## Failure modes
| What fails | Effect |
|---|---|
| Bridge/Traefik down | handlers throw (no timeout/try-catch) → 500 in the UI |
| Auth service down | login/refresh fail; existing access tokens keep working until expiry |
| Refresh token expired | `401` → redirect to `/login` |
| Upstream returns non-JSON | handlers return `{}` with the upstream status (error body lost) |

## Known gaps (found while writing, 2026-10-25; code read, UI not exercised)
See `docs/plans/code-findings-backlog.md` F-97…F-102.
1. `/api/auth/token` hands the access JWT to page JavaScript and WebSockets authenticate with `?token=` in the URL — this defeats `httpOnly` for XSS and puts tokens in proxy/access logs.
2. No frontend tests, lint or build in CI: eight `*.test.js` files exist but there is no `test` script and `.github/workflows/ci.yml` never runs them.
3. Stale defaults: `next.config.ts` rewrites `/api/bridge/*` to `http://them-bridge:8001` (the removed Python bridge) unless `THE_M_API_URL` is set, and hard-codes `allowedDevOrigins`.
4. BFF handlers have no timeouts or error handling, accept any path segments (not validated), and swallow non-JSON error bodies.
5. Size rule (`CLAUDE.md`, ≤400 lines): `apiTypes.ts` 1348, `tenants/page.tsx` 975, `tenant/settings/page.tsx` 931, `api.ts` 911, `CanvasBuilderView.tsx` 787, and others.
6. `/api/debug/llm` is an unauthenticated server-side relay (fixed provider URLs, caller-supplied key): anyone who can reach the frontend can use it to call those providers through the server (F-102).
7. `frontend/AGENTS.md` warns that this Next.js version has breaking changes; read the docs shipped in `node_modules/next/dist/docs/` before writing Next code.

## Where to change things
| To… | Edit |
|---|---|
| Add a page | `src/app/<route>/page.tsx`; wrap it in `AuthGuard` yourself (there is no layout-level guard) |
| Add/rename a backend call | `lib/api.ts` function + `lib/apiTypes.ts` type; the route is automatic through `/api/them/[...path]` |
| Change proxy/cookie behaviour | `src/app/api/**/route.ts` (keep cookie names and `secure` rule consistent across login/refresh/token) |
| Change canvas rules | `admin/applications/components/cbv/`, `lib/nodeRegistry.ts`, and the Go node definitions (`docs/architecture/app-canvas-appflow.md`) |
| Change runtime env | `docker-compose.yml` `them-frontend` environment (`THE_M_API_URL`, `THE_M_AUTH_URL`, `NEXT_PUBLIC_*` — `NEXT_PUBLIC_*` are baked at build time, see `docs/lessons/infra-docker.md`) |

Run locally: `docker compose … up -d them-frontend` (dev overlay uses `Dockerfile.dev` / `npm run dev -p 3200`); unit tests by hand: `node src/lib/__tests__/graphWalk.test.js` (each file documents its own command).

## Related
`auth-and-roles.md`, `platform-overview.md`, `app-canvas-appflow.md`, `docs/guides/local-dev-and-deploy.md`
