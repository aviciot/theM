-- Migration: Add config validation status to gateway_clients
-- Date: 2026-10-18
-- Purpose: Enforce rule that every gateway client must have a policy profile

-- Add validation status columns
ALTER TABLE them.gateway_clients
ADD COLUMN IF NOT EXISTS config_status TEXT DEFAULT 'unknown'
  CHECK (config_status IN ('valid', 'invalid_no_policy', 'invalid_profile_without_policy', 'unknown')),
ADD COLUMN IF NOT EXISTS config_status_reason TEXT;

-- Create index for filtering invalid clients
CREATE INDEX IF NOT EXISTS idx_gateway_clients_config_status
  ON them.gateway_clients(config_status) WHERE config_status != 'valid';

-- Update existing rows based on current state
UPDATE them.gateway_clients
SET
  config_status = CASE
    WHEN policy_profile_id IS NOT NULL THEN 'valid'
    WHEN app_id IS NOT NULL AND policy_profile_id IS NULL THEN 'invalid_profile_without_policy'
    WHEN policy_profile_id IS NULL THEN 'invalid_no_policy'
    ELSE 'unknown'
  END,
  config_status_reason = CASE
    WHEN policy_profile_id IS NOT NULL THEN NULL
    WHEN app_id IS NOT NULL AND policy_profile_id IS NULL THEN 'Profile app assigned but no policy profile - required for budget and model enforcement'
    WHEN policy_profile_id IS NULL THEN 'No policy profile assigned - required for budget and model enforcement'
    ELSE 'Unknown configuration state'
  END
WHERE config_status = 'unknown';

-- Function to validate client config on INSERT/UPDATE
CREATE OR REPLACE FUNCTION them.validate_gateway_client_config()
RETURNS TRIGGER AS $$
BEGIN
  -- Rule: Every client must have a policy profile
  IF NEW.policy_profile_id IS NULL THEN
    IF NEW.app_id IS NOT NULL THEN
      NEW.config_status := 'invalid_profile_without_policy';
      NEW.config_status_reason := 'Profile app assigned but no policy profile - required for budget and model enforcement';
    ELSE
      NEW.config_status := 'invalid_no_policy';
      NEW.config_status_reason := 'No policy profile assigned - required for budget and model enforcement';
    END IF;
  ELSE
    -- Has policy - valid
    NEW.config_status := 'valid';
    NEW.config_status_reason := NULL;
  END IF;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Trigger to auto-validate on INSERT/UPDATE
DROP TRIGGER IF EXISTS trg_validate_gateway_client_config ON them.gateway_clients;
CREATE TRIGGER trg_validate_gateway_client_config
  BEFORE INSERT OR UPDATE ON them.gateway_clients
  FOR EACH ROW
  EXECUTE FUNCTION them.validate_gateway_client_config();

COMMENT ON COLUMN them.gateway_clients.config_status IS 'Configuration validation status: valid (has policy), invalid_no_policy (missing policy), invalid_profile_without_policy (has app but no policy)';
COMMENT ON COLUMN them.gateway_clients.config_status_reason IS 'Human-readable explanation when config_status is invalid';
