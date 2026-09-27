-- docs/APPFLOW_TEXT_GUARDS_PLAN.md: PII Guard's detectable categories
-- (email/phone/credit_card/ssn) were hardcoded with no way to configure
-- which ones actually run -- confirmed with the user 2026-09-27 to add a
-- real "categories" config field (empty/omitted = scan everything,
-- fail-open convention), matching internal/middleware/pii.CategoryNames().
UPDATE them.middleware_defs
SET config_fields = config_fields || '[
  {"key": "categories", "type": "array", "required": false, "options": ["email", "phone", "credit_card", "ssn"], "description": "Which PII categories to scan for. Empty means all categories.", "example": "[\"email\", \"credit_card\"]"}
]'::jsonb
WHERE slug = 'pii_redact';
