"""
Runs API — query run history, steps, and usage.
Auth: JWT (any authenticated user sees their own runs; admin sees all).
"""

import uuid
from decimal import Decimal
from datetime import datetime
from typing import Any, Dict, List, Optional

from fastapi import APIRouter, Depends, HTTPException, Query, status
from pydantic import BaseModel
from sqlalchemy import select, func, text
from sqlalchemy.ext.asyncio import AsyncSession

from app.database import get_db
from app.models import Run, RunStep, RunUsage, Task, Artifact, TaskMessage
from app._deps import require_jwt

router = APIRouter(prefix="/runs", tags=["runs"])


# ------------------------------------------------------------------ #
# Pydantic schemas                                                     #
# ------------------------------------------------------------------ #

class RunStepOut(BaseModel):
    id: uuid.UUID
    iteration: int
    agent_slug: str
    tool_call_id: str
    input: Dict[str, Any]
    output: Optional[str]
    status: str
    error: Optional[str]
    latency_ms: Optional[int]
    started_at: datetime
    ended_at: Optional[datetime]

    class Config:
        from_attributes = True


class RunUsageOut(BaseModel):
    provider: str
    model: str
    tokens_input: int
    tokens_output: int
    cost_usd: Decimal

    class Config:
        from_attributes = True


class RunOut(BaseModel):
    id: uuid.UUID
    orchestrator_id: uuid.UUID
    orchestrator_name: str
    user_id: int
    session_id: uuid.UUID
    goal: str
    status: str
    final_output: Optional[str]
    error: Optional[str]
    iterations: int
    total_tokens_in: int
    total_tokens_out: int
    total_tokens: int = 0
    total_cost_usd: Decimal
    started_at: datetime
    ended_at: Optional[datetime]
    duration_ms: Optional[int] = None

    class Config:
        from_attributes = True


class RunDetailOut(RunOut):
    steps: List[RunStepOut] = []
    usage: List[RunUsageOut] = []


class ContextSession(BaseModel):
    context_id: uuid.UUID
    orchestrator_name: str
    turn_count: int
    title: str
    last_active: datetime


# ------------------------------------------------------------------ #
# Helpers                                                              #
# ------------------------------------------------------------------ #

def _run_to_out(row: Run) -> RunOut:
    tin  = row.total_tokens_in  or 0
    tout = row.total_tokens_out or 0
    duration_ms = None
    if row.ended_at and row.started_at:
        duration_ms = int((row.ended_at - row.started_at).total_seconds() * 1000)
    return RunOut(
        id=row.id,
        orchestrator_id=row.orchestrator_id,
        orchestrator_name=row.orchestrator_name,
        user_id=row.user_id,
        session_id=row.session_id,
        goal=row.goal,
        status=row.status,
        final_output=row.final_output,
        error=row.error,
        iterations=row.iterations,
        total_tokens_in=tin,
        total_tokens_out=tout,
        total_tokens=tin + tout,
        total_cost_usd=row.total_cost_usd,
        started_at=row.started_at,
        ended_at=row.ended_at,
        duration_ms=duration_ms,
    )


# ------------------------------------------------------------------ #
# Routes                                                               #
# ------------------------------------------------------------------ #

@router.get("", response_model=List[RunOut])
async def list_runs(
    orchestrator: Optional[str] = None,
    status_filter: Optional[str] = Query(None, alias="status"),
    limit: int = Query(50, ge=1, le=500),
    offset: int = Query(0, ge=0),
    user: dict = Depends(require_jwt),
    db: AsyncSession = Depends(get_db),
):
    """List runs. Admins see all; regular users see only their own."""
    q = select(Run).order_by(Run.started_at.desc()).limit(limit).offset(offset)

    is_admin = user.get("role") in ("admin", "superadmin", "super_admin")
    if not is_admin:
        q = q.where(Run.user_id == user["user_id"])

    if orchestrator:
        q = q.where(Run.orchestrator_name == orchestrator)
    if status_filter:
        q = q.where(Run.status == status_filter)

    result = await db.execute(q)
    return [_run_to_out(r) for r in result.scalars()]


@router.get("/stats")
async def run_stats(
    user: dict = Depends(require_jwt),
    db: AsyncSession = Depends(get_db),
):
    """Aggregate stats: total runs, by status, total cost."""
    is_admin = user.get("role") in ("admin", "superadmin", "super_admin")

    base = select(Run)
    if not is_admin:
        base = base.where(Run.user_id == user["user_id"])

    total = await db.scalar(select(func.count()).select_from(base.subquery()))

    status_counts = {}
    for s in ("running", "completed", "failed", "stopped"):
        q = select(func.count()).select_from(
            base.where(Run.status == s).subquery()
        )
        status_counts[s] = await db.scalar(q) or 0

    cost_q = select(func.sum(Run.total_cost_usd)).select_from(base.subquery())
    total_cost = await db.scalar(cost_q) or Decimal("0")

    return {
        "total": total,
        "by_status": {k: v for k, v in status_counts.items() if v > 0},
        "total_cost_usd": float(total_cost),
    }


@router.get("/contexts", response_model=List[ContextSession])
async def list_contexts(
    orchestrator: Optional[str] = None,
    limit: int = Query(50, ge=1, le=200),
    user: dict = Depends(require_jwt),
    db: AsyncSession = Depends(get_db),
):
    """List distinct conversation sessions (context_ids), most recent first."""
    is_admin = user.get("role") in ("admin", "superadmin", "super_admin")

    # Subquery: per context_id — turn count, last active, orchestrator name
    # Join to runs to get orchestrator_name; use the most recent run per context
    ctx_q = (
        select(
            Task.context_id,
            func.count(Task.id).label("turn_count"),
            func.max(Task.created_at).label("last_active"),
        )
        .where(Task.kind == "root")
        .group_by(Task.context_id)
    )
    if not is_admin:
        ctx_q = ctx_q.where(Task.user_id == user["user_id"])

    ctx_result = await db.execute(ctx_q.order_by(text("last_active DESC")).limit(limit))
    rows = ctx_result.all()

    sessions = []
    for row in rows:
        cid, turn_count, last_active = row

        # Orchestrator name — from the most recent run for this context
        run_row = await db.scalar(
            select(Run.orchestrator_name)
            .join(Task, Task.run_id == Run.id)
            .where(Task.context_id == cid, Task.kind == "root")
            .order_by(Run.started_at.desc())
            .limit(1)
        )
        orch_name = run_row or "unknown"
        if orchestrator and orch_name != orchestrator:
            continue

        # Title — first user message of the first turn
        first_msg = await db.scalar(
            select(TaskMessage.parts)
            .join(Task, TaskMessage.task_id == Task.id)
            .where(Task.context_id == cid, Task.kind == "root", TaskMessage.role == "user", TaskMessage.seq == 0)
            .order_by(Task.created_at.asc())
            .limit(1)
        )
        title = ""
        if first_msg:
            content = first_msg.get("content", "")
            if isinstance(content, list):
                # Anthropic-style parts list
                for part in content:
                    if isinstance(part, dict) and part.get("type") == "text":
                        title = part.get("text", "")[:80]
                        break
            elif isinstance(content, str):
                title = content[:80]
        if not title:
            title = f"Session {str(cid)[:8]}…"

        sessions.append(ContextSession(
            context_id=cid,
            orchestrator_name=orch_name,
            turn_count=turn_count,
            title=title,
            last_active=last_active,
        ))

    return sessions


@router.get("/{run_id}", response_model=RunDetailOut)
async def get_run(
    run_id: uuid.UUID,
    user: dict = Depends(require_jwt),
    db: AsyncSession = Depends(get_db),
):
    """Get run detail including steps and usage."""
    row = await db.get(Run, run_id)
    if row is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Run not found")

    is_admin = user.get("role") in ("admin", "superadmin", "super_admin")
    if not is_admin and row.user_id != user.get("user_id"):
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="Access denied")

    steps_result = await db.execute(
        select(RunStep).where(RunStep.run_id == run_id).order_by(RunStep.started_at)
    )
    usage_result = await db.execute(
        select(RunUsage).where(RunUsage.run_id == run_id).order_by(RunUsage.created_at)
    )

    out = RunDetailOut(**_run_to_out(row).model_dump())
    out.steps = [RunStepOut.model_validate(s) for s in steps_result.scalars()]
    out.usage = [RunUsageOut.model_validate(u) for u in usage_result.scalars()]
    return out


@router.delete("/{run_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_run(
    run_id: uuid.UUID,
    user: dict = Depends(require_jwt),
    db: AsyncSession = Depends(get_db),
):
    """Delete a run and its steps/usage (cascade). Admin only."""
    if user.get("role") not in ("admin", "superadmin", "super_admin"):
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="Admin role required")

    row = await db.get(Run, run_id)
    if row is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Run not found")

    await db.delete(row)
    await db.commit()


# ------------------------------------------------------------------ #
# Phase 6 — Task graph, artifacts, context inspector                  #
# ------------------------------------------------------------------ #

class TaskOut(BaseModel):
    id: uuid.UUID
    parent_task_id: Optional[uuid.UUID]
    agent_id: Optional[uuid.UUID]
    orchestrator_id: Optional[uuid.UUID]
    context_id: uuid.UUID
    state: str
    kind: str
    remote_task_id: Optional[str]
    budget_tokens: Optional[int]
    tokens_used: int
    error: Optional[str]
    created_at: datetime
    updated_at: datetime

    class Config:
        from_attributes = True


class ArtifactOut(BaseModel):
    id: uuid.UUID
    task_id: uuid.UUID
    context_id: uuid.UUID
    artifact_id: str
    name: Optional[str]
    parts: list
    append_index: int
    last_chunk: bool
    created_at: datetime

    class Config:
        from_attributes = True


def _is_admin(user: dict) -> bool:
    return user.get("role") in ("admin", "superadmin", "super_admin")


async def _load_run_authorized(run_id: uuid.UUID, user: dict, db: AsyncSession) -> Run:
    """Load a run by id; raise 404 if missing, 403 if caller lacks access."""
    row = await db.get(Run, run_id)
    if row is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Run not found")
    if not _is_admin(user) and row.user_id != user.get("user_id"):
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="Access denied")
    return row


@router.get("/{run_id}/tasks", response_model=List[TaskOut])
async def get_run_tasks(
    run_id: uuid.UUID,
    user: dict = Depends(require_jwt),
    db: AsyncSession = Depends(get_db),
):
    """Return the task graph for a run — all tasks ordered by created_at."""
    await _load_run_authorized(run_id, user, db)

    result = await db.execute(
        select(Task)
        .where(Task.run_id == run_id)
        .order_by(Task.created_at)
    )
    return [TaskOut.model_validate(t) for t in result.scalars()]


@router.get("/{run_id}/artifacts", response_model=List[ArtifactOut])
async def get_run_artifacts(
    run_id: uuid.UUID,
    user: dict = Depends(require_jwt),
    db: AsyncSession = Depends(get_db),
):
    """Return all artifacts for tasks linked to a run, ordered by created_at."""
    await _load_run_authorized(run_id, user, db)

    result = await db.execute(
        select(Artifact)
        .join(Task, Artifact.task_id == Task.id)
        .where(Task.run_id == run_id)
        .order_by(Artifact.created_at)
    )
    return [ArtifactOut.model_validate(a) for a in result.scalars()]


@router.get("/context/{context_id}/artifacts", response_model=List[ArtifactOut])
async def get_context_artifacts(
    context_id: uuid.UUID,
    limit: int = Query(100, ge=1, le=500),
    user: dict = Depends(require_jwt),
    db: AsyncSession = Depends(get_db),
):
    """Memory inspector — return the most recent artifacts for a context_id (last_chunk only)."""
    result = await db.execute(
        select(Artifact)
        .where(Artifact.context_id == context_id, Artifact.last_chunk.is_(True))
        .order_by(Artifact.created_at.desc())
        .limit(limit)
    )
    return [ArtifactOut.model_validate(a) for a in result.scalars()]
