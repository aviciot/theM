-- Per-entry-point LLM override (provider + model).
-- When NULL the runtime falls back to the app_orchestrator's llm_provider/llm_model.
ALTER TABLE them.entry_points
  ADD COLUMN IF NOT EXISTS llm_provider TEXT,
  ADD COLUMN IF NOT EXISTS llm_model    TEXT;
