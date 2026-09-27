-- docs/APPFLOW_TEXT_GUARDS_PLAN.md: pii_redact's "llm_assist" checkbox has
-- no real runtime effect (reserved for a future LLM-judge-based detection
-- pass, not implemented) -- confirmed with the user 2026-09-27 to mark it
-- "(placeholder)" in the UI rather than hide it, using the new generic
-- ConfigFieldDoc.Placeholder flag (nodedefs.go) instead of a one-off
-- frontend special-case for this specific field key.
UPDATE them.middleware_defs
SET config_fields = (
  SELECT jsonb_agg(
    CASE WHEN field->>'key' = 'llm_assist'
      THEN field || '{"placeholder": true}'::jsonb
      ELSE field
    END
  )
  FROM jsonb_array_elements(config_fields) AS field
)
WHERE slug = 'pii_redact';
