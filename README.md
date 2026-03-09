<p align="center">
  <br>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/trioclaw-banner-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="docs/assets/trioclaw-banner-light.svg">
    <img src="docs/assets/trioclaw-banner-dark.svg" alt="TrioClaw — Eyes, ears, voice & hands for AI agents" width="600">
  </picture>
</p>

<p align="center">
  An <a href="https://github.com/openclaw/openclaw">OpenClaw</a> node that connects cameras, microphones, speakers, and smart devices<br>so your AI agent can see, hear, speak, and act in the real world.
</p>

<p align="center">
  <a href="https://github.com/machinefi/trioclaw/actions"><img src="https://img.shields.io/github/actions/workflow/status/machinefi/trioclaw/ci.yml?branch=main&style=for-the-badge" alt="CI"></a>
  <a href="https://github.com/machinefi/trioclaw/releases"><img src="https://img.shields.io/github/v/release/machinefi/trioclaw?include_prereleases&style=for-the-badge" alt="Release"></a>
  <a href="https://goreportcard.com/report/github.com/machinefi/trioclaw"><img src="https://goreportcard.com/badge/github.com/machinefi/trioclaw?style=for-the-badge" alt="Go Report"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-blue.svg?style=for-the-badge" alt="MIT License"></a>
  <a href="https://discord.gg/clawd"><img src="https://img.shields.io/discord/1456350064065904867?label=Discord&logo=discord&logoColor=white&color=5865F2&style=for-the-badge" alt="Discord"></a>
</p>

<p align="center">
  <a href="#quick-start">Quick Start</a> ·
  <a href="#configuration">Configuration</a> ·
  <a href="#http-api">HTTP API</a> ·
  <a href="DESIGN.md">Design Doc</a> ·
  <a href="#roadmap">Roadmap</a> ·
  <a href="https://discord.gg/clawd">Discord</a>
</p>

---

## What It Does

TrioClaw is a single Go binary that gives AI agents physical senses:

```
Camera  ──→ trio-core (VLM) ──→ "I see a delivery person"
                                  ├─→ Telegram / Slack / Webhook notification
                                  ├─→ Save video clip
                                  └─→ Push to OpenClaw Gateway
```

It continuously monitors cameras via [trio-core](https://trio.machinefi.com) (a vision language model), detects conditions you define ("is there a person?", "is the gate open?"), and reacts — sending notifications, recording clips, generating daily digests, and forwarding alerts to AI agents.

---

## Quick Start

```bash
# Install
curl -sSL https://raw.githubusercontent.com/machinefi/TrioClaw/main/install.sh | sh

# Add a camera
trioclaw camera add \
  --id front-door \
  --name "Front Door" \
  --source rtsp://admin:pass@192.168.1.10:554/stream \
  --question "is there a person at the door?"

# Start monitoring
trioclaw run
```

On first run, TrioClaw checks if trio-core is available. If not, it prompts you:

```
trio-core is not reachable at http://localhost:8000

  1) Install locally  — runs on your machine, needs Python 3.10+
  2) Use cloud        — connects to trio.machinefi.com

Choose [1/2] (default: 2):
```

Option 1 auto-detects Python and uv/pip, installs trio-core, and manages the process. Option 2 uses the hosted API with zero setup.

### Standalone Mode

Works without OpenClaw — no Gateway needed:

```bash
# One-shot: snap + ask
trioclaw snap --analyze "what do you see?"

# Check system health
trioclaw doctor
```

### With OpenClaw Gateway

```bash
# Pair with your Gateway (one-time)
trioclaw pair --gateway ws://192.168.1.100:18789

# Start — connects to Gateway + monitors cameras
trioclaw run
```

Then, in OpenClaw:

```
You:   "What do you see on the front door camera?"
Agent: "The front porch is empty."

You:   "Watch for deliveries and let me know."
Agent: "I'll keep an eye on it."

       ... 30 min later ...

Agent: "A delivery person just left a package at your front door."
```

---

## Configuration

Config file: `~/.trioclaw/config.yaml`

```yaml
trio_core:
  url: http://localhost:8000     # or https://trio.machinefi.com

cameras:
  - id: front-door
    name: Front Door
    source: rtsp://admin:pass@192.168.1.10:554/stream
    fps: 1
    conditions:
      - id: person
        question: "is there a person at the door?"
        actions: [webhook, telegram, clip]
      - id: package
        question: "is there a package on the ground?"
        actions: [telegram]

  - id: backyard
    name: Backyard
    source: rtsp://192.168.1.11:554/stream
    conditions:
      - id: animal
        question: "is there an animal in the yard?"
        actions: [slack]

notifications:
  webhook:
    url: https://example.com/hook
    headers:
      Authorization: Bearer xxx
  telegram:
    bot_token: "123456:ABC-DEF"
    chat_id: "-1001234567890"
  slack:
    webhook_url: https://hooks.slack.com/services/T.../B.../xxx

clips:
  dir: ~/.trioclaw/clips/
  post_seconds: 15

digest:
  enabled: true
  schedule: "0 22 * * *"     # daily at 10 PM
  llm: local                  # local | claude | openai
  push_to: [telegram]
```

### Condition Actions

Each condition can trigger one or more actions when the VLM detects a match:

| Action | What it does |
|--------|-------------|
| `webhook` | POST alert JSON to your webhook URL |
| `telegram` | Send message + snapshot to Telegram chat |
| `slack` | Send formatted message to Slack channel |
| `clip` | Record post-alert video clip via ffmpeg |

### Daily Digest

When enabled, TrioClaw generates a daily summary of all alerts using an LLM:

| LLM | Description |
|-----|-------------|
| `local` | Uses trio-core's `/v1/chat/completions` endpoint (default) |
| `claude` | Uses Anthropic Claude API (set `ANTHROPIC_API_KEY`) |
| `openai` | Uses OpenAI API (set `OPENAI_API_KEY`) |

Falls back to a plain-text summary if the LLM is unavailable.

---

## HTTP API

TrioClaw runs an HTTP API server on `:8080` (configurable with `--listen`).

| Endpoint | Description |
|----------|-------------|
| `GET /healthz` | Health check |
| `GET /api/status` | Service overview (cameras, watches, uptime) |
| `GET /api/cameras` | List configured cameras (credentials masked) |
| `GET /api/watches` | List active watch streams |
| `GET /api/events?date=2026-03-09&camera=front-door&limit=100` | Query events |
| `GET /api/alerts?date=2026-03-09&camera=front-door` | Query triggered alerts |
| `GET /api/alerts/recent?limit=20` | Most recent alerts |
| `GET /api/stats` | Aggregate statistics |
| `GET /api/clips/{filename}` | Serve a saved clip file |

---

## CLI Commands

| Command | Description |
|---------|-------------|
| `trioclaw run` | Start the service (trio-core SSE + API + Gateway) |
| `trioclaw camera add` | Add a camera to config |
| `trioclaw camera remove <id>` | Remove a camera |
| `trioclaw camera list` | List configured cameras |
| `trioclaw status` | Show service status and recent alerts |
| `trioclaw snap [--analyze "question"]` | One-shot capture + optional VLM analysis |
| `trioclaw pair --gateway ws://...` | Pair with an OpenClaw Gateway |
| `trioclaw doctor` | Check dependencies and devices |
| `trioclaw update` | Self-update to latest release |
| `trioclaw version` | Show version info |

### Run Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `~/.trioclaw/config.yaml` | Config file path |
| `--camera` | — | Additional camera source (repeatable) |
| `--trio-api` | — | Override trio-core URL |
| `--listen` | `:8080` | HTTP API listen address |
| `--ha-url` | — | Home Assistant URL |
| `--ha-token` | — | Home Assistant access token |
| `--plugin-dir` | `~/.trioclaw/plugins/` | Exec plugin scripts directory |

---

## OpenClaw Commands

When connected to a Gateway, TrioClaw responds to these invoke commands:

| Command | Description |
|---------|-------------|
| `camera.snap` | Capture a JPEG frame |
| `camera.list` | List available cameras |
| `camera.clip` | Record a short video clip |
| `vision.analyze` | Capture + VLM analysis ("what do you see?") |
| `vision.watch` | Start continuous monitoring with conditions |
| `vision.watch.stop` | Stop a watch stream |
| `vision.status` | Show active watches and their state |
| `device.list` | List available smart devices |
| `device.control` | Send command to a smart device |

---

## How It Works

```
┌─────────────────────────────────────────────────────────────────┐
│  TrioClaw (single Go binary)                                    │
│                                                                 │
│   Config ──→ Watch Manager ──→ trio-core SSE streams            │
│              (per camera)      /vision/watch                    │
│                    │                                            │
│                    ├─→ SQLite (events + alerts)                 │
│                    ├─→ Notification Dispatcher                  │
│                    │     ├─→ Webhook                            │
│                    │     ├─→ Telegram (message + photo)         │
│                    │     └─→ Slack                              │
│                    ├─→ Clip Recorder (ffmpeg -c:v copy)         │
│                    └─→ OpenClaw Gateway (vision.alert event)    │
│                                                                 │
│   HTTP API (:8080)                                              │
│     /api/status, /api/cameras, /api/events, /api/alerts, ...   │
│                                                                 │
│   Daily Digest                                                  │
│     LLM summary ──→ Notification Dispatcher                    │
│                                                                 │
│   Plugin System                                                 │
│     Home Assistant, exec scripts                                │
└─────────────────────────────────────────────────────────────────┘
```

### Key Design Decisions

- **Zero CGO** — all device access through ffmpeg subprocess; SQLite via modernc.org/sqlite (pure Go)
- **SSE-based monitoring** — trio-core streams results/alerts over Server-Sent Events, no polling
- **Credential masking** — RTSP passwords are masked (`***:***@host`) in all API responses and logs
- **Path traversal protection** — clip serving endpoint validates paths stay within clip directory
- **Graceful shutdown** — SIGINT/SIGTERM triggers ordered cleanup of SSE streams, HTTP server, trio-core subprocess
- **trio-core auto-management** — detects Python, installs via uv/pip, manages subprocess lifecycle

---

## Project Structure

```
TrioClaw/
├── cmd/trioclaw/              # CLI: pair, run, snap, camera, status, doctor, update
├── internal/
│   ├── api/
│   │   └── server.go          # HTTP REST API (/api/events, /api/alerts, etc.)
│   ├── capture/
│   │   ├── ffmpeg.go          # Camera capture via ffmpeg subprocess
│   │   └── mic.go             # Microphone capture
│   ├── clip/
│   │   └── clip.go            # Post-alert video clip recording via ffmpeg
│   ├── config/
│   │   └── config.go          # YAML config loader (~/.trioclaw/config.yaml)
│   ├── digest/
│   │   └── digest.go          # Daily digest (local LLM / Claude / OpenAI)
│   ├── gateway/
│   │   ├── protocol.go        # OpenClaw WebSocket protocol v3
│   │   ├── client.go          # WebSocket client + reconnect
│   │   └── handler.go         # Invoke command dispatcher
│   ├── notify/
│   │   └── notify.go          # Notification dispatcher (webhook, telegram, slack)
│   ├── plugin/
│   │   ├── plugin.go          # Plugin registry
│   │   ├── homeassistant/     # Home Assistant plugin
│   │   └── execplugin/        # Exec script plugins
│   ├── store/
│   │   └── store.go           # SQLite event store (WAL mode)
│   ├── triocore/
│   │   ├── client.go          # trio-core SSE client
│   │   ├── manager.go         # Watch manager (SSE per camera)
│   │   └── setup.go           # trio-core auto-detect / install / lifecycle
│   ├── vision/
│   │   └── trio.go            # Trio API client (POST /check-once)
│   └── state/
│       └── state.go           # Persistent state (~/.trioclaw/state.json)
├── e2e/
│   └── smoke_test.go          # 31 smoke tests
├── DESIGN.md
├── SKILL.md
├── go.mod
└── install.sh
```

---

## Supported Devices

| Device | How | Platforms |
|--------|-----|-----------|
| Built-in webcam | `ffmpeg -f avfoundation` / `v4l2` / `dshow` | macOS, Linux, Windows |
| USB camera | Same as webcam (auto-discovered) | All |
| RTSP / IP camera | `ffmpeg -rtsp_transport tcp -i rtsp://...` | All |
| Built-in mic | `ffmpeg -f avfoundation` / `pulse` / `alsa` | macOS, Linux |
| Speaker (TTS) | `espeak-ng` / macOS `say` | macOS, Linux |
| Smart home | Plugin system (Home Assistant, exec scripts) | All |

---

## Development

```bash
# Build
go build -o trioclaw ./cmd/trioclaw

# Test
go test ./...

# Run smoke tests
go test ./e2e/ -v

# Cross-compile
make cross
```

---

## Roadmap

| Phase | What | Status |
|-------|------|--------|
| **MVP** | Camera capture, Gateway integration, `camera.snap`, `vision.analyze`, standalone CLI | Done |
| **Phase 2** | YAML config, camera management, continuous monitoring (vision.watch SSE), notifications, HTTP API, clip recording, daily digest, trio-core auto-setup, plugin system | Done |
| **Phase 3** | STT via Whisper, TTS via espeak/`say`, audio monitoring | Planned |
| **Phase 4** | GitHub releases, Docker image, OpenClaw Skill Hub listing | Planned |

---

## Related Projects

| Project | Description |
|---------|-------------|
| [OpenClaw](https://github.com/openclaw/openclaw) | Personal AI assistant — the Gateway that TrioClaw connects to |
| [ClawGo](https://github.com/openclaw/clawgo) | Go node for voice (ears + mouth) |
| [MimiClaw](https://github.com/memovai/mimiclaw) | $5 chip AI assistant — standalone OpenClaw node |
| [Trio API](https://trio.machinefi.com) | Vision Language Model API that powers TrioClaw's eyes |

---

## License

[MIT](LICENSE)

## Contributing

Contributions welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

Built with [Trio](https://trio.machinefi.com) by [MachineFi](https://github.com/machinefi).
