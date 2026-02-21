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
  <a href="DESIGN.md">Design Doc</a> ·
  <a href="SKILL.md">Skill Spec</a> ·
  <a href="#roadmap">Roadmap</a> ·
  <a href="https://discord.gg/clawd">Discord</a>
</p>

---

## The Four Senses

```
👁  Eyes   — Camera  → ffmpeg → Trio API (VLM)    → "I see a delivery person"
👂  Ears   — Mic     → ffmpeg → Whisper API (STT)  → "User said: check the door"
🗣  Mouth  — Speaker ← espeak / say                ← "There's a package outside"
🤚  Hands  — Devices ← plugin command              ← "Turning on porch light"
```

TrioClaw is a single Go binary. One side connects to local hardware (cameras, microphones, speakers, smart devices). The other side connects to an [OpenClaw Gateway](https://github.com/openclaw/openclaw). All heavy AI processing happens in the cloud — TrioClaw itself is thin.

| Sense | Device | Cloud API | Status |
|-------|--------|-----------|--------|
| **Eyes** | Webcam, USB camera, RTSP/IP camera | [Trio API](https://trio.machinefi.com) (VLM) | **Ready** |
| **Ears** | Built-in mic, USB mic | Whisper API (STT) | Phase 2 |
| **Mouth** | System speaker | espeak-ng / macOS `say` (TTS) | Phase 2 |
| **Hands** | Smart lights, locks, switches, GPIO | Plugin system | Phase 3 |

### Why TrioClaw?

[ClawGo](https://github.com/openclaw/clawgo) gave OpenClaw **ears and a voice**. TrioClaw adds **eyes and hands** — and wraps it all in a plugin system anyone can extend.

| | ClawGo | TrioClaw |
|---|--------|----------|
| Eyes | — | Trio API (VLM) |
| Ears | Whisper | Whisper (same) |
| Mouth | espeak | espeak / `say` (same) |
| Hands | — | Plugin system |
| Devices | Mic only | Camera + Mic + RTSP + IoT |
| Standalone | No | Yes |
| Plugins | No | Yes |

---

## Quick Start

**Prerequisites:** [ffmpeg](https://ffmpeg.org/) installed on your system.

```bash
# Install
curl -sSL https://raw.githubusercontent.com/machinefi/TrioClaw/main/install.sh | sh

# Verify everything works
trioclaw doctor
# ✓ ffmpeg: 6.1.1
# ✓ Camera: FaceTime HD Camera
# ✓ Microphone: MacBook Pro Microphone
# ✓ Trio API: https://trio.machinefi.com
# ✗ Gateway: not paired

# Pair with your OpenClaw Gateway (one-time)
trioclaw pair --gateway ws://192.168.1.100:18789

# Start the node
trioclaw run --camera rtsp://192.168.1.50/stream1
```

Then, in OpenClaw:

```
You:   "What do you see on the front door camera?"
Agent: "The front porch is empty. No one is there."

You:   "Watch for deliveries and let me know."
Agent: "I'll keep an eye on it."

       ... 30 min later ...

Agent: "A delivery person just left a package at your front door."

You:   "Turn on the porch light."
Agent: "Done. Porch light is on."
```

### Standalone Mode

Works without OpenClaw — no Gateway needed:

```bash
# One-shot: snap + ask
trioclaw snap --analyze "what do you see?"

# Watch mode: monitor + webhook
trioclaw watch rtsp://192.168.1.50/stream1 \
  --when "is there a package at the door?" \
  --webhook https://hooks.slack.com/services/xxx
```

---

## How It Works

```
┌────────────────────────────────────────────────────────────┐
│                                                            │
│   TrioClaw (single Go binary, ~12MB)                       │
│                                                            │
│   👁  Camera  ──→ ffmpeg ──→ Trio API (VLM)    ──┐        │
│   👂  Mic     ──→ ffmpeg ──→ Whisper API (STT) ──┼──→ OpenClaw
│   🗣  Speaker ←── espeak ←── agent text        ←─┤   Gateway
│   🤚  Device  ←── plugin ←── agent action      ←─┘        │
│                                                            │
│   Cloud: VLM + STT          Local: capture + playback      │
│                                                            │
└────────────────────────────────────────────────────────────┘
```

- **Zero CGO** — all device access through `ffmpeg` subprocess
- **Cross-platform** — macOS, Linux, Windows, Raspberry Pi
- **Single binary** — `go build` and ship, no runtime deps
- **Thin** — no local AI, no heavy frameworks, ~2K lines of Go

---

## Supported Devices

| Device | How | Platforms |
|--------|-----|-----------|
| Built-in webcam | `ffmpeg -f avfoundation` / `v4l2` / `dshow` | macOS, Linux, Windows |
| USB camera | Same as webcam (auto-discovered) | All |
| RTSP / IP camera | `ffmpeg -rtsp_transport tcp -i rtsp://...` | All |
| Built-in mic | `ffmpeg -f avfoundation` / `pulse` / `alsa` | macOS, Linux |
| Speaker (TTS) | `espeak-ng` / macOS `say` | macOS, Linux |
| Smart home | Plugin system (any language) | All |

---

## CLI Commands

| Command | Description |
|---------|-------------|
| `trioclaw pair --gateway ws://host:18789` | Pair with an OpenClaw Gateway (one-time) |
| `trioclaw run [--camera rtsp://...]` | Start as an OpenClaw node (main mode) |
| `trioclaw snap [--analyze "question"]` | Capture a frame, optionally analyze with VLM |
| `trioclaw doctor` | Check ffmpeg, devices, API connectivity |
| `trioclaw update` | Self-update to the latest release |
| `trioclaw version` | Show version info |

## OpenClaw Commands

When connected to a Gateway, TrioClaw responds to these invoke commands:

| Command | Sense | Description |
|---------|-------|-------------|
| `camera.snap` | Eyes | Capture a JPEG frame |
| `camera.list` | Eyes | List available cameras |
| `camera.clip` | Eyes | Record a short video clip |
| `vision.analyze` | Eyes | Capture + VLM analysis ("what do you see?") |
| `tts.speak` | Mouth | Speak text through local speaker |
| `mic.listen` | Ears | Record and transcribe audio |
| `device.control` | Hands | Send command to a smart device |

---

## Project Structure

```
TrioClaw/
├── cmd/trioclaw/             # CLI (cobra): pair, run, snap, doctor
├── internal/
│   ├── capture/
│   │   ├── ffmpeg.go         # Camera capture via ffmpeg subprocess
│   │   └── mic.go            # Microphone capture via ffmpeg
│   ├── gateway/
│   │   ├── protocol.go       # OpenClaw WebSocket protocol v3
│   │   ├── client.go         # WebSocket client + reconnect
│   │   └── handler.go        # Invoke command dispatcher
│   ├── vision/
│   │   └── trio.go           # Trio API client (POST /check-once)
│   └── state/
│       └── state.go          # Persistent state (~/.trioclaw/state.json)
├── e2e/                      # End-to-end tests
├── DESIGN.md                 # Architecture & design decisions
├── SKILL.md                  # OpenClaw Skill Hub definition
├── go.mod
└── Makefile
```

---

## Development

```bash
# Build
go build -o trioclaw ./cmd/trioclaw

# Test
go test ./...

# Cross-compile
make cross
# → bin/trioclaw-darwin-arm64
# → bin/trioclaw-linux-amd64
# → bin/trioclaw-linux-arm64
# → bin/trioclaw-windows-amd64.exe
```

---

## Roadmap

| Phase | What | Senses |
|-------|------|--------|
| **MVP** | Camera capture (webcam + USB + RTSP), mic discovery, Gateway integration, `camera.snap`, `camera.list`, `vision.analyze`, standalone CLI | Eyes |
| **Phase 2** | STT via Whisper, TTS via espeak/`say`, `vision.watch` with motion detection, proactive events | + Ears + Mouth |
| **Phase 3** | Plugin system, smart device control via plugins (Tuya, Zigbee, HomeKit, GPIO) | + Hands |
| **Phase 4** | GitHub releases, `curl \| sh` installer, Docker image, OpenClaw Skill Hub listing | Release |

See [DESIGN.md](DESIGN.md) for detailed architecture, protocol spec, and implementation plan.

---

## Related Projects

| Project | Description |
|---------|-------------|
| [OpenClaw](https://github.com/openclaw/openclaw) | Personal AI assistant — the Gateway that TrioClaw connects to |
| [ClawGo](https://github.com/openclaw/clawgo) | Go node for voice (ears + mouth) — TrioClaw's predecessor |
| [MimiClaw](https://github.com/memovai/mimiclaw) | $5 chip AI assistant — standalone OpenClaw node |
| [Trio API](https://trio.machinefi.com) | Vision Language Model API that powers TrioClaw's eyes |

---

## License

[MIT](LICENSE)

## Contributing

Contributions welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

Built with [Trio](https://trio.machinefi.com) by [MachineFi](https://github.com/machinefi).
