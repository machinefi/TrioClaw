// handler.go dispatches invoke commands from the gateway to the appropriate
// capture/vision functions and sends results back.
//
// The Handler is a bridge between:
//   - Gateway protocol (invoke requests/results)
//   - Capture layer (ffmpeg camera/mic access)
//   - Vision layer (Trio API for VLM analysis)
//
// Command routing:
//   "camera.snap"     → capture.CaptureFrame → base64 JPEG → invoke result
//   "camera.list"     → capture.ListDevices → device list → invoke result
//   "camera.clip"     → capture.RecordClip → base64 MP4 → invoke result
//   "vision.analyze"  → capture.CaptureFrame → vision.Analyze → text → invoke result
package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/machinefi/trioclaw/internal/capture"
	"github.com/machinefi/trioclaw/internal/vision"
)

// Handler processes invoke requests from the gateway.
type Handler struct {
	devices     []capture.Device    // available devices
	trioClient  *vision.TrioClient  // Trio API client for VLM
	extraCameras []string           // additional camera sources (RTSP URLs, etc.)
	nodeID      string             // this node's ID
}

// NewHandler creates a handler with discovered devices and a Trio API client.
func NewHandler(devices []capture.Device, trioClient *vision.TrioClient, extraCameras []string) *Handler {
	return &Handler{
		devices:      devices,
		trioClient:   trioClient,
		extraCameras: extraCameras,
	}
}

// SetNodeID sets the node ID for invoke results.
func (h *Handler) SetNodeID(nodeID string) {
	h.nodeID = nodeID
}

// HandleInvoke dispatches an invoke request to the appropriate handler.
// Returns a result to send back to the gateway.
//
// This is called by Client.Run() when a "node.invoke.request" event arrives.
func (h *Handler) HandleInvoke(ctx context.Context, req InvokeRequest) InvokeResult {
	// Route command to appropriate handler
	switch req.Command {
	case "camera.snap":
		return h.handleCameraSnap(ctx, req)
	case "camera.list":
		return h.handleCameraList(ctx, req)
	case "camera.clip":
		return h.handleCameraClip(ctx, req)
	case "vision.analyze":
		return h.handleVisionAnalyze(ctx, req)
	default:
		return InvokeResult{
			ID:     req.ID,
			NodeID: h.nodeID,
			OK:     false,
			Error:   &ErrorPayload{Code: "UNAVAILABLE", Message: fmt.Sprintf("unknown command: %s", req.Command)},
		}
	}
}

// handleCameraSnap captures a single JPEG frame and returns it as base64.
//
// Params: CameraSnapParams (deviceId, maxWidth, quality)
// Result: CameraSnapResult (format, base64, width, height)
func (h *Handler) handleCameraSnap(ctx context.Context, req InvokeRequest) InvokeResult {
	// Parse params
	var params CameraSnapParams
	if err := DecodePayloadJSON(string(req.Params), &params); err != nil {
		return InvokeResult{
			ID:     req.ID,
			NodeID: h.nodeID,
			OK:     false,
			Error:   &ErrorPayload{Code: "INVALID_PARAMS", Message: fmt.Sprintf("failed to parse params: %v", err)},
		}
	}

	// Determine source
	source := params.DeviceID
	if source == "" {
		// Use default webcam (first video device)
		source = "0"
	}

	// Check if source is an extra camera (RTSP URL)
	if isExtraCameraID(source) {
		source = h.GetExtraCameraSource(source)
	}

	// Capture frame
	frame, err := capture.CaptureFrameCtx(ctx, source)
	if err != nil {
		return InvokeResult{
			ID:     req.ID,
			NodeID: h.nodeID,
			OK:     false,
			Error:   &ErrorPayload{Code: "CAPTURE_FAILED", Message: fmt.Sprintf("failed to capture frame: %v", err)},
		}
	}

	// Resize if maxWidth specified (MVP: simple implementation, would use ffmpeg resize)
	// For MVP, we'll skip resize and just return the captured frame

	// Base64 encode
	base64Data := base64.StdEncoding.EncodeToString(frame.JPEG)

	// Build result
	result := CameraSnapResult{
		Format: "jpeg",
		Base64: base64Data,
		Width:  1280, // Default - would parse from JPEG header
		Height: 720,
	}

	resultJSON := marshalResult(result)

	return InvokeResult{
		ID:     req.ID,
		NodeID: h.nodeID,
		OK:     true,
		Result: resultJSON,
	}
}

// handleCameraList returns all available cameras.
//
// No params.
// Result: CameraListResult (array of devices)
func (h *Handler) handleCameraList(ctx context.Context, req InvokeRequest) InvokeResult {
	var cameras []CameraDevice

	// Filter video devices
	for _, dev := range h.devices {
		if dev.Type == "video" {
			cameras = append(cameras, CameraDevice{
				ID:   dev.ID,
				Name: dev.Name,
			})
		}
	}

	// Add extra cameras (RTSP URLs specified via --camera flag)
	for i, camURL := range h.extraCameras {
		cameras = append(cameras, CameraDevice{
			ID:   fmt.Sprintf("extra-%d", i),
			Name: fmt.Sprintf("RTSP Camera %d (%s)", i+1, camURL),
		})
	}

	// Build result
	result := CameraListResult{
		Devices: cameras,
	}

	resultJSON := marshalResult(result)

	return InvokeResult{
		ID:     req.ID,
		NodeID: h.nodeID,
		OK:     true,
		Result: resultJSON,
	}
}

// handleCameraClip records a video clip and returns it as base64 MP4.
//
// Params: { durationMs: 5000, deviceId: "..." }
// Result: { format: "mp4", base64: "...", durationMs: 5000 }
func (h *Handler) handleCameraClip(ctx context.Context, req InvokeRequest) InvokeResult {
	// Parse params - MVP: use simple struct for clip params
	type ClipParams struct {
		DurationMs int    `json:"durationMs"`
		DeviceID   string `json:"deviceId"`
	}

	var params ClipParams
	if err := DecodePayloadJSON(string(req.Params), &params); err != nil {
		return InvokeResult{
			ID:     req.ID,
			NodeID: h.nodeID,
			OK:     false,
			Error:   &ErrorPayload{Code: "INVALID_PARAMS", Message: fmt.Sprintf("failed to parse params: %v", err)},
		}
	}

	// Set default duration
	durationMs := params.DurationMs
	if durationMs <= 0 {
		durationMs = 5000 // 5 seconds default
	}

	// Determine source
	source := params.DeviceID
	if source == "" {
		source = "0"
	}

	// Check if source is an extra camera (RTSP URL)
	if isExtraCameraID(source) {
		source = h.GetExtraCameraSource(source)
	}

	// Record clip
	clip, err := capture.RecordClip(source, durationMs)
	if err != nil {
		return InvokeResult{
			ID:     req.ID,
			NodeID: h.nodeID,
			OK:     false,
			Error:   &ErrorPayload{Code: "RECORD_FAILED", Message: fmt.Sprintf("failed to record clip: %v", err)},
		}
	}

	// Base64 encode
	base64Data := base64.StdEncoding.EncodeToString(clip.MP4)

	// Build result (using anonymous struct for MP4 result)
	type ClipResult struct {
		Format     string `json:"format"`
		Base64     string `json:"base64"`
		DurationMs int    `json:"durationMs"`
	}

	result := ClipResult{
		Format:     "mp4",
		Base64:     base64Data,
		DurationMs: clip.Duration,
	}

	resultJSON := marshalResult(result)

	return InvokeResult{
		ID:     req.ID,
		NodeID: h.nodeID,
		OK:     true,
		Result: resultJSON,
	}
}

// handleVisionAnalyze captures a frame and runs VLM analysis via the Trio API.
//
// This is TrioClaw's unique capability — not just returning a photo,
// but understanding what's in it.
//
// Params: VisionAnalyzeParams (question, deviceId)
// Result: VisionAnalyzeResult (answer, confidence, frame)
func (h *Handler) handleVisionAnalyze(ctx context.Context, req InvokeRequest) InvokeResult {
	// Parse params
	var params VisionAnalyzeParams
	if err := DecodePayloadJSON(string(req.Params), &params); err != nil {
		return InvokeResult{
			ID:     req.ID,
			NodeID: h.nodeID,
			OK:     false,
			Error:   &ErrorPayload{Code: "INVALID_PARAMS", Message: fmt.Sprintf("failed to parse params: %v", err)},
		}
	}

	// Validate question
	if params.Question == "" {
		return InvokeResult{
			ID:     req.ID,
			NodeID: h.nodeID,
			OK:     false,
			Error:   &ErrorPayload{Code: "INVALID_PARAMS", Message: "question parameter is required"},
		}
	}

	// Determine source
	source := params.DeviceID
	if source == "" {
		source = "0"
	}

	// Check if source is an extra camera (RTSP URL)
	if isExtraCameraID(source) {
		source = h.GetExtraCameraSource(source)
	}

	// Capture frame
	frame, err := capture.CaptureFrameCtx(ctx, source)
	if err != nil {
		return InvokeResult{
			ID:     req.ID,
			NodeID: h.nodeID,
			OK:     false,
			Error:   &ErrorPayload{Code: "CAPTURE_FAILED", Message: fmt.Sprintf("failed to capture frame: %v", err)},
		}
	}

	// Call Trio API for analysis
	analyzeResult, err := h.trioClient.Analyze(ctx, frame.JPEG, params.Question)
	if err != nil {
		return InvokeResult{
			ID:     req.ID,
			NodeID: h.nodeID,
			OK:     false,
			Error:   &ErrorPayload{Code: "ANALYSIS_FAILED", Message: fmt.Sprintf("failed to analyze frame: %v", err)},
		}
	}

	// Build frame result
	frameResult := CameraSnapResult{
		Format: "jpeg",
		Base64: base64.StdEncoding.EncodeToString(frame.JPEG),
		Width:  1280,
		Height: 720,
	}

	// Build final result
	result := VisionAnalyzeResult{
		Answer:     analyzeResult.Explanation,
		Confidence: analyzeResult.Confidence,
		Frame:      &frameResult,
	}

	resultJSON := marshalResult(result)

	return InvokeResult{
		ID:     req.ID,
		NodeID: h.nodeID,
		OK:     true,
		Result: resultJSON,
	}
}

// marshalResult is a helper to serialize a result struct into json.RawMessage
// for inclusion in InvokeResult.
func marshalResult(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{"error":"failed to marshal result"}`)
	}
	// Note: The protocol expects payloadJSON to be a JSON string, so we return the raw JSON
	return data
}

// GetExtraCameraSource returns the source URL for an extra camera by ID.
func (h *Handler) GetExtraCameraSource(id string) string {
	if !isExtraCameraID(id) {
		return ""
	}
	// Extract index from "extra-{i}"
	var index int
	fmt.Sscanf(id, "extra-%d", &index)
	if index >= 0 && index < len(h.extraCameras) {
		return h.extraCameras[index]
	}
	return ""
}

// isExtraCameraID checks if the ID refers to an extra camera.
func isExtraCameraID(id string) bool {
	if len(id) < 7 {
		return false
	}
	prefix := id[:7]
	return prefix == "extra-"
}
