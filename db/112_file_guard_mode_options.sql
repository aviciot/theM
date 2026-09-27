-- Fix (docs/APPFLOW_A2A_RESPONSE_KINDS_PLAN.md follow-up): file-guard's "mode"
-- config field was declared type "string" with no fixed value set, so the
-- App Canvas Guards section (frontend/.../AgentGuardsSection.tsx) rendered it
-- as free text -- a user could type "blockk" and silently misconfigure the
-- wiring. Only two real values are ever read by the runtime
-- (internal/middleware/gate.go's mode handling: "block" or "warn"). Adds
-- "options" to the existing config_fields JSONB so the frontend can render a
-- select instead (internal/nodedefs.ConfigFieldDoc.Options, new field).
UPDATE them.middleware_defs
SET config_fields = (
  SELECT jsonb_agg(
    CASE WHEN field->>'key' = 'mode'
      THEN field || '{"options": ["block", "warn"]}'::jsonb
      ELSE field
    END
  )
  FROM jsonb_array_elements(config_fields) AS field
)
WHERE slug = 'file-guard';
