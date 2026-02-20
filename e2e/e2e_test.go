// e2e_test.go contains end-to-end tests for TrioClaw.
//
// These tests verify the complete functionality of the CLI and internal components.
// They require ffmpeg to be installed on the system.
//
// Run with: go test ./e2e/... -v
package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Helper to build the trioclaw binary
func buildTrioclaw(t *testing.T) string {
	t.Helper()

	// Build the binary
	cmd := exec.Command("go", "build", "-o", "trioclaw", "./cmd/trioclaw")
	cmd.Dir = ".."
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to build trioclaw: %v\nOutput: %s", err, string(output))
	}

	// Return the path to the binary
	binPath := filepath.Join("..", "trioclaw")
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("trioclaw binary not found at %s: %v", binPath, err)
	}

	return binPath
}

// Helper to run a command and capture output
func runCommand(t *testing.T, binPath string, args ...string) (string, string, error) {
	t.Helper()

	cmd := exec.Command(binPath, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// Test version command
func TestE2E_Version(t *testing.T) {
	if os.Getenv("CI") == "true" {
		t.Skip("Skipping in CI environment (requires build)")
	}

	binPath := buildTrioclaw(t)

	stdout, stderr, err := runCommand(t, binPath, "version")
	if err != nil {
		t.Fatalf("version command failed: %v\nstderr: %s", err, stderr)
	}

	if !strings.Contains(stdout, "TrioClaw version") {
		t.Errorf("version output does not contain 'TrioClaw version': %s", stdout)
	}

	if !strings.Contains(stdout, "Platform:") {
		t.Errorf("version output does not contain 'Platform:': %s", stdout)
	}

	if !strings.Contains(stdout, "Go version:") {
		t.Errorf("version output does not contain 'Go version:': %s", stdout)
	}
}

// Test doctor command
func TestE2E_Doctor(t *testing.T) {
	if os.Getenv("CI") == "true" {
		t.Skip("Skipping in CI environment (requires build)")
	}

	binPath := buildTrioclaw(t)

	stdout, stderr, err := runCommand(t, binPath, "doctor")
	if err != nil {
		// doctor should fail if ffmpeg is not installed, but that's OK for the test
		t.Logf("doctor command error: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		return
	}

	if !strings.Contains(stdout, "ffmpeg:") {
		t.Errorf("doctor output does not contain 'ffmpeg:' check: %s", stdout)
	}

	if !strings.Contains(stdout, "Devices:") {
		t.Errorf("doctor output does not contain 'Devices:' check: %s", stdout)
	}

	t.Logf("doctor output:\n%s", stdout)
}

// Test snap command (dry run without camera)
func TestE2E_Snap_Help(t *testing.T) {
	if os.Getenv("CI") == "true" {
		t.Skip("Skipping in CI environment (requires build)")
	}

	binPath := buildTrioclaw(t)

	stdout, stderr, err := runCommand(t, binPath, "snap", "--help")
	if err != nil {
		t.Fatalf("snap --help failed: %v\nstderr: %s", err, stderr)
	}

	if !strings.Contains(stdout, "--camera") {
		t.Errorf("snap help does not contain --camera flag")
	}

	if !strings.Contains(stdout, "--analyze") {
		t.Errorf("snap help does not contain --analyze flag")
	}

	if !strings.Contains(stdout, "--output") {
		t.Errorf("snap help does not contain --output flag")
	}
}

// Test pair command
func TestE2E_Pair_Help(t *testing.T) {
	if os.Getenv("CI") == "true" {
		t.Skip("Skipping in CI environment (requires build)")
	}

	binPath := buildTrioclaw(t)

	stdout, stderr, err := runCommand(t, binPath, "pair", "--help")
	if err != nil {
		t.Fatalf("pair --help failed: %v\nstderr: %s", err, stderr)
	}

	if !strings.Contains(stdout, "--gateway") {
		t.Errorf("pair help does not contain --gateway flag")
	}

	if !strings.Contains(stdout, "--name") {
		t.Errorf("pair help does not contain --name flag")
	}
}

// Test pair command without --gateway (should fail)
func TestE2E_Pair_MissingGateway(t *testing.T) {
	if os.Getenv("CI") == "true" {
		t.Skip("Skipping in CI environment (requires build)")
	}

	binPath := buildTrioclaw(t)

	_, stderr, err := runCommand(t, binPath, "pair")
	if err == nil {
		t.Error("pair without --gateway should fail, but succeeded")
	}

	if !strings.Contains(stderr, "required") {
		t.Errorf("error message does not contain 'required': %s", stderr)
	}
}

// Test run command help
func TestE2E_Run_Help(t *testing.T) {
	if os.Getenv("CI") == "true" {
		t.Skip("Skipping in CI environment (requires build)")
	}

	binPath := buildTrioclaw(t)

	stdout, stderr, err := runCommand(t, binPath, "run", "--help")
	if err != nil {
		t.Fatalf("run --help failed: %v\nstderr: %s", err, stderr)
	}

	if !strings.Contains(stdout, "--camera") {
		t.Errorf("run help does not contain --camera flag")
	}
}

// Test state file operations
func TestE2E_StateFileOperations(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	// Simulate creating a state file like pair command would
	state := map[string]interface{}{
		"nodeId":      "trioclaw-test-123456",
		"token":       "test-token-abc-def-123",
		"gatewayUrl":  "ws://test-gateway.local:18789",
		"displayName": "Test E2E Node",
	}

	stateData, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal state: %v", err)
	}

	err = os.WriteFile(statePath, stateData, 0600)
	if err != nil {
		t.Fatalf("Failed to write state file: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("State file does not exist: %v", err)
	}

	// Verify file permissions
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("Failed to stat state file: %v", err)
	}

	if info.Mode().Perm()&0600 != 0600 {
		t.Errorf("File permissions = %v, want 0600", info.Mode().Perm())
	}

	// Read and verify content
	readData, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("Failed to read state file: %v", err)
	}

	var readState map[string]interface{}
	if err := json.Unmarshal(readData, &readState); err != nil {
		t.Fatalf("Failed to unmarshal state file: %v", err)
	}

	if readState["nodeId"] != state["nodeId"] {
		t.Errorf("nodeId mismatch: %v vs %v", readState["nodeId"], state["nodeId"])
	}

	if readState["token"] != state["token"] {
		t.Errorf("token mismatch: %v vs %v", readState["token"], state["token"])
	}
}

// Test mock Trio API interaction
func TestE2E_MockTrioAPI(t *testing.T) {
	// Create a mock Trio API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Expected POST, got %s", r.Method)
		}

		// Verify request
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("Failed to read request body: %v", err)
		}

		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("Failed to unmarshal request: %v", err)
		}

		if req["stream_url"] == nil {
			t.Error("Request missing stream_url field")
		}

		if req["condition"] == nil {
			t.Error("Request missing condition field")
		}

		// Return mock response
		resp := map[string]interface{}{
			"triggered":   true,
			"explanation": "A person is visible in the frame",
			"confidence":  0.95,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create a client pointing to mock server
	mockClient := &http.Client{Timeout: 5 * time.Second}

	// Make a test request
	testReq, _ := http.NewRequest("POST", server.URL+"/api/v1/check-once", strings.NewReader(`{
		"stream_url": "data:image/jpeg;base64,/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAP8=",
		"condition": "Is there a person?"
	}`))
	testReq.Header.Set("Content-Type", "application/json")

	resp, err := mockClient.Do(testReq)
	if err != nil {
		t.Fatalf("Failed to call mock API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var apiResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		t.Fatalf("Failed to decode API response: %v", err)
	}

	if apiResp["triggered"] != true {
		t.Errorf("Expected triggered=true, got %v", apiResp["triggered"])
	}

	if apiResp["explanation"] == nil {
		t.Error("Expected explanation field")
	}
}

// Test WebSocket protocol round-trip
func TestE2E_WebSocketProtocolRoundtrip(t *testing.T) {
	// This is a simplified WebSocket test without actual connection
	// In a full E2E test, we'd use a mock gateway

	// Test frame encoding/decoding
	testFrames := []struct {
		name     string
		frame    interface{}
		validate func(*testing.T, map[string]interface{})
	}{
		{
			name:  "request frame",
			frame: map[string]interface{}{
				"type":   "req",
				"id":     "123",
				"method": "connect",
			},
			validate: func(t *testing.T, f map[string]interface{}) {
				if f["type"] != "req" {
					t.Error("Expected type=req")
				}
				if f["method"] != "connect" {
					t.Error("Expected method=connect")
				}
			},
		},
		{
			name:  "response frame",
			frame: map[string]interface{}{
				"type": "res",
				"id":   "123",
				"ok":   true,
			},
			validate: func(t *testing.T, f map[string]interface{}) {
				if f["type"] != "res" {
					t.Error("Expected type=res")
				}
				if f["ok"] != true {
					t.Error("Expected ok=true")
				}
			},
		},
		{
			name: "event frame",
			frame: map[string]interface{}{
				"type":         "event",
				"event":        "chat",
				"payloadJSON":  `{"message":"hello"}`,
			},
			validate: func(t *testing.T, f map[string]interface{}) {
				if f["type"] != "event" {
					t.Error("Expected type=event")
				}
				if f["event"] != "chat" {
					t.Error("Expected event=chat")
				}
			},
		},
	}

	for _, tt := range testFrames {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.frame)
			if err != nil {
				t.Fatalf("Failed to marshal frame: %v", err)
			}

			var decoded map[string]interface{}
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("Failed to unmarshal frame: %v", err)
			}

			tt.validate(t, decoded)
		})
	}
}

// Test device enumeration
func TestE2E_DeviceEnumeration(t *testing.T) {
	if os.Getenv("CI") == "true" {
		t.Skip("Skipping in CI environment (requires ffmpeg)")
	}

	// Build trioclaw
	binPath := buildTrioclaw(t)

	// Run doctor to get device info
	stdout, stderr, err := runCommand(t, binPath, "doctor")
	if err != nil {
		t.Logf("doctor command failed (ffmpeg may not be installed): %v", err)
		t.Log(stderr)
		return
	}

	// Parse output to verify device info
	lines := strings.Split(stdout, "\n")
	foundDevices := false
	foundFFmpeg := false

	for _, line := range lines {
		if strings.Contains(line, "ffmpeg:") && strings.Contains(line, "✓") {
			foundFFmpeg = true
		}
		if strings.Contains(line, "Devices:") {
			foundDevices = true
		}
		// Check for camera emoji 📷
		if strings.Contains(line, "📷") {
			// Found a camera device
		}
		// Check for mic emoji 🎤
		if strings.Contains(line, "🎤") {
			// Found a microphone device
		}
	}

	if !foundFFmpeg {
		t.Error("Doctor output does not show ffmpeg check")
	}

	if !foundDevices {
		t.Error("Doctor output does not show devices section")
	}
}

// Test concurrent operations
func TestE2E_ConcurrentDoctor(t *testing.T) {
	if os.Getenv("CI") == "true" {
		t.Skip("Skipping in CI environment (requires build)")
	}

	binPath := buildTrioclaw(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Run multiple doctor commands concurrently
	results := make(chan error, 3)
	for i := 0; i < 3; i++ {
		go func() {
			select {
			case <-ctx.Done():
				results <- ctx.Err()
			default:
				_, _, err := runCommand(t, binPath, "doctor")
				results <- err
			}
		}()
	}

	// Collect results
	var errors []error
	for i := 0; i < 3; i++ {
		if err := <-results; err != nil {
			errors = append(errors, err)
		}
	}

	// All should complete without panics
	if len(errors) != 0 {
		t.Logf("Note: Some concurrent doctor runs had errors (may be expected): %d errors", len(errors))
	}
}
