"""
Seed: Debate Master — sub-orchestrator composition test.

Creates:
  1. Sets debate_flow.a2a_exposed = true  (makes it callable as a sub-orchestrator)
  2. Creates orchestrator: debate_master   (top-level, routes to debate_flow)
  3. Creates application:  debate-master   (WS entry point, token auth)

Run inside the bridge container:
  docker cp scripts/seed_debate_master.py them-bridge:/tmp/
  docker exec them-bridge python3 /tmp/seed_debate_master.py
"""

import asyncio
import sys
import uuid

sys.path.insert(0, "/app")

import app.database as db_module
from app.models import Application, Orchestrator
from sqlalchemy import select


DEBATE_FLOW_NAME = "debate_flow"
MASTER_NAME      = "debate_master"
MASTER_SLUG      = "debate-master"

MASTER_SYSTEM_PROMPT = """You are the Debate Master — a meta-orchestrator that runs structured debates.

You have one sub-orchestrator available:
- agent__orch__debate_flow: runs a full 2-round debate with evidence, logic, and creative agents, then judges the result.

## Your job
When the user gives you a debate topic or question:
1. Briefly acknowledge the topic (1 sentence).
2. Call agent__orch__debate_flow with: {"message": "<the debate question>"}
3. Present the full debate result to the user, formatted clearly.

Do not run the debate yourself — always delegate to agent__orch__debate_flow.
Keep your own framing brief; the debate content is what matters.
"""


async def main():
    await db_module.init_db()

    async with db_module.AsyncSessionLocal() as db:

        # ── 1. Find debate_flow ────────────────────────────────────────────
        result = await db.execute(
            select(Orchestrator).where(Orchestrator.name == DEBATE_FLOW_NAME)
        )
        debate_flow = result.scalar_one_or_none()
        if debate_flow is None:
            print(f"ERROR: orchestrator '{DEBATE_FLOW_NAME}' not found — run the main seed first.")
            sys.exit(1)

        debate_flow_id = debate_flow.id
        encrypted_key  = debate_flow.llm_api_key_encrypted

        # ── 2. Set debate_flow.a2a_exposed = true ─────────────────────────
        if not debate_flow.a2a_exposed:
            debate_flow.a2a_exposed = True
            await db.commit()
            print(f"✓ debate_flow.a2a_exposed set to true (id={debate_flow_id})")
        else:
            print(f"  debate_flow already a2a_exposed=true (id={debate_flow_id})")

        # ── 3. Create / update debate_master orchestrator ──────────────────
        result = await db.execute(
            select(Orchestrator).where(Orchestrator.name == MASTER_NAME)
        )
        master = result.scalar_one_or_none()

        if master is None:
            master = Orchestrator(
                name=MASTER_NAME,
                display_name="Debate Master",
                system_prompt=MASTER_SYSTEM_PROMPT,
                llm_provider="anthropic",
                llm_model="claude-sonnet-4-6",
                llm_api_key_encrypted=encrypted_key,
                max_iterations=6,
                max_parallel_tools=2,
                rate_limit_rpm=60,
                daily_budget_usd="5.00",
                allowed_agent_ids=[debate_flow_id],   # sub-orchestrator UUID
                enabled=True,
                a2a_exposed=False,
                memory_enabled=False,
                summarize_every_n_calls=3,
                memory_raw_fallback_n=5,
                history_window=10,
                budget_tokens=20000,
            )
            db.add(master)
            await db.commit()
            await db.refresh(master)
            print(f"✓ orchestrator '{MASTER_NAME}' created (id={master.id})")
        else:
            master.system_prompt     = MASTER_SYSTEM_PROMPT
            master.allowed_agent_ids = [debate_flow_id]
            master.enabled           = True
            master.llm_api_key_encrypted = encrypted_key
            await db.commit()
            await db.refresh(master)
            print(f"  orchestrator '{MASTER_NAME}' already exists — updated (id={master.id})")

        master_id = master.id

        # ── 4. Create / update application: debate-master ─────────────────
        result = await db.execute(
            select(Application).where(Application.slug == MASTER_SLUG)
        )
        app_row = result.scalar_one_or_none()

        if app_row is None:
            app_row = Application(
                name="Debate Master",
                slug=MASTER_SLUG,
                entry_point_type="websocket",
                orchestrator_id=master_id,
                access_policy={"mode": "token"},
                presentation={
                    "theme": "dark",
                    "icon": "forum",
                    "description": "Multi-orchestrator debate — powered by sub-orchestrator composition",
                },
                enabled=True,
            )
            db.add(app_row)
            await db.commit()
            await db.refresh(app_row)
            print(f"✓ application '{MASTER_SLUG}' created (id={app_row.id})")
        else:
            app_row.orchestrator_id = master_id
            app_row.enabled         = True
            await db.commit()
            print(f"  application '{MASTER_SLUG}' already exists — updated (id={app_row.id})")

        # ── 5. Bust Redis cache ────────────────────────────────────────────
        try:
            import app.database as db_module2
            redis = db_module2.redis_client
            if redis:
                await redis.delete("them:agents:registry")
                await redis.delete(f"them:orchestrators:{MASTER_NAME}")
                await redis.delete(f"them:orchestrators:{DEBATE_FLOW_NAME}")
                await redis.publish("them:agents:changed", "changed")
                print("✓ Redis cache busted")
        except Exception as e:
            print(f"  Redis bust skipped: {e}")

        print()
        print("=" * 55)
        print("  Debate Master ready!")
        print("=" * 55)
        print(f"  Sub-orchestrator:  debate_flow  (a2a_exposed=true)")
        print(f"  Main orchestrator: {MASTER_NAME}  (id={master_id})")
        print(f"  Application slug:  {MASTER_SLUG}")
        print()
        print("  Test WS endpoint:")
        print(f"    ws://localhost:8001/apps/{MASTER_SLUG}/ws")
        print()
        print("  Or use the Applications canvas in the UI.")
        print("=" * 55)


if __name__ == "__main__":
    asyncio.run(main())
