"""
Security scanner logic — HTTP surface probes + LLM card/skill analysis.
No A2A imports. Called from main.py execute().
"""

import asyncio
import json
from datetime import datetime, timezone
from typing import Any

import httpx

# ── Prompt ────────────────────────────────────────────────────────────────────

_SYSTEM_PROMPT = """\
You are a security auditor for an AI agent orchestration platform. You analyze one \
agent's declared metadata (agent card, description, and skills) for security risk. \
You do NOT execute anything or call the agent. Judge only what the metadata reveals.

Assess these dimensions:
1. Skill scope — are any skills dangerously broad or capable of destructive/arbitrary \
action (e.g. "execute commands", "read any file", "run arbitrary code", unrestricted \
network/filesystem/database access)?
2. Description quality — is the tool description accurate, specific, and appropriately \
scoped? Flag vague, over-promising, or manipulable descriptions that raise prompt-injection risk.
3. Input/output modes — are risky or unconstrained data types accepted with no stated limits?
4. Missing guardrails — absence of an input schema or constraints means the agent accepts \
unbounded input; treat as elevated risk.

Return ONLY a JSON object, no prose, no markdown fences, with this exact shape:
{
  "summary": "<one plain-English sentence summarizing overall security posture>",
  "findings": [
    {
      "id": "<short_snake_case_id>",
      "label": "<short human label>",
      "status": "pass" | "warn" | "fail",
      "risk": "low" | "medium" | "high",
      "detail": "<one sentence: what you observed>",
      "recommendation": "<one sentence: concrete fix, or 'No action needed'>"
    }
  ]
}
Rules: 2-5 findings. Use "pass"/"low" for things that look fine. Reserve "fail"/"high" \
for genuinely dangerous scope or missing auth-relevant guardrails. Be concise and specific.\
"""


def _build_user_prompt(payload: dict) -> str:
    skills_json = json.dumps(payload.get("skills", []), indent=2)
    endpoint_url = payload.get("endpoint_url", "")
    scheme = "https" if endpoint_url.startswith("https://") else "http"
    agent_card = payload.get("agent_card") or {}
    has_input_schema = bool(
        agent_card.get("inputModes") or
        any(s.get("inputModes") for s in (payload.get("skills") or []) if isinstance(s, dict))
    )
    return (
        f"Agent under review:\n\n"
        f"slug: {payload.get('slug', '?')}\n"
        f"display_name: {payload.get('display_name', '?')}\n\n"
        f"Description (this is the text the orchestrating LLM sees to decide when to call it):\n"
        f"{payload.get('description', '(none)')}\n\n"
        f"Declared skills (JSON):\n{skills_json}\n\n"
        f"Capabilities: streaming={payload.get('supports_streaming', False)}, "
        f"push={payload.get('supports_push', False)}\n"
        f"Input schema present: {'yes' if has_input_schema else 'no'}\n"
        f"Endpoint scheme: {scheme}\n\n"
        f"Analyze and return the JSON object."
    )


# ── HTTP probes ───────────────────────────────────────────────────────────────

async def http_probes(endpoint_url: str, has_auth_token: bool) -> dict:
    """
    Returns {"tls": "pass"|"fail", "auth_required": "pass"|"fail", "reachable": bool}
    """
    base = endpoint_url.rstrip("/")
    tls = "pass" if base.startswith("https://") else "fail"
    reachable = False
    auth_required = "fail"

    try:
        async with httpx.AsyncClient(timeout=8.0, follow_redirects=True) as client:
            # Reachability: fetch agent card
            card_url = f"{base}/.well-known/agent-card.json"
            try:
                r = await client.get(card_url)
                reachable = True
            except Exception:
                return {"tls": tls, "auth_required": auth_required, "reachable": False}

            # Auth enforcement: POST root with no auth header
            try:
                probe = await client.post(
                    base + "/",
                    json={
                        "jsonrpc": "2.0",
                        "method": "GetTask",
                        "params": {"id": "probe-test"},
                        "id": "probe-1",
                    },
                    headers={"Content-Type": "application/json", "A2A-Version": "1.0"},
                )
                auth_required = "pass" if probe.status_code in (401, 403) else "fail"
            except Exception:
                auth_required = "fail"

    except Exception:
        pass

    return {"tls": tls, "auth_required": auth_required, "reachable": reachable}


# ── LLM analysis ──────────────────────────────────────────────────────────────

async def llm_card_analysis(payload: dict, anthropic_api_key: str) -> dict:
    """
    Calls Haiku for card/skill analysis. Returns {"summary": str, "findings": list}.
    Never raises — returns degraded result on any failure.
    """
    if not anthropic_api_key:
        return {"summary": "Card analysis unavailable — probes only (no API key configured).", "findings": []}

    try:
        import anthropic

        client = anthropic.Anthropic(api_key=anthropic_api_key)
        user_prompt = _build_user_prompt(payload)

        response = await asyncio.to_thread(
            client.messages.create,
            model="claude-haiku-4-5-20251001",
            max_tokens=1500,
            temperature=0,
            system=_SYSTEM_PROMPT,
            messages=[{"role": "user", "content": user_prompt}],
        )

        raw = response.content[0].text.strip()
        # Strip markdown fences if present
        if raw.startswith("```"):
            raw = raw.split("```")[1]
            if raw.startswith("json"):
                raw = raw[4:]
            raw = raw.strip()

        return json.loads(raw)

    except Exception as exc:
        return {
            "summary": f"Card analysis unavailable — probes only ({exc}).",
            "findings": [],
        }


# ── Score ─────────────────────────────────────────────────────────────────────

def compute_score(probes: dict, llm_findings: list) -> int:
    """
    Starts at 100. HTTP probes deduct up to 60, LLM findings up to 40.
    """
    score = 100

    if probes.get("tls") == "fail":
        score -= 30
    if probes.get("auth_required") == "fail":
        score -= 25
    if not probes.get("reachable", True):
        score -= 5

    llm_penalty = 0
    for f in llm_findings:
        risk = f.get("risk", "low")
        if risk == "high":
            llm_penalty += 20
        elif risk == "medium":
            llm_penalty += 10
    score -= min(llm_penalty, 40)

    return max(0, min(100, score))


# ── Probe findings (deterministic) ────────────────────────────────────────────

def _probe_findings(probes: dict) -> list:
    findings = []

    if probes.get("reachable"):
        findings.append({
            "id": "reachable",
            "label": "Reachability",
            "status": "pass",
            "risk": "low",
            "detail": "Agent endpoint responded successfully.",
            "recommendation": "No action needed.",
        })
    else:
        findings.append({
            "id": "reachable",
            "label": "Reachability",
            "status": "warn",
            "risk": "medium",
            "detail": "Agent endpoint did not respond — may be offline or unreachable.",
            "recommendation": "Verify the agent is deployed and network-reachable from the orchestrator.",
        })

    if probes.get("tls") == "pass":
        findings.append({
            "id": "tls",
            "label": "TLS Enforcement",
            "status": "pass",
            "risk": "low",
            "detail": "Endpoint uses HTTPS — traffic is encrypted in transit.",
            "recommendation": "No action needed.",
        })
    else:
        findings.append({
            "id": "tls",
            "label": "TLS Enforcement",
            "status": "fail",
            "risk": "high",
            "detail": "Endpoint uses HTTP — credentials and payloads are transmitted in plaintext.",
            "recommendation": "Move the agent to an HTTPS endpoint before using it in production.",
        })

    if probes.get("auth_required") == "pass":
        findings.append({
            "id": "auth",
            "label": "Auth Enforcement",
            "status": "pass",
            "risk": "low",
            "detail": "Endpoint requires authentication — unauthenticated requests are rejected.",
            "recommendation": "No action needed.",
        })
    else:
        findings.append({
            "id": "auth",
            "label": "Auth Enforcement",
            "status": "fail",
            "risk": "high",
            "detail": "Endpoint responds to unauthenticated requests — anyone can invoke this agent.",
            "recommendation": "Require a Bearer token on the agent endpoint.",
        })

    return findings


# ── Degraded analysis finding ─────────────────────────────────────────────────

def _degraded_finding() -> dict:
    return {
        "id": "analysis",
        "label": "Card Analysis",
        "status": "warn",
        "risk": "medium",
        "detail": "LLM card/skill analysis was unavailable — assessment based on HTTP probes only.",
        "recommendation": "Re-run the scan once the scanner API key is configured.",
    }


# ── Main entry ────────────────────────────────────────────────────────────────

async def run_scan(payload: dict, anthropic_api_key: str) -> dict:
    endpoint_url = payload.get("endpoint_url", "")
    has_auth_token = bool(payload.get("has_auth_token", False))

    probes, llm = await asyncio.gather(
        http_probes(endpoint_url, has_auth_token),
        llm_card_analysis(payload, anthropic_api_key),
    )

    probe_findings = _probe_findings(probes)
    llm_findings = llm.get("findings", [])
    degraded = not llm_findings and "probes only" in llm.get("summary", "")

    all_findings = probe_findings + llm_findings
    if degraded:
        all_findings.append(_degraded_finding())

    score = compute_score(probes, llm_findings)
    if degraded:
        score = max(0, score - 10)

    risk = "low" if score >= 80 else "medium" if score >= 50 else "high"
    summary = llm.get("summary") or _synthesize_summary(probes, score, risk)

    return {
        "score": score,
        "risk": risk,
        "summary": summary,
        "findings": all_findings,
        "http_probes": probes,
        "scanned_at": datetime.now(timezone.utc).isoformat(),
    }


def _synthesize_summary(probes: dict, score: int, risk: str) -> str:
    issues = []
    if probes.get("tls") == "fail":
        issues.append("no TLS encryption")
    if probes.get("auth_required") == "fail":
        issues.append("no auth enforcement")
    if not probes.get("reachable", True):
        issues.append("endpoint unreachable")
    if issues:
        return f"Agent has {' and '.join(issues)} — overall risk is {risk} (score {score}/100)."
    return f"Agent passed HTTP surface checks — overall risk is {risk} (score {score}/100)."
