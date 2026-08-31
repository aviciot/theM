-- Migration 048: add slug to applications, relax EP slug uniqueness to per-application
-- Run AFTER 047_ep_llm.sql.

-- ── Step 1: add slug column (nullable initially so backfill can run first) ───
ALTER TABLE them.applications
    ADD COLUMN IF NOT EXISTS slug TEXT;

-- ── Step 2: backfill slug from name ─────────────────────────────────────────
-- Rules: lowercase, collapse non-alphanumeric runs to hyphens, strip leading/
-- trailing hyphens, truncate to 48 chars.
UPDATE them.applications
SET slug = LOWER(
    REGEXP_REPLACE(
        REGEXP_REPLACE(
            TRIM(BOTH '-' FROM
                REGEXP_REPLACE(name, '[^a-zA-Z0-9]+', '-', 'g')
            ),
        '-{2,}', '-', 'g'),
    '^-+|-+$', '', 'g')
)
WHERE slug IS NULL OR slug = '';

-- Truncate any slug that is over 48 chars after backfill
UPDATE them.applications
SET slug = LEFT(slug, 48)
WHERE LENGTH(slug) > 48;

-- Strip trailing hyphens that may appear after truncation
UPDATE them.applications
SET slug = REGEXP_REPLACE(slug, '-+$', '')
WHERE slug ~ '-$';

-- ── Step 3: deduplicate slugs within the same tenant ─────────────────────────
-- If two rows in the same tenant share a slug (e.g. two "New Application" rows),
-- append the first 8 chars of their UUID to all but the earliest-created one.
UPDATE them.applications a
SET slug = LEFT(a.slug, 39) || '-' || SUBSTRING(a.id::text, 1, 8)
WHERE a.id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (PARTITION BY tenant_id, slug ORDER BY created_at, id) AS rn
        FROM them.applications
    ) ranked
    WHERE rn > 1
);

-- ── Step 4: enforce NOT NULL + format constraint + UNIQUE(tenant_id, slug) ───
ALTER TABLE them.applications
    ALTER COLUMN slug SET NOT NULL;

ALTER TABLE them.applications
    ADD CONSTRAINT applications_slug_check
        CHECK (slug ~ '^[a-z0-9][a-z0-9_-]{0,47}$');

ALTER TABLE them.applications
    ADD CONSTRAINT uq_applications_tenant_slug
        UNIQUE (tenant_id, slug);

-- ── Step 5: relax EP slug uniqueness from per-tenant → per-application ───────
-- The old constraint prevented two apps under the same tenant from sharing an
-- EP slug (e.g. both having an EP called "chat"). With app_slug in the URL the
-- full path is unique; we only need uniqueness within one application.
ALTER TABLE them.entry_points
    DROP CONSTRAINT IF EXISTS uq_entry_points_tenant_slug;

ALTER TABLE them.entry_points
    ADD CONSTRAINT uq_entry_points_app_slug
        UNIQUE (application_id, slug);

-- ── Step 6: record migration ──────────────────────────────────────────────────
INSERT INTO them.schema_migrations (version, description)
VALUES ('048', '048_application_slug: slug on applications, per-app EP uniqueness')
ON CONFLICT (version) DO NOTHING;
