"""
SQLAlchemy 2.0 ORM models for the them schema.
"""

import uuid
from datetime import datetime
from decimal import Decimal
from typing import Any, Dict, List, Optional

from sqlalchemy import (
    BigInteger, Boolean, DateTime, Integer, Numeric,
    String, Text, func, ForeignKey,
)
from sqlalchemy.dialects.postgresql import ARRAY, JSONB, UUID
from sqlalchemy.orm import Mapped, mapped_column, relationship

from app.database import Base


class LLMProvider(Base):
    __tablename__ = "llm_providers"
    __table_args__ = {"schema": "them"}

    id: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=True)
    name: Mapped[str] = mapped_column(Text, nullable=False, unique=True)
    display_name: Mapped[str] = mapped_column(Text, nullable=False)
    api_key_encrypted: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    base_url: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    default_model: Mapped[str] = mapped_column(Text, nullable=False)
    model_pricing: Mapped[Dict[str, Any]] = mapped_column(JSONB, nullable=False, default=dict)
    enabled: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now(), onupdate=func.now())


class Config(Base):
    __tablename__ = "config"
    __table_args__ = {"schema": "them"}

    config_key: Mapped[str] = mapped_column(Text, primary_key=True)
    config_value: Mapped[Dict[str, Any]] = mapped_column(JSONB, nullable=False)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now(), onupdate=func.now())


class Agent(Base):
    __tablename__ = "agents"
    __table_args__ = {"schema": "them"}

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    slug: Mapped[str] = mapped_column(Text, nullable=False, unique=True)
    display_name: Mapped[str] = mapped_column(Text, nullable=False)
    description: Mapped[str] = mapped_column(Text, nullable=False)
    transport: Mapped[str] = mapped_column(Text, nullable=False, default="a2a_async")
    endpoint_url: Mapped[str] = mapped_column(Text, nullable=False)
    auth_token_encrypted: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    input_schema: Mapped[Dict[str, Any]] = mapped_column(JSONB, nullable=False, default=dict)
    timeout_seconds: Mapped[int] = mapped_column(Integer, nullable=False, default=120)
    max_concurrency: Mapped[int] = mapped_column(Integer, nullable=False, default=4)
    enabled: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    tags: Mapped[List[str]] = mapped_column(ARRAY(Text), nullable=False, default=list)
    agent_card: Mapped[Optional[Dict[str, Any]]] = mapped_column(JSONB, nullable=True)
    agent_card_url: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    card_fetched_at: Mapped[Optional[datetime]] = mapped_column(DateTime(timezone=True), nullable=True)
    skills: Mapped[List[Dict[str, Any]]] = mapped_column(JSONB, nullable=False, default=list)
    supports_streaming: Mapped[bool] = mapped_column(Boolean, nullable=False, default=False)
    supports_push: Mapped[bool] = mapped_column(Boolean, nullable=False, default=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now(), onupdate=func.now())

    run_steps: Mapped[List["RunStep"]] = relationship("RunStep", back_populates="agent")


class Orchestrator(Base):
    __tablename__ = "orchestrators"
    __table_args__ = {"schema": "them"}

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    name: Mapped[str] = mapped_column(Text, nullable=False, unique=True)
    display_name: Mapped[str] = mapped_column(Text, nullable=False)
    system_prompt: Mapped[str] = mapped_column(Text, nullable=False, default="")
    allowed_agent_ids: Mapped[List[uuid.UUID]] = mapped_column(ARRAY(UUID(as_uuid=True)), nullable=False, default=list)
    llm_provider: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    llm_model: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    llm_api_key_encrypted: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    llm_base_url: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    max_iterations: Mapped[int] = mapped_column(Integer, nullable=False, default=10)
    max_parallel_tools: Mapped[int] = mapped_column(Integer, nullable=False, default=4)
    rate_limit_rpm: Mapped[int] = mapped_column(Integer, nullable=False, default=30)
    daily_budget_usd: Mapped[Decimal] = mapped_column(Numeric(10, 4), nullable=False, default=0)
    enabled: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    a2a_exposed: Mapped[bool] = mapped_column(Boolean, nullable=False, default=False)
    voice_enabled: Mapped[bool] = mapped_column(Boolean, nullable=False, default=False)
    transcription_provider: Mapped[Optional[str]] = mapped_column(String(32), nullable=True)
    transcription_model: Mapped[Optional[str]] = mapped_column(String(64), nullable=True)
    transcription_api_key_encrypted: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    tts_enabled: Mapped[bool] = mapped_column(Boolean, nullable=False, default=False)
    tts_provider: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    tts_voice: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    tts_api_key_encrypted: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    memory_enabled: Mapped[bool] = mapped_column(Boolean, nullable=False, default=False)
    summarize_every_n_calls: Mapped[int] = mapped_column(Integer, nullable=False, default=3)
    memory_raw_fallback_n: Mapped[int] = mapped_column(Integer, nullable=False, default=5)
    summarizer_provider: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    summarizer_model: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    summarizer_api_key_encrypted: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    edges: Mapped[List[str]] = mapped_column(ARRAY(Text), nullable=False, default=lambda: ["websocket"])
    budget_tokens: Mapped[Optional[int]] = mapped_column(Integer, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now(), onupdate=func.now())

    runs: Mapped[List["Run"]] = relationship("Run", back_populates="orchestrator")
    access_tokens: Mapped[List["AccessToken"]] = relationship("AccessToken", back_populates="orchestrator")


class AccessToken(Base):
    __tablename__ = "access_tokens"
    __table_args__ = {"schema": "them"}

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    token_hash: Mapped[str] = mapped_column(Text, nullable=False, unique=True)
    label: Mapped[str] = mapped_column(Text, nullable=False)
    user_id: Mapped[int] = mapped_column(Integer, nullable=False)
    orchestrator_id: Mapped[Optional[uuid.UUID]] = mapped_column(UUID(as_uuid=True), ForeignKey("them.orchestrators.id", ondelete="CASCADE"), nullable=True)
    enabled: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    expires_at: Mapped[Optional[datetime]] = mapped_column(DateTime(timezone=True), nullable=True)
    last_used_at: Mapped[Optional[datetime]] = mapped_column(DateTime(timezone=True), nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())

    orchestrator: Mapped[Optional["Orchestrator"]] = relationship("Orchestrator", back_populates="access_tokens")


class Run(Base):
    __tablename__ = "runs"
    __table_args__ = {"schema": "them"}

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    orchestrator_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), ForeignKey("them.orchestrators.id"), nullable=False)
    orchestrator_name: Mapped[str] = mapped_column(Text, nullable=False)
    user_id: Mapped[int] = mapped_column(Integer, nullable=False)
    session_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
    goal: Mapped[str] = mapped_column(Text, nullable=False)
    status: Mapped[str] = mapped_column(Text, nullable=False, default="running")
    final_output: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    error: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    iterations: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
    total_tokens_in: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
    total_tokens_out: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
    total_cost_usd: Mapped[Decimal] = mapped_column(Numeric(12, 8), nullable=False, default=0)
    started_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    ended_at: Mapped[Optional[datetime]] = mapped_column(DateTime(timezone=True), nullable=True)

    orchestrator: Mapped["Orchestrator"] = relationship("Orchestrator", back_populates="runs")
    steps: Mapped[List["RunStep"]] = relationship("RunStep", back_populates="run", cascade="all, delete-orphan")
    usage: Mapped[List["RunUsage"]] = relationship("RunUsage", back_populates="run", cascade="all, delete-orphan")


class RunStep(Base):
    __tablename__ = "run_steps"
    __table_args__ = {"schema": "them"}

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    run_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), ForeignKey("them.runs.id", ondelete="CASCADE"), nullable=False)
    iteration: Mapped[int] = mapped_column(Integer, nullable=False)
    agent_id: Mapped[Optional[uuid.UUID]] = mapped_column(UUID(as_uuid=True), ForeignKey("them.agents.id"), nullable=True)
    agent_slug: Mapped[str] = mapped_column(Text, nullable=False)
    tool_call_id: Mapped[str] = mapped_column(Text, nullable=False)
    input: Mapped[Dict[str, Any]] = mapped_column(JSONB, nullable=False)
    output: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    status: Mapped[str] = mapped_column(Text, nullable=False, default="pending")
    error: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    latency_ms: Mapped[Optional[int]] = mapped_column(Integer, nullable=True)
    started_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    ended_at: Mapped[Optional[datetime]] = mapped_column(DateTime(timezone=True), nullable=True)

    run: Mapped["Run"] = relationship("Run", back_populates="steps")
    agent: Mapped[Optional["Agent"]] = relationship("Agent", back_populates="run_steps")


class RunUsage(Base):
    __tablename__ = "run_usage"
    __table_args__ = {"schema": "them"}

    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    run_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), ForeignKey("them.runs.id", ondelete="CASCADE"), nullable=False)
    user_id: Mapped[int] = mapped_column(Integer, nullable=False)
    provider: Mapped[str] = mapped_column(Text, nullable=False)
    model: Mapped[str] = mapped_column(Text, nullable=False)
    tokens_input: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
    tokens_output: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
    cost_usd: Mapped[Decimal] = mapped_column(Numeric(12, 8), nullable=False, default=0)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())

    run: Mapped["Run"] = relationship("Run", back_populates="usage")


class AuditLog(Base):
    __tablename__ = "audit_logs"
    __table_args__ = {"schema": "them"}

    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    user_id: Mapped[Optional[int]] = mapped_column(Integer, nullable=True)
    action: Mapped[str] = mapped_column(Text, nullable=False)
    entity_type: Mapped[str] = mapped_column(Text, nullable=False)
    entity_id: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    details: Mapped[Dict[str, Any]] = mapped_column(JSONB, nullable=False, default=dict)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())


# ── Phase 2: Task graph ───────────────────────────────────────────────────────

class Task(Base):
    __tablename__ = "tasks"
    __table_args__ = {"schema": "them"}

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    run_id: Mapped[Optional[uuid.UUID]] = mapped_column(UUID(as_uuid=True), ForeignKey("them.runs.id", ondelete="SET NULL"), nullable=True)
    parent_task_id: Mapped[Optional[uuid.UUID]] = mapped_column(UUID(as_uuid=True), ForeignKey("them.tasks.id", ondelete="CASCADE"), nullable=True)
    orchestrator_id: Mapped[Optional[uuid.UUID]] = mapped_column(UUID(as_uuid=True), ForeignKey("them.orchestrators.id"), nullable=True)
    agent_id: Mapped[Optional[uuid.UUID]] = mapped_column(UUID(as_uuid=True), ForeignKey("them.agents.id"), nullable=True)
    context_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
    state: Mapped[str] = mapped_column(Text, nullable=False, default="submitted")
    kind: Mapped[str] = mapped_column(Text, nullable=False, default="root")
    remote_task_id: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    push_url: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    status_message: Mapped[Optional[Dict[str, Any]]] = mapped_column(JSONB, nullable=True)
    input_message: Mapped[Dict[str, Any]] = mapped_column(JSONB, nullable=False, default=dict)
    budget_tokens: Mapped[Optional[int]] = mapped_column(Integer, nullable=True)
    deadline: Mapped[Optional[datetime]] = mapped_column(DateTime(timezone=True), nullable=True)
    max_depth: Mapped[int] = mapped_column(Integer, nullable=False, default=5)
    tokens_used: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
    error: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now(), onupdate=func.now())

    artifacts: Mapped[List["Artifact"]] = relationship("Artifact", back_populates="task", cascade="all, delete-orphan")
    messages: Mapped[List["TaskMessage"]] = relationship("TaskMessage", back_populates="task", cascade="all, delete-orphan")


class Artifact(Base):
    __tablename__ = "artifacts"
    __table_args__ = {"schema": "them"}

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    task_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), ForeignKey("them.tasks.id", ondelete="CASCADE"), nullable=False)
    context_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
    artifact_id: Mapped[str] = mapped_column(Text, nullable=False)
    name: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    parts: Mapped[Dict[str, Any]] = mapped_column(JSONB, nullable=False)
    append_index: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
    last_chunk: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())

    task: Mapped["Task"] = relationship("Task", back_populates="artifacts")


class TaskMessage(Base):
    __tablename__ = "task_messages"
    __table_args__ = {"schema": "them"}

    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    task_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), ForeignKey("them.tasks.id", ondelete="CASCADE"), nullable=False)
    role: Mapped[str] = mapped_column(Text, nullable=False)
    parts: Mapped[Dict[str, Any]] = mapped_column(JSONB, nullable=False)
    seq: Mapped[int] = mapped_column(Integer, nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())

    task: Mapped["Task"] = relationship("Task", back_populates="messages")
