"""
Application Runtime Management Layer.

Enforces runtime policy at connection time, before any Temporal workflow starts.
Unlike session_manager (best-effort, never-raises), functions here DO raise
RuntimeLimitError when a policy check fails — callers must handle it.

Gate order (app-level first, then entry-point-level):
  1. Blocked token   — sha256(raw_token) in app_runtime.blocked_tokens
  2. Blocked user    — user_id in app_runtime.blocked_user_ids
  3. App rate limit  — INCR rl:them:app:{app_id}:{hour_slot} vs app_runtime.rate_limit_rpm
  4. App session cap — SCARD them:app:{app_id}:sessions vs app_runtime.max_concurrent_sessions
                       NOTE: SOFT cap (non-atomic SCARD read). Rationale: the strict per-EP
                       cap is enforced atomically via Lua EVAL below; the app cap is a coarse
                       guardrail. Under very high concurrency it can transiently overshoot by
                       the number of racing requests — acceptable at this tier.
  5. EP session cap  — Lua EVAL on them:ep:{ep_slug}:sessions vs ep_max_concurrent
                       Lua atomically prunes ghost entries (pod-crash leftovers), checks cap,
                       SADDs the new session_id.
                       If cap hit AND queue_timeout_seconds is set → raises RuntimeQueueFull
                       so caller can wait and retry via ep_gate_try().
  6. Orchestrator rate limit — check_rate_limit(user_id, rate_limit_rpm)

Redis keys used:
  them:ep:{ep_slug}:sessions          Set — read (SCARD) + atomic reserve via Lua EVAL
  them:app:{app_id}:sessions          Set — read (SCARD) only for soft app cap
  rl:them:app:{app_id}:{hour_slot}    String — INCR for app-level rate limit (TTL 7200s)
  them:sess:control:{sid}             Pub/Sub channel — published by signal_disconnect()
"""

import time
import uuid
from typing import Optional

import app.database as _db
from app.utils.logger import logger

_SESS_CONTROL_PREFIX = "them:sess:control:"
_APP_RL_PREFIX = "rl:them:app:"   # rl:them:app:{app_id}:{hour_slot}

# Lua script: prune dead session members, then atomic check-and-add.
#
# KEYS[1] = them:ep:{slug}:sessions   (the EP Set)
# ARGV[1] = new session_id to add
# ARGV[2] = max_concurrent_sessions (or -1 for unlimited)
# ARGV[3] = them:sess: prefix (so the script can check liveness)
#
# For each member in the set, check whether its session hash exists. If not
# (pod crashed before session_manager.end() ran), remove the ghost entry.
# Then apply the cap check and SADD.
#
# Returns: -1 if cap reached, else new set cardinality after SADD.
_LUA_GATE = """
local members = redis.call('SMEMBERS', KEYS[1])
for _, sid in ipairs(members) do
    local sess_key = ARGV[3] .. sid
    if redis.call('EXISTS', sess_key) == 0 then
        redis.call('SREM', KEYS[1], sid)
    end
end
local n = redis.call('SCARD', KEYS[1])
if tonumber(ARGV[2]) >= 0 and n >= tonumber(ARGV[2]) then return -1 end
redis.call('SADD', KEYS[1], ARGV[1])
return n + 1
"""
_SESS_PREFIX_FOR_LUA = "them:sess:"


class RuntimeLimitError(Exception):
    """Raised when a runtime policy check fails."""
    def __init__(self, reason: str, ws_code: int, detail: str) -> None:
        super().__init__(detail)
        self.reason = reason    # "rate_limited" | "session_cap" | "blocked"
        self.ws_code = ws_code  # 4429 = rate limited, 4403 = cap/blocked
        self.detail = detail


class RuntimeQueueFull(Exception):
    """Raised when EP is at cap but queuing is enabled — caller should wait and retry via ep_gate_try()."""
    def __init__(self, queue_message: Optional[str], deadline: float) -> None:
        self.queue_message = queue_message or "All agents are busy, please wait..."
        self.deadline = deadline


async def ep_gate_try(
    ep_slug: str,
    session_id: uuid.UUID,
    ep_max_concurrent: int,
) -> bool:
    """
    Single atomic attempt to acquire an EP slot.
    Returns True if slot acquired, False if at cap.
    Fail-open on Redis error (returns True).
    """
    redis = _db.redis_client
    if redis is None:
        return True
    ep_set_key = f"them:ep:{ep_slug}:sessions"
    try:
        result = await redis.eval(
            _LUA_GATE, 1, ep_set_key,
            str(session_id), str(ep_max_concurrent), _SESS_PREFIX_FOR_LUA,
        )
        return int(result) != -1
    except Exception as exc:
        logger.warning("ep_gate_try: Lua eval failed — allowing", ep_slug=ep_slug, error=str(exc))
        return True


async def runtime_gate(
    ep_slug: Optional[str],
    app_id: Optional[str],
    user_id: int,
    session_id: uuid.UUID,
    token_hash: Optional[str] = None,
    app_runtime: Optional[dict] = None,
    ep_max_concurrent: Optional[int] = None,
    rate_limit_rpm: int = 0,
    queue_timeout_seconds: Optional[int] = None,
    queue_message: Optional[str] = None,
) -> None:
    """
    Gate called after auth, before workflow start.

    Pass app_runtime=app.runtime_config (JSONB dict) for app-level enforcement.
    Pass ep_max_concurrent=ep.max_concurrent_sessions for EP-level cap.
    Pass token_hash=hashlib.sha256(raw_token).hexdigest() for blocked-token check.

    Fail-open on Redis unavailability (infra issue should not block users);
    fail-closed on explicit cap (max_concurrent_sessions is set and cap is reached).
    """
    app_rt = app_runtime or {}

    # ── 1. Blocked token ──────────────────────────────────────────────────────
    blocked_tokens = app_rt.get("blocked_tokens") or []
    if token_hash and blocked_tokens and token_hash in blocked_tokens:
        raise RuntimeLimitError(
            reason="blocked",
            ws_code=4403,
            detail="Access denied for this token.",
        )

    # ── 2. Blocked user ───────────────────────────────────────────────────────
    blocked_users = app_rt.get("blocked_user_ids") or []
    if user_id and blocked_users and user_id in blocked_users:
        raise RuntimeLimitError(
            reason="blocked",
            ws_code=4403,
            detail="Access denied for this user.",
        )

    # ── 3. App rate limit ─────────────────────────────────────────────────────
    app_rpm = app_rt.get("rate_limit_rpm")
    if app_id and app_rpm:
        redis = _db.redis_client
        if redis is not None:
            slot = int(time.time()) // 3600
            key = f"{_APP_RL_PREFIX}{app_id}:{slot}"
            try:
                count = await redis.incr(key)
                if count == 1:
                    await redis.expire(key, 7200)
                if count > app_rpm * 60:
                    raise RuntimeLimitError(
                        reason="rate_limited",
                        ws_code=4429,
                        detail=f"Application rate limit exceeded ({count} requests this hour). Try again later.",
                    )
            except RuntimeLimitError:
                raise
            except Exception as exc:
                logger.warning("runtime_gate: app rate limit check failed — allowing",
                               app_id=app_id, error=str(exc))

    # ── 4. App session cap (SOFT — SCARD read, non-atomic) ────────────────────
    app_cap = app_rt.get("max_concurrent_sessions")
    if app_id and app_cap is not None:
        try:
            from app.services.session_manager import count_app_sessions
            current = await count_app_sessions(app_id)
            if current >= app_cap:
                raise RuntimeLimitError(
                    reason="session_cap",
                    ws_code=4403,
                    detail=f"Application is at capacity ({app_cap} concurrent sessions). Try again later.",
                )
        except RuntimeLimitError:
            raise
        except Exception as exc:
            logger.warning("runtime_gate: app session cap check failed — allowing",
                           app_id=app_id, error=str(exc))

    # ── 5. Orchestrator rate limit ────────────────────────────────────────────
    if rate_limit_rpm > 0:
        from app.services.rate_limiter import check_rate_limit
        allowed, count = await check_rate_limit(user_id, rate_limit_rpm)
        if not allowed:
            raise RuntimeLimitError(
                reason="rate_limited",
                ws_code=4429,
                detail=f"Rate limit exceeded ({count} requests this hour). Try again later.",
            )

    # ── 6. EP session cap (atomic Lua: prune ghosts + check + SADD) ──────────
    if ep_slug and ep_max_concurrent is not None:
        acquired = await ep_gate_try(ep_slug, session_id, ep_max_concurrent)
        if not acquired:
            if queue_timeout_seconds:
                deadline = time.time() + queue_timeout_seconds
                raise RuntimeQueueFull(queue_message, deadline)
            raise RuntimeLimitError(
                reason="session_cap",
                ws_code=4403,
                detail=(
                    f"Entry point '{ep_slug}' is at capacity "
                    f"({ep_max_concurrent} concurrent sessions). Try again later."
                ),
            )
        logger.info(
            "runtime_gate: EP session slot reserved",
            ep_slug=ep_slug,
            session_id=str(session_id),
            max=ep_max_concurrent,
        )


async def signal_disconnect(session_id: uuid.UUID) -> bool:
    """
    Publish a disconnect signal to them:sess:control:{session_id}.
    Returns True if at least one subscriber received the message.
    Best-effort — never raises.
    """
    redis = _db.redis_client
    if redis is None:
        return False
    try:
        channel = f"{_SESS_CONTROL_PREFIX}{session_id}"
        receivers = await redis.publish(channel, "disconnect")
        logger.info("runtime_manager.signal_disconnect", session_id=str(session_id), receivers=receivers)
        return int(receivers) >= 1
    except Exception as exc:
        logger.warning("runtime_manager.signal_disconnect failed", session_id=str(session_id), error=str(exc))
        return False
