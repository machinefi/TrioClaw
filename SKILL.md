---
name: trioclaw
display_name: TrioClaw — AI Vision & Sensing Node
description: >
  Give your OpenClaw agent eyes, ears, and senses.
  Connect any camera (RTSP, USB, webcam), microphone, or smart device
  and let your agent see, hear, and understand the real world.
author: machinefi
repository: https://github.com/machinefi/trioclaw
category: perception
tags: [vision, camera, rtsp, microphone, iot, home, surveillance, monitoring, smart-home]
platforms: [linux, darwin, windows]
install: curl -sSL https://get.trioclaw.com | sh
---

# TrioClaw

AI vision and sensing node for OpenClaw. Connects cameras, microphones,
and smart devices to your agent.

## Capabilities

### Camera (Standard OpenClaw commands)
- **camera.snap** — Capture a photo from any connected camera
- **camera.list** — List available cameras
- **camera.clip** — Record a short video clip

### Vision (TrioClaw-exclusive, AI-powered)
- **vision.analyze** — Take a photo and explain what's happening
- **vision.watch** — Continuously monitor and alert on conditions
- **vision.status** — Check cameras and active monitoring jobs

### Audio (future)
- **mic.listen** — Transcribe ambient audio
- **mic.detect** — Detect specific sounds (doorbell, alarm, etc.)

## Quick Start

```bash
# Install
curl -sSL https://get.trioclaw.com | sh

# Pair with your Gateway
trioclaw pair --gateway ws://YOUR_GATEWAY:18789

# Start the node with a camera
trioclaw run --camera rtsp://YOUR_CAMERA_IP/stream
```

## Example Conversations

- "Take a photo from the front door camera"
- "What do you see on the garage camera?"
- "Watch the driveway and tell me when someone arrives"
- "Is there a package at the door right now?"
- "Summarize what happened on the patio in the last 10 minutes"
- "Alert me when my dog gets on the couch"

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
