---
name: trioclaw
display_name: TrioClaw — Eyes, Ears, Voice & Hands for AI
description: >
  Give your OpenClaw agent a complete physical presence.
  See through cameras, hear through microphones, speak through speakers,
  and control smart devices — all from a single lightweight node.
author: machinefi
repository: https://github.com/machinefi/trioclaw
category: perception
tags: [vision, camera, rtsp, microphone, tts, stt, iot, smart-home, home, surveillance, monitoring]
platforms: [linux, darwin, windows]
install: curl -sSL https://get.trioclaw.com | sh
---

# TrioClaw

Eyes, ears, voice, and hands for OpenClaw agents. Connects cameras, microphones,
speakers, and smart devices to your agent through a single lightweight node.

## Capabilities

### Eyes — Vision (MVP)

- **camera.snap** — Capture a photo from any connected camera
- **camera.list** — List available cameras
- **camera.clip** — Record a short video clip
- **vision.analyze** — Capture a photo and describe what's happening (Trio API VLM)
- **vision.watch** — Continuously monitor and alert on conditions (Phase 2)
- **vision.status** — Check cameras and active monitoring jobs (Phase 2)

### Ears — Hearing (Phase 2)

- **mic.listen** — Transcribe speech and ambient audio (Whisper API)
- **mic.detect** — Detect specific sounds (doorbell, alarm, glass break)

### Mouth — Voice (Phase 2)

- **tts.speak** — Speak text aloud through the local speaker

### Hands — Device Control (Phase 3)

- **device.list** — List controllable smart devices
- **device.control** — Send commands (on/off, brightness, lock/unlock, etc.)
- **device.status** — Query device state

## Quick Start

```bash
# Install
curl -sSL https://get.trioclaw.com | sh

# Pair with your Gateway
trioclaw pair --gateway ws://YOUR_GATEWAY:18789

# Start the node
trioclaw run --camera rtsp://YOUR_CAMERA_IP/stream
```

## Example Conversations

**Eyes:**
- "Take a photo from the front door camera"
- "What do you see on the garage camera?"
- "Watch the driveway and tell me when someone arrives"
- "Is there a package at the door right now?"

**Ears:**
- "What did I just say?"
- "Listen for the doorbell and let me know"

**Mouth:**
- "Announce that dinner is ready" (spoken through the local speaker)

**Hands:**
- "Turn on the porch light"
- "Lock the front door"
- "Set the living room lights to 50%"

## Standalone Mode

Works without OpenClaw too:

```bash
trioclaw watch rtsp://YOUR_CAMERA/stream \
  --when "is there a package at the door?" \
  --webhook https://hooks.slack.com/services/xxx
```

## Adding New Devices

Drop a folder in `~/.trioclaw/plugins/` with a `plugin.yaml` and an executable.
Plugins can be written in any language (Python, Go, Node.js, bash).

See [Plugin Author Guide](https://github.com/machinefi/trioclaw/blob/main/docs/plugins.md)
