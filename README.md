# TrioClaw

> Give any AI agent eyes, ears, and senses. A lightweight, cross-platform node that connects cameras, microphones, and smart devices to [OpenClaw](https://github.com/openclaw/openclaw).

## What Is TrioClaw?

TrioClaw is a complete sensory system for AI agents. It provides:

- **Vision** — camera capture + VLM analysis via Trio API
- **Hearing** — microphone capture + speech-to-text (via Whisper API, Phase 2)
- **Voice** — text-to-speech playback via espeak/macOS `say` (Phase 2)

It runs as a single Go binary on your machine. On one side it connects to local devices (cameras, microphones, speakers). On the other side it connects to an OpenClaw Gateway. The AI agent sees, hears, and speaks through TrioClaw.

All heavy AI processing happens in the cloud — TrioClaw itself is thin. It captures frames and audio locally, sends them to cloud APIs for understanding, and relays results to Gateway.

### Compared to ClawGo

[ClawGo](https://github.com/openclaw/clawgo) gave OpenClaw ears and a voice. TrioClaw is its superset — adding vision on top of the same STT/TTS pattern:

| Capability      | ClawGo          | TrioClaw               |
|-----------------|-----------------|-------------------------|
| Ears (STT)      | Whisper API      | Whisper API (same approach) |
| Mouth (TTS)      | espeak subprocess| espeak / macOS `say` (same approach) |
| Eyes (Vision)    | -               | Trio API (VLM)          |
| Devices          | Microphone only  | Camera + Mic + RTSP + future IoT |
| Processing       | All local       | All in cloud (Trio API)   |

The STT and TTS code follows the exact same subprocess pattern as ClawGo. No magic — just `ffmpeg` for capture, cloud APIs for understanding, and subprocess calls for playback.

## Quick Start

```bash
# Install (single binary, no runtime dependencies)
curl -sSL https://get.trioclaw.com | sh

# Check that everything works
trioclaw doctor

# Pair with your OpenClaw Gateway (one-time)
trioclaw pair --gateway ws://192.168.1.100:18789

# Run as a node (camera + mic + speaker)
trioclaw run --camera rtsp://192.168.1.50/stream1
```

Then talk to your OpenClaw agent:

```
You:   "What do you see on the front door camera?"
Agent: "The front porch is empty. No one is there."

You:   "Watch for deliveries and let me know."
Agent: "I'll keep an eye on it. I'll message you when a package arrives."

... 30 min later ...

Agent: "A delivery person just left a package at your front door."
       (spoken aloud through your speaker if TTS is enabled)
```

## Standalone Mode (no OpenClaw needed)

```bash
trioclaw watch rtsp://192.168.1.50/stream1 \
  --when "is there a package at the door?" \
  --webhook https://hooks.slack.com/services/xxx
```

## Supported Devices

| Device Type          | Access Method               | Description                      |
|---------------------|----------------------------|----------------------------------|
| Built-in webcam     | ffmpeg avfoundation/v4l2/dshow | Auto-discovered                  |
| USB camera         | ffmpeg (same as webcam)      | Auto-discovered                  |
| RTSP camera        | ffmpeg -rtsp_transport tcp     | User specifies via --camera flag    |
| Built-in microphone | ffmpeg avfoundation/pulse/alsa | Auto-discovered                  |
| Speaker (TTS)      | espeak / macOS `say` subprocess | Phase 2                          |
| Smart home / IoT   | Plugin system (Phase 3)       | Extensible via plugins            |

## Features

- **Vision**: camera capture + VLM analysis via Trio API
- **Hearing**: microphone capture + speech-to-text via Whisper API (Phase 2)
- **Voice**: text-to-speech playback via espeak / macOS `say` (Phase 2)
- **Any camera**: RTSP, USB, webcam, phone (WebRTC, Phase 3)
- **Any microphone**: system mic, USB, network
- **Cross-platform**: macOS, Linux, Windows, Raspberry Pi
- **Single binary**: ~12MB, zero runtime dependencies
- **Plugin system**: add new devices in any language (Python, Go, shell script)

## How It Works

```
┌─────────────────────────────────────────────────────┐
│  TrioClaw (Go binary on your machine)         │
│                                                     │
│  Camera      ──→ ffmpeg ──→ Trio API (VLM) ──┐  │
│  Mic         ──→ ffmpeg ──→ Whisper API (STT) ───┼──→ Gateway ──→ AI Agent
│  Speaker     ←── espeak / say ←── TTS text  ←──┘  │
│                                                     │
│              ┌─────────────────────────────────────┐      │
│              │ All VLM/STT happens in cloud   │      │
└─────────────┴─────────────────────────────────────┘
```

### Architecture Principles

- **Capture**: all device access through `ffmpeg` subprocess — zero CGO, works everywhere
- **Understand**: frames/audio → [Trio API](https://trio.machinefi.com) (VLM), [Whisper API](https://platform.openai.com) (STT) — all cloud processing
- **Speak**: text from agent → `espeak` (Linux) or `say` (macOS) subprocess
- **Connect**: WebSocket to OpenClaw Gateway (protocol v3)

## Directory Structure

```
TrioClaw/
├── cmd/trioclaw/          # CLI entry point (pair/run/snap/doctor)
├── internal/
│   ├── state/            # State persistence (~/.trioclaw/state.json)
│   ├── gateway/
│   │   ├── protocol.go   # WebSocket protocol types
│   │   ├── client.go     # WebSocket connection management
│   │   └── handler.go    # Command dispatcher
│   ├── capture/
│   │   ├── ffmpeg.go     # Camera capture via ffmpeg subprocess
│   │   └── mic.go         # Microphone capture via ffmpeg
│   └── vision/
│       └── trio.go        # Trio API client for VLM analysis
├── e2e/                  # End-to-end tests
├── DESIGN.md             # Detailed design document
├── go.mod, go.sum       # Go module dependencies
└── Makefile              # Build targets
```

## Development

```bash
# Install dependencies
go mod tidy

# Run tests
go test ./...

# Build for current platform
go build -o trioclaw ./cmd/trioclaw

# Cross-compile
GOOS=linux GOARCH=arm64 go build -o trioclaw-linux-arm64 ./cmd/trioclaw
GOOS=darwin GOARCH=arm64 go build -o trioclaw-darwin-arm64 ./cmd/trioclaw
GOOS=windows GOARCH=amd64 go build -o trioclaw-windows-amd64.exe ./cmd/trioclaw

# Run tests with coverage
go test ./... -cover
```

## License

MIT License - see LICENSE file for details

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.
