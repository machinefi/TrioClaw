// handler.go dispatches invoke commands from the gateway to the appropriate
// capture/vision/plugin functions and sends results back.
//
// The Handler is a bridge between:
//   - Gateway protocol (invoke requests/results)
//   - Capture layer (ffmpeg camera/mic access)
//   - Vision layer (Trio API for VLM analysis)
//   - Plugin layer (device control via Home Assistant, scripts, etc.)
//
// Command routing:
//   "camera.snap"     → capture.CaptureFrame → base64 JPEG → invoke result
//   "camera.list"     → capture.ListDevices → device list → invoke result
//   "camera.clip"     → capture.RecordClip → base64 MP4 → invoke result
//   "vision.analyze"  → capture.CaptureFrame → vision.Analyze → text → invoke result
//   "device.list"     → plugin.Registry.ListAllDevices → device list → invoke result
//   "device.control"  → plugin.Registry.Execute → result → invoke result
package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/machinefi/trioclaw/internal/capture"
	"github.com/machinefi/trioclaw/internal/config"
	"github.com/machinefi/trioclaw/internal/plugin"
	"github.com/machinefi/trioclaw/internal/triocore"
	"github.com/machinefi/trioclaw/internal/vision"
)

// Handler processes invoke requests from the gateway.
type Handler struct {
	devices      []capture.Device      // available devices
	trioClient   *vision.TrioClient    // Trio API client for VLM
	extraCameras []string              // additional camera sources (RTSP URLs, etc.)
	plugins      *plugin.Registry      // device control plugins
	watchMgr     *triocore.Manager     // watch manager for vision.watch commands
	nodeID       string                // this node's ID
}

// NewHandler creates a handler with discovered devices and a Trio API client.
func NewHandler(devices []capture.Device, trioClient *vision.TrioClient, extraCameras []string) *Handler {
	return &Handler{
		devices:      devices,
		trioClient:   trioClient,
		extraCameras: extraCameras,
		plugins:      plugin.NewRegistry(),
	}
}

// SetPlugins sets the plugin registry for device control.
func (h *Handler) SetPlugins(registry *plugin.Registry) {
	h.plugins = registry
}

// SetWatchManager sets the watch manager for vision.watch commands.
func (h *Handler) SetWatchManager(mgr *triocore.Manager) {
	h.watchMgr = mgr
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
	case "vision.watch":
		return h.handleVisionWatch(ctx, req)
	case "vision.watch.stop":
		return h.handleVisionWatchStop(ctx, req)
	case "vision.status":
		return h.handleVisionStatus(ctx, req)
	case "device.list":
		return h.handleDeviceList(ctx, req)
	case "device.control":
		return h.handleDeviceControl(ctx, req)
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

// =============================================================================
// Vision watch handlers (continuous monitoring)
// =============================================================================

// handleVisionWatch starts a new watch via the watch manager.
func (h *Handler) handleVisionWatch(ctx context.Context, req InvokeRequest) InvokeResult {
	if h.watchMgr == nil {
		return h.errorResult(req, "UNAVAILABLE", "watch manager not available")
	}

	var params VisionWatchParams
	if err := DecodePayloadJSON(string(req.Params), &params); err != nil {
		return h.errorResult(req, "INVALID_PARAMS", fmt.Sprintf("failed to parse params: %v", err))
	}

	if params.CameraID == "" {
		return h.errorResult(req, "INVALID_PARAMS", "cameraId is required")
	}
	if params.Source == "" {
		return h.errorResult(req, "INVALID_PARAMS", "source is required")
	}

	// Convert to config.CameraConfig
	cam := config.CameraConfig{
		ID:     params.CameraID,
		Name:   params.CameraID,
		Source: params.Source,
		FPS:    params.FPS,
	}
	if cam.FPS <= 0 {
		cam.FPS = 1
	}
	for _, cond := range params.Conditions {
		cam.Conditions = append(cam.Conditions, config.ConditionConfig{
			ID:       cond.ID,
			Question: cond.Question,
			Actions:  cond.Actions,
		})
	}

	if err := h.watchMgr.StartDynamicWatch(cam); err != nil {
		return h.errorResult(req, "WATCH_FAILED", fmt.Sprintf("failed to start watch: %v", err))
	}

	result := VisionWatchResult{
		CameraID: params.CameraID,
		Status:   "started",
	}
	return InvokeResult{
		ID:     req.ID,
		NodeID: h.nodeID,
		OK:     true,
		Result: marshalResult(result),
	}
}

// handleVisionWatchStop stops an active watch.
func (h *Handler) handleVisionWatchStop(ctx context.Context, req InvokeRequest) InvokeResult {
	if h.watchMgr == nil {
		return h.errorResult(req, "UNAVAILABLE", "watch manager not available")
	}

	var params VisionWatchStopParams
	if err := DecodePayloadJSON(string(req.Params), &params); err != nil {
		return h.errorResult(req, "INVALID_PARAMS", fmt.Sprintf("failed to parse params: %v", err))
	}

	if params.CameraID == "" {
		return h.errorResult(req, "INVALID_PARAMS", "cameraId is required")
	}

	if err := h.watchMgr.StopDynamicWatch(ctx, params.CameraID); err != nil {
		return h.errorResult(req, "WATCH_STOP_FAILED", err.Error())
	}

	result := VisionWatchStopResult{
		CameraID: params.CameraID,
		Status:   "stopped",
	}
	return InvokeResult{
		ID:     req.ID,
		NodeID: h.nodeID,
		OK:     true,
		Result: marshalResult(result),
	}
}

// handleVisionStatus returns all active watches.
func (h *Handler) handleVisionStatus(ctx context.Context, req InvokeRequest) InvokeResult {
	if h.watchMgr == nil {
		return h.errorResult(req, "UNAVAILABLE", "watch manager not available")
	}

	entries := h.watchMgr.Status()
	var watches []VisionStatusEntry
	for _, e := range entries {
		watches = append(watches, VisionStatusEntry{
			CameraID: e.CameraID,
			WatchID:  e.WatchID,
			Source:   e.Source,
			State:    e.State,
		})
	}
	if watches == nil {
		watches = []VisionStatusEntry{} // ensure JSON [] not null
	}

	result := VisionStatusResult{Watches: watches}
	return InvokeResult{
		ID:     req.ID,
		NodeID: h.nodeID,
		OK:     true,
		Result: marshalResult(result),
	}
}

// errorResult is a helper to build error InvokeResults.
func (h *Handler) errorResult(req InvokeRequest, code, message string) InvokeResult {
	return InvokeResult{
		ID:     req.ID,
		NodeID: h.nodeID,
		OK:     false,
		Error:  &ErrorPayload{Code: code, Message: message},
	}
}

// =============================================================================
// Device control handlers (Hands)
// =============================================================================

// handleDeviceList returns all controllable devices across all plugins.
//
// No params.
// Result: { devices: [{id, name, plugin, type, state, actions}] }
func (h *Handler) handleDeviceList(ctx context.Context, req InvokeRequest) InvokeResult {
	devices, err := h.plugins.ListAllDevices(ctx)
	if err != nil {
		return InvokeResult{
			ID:     req.ID,
			NodeID: h.nodeID,
			OK:     false,
			Error:  &ErrorPayload{Code: "DEVICE_LIST_FAILED", Message: fmt.Sprintf("failed to list devices: %v", err)},
		}
	}

	type deviceListResult struct {
		Devices []plugin.Device `json:"devices"`
	}
	result := deviceListResult{Devices: devices}
	resultJSON := marshalResult(result)

	return InvokeResult{
		ID:     req.ID,
		NodeID: h.nodeID,
		OK:     true,
		Result: resultJSON,
	}
}

// handleDeviceControl executes an action on a device.
//
// Params: DeviceControlParams (deviceId, action, params)
// Result: DeviceControlResult (success, message, newState)
func (h *Handler) handleDeviceControl(ctx context.Context, req InvokeRequest) InvokeResult {
	var params DeviceControlParams
	if err := DecodePayloadJSON(string(req.Params), &params); err != nil {
		return InvokeResult{
			ID:     req.ID,
			NodeID: h.nodeID,
			OK:     false,
			Error:  &ErrorPayload{Code: "INVALID_PARAMS", Message: fmt.Sprintf("failed to parse params: %v", err)},
		}
	}

	if params.DeviceID == "" {
		return InvokeResult{
			ID:     req.ID,
			NodeID: h.nodeID,
			OK:     false,
			Error:  &ErrorPayload{Code: "INVALID_PARAMS", Message: "deviceId is required"},
		}
	}
	if params.Action == "" {
		return InvokeResult{
			ID:     req.ID,
			NodeID: h.nodeID,
			OK:     false,
			Error:  &ErrorPayload{Code: "INVALID_PARAMS", Message: "action is required"},
		}
	}

	result, err := h.plugins.Execute(ctx, params.DeviceID, params.Action, params.Params)
	if err != nil {
		return InvokeResult{
			ID:     req.ID,
			NodeID: h.nodeID,
			OK:     false,
			Error:  &ErrorPayload{Code: "DEVICE_CONTROL_FAILED", Message: fmt.Sprintf("failed to control device: %v", err)},
		}
	}

	resultJSON := marshalResult(result)

	return InvokeResult{
		ID:     req.ID,
		NodeID: h.nodeID,
		OK:     true,
		Result: resultJSON,
	}
}
