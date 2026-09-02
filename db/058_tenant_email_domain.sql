-- Step 17: Add email_domain to them.tenants for email-domain → tenant routing.
-- Allows platform admins to associate a corporate email domain with a tenant so
-- that users are automatically routed to the correct tenant's IdP on login.
-- NULL means "no domain claim" (default); UNIQUE enforced when non-null via partial index.

ALTER TABLE them.tenants
    ADD COLUMN IF NOT EXISTS email_domain TEXT DEFAULT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS tenants_email_domain_uq
    ON them.tenants (email_domain)
    WHERE email_domain IS NOT NULL;
