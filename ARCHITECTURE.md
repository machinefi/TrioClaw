# TrioClaw + TrioCore — System Architecture

## Overview

Two components, clear boundary:

```
trioclaw (Go)  = application layer — cameras, notifications, storage, UI
trio-core (Py) = inference engine  — video stream → AI results
```

They run on the same machine. trioclaw tells trio-core **what to watch**;
trio-core handles **how to watch** and streams results back.

---

## System Diagram

```
┌──────────────────────────────────────────────────────────────────┐
│                       Mac Mini / Edge Server                      │
│                                                                   │
│  ┌──────────────────────────┐     ┌────────────────────────────┐ │
│  │    trioclaw (Go) :8080   │     │   trio-core (Python) :8000 │ │
│  │                          │     │                            │ │
│  │  ┌────────────────────┐  │     │  ┌──────────────────────┐  │ │
│  │  │  Camera Registry   │  │     │  │  /v1/watch           │  │ │
│  │  │                    │  │     │  │                      │  │ │
│  │  │  cam0: Front Door  │  │POST │  │  ┌────────────────┐  │  │ │
│  │  │  cam1: Garage      │──│────→│  │  │ RTSP/Stream    │  │  │ │
│  │  │  cam2: Baby Room   │  │{url,│  │  │ Reader         │  │  │ │
│  │  │                    │  │ conditions}│ (ffmpeg/PyAV)  │  │  │ │
│  │  └────────────────────┘  │     │  │  └───────┬────────┘  │  │ │
│  │                          │     │  │          │ frames    │  │ │
│  │  ┌────────────────────┐  │     │  │  ┌───────▼────────┐  │  │ │
│  │  │  Event Consumer    │  │ SSE │  │  │ Pre-process    │  │  │ │
│  │  │                    │←─│←────│  │  │ · Dedup        │  │  │ │
│  │  │  on each result:   │  │event│  │  │ · Motion Gate  │  │  │ │
│  │  │  · check triggered │  │stream  │  │ · Frame Sample │  │  │ │
│  │  │  · route to actions│  │     │  │  └───────┬────────┘  │  │ │
│  │  └─────────┬──────────┘  │     │  │          │           │  │ │
│  │            │             │     │  │  ┌───────▼────────┐  │  │ │
│  │  ┌─────────▼──────────┐  │     │  │  │ VLM Inference  │  │  │ │
│  │  │  Action Router     │  │     │  │  │ · Multi-frame  │  │  │ │
│  │  │                    │  │     │  │  │ · ToMe / FastV │  │  │ │
│  │  │  · Webhook POST    │  │     │  │  │ · KV Cache     │  │  │ │
│  │  │  · Telegram Bot    │  │     │  │  └────────────────┘  │  │ │
│  │  │  · Slack Message   │  │     │  └──────────────────────┘  │ │
│  │  │  · Save 30s Clip   │  │     │                            │ │
│  │  │  · SQLite Event    │  │     │  ┌──────────────────────┐  │ │
│  │  │  · Audio Alert     │  │     │  │  /analyze-frame      │  │ │
│  │  └────────────────────┘  │     │  │  (single-shot, kept  │  │ │
│  │                          │     │  │   for compatibility)  │  │ │
│  │  ┌────────────────────┐  │     │  └──────────────────────┘  │ │
│  │  │  Daily Digest      │  │     │                            │ │
│  │  │  (cron, nightly)   │  │     │  ┌──────────────────────┐  │ │
│  │  │                    │  │     │  │  /v1/chat/completions │  │ │
│  │  │  SQLite events ────│──│────→│  │  (OpenAI-compatible) │  │ │
│  │  │  → summarize       │  │ or  │  └──────────────────────┘  │ │
│  │  │                    │──│────→│  Claude API / GPT API      │ │
│  │  │  (user-configured) │  │     │  (cloud LLM, optional)     │ │
│  │  └────────────────────┘  │     │                            │ │
│  │                          │     │                            │ │
│  │  ┌────────────────────┐  │     │                            │ │
│  │  │  Web UI / Config   │  │     │                            │ │
│  │  │  :8080             │  │     │                            │ │
│  │  └────────────────────┘  │     │                            │ │
│  └──────────────────────────┘     └────────────────────────────┘ │
│                                                                   │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐         │
│  │ Camera 1 │  │ Camera 2 │  │ Camera 3 │  │ USB Cam  │         │
│  │ RTSP     │  │ RTSP     │  │ ONVIF    │  │          │         │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘         │
└──────────────────────────────────────────────────────────────────┘
```

---

## Responsibility Boundary

| Concern | trioclaw (Go) | trio-core (Python) |
|---|---|---|
| **Camera discovery** | ONVIF scan, store RTSP URLs | — |
| **Video stream** | — | Connect RTSP, read frames |
| **Frame strategy** | — | Dedup, motion gate, FPS, sampling |
| **Inference** | — | VLM, ToMe, FastV, KV cache |
| **Result delivery** | — | SSE event stream |
| **Alert logic** | Receive triggered events, route | — |
| **Notifications** | Webhook, Telegram, Slack, SMS | — |
| **Clip recording** | Ring buffer, save on trigger | — |
| **Event storage** | SQLite | — |
| **Daily digest** | Cron job, call LLM | Provide local LLM (optional) |
| **Web UI** | Serve frontend | — |
| **User config** | Cameras, conditions, actions | — |

**Principle: trio-core doesn't know what happens with its answers. trioclaw doesn't know how inference works.**

---

## API Contract

### 1. Start Watching — `POST /v1/watch`

trioclaw sends an RTSP URL and conditions. trio-core connects to the stream,
handles all video processing, and returns results as SSE.

**Request:**

```http
POST /v1/watch HTTP/1.1
Content-Type: application/json

{
  "source": "rtsp://admin:pass@192.168.86.45:554/h264Preview_01_sub",
  "conditions": [
    {"id": "person", "question": "Is there a person?"},
    {"id": "package", "question": "Is there a package on the doorstep?"}
  ],
  "fps": 1,
  "stream": true
}
```

**Response: SSE stream** (`text/event-stream`)

```
event: status
data: {"watch_id": "w_abc123", "state": "connecting"}

event: status
data: {"watch_id": "w_abc123", "state": "running", "resolution": "896x512", "model": "Qwen3.5-2B-4bit"}

event: result
data: {
  "watch_id": "w_abc123",
  "ts": "2026-03-08T21:01:30Z",
  "conditions": [
    {"id": "person", "triggered": false, "answer": "No"},
    {"id": "package", "triggered": false, "answer": "No"}
  ],
  "metrics": {"latency_ms": 242, "tok_s": 73.4, "frames_analyzed": 4}
}

event: alert
data: {
  "watch_id": "w_abc123",
  "ts": "2026-03-08T21:05:12Z",
  "conditions": [
    {"id": "person", "triggered": true, "answer": "Yes, a person is standing at the front door"},
    {"id": "package", "triggered": false, "answer": "No"}
  ],
  "metrics": {"latency_ms": 251, "tok_s": 71.2, "frames_analyzed": 4},
  "frame_b64": "<base64 JPEG of the triggering frame>"
}
```

Notes:
- `event: result` sent on every inference cycle (every few seconds)
- `event: alert` sent only when a condition triggers; includes `frame_b64`
- trio-core decides frame count, sampling, dedup — trioclaw doesn't specify these
- `fps` is a hint for maximum check rate; trio-core may check less often (motion gate)

### 2. List Active Watches — `GET /v1/watch`

```http
GET /v1/watch HTTP/1.1

→ 200 OK
[
  {
    "watch_id": "w_abc123",
    "source": "rtsp://...45:554/...",
    "state": "running",
    "conditions": [{"id": "person", ...}, {"id": "package", ...}],
    "uptime_s": 3621,
    "checks": 1207,
    "alerts": 3
  }
]
```

### 3. Stop Watching — `DELETE /v1/watch/{watch_id}`

```http
DELETE /v1/watch/w_abc123 HTTP/1.1

→ 200 OK
{"status": "stopped", "total_checks": 1207, "total_alerts": 3}
```

### 4. Single-Shot Analysis — `POST /analyze-frame` (unchanged)

For one-off analysis without a persistent stream. Kept for backward compatibility.

```http
POST /analyze-frame HTTP/1.1
Content-Type: application/json

{"frame_b64": "<base64 jpeg>", "question": "What do you see?"}

→ {"answer": "A person at a desk", "triggered": true, "latency_ms": 242}
```

### 5. Chat — `POST /v1/chat/completions` (unchanged)

OpenAI-compatible. Used by trioclaw for daily digest (if using local LLM)
or any ad-hoc question.

---

## Data Flow

### Real-time Monitoring

```
Camera ──RTSP──→ trio-core ──SSE──→ trioclaw
                    │                    │
              [dedup, motion,           │
               multi-frame,         triggered?
               VLM inference]           │
                    │               ┌───┴────┐
                    │          yes  │        │  always
                    │               ▼        ▼
                    │          ┌──────┐  ┌────────┐
                    │          │Notify│  │SQLite  │
                    │          │Clip  │  │Event   │
                    │          └──────┘  └────────┘
```

### Daily Digest

```
trioclaw (nightly cron):

  1. SELECT * FROM events WHERE date = today
  2. Format as text prompt:
     "Summarize today's monitoring events:
      [08:12] Person detected at front door
      [08:15] Person left
      [09:30] Package delivered ..."
  3. Choose LLM based on user config:
     ─→ trio-core /v1/chat/completions  (local, free, good enough)
     ─→ Claude API                      (cloud, better quality)
     ─→ GPT API                         (cloud, alternative)
  4. Store summary, push to user
```

### Multi-Camera

```
trioclaw                          trio-core

  cam0 (front door) ──POST /v1/watch──→  watch_0 (goroutine)
  cam1 (garage)     ──POST /v1/watch──→  watch_1 (goroutine)
  cam2 (baby room)  ──POST /v1/watch──→  watch_2 (goroutine)
       │                                     │
       │←─────── SSE stream per watch ───────┘
       │
  Event Consumer (multiplexed)
       │
  Route by watch_id → actions
```

Each `/v1/watch` call creates an independent SSE stream.
trio-core manages GPU/model resources across concurrent watches
(sequential inference, shared model weights).

---

## Configuration

### trioclaw config (`~/.trioclaw/config.yaml`)

```yaml
# trio-core connection
trio_core:
  url: http://localhost:8000

# Cameras
cameras:
  - id: front-door
    name: Front Door
    source: rtsp://admin:pass@192.168.86.45:554/h264Preview_01_sub
    conditions:
      - id: person
        question: "Is there a person?"
        actions: [telegram, clip]
      - id: package
        question: "Is there a package on the doorstep?"
        actions: [slack]

  - id: garage
    name: Garage
    source: rtsp://admin:pass@192.168.86.50:554/stream
    conditions:
      - id: car
        question: "Is the garage door open?"
        actions: [webhook]

# Notification channels
notifications:
  telegram:
    bot_token: "123456:ABC..."
    chat_id: "-100123456"
  slack:
    webhook_url: "https://hooks.slack.com/services/T.../B.../xxx"
  webhook:
    url: "https://example.com/alerts"
    headers:
      Authorization: "Bearer xxx"

# Clip recording
clips:
  dir: ~/.trioclaw/clips/
  pre_seconds: 15    # save 15s before trigger
  post_seconds: 15   # save 15s after trigger

# Daily digest
digest:
  enabled: true
  schedule: "0 22 * * *"   # 10 PM daily
  llm: local               # "local" = trio-core, "claude" = Claude API, "openai" = GPT
  claude_api_key: ""       # only if llm: claude
  push_to: [telegram]
```

### trio-core config (unchanged)

trio-core is configured via `TRIO_` environment variables or `EngineConfig`.
It doesn't need to know about cameras, notifications, or storage.

```bash
TRIO_MODEL=mlx-community/Qwen3.5-2B-MLX-4bit
TRIO_PORT=8000
```

---

## What Changes from Current Design

### trio-core changes

| Before | After |
|---|---|
| `/analyze-frame` only | + `/v1/watch` (SSE stream) |
| Stateless (one request, one response) | + Stateful watches (persistent RTSP connections) |
| No RTSP in server | Server manages RTSP streams |
| `trio cam` CLI command does everything | `trio cam` becomes a demo/dev tool |

Implementation: add a `/v1/watch` endpoint in FastAPI that:
1. Spawns a background task per watch
2. Connects to RTSP (reuses existing `RTSPReader` / `StreamCapture`)
3. Runs dedup + motion gate + multi-frame inference in a loop
4. Yields SSE events to the client

### trioclaw changes

| Before (DESIGN.md) | After |
|---|---|
| trioclaw captures frames via ffmpeg | trioclaw sends RTSP URL to trio-core |
| trioclaw calls `/check-once` per frame | trioclaw opens `/v1/watch` SSE stream |
| trioclaw manages frame timing | trio-core manages frame timing |
| No notification system | + Webhook / Telegram / Slack |
| No persistence | + SQLite event log |
| No clip recording | + Ring buffer + clip save |
| No daily digest | + Cron + configurable LLM |

---

## Implementation Priority

### Phase 1: Watch API (trio-core side)

```
[ ] POST /v1/watch — SSE endpoint
[ ] Background task: RTSP → dedup → motion gate → inference → yield events
[ ] GET /v1/watch — list active watches
[ ] DELETE /v1/watch/{id} — stop watch
[ ] Multi-condition support (multiple questions per watch)
[ ] Concurrent watches (multiple cameras)
```

### Phase 2: Event Processing (trioclaw side)

```
[ ] SSE client — connect to /v1/watch, parse events
[ ] SQLite event storage
[ ] Action router (on trigger → dispatch to channels)
[ ] Webhook notification
[ ] Telegram bot notification (with screenshot)
[ ] 30-second clip recording (ring buffer + save on trigger)
```

### Phase 3: Digest & UI (trioclaw side)

```
[ ] Daily digest cron job
[ ] Configurable LLM (local trio-core or cloud API)
[ ] Slack notification
[ ] Web UI for camera management + event history
[ ] Multi-camera dashboard
```
