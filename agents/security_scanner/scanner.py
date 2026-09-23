"""
Security scanner logic — HTTP surface probes only.
No A2A imports. Called from main.py execute().

The LLM card/skill risk analysis step used to run here (llm_card_analysis,
calling Anthropic directly with a single hardcoded ANTHROPIC_API_KEY shared
by every tenant). It now runs in go-bridge instead
(internal/admin/security_scan_llm.go, llmCardAnalysis) so a tenant's own
"security_scanner" general/custom mode config (see
docs/TENANT_LLM_PROVIDERS_PLAN.md) can be used instead of one shared
platform key. This scanner is now HTTP-probes-only; go-bridge's
runScanJob merges this result with its own LLM analysis before persisting.
"""

from datetime import datetime, timezone

import httpx

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


# ── Score ─────────────────────────────────────────────────────────────────────

def compute_score(probes: dict) -> int:
    """
    Starts at 100. HTTP probes deduct up to 60. The LLM-findings penalty
    (up to 40, plus the degraded-analysis -10) is now applied in Go, after
    llmCardAnalysis runs — see mergeSecurityScanResult in scanjob.go.
    """
    score = 100

    if probes.get("tls") == "fail":
        score -= 30
    if probes.get("auth_required") == "fail":
        score -= 25
    if not probes.get("reachable", True):
        score -= 5

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


# ── Main entry ────────────────────────────────────────────────────────────────

async def run_scan(payload: dict) -> dict:
    """
    Probes-only result. go-bridge's runScanJob merges this with its own
    llmCardAnalysis output (findings, summary, score adjustment) before
    persisting the final ScanResult.
    """
    endpoint_url = payload.get("endpoint_url", "")

    probes = await http_probes(endpoint_url, bool(payload.get("has_auth_token", False)))
    probe_findings = _probe_findings(probes)
    score = compute_score(probes)
    risk = "low" if score >= 80 else "medium" if score >= 50 else "high"
    summary = _synthesize_summary(probes, score, risk)

    return {
        "score": score,
        "risk": risk,
        "summary": summary,
        "findings": probe_findings,
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
