# Investigation: How does the Playground test Entry Points?

## Context

the-M is a multi-agent orchestration platform. Tenants build AI apps in a Canvas.
Each app has Entry Points (EPs) — doors that external callers connect through.
EPs have access policies (token, user_jwt, external_jwt, public) that control who is admitted.

The **Playground** is a developer tool in the Admin UI that lets you chat with an app.

## The question

We observed that the Playground works even when the EP access mode is `token` (which should
require an opaque bearer token). The logged-in user only has a HS256 user JWT — not an opaque token.
According to the lifecycle admission code, this should be rejected. But it isn't.

We need to understand exactly how the Playground connects — does it bypass EP admission,
hit a different route, or is there a fallback we missed?

## What we know so far

- Playground fetches a user JWT from `/api/auth/token` (frontend Next.js route)
- For orchestrator targets → connects to `/ws/dashboard` (bypasses EP entirely — confirmed)
- For EP targets → `targetWsUrl()` in `playgroundTypes.ts:27` builds:
  `/{tenantSlug}/apps/{appSlug}/{epSlug}/ws?token=<jwt>`
  This looks like the real EP URL
- Lifecycle admission at `go/internal/execution/lifecycle.go:297` rejects `access_mode=token`
  when `tokenInfo == nil` — and a user JWT would not resolve via the opaque token cache
- No super_admin bypass found in lifecycle or WS handler

## What to investigate

1. Does the playground actually hit the real EP WS URL, or does it go somewhere else?
   Check browser Network tab WS connections, or trace via go-bridge logs when playground connects.

2. Is there a separate playground/dashboard WS handler that handles EP slugs differently?
   Check `go/cmd/them/main.go` for all registered WS routes.

3. Does the WS handler or lifecycle have any path that accepts a user JWT when access_mode=token?
   Read `go/internal/ws/handler.go` fully and `go/internal/execution/lifecycle.go` fully.

4. For A2A EPs — `useChatConnection.ts:138` calls `themApi.a2aStream()` with the user JWT.
   Does the A2A handler accept user JWTs? Check `go/internal/a2a/server.go`.

5. For Voice EPs — voice has its own handler (`go/internal/voice/handler.go`).
   How does it authenticate? Does the playground hit the real voice EP or something else?

6. What actually happens at the go-bridge when the playground connects to `my-ws` EP
   (access_mode=token) as a logged-in admin user — admitted or rejected?
   Check: `docker logs them-go-bridge --tail 100` after a playground test.

## Key files to read

- `go/cmd/them/main.go` — all registered routes
- `go/internal/ws/handler.go` — WS admission
- `go/internal/execution/lifecycle.go` — full admission logic
- `go/internal/a2a/server.go` — A2A admission
- `go/internal/voice/handler.go` — voice admission
- `frontend/src/app/admin/playground/useChatConnection.ts` — how playground connects
- `frontend/src/app/admin/playground/playgroundTypes.ts` — URL construction

## Expected output

A clear answer to:
- Does the playground bypass EP admission for any EP type? If yes, how exactly?
- If not, why does it work with access_mode=token and a user JWT?
- Are there any security implications (e.g. admin can bypass EP restrictions)?
- What is the correct documentation for which EP types can be tested from the playground?
