# Keycloak — fake customer login (test only)

`them-keycloak` is **not part of the product**. It pretends to be a customer's SSO
provider (like Okta) so we can test the-M's SSO without a real customer.
Runs only with the `sso` compose profile.

## What is in it

One Keycloak, several **realms**. Each realm = one fake customer login system.
The `*-realm.json` files are imported at startup.

| Realm | Purpose |
|---|---|
| `payops_ai` | Fake customer PayOps (users: alice = tier premium, bob = tier standard) |
| `bank` | Fake bank customer |
| `rnd` | Fake R&D customer |
| `them` | the-M's own realm (not checked what uses it) |

## Where to see it

- Admin console: `http://<host>:8088/auth/keycloak/admin/`
- Login `admin` / `admin123` (dev default; `KEYCLOAK_ADMIN*` in `.env`).
- You land in the **master** realm (Keycloak's own admin). Use the realm dropdown
  (top left) to open `payops_ai`, `bank` or `rnd`, then **Users** / **Clients**.
- `KC_HOSTNAME` in `docker-compose.dev.yml` must match the address you browse to,
  otherwise Keycloak redirects to the wrong host (it is `localhost:8088` today).

## How the-M uses it

The-M talks to a customer IdP over **OIDC**. Keycloak plays that IdP:
employees log in via OIDC SSO, groups map to the-M roles; or end users call an
`external_jwt` entry point with a token from the realm.
Details: `docs/architecture/auth-and-roles.md`.

## Real customers

| Customer has | Needs Keycloak in between? |
|---|---|
| Okta, Microsoft Entra ID, Google Workspace, Auth0, Ping, OneLogin, JumpCloud (all OIDC) | **No** — the-M connects directly via the tenant's IdP settings |
| SAML only | Yes, as a bridge (not built) |
| LDAP / on-prem Active Directory only | Yes, as a bridge via Keycloak User Federation (not built) |

The-M has no direct SAML or LDAP support. A Keycloak bridge is an option, not a
current feature. To simulate an LDAP-only customer, add an OpenLDAP container
with fake users and connect it to a realm via User Federation.
