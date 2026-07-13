-- Phase 11: per-conversation token budget on applications
-- NULL = no limit. When set, the sum of tokens_used across all tasks
-- in a context_id is checked before starting a new run. If the sum
-- meets or exceeds this limit the request is rejected with a 429.

ALTER TABLE them.applications
    ADD COLUMN IF NOT EXISTS conversation_token_limit INTEGER;
