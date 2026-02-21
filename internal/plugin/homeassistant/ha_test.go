package homeassistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestServer creates an httptest server that simulates Home Assistant's REST API.
// The handler map routes path -> handler function.
func newTestServer(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header is present
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"401: Unauthorized"}`))
			return
		}

		if handler, ok := handlers[r.URL.Path]; ok {
			handler(w, r)
			return
		}

		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
}

func TestHAPlugin_Name(t *testing.T) {
	p := New("http://localhost:8123", "token")
	if p.Name() != "ha" {
		t.Errorf("Name() = %s, want ha", p.Name())
	}
}

func TestHAPlugin_ListDevices(t *testing.T) {
	states := []haState{
		{
			EntityID: "light.living_room",
			State:    "on",
			Attributes: map[string]any{
				"friendly_name": "Living Room Light",
				"brightness":    200.0,
			},
		},
		{
			EntityID: "switch.porch",
			State:    "off",
			Attributes: map[string]any{
				"friendly_name": "Porch Switch",
			},
		},
		{
			EntityID: "sensor.temperature",
			State:    "72.5",
			Attributes: map[string]any{
				"friendly_name":     "Temperature Sensor",
				"unit_of_measurement": "F",
			},
		},
		{
			EntityID: "lock.front_door",
			State:    "locked",
			Attributes: map[string]any{
				"friendly_name": "Front Door",
			},
		},
		{
			EntityID: "climate.thermostat",
			State:    "heat",
			Attributes: map[string]any{
				"friendly_name":       "Thermostat",
				"temperature":         72.0,
				"current_temperature": 68.0,
				"hvac_mode":           "heat",
			},
		},
	}

	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api/states": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				t.Errorf("ListDevices: method = %s, want GET", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(states)
		},
	})
	defer srv.Close()

	p := New(srv.URL, "test-token")
	ctx := context.Background()

	devices, err := p.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices() error = %v", err)
	}

	// sensor.temperature should be filtered out (not controllable)
	// So we expect: light, switch, lock, climate = 4 devices
	if len(devices) != 4 {
		t.Fatalf("ListDevices() count = %d, want 4", len(devices))
	}

	// Verify each device
	byID := make(map[string]struct {
		Name    string
		Type    string
		State   string
		Actions []string
	})
	for _, d := range devices {
		byID[d.ID] = struct {
			Name    string
			Type    string
			State   string
			Actions []string
		}{d.Name, d.Type, d.State, d.Actions}
	}

	// Check light
	light, ok := byID["light.living_room"]
	if !ok {
		t.Fatal("missing device light.living_room")
	}
	if light.Name != "Living Room Light" {
		t.Errorf("light Name = %s, want 'Living Room Light'", light.Name)
	}
	if light.Type != "light" {
		t.Errorf("light Type = %s, want 'light'", light.Type)
	}
	if light.State != "on" {
		t.Errorf("light State = %s, want 'on'", light.State)
	}
	if len(light.Actions) != 3 {
		t.Errorf("light Actions count = %d, want 3", len(light.Actions))
	}

	// Check lock
	lock, ok := byID["lock.front_door"]
	if !ok {
		t.Fatal("missing device lock.front_door")
	}
	if lock.Type != "lock" {
		t.Errorf("lock Type = %s, want 'lock'", lock.Type)
	}
	if len(lock.Actions) != 2 {
		t.Errorf("lock Actions count = %d, want 2 (lock, unlock)", len(lock.Actions))
	}

	// Check climate has metadata
	for _, d := range devices {
		if d.ID == "climate.thermostat" {
			if d.Metadata == nil {
				t.Error("climate Metadata is nil, want temperature/current_temperature/hvac_mode")
			} else {
				if _, ok := d.Metadata["temperature"]; !ok {
					t.Error("climate Metadata missing 'temperature'")
				}
				if _, ok := d.Metadata["current_temperature"]; !ok {
					t.Error("climate Metadata missing 'current_temperature'")
				}
			}
		}
	}
}

func TestHAPlugin_ListDevices_FiltersDomains(t *testing.T) {
	// Every domain that should be filtered OUT
	nonControllable := []haState{
		{EntityID: "sensor.temp", State: "72", Attributes: map[string]any{"friendly_name": "Temp"}},
		{EntityID: "binary_sensor.motion", State: "off", Attributes: map[string]any{"friendly_name": "Motion"}},
		{EntityID: "device_tracker.phone", State: "home", Attributes: map[string]any{"friendly_name": "Phone"}},
		{EntityID: "weather.home", State: "sunny", Attributes: map[string]any{"friendly_name": "Weather"}},
		{EntityID: "zone.home", State: "0", Attributes: map[string]any{"friendly_name": "Home"}},
		{EntityID: "person.john", State: "home", Attributes: map[string]any{"friendly_name": "John"}},
	}

	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api/states": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(nonControllable)
		},
	})
	defer srv.Close()

	p := New(srv.URL, "test-token")
	ctx := context.Background()

	devices, err := p.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices() error = %v", err)
	}

	if len(devices) != 0 {
		t.Errorf("ListDevices() count = %d, want 0 (all should be filtered)", len(devices))
		for _, d := range devices {
			t.Logf("  unexpected device: %s (%s)", d.ID, d.Type)
		}
	}
}

func TestHAPlugin_ListDevices_AllControllableDomains(t *testing.T) {
	controllable := []haState{
		{EntityID: "light.a", State: "on", Attributes: map[string]any{"friendly_name": "A"}},
		{EntityID: "switch.b", State: "off", Attributes: map[string]any{"friendly_name": "B"}},
		{EntityID: "lock.c", State: "locked", Attributes: map[string]any{"friendly_name": "C"}},
		{EntityID: "climate.d", State: "heat", Attributes: map[string]any{"friendly_name": "D"}},
		{EntityID: "cover.e", State: "open", Attributes: map[string]any{"friendly_name": "E"}},
		{EntityID: "fan.f", State: "on", Attributes: map[string]any{"friendly_name": "F"}},
		{EntityID: "media_player.g", State: "playing", Attributes: map[string]any{"friendly_name": "G"}},
		{EntityID: "scene.h", State: "scening", Attributes: map[string]any{"friendly_name": "H"}},
		{EntityID: "script.i", State: "off", Attributes: map[string]any{"friendly_name": "I"}},
		{EntityID: "automation.j", State: "on", Attributes: map[string]any{"friendly_name": "J"}},
		{EntityID: "vacuum.k", State: "docked", Attributes: map[string]any{"friendly_name": "K"}},
		{EntityID: "humidifier.l", State: "on", Attributes: map[string]any{"friendly_name": "L"}},
		{EntityID: "water_heater.m", State: "eco", Attributes: map[string]any{"friendly_name": "M"}},
		{EntityID: "input_boolean.n", State: "on", Attributes: map[string]any{"friendly_name": "N"}},
	}

	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api/states": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(controllable)
		},
	})
	defer srv.Close()

	p := New(srv.URL, "test-token")
	ctx := context.Background()

	devices, err := p.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices() error = %v", err)
	}

	if len(devices) != 14 {
		t.Errorf("ListDevices() count = %d, want 14", len(devices))
		for _, d := range devices {
			t.Logf("  device: %s (%s)", d.ID, d.Type)
		}
	}
}

func TestHAPlugin_ListDevices_FriendlyNameFallback(t *testing.T) {
	states := []haState{
		{
			EntityID:   "light.no_name",
			State:      "on",
			Attributes: map[string]any{}, // no friendly_name
		},
	}

	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api/states": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(states)
		},
	})
	defer srv.Close()

	p := New(srv.URL, "test-token")
	ctx := context.Background()

	devices, err := p.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices() error = %v", err)
	}

	if len(devices) != 1 {
		t.Fatalf("ListDevices() count = %d, want 1", len(devices))
	}

	// Should fall back to entity_id as the name
	if devices[0].Name != "light.no_name" {
		t.Errorf("Name = %s, want 'light.no_name' (entity_id fallback)", devices[0].Name)
	}
}

func TestHAPlugin_Execute(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api/services/light/turn_on": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				t.Errorf("Execute: method = %s, want POST", r.Method)
			}

			ct := r.Header.Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("Content-Type = %s, want application/json", ct)
			}

			// Parse the request body
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("failed to decode request body: %v", err)
			}

			// Verify entity_id is in payload
			if eid, ok := payload["entity_id"].(string); !ok || eid != "light.living_room" {
				t.Errorf("entity_id = %v, want 'light.living_room'", payload["entity_id"])
			}

			// Verify brightness param is passed through
			if b, ok := payload["brightness"].(float64); !ok || int(b) != 200 {
				t.Errorf("brightness = %v, want 200", payload["brightness"])
			}

			// Return changed states
			changedStates := []haState{
				{
					EntityID: "light.living_room",
					State:    "on",
					Attributes: map[string]any{
						"brightness": 200,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(changedStates)
		},
	})
	defer srv.Close()

	p := New(srv.URL, "test-token")
	ctx := context.Background()

	result, err := p.Execute(ctx, "light.living_room", "turn_on", map[string]any{"brightness": 200})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !result.Success {
		t.Error("Execute() Success = false, want true")
	}
	if result.NewState != "on" {
		t.Errorf("Execute() NewState = %s, want on", result.NewState)
	}
	if result.Message != "light.turn_on executed on light.living_room" {
		t.Errorf("Execute() Message = %q, want 'light.turn_on executed on light.living_room'", result.Message)
	}
}

func TestHAPlugin_Execute_NoParams(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api/services/switch/turn_off": func(w http.ResponseWriter, r *http.Request) {
			var payload map[string]any
			json.NewDecoder(r.Body).Decode(&payload)

			if eid, ok := payload["entity_id"].(string); !ok || eid != "switch.porch" {
				t.Errorf("entity_id = %v, want 'switch.porch'", payload["entity_id"])
			}

			// Return empty changed states
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[]`))
		},
	})
	defer srv.Close()

	p := New(srv.URL, "test-token")
	ctx := context.Background()

	result, err := p.Execute(ctx, "switch.porch", "turn_off", nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !result.Success {
		t.Error("Execute() Success = false, want true")
	}
	// No changed states returned, so NewState should be empty
	if result.NewState != "" {
		t.Errorf("Execute() NewState = %s, want empty", result.NewState)
	}
}

func TestHAPlugin_Execute_InvalidEntityID(t *testing.T) {
	p := New("http://localhost:8123", "token")
	ctx := context.Background()

	_, err := p.Execute(ctx, "no_dot_here", "turn_on", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want 'invalid entity ID' error")
	}
}

func TestHAPlugin_ListDevices_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"401: Unauthorized"}`))
	}))
	defer srv.Close()

	p := New(srv.URL, "bad-token")
	ctx := context.Background()

	_, err := p.ListDevices(ctx)
	if err == nil {
		t.Fatal("ListDevices() error = nil, want 401 error")
	}
	if !containsStr(err.Error(), "401") {
		t.Errorf("error = %q, want to contain '401'", err.Error())
	}
}

func TestHAPlugin_ListDevices_ServerError(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api/states": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message":"internal server error"}`))
		},
	})
	defer srv.Close()

	p := New(srv.URL, "test-token")
	ctx := context.Background()

	_, err := p.ListDevices(ctx)
	if err == nil {
		t.Fatal("ListDevices() error = nil, want 500 error")
	}
	if !containsStr(err.Error(), "500") {
		t.Errorf("error = %q, want to contain '500'", err.Error())
	}
}

func TestHAPlugin_Execute_ServerError(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api/services/light/turn_on": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message":"service unavailable"}`))
		},
	})
	defer srv.Close()

	p := New(srv.URL, "test-token")
	ctx := context.Background()

	_, err := p.Execute(ctx, "light.living_room", "turn_on", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want 500 error")
	}
	if !containsStr(err.Error(), "500") {
		t.Errorf("error = %q, want to contain '500'", err.Error())
	}
}

func TestHAPlugin_ListDevices_InvalidJSON(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api/states": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`not json at all`))
		},
	})
	defer srv.Close()

	p := New(srv.URL, "test-token")
	ctx := context.Background()

	_, err := p.ListDevices(ctx)
	if err == nil {
		t.Fatal("ListDevices() error = nil, want JSON parse error")
	}
	if !containsStr(err.Error(), "parse") {
		t.Errorf("error = %q, want to contain 'parse'", err.Error())
	}
}

func TestHAPlugin_TrailingSlash(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api/states": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[]`))
		},
	})
	defer srv.Close()

	// New should strip the trailing slash
	p := New(srv.URL+"/", "test-token")
	ctx := context.Background()

	devices, err := p.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices() error = %v", err)
	}
	if len(devices) != 0 {
		t.Errorf("ListDevices() count = %d, want 0", len(devices))
	}
}

func TestDomainOf(t *testing.T) {
	tests := []struct {
		entityID string
		want     string
	}{
		{"light.living_room", "light"},
		{"switch.porch", "switch"},
		{"lock.front_door", "lock"},
		{"climate.thermostat", "climate"},
		{"sensor.temperature", "sensor"},
		{"binary_sensor.motion", "binary_sensor"},
		{"no_dot", ""},
		{"", ""},
		{".leading_dot", ""},
	}

	for _, tt := range tests {
		t.Run(tt.entityID, func(t *testing.T) {
			got := domainOf(tt.entityID)
			if got != tt.want {
				t.Errorf("domainOf(%q) = %q, want %q", tt.entityID, got, tt.want)
			}
		})
	}
}

func TestActionsForDomain(t *testing.T) {
	tests := []struct {
		domain      string
		wantCount   int
		wantContain string
	}{
		{"light", 3, "toggle"},
		{"switch", 3, "turn_on"},
		{"lock", 2, "unlock"},
		{"climate", 4, "set_temperature"},
		{"cover", 3, "open_cover"},
		{"fan", 3, "set_percentage"},
		{"media_player", 5, "volume_set"},
		{"scene", 1, "turn_on"},
		{"script", 1, "turn_on"},
		{"automation", 3, "trigger"},
		{"vacuum", 3, "return_to_base"},
		{"unknown_domain", 2, "turn_off"}, // default fallback
	}

	for _, tt := range tests {
		t.Run(tt.domain, func(t *testing.T) {
			actions := actionsForDomain(tt.domain)
			if len(actions) != tt.wantCount {
				t.Errorf("actionsForDomain(%q) count = %d, want %d: %v", tt.domain, len(actions), tt.wantCount, actions)
			}
			found := false
			for _, a := range actions {
				if a == tt.wantContain {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("actionsForDomain(%q) = %v, want to contain %q", tt.domain, actions, tt.wantContain)
			}
		})
	}
}

func TestIsControllable(t *testing.T) {
	controllable := []string{
		"light", "switch", "lock", "climate", "cover", "fan",
		"media_player", "scene", "script", "automation", "vacuum",
		"humidifier", "water_heater", "input_boolean",
	}
	for _, domain := range controllable {
		if !isControllable(domain) {
			t.Errorf("isControllable(%q) = false, want true", domain)
		}
	}

	notControllable := []string{
		"sensor", "binary_sensor", "device_tracker", "weather",
		"zone", "person", "sun", "calendar", "",
	}
	for _, domain := range notControllable {
		if isControllable(domain) {
			t.Errorf("isControllable(%q) = true, want false", domain)
		}
	}
}

func TestFilterMetadata(t *testing.T) {
	attrs := map[string]any{
		"friendly_name":       "Test Light",  // not relevant
		"brightness":          200,            // relevant
		"color_temp":          350,            // relevant
		"supported_features":  44,             // not relevant
		"temperature":         72.0,           // relevant
		"current_temperature": 68.0,           // relevant
		"icon":                "mdi:lightbulb", // not relevant
	}

	filtered := filterMetadata(attrs)
	if filtered == nil {
		t.Fatal("filterMetadata() returned nil, want non-nil")
	}

	if _, ok := filtered["brightness"]; !ok {
		t.Error("missing 'brightness'")
	}
	if _, ok := filtered["color_temp"]; !ok {
		t.Error("missing 'color_temp'")
	}
	if _, ok := filtered["temperature"]; !ok {
		t.Error("missing 'temperature'")
	}
	if _, ok := filtered["current_temperature"]; !ok {
		t.Error("missing 'current_temperature'")
	}
	if _, ok := filtered["friendly_name"]; ok {
		t.Error("should not include 'friendly_name'")
	}
	if _, ok := filtered["supported_features"]; ok {
		t.Error("should not include 'supported_features'")
	}
	if _, ok := filtered["icon"]; ok {
		t.Error("should not include 'icon'")
	}
}

func TestFilterMetadata_Empty(t *testing.T) {
	filtered := filterMetadata(map[string]any{
		"friendly_name":      "Test",
		"supported_features": 0,
	})

	// No relevant keys => should return nil
	if filtered != nil {
		t.Errorf("filterMetadata() = %v, want nil", filtered)
	}
}

// containsStr is a helper to check substring presence.
func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
