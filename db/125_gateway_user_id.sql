-- Migration 125: Add user_id to gateway_requests for user_jwt mode
-- Gateway now supports two auth modes:
--   1. Token-based (client_id + token_hash from access_tokens) - existing
--   2. User JWT (user_id from HS256 JWT claims) - NEW
-- This adds user_id column to track which the-M user made the request.

ALTER TABLE them.gateway_requests
ADD COLUMN user_id INTEGER
REFERENCES auth_service.users(id) ON DELETE SET NULL;

CREATE INDEX idx_gateway_requests_user_id ON them.gateway_requests(user_id);

COMMENT ON COLUMN them.gateway_requests.user_id IS 'the-M user ID from JWT (user_jwt mode); NULL for token-based auth';
