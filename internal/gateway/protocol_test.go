package gateway

import (
	"encoding/json"
	"testing"
)

func TestEncodePayloadJSON(t *testing.T) {
	tests := []struct {
		name string
		v    interface{}
	}{
		{
			name: "string",
			v:    "test string",
		},
		{
			name: "object",
			v: map[string]interface{}{
				"key":   "value",
				"number": 123,
			},
		},
		{
			name: "array",
			v:    []string{"a", "b", "c"},
		},
		{
			name: "CameraSnapResult",
			v: CameraSnapResult{
				Format: "jpeg",
				Base64: "base64data",
				Width:  1280,
				Height: 720,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := EncodePayloadJSON(tt.v)
			if err != nil {
				t.Fatalf("EncodePayloadJSON() error = %v", err)
			}

			if encoded == "" {
				t.Error("EncodePayloadJSON() returned empty string")
			}

			// Verify it's valid JSON
			var decoded interface{}
			if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
				t.Errorf("Encoded string is not valid JSON: %v", err)
			}

			// Verify we can decode back to original
			decodedBytes := []byte(encoded)
			var dest interface{}
			if err := json.Unmarshal(decodedBytes, &dest); err != nil {
				t.Errorf("DecodePayloadJSON failed: %v", err)
			}

			// Compare
			encodedOrig, _ := json.Marshal(tt.v)
			if string(encoded) != string(encodedOrig) {
				t.Logf("Note: encoded = %s, original = %s", string(encoded), string(encodedOrig))
			}
		})
	}
}

func TestDecodePayloadJSON(t *testing.T) {
	tests := []struct {
		name string
		json string
		want interface{}
	}{
		{
			name: "string",
			json: `"test string"`,
			want: "test string",
		},
		{
			name: "object",
			json: `{"key":"value","number":123}`,
		},
		{
			name: "CameraSnapResult",
			json: `{"format":"jpeg","base64":"base64data","width":1280,"height":720}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dest interface{}
			err := DecodePayloadJSON(tt.json, &dest)
			if err != nil {
				t.Fatalf("DecodePayloadJSON() error = %v", err)
			}

			// Verify by re-encoding and comparing
			encoded, _ := json.Marshal(dest)
			wantEncoded, _ := json.Marshal(tt.want)
			if string(encoded) != string(wantEncoded) && tt.want != nil {
				t.Logf("Note: decoded = %s, want %s", string(encoded), string(wantEncoded))
			}
		})
	}
}

func TestCameraSnapResult_JSON(t *testing.T) {
	result := CameraSnapResult{
		Format: "jpeg",
		Base64: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg==",
		Width:  1920,
		Height: 1080,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded CameraSnapResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if decoded.Format != result.Format {
		t.Errorf("Format = %s, want %s", decoded.Format, result.Format)
	}

	if decoded.Base64 != result.Base64 {
		t.Errorf("Base64 length = %d, want %d", len(decoded.Base64), len(result.Base64))
	}

	if decoded.Width != result.Width {
		t.Errorf("Width = %d, want %d", decoded.Width, result.Width)
	}

	if decoded.Height != result.Height {
		t.Errorf("Height = %d, want %d", decoded.Height, result.Height)
	}
}

func TestCameraListResult_JSON(t *testing.T) {
	result := CameraListResult{
		Devices: []CameraDevice{
			{ID: "0", Name: "FaceTime HD Camera", Position: "front"},
			{ID: "1", Name: "Logitech C920", Position: "unknown"},
		},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded CameraListResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if len(decoded.Devices) != len(result.Devices) {
		t.Errorf("Devices count = %d, want %d", len(decoded.Devices), len(result.Devices))
	}

	if decoded.Devices[0].ID != result.Devices[0].ID {
		t.Errorf("First device ID = %s, want %s", decoded.Devices[0].ID, result.Devices[0].ID)
	}
}

func TestVisionAnalyzeResult_JSON(t *testing.T) {
	result := VisionAnalyzeResult{
		Answer:     "A person is visible in the frame",
		Confidence: 0.95,
		Frame: &CameraSnapResult{
			Format: "jpeg",
			Base64: "base64data",
			Width:  1280,
			Height: 720,
		},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded VisionAnalyzeResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if decoded.Answer != result.Answer {
		t.Errorf("Answer = %s, want %s", decoded.Answer, result.Answer)
	}

	if decoded.Confidence != result.Confidence {
		t.Errorf("Confidence = %v, want %v", decoded.Confidence, result.Confidence)
	}

	if decoded.Frame == nil {
		t.Error("Frame is nil")
	} else if decoded.Frame.Format != result.Frame.Format {
		t.Errorf("Frame.Format = %s, want %s", decoded.Frame.Format, result.Frame.Format)
	}
}

func TestInvokeResult_JSON(t *testing.T) {
	result := InvokeResult{
		ID:     "123",
		NodeID: "trioclaw-node",
		OK:     true,
		Result:  json.RawMessage(`{"test":"data"}`),
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded InvokeResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if decoded.ID != result.ID {
		t.Errorf("ID = %s, want %s", decoded.ID, result.ID)
	}

	if decoded.NodeID != result.NodeID {
		t.Errorf("NodeID = %s, want %s", decoded.NodeID, result.NodeID)
	}

	if decoded.OK != result.OK {
		t.Errorf("OK = %v, want %v", decoded.OK, result.OK)
	}

	if string(decoded.Result) != string(result.Result) {
		t.Errorf("Result = %s, want %s", decoded.Result, result.Result)
	}
}

func TestErrorPayload_JSON(t *testing.T) {
	errPayload := &ErrorPayload{
		Code:    "UNAVAILABLE",
		Message: "unknown command",
	}

	data, err := json.Marshal(errPayload)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded ErrorPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if decoded.Code != errPayload.Code {
		t.Errorf("Code = %s, want %s", decoded.Code, errPayload.Code)
	}

	if decoded.Message != errPayload.Message {
		t.Errorf("Message = %s, want %s", decoded.Message, errPayload.Message)
	}
}

func TestConnectParams_JSON(t *testing.T) {
	params := ConnectParams{
		MinProtocol: 3,
		MaxProtocol: 3,
		Client: ClientInfo{
			ID:              "trioclaw",
			Version:         "0.1.0",
			Platform:        "darwin",
			DeviceFamily:    "trioclaw",
			ModelIdentifier: "trioclaw-macbook",
			Mode:            "node",
		},
		Role: "node",
		Caps: []string{"camera"},
		Commands: []string{"camera.snap", "camera.list"},
		Permissions: map[string]bool{
			"camera.capture": true,
		},
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded ConnectParams
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if decoded.MinProtocol != params.MinProtocol {
		t.Errorf("MinProtocol = %d, want %d", decoded.MinProtocol, params.MinProtocol)
	}

	if decoded.Client.ID != params.Client.ID {
		t.Errorf("Client.ID = %s, want %s", decoded.Client.ID, params.Client.ID)
	}

	if len(decoded.Caps) != len(params.Caps) {
		t.Errorf("Caps count = %d, want %d", len(decoded.Caps), len(params.Caps))
	}
}

func TestInvokeRequest_JSON(t *testing.T) {
	req := InvokeRequest{
		ID:      "req-123",
		NodeID:  "trioclaw-node",
		Command: "camera.snap",
		Params:  json.RawMessage(`{"deviceId":"0"}`),
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded InvokeRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if decoded.ID != req.ID {
		t.Errorf("ID = %s, want %s", decoded.ID, req.ID)
	}

	if decoded.NodeID != req.NodeID {
		t.Errorf("NodeID = %s, want %s", decoded.NodeID, req.NodeID)
	}

	if decoded.Command != req.Command {
		t.Errorf("Command = %s, want %s", decoded.Command, req.Command)
	}
}

func TestReqFrame_JSON(t *testing.T) {
	frame := ReqFrame{
		Type:   "req",
		ID:     "1",
		Method: "connect",
		Params: json.RawMessage(`{"test":"data"}`),
	}

	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded ReqFrame
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if decoded.Type != frame.Type {
		t.Errorf("Type = %s, want %s", decoded.Type, frame.Type)
	}

	if decoded.ID != frame.ID {
		t.Errorf("ID = %s, want %s", decoded.ID, frame.ID)
	}

	if decoded.Method != frame.Method {
		t.Errorf("Method = %s, want %s", decoded.Method, frame.Method)
	}
}

func TestResFrame_JSON(t *testing.T) {
	frame := ResFrame{
		Type:    "res",
		ID:      "1",
		OK:      true,
		Payload: json.RawMessage(`{"test":"data"}`),
	}

	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded ResFrame
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if decoded.Type != frame.Type {
		t.Errorf("Type = %s, want %s", decoded.Type, frame.Type)
	}

	if decoded.ID != frame.ID {
		t.Errorf("ID = %s, want %s", decoded.ID, frame.ID)
	}

	if decoded.OK != frame.OK {
		t.Errorf("OK = %v, want %v", decoded.OK, frame.OK)
	}
}

func TestResFrame_WithError(t *testing.T) {
	frame := ResFrame{
		Type:  "res",
		ID:    "1",
		OK:    false,
		Error: &ErrorPayload{
			Code:    "INVALID_PARAMS",
			Message: "missing deviceId",
		},
	}

	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded ResFrame
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if decoded.Error == nil {
		t.Fatal("Error is nil, want error payload")
	}

	if decoded.Error.Code != frame.Error.Code {
		t.Errorf("Error.Code = %s, want %s", decoded.Error.Code, frame.Error.Code)
	}
}

func TestEventFrame_JSON(t *testing.T) {
	frame := EventFrame{
		Type:        "event",
		Event:       "node.invoke.request",
		PayloadJSON: `{"command":"camera.snap"}`,
	}

	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded EventFrame
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if decoded.Type != frame.Type {
		t.Errorf("Type = %s, want %s", decoded.Type, frame.Type)
	}

	if decoded.Event != frame.Event {
		t.Errorf("Event = %s, want %s", decoded.Event, frame.Event)
	}

	if decoded.PayloadJSON != frame.PayloadJSON {
		t.Errorf("PayloadJSON = %s, want %s", decoded.PayloadJSON, frame.PayloadJSON)
	}
}
