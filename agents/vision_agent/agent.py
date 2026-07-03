"""
Vision Agent — Location Visualizer

A2A v1.0 (JSON-RPC 2.0 over HTTP) compatible agent that:
  1. Geocodes an address via Google Geocoding API
  2. Fetches a street-level photo via Google Street View Static API
  3. Sends the photo + building description to fal.ai FLUX.1 Kontext Pro
  4. Returns the AI-composited image URL + cost breakdown

Endpoints:
  POST /                          — A2A JSON-RPC 2.0 (SendMessage / GetTask)
  GET  /.well-known/agent-card.json — Agent capability card
  GET  /health                    — Health probe
"""

import json
import os
import time
import urllib.parse
from contextlib import asynccontextmanager
from typing import Any
from uuid import uuid4

import anthropic
import fal_client
import httpx
from dotenv import load_dotenv
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

load_dotenv()

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

GOOGLE_MAPS_API_KEY = os.getenv("GOOGLE_MAPS_API_KEY", "")
FAL_API_KEY = os.getenv("FAL_API_KEY", "")
ANTHROPIC_API_KEY = os.getenv("ANTHROPIC_API_KEY", "")
AGENT_VERSION = os.getenv("AGENT_VERSION", "1.0.0")

GEOCODING_URL = "https://maps.googleapis.com/maps/api/geocode/json"
STREET_VIEW_URL = "https://maps.googleapis.com/maps/api/streetview"
FAL_MODEL = "fal-ai/flux-pro/kontext"

# Approximate costs (USD)
COST_GEOCODING = 0.000   # effectively free on the standard free tier (40k/mo)
COST_STREET_VIEW = 0.007  # $7 per 1000 after free tier
COST_FLUX_KONTEXT = 0.055  # per image at 1 megapixel

# ---------------------------------------------------------------------------
# In-memory task store (single-process; stateless across restarts)
# ---------------------------------------------------------------------------
_tasks: dict[str, dict] = {}

# ---------------------------------------------------------------------------
# Agent card
# ---------------------------------------------------------------------------

AGENT_CARD = {
    "name": "Vision Agent — Location Visualizer",
    "version": AGENT_VERSION,
    "description": (
        "Takes a real-world address and building description, fetches a street-level photo "
        "via Google Street View, then uses FLUX.1 Kontext AI to render the described building "
        "into the real photo. Returns a photorealistic composite image."
    ),
    "skills": [
        {
            "id": "visualize_location",
            "name": "visualize_location",
            "description": (
                "Render a described building at a real-world location using street-level "
                "photography and AI image editing"
            ),
            "inputSchema": {
                "type": "object",
                "properties": {
                    "address": {
                        "type": "string",
                        "description": "Street address or landmark to visualize",
                    },
                    "description": {
                        "type": "string",
                        "description": (
                            "Building description, e.g. '10 floor modern glass tower with green terraces'"
                        ),
                    },
                    "view_type": {
                        "type": "string",
                        "enum": ["street", "satellite"],
                        "default": "street",
                        "description": "Type of base photo to use",
                    },
                    "image_size": {
                        "type": "string",
                        "enum": ["640x640", "1024x1024"],
                        "default": "640x640",
                        "description": "Output image dimensions",
                    },
                },
                "required": ["address", "description"],
            },
        }
    ],
    "capabilities": {
        "streaming": False,
        "pushNotifications": False,
        "stateTransitionHistory": False,
    },
}

# ---------------------------------------------------------------------------
# Lifespan
# ---------------------------------------------------------------------------


@asynccontextmanager
async def lifespan(app: FastAPI):
    if FAL_API_KEY:
        os.environ["FAL_KEY"] = FAL_API_KEY
    yield


# ---------------------------------------------------------------------------
# App
# ---------------------------------------------------------------------------

app = FastAPI(title="Vision Agent", version=AGENT_VERSION, lifespan=lifespan)

# ---------------------------------------------------------------------------
# Helpers — API calls
# ---------------------------------------------------------------------------


async def geocode(address: str) -> tuple[float, float]:
    """Return (lat, lng) for the given address. Raises ValueError on failure."""
    if not GOOGLE_MAPS_API_KEY:
        raise ValueError("GOOGLE_MAPS_API_KEY is not set")

    params = {"address": address, "key": GOOGLE_MAPS_API_KEY}
    async with httpx.AsyncClient(timeout=10.0) as client:
        resp = await client.get(GEOCODING_URL, params=params)
        resp.raise_for_status()
        data = resp.json()

    if data.get("status") != "OK" or not data.get("results"):
        raise ValueError(
            f"Geocoding failed for '{address}': {data.get('status', 'UNKNOWN_ERROR')}"
        )

    location = data["results"][0]["geometry"]["location"]
    return location["lat"], location["lng"]


def street_view_url(lat: float, lng: float, size: str = "640x640") -> str:
    """Build a Google Street View Static API URL."""
    if not GOOGLE_MAPS_API_KEY:
        raise ValueError("GOOGLE_MAPS_API_KEY is not set")
    params = urllib.parse.urlencode(
        {"size": size, "location": f"{lat},{lng}", "key": GOOGLE_MAPS_API_KEY}
    )
    return f"{STREET_VIEW_URL}?{params}"


async def run_flux_kontext(image_url: str, prompt: str, image_size: str) -> str:
    """Submit image + prompt to fal.ai FLUX.1 Kontext Pro and return result image URL."""
    if not FAL_API_KEY:
        raise ValueError("FAL_API_KEY is not set")

    size_map = {
        "640x640": "square",
        "1024x1024": "square_hd",
    }
    fal_size = size_map.get(image_size, "square")

    result = await fal_client.run_async(
        FAL_MODEL,
        arguments={
            "image_url": image_url,
            "prompt": prompt,
            "image_size": fal_size,
            "num_inference_steps": 28,
            "guidance_scale": 3.5,
            "num_images": 1,
            "safety_tolerance": "2",
            "output_format": "jpeg",
        },
    )

    images = result.get("images", [])
    if not images:
        raise ValueError("fal.ai returned no images in the response")
    return images[0]["url"]


# ---------------------------------------------------------------------------
# Helpers — LLM text extraction
# ---------------------------------------------------------------------------


def extract_args_from_text(text: str) -> dict[str, str]:
    """
    Use claude-haiku to parse natural-language input into structured arguments.
    Returns dict with at least 'address' and 'description' keys.
    Raises ValueError if extraction fails.
    """
    if not ANTHROPIC_API_KEY:
        raise ValueError(
            "ANTHROPIC_API_KEY is not set — required for natural language parsing. "
            "Send structured JSON instead: "
            '{\"tool\": \"visualize_location\", \"arguments\": {\"address\": \"...\", \"description\": \"...\"}}'
        )

    client = anthropic.Anthropic(api_key=ANTHROPIC_API_KEY)
    system = (
        "You are a parameter extractor. The user will describe a building visualization request. "
        "Extract the real-world address (or landmark) and the building description. "
        "Respond ONLY with a JSON object with keys 'address' and 'description'. "
        "If you cannot determine the address, use an empty string. "
        "Do not add any explanation outside the JSON."
    )
    message = client.messages.create(
        model="claude-haiku-4-5-20251001",
        max_tokens=256,
        system=system,
        messages=[{"role": "user", "content": text}],
    )
    raw = message.content[0].text.strip()
    # Strip markdown code fences if present
    if raw.startswith("```"):
        raw = raw.split("```")[1]
        if raw.startswith("json"):
            raw = raw[4:]
        raw = raw.strip()
    parsed = json.loads(raw)
    if not parsed.get("address") or not parsed.get("description"):
        raise ValueError(
            f"Could not extract 'address' and 'description' from input. Parsed: {parsed}"
        )
    return parsed


# ---------------------------------------------------------------------------
# Core skill implementation
# ---------------------------------------------------------------------------


async def visualize_location(
    address: str,
    description: str,
    view_type: str = "street",
    image_size: str = "640x640",
) -> str:
    """
    Full pipeline: geocode → street view URL → FLUX Kontext → result text.
    Returns a multi-line result string with the image URL and cost breakdown.
    """
    missing = []
    if not GOOGLE_MAPS_API_KEY:
        missing.append("GOOGLE_MAPS_API_KEY")
    if not FAL_API_KEY:
        missing.append("FAL_API_KEY")
    if missing:
        raise ValueError(
            f"Missing required API key(s): {', '.join(missing)}. "
            "Set them as environment variables and restart the container."
        )

    # Step 1 — Geocode
    lat, lng = await geocode(address)

    # Step 2 — Street View URL (the API key is embedded; fal.ai will fetch it)
    photo_url = street_view_url(lat, lng, size=image_size)

    # Step 3 — FLUX.1 Kontext
    flux_prompt = (
        f"Replace the existing building in this street-level photo with the following architecture: "
        f"{description}. Keep the street, sky, trees, and surrounding context exactly as they are. "
        f"Make the result photorealistic, architecturally accurate, and seamlessly composited."
    )
    result_url = await run_flux_kontext(photo_url, flux_prompt, image_size)

    # Cost breakdown
    sv_cost = COST_STREET_VIEW
    flux_cost = COST_FLUX_KONTEXT
    total = COST_GEOCODING + sv_cost + flux_cost

    result_text = (
        f"Visualization complete for: {address}\n"
        f"Coordinates: {lat:.6f}, {lng:.6f}\n"
        f"Building description: {description}\n\n"
        f"Result image URL:\n{result_url}\n\n"
        f"Base photo (Street View):\n{photo_url}\n\n"
        f"Cost breakdown:\n"
        f"  Geocoding:    ${COST_GEOCODING:.3f} (free tier)\n"
        f"  Street View:  ${sv_cost:.3f}\n"
        f"  FLUX Kontext: ${flux_cost:.3f}\n"
        f"  Total:        ${total:.3f}"
    )
    return result_text


# ---------------------------------------------------------------------------
# A2A task helpers
# ---------------------------------------------------------------------------


def _make_task(
    task_id: str,
    state: str,
    result_text: str | None = None,
    error: str | None = None,
) -> dict[str, Any]:
    artifacts: list[dict] = []
    status_message: dict[str, Any] = {}

    if result_text:
        artifacts.append({"parts": [{"text": result_text}]})
    if error:
        status_message = {"parts": [{"text": f"Error: {error}"}]}

    return {
        "id": task_id,
        "status": {
            "state": state,
            "message": status_message,
            "timestamp": time.time(),
        },
        "artifacts": artifacts,
    }


def _rpc_result(rpc_id: str | int | None, result: Any) -> dict:
    return {"jsonrpc": "2.0", "id": rpc_id, "result": result}


def _rpc_error(rpc_id: str | int | None, code: int, message: str) -> dict:
    return {"jsonrpc": "2.0", "id": rpc_id, "error": {"code": code, "message": message}}


# ---------------------------------------------------------------------------
# A2A message dispatch
# ---------------------------------------------------------------------------


async def handle_send_message(params: dict) -> dict:
    """Process a SendMessage call and return a completed (or failed) task synchronously."""
    task_id = str(uuid4())
    message = params.get("message", {})
    parts = message.get("parts", [])

    # Extract text from message parts
    input_text = ""
    for part in parts:
        if "text" in part:
            input_text = part["text"].strip()
            break

    if not input_text:
        task = _make_task(task_id, "TASK_STATE_FAILED", error="No input text in message parts")
        _tasks[task_id] = task
        return task

    # Determine if input is JSON-structured or natural language
    address: str | None = None
    description: str | None = None
    view_type = "street"
    image_size = "640x640"
    extraction_error: str | None = None

    try:
        parsed = json.loads(input_text)
        if isinstance(parsed, dict):
            tool = parsed.get("tool", "")
            args = parsed.get("arguments", parsed) if tool == "visualize_location" else parsed
            address = args.get("address")
            description = args.get("description")
            view_type = args.get("view_type", "street")
            image_size = args.get("image_size", "640x640")
    except (json.JSONDecodeError, ValueError):
        # Natural language — extract via LLM
        try:
            extracted = extract_args_from_text(input_text)
            address = extracted.get("address")
            description = extracted.get("description")
        except Exception as exc:
            extraction_error = str(exc)

    if extraction_error:
        task = _make_task(task_id, "TASK_STATE_FAILED", error=extraction_error)
        _tasks[task_id] = task
        return task

    if not address or not description:
        task = _make_task(
            task_id,
            "TASK_STATE_FAILED",
            error=(
                "Could not determine 'address' and 'description'. "
                "Send JSON: {\"tool\": \"visualize_location\", \"arguments\": "
                "{\"address\": \"...\", \"description\": \"...\"}}"
            ),
        )
        _tasks[task_id] = task
        return task

    # Sanitize optional params
    if view_type not in ("street", "satellite"):
        view_type = "street"
    if image_size not in ("640x640", "1024x1024"):
        image_size = "640x640"

    try:
        result_text = await visualize_location(address, description, view_type, image_size)
        task = _make_task(task_id, "TASK_STATE_COMPLETED", result_text=result_text)
    except Exception as exc:
        task = _make_task(task_id, "TASK_STATE_FAILED", error=str(exc))

    _tasks[task_id] = task
    return task


async def handle_get_task(params: dict) -> dict | None:
    task_id = params.get("id", "")
    return _tasks.get(task_id)


# ---------------------------------------------------------------------------
# Routes
# ---------------------------------------------------------------------------


@app.get("/.well-known/agent-card.json")
async def agent_card():
    return JSONResponse(content=AGENT_CARD)


@app.get("/health")
async def health():
    return {"status": "ok", "version": AGENT_VERSION}


@app.post("/")
async def a2a_endpoint(request: Request):
    try:
        body = await request.json()
    except Exception:
        return JSONResponse(
            content=_rpc_error(None, -32700, "Parse error: invalid JSON"),
            status_code=400,
        )

    rpc_id = body.get("id")
    method = body.get("method", "")
    params = body.get("params", {})

    if body.get("jsonrpc") != "2.0":
        return JSONResponse(
            content=_rpc_error(rpc_id, -32600, "Invalid Request: jsonrpc must be '2.0'"),
            status_code=400,
        )

    if method == "SendMessage":
        task = await handle_send_message(params)
        return JSONResponse(content=_rpc_result(rpc_id, task))

    elif method == "GetTask":
        task = await handle_get_task(params)
        if task is None:
            return JSONResponse(
                content=_rpc_error(rpc_id, -32001, f"Task not found: {params.get('id')}"),
                status_code=404,
            )
        return JSONResponse(content=_rpc_result(rpc_id, task))

    else:
        return JSONResponse(
            content=_rpc_error(rpc_id, -32601, f"Method not found: {method}"),
            status_code=404,
        )


# ---------------------------------------------------------------------------
# Entrypoint
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    import uvicorn

    port = int(os.getenv("PORT", "9100"))
    uvicorn.run("agent:app", host="0.0.0.0", port=port, reload=False)
