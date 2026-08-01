-- stream_publish.lua — atomic dual-publish for Phase 11c-A
--
-- atomicPublish(stream_key, pubsub_channel, maxlen, safety_ttl, final_ttl, is_terminal, payload, dual_publish)
-- KEYS[1] = stream key   (them:dash:run:{runID}:stream)
-- KEYS[2] = pubsub channel (them:dash:run:{runID}:tokens)
-- ARGV[1] = maxlen (integer, approximate trim target)
-- ARGV[2] = safety_ttl (integer seconds, e.g. 172800 for 48h)
-- ARGV[3] = final_ttl (integer seconds, e.g. 86400 for 24h)
-- ARGV[4] = is_terminal (string "1" or "0")
-- ARGV[5] = payload (JSON string)
-- ARGV[6] = dual_publish (string "1" or "0")
--
-- Returns: stream entry ID (string)
--
-- All three operations (XADD, PUBLISH, EXPIRE) are atomic — either all execute
-- or none do (if Redis errors before the script completes). This prevents:
--   - XADD succeeding but PUBLISH failing (transports inconsistent)
--   - XADD succeeding but EXPIRE failing (permanent key leak)

local stream_key    = KEYS[1]
local channel       = KEYS[2]
local maxlen        = tonumber(ARGV[1])
local safety_ttl    = tonumber(ARGV[2])
local final_ttl     = tonumber(ARGV[3])
local is_terminal   = ARGV[4] == "1"
local payload       = ARGV[5]
local dual_publish  = ARGV[6] == "1"

-- 1. XADD with MAXLEN approximate trim
local entry_id = redis.call('XADD', stream_key, 'MAXLEN', '~', maxlen, '*', 'data', payload)

-- 2. PUBLISH on legacy channel if dual-publish is enabled
if dual_publish then
  redis.call('PUBLISH', channel, payload)
end

-- 3. Set retention TTL atomically
if is_terminal then
  -- Final TTL: run is done; start 24h retention window from now.
  -- Replaces any previously set safety TTL.
  redis.call('EXPIRE', stream_key, final_ttl)
else
  -- Safety TTL: only set if this is the first entry (XLEN == 1).
  -- Avoids resetting TTL on every event and keeps O(1) cost.
  if redis.call('XLEN', stream_key) == 1 then
    redis.call('EXPIRE', stream_key, safety_ttl)
  end
end

return entry_id
