// Package capture handles camera and device access via ffmpeg subprocess.
//
// All functions shell out to ffmpeg — zero CGO, works on macOS/Linux/Windows.
// Platform-specific ffmpeg flags (avfoundation vs v4l2 vs dshow) are handled
// internally based on runtime.GOOS.
//
// External dependency: ffmpeg must be installed on the system.
package capture

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// Device represents a discovered camera or audio input device.
type Device struct {
	ID       string // unique identifier: "0", "/dev/video0", "rtsp://..."
	Name     string // human-readable: "FaceTime HD Camera", "USB Webcam"
	Type     string // "video" or "audio"
	Platform string // "avfoundation", "v4l2", "dshow", "rtsp"
	Index    int    // device index on the system (-1 for RTSP)
}

// Frame is a captured image from a camera.
type Frame struct {
	JPEG   []byte // JPEG-encoded image bytes
	Width  int    // image width in pixels
	Height int    // image height in pixels
	Source string // which device captured this frame
}

// Clip is a recorded video segment.
type Clip struct {
	MP4      []byte // MP4-encoded video bytes
	Duration int    // duration in milliseconds
	Source   string // which device recorded this clip
}

var (
	ffmpegPath string
)

// CheckFFmpeg verifies that ffmpeg is installed and returns its version string.
// Returns ("6.1.1", nil) or ("", ErrFFmpegNotFound).
func CheckFFmpeg() (string, error) {
	if ffmpegPath != "" {
		// Already cached
		return getFFmpegVersion()
	}

	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", fmt.Errorf("ffmpeg not found: %w (install with 'brew install ffmpeg' or your package manager)", err)
	}
	ffmpegPath = path

	return getFFmpegVersion()
}

func getFFmpegVersion() (string, error) {
	cmd := exec.Command(ffmpegPath, "-version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ffmpeg -version failed: %w", err)
	}

	// Parse first line for version: "ffmpeg version 6.1.1 Copyright ..."
	lines := strings.Split(string(output), "\n")
	if len(lines) == 0 {
		return "", fmt.Errorf("ffmpeg returned no output")
	}

	// Extract version number using regex
	re := regexp.MustCompile(`ffmpeg version (\d+\.\d+\.\d+)`)
	matches := re.FindStringSubmatch(lines[0])
	if len(matches) < 2 {
		return "", fmt.Errorf("could not parse ffmpeg version from: %s", lines[0])
	}

	return matches[1], nil
}

// ListDevices enumerates all available video and audio devices on the system.
//
// On macOS: runs "ffmpeg -f avfoundation -list_devices true -i ''"
// On Linux: runs "v4l2-ctl --list-devices" + "arecord -l"
// On Windows: runs "ffmpeg -f dshow -list_devices true -i dummy"
//
// The output goes to stderr (ffmpeg quirk) — parse it with parseDeviceList().
func ListDevices() ([]Device, error) {
	var devices []Device

	switch runtime.GOOS {
	case "darwin":
		darwinDevices, err := listDevicesDarwin()
		if err != nil {
			return nil, err
		}
		devices = append(devices, darwinDevices...)

	case "linux":
		videoDevices, err := listDevicesLinuxVideo()
		if err != nil {
			// v4l2-ctl might not be installed, try ffmpeg fallback
			return listDevicesFFmpeg()
		}
		devices = append(devices, videoDevices...)

		audioDevices, err := listDevicesLinuxAudio()
		if err == nil {
			devices = append(devices, audioDevices...)
		}

	case "windows":
		return listDevicesWindows()

	default:
		return nil, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return devices, nil
}

// listDevicesDarwin lists devices on macOS using avfoundation.
func listDevicesDarwin() ([]Device, error) {
	cmd := exec.Command(ffmpegPath, "-f", "avfoundation", "-list_devices", "true", "-i", "")
	// Suppress the "Output #0" error message that ffmpeg always prints
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// This command will fail with exit code 1, but that's expected
	_ = cmd.Run()

	return parseDeviceListDarwin(stderr.String()), nil
}

// parseDeviceListDarwin parses ffmpeg -list_devices output on macOS.
func parseDeviceListDarwin(output string) []Device {
	var devices []Device
	lines := strings.Split(output, "\n")
	var currentType string
	index := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Detect section headers
		if strings.Contains(line, "AVFoundation video devices:") {
			currentType = "video"
			continue
		}
		if strings.Contains(line, "AVFoundation audio devices:") {
			currentType = "audio"
			continue
		}

		// Skip empty lines and error messages
		if line == "" || strings.HasPrefix(line, "Output") || strings.Contains(line, "Error") {
			continue
		}

		// Parse device lines: "[0] FaceTime HD Camera"
		if strings.HasPrefix(line, "[") && currentType != "" {
			// Extract device name
			if idx := strings.Index(line, "] "); idx > 0 {
				name := strings.TrimSpace(line[idx+2:])
				if name != "" {
					devices = append(devices, Device{
						ID:       strconv.Itoa(index),
						Name:     name,
						Type:     currentType,
						Platform: "avfoundation",
						Index:    index,
					})
					index++
				}
			}
		}
	}

	return devices
}

// listDevicesLinuxVideo lists video devices on Linux using v4l2-ctl.
func listDevicesLinuxVideo() ([]Device, error) {
	cmd := exec.Command("v4l2-ctl", "--list-devices")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}

	return parseV4L2Devices(string(output)), nil
}

// parseV4L2Devices parses v4l2-ctl output.
func parseV4L2Devices(output string) []Device {
	var devices []Device
	lines := strings.Split(output, "\n")
	var currentName string
	index := 0

	reDevicePath := regexp.MustCompile(`/dev/video(\d+)`)

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Device name lines end with colon
		if strings.HasSuffix(line, ":") {
			currentName = strings.TrimSuffix(line, ":")
			continue
		}

		// Device path lines
		matches := reDevicePath.FindStringSubmatch(line)
		if len(matches) > 1 && currentName != "" {
			devices = append(devices, Device{
				ID:       matches[1], // use the video number
				Name:     currentName,
				Type:     "video",
				Platform: "v4l2",
				Index:    index,
			})
			currentName = ""
			index++
		}
	}

	return devices
}

// listDevicesLinuxAudio lists audio devices on Linux using arecord.
func listDevicesLinuxAudio() ([]Device, error) {
	cmd := exec.Command("arecord", "-l")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}

	return parseAudioDevicesLinux(string(output)), nil
}

// parseAudioDevicesLinux parses arecord -l output.
func parseAudioDevicesLinux(output string) []Device {
	var devices []Device
	lines := strings.Split(output, "\n")
	var currentCard string
	currentNum := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Card header: "card 0: Device Name [Device-HW]"
		if strings.HasPrefix(line, "card ") {
			if idx := strings.Index(line, ": "); idx > 0 {
				currentCard = strings.TrimSpace(line[idx+2:])
				// Extract just the card number
				if numIdx := strings.Index(line, "card "); numIdx >= 0 {
					numStr := strings.TrimSpace(line[numIdx+5 : idx])
					currentNum, _ = strconv.Atoi(numStr)
				}
			}
			continue
		}

		// Device line: "  Subdevice #0: subdevice #0"
		if strings.Contains(line, "Subdevice") && currentCard != "" {
			devices = append(devices, Device{
				ID:       "hw:" + strconv.Itoa(currentNum) + ",0",
				Name:     currentCard,
				Type:     "audio",
				Platform: "alsa",
				Index:    currentNum,
			})
			currentCard = ""
		}
	}

	return devices
}

// listDevicesWindows lists devices on Windows using dshow.
func listDevicesWindows() ([]Device, error) {
	cmd := exec.Command(ffmpegPath, "-f", "dshow", "-list_devices", "true", "-i", "dummy")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	_ = cmd.Run()
	return parseDeviceListWindows(stderr.String()), nil
}

// parseDeviceListWindows parses ffmpeg -list_devices output on Windows.
func parseDeviceListWindows(output string) []Device {
	var devices []Device
	lines := strings.Split(output, "\n")
	var currentType string
	index := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Detect section headers
		if strings.Contains(line, "DirectShow video devices:") {
			currentType = "video"
			continue
		}
		if strings.Contains(line, "DirectShow audio devices:") {
			currentType = "audio"
			continue
		}

		// Parse device lines: "[0] Integrated Camera"
		if strings.HasPrefix(line, "[") && currentType != "" {
			if idx := strings.Index(line, "] "); idx > 0 {
				name := strings.TrimSpace(line[idx+2:])
				if name != "" {
					devices = append(devices, Device{
						ID:       strconv.Itoa(index),
						Name:     name,
						Type:     currentType,
						Platform: "dshow",
						Index:    index,
					})
					index++
				}
			}
		}
	}

	return devices
}

// listDevicesFFmpeg is a fallback using only ffmpeg for device enumeration.
func listDevicesFFmpeg() ([]Device, error) {
	switch runtime.GOOS {
	case "darwin":
		return listDevicesDarwin()
	case "linux":
		// Try with v4l2 input for video only
		cmd := exec.Command(ffmpegPath, "-f", "v4l2", "-list_formats", "all", "-i", "/dev/video0")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		_ = cmd.Run()
		// This is a simple fallback; actual parsing would be more complex
		return []Device{
			{ID: "0", Name: "/dev/video0", Type: "video", Platform: "v4l2", Index: 0},
		}, nil
	case "windows":
		return listDevicesWindows()
	}
	return nil, fmt.Errorf("unsupported platform")
}

// CaptureFrame captures a single JPEG frame from the given source.
//
// source can be:
//   - "" or "0"                  → default webcam (device index 0)
//   - "1", "2"                   → webcam by index
//   - "/dev/video0"              → Linux V4L2 device path
//   - "rtsp://user:pass@host/p"  → RTSP stream
//
// The frame is captured by running ffmpeg with -frames:v 1 and piping JPEG
// to stdout (pipe:1). No temp files needed.
func CaptureFrame(source string) (*Frame, error) {
	return CaptureFrameCtx(context.Background(), source)
}

// CaptureFrameCtx is CaptureFrame with context for cancellation/timeout.
func CaptureFrameCtx(ctx context.Context, source string) (*Frame, error) {
	// Check ffmpeg
	if ffmpegPath == "" {
		if _, err := CheckFFmpeg(); err != nil {
			return nil, err
		}
	}

	// Determine source type and build args
	var args []string
	var captureSource string

	if source == "" {
		source = "0" // default to first webcam
	}

	if isRTSP(source) {
		args = buildRTSPArgs(source, 1)
		captureSource = source
	} else {
		// Parse as device index or path
		if strings.HasPrefix(source, "/dev/") {
			// Linux device path
			args = buildV4L2Args(source, 1)
			captureSource = source
		} else {
			// Device index
			index, err := strconv.Atoi(source)
			if err != nil {
				return nil, fmt.Errorf("invalid source: %s (use device index like '0', device path like '/dev/video0', or RTSP URL)", source)
			}
			args = buildWebcamArgs(index, 1)
			captureSource = source
		}
	}

	// Execute ffmpeg
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	// We don't care about stderr for capture, it's usually noise
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg capture failed: %w", err)
	}

	// Return the captured frame
	return &Frame{
		JPEG:   stdout.Bytes(),
		Source: captureSource,
		// Width/Height would need ffmpeg to report them, using parse of JPEG header
		// For MVP, we'll return 0s and let the caller determine size
	}, nil
}

// RecordClip records a video clip from the given source.
//
// durationMs: recording duration in milliseconds (e.g., 5000 for 5 seconds).
// Uses a temp file because MP4 needs seekable output (can't pipe).
func RecordClip(source string, durationMs int) (*Clip, error) {
	// Check ffmpeg
	if ffmpegPath == "" {
		if _, err := CheckFFmpeg(); err != nil {
			return nil, err
		}
	}

	// Create temp file
	tmpfile, err := os.CreateTemp("", "trioclaw-*.mp4")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpfile.Name()
	defer os.Remove(tmpPath)
	tmpfile.Close()

	// Build args
	var args []string
	seconds := float64(durationMs) / 1000.0

	if isRTSP(source) {
		args = buildRTSPArgs(source, 0) // 0 frames = continuous
	} else if strings.HasPrefix(source, "/dev/") {
		args = buildV4L2Args(source, 0)
	} else {
		index, _ := strconv.Atoi(source)
		args = buildWebcamArgs(index, 0)
	}

	// Add recording args
	args = append(args,
		"-t", fmt.Sprintf("%.2f", seconds),
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-y", tmpPath,
	)

	// Execute
	cmd := exec.Command(ffmpegPath, args...)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg recording failed: %w", err)
	}

	// Read the file
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read recorded clip: %w", err)
	}

	return &Clip{
		MP4:      data,
		Duration: durationMs,
		Source:   source,
	}, nil
}

// isRTSP returns true if the source string looks like an RTSP URL.
// Case-insensitive check for RTSP URLs (RFC 2326 defines RTSP in lowercase).
func isRTSP(source string) bool {
	lower := strings.ToLower(source)
	return strings.HasPrefix(lower, "rtsp://") || strings.HasPrefix(lower, "rtsps://")
}

// buildWebcamArgs returns ffmpeg args for capturing from a local webcam.
// Handles platform detection (avfoundation/v4l2/dshow).
func buildWebcamArgs(deviceIndex int, framesCount int) []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"-f", "avfoundation",
			"-i", fmt.Sprintf("%d:none", deviceIndex), // video only, no audio
			"-frames:v", strconv.Itoa(framesCount),
			"-f", "image2",
			"-vcodec", "mjpeg",
			"-",
		}

	case "linux":
		return []string{
			"-f", "v4l2",
			"-i", fmt.Sprintf("/dev/video%d", deviceIndex),
			"-frames:v", strconv.Itoa(framesCount),
			"-f", "image2",
			"-vcodec", "mjpeg",
			"-",
		}

	case "windows":
		// On Windows, we'd need to first get the device name from list
		// For MVP, assume dshow will find the device by index somehow
		// This is a simplification - real implementation would cache device names
		return []string{
			"-f", "dshow",
			"-i", fmt.Sprintf("video=%s", getWindowsDeviceName(deviceIndex)),
			"-frames:v", strconv.Itoa(framesCount),
			"-f", "image2",
			"-vcodec", "mjpeg",
			"-",
		}

	default:
		return []string{}
	}
}

// buildV4L2Args returns ffmpeg args for Linux V4L2 devices.
func buildV4L2Args(devicePath string, framesCount int) []string {
	return []string{
		"-f", "v4l2",
		"-i", devicePath,
		"-frames:v", strconv.Itoa(framesCount),
		"-f", "image2",
		"-vcodec", "mjpeg",
		"-",
	}
}

// buildRTSPArgs returns ffmpeg args for capturing from an RTSP stream.
func buildRTSPArgs(url string, framesCount int) []string {
	args := []string{
		"-rtsp_transport", "tcp",
		"-stimeout", "5000000", // 5 second timeout
		"-i", url,
		"-frames:v", strconv.Itoa(framesCount),
		"-f", "image2",
		"-vcodec", "mjpeg",
		"-",
	}
	return args
}

// getWindowsDeviceName is a helper for Windows - in real implementation
// this would cache device names from listDevicesWindows()
func getWindowsDeviceName(index int) string {
	return fmt.Sprintf("video=%d", index)
}

// parseDeviceList is a generic device list parser for ffmpeg -list_devices output.
func parseDeviceList(stderr string) []Device {
	var devices []Device

	switch runtime.GOOS {
	case "darwin":
		return parseDeviceListDarwin(stderr)
	case "linux":
		// This is simplified - real implementation would parse ffmpeg output
		// which is less structured than v4l2-ctl
		return devices
	case "windows":
		return parseDeviceListWindows(stderr)
	}

	return devices
}

// JSONDevice exports Device as JSON for debugging/plugin compatibility.
func (d *Device) JSONDevice() map[string]interface{} {
	return map[string]interface{}{
		"id":       d.ID,
		"name":     d.Name,
		"type":     d.Type,
		"platform":  d.Platform,
		"index":    d.Index,
	}
}

// ToJSON converts a Device to JSON bytes.
func (d *Device) ToJSON() ([]byte, error) {
	return json.Marshal(d.JSONDevice())
}
