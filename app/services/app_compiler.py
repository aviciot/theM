"""
Application graph compiler.

Accepts a graph (nodes + edges) from the frontend, validates it,
then compiles it into the relational tables:

  app_orchestrators  ← one row per orchestrator node (keyed by node_id)
  entry_points       ← one row per entryPoint node, FK → its orch
  allowed_agent_ids  ← derived from orch→agent edges (through middleware)
  middleware_wirings ← derived from orch→mw→agent chains

Temporal runtime is unchanged — it still reads app_orchestrators by name
and reads allowed_agent_ids from that row.

Graph node types:  entryPoint | orchestrator | agent | middleware
Graph edge rules:  EP→Orch, Orch→Orch, Orch→Agent, Orch→MW, MW→Agent, MW→MW
"""

import uuid
from typing import Any, Dict, List, Optional, Tuple

from fastapi import HTTPException, status
from pydantic import BaseModel, Field
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models import Agent, AppOrchestrator, EntryPoint, MiddlewareWiring
from app.utils.crypto import encrypt_value
from app.utils.logger import logger


# ── Pydantic schema for the graph payload ─────────────────────────────────────

class GraphNode(BaseModel):
    id: str                          # React Flow node id — stable canvas identity
    type: str                        # entryPoint | orchestrator | agent | middleware
    data: Dict[str, Any] = Field(default_factory=dict)


class GraphEdge(BaseModel):
    id: str = ""
    source: str
    target: str


class AppGraph(BaseModel):
    nodes: List[GraphNode] = Field(default_factory=list)
    edges: List[GraphEdge] = Field(default_factory=list)


# ── Constants ─────────────────────────────────────────────────────────────────

_VALID_NODE_TYPES = {"entryPoint", "orchestrator", "agent", "middleware"}
_SLUG_RE = __import__("re").compile(r"^[a-z0-9_-]{1,64}$")
_VALID_EP_TYPES = {"websocket", "sse", "webrtc", "a2a", "voice"}


# ── Validation ────────────────────────────────────────────────────────────────

def _err(msg: str, code: int = 422) -> HTTPException:
    return HTTPException(status_code=code, detail=msg)


def validate_graph(graph: AppGraph) -> None:
    """Pure structural validation — no DB reads. Raises 422 on any violation."""
    node_ids = {n.id for n in graph.nodes}
    node_by_id = {n.id: n for n in graph.nodes}

    # 1. Valid node types
    for n in graph.nodes:
        if n.type not in _VALID_NODE_TYPES:
            raise _err(f"Unknown node type '{n.type}' on node '{n.id}'")

    # 2. Every edge references existing nodes
    for e in graph.edges:
        if e.source not in node_ids:
            raise _err(f"Edge source '{e.source}' references unknown node")
        if e.target not in node_ids:
            raise _err(f"Edge target '{e.target}' references unknown node")

    # 3. At least one EP
    ep_nodes = [n for n in graph.nodes if n.type == "entryPoint"]
    if not ep_nodes:
        raise _err("Graph must contain at least one entryPoint node")

    # 4. EP slugs present, valid format, unique within graph
    seen_slugs: set[str] = set()
    for n in ep_nodes:
        slug = n.data.get("slug", "")
        if not slug:
            raise _err(f"entryPoint node '{n.id}' is missing a slug")
        if not _SLUG_RE.match(slug):
            raise _err(f"entryPoint slug '{slug}' must match ^[a-z0-9_-]{{1,64}}$")
        ep_type = n.data.get("epType", "")
        if ep_type not in _VALID_EP_TYPES:
            raise _err(f"entryPoint '{n.id}' has invalid epType '{ep_type}'")
        if slug in seen_slugs:
            raise _err(f"Duplicate slug '{slug}' within graph")
        seen_slugs.add(slug)

    # 5. Each EP has exactly one outgoing edge to an orchestrator
    for n in ep_nodes:
        out_edges = [e for e in graph.edges if e.source == n.id]
        if len(out_edges) == 0:
            raise _err(f"entryPoint '{n.data.get('slug', n.id)}' is not connected to an orchestrator")
        orch_targets = [e for e in out_edges if node_by_id.get(e.target, GraphNode(id='', type='')).type == "orchestrator"]
        if len(orch_targets) == 0:
            raise _err(f"entryPoint '{n.data.get('slug', n.id)}' must connect to an orchestrator node")

    # 6. No orphan orchestrators (every orch must be reachable from at least one EP or another orch)
    orch_nodes = [n for n in graph.nodes if n.type == "orchestrator"]
    reachable_orch_ids: set[str] = set()
    for e in graph.edges:
        tgt = node_by_id.get(e.target)
        if tgt and tgt.type == "orchestrator":
            reachable_orch_ids.add(e.target)
    for n in orch_nodes:
        if n.id not in reachable_orch_ids:
            raise _err(f"Orchestrator node '{n.id}' ('{n.data.get('displayName', '')}') has no entry point or orchestrator pointing to it")


# ── Agent reachability ────────────────────────────────────────────────────────

def _resolve_agents_for_orch(orch_id: str, graph: AppGraph) -> List[str]:
    """
    Return agent_ids reachable from orch_id following outgoing edges.
    Traverses through middleware nodes transparently.
    """
    node_by_id = {n.id: n for n in graph.nodes}
    agent_ids: list[str] = []
    visited: set[str] = set()

    def _walk(node_id: str) -> None:
        if node_id in visited:
            return
        visited.add(node_id)
        node = node_by_id.get(node_id)
        if node is None:
            return
        for edge in graph.edges:
            if edge.source != node_id:
                continue
            target = node_by_id.get(edge.target)
            if target is None:
                continue
            if target.type == "agent":
                agent_id = target.data.get("agentId") or target.data.get("agent_id")
                if agent_id and agent_id not in agent_ids:
                    agent_ids.append(agent_id)
            elif target.type in ("middleware", "orchestrator"):
                _walk(target.id)

    _walk(orch_id)
    return agent_ids


def _resolve_mw_chains(orch_id: str, graph: AppGraph) -> List[Tuple[str, str, int, str, Dict]]:
    """
    Return middleware wiring tuples for orch_id:
    [(def_id, agent_id, position, mw_node_id, config_override), ...]
    """
    node_by_id = {n.id: n for n in graph.nodes}
    result: list[tuple[str, str, int, str, dict]] = []

    # Flat approach: for each orch→mw edge, follow the chain
    for edge in graph.edges:
        if edge.source != orch_id:
            continue
        mw_node = node_by_id.get(edge.target)
        if mw_node is None or mw_node.type != "middleware":
            continue
        # Follow the mw chain to find the terminal agent
        chain: list[str] = [mw_node.id]
        current = mw_node.id
        while True:
            next_edges = [e for e in graph.edges if e.source == current]
            if not next_edges:
                break
            nxt = node_by_id.get(next_edges[0].target)
            if nxt is None:
                break
            if nxt.type == "agent":
                agent_id = nxt.data.get("agentId") or nxt.data.get("agent_id")
                if agent_id:
                    for pos, mw_id in enumerate(chain):
                        mw = node_by_id[mw_id]
                        def_id = mw.data.get("defId") or mw.data.get("def_id", "")
                        config = mw.data.get("configOverride") or mw.data.get("config_override") or {}
                        result.append((def_id, agent_id, pos, mw_id, config))
                break
            elif nxt.type == "middleware":
                chain.append(nxt.id)
                current = nxt.id
            else:
                break

    return result


# ── Main compiler ─────────────────────────────────────────────────────────────

async def compile_graph(
    db: AsyncSession,
    app_id: uuid.UUID,
    graph: AppGraph,
    all_orch_names: set[str],
    existing_entry_points: List[EntryPoint],
    existing_ao_list: List[AppOrchestrator],
    exclude_app_id: Optional[uuid.UUID] = None,
) -> List[str]:
    """
    Compile graph into relational tables within the caller's transaction.
    Returns list of app_orchestrator names that were touched (for cache flush).

    Order:
    1. Validate cross-DB (slug conflicts, agent existence)
    2. Upsert app_orchestrators keyed by node_id
    3. Delete removed orchestrators
    4. Upsert entry_points keyed by slug
    5. Delete removed entry_points
    6. Replace middleware_wirings
    """
    node_by_id = {n.id: n for n in graph.nodes}
    ep_nodes = [n for n in graph.nodes if n.type == "entryPoint"]
    orch_nodes = [n for n in graph.nodes if n.type == "orchestrator"]

    # ── 1. Cross-DB validation ────────────────────────────────────────────────

    # Slug conflict check against other apps
    slugs = [n.data["slug"] for n in ep_nodes]
    q = select(EntryPoint).where(EntryPoint.slug.in_(slugs))
    if exclude_app_id:
        q = q.where(EntryPoint.application_id != exclude_app_id)
    conflict = (await db.execute(q)).scalars().first()
    if conflict:
        raise _err(f"Slug '{conflict.slug}' is already used by another application", 409)

    # Agent existence check
    all_agent_ids_in_graph = [
        n.data.get("agentId") or n.data.get("agent_id")
        for n in graph.nodes if n.type == "agent"
    ]
    all_agent_ids_in_graph = [a for a in all_agent_ids_in_graph if a]
    if all_agent_ids_in_graph:
        try:
            agent_uuids = [uuid.UUID(str(a)) for a in all_agent_ids_in_graph]
        except ValueError as exc:
            raise _err(f"Invalid agent_id in graph: {exc}")
        result = await db.execute(
            select(Agent.id).where(Agent.id.in_(agent_uuids), Agent.enabled == True)  # noqa: E712
        )
        found = {str(r) for r in result.scalars()}
        missing = [a for a in all_agent_ids_in_graph if a not in found]
        if missing:
            raise _err(f"Agent(s) not found or disabled: {missing}")

    # ── 2. Upsert app_orchestrators (keyed by node_id) ────────────────────────

    existing_ao_by_node_id = {ao.node_id: ao for ao in existing_ao_list}
    # Also index by AO DB id — frontend always sends appOrchestratorId in node data,
    # so we can match existing rows even when the canvas node_id differs from what
    # migration 018 backfilled (e.g. "orch_<uuid>" vs "orch-<uuid>").
    existing_ao_by_db_id = {str(ao.id): ao for ao in existing_ao_list}
    touched_names: list[str] = []
    node_id_to_ao: dict[str, AppOrchestrator] = {}

    # Derive delegatable from graph structure: an orch is delegatable iff another
    # orch has an outgoing edge to it. No checkbox needed — the edge IS the declaration.
    orch_node_ids = {n.id for n in orch_nodes}
    delegatable_node_ids = {
        e.target for e in graph.edges
        if e.source in orch_node_ids and e.target in orch_node_ids
    }

    for orch_node in orch_nodes:
        node_id = orch_node.id
        d = orch_node.data
        ao_db_id = d.get("appOrchestratorId") or d.get("app_orchestrator_id")
        existing_ao = (
            existing_ao_by_node_id.get(node_id)
            or (existing_ao_by_db_id.get(str(ao_db_id)) if ao_db_id else None)
        )
        # If matched by DB id with a different node_id, update so future saves use exact match
        if existing_ao and existing_ao.node_id != node_id:
            existing_ao.node_id = node_id

        # Derive allowed_agent_ids and delegatable from edges — not from node data
        derived_agent_ids = _resolve_agents_for_orch(node_id, graph)
        is_delegatable = node_id in delegatable_node_ids

        if existing_ao is not None:
            # Update mutable fields — name is immutable
            _apply_orch_data(existing_ao, d, derived_agent_ids, is_delegatable)
            node_id_to_ao[node_id] = existing_ao
            touched_names.append(existing_ao.name)
        else:
            # Create new AO
            import re as _re, secrets as _secrets
            proposed = d.get("name")
            hint = d.get("displayName") or d.get("display_name") or node_id
            ao_name = _generate_orch_name(proposed, hint, all_orch_names, _re, _secrets)
            all_orch_names.add(ao_name)

            _raw_key = d.get("llmApiKey") or d.get("llm_api_key")
            _raw_transcription_key = d.get("transcriptionApiKey") or d.get("transcription_api_key")
            _raw_tts_key = d.get("ttsApiKey") or d.get("tts_api_key")
            _transcription_provider = d.get("transcriptionProvider") or d.get("transcription_provider")
            _tts_provider = d.get("ttsProvider") or d.get("tts_provider")
            ao = AppOrchestrator(
                application_id=app_id,
                node_id=node_id,
                name=ao_name,
                display_name=d.get("displayName") or d.get("display_name"),
                system_prompt=d.get("systemPrompt") or d.get("system_prompt"),
                llm_provider=d.get("llmProvider") or d.get("llm_provider"),
                llm_model=d.get("llmModel") or d.get("llm_model"),
                llm_api_key_encrypted=encrypt_value(_raw_key) if _raw_key else None,
                max_iterations=d.get("maxIterations") or d.get("max_iterations") or 10,
                max_parallel_tools=d.get("maxParallelTools") or d.get("max_parallel_tools") or 3,
                history_window=d.get("historyWindow") or d.get("history_window") or 20,
                delegatable=is_delegatable,
                kind=d.get("kind") or "standard",
                budget_tokens=d.get("budgetTokens") or d.get("budget_tokens"),
                allowed_agent_ids=[str(a) for a in derived_agent_ids],
                enabled=True,
                transcription_provider=_transcription_provider,
                transcription_model=d.get("transcriptionModel") or d.get("transcription_model"),
                transcription_api_key_encrypted=encrypt_value(_raw_transcription_key) if _raw_transcription_key else None,
                tts_provider=_tts_provider,
                tts_voice=d.get("ttsVoice") or d.get("tts_voice"),
                tts_api_key_encrypted=encrypt_value(_raw_tts_key) if _raw_tts_key else None,
                voice_enabled=bool(_transcription_provider),
                tts_enabled=bool(_tts_provider),
            )
            db.add(ao)
            await db.flush()
            node_id_to_ao[node_id] = ao
            touched_names.append(ao_name)

    # ── 3. Delete removed orchestrators ──────────────────────────────────────

    desired_node_ids = {n.id for n in orch_nodes}
    for ao in existing_ao_list:
        if ao.node_id not in desired_node_ids:
            touched_names.append(ao.name)
            await db.delete(ao)

    await db.flush()

    # ── 4. Upsert entry_points (keyed by slug) ────────────────────────────────

    existing_ep_by_slug = {ep.slug: ep for ep in existing_entry_points}
    desired_slugs: set[str] = set()

    for ep_node in ep_nodes:
        d = ep_node.data
        slug = d["slug"]
        desired_slugs.add(slug)

        # Find which orch this EP connects to
        orch_edge = next(
            (e for e in graph.edges
             if e.source == ep_node.id
             and node_by_id.get(e.target, GraphNode(id='', type='')).type == "orchestrator"),
            None,
        )
        if orch_edge is None:
            raise _err(f"entryPoint '{slug}' has no edge to an orchestrator")
        ao = node_id_to_ao.get(orch_edge.target)
        if ao is None:
            raise _err(f"entryPoint '{slug}' points to unknown orchestrator node '{orch_edge.target}'")

        ep_type = d.get("epType") or d.get("entry_point_type") or "websocket"
        access_mode = d.get("accessMode") or d.get("access_mode") or "token"
        access_policy = d.get("accessPolicy") or d.get("access_policy") or {"mode": access_mode}
        token_limit_raw = d.get("convTokenLimit") or d.get("conversation_token_limit")
        token_limit: Optional[int] = None
        if token_limit_raw is not None and str(token_limit_raw).strip() not in ("", "null", "None"):
            try:
                token_limit = int(token_limit_raw)
            except (ValueError, TypeError):
                pass

        max_concurrent_raw = d.get("maxConcurrentSessions") or d.get("max_concurrent_sessions")
        max_concurrent: Optional[int] = None
        if max_concurrent_raw is not None and str(max_concurrent_raw).strip() not in ("", "null", "None"):
            try:
                max_concurrent = int(max_concurrent_raw)
            except (ValueError, TypeError):
                pass

        queue_timeout_raw = d.get("queueTimeout") or d.get("queue_timeout_seconds")
        queue_timeout: Optional[int] = None
        if queue_timeout_raw is not None and str(queue_timeout_raw).strip() not in ("", "null", "None"):
            try:
                queue_timeout = int(queue_timeout_raw)
            except (ValueError, TypeError):
                pass

        queue_msg = d.get("queueMessage") or d.get("queue_message") or None
        if queue_msg is not None:
            queue_msg = str(queue_msg).strip() or None

        existing_ep = existing_ep_by_slug.get(slug)
        if existing_ep is not None:
            existing_ep.entry_point_type = ep_type
            existing_ep.access_policy = access_policy
            existing_ep.conversation_token_limit = token_limit
            existing_ep.max_concurrent_sessions = max_concurrent
            existing_ep.queue_timeout_seconds = queue_timeout
            existing_ep.queue_message = queue_msg
            existing_ep.app_orchestrator_id = ao.id
        else:
            db.add(EntryPoint(
                application_id=app_id,
                slug=slug,
                entry_point_type=ep_type,
                access_policy=access_policy,
                conversation_token_limit=token_limit,
                max_concurrent_sessions=max_concurrent,
                queue_timeout_seconds=queue_timeout,
                queue_message=queue_msg,
                enabled=True,
                app_orchestrator_id=ao.id,
            ))

    # ── 5. Delete removed entry_points ────────────────────────────────────────

    for slug, ep in existing_ep_by_slug.items():
        if slug not in desired_slugs:
            await db.delete(ep)

    await db.flush()

    # ── 6. Replace middleware_wirings ─────────────────────────────────────────

    await db.execute(
        __import__("sqlalchemy", fromlist=["delete"]).delete(MiddlewareWiring).where(
            MiddlewareWiring.application_id == app_id
        )
    )

    for orch_node in orch_nodes:
        ao = node_id_to_ao.get(orch_node.id)
        if ao is None:
            continue
        chains = _resolve_mw_chains(orch_node.id, graph)
        for (def_id_str, agent_id_str, position, mw_node_id, config_override) in chains:
            if not def_id_str or not agent_id_str:
                continue
            try:
                def_uuid = uuid.UUID(str(def_id_str))
                agent_uuid = uuid.UUID(str(agent_id_str))
            except ValueError:
                continue
            db.add(MiddlewareWiring(
                application_id=app_id,
                agent_id=agent_uuid,
                def_id=def_uuid,
                position=position,
                config_override=config_override,
                node_id=mw_node_id,
                enabled=True,
            ))

    await db.flush()
    return touched_names


# ── Helpers ───────────────────────────────────────────────────────────────────

def _apply_orch_data(ao: AppOrchestrator, d: Dict[str, Any], derived_agent_ids: List[str], delegatable: bool = False) -> None:
    """Apply graph node data onto an existing AppOrchestrator row. Name is immutable."""
    def _get(key1: str, key2: str, default: Any = None) -> Any:
        v = d.get(key1)
        return v if v is not None else d.get(key2, default)

    display_name = _get("displayName", "display_name")
    if display_name is not None:
        ao.display_name = display_name
    system_prompt = _get("systemPrompt", "system_prompt")
    if system_prompt is not None:
        ao.system_prompt = system_prompt
    llm_provider = _get("llmProvider", "llm_provider")
    if llm_provider is not None:
        ao.llm_provider = llm_provider
    llm_model = _get("llmModel", "llm_model")
    if llm_model is not None:
        ao.llm_model = llm_model
    max_iter = _get("maxIterations", "max_iterations")
    if max_iter is not None:
        ao.max_iterations = int(max_iter)
    max_par = _get("maxParallelTools", "max_parallel_tools")
    if max_par is not None:
        ao.max_parallel_tools = int(max_par)
    hw = _get("historyWindow", "history_window")
    if hw is not None:
        ao.history_window = int(hw)
    ao.delegatable = delegatable  # always derived from graph structure, not node data
    kind = d.get("kind")
    if kind is not None:
        ao.kind = kind
    bt = _get("budgetTokens", "budget_tokens")
    if bt is not None:
        ao.budget_tokens = int(bt)
    api_key = _get("llmApiKey", "llm_api_key")
    if api_key:
        ao.llm_api_key_encrypted = encrypt_value(api_key)
    transcription_provider = _get("transcriptionProvider", "transcription_provider")
    if transcription_provider is not None:
        ao.transcription_provider = transcription_provider or None
    transcription_model = _get("transcriptionModel", "transcription_model")
    if transcription_model is not None:
        ao.transcription_model = transcription_model or None
    tts_provider = _get("ttsProvider", "tts_provider")
    if tts_provider is not None:
        ao.tts_provider = tts_provider or None
    tts_voice = _get("ttsVoice", "tts_voice")
    if tts_voice is not None:
        ao.tts_voice = tts_voice or None
    transcription_api_key = _get("transcriptionApiKey", "transcription_api_key")
    if transcription_api_key:
        ao.transcription_api_key_encrypted = encrypt_value(transcription_api_key)
    tts_api_key = _get("ttsApiKey", "tts_api_key")
    if tts_api_key:
        ao.tts_api_key_encrypted = encrypt_value(tts_api_key)
    # Derive enable flags from provider presence (always set so clearing a provider disables voice)
    ao.voice_enabled = bool(ao.transcription_provider)
    ao.tts_enabled = bool(ao.tts_provider)
    # Always overwrite allowed_agent_ids from derived edges
    ao.allowed_agent_ids = [str(a) for a in derived_agent_ids]


def _generate_orch_name(proposed, hint, db_names, _re, _secrets) -> str:
    import re as re_mod
    _NAME_RE = re_mod.compile(r"^[a-z0-9_-]{1,64}$")
    if proposed:
        name = proposed.strip().lower()
        if not _NAME_RE.match(name):
            raise HTTPException(422, f"Invalid orchestrator name '{name}'")
        if name in db_names:
            raise HTTPException(409, f"Orchestrator name '{name}' already taken")
        return name
    base = re_mod.sub(r"[^a-z0-9_-]", "-", hint.strip().lower()).strip("-_")[:55] or "orch"
    if base not in db_names:
        return base
    for _ in range(10):
        cand = f"{base}-{_secrets.token_hex(3)}"[:64]
        if cand not in db_names:
            return cand
    raise HTTPException(500, "Could not allocate unique orchestrator name")


# ── Graph export (application → AppGraph) ────────────────────────────────────

def export_graph(
    entry_points: List[EntryPoint],
    ao_list: List[AppOrchestrator],
    mw_wirings: List[MiddlewareWiring],
    canvas: Optional[Dict[str, Any]] = None,
) -> Dict[str, Any]:
    """
    Convert relational rows back into a nodes+edges graph.
    This is the inverse of compile_graph — used for export and GET responses.
    """
    nodes: list[dict] = []
    edges: list[dict] = []
    emitted_agent_ids: set[str] = set()
    ao_by_id = {str(ao.id): ao for ao in ao_list}

    for ao in ao_list:
        nodes.append({
            "id": ao.node_id,
            "type": "orchestrator",
            "data": {
                "appOrchestratorId": str(ao.id),
                "displayName": ao.display_name or ao.name,
                "systemPrompt": ao.system_prompt,
                "llmProvider": ao.llm_provider,
                "llmModel": ao.llm_model,
                "maxIterations": ao.max_iterations,
                "maxParallelTools": ao.max_parallel_tools,
                "historyWindow": ao.history_window,
                "delegatable": ao.delegatable,
                "kind": ao.kind,
                "budgetTokens": ao.budget_tokens,
                "name": ao.name,
                "transcriptionProvider": ao.transcription_provider,
                "transcriptionModel": ao.transcription_model,
                "ttsProvider": ao.tts_provider,
                "ttsVoice": ao.tts_voice,
            },
        })
        for agent_id in (ao.allowed_agent_ids or []):
            if agent_id not in emitted_agent_ids:
                emitted_agent_ids.add(agent_id)
                nodes.append({
                    "id": f"agent_{agent_id}",
                    "type": "agent",
                    "data": {"agentId": agent_id},
                })
            edges.append({
                "id": f"e_orch_agent_{ao.node_id}_{agent_id}",
                "source": ao.node_id,
                "target": f"agent_{agent_id}",
            })

    for ep in entry_points:
        ep_node_id = f"ep_{ep.slug}"
        nodes.append({
            "id": ep_node_id,
            "type": "entryPoint",
            "data": {
                "epId": str(ep.id),
                "slug": ep.slug,
                "epType": ep.entry_point_type,
                "accessMode": (ep.access_policy or {}).get("mode", "token"),
                "convTokenLimit": ep.conversation_token_limit,
                "maxConcurrentSessions": ep.max_concurrent_sessions,
                "queueTimeout": ep.queue_timeout_seconds,
                "queueMessage": ep.queue_message,
            },
        })
        if ep.app_orchestrator_id:
            ao = ao_by_id.get(str(ep.app_orchestrator_id))
            if ao:
                edges.append({
                    "id": f"e_ep_{ep.slug}",
                    "source": ep_node_id,
                    "target": ao.node_id,
                })

    # Middleware nodes + edges
    for mw in mw_wirings:
        if not mw.node_id:
            continue
        nodes.append({
            "id": mw.node_id,
            "type": "middleware",
            "data": {
                "defId": str(mw.def_id),
                "configOverride": mw.config_override or {},
                "enabled": mw.enabled,
            },
        })

    return {
        "nodes": nodes,
        "edges": edges,
        "canvas": canvas or {},
    }
