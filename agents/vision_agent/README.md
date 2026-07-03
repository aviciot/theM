# Vision Agent — Location Visualizer

Standalone A2A v1.0 agent that composites an AI-rendered building into a real street-level photo.

## Pipeline

```
Input: address + building description
  ↓
Google Geocoding API → lat/lng
  ↓
Google Street View Static API → 640×640 street photo
  ↓
fal.ai FLUX.1 Kontext Pro → building rendered into photo
  ↓
Returns: result image URL + cost breakdown + metadata
```

## API Keys Required

| Key | Where to get it | APIs to enable |
|---|---|---|
| `GOOGLE_MAPS_API_KEY` | [Google Cloud Console](https://console.cloud.google.com/apis/credentials) | Geocoding API, Street View Static API |
| `FAL_API_KEY` | [fal.ai dashboard](https://fal.ai/dashboard/keys) | — |
| `ANTHROPIC_API_KEY` | [Anthropic console](https://console.anthropic.com/settings/api-keys) | Optional — only needed for natural-language input |

## Running with Docker Compose (recommended)

Add keys to `/opt/docker/odin/.env`:

```
GOOGLE_MAPS_API_KEY=your_key_here
FAL_API_KEY=your_key_here
ANTHROPIC_API_KEY=your_key_here
```

Then start:

```bash
docker compose up -d vision-agent
```

The agent is available at `http://localhost:9100`.

## Running standalone

```bash
cp .env.example .env
# fill in .env
pip install -r requirements.txt
python agent.py
```

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/.well-known/agent-card.json` | A2A agent capability card |
| `GET` | `/health` | Health probe |
| `POST` | `/` | A2A JSON-RPC 2.0 endpoint |

## A2A Protocol

The agent implements A2A v1.0 JSON-RPC 2.0 synchronously (no streaming, no push).

### SendMessage — structured JSON input

```bash
curl -X POST http://localhost:9100/ \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "1",
    "method": "SendMessage",
    "params": {
      "message": {
        "role": "user",
        "parts": [{
          "text": "{\"tool\": \"visualize_location\", \"arguments\": {\"address\": \"1 Infinite Loop, Cupertino, CA\", \"description\": \"20-storey curved glass tower with rooftop garden\"}}"
        }],
        "messageId": "msg-1"
      }
    }
  }'
```

### SendMessage — natural language input (requires ANTHROPIC_API_KEY)

```bash
curl -X POST http://localhost:9100/ \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "2",
    "method": "SendMessage",
    "params": {
      "message": {
        "role": "user",
        "parts": [{"text": "Show me a 15-floor brutalist office block at Times Square, New York"}],
        "messageId": "msg-2"
      }
    }
  }'
```

### GetTask

```bash
curl -X POST http://localhost:9100/ \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": "3",
    "method": "GetTask",
    "params": {"id": "<task-id-from-send-response>"}
  }'
```

### Response shape

```json
{
  "jsonrpc": "2.0",
  "id": "1",
  "result": {
    "id": "<task-uuid>",
    "status": {"state": "TASK_STATE_COMPLETED", "message": {}, "timestamp": 1234567890.0},
    "artifacts": [{"parts": [{"text": "Visualization complete for: ...\n\nResult image URL:\nhttps://..."}]}]
  }
}
```

## Skill parameters

| Parameter | Type | Required | Default | Description |
|---|---|---|---|---|
| `address` | string | yes | — | Street address or landmark |
| `description` | string | yes | — | Building description |
| `view_type` | string | no | `street` | `street` or `satellite` |
| `image_size` | string | no | `640x640` | `640x640` or `1024x1024` |

## Cost per call (approximate)

| Step | Cost |
|---|---|
| Google Geocoding | $0.000 (free tier: 40k/mo) |
| Google Street View | $0.007 |
| FLUX.1 Kontext Pro | $0.055 |
| **Total** | **~$0.062** |

## Registering with Odin

In the Odin admin UI, create an agent with:
- Transport: `a2a`
- Endpoint URL: `http://vision-agent:9100/`
- Description: `Renders a described building at a real-world location. Input: address and building description.`
