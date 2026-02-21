package plugin

import (
	"encoding/json"
	"testing"
)

func TestDevice_JSON(t *testing.T) {
	dev := Device{
		ID:      "light.living_room",
		Name:    "Living Room Light",
		Plugin:  "ha",
		Type:    "light",
		State:   "on",
		Actions: []string{"turn_on", "turn_off", "toggle"},
		Metadata: map[string]any{
			"brightness": 200,
			"color_temp": 350,
		},
	}

	data, err := json.Marshal(dev)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded Device
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if decoded.ID != dev.ID {
		t.Errorf("ID = %s, want %s", decoded.ID, dev.ID)
	}
	if decoded.Name != dev.Name {
		t.Errorf("Name = %s, want %s", decoded.Name, dev.Name)
	}
	if decoded.Plugin != dev.Plugin {
		t.Errorf("Plugin = %s, want %s", decoded.Plugin, dev.Plugin)
	}
	if decoded.Type != dev.Type {
		t.Errorf("Type = %s, want %s", decoded.Type, dev.Type)
	}
	if decoded.State != dev.State {
		t.Errorf("State = %s, want %s", decoded.State, dev.State)
	}
	if len(decoded.Actions) != len(dev.Actions) {
		t.Errorf("Actions count = %d, want %d", len(decoded.Actions), len(dev.Actions))
	}
	for i, a := range decoded.Actions {
		if a != dev.Actions[i] {
			t.Errorf("Actions[%d] = %s, want %s", i, a, dev.Actions[i])
		}
	}
	if decoded.Metadata == nil {
		t.Fatal("Metadata is nil")
	}
	if b, ok := decoded.Metadata["brightness"].(float64); !ok || int(b) != 200 {
		t.Errorf("Metadata[brightness] = %v, want 200", decoded.Metadata["brightness"])
	}
}

func TestDevice_JSON_OmitEmpty(t *testing.T) {
	dev := Device{
		ID:     "switch.porch",
		Name:   "Porch Switch",
		Plugin: "ha",
		Type:   "switch",
		// State, Actions, Metadata are empty => should be omitted
	}

	data, err := json.Marshal(dev)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	raw := string(data)

	// These fields have omitempty, so they should not appear when empty
	if contains(raw, `"state"`) {
		t.Error("JSON contains 'state' field, but it should be omitted when empty")
	}
	if contains(raw, `"actions"`) {
		t.Error("JSON contains 'actions' field, but it should be omitted when empty")
	}
	if contains(raw, `"metadata"`) {
		t.Error("JSON contains 'metadata' field, but it should be omitted when empty")
	}

	// These fields should always appear
	if !contains(raw, `"id"`) {
		t.Error("JSON missing required 'id' field")
	}
	if !contains(raw, `"name"`) {
		t.Error("JSON missing required 'name' field")
	}
	if !contains(raw, `"plugin"`) {
		t.Error("JSON missing required 'plugin' field")
	}
	if !contains(raw, `"type"`) {
		t.Error("JSON missing required 'type' field")
	}
}

func TestResult_JSON(t *testing.T) {
	result := Result{
		Success:  true,
		Message:  "light.turn_on executed on light.living_room",
		NewState: "on",
		Data: map[string]any{
			"brightness": 255,
		},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded Result
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if decoded.Success != result.Success {
		t.Errorf("Success = %v, want %v", decoded.Success, result.Success)
	}
	if decoded.Message != result.Message {
		t.Errorf("Message = %s, want %s", decoded.Message, result.Message)
	}
	if decoded.NewState != result.NewState {
		t.Errorf("NewState = %s, want %s", decoded.NewState, result.NewState)
	}
	if decoded.Data == nil {
		t.Fatal("Data is nil")
	}
	if b, ok := decoded.Data["brightness"].(float64); !ok || int(b) != 255 {
		t.Errorf("Data[brightness] = %v, want 255", decoded.Data["brightness"])
	}
}

func TestResult_JSON_OmitEmpty(t *testing.T) {
	result := Result{
		Success: false,
		// Message, NewState, Data are empty => should be omitted
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	raw := string(data)

	// success is not omitempty, so it must appear even when false
	if !contains(raw, `"success"`) {
		t.Error("JSON missing required 'success' field")
	}

	if contains(raw, `"message"`) {
		t.Error("JSON contains 'message' field, but it should be omitted when empty")
	}
	if contains(raw, `"new_state"`) {
		t.Error("JSON contains 'new_state' field, but it should be omitted when empty")
	}
	if contains(raw, `"data"`) {
		t.Error("JSON contains 'data' field, but it should be omitted when empty")
	}
}

func TestResult_JSON_SuccessFalse(t *testing.T) {
	result := Result{
		Success: false,
		Message: "permission denied",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded Result
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if decoded.Success != false {
		t.Errorf("Success = %v, want false", decoded.Success)
	}
	if decoded.Message != "permission denied" {
		t.Errorf("Message = %s, want 'permission denied'", decoded.Message)
	}
}

func TestDevice_JSON_RoundTrip(t *testing.T) {
	original := `{"id":"lock.front","name":"Front Door Lock","plugin":"ha","type":"lock","state":"locked","actions":["lock","unlock"]}`

	var dev Device
	if err := json.Unmarshal([]byte(original), &dev); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if dev.ID != "lock.front" {
		t.Errorf("ID = %s, want lock.front", dev.ID)
	}
	if dev.Type != "lock" {
		t.Errorf("Type = %s, want lock", dev.Type)
	}
	if dev.State != "locked" {
		t.Errorf("State = %s, want locked", dev.State)
	}
	if len(dev.Actions) != 2 {
		t.Errorf("Actions count = %d, want 2", len(dev.Actions))
	}

	// Re-marshal and verify
	data, err := json.Marshal(dev)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	if string(data) != original {
		t.Errorf("round-trip JSON mismatch:\n  got:  %s\n  want: %s", string(data), original)
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
