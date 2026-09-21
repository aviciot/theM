-- Phase 5 (docs/NODE_REGISTRY_PLAN.md): middleware_defs adopts the node contract.
-- Adds the same shape code-defined nodes already carry via internal/nodedefs.Meta
-- (edges/input_ports/output_ports/config_fields), so GET /admin/node-types can
-- merge in a "middleware" family alongside "agentgen" and "appflow". All columns
-- nullable — existing rows with no seeded data keep working (fail-open, same
-- precedent as 097_middleware_defs_visual.sql).

ALTER TABLE them.middleware_defs
  ADD COLUMN IF NOT EXISTS edges         JSONB,
  ADD COLUMN IF NOT EXISTS input_ports   JSONB,
  ADD COLUMN IF NOT EXISTS output_ports  JSONB,
  ADD COLUMN IF NOT EXISTS config_fields JSONB;

-- Seed File Guard's real shape. In the app canvas it sits inline on an
-- orchestrator -> middleware -> agent chain: exactly one edge in, one out
-- (it is a pass-through filter, not a branching node — see
-- internal/appflow/workflow.go's "case middleware" pass-through).
-- config_fields mirror MiddlewareNodePanel.tsx's real fields exactly
-- (frontend/src/app/admin/applications/components/cbv/panels/MiddlewareNodePanel.tsx).
UPDATE them.middleware_defs
SET edges = '{"min_in": 1, "max_in": 1, "min_out": 1, "max_out": 1}'::jsonb,
    config_fields = '[
      {"key": "enabled", "type": "bool", "required": true, "description": "Whether File Guard is active on this wiring.", "example": "true"},
      {"key": "mode", "type": "string", "required": true, "description": "\"block\" quarantines infected files; \"warn\" logs only and still delivers the file.", "example": "block"},
      {"key": "max_file_size_mb", "type": "int", "required": false, "description": "Maximum accepted file size in megabytes.", "example": "5"},
      {"key": "allowed_types", "type": "array", "required": false, "description": "Comma-separated file extensions to allow. Empty means all types are allowed.", "example": "[\"pdf\", \"png\", \"csv\"]"},
      {"key": "blocked_types", "type": "array", "required": false, "description": "Comma-separated file extensions to always reject.", "example": "[\"exe\", \"sh\", \"bat\"]"},
      {"key": "notify_on_fail", "type": "bool", "required": false, "description": "Send a notification when a file is blocked or flagged.", "example": "true"}
    ]'::jsonb
WHERE slug = 'file-guard';
