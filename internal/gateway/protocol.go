// Package gateway implements the OpenClaw Gateway WebSocket protocol (v3).
//
// Protocol overview:
//   - Transport: WebSocket on port 18789
//   - Frame types: req (request), res (response), event
//   - All frames are JSON text messages
//   - payloadJSON fields are DOUBLE-ENCODED: a JSON string containing JSON
//
// Connection flow:
//   1. WS connect
//   2. Receive: event "connect.challenge" with nonce
//   3. Send: req "connect" with caps, commands, auth token
//   4. Receive: res with "hello-ok"
//   5. Ready for invocations
//
// Reference: ClawGo (github.com/openclaw/clawgo), OpenClaw Gateway source
package gateway

import "encoding/json"

// =============================================================================
// Wire frame types — what goes over the WebSocket
// =============================================================================

// ReqFrame is a request from client to gateway (or gateway to client).
//
//	{"type":"req","id":"1","method":"connect","params":{...}}
type ReqFrame struct {
	Type   string          `json:"type"`   // always "req"
	ID     string          `json:"id"`     // unique request ID
	Method string          `json:"method"` // "connect", "node.pair.request", "node.invoke.result"
	Params json.RawMessage `json:"params"` // method-specific parameters
}

// ResFrame is a response to a request.
//
//	{"type":"res","id":"1","ok":true,"payload":{...}}
//	{"type":"res","id":"1","ok":false,"error":{"code":"...","message":"..."}}
type ResFrame struct {
	Type    string          `json:"type"`              // always "res"
	ID      string          `json:"id"`                // matches request ID
	OK      bool            `json:"ok"`                // success or failure
	Payload json.RawMessage `json:"payload,omitempty"` // success payload
	Error   *ErrorPayload   `json:"error,omitempty"`   // error details
}

// EventFrame is a unidirectional event (either direction).
//
// IMPORTANT: payloadJSON is a STRING containing JSON, not a nested object.
//
//	{"type":"event","event":"chat","payloadJSON":"{\"text\":\"hello\"}"}
type EventFrame struct {
	Type        string `json:"type"`                  // always "event"
	Event       string `json:"event"`                 // event name
	PayloadJSON string `json:"payloadJSON,omitempty"` // double-encoded JSON string
}

// ErrorPayload is error detail in a failed response.
type ErrorPayload struct {
	Code    string `json:"code"`    // e.g. "UNAVAILABLE", "NOT_PAIRED"
	Message string `json:"message"` // human-readable error
}

// =============================================================================
// Connect params — sent during initial handshake
// =============================================================================

// ConnectParams is the params for the "connect" request.
type ConnectParams struct {
	MinProtocol int               `json:"minProtocol"` // 3
	MaxProtocol int               `json:"maxProtocol"` // 3
	Client      ClientInfo        `json:"client"`
	Role        string            `json:"role"` // "node"
	Caps        []string          `json:"caps"`
	Commands    []string          `json:"commands"`
	Permissions map[string]bool   `json:"permissions"`
	Auth        *AuthInfo         `json:"auth,omitempty"` // nil for first-time pairing
}

// ClientInfo identifies this node to the gateway.
type ClientInfo struct {
	ID              string `json:"id"`              // "trioclaw"
	Version         string `json:"version"`         // "0.1.0"
	Platform        string `json:"platform"`        // "darwin", "linux", "windows"
	DeviceFamily    string `json:"deviceFamily"`    // "trioclaw"
	ModelIdentifier string `json:"modelIdentifier"` // hostname or custom
	Mode            string `json:"mode"`            // "node"
}

// AuthInfo carries the device token for authentication.
type AuthInfo struct {
	Token string `json:"token"`
}

// =============================================================================
// Pairing params
// =============================================================================

// PairRequestParams is the params for "node.pair.request".
type PairRequestParams struct {
	NodeID      string          `json:"nodeId"`
	DisplayName string          `json:"displayName"`
	Platform    string          `json:"platform"`
	Version     string          `json:"version"`
	DeviceFamily string         `json:"deviceFamily"`
	Caps        []string        `json:"caps"`
	Commands    []string        `json:"commands"`
	Silent      bool            `json:"silent"` // false for interactive pairing
}

// =============================================================================
// Invoke — gateway asking the node to do something
// =============================================================================

// InvokeRequest is received when the gateway wants to node to execute a command.
// Comes as event "node.invoke.request".
type InvokeRequest struct {
	ID      string          `json:"id"`      // invoke ID, must be echoed in response
	NodeID  string          `json:"nodeId"`
	Command string          `json:"command"` // "camera.snap", "camera.list", "vision.analyze"
	Params  json.RawMessage `json:"params"`  // command-specific params (double-encoded JSON)
}

// InvokeResult is sent back to the gateway after executing an invoke.
// Sent as req with method "node.invoke.result".
type InvokeResult struct {
	ID     string          `json:"id"`               // matches InvokeRequest.ID
	NodeID string          `json:"nodeId"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"payloadJSON,omitempty"` // success result (double-encoded)
	Error  *ErrorPayload   `json:"error,omitempty"`       // failure detail
}

// =============================================================================
// Camera invoke params (standard OpenClaw camera commands)
// =============================================================================

// CameraSnapParams is the params for "camera.snap" invoke.
type CameraSnapParams struct {
	Facing   string  `json:"facing,omitempty"`   // "front" or "back" (ignored for RTSP)
	MaxWidth int     `json:"maxWidth,omitempty"` // resize to max width (0 = no resize)
	Quality  float64 `json:"quality,omitempty"`  // JPEG quality 0.0-1.0 (default 0.85)
	DeviceID string  `json:"deviceId,omitempty"` // specific device to use
}

// CameraSnapResult is the result for "camera.snap".
type CameraSnapResult struct {
	Format string `json:"format"` // "jpeg"
	Base64 string `json:"base64"` // base64-encoded JPEG
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// CameraListResult is the result for "camera.list".
type CameraListResult struct {
	Devices []CameraDevice `json:"devices"`
}

// CameraDevice describes an available camera.
type CameraDevice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Position string `json:"position,omitempty"` // "front", "back", "unknown"
}

// =============================================================================
// Vision invoke params (TrioClaw-specific commands)
// =============================================================================

// VisionAnalyzeParams is the params for "vision.analyze" invoke.
type VisionAnalyzeParams struct {
	Question string `json:"question"`            // "what do you see?", "is there a person?"
	DeviceID string `json:"deviceId,omitempty"`   // which camera to use
}

// VisionAnalyzeResult is the result for "vision.analyze".
type VisionAnalyzeResult struct {
	Answer     string            `json:"answer"`     // VLM response text
	Confidence float64           `json:"confidence"` // 0.0-1.0 (from Trio API)
	Frame      *CameraSnapResult `json:"frame"`      // the frame that was analyzed
}

// =============================================================================
// Device control invoke params (TrioClaw "Hands" commands)
// =============================================================================

// DeviceControlParams is the params for "device.control" invoke.
type DeviceControlParams struct {
	DeviceID string         `json:"deviceId"` // "ha:light.living_room" or "exec:my-script/desk-lamp"
	Action   string         `json:"action"`   // "turn_on", "turn_off", "lock", "set_temperature", etc.
	Params   map[string]any `json:"params"`   // action-specific params (e.g. {"brightness": 200})
}

// =============================================================================
// Utility: encode/decode frames
// =============================================================================

// EncodePayloadJSON double-encodes a value into a payloadJSON string.
// This is required by the OpenClaw protocol: payloadJSON is a JSON string
// containing JSON, not a nested object.
func EncodePayloadJSON(v interface{}) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	// Double-encode: marshal the JSON string as a string
	return string(data), nil
}

// DecodePayloadJSON decodes a double-encoded payloadJSON string into dest.
func DecodePayloadJSON(payloadJSON string, dest interface{}) error {
	return json.Unmarshal([]byte(payloadJSON), dest)
}
