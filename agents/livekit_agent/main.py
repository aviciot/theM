import asyncio
import json
import logging
import os
import websockets
from livekit.agents import AutoSubscribe, JobContext, WorkerOptions, cli, llm
from livekit.agents.voice_assistant import VoiceAssistant
from livekit.plugins import openai, silero

logger = logging.getLogger("livekit-agent")

THEM_BRIDGE_WS = os.getenv("THEM_BRIDGE_WS", "ws://them-bridge:8001")


class ThemLLM(llm.LLM):
    """Proxies LLM turns to the-M WS orchestrator."""

    def __init__(self, slug: str, context_id: str):
        super().__init__()
        self._slug = slug
        self._context_id = context_id

    def chat(self, *, chat_ctx: llm.ChatContext, **kwargs) -> "ThemLLMStream":
        user_msg = ""
        for m in reversed(chat_ctx.messages):
            if m.role == "user":
                user_msg = m.content if isinstance(m.content, str) else ""
                break
        return ThemLLMStream(self, slug=self._slug, context_id=self._context_id, user_message=user_msg)


class ThemLLMStream(llm.LLMStream):
    def __init__(self, llm_instance, *, slug: str, context_id: str, user_message: str):
        super().__init__(llm_instance, chat_ctx=llm.ChatContext(), tools=[])
        self._slug = slug
        self._context_id = context_id
        self._user_message = user_message

    async def _run(self):
        uri = f"{THEM_BRIDGE_WS}/apps/{self._slug}/ws"
        try:
            async with websockets.connect(uri) as ws:
                await ws.send(json.dumps({
                    "content": self._user_message,
                    "context_id": self._context_id,
                }))
                async for raw in ws:
                    msg = json.loads(raw)
                    if msg.get("type") == "token":
                        chunk = llm.ChatChunk(
                            request_id="",
                            choices=[llm.Choice(
                                delta=llm.ChoiceDelta(role="assistant", content=msg["text"]),
                                index=0,
                            )],
                        )
                        self._event_ch.send_nowait(chunk)
                    elif msg.get("type") == "done":
                        break
                    elif msg.get("type") == "error":
                        logger.error("them ws error: %s", msg.get("message"))
                        break
        except Exception as e:
            logger.error("them ws connection failed: %s", e)


async def entrypoint(ctx: JobContext):
    await ctx.connect(auto_subscribe=AutoSubscribe.AUDIO_ONLY)

    metadata = {}
    try:
        metadata = json.loads(ctx.room.metadata or "{}")
    except Exception:
        pass

    slug = metadata.get("slug", "")
    context_id = metadata.get("context_id", str(ctx.room.name))

    if not slug:
        logger.error("no slug in room metadata, cannot route to orchestrator")
        return

    them_llm = ThemLLM(slug=slug, context_id=context_id)

    assistant = VoiceAssistant(
        vad=silero.VAD.load(),
        stt=openai.STT(),
        llm=them_llm,
        tts=openai.TTS(),
        chat_ctx=llm.ChatContext(),
    )

    assistant.start(ctx.room)
    await assistant.say("Hello! How can I help you?", allow_interruptions=True)
    await asyncio.sleep(3600)


if __name__ == "__main__":
    cli.run_app(WorkerOptions(entrypoint_fnc=entrypoint))
