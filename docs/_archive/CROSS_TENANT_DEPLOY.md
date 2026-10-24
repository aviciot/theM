# Cross-Tenant Canvas Deploy — Open Problem

## The Problem

When a canvas blueprint is built in tenant A and deployed to tenant B, it fails.

**Why:** The blueprint JSON stores agent UUIDs from the tenant where it was built. Those UUIDs don't exist in the target tenant — each tenant has its own UUIDs for the same agents.

Example:
```
Blueprint has: agent_creative → UUID abc-111  (default tenant)
payops_ai has: agent_creative → UUID xyz-999  (different UUID, same agent)
Deploy fails: UUID abc-111 not found in payops_ai
```

## Current Fix (Interim)

At publish time, before deploying, the Go code scans each component in the blueprint, looks up the agent by `kind+name` in the target tenant, and replaces the UUID. Works when the agent exists in the target tenant by the same name.

Fails clearly if an agent doesn't exist in the target tenant at all.

## Possible Solutions

### Option 1 — Resolve by name at publish time (current)
Look up each component by `kind+name` in target tenant. Replace UUID. Deploy.
- **Pro:** Simple, no new UI
- **Con:** Requires agent to already exist in target tenant. No auto-creation.

### Option 2 — Scan + auto-create missing components
Same as Option 1, but if a component is missing in target tenant, copy it from source tenant automatically.
- **Pro:** Fully self-contained — one-click deploy regardless of target state
- **Con:** Silent auto-creation is risky (wrong version, missing credentials, user unaware)

### Option 3 — Blueprints store names only, never UUIDs
Remove `definition_id` (UUID) from blueprints entirely. Resolve only by `kind+name` at all times.
- **Pro:** Blueprints become truly portable — no tenant-specific data
- **Con:** Loses the UUID fast-path; name uniqueness must be enforced strictly

### Option 4 — Pre-deploy diff UI
Before deploying to a target tenant, show the user a diff: "these components exist ✓, these are missing ✗". User chooses: copy missing ones or cancel.
- **Pro:** User is in control, no surprises
- **Con:** More UI work

## Open Question

Which option (or combination) is the right long-term approach?

Key trade-offs to decide:
- Should deploy be fully automatic (Options 1/2/3) or require user confirmation for missing components (Option 4)?
- Should blueprints store UUIDs at all, or names only?
- Who is responsible for ensuring agents exist in the target tenant — the user before deploy, or the system during deploy?
