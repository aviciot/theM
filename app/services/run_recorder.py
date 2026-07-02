"""
Run recorder — writes run lifecycle to odin.runs, odin.run_steps, odin.run_usage.
All methods are fire-and-forget safe: they log errors but never raise.
"""

import uuid
from datetime import datetime, timezone
from decimal import Decimal
from typing import Optional

from sqlalchemy import update, func
from sqlalchemy.ext.asyncio import AsyncSession

from app.models import Run, RunStep, RunUsage
from app.services.providers.base import ToolCall, TokenUsage
from app.utils.logger import logger


def _now() -> datetime:
    return datetime.now(timezone.utc)


async def start_run(
    db: AsyncSession,
    *,
    orchestrator_id: uuid.UUID,
    orchestrator_name: str,
    user_id: int,
    session_id: uuid.UUID,
    goal: str,
) -> uuid.UUID:
    """Insert a new run row (status=running). Returns run_id."""
    run = Run(
        orchestrator_id=orchestrator_id,
        orchestrator_name=orchestrator_name,
        user_id=user_id,
        session_id=session_id,
        goal=goal,
        status="running",
    )
    db.add(run)
    await db.commit()
    await db.refresh(run)
    logger.info("run started", run_id=str(run.id), orchestrator=orchestrator_name)
    return run.id


async def record_step(
    db: AsyncSession,
    *,
    run_id: uuid.UUID,
    iteration: int,
    agent_id: Optional[uuid.UUID],
    agent_slug: str,
    tool_call_id: str,
    input: dict,
) -> uuid.UUID:
    """Insert a run_step row (status=running). Returns step_id."""
    try:
        step = RunStep(
            run_id=run_id,
            iteration=iteration,
            agent_id=agent_id,
            agent_slug=agent_slug,
            tool_call_id=tool_call_id,
            input=input,
            status="running",
            started_at=_now(),
        )
        db.add(step)
        await db.commit()
        await db.refresh(step)
        return step.id
    except Exception as exc:
        logger.error("record_step failed", run_id=str(run_id), error=str(exc))
        await db.rollback()
        return uuid.uuid4()  # return dummy so caller doesn't crash


async def complete_step(
    db: AsyncSession,
    *,
    step_id: uuid.UUID,
    output: Optional[str],
    status: str = "completed",
    error: Optional[str] = None,
    latency_ms: Optional[int] = None,
) -> None:
    """Update run_step to completed/failed."""
    try:
        await db.execute(
            update(RunStep)
            .where(RunStep.id == step_id)
            .values(
                output=output,
                status=status,
                error=error,
                latency_ms=latency_ms,
                ended_at=_now(),
            )
        )
        await db.commit()
    except Exception as exc:
        logger.error("complete_step failed", step_id=str(step_id), error=str(exc))
        await db.rollback()


async def record_usage(
    db: AsyncSession,
    *,
    run_id: uuid.UUID,
    user_id: int,
    provider: str,
    model: str,
    usage: TokenUsage,
    cost_usd: Decimal = Decimal("0"),
) -> None:
    """Insert a run_usage row."""
    try:
        row = RunUsage(
            run_id=run_id,
            user_id=user_id,
            provider=provider,
            model=model,
            tokens_input=usage.input_tokens,
            tokens_output=usage.output_tokens,
            cost_usd=cost_usd,
        )
        db.add(row)
        await db.commit()
    except Exception as exc:
        logger.error("record_usage failed", run_id=str(run_id), error=str(exc))
        await db.rollback()


async def complete_run(
    db: AsyncSession,
    *,
    run_id: uuid.UUID,
    status: str,
    final_output: Optional[str] = None,
    error: Optional[str] = None,
    iterations: int = 0,
    total_tokens_in: int = 0,
    total_tokens_out: int = 0,
    total_cost_usd: Decimal = Decimal("0"),
) -> None:
    """Update run to completed/failed/stopped."""
    try:
        await db.execute(
            update(Run)
            .where(Run.id == run_id)
            .values(
                status=status,
                final_output=final_output,
                error=error,
                iterations=iterations,
                total_tokens_in=total_tokens_in,
                total_tokens_out=total_tokens_out,
                total_cost_usd=total_cost_usd,
                ended_at=_now(),
            )
        )
        await db.commit()
        logger.info("run completed", run_id=str(run_id), status=status, iterations=iterations)
    except Exception as exc:
        logger.error("complete_run failed", run_id=str(run_id), error=str(exc))
        await db.rollback()
