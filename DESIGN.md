# TrioClaw — Design Document

## Table of Contents

1. [High-Level Goals](#1-high-level-goals)
2. [Language Decision](#2-language-decision)
3. [Architecture](#3-architecture)
4. [Plugin System](#4-plugin-system)
5. [OpenClaw Protocol Integration](#5-openclaw-protocol-integration)
6. [Operating Modes](#6-operating-modes)
7. [Directory Structure](#7-directory-structure)
8. [Device Access — How It Works](#8-device-access--how-it-works)
9. [MVP — Minimum Viable Product](#9-mvp--minimum-viable-product)
10. [Post-MVP Phases](#10-post-mvp-phases)

---

## 1. High-Level Goals

1. **Cross-platform, low friction** — Single binary runs on macOS, Windows, Linux, Raspberry Pi. No runtime, no pip, no npm. `curl | sh` and go.

2. **Easy device onboarding** — Users can quickly connect microphone, camera, and future smart home devices. Long-term stable operation (always-on daemon).

3. **OpenClaw integration** — Low-friction connection to OpenClaw Gateway. Enables wow moments ("AI saw my gardener arrive") and IFTTT-like automation. Local machine is thin — all AI processing via Trio API.

4. **Simple, forkable architecture** — Plugin system for fragmented devices and IoT protocols. Contributors can add new device support in any language without touching the core.

---

## 2. Language Decision

### Recommendation: Go

| Criterion | Go | Rust | Python | Node.js |
|---|:---:|:---:|:---:|:---:|
| Single binary distribution | **A+** | A | D | B- |
| Cross-compile to ARM | **A+** (trivial) | B+ | C | B |
| RPi memory footprint | **B+** (10-20MB) | A+ (3-8MB) | D (80-150MB) | B- (20-40MB) |
| Contributor accessibility | **A** | C | A+ | A |
| WebSocket client | **A** | A | A | A |
| Long-running daemon | **A** | A+ | B- | B |
| Ecosystem alignment | **A** (ClawGo, PicoClaw, go2rtc) | B | B (Trio) | B (OpenClaw) |

### Why Go Wins

1. **Zero-friction distribution**: `GOOS=linux GOARCH=arm64 go build` → single static binary. No CGO needed for our use case.

2. **Proven pattern**: go2rtc (camera proxy, 30K+ stars) and ClawGo both use Go + subprocess delegation for device access. We follow the same model.

3. **Contributor pool**: Go is top-10 language. ClawGo and PicoClaw mean the OpenClaw community already has Go expertise.

4. **Right tradeoffs**: 10-20MB binary (vs Rust's 3MB) is irrelevant on RPi 4. Go's GC overhead is undetectable for our workload (WebSocket + subprocess management).

### Device Access: Subprocess Delegation

Go has no native camera/mic access. We delegate to external tools:

```
Camera → ffmpeg subprocess (avfoundation/dshow/v4l2)
Mic    → ffmpeg / arecord / sox subprocess
GPIO   → /sys/class/gpio file I/O (no CGO)
IoT    → MQTT client (pure Go) or plugin subprocess
```

This is exactly what go2rtc and ClawGo do. It works.

### ffmpeg as runtime dependency

Users need ffmpeg for camera/mic features. Mitigation:
- Clear error message when ffmpeg is missing
- `trioclaw doctor` command checks for dependencies
- Most Linux distros + macOS (brew) have ffmpeg available
- Docker image bundles ffmpeg

---

## 3. Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  TrioClaw (Go binary)                                         │
│                                                               │
│  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐│
│  │ Gateway       │  │ Plugin       │  │ Trio API Client     ││
│  │ Client        │  │ Manager      │  │                     ││
│  │               │  │              │  │ POST /check-once    ││
│  │ • WebSocket   │  │ • Scan dir   │  │ POST /live-monitor  ││
│  │ • Pair/Auth   │  │ • Spawn      │  │ POST /live-digest   ││
│  │ • Invoke      │  │ • JSON Lines │  │                     ││
│  │ • Events      │  │ • Lifecycle  │  │ (all VLM processing ││
│  │               │  │ • Health     │  │  happens in cloud)  ││
│  └──────┬───────┘  └──────┬───────┘  └──────────┬──────────┘│
│         │                  │                      │           │
│         │      ┌───────────┴───────────┐         │           │
│         │      │    Device Registry     │         │           │
│         │      │                        │         │           │
│         │      │ cam0: Front Door (RTSP)│         │           │
│         │      │ cam1: Garage (USB)     │         │           │
│         │      │ mic0: System Mic       │         │           │
│         │      │ sensor0: Temp (Zigbee) │         │           │
│         │      └────────────────────────┘         │           │
│         │                                         │           │
│  ┌──────┴─────────────────────────────────────────┴─────────┐│
│  │                    Command Router                         ││
│  │                                                           ││
│  │  camera.snap   → Plugin Manager → camera plugin → frame   ││
│  │  vision.analyze → frame + Trio API → analysis result      ││
│  │  vision.watch  → background job (motion + periodic VLM)   ││
│  │  mic.listen    → Plugin Manager → mic plugin → audio      ││
│  └───────────────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────────────┘
         │                    │
    ┌────┴────┐     ┌────────┴────────┐
    │ OpenClaw│     │ Built-in + User │
    │ Gateway │     │ Plugins         │
    │ (WS)    │     │ (subprocesses)  │
    └─────────┘     └─────────────────┘
```

### Core Principle: TrioClaw is THIN

- **No VLM locally** — all AI via Trio API (cloud)
- **No heavy deps** — device access via subprocess/plugins
- **No framework** — just a Go binary with goroutines
- **Core is ~2,000 lines** — entire project is graspable

---

## 4. Plugin System

### Design: Directory Convention + Subprocess JSON Lines

Simplest possible plugin architecture. A plugin is:
1. A folder in `~/.trioclaw/plugins/`
2. A `plugin.yaml` manifest
3. An executable that speaks JSON Lines on stdin/stdout

```
~/.trioclaw/plugins/
├── camera-rtsp/
│   ├── plugin.yaml
│   └── plugin           # Go built-in (compiled into binary)
├── camera-usb/
│   ├── plugin.yaml
│   └── plugin.py        # Python script using OpenCV
├── mic-system/
│   ├── plugin.yaml
│   └── plugin.sh        # Shell script using arecord
├── tuya-sensor/
│   ├── plugin.yaml
│   └── plugin.js        # Node.js script using tuyapi
└── homekit-bridge/
    ├── plugin.yaml
    └── plugin            # Any language
```

### Manifest: plugin.yaml

```yaml
name: camera-usb
version: 0.1.0
description: USB webcam support via ffmpeg
author: contributor@example.com
license: MIT

# How to run
run: python3 plugin.py       # or: ./plugin, node plugin.js, ./plugin.sh

# What devices this provides
device_type: camera           # camera | microphone | sensor | actuator

# What it can do
capabilities:
  - discover                  # list available devices
  - command                   # handle commands (snap, clip, etc.)
  - stream                    # emit continuous events

# Platform constraints (optional)
platforms: [linux, darwin]

# Dependencies hint (optional, for trioclaw doctor)
requires: [ffmpeg]
```

### Protocol: 3 Methods + Events

Every plugin reads JSON Lines from stdin, writes JSON Lines to stdout.

**Core → Plugin (stdin):**

```jsonl
{"method":"discover","id":"1"}
{"method":"command","id":"2","params":{"device":"cam0","action":"snap","args":{"maxWidth":1280,"quality":0.85}}}
{"method":"command","id":"3","params":{"device":"cam0","action":"clip","args":{"durationMs":5000}}}
{"method":"ping","id":"99"}
{"method":"shutdown","id":"100"}
```

**Plugin → Core (stdout):**

```jsonl
{"ready":true}
{"id":"1","result":{"devices":[{"id":"cam0","name":"Logitech C920","type":"camera","meta":{"resolution":"1920x1080"}}]}}
{"id":"2","result":{"frame_path":"/tmp/trioclaw/camera-usb/frame_001.jpg","format":"jpeg","width":1280,"height":720}}
{"id":"3","result":{"clip_path":"/tmp/trioclaw/camera-usb/clip_001.mp4","format":"mp4","durationMs":5000}}
{"event":"motion.detected","data":{"device":"cam0","region":"center","confidence":0.87,"ts":1708444800}}
{"id":"99","result":{"status":"ok","devices_active":1}}
{"id":"100","result":{"status":"ok"}}
```

**That's the entire protocol.** A contributor can write a plugin in 30 lines of bash.

### Minimal Plugin Example (bash, 20 lines)

```bash
#!/bin/bash
# camera-usb/plugin.sh — Simplest possible camera plugin

echo '{"ready":true}'

while IFS= read -r line; do
  method=$(echo "$line" | jq -r '.method')
  id=$(echo "$line" | jq -r '.id')

  case "$method" in
    discover)
      echo "{\"id\":\"$id\",\"result\":{\"devices\":[{\"id\":\"cam0\",\"name\":\"USB Camera\",\"type\":\"camera\"}]}}"
      ;;
    command)
      action=$(echo "$line" | jq -r '.params.action')
      if [ "$action" = "snap" ]; then
        ffmpeg -f v4l2 -i /dev/video0 -frames:v 1 -y /tmp/trioclaw/frame.jpg 2>/dev/null
        echo "{\"id\":\"$id\",\"result\":{\"frame_path\":\"/tmp/trioclaw/frame.jpg\",\"format\":\"jpeg\"}}"
      fi
      ;;
    shutdown)
      echo "{\"id\":\"$id\",\"result\":{\"status\":\"ok\"}}"
      exit 0
      ;;
  esac
done
```

### Binary Data Exchange

Frames/audio go through **file paths**, not stdin/stdout:

```
Core sets: PLUGIN_DATA_DIR=/tmp/trioclaw/camera-usb/
Plugin writes: /tmp/trioclaw/camera-usb/frame_001.jpg
Plugin returns: {"result":{"frame_path":"/tmp/trioclaw/camera-usb/frame_001.jpg"}}
Core reads the file, base64 encodes for Gateway or Trio API
```

No base64 over pipes. No buffer bloat. Works with any language.

### Plugin Lifecycle

```
STOPPED → spawn → STARTING → "ready" line → RUNNING → crash → RESTARTING (max 3) → STOPPED
                                                ↑                    │
                                                └────────────────────┘
```

Core manages: spawn, health check (ping every 60s), restart on crash (backoff), clean shutdown.

### Built-in Plugins (compiled into binary)

For zero-dependency experience, core device support is compiled in:

| Plugin | Built-in? | Method |
|--------|:---------:|--------|
| camera-rtsp | Yes | ffmpeg subprocess |
| camera-usb | Yes | ffmpeg subprocess |
| mic-system | Yes | ffmpeg/arecord subprocess |
| camera-webrtc | Phase 2 | Pion WebRTC (pure Go) |

Users can override built-ins by dropping a plugin with the same name in the plugins dir.

### Future: Plugin Registry

```bash
trioclaw plugin install tuya-sensor    # downloads from registry
trioclaw plugin list                   # show installed plugins
trioclaw plugin create my-device       # scaffold a new plugin
```

But Phase 1 is just built-in plugins + manual plugin directory.

---

## 5. OpenClaw Protocol Integration

### WebSocket Protocol (Port 18789)

TrioClaw implements the current OpenClaw Gateway WebSocket protocol (not the deprecated TCP Bridge that ClawGo uses).

#### Connection Flow

```
1. WS connect → ws://<gateway>:18789
2. Receive:  { event: "connect.challenge", payload: { nonce, ts } }
3. Send:     { type: "req", method: "connect", params: {
                client: { id: "trioclaw", mode: "node", ... },
                role: "node",
                caps: ["camera"],
                commands: ["camera.snap","camera.list","camera.clip",
                           "vision.analyze","vision.watch","vision.status"],
                permissions: { "camera.capture": true },
                auth: { token: "<saved-token>" }
              }}
4. Receive:  { type: "res", ok: true, payload: { type: "hello-ok", ... } }
5. → Connection established. Ready for invocations.
```

#### Pairing (first connection)

```
1. Send: { method: "node.pair.request", params: {
            nodeId: "trioclaw-a1b2c3",
            displayName: "Front Door Camera",
            caps: ["camera"],
            commands: [...] }}
2. User approves: `openclaw devices approve <requestId>`
3. Receive: device.pair.resolved (approved) + token
4. Save token to ~/.trioclaw/state.json
```

#### Handling Invocations

Gateway sends `node.invoke.request` → TrioClaw dispatches to handler:

```
camera.snap     → plugin: capture frame → return base64 JPEG
camera.list     → plugin: enumerate devices → return device list
camera.clip     → plugin: record clip → return base64 MP4
vision.analyze  → capture frame → POST to Trio API /check-once → return analysis
vision.watch    → start background job → return jobId → emit events on trigger
vision.status   → return active cameras + jobs
```

#### Proactive Events

TrioClaw can push events to Gateway without being asked:

```json
{
  "type": "event",
  "event": "agent.request",
  "payloadJSON": "{\"message\":\"[TrioClaw] Front door: a delivery person placed a package.\",\"sessionKey\":\"main\"}"
}
```

This enables the IFTTT-like pattern:
- vision.watch detects condition → emits agent.request → agent processes → responds/acts

---

## 6. Operating Modes

### Mode 1: OpenClaw Node (primary)

```bash
trioclaw run --gateway ws://192.168.1.100:18789 --camera rtsp://192.168.1.50/stream
```

- Connects to Gateway via WebSocket
- Registers as node with camera + vision capabilities
- Responds to invoke commands
- Emits proactive events (vision triggers)
- Reconnects with exponential backoff

### Mode 2: Standalone Watch (no Gateway)

```bash
trioclaw watch rtsp://192.168.1.50/stream \
  --when "is there a person at the door?" \
  --webhook https://hooks.slack.com/services/xxx \
  --interval 30s \
  --duration 60m
```

- Same camera + vision logic
- No Gateway connection
- Triggers send webhook/Telegram notification
- Good for users who don't use OpenClaw

### Mode 3: One-shot Check

```bash
trioclaw snap rtsp://192.168.1.50/stream --analyze "what do you see?"
```

- Capture single frame, run VLM analysis, print result, exit.

### Mode 4: Service Daemon

```bash
trioclaw serve --config ~/.trioclaw/config.yaml
```

- Reads config file for cameras, Gateway, plugins
- Runs as background daemon (systemd/launchd)
- Exposes local REST API on localhost:8384

---

## 7. Directory Structure

```
TrioClaw/
├── README.md
├── DESIGN.md                  # This file
├── SKILL.md                   # OpenClaw Skill definition
├── go.mod
├── go.sum
├── Makefile                   # Cross-compile targets
├── cmd/
│   └── trioclaw/
│       └── main.go            # CLI entry (pair/run/watch/snap/serve/doctor)
├── internal/
│   ├── gateway/
│   │   ├── client.go          # WebSocket connection, reconnect, keepalive
│   │   ├── protocol.go        # Frame types (req/res/event), serialization
│   │   ├── pair.go            # Pairing protocol
│   │   └── handler.go         # Invoke dispatcher → camera/vision handlers
│   ├── plugin/
│   │   ├── manager.go         # Scan dir, spawn, lifecycle, health check
│   │   ├── protocol.go        # JSON Lines stdin/stdout protocol
│   │   ├── registry.go        # Device registry (all discovered devices)
│   │   └── builtin/           # Built-in plugins (compiled in)
│   │       ├── camera_ffmpeg.go    # RTSP + USB camera via ffmpeg
│   │       └── mic_ffmpeg.go       # System mic via ffmpeg
│   ├── vision/
│   │   ├── analyze.go         # Single-frame analysis via Trio API
│   │   ├── watch.go           # Background monitoring job
│   │   ├── motion.go          # Motion detection (frame diff)
│   │   └── trio_client.go     # HTTP client for Trio API
│   ├── config/
│   │   ├── config.go          # YAML config loader
│   │   └── state.go           # Persistent state (~/.trioclaw/state.json)
│   └── mdns/
│       └── mdns.go            # Bonjour advertising
├── plugins/                   # Example external plugins
│   ├── camera-opencv/
│   │   ├── plugin.yaml
│   │   └── plugin.py
│   ├── sensor-tuya/
│   │   ├── plugin.yaml
│   │   └── plugin.js
│   └── gpio-raspi/
│       ├── plugin.yaml
│       └── plugin.sh
├── scripts/
│   ├── install.sh             # curl | sh installer
│   └── trioclaw.service       # systemd unit
└── Dockerfile
```

### User's Machine Layout

```
~/.trioclaw/
├── state.json                 # nodeId, token, gatewayUrl
├── config.yaml                # cameras, plugins, settings (optional)
└── plugins/                   # user-installed plugins
    └── my-custom-sensor/
        ├── plugin.yaml
        └── plugin.py
```

---

## 8. Device Access — How It Works

### Universal Approach: ffmpeg Subprocess (Zero CGO)

One external dependency covers ALL device types on ALL platforms:

```
┌──────────────┬───────────────────────────────────────────────────┐
│ Device       │ ffmpeg command                                    │
├──────────────┼───────────────────────────────────────────────────┤
│ macOS webcam │ ffmpeg -f avfoundation -i "0:none" -frames:v 1 … │
│ Linux webcam │ ffmpeg -f v4l2 -i /dev/video0 -frames:v 1 …      │
│ Windows cam  │ ffmpeg -f dshow -i video="Camera" -frames:v 1 …  │
│ USB camera   │ same as above (USB appears as native device)      │
│ RTSP camera  │ ffmpeg -rtsp_transport tcp -i rtsp://… -frames:v 1│
│ Pi CSI cam   │ libcamera-still -o frame.jpg (or v4l2 via ffmpeg) │
│ macOS mic    │ ffmpeg -f avfoundation -i ":0" -t 5 output.wav   │
│ Linux mic    │ ffmpeg -f pulse -i default -t 5 output.wav        │
│ Pi mic       │ ffmpeg -f alsa -i default -t 5 output.wav         │
└──────────────┴───────────────────────────────────────────────────┘
```

### Device Enumeration

```bash
# macOS (camera + mic):
ffmpeg -f avfoundation -list_devices true -i ""

# Linux (camera):
v4l2-ctl --list-devices        # or: ls /dev/video*

# Linux (mic):
arecord -l
```

Parse stderr output in Go — device list goes to stderr, not stdout.

### Go Libraries (optional, for specific paths)

| Library | CGO? | Use Case |
|---------|:----:|----------|
| `AlexEidt/Vidio` | No | Clean ffmpeg webcam wrapper (auto platform detection) |
| `blackjack/webcam` | No | Pure Go V4L2 on Linux (no ffmpeg for this path) |
| `bluenviron/gortsplib` | No (MJPEG) | Pure Go RTSP client (avoids ffmpeg for MJPEG streams) |
| `gen2brain/malgo` | Yes (minimal) | Native mic capture (no extra install on macOS) |

### MVP Approach: ffmpeg Only

For MVP, we use ffmpeg subprocess for everything. Zero CGO, zero Go dependencies for device access. Add pure-Go paths (gortsplib, blackjack/webcam) later for environments where ffmpeg isn't available.

---

## 9. MVP — Minimum Viable Product

### What the MVP Does

```
User installs TrioClaw → connects laptop camera + mic + optionally RTSP camera
→ pairs with OpenClaw Gateway → agent can see and hear through the devices
```

### MVP Scope

| Feature | In MVP? | Method |
|---------|:-------:|--------|
| Laptop webcam | ✅ | ffmpeg avfoundation/v4l2 |
| USB camera | ✅ | ffmpeg (same as webcam) |
| RTSP camera | ✅ | ffmpeg -rtsp_transport tcp |
| Laptop microphone | ✅ | ffmpeg avfoundation/pulse/alsa |
| OpenClaw Gateway | ✅ | WebSocket protocol v3 |
| camera.snap | ✅ | Standard OpenClaw command |
| camera.list | ✅ | Standard OpenClaw command |
| vision.analyze | ✅ | camera.snap + Trio API /check-once |
| Standalone CLI | ✅ | trioclaw snap / trioclaw watch |
| Plugin system | ❌ | Phase 2 |
| vision.watch | ❌ | Phase 2 |
| Motion detection | ❌ | Phase 2 |
| Smart home devices | ❌ | Phase 3 |
| WebRTC camera | ❌ | Phase 3 |

### MVP Architecture (simplified)

```
┌──────────────────────────────────────────────────┐
│  TrioClaw MVP (~1,500 lines Go)                   │
│                                                   │
│  ┌────────────┐  ┌────────────┐  ┌─────────────┐│
│  │  Capture    │  │  Gateway   │  │  Trio API   ││
│  │             │  │  Client    │  │  Client     ││
│  │ ffmpeg      │  │            │  │             ││
│  │ subprocess  │  │ WebSocket  │  │ POST        ││
│  │ (cam + mic) │  │ pair/auth  │  │ /check-once ││
│  │             │  │ invoke     │  │             ││
│  └──────┬─────┘  └──────┬─────┘  └──────┬──────┘│
│         │               │               │        │
│         └───────────────┼───────────────┘        │
│                         │                         │
│              ┌──────────┴──────────┐             │
│              │   Command Router    │             │
│              │                     │             │
│              │ camera.snap → ffmpeg │             │
│              │ camera.list → enum   │             │
│              │ vision.analyze       │             │
│              │  → snap + Trio API   │             │
│              └─────────────────────┘             │
└──────────────────────────────────────────────────┘
```

### MVP CLI Commands

```bash
# Pair with Gateway (one time)
trioclaw pair --gateway ws://192.168.1.100:18789
# → "Pairing request sent. Approve: openclaw devices approve abc123"
# → "✓ Paired. Token saved."

# Run as node (main mode)
trioclaw run [--camera rtsp://192.168.1.50/stream]
# → "Connected to Gateway."
# → "Devices: Webcam (built-in), RTSP Camera (192.168.1.50)"
# → "Microphone: MacBook Pro Microphone"
# → "Ready. Commands: camera.snap, camera.list, vision.analyze"

# One-shot snap + analyze (standalone, no Gateway)
trioclaw snap --analyze "what do you see?"
# → captures webcam frame → calls Trio API → prints result
# → "A person sitting at a desk with a laptop and coffee mug."

# One-shot snap from RTSP
trioclaw snap --camera rtsp://192.168.1.50/stream --analyze "is anyone there?"

# Check dependencies
trioclaw doctor
# → ✓ ffmpeg found: 6.1.1
# → ✓ Camera: FaceTime HD Camera (built-in)
# → ✓ Microphone: MacBook Pro Microphone
# → ✓ Network: can reach trio.machinefi.com
# → ✗ Gateway: not configured (run trioclaw pair)
```

### MVP File Structure

```
TrioClaw/
├── go.mod
├── go.sum
├── cmd/
│   └── trioclaw/
│       └── main.go               # CLI: pair, run, snap, doctor (~200 lines)
├── internal/
│   ├── capture/
│   │   ├── ffmpeg.go             # ffmpeg subprocess: snap, clip, list devices (~250 lines)
│   │   └── mic.go                # ffmpeg mic capture: record, stream (~100 lines)
│   ├── gateway/
│   │   ├── client.go             # WebSocket: connect, pair, hello, reconnect (~300 lines)
│   │   ├── protocol.go           # Frame types, serialize/deserialize (~150 lines)
│   │   └── handler.go            # Invoke dispatcher: camera.*, vision.* (~150 lines)
│   ├── vision/
│   │   └── trio.go               # Trio API client: /check-once (~100 lines)
│   └── state/
│       └── state.go              # ~/.trioclaw/state.json persistence (~80 lines)
├── Makefile                       # build, cross-compile
├── README.md
├── DESIGN.md
└── SKILL.md
```

**Total: ~1,330 lines.** Completable in one week.

### MVP Implementation Plan

```
Day 1-2: Capture Layer
  ☐ internal/capture/ffmpeg.go
    - ListDevices() — parse ffmpeg -list_devices stderr
    - CaptureFrame(source string) ([]byte, error) — single JPEG via pipe:1
    - RecordClip(source, duration) ([]byte, error) — MP4 via temp file
    - Platform detection (avfoundation / v4l2 / dshow)
  ☐ internal/capture/mic.go
    - RecordAudio(duration) ([]byte, error) — WAV via temp file
  ☐ trioclaw doctor — check ffmpeg, list devices
  ☐ trioclaw snap — capture + print/save

Day 3-4: Gateway Protocol
  ☐ internal/gateway/protocol.go
    - ReqFrame, ResFrame, EventFrame structs
    - JSON marshal/unmarshal with payloadJSON double-encoding
  ☐ internal/gateway/client.go
    - Connect(url) — WebSocket dial
    - HandleChallenge(nonce) — connect request
    - Pair(displayName) — node.pair.request + wait for approval
    - Hello(token) — authenticate with saved token
    - Reconnect loop with exponential backoff (1s → 15s)
    - Ping/pong goroutine (30s interval)
  ☐ internal/state/state.go
    - Load/Save ~/.trioclaw/state.json (nodeId, token, gateway)

Day 5-6: Invoke Handlers + Vision
  ☐ internal/gateway/handler.go
    - camera.snap → CaptureFrame → base64 → invoke-res
    - camera.list → ListDevices → invoke-res
    - vision.analyze → CaptureFrame → POST Trio API /check-once → invoke-res
  ☐ internal/vision/trio.go
    - AnalyzeFrame(jpeg []byte, question string) (string, error)
    - POST /check-once with base64 frame + condition
  ☐ trioclaw run — main loop: connect, handle invokes, reconnect

Day 7: Polish + Test
  ☐ End-to-end test: pair → run → agent "take a photo" → see image
  ☐ End-to-end test: agent "what do you see?" → vision.analyze → answer
  ☐ RTSP camera test
  ☐ Error handling, logging
  ☐ README update with real demo
```

### First Demo: The Wow Moment

```
Terminal 1:
  $ trioclaw run
  Connected to Gateway (ws://macbook.local:18789)
  Devices:
    📷 webcam      — FaceTime HD Camera (built-in)
    📷 rtsp-front  — rtsp://192.168.1.50/stream1
    🎤 mic         — MacBook Pro Microphone
  Ready. Listening for commands...

Terminal 2 (OpenClaw chat):
  You:   what's on my webcam right now?
  Agent: I can see you sitting at your desk. There's a laptop, a coffee mug,
         and what appears to be a plant in the background.

  You:   is anyone at the front door? (use the RTSP camera)
  Agent: No one is at the front door right now. The porch is empty.

  You:   record 5 seconds from the webcam
  Agent: Here's a 5-second clip from your webcam. [video attachment]
```

---

## 10. Post-MVP Phases

### Phase 2: Intelligence + Monitoring (2 weeks)

```
  ☐ vision.watch — background monitoring with condition + interval
  ☐ Motion detection (frame diff in pure Go, or ffmpeg scene detect)
  ☐ Proactive events (agent.request when vision trigger fires)
  ☐ vision.status — list active watches
  ☐ camera.clip invoke handler
  ☐ Mic: continuous capture → send to Whisper API → transcription events
  ☐ trioclaw watch (standalone mode, no Gateway, webhook output)
```

### Phase 3: Plugin System (1 week)

```
  ☐ Plugin manager (scan dir, spawn subprocess, JSON Lines protocol)
  ☐ plugin.yaml manifest spec
  ☐ Plugin lifecycle (start, health, restart, shutdown)
  ☐ Example plugins: tuya-sensor (Python), gpio-raspi (bash)
  ☐ trioclaw plugin list
```

### Phase 4: Release (1 week)

```
  ☐ Cross-compile (linux/amd64, linux/arm64, darwin/arm64, windows/amd64)
  ☐ GitHub releases + install.sh
  ☐ Docker image
  ☐ SKILL.md for OpenClaw registry
  ☐ Demo GIFs in README
```

---

## Appendix A: Comparison with Existing Nodes

| | ClawGo | iOS Node | Windows Hub | TrioClaw |
|---|---|---|---|---|
| Language | Go | Swift | C# | **Go** |
| Sense | Hearing | Vision + Location | Screen + Shell | **Vision + Hearing + IoT** |
| Device access | subprocess (espeak) | native APIs | native APIs | **subprocess (ffmpeg) + plugins** |
| Local intelligence | None | None | None | **Motion detection + Trio API** |
| Protocol | TCP Bridge (deprecated) | WebSocket | WebSocket | **WebSocket** |
| Standalone? | No | No | No | **Yes** |
| Plugin system | No | No | No | **Yes** |
| Target hardware | RPi | iPhone | Windows PC | **Anything** |

## Appendix B: Why Not Other Languages

### Rust
- 3.4MB binary, 8MB RAM — impressive but unnecessary on RPi 4 (1-8GB)
- Contributor accessibility is the dealbreaker: 45% of Rust devs cite complexity as barrier
- ZeroClaw chose Rust as a statement piece; we need contributor velocity

### Python
- Richest device ecosystem (OpenCV, PyAudio), but:
- No clean single-binary story (PyInstaller: 50-200MB, slow startup)
- 80-150MB RAM on RPi with OpenCV loaded
- Trio backend is Python — TrioClaw is a separate product, code sharing minimal

### Node.js / Bun
- OpenClaw Gateway is Node.js, but:
- 30-40MB idle RAM, no native camera access
- Bun only supports 64-bit (excludes older RPi)
- Same subprocess-for-devices pattern, but heavier runtime

### Go + CGO
- Would enable native OpenCV/PortAudio, but:
- Breaks cross-compilation (need C cross-compilers per target)
- ffmpeg subprocess gives same capability with zero CGO

## Appendix C: Protocol Detail — payloadJSON Double-Encoding

OpenClaw protocol double-encodes JSON in event payloads:

```json
{
  "type": "event",
  "event": "agent.request",
  "payloadJSON": "{\"message\":\"hello\",\"sessionKey\":\"main\"}"
}
```

Note: `payloadJSON` is a **string containing JSON**, not a nested object. This is a critical implementation detail inherited from ClawGo's TCP Bridge protocol and carried into the WebSocket protocol.
