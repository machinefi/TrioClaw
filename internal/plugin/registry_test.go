package plugin

import (
	"context"
	"fmt"
	"testing"
)

// mockPlugin is a simple Plugin implementation for testing the Registry.
type mockPlugin struct {
	name    string
	devices []Device
	err     error // if set, ListDevices returns this error
}

func (m *mockPlugin) Name() string { return m.name }

func (m *mockPlugin) ListDevices(ctx context.Context) ([]Device, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.devices, nil
}

func (m *mockPlugin) Execute(ctx context.Context, deviceID string, action string, params map[string]any) (*Result, error) {
	return &Result{
		Success:  true,
		Message:  fmt.Sprintf("%s.%s on %s", m.name, action, deviceID),
		NewState: "on",
	}, nil
}

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry() returned nil")
	}
	names := r.PluginNames()
	if len(names) != 0 {
		t.Errorf("PluginNames() = %v, want empty slice", names)
	}
}

func TestRegister(t *testing.T) {
	r := NewRegistry()

	p := &mockPlugin{name: "test"}
	if err := r.Register(p); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	names := r.PluginNames()
	if len(names) != 1 {
		t.Fatalf("PluginNames() count = %d, want 1", len(names))
	}
	if names[0] != "test" {
		t.Errorf("PluginNames()[0] = %s, want test", names[0])
	}
}

func TestRegister_Duplicate(t *testing.T) {
	r := NewRegistry()

	p1 := &mockPlugin{name: "dup"}
	p2 := &mockPlugin{name: "dup"}

	if err := r.Register(p1); err != nil {
		t.Fatalf("Register(p1) error = %v", err)
	}

	err := r.Register(p2)
	if err == nil {
		t.Fatal("Register(p2) error = nil, want duplicate error")
	}

	// Verify the error message
	if !contains(err.Error(), "already registered") {
		t.Errorf("error = %q, want to contain 'already registered'", err.Error())
	}
}

func TestRegister_Multiple(t *testing.T) {
	r := NewRegistry()

	plugins := []*mockPlugin{
		{name: "alpha"},
		{name: "beta"},
		{name: "gamma"},
	}

	for _, p := range plugins {
		if err := r.Register(p); err != nil {
			t.Fatalf("Register(%s) error = %v", p.name, err)
		}
	}

	names := r.PluginNames()
	if len(names) != 3 {
		t.Errorf("PluginNames() count = %d, want 3", len(names))
	}
}

func TestListAllDevices(t *testing.T) {
	r := NewRegistry()

	p1 := &mockPlugin{
		name: "ha",
		devices: []Device{
			{ID: "light.living_room", Name: "Living Room", Type: "light"},
			{ID: "switch.porch", Name: "Porch Switch", Type: "switch"},
		},
	}
	p2 := &mockPlugin{
		name: "exec",
		devices: []Device{
			{ID: "desk-lamp", Name: "Desk Lamp", Type: "light"},
		},
	}

	r.Register(p1)
	r.Register(p2)

	ctx := context.Background()
	devices, err := r.ListAllDevices(ctx)
	if err != nil {
		t.Fatalf("ListAllDevices() error = %v", err)
	}

	if len(devices) != 3 {
		t.Fatalf("ListAllDevices() count = %d, want 3", len(devices))
	}

	// Verify device IDs are prefixed with plugin name
	foundHA := 0
	foundExec := 0
	for _, d := range devices {
		if d.Plugin == "ha" {
			foundHA++
			// ID should start with "ha:"
			if len(d.ID) < 3 || d.ID[:3] != "ha:" {
				t.Errorf("HA device ID = %s, want prefix 'ha:'", d.ID)
			}
		}
		if d.Plugin == "exec" {
			foundExec++
			if len(d.ID) < 5 || d.ID[:5] != "exec:" {
				t.Errorf("Exec device ID = %s, want prefix 'exec:'", d.ID)
			}
		}
	}

	if foundHA != 2 {
		t.Errorf("found %d HA devices, want 2", foundHA)
	}
	if foundExec != 1 {
		t.Errorf("found %d exec devices, want 1", foundExec)
	}
}

func TestListAllDevices_WithError(t *testing.T) {
	r := NewRegistry()

	pOK := &mockPlugin{
		name: "ok",
		devices: []Device{
			{ID: "dev1", Name: "Device 1", Type: "light"},
		},
	}
	pFail := &mockPlugin{
		name: "fail",
		err:  fmt.Errorf("connection refused"),
	}

	r.Register(pOK)
	r.Register(pFail)

	ctx := context.Background()
	devices, err := r.ListAllDevices(ctx)
	if err != nil {
		t.Fatalf("ListAllDevices() error = %v, want nil (errors are soft)", err)
	}

	// Should have 1 real device + 1 error pseudo-device
	if len(devices) != 2 {
		t.Fatalf("ListAllDevices() count = %d, want 2", len(devices))
	}

	// Find the error device
	var errDevice *Device
	for i, d := range devices {
		if d.Type == "error" {
			errDevice = &devices[i]
			break
		}
	}
	if errDevice == nil {
		t.Fatal("no error pseudo-device found")
	}
	if errDevice.Plugin != "fail" {
		t.Errorf("error device Plugin = %s, want fail", errDevice.Plugin)
	}
	if !contains(errDevice.Name, "connection refused") {
		t.Errorf("error device Name = %q, want to contain 'connection refused'", errDevice.Name)
	}
}

func TestListAllDevices_Empty(t *testing.T) {
	r := NewRegistry()

	ctx := context.Background()
	devices, err := r.ListAllDevices(ctx)
	if err != nil {
		t.Fatalf("ListAllDevices() error = %v", err)
	}
	if devices != nil && len(devices) != 0 {
		t.Errorf("ListAllDevices() = %v, want nil or empty", devices)
	}
}

func TestExecute(t *testing.T) {
	r := NewRegistry()

	p := &mockPlugin{
		name: "ha",
		devices: []Device{
			{ID: "light.living_room", Name: "Living Room", Type: "light"},
		},
	}
	r.Register(p)

	ctx := context.Background()
	result, err := r.Execute(ctx, "ha:light.living_room", "turn_on", map[string]any{"brightness": 200})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !result.Success {
		t.Error("Execute() Success = false, want true")
	}
	if result.NewState != "on" {
		t.Errorf("Execute() NewState = %s, want on", result.NewState)
	}
	if !contains(result.Message, "turn_on") {
		t.Errorf("Execute() Message = %q, want to contain 'turn_on'", result.Message)
	}
	if !contains(result.Message, "light.living_room") {
		t.Errorf("Execute() Message = %q, want to contain 'light.living_room'", result.Message)
	}
}

func TestExecute_UnknownPlugin(t *testing.T) {
	r := NewRegistry()

	p := &mockPlugin{name: "ha"}
	r.Register(p)

	ctx := context.Background()
	_, err := r.Execute(ctx, "hue:light.bedroom", "turn_on", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want 'unknown plugin' error")
	}
	if !contains(err.Error(), "unknown plugin") {
		t.Errorf("error = %q, want to contain 'unknown plugin'", err.Error())
	}
}

func TestExecute_InvalidDeviceID(t *testing.T) {
	r := NewRegistry()

	ctx := context.Background()
	_, err := r.Execute(ctx, "no-colon-here", "turn_on", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want 'invalid device ID' error")
	}
	if !contains(err.Error(), "invalid device ID") {
		t.Errorf("error = %q, want to contain 'invalid device ID'", err.Error())
	}
}

func TestSplitDeviceID(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		wantPlugin string
		wantDevice string
		wantErr    bool
	}{
		{
			name:       "normal",
			id:         "ha:light.living_room",
			wantPlugin: "ha",
			wantDevice: "light.living_room",
		},
		{
			name:       "exec plugin",
			id:         "exec:my-script/lamp",
			wantPlugin: "exec",
			wantDevice: "my-script/lamp",
		},
		{
			name:       "colon in device ID",
			id:         "ha:sensor:temperature",
			wantPlugin: "ha",
			wantDevice: "sensor:temperature",
		},
		{
			name:    "no colon",
			id:      "just-a-string",
			wantErr: true,
		},
		{
			name:    "empty string",
			id:      "",
			wantErr: true,
		},
		{
			name:       "colon at start",
			id:         ":device",
			wantPlugin: "",
			wantDevice: "device",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pluginName, deviceID, err := splitDeviceID(tt.id)
			if tt.wantErr {
				if err == nil {
					t.Errorf("splitDeviceID(%q) error = nil, want error", tt.id)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitDeviceID(%q) error = %v", tt.id, err)
			}
			if pluginName != tt.wantPlugin {
				t.Errorf("pluginName = %s, want %s", pluginName, tt.wantPlugin)
			}
			if deviceID != tt.wantDevice {
				t.Errorf("deviceID = %s, want %s", deviceID, tt.wantDevice)
			}
		})
	}
}
