-- Migration 090: stable IdP identity anchor on auth_service.users
-- Adds idp_sub (OIDC subject claim) + idp_issuer (discovery URL / realm)
-- so SSO users are matched by their stable IdP identity, not by email.
--
-- Why: email-based matching has two risks:
--   1. Email collision — a local account and an SSO account share the same email
--      and would merge into one user row unintentionally.
--   2. Email change at IdP — if the IdP changes a user's email, next SSO login
--      creates a second user row and orphans the old one (old history lost).
--
-- With idp_sub + idp_issuer: UpsertOIDCUser matches on (idp_sub, idp_issuer) first.
-- Local accounts never have these set (NULL) so they are never matched by SSO.

ALTER TABLE auth_service.users
    ADD COLUMN IF NOT EXISTS idp_sub    TEXT,
    ADD COLUMN IF NOT EXISTS idp_issuer TEXT;

-- One user per (sub, issuer) pair. Partial — NULL rows (local accounts) unconstrained.
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_idp_sub_issuer
    ON auth_service.users (idp_sub, idp_issuer)
    WHERE idp_sub IS NOT NULL AND idp_issuer IS NOT NULL;
