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
from sqlalchemy import select, func
from sqlalchemy.ext.asyncio import AsyncSession

from app.database import get_db
from app.models import Run, RunStep, RunUsage
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
    total_cost_usd: Decimal
    started_at: datetime
    ended_at: Optional[datetime]

    class Config:
        from_attributes = True


class RunDetailOut(RunOut):
    steps: List[RunStepOut] = []
    usage: List[RunUsageOut] = []


# ------------------------------------------------------------------ #
# Helpers                                                              #
# ------------------------------------------------------------------ #

def _run_to_out(row: Run) -> RunOut:
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
        total_tokens_in=row.total_tokens_in,
        total_tokens_out=row.total_tokens_out,
        total_cost_usd=row.total_cost_usd,
        started_at=row.started_at,
        ended_at=row.ended_at,
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

    is_admin = user.get("role") in ("admin", "superadmin")
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
    is_admin = user.get("role") in ("admin", "superadmin")

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
        "total_runs": total,
        "by_status": status_counts,
        "total_cost_usd": str(total_cost),
    }


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

    is_admin = user.get("role") in ("admin", "superadmin")
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
    if user.get("role") not in ("admin", "superadmin"):
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="Admin role required")

    row = await db.get(Run, run_id)
    if row is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Run not found")

    await db.delete(row)
    await db.commit()
