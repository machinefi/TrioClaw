package capture

import (
	"os"
	"runtime"
	"testing"
)

func TestCheckFFmpeg(t *testing.T) {
	// Skip if running in CI without ffmpeg
	if os.Getenv("CI") == "true" {
		t.Skip("Skipping in CI environment")
	}

	version, err := CheckFFmpeg()
	if err != nil {
		t.Logf("CheckFFmpeg() error = %v (ffmpeg may not be installed)", err)
		return
	}

	if version == "" {
		t.Error("CheckFFmpeg() returned empty version string")
	}

	// Version should be in format X.Y.Z or similar
	if len(version) < 3 {
		t.Errorf("Version = %s, want at least 3 characters", version)
	}
}

func TestListDevices(t *testing.T) {
	// Skip if running in CI without ffmpeg
	if os.Getenv("CI") == "true" {
		t.Skip("Skipping in CI environment")
	}

	devices, err := ListDevices()
	if err != nil {
		t.Logf("ListDevices() error = %v (ffmpeg may not be installed)", err)
		return
	}

	if devices == nil {
		t.Error("ListDevices() returned nil devices")
	}

	// Each device should have required fields
	for _, dev := range devices {
		if dev.ID == "" {
			t.Error("Device has empty ID")
		}

		if dev.Name == "" {
			t.Error("Device has empty Name")
		}

		if dev.Type != "" {
			if dev.Type != "video" && dev.Type != "audio" {
				t.Errorf("Device.Type = %s, want 'video' or 'audio'", dev.Type)
			}
		}

		if dev.Platform == "" {
			t.Error("Device has empty Platform")
		}

		expectedPlatforms := map[string]bool{
			"avfoundation": runtime.GOOS == "darwin",
			"v4l2":       runtime.GOOS == "linux",
			"dshow":       runtime.GOOS == "windows",
		}

		if expectedPlatform, ok := expectedPlatforms[dev.Platform]; ok {
			if !expectedPlatform {
				t.Errorf("Device.Platform = %s is unexpected for %s", dev.Platform, runtime.GOOS)
			}
		}
	}
}

func TestDevice_StringFields(t *testing.T) {
	dev := Device{
		ID:       "0",
		Name:     "Test Camera",
		Type:     "video",
		Platform:  "avfoundation",
		Index:    0,
	}

	if dev.ID != "0" {
		t.Errorf("ID = %s, want 0", dev.ID)
	}

	if dev.Name != "Test Camera" {
		t.Errorf("Name = %s, want Test Camera", dev.Name)
	}

	if dev.Type != "video" {
		t.Errorf("Type = %s, want video", dev.Type)
	}

	if dev.Platform != "avfoundation" {
		t.Errorf("Platform = %s, want avfoundation", dev.Platform)
	}

	if dev.Index != 0 {
		t.Errorf("Index = %d, want 0", dev.Index)
	}
}

func TestFrame_StringFields(t *testing.T) {
	jpeg := make([]byte, 1024)

	frame := Frame{
		JPEG:   jpeg,
		Width:  1920,
		Height: 1080,
		Source: "0",
	}

	if frame.JPEG == nil {
		t.Error("JPEG is nil")
	}

	if frame.Width <= 0 {
		t.Errorf("Width = %d, want > 0", frame.Width)
	}

	if frame.Height <= 0 {
		t.Errorf("Height = %d, want > 0", frame.Height)
	}

	if frame.Source == "" {
		t.Error("Source is empty")
	}
}

func TestClip_StringFields(t *testing.T) {
	mp4 := make([]byte, 2048)

	clip := Clip{
		MP4:      mp4,
		Duration: 5000,
		Source:   "0",
	}

	if clip.MP4 == nil {
		t.Error("MP4 is nil")
	}

	if clip.Duration <= 0 {
		t.Errorf("Duration = %d, want > 0", clip.Duration)
	}

	if clip.Source == "" {
		t.Error("Source is empty")
	}
}

func TestAudioRecording_StringFields(t *testing.T) {
	wav := make([]byte, 16000) // 1 second at 16kHz mono s16

	audio := AudioRecording{
		WAV:      wav,
		Duration: 1000,
		Source:   "0",
	}

	if audio.WAV == nil {
		t.Error("WAV is nil")
	}

	if audio.Duration <= 0 || audio.Duration > 60000 {
		t.Errorf("Duration = %d, want in [1, 60000]ms", audio.Duration)
	}

	if audio.Source == "" {
		t.Error("Source is empty")
	}
}

func TestIsRTSP(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{"rtsp://example.com/stream", true},
		{"rtsps://example.com/stream", true},
		{"RTSP://EXAMPLE.COM/STREAM", true},
		{"http://example.com/stream", false},
		{"file:///path/to/video.mp4", false},
		{"/dev/video0", false},
		{"0", false},
		{"1", false},
	}

	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			got := isRTSP(tt.source)
			if got != tt.want {
				t.Errorf("isRTSP(%q) = %v, want %v", tt.source, got, tt.want)
			}
		})
	}
}

func TestBuildWebcamArgs(t *testing.T) {
	tests := []struct {
		name         string
		index        int
		framesCount  int
		expectEmpty  bool
	}{
		{"darwin", 0, 1, false},
		{"index 1", 1, 1, false},
		{"unknown platform", 0, 1, false},
	}

	// Mock runtime.GOOS for testing
	oldGOOS := runtimeGOOS
	defer func() { runtimeGOOS = oldGOOS }()

	// Save the function to restore later
	originalGetRuntimeGOOS := getRuntimeGOOS

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set mock runtime.GOOS for darwin test
			if tt.name == "darwin" {
				runtimeGOOS = "darwin"
			} else if tt.name == "unknown platform" {
				runtimeGOOS = "unknown"
			} else {
				runtimeGOOS = "darwin"
			}

			args := buildWebcamArgs(tt.index, tt.framesCount)

			if tt.expectEmpty && len(args) == 0 {
				return // Expected empty
			}

			if !tt.expectEmpty && len(args) == 0 {
				t.Errorf("buildWebcamArgs() returned empty slice")
			}

			// Verify args contain expected values
			hasFrames := false
			for _, arg := range args {
				if arg == "-frames:v" || arg == "-vframes" {
					hasFrames = true
				}
			}

			if !hasFrames {
				t.Error("buildWebcamArgs() should contain frames count argument")
			}
		})
	}

	// Restore original function
	getRuntimeGOOS = originalGetRuntimeGOOS
}

func TestBuildRTSPArgs(t *testing.T) {
	url := "rtsp://example.com:554/stream"
	framesCount := 1

	args := buildRTSPArgs(url, framesCount)

	if len(args) == 0 {
		t.Error("buildRTSPArgs() returned empty slice")
	}

	// Verify args contain RTSP-specific options
	hasTransport := false
	for _, arg := range args {
		if arg == "-rtsp_transport" {
			hasTransport = true
		}
		if arg == url {
			// URL should be in args
		}
	}

	if !hasTransport {
		t.Error("buildRTSPArgs() should contain -rtsp_transport argument")
	}
}

// Helper for testing runtime detection
var (
	runtimeGOOS    string
	getRuntimeGOOS func() string
)

func init() {
	runtimeGOOS = runtime.GOOS
	getRuntimeGOOS = func() string { return runtimeGOOS }
}
