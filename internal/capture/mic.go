// mic.go handles microphone capture via ffmpeg subprocess.
//
// Audio is captured as WAV (16kHz, mono, s16le) — suitable for STT APIs.
//
// Platform-specific input:
//   macOS: -f avfoundation -i ":0"     (audio device 0, no video)
//   Linux: -f pulse -i default          (PulseAudio default)
//   Linux: -f alsa -i default           (ALSA fallback, e.g. Raspberry Pi)
package capture

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
)

// AudioRecording is a captured audio segment.
type AudioRecording struct {
	WAV      []byte // WAV-encoded audio bytes (16kHz, mono, s16le)
	Duration int    // actual duration in milliseconds
	Source   string // which device captured this
}

// RecordAudio records audio from the default microphone for the given duration.
//
// durationMs: recording duration in milliseconds.
// Output format: WAV, 16kHz, mono, s16le — optimized for speech recognition APIs.
//
// ffmpeg args:
//   macOS: -f avfoundation -i ":0" -t {sec} -ac 1 -ar 16000 -sample_fmt s16 {tmpfile}
//   Linux: -f pulse -i default -t {sec} -ac 1 -ar 16000 -sample_fmt s16 {tmpfile}
func RecordAudio(durationMs int) (*AudioRecording, error) {
	return RecordAudioCtx(context.Background(), durationMs)
}

// RecordAudioCtx is RecordAudio with context for cancellation.
func RecordAudioCtx(ctx context.Context, durationMs int) (*AudioRecording, error) {
	// Check ffmpeg
	if ffmpegPath == "" {
		if _, err := CheckFFmpeg(); err != nil {
			return nil, err
		}
	}

	// Validate duration
	if durationMs <= 0 || durationMs > 60000 {
		return nil, fmt.Errorf("invalid duration: %dms (must be 1-60000)", durationMs)
	}

	// Create temp file
	tmpfile, err := os.CreateTemp("", "trioclaw-*.wav")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpfile.Name()
	tmpfile.Close()
	defer os.Remove(tmpPath)

	// Build ffmpeg args
	seconds := float64(durationMs) / 1000.0
	args := buildMicArgs(seconds)

	// Append output file
	args = append(args, tmpPath)

	// Execute ffmpeg
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg audio capture failed: %w", err)
	}

	// Read the recorded file
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read recorded audio: %w", err)
	}

	return &AudioRecording{
		WAV:      data,
		Duration: durationMs,
		Source:   "default",
	}, nil
}

// StreamMicrophone starts continuous audio capture from the default microphone.
// Returns an io.ReadCloser of raw PCM audio (16kHz, mono, s16le).
//
// The caller reads PCM chunks from the reader. Close the reader (or cancel ctx)
// to stop the ffmpeg process.
//
// ffmpeg args:
//   -f avfoundation -i ":0" -ac 1 -ar 16000 -f s16le -acodec pcm_s16le pipe:1
//
// Typical usage:
//   reader, stop, _ := StreamMicrophone(ctx)
//   defer stop()
//   buf := make([]byte, 3200) // 100ms of 16kHz 16-bit mono
//   for { n, _ := reader.Read(buf); process(buf[:n]) }
func StreamMicrophone(ctx context.Context) (io.ReadCloser, func(), error) {
	// Check ffmpeg
	if ffmpegPath == "" {
		if _, err := CheckFFmpeg(); err != nil {
			return nil, nil, err
		}
	}

	// Build args for streaming (no -t, output to pipe:1)
	args := buildMicArgs(0) // 0 duration = continuous
	args = append(args,
		"-f", "s16le",
		"-acodec", "pcm_s16le",
		"pipe:1",
	)

	// Start ffmpeg
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)

	// Create pipe for stdout
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// Suppress stderr
	cmd.Stderr = nil

	// Start the process
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Create a cleanup function
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			cmd.Process.Kill()
			cmd.Wait()
		})
	}

	// Wrap stdoutPipe in a ReadCloser that calls cleanup
	return &readCloserWrapper{
		Reader: stdoutPipe,
		close:  cleanup,
	}, cleanup, nil
}

// readCloserWrapper wraps an io.Reader with a close function.
type readCloserWrapper struct {
	io.Reader
	close func()
}

func (w *readCloserWrapper) Close() error {
	w.close()
	return nil
}

// buildMicArgs returns ffmpeg args for microphone capture on the current platform.
func buildMicArgs(durationSec float64) []string {
	switch runtime.GOOS {
	case "darwin":
		args := []string{
			"-f", "avfoundation",
			"-i", ":0", // audio device 0, no video
		}
		if durationSec > 0 {
			args = append(args, "-t", strconv.FormatFloat(durationSec, 'f', 2, 64))
		}
		args = append(args,
			"-ac", "1",       // mono
			"-ar", "16000",    // 16kHz sample rate
			"-sample_fmt", "s16", // signed 16-bit
		)
		return args

	case "linux":
		// Try PulseAudio first, fall back to ALSA
		// For MVP, we'll try pulse first; if it fails, the user can use alsa explicitly
		args := []string{
			"-f", "pulse",
			"-i", "default",
		}
		if durationSec > 0 {
			args = append(args, "-t", strconv.FormatFloat(durationSec, 'f', 2, 64))
		}
		args = append(args,
			"-ac", "1",
			"-ar", "16000",
			"-sample_fmt", "s16",
		)
		return args

	case "windows":
		args := []string{
			"-f", "dshow",
			"-i", "audio=default", // Use default audio device
		}
		if durationSec > 0 {
			args = append(args, "-t", strconv.FormatFloat(durationSec, 'f', 2, 64))
		}
		args = append(args,
			"-ac", "1",
			"-ar", "16000",
			"-sample_fmt", "s16",
		)
		return args

	default:
		return []string{}
	}
}

// buildMicArgsALSA returns ffmpeg args using ALSA (for Linux/Raspberry Pi).
func buildMicArgsALSA(durationSec float64) []string {
	args := []string{
		"-f", "alsa",
		"-i", "default",
	}
	if durationSec > 0 {
		args = append(args, "-t", strconv.FormatFloat(durationSec, 'f', 2, 64))
	}
	args = append(args,
		"-ac", "1",
		"-ar", "16000",
		"-sample_fmt", "s16",
	)
	return args
}
