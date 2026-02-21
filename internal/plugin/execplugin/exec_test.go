package execplugin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeScript creates an executable script file in the given directory.
// On non-Windows systems it uses a bash shebang. The script content should
// be a complete shell script body (the shebang is prepended automatically).
func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := "#!/bin/bash\n" + body
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("failed to write script %s: %v", name, err)
	}
	return path
}

func TestExecPlugin_Name(t *testing.T) {
	dir := t.TempDir()
	p, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if p.Name() != "exec" {
		t.Errorf("Name() = %s, want exec", p.Name())
	}
}

func TestExecPlugin_DiscoverScripts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows: bash scripts not supported")
	}

	dir := t.TempDir()

	// Create executable scripts
	writeScript(t, dir, "lamp.sh", `echo "[]"`)
	writeScript(t, dir, "thermostat.py", `echo "[]"`)

	// Create a non-executable file (should be skipped)
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not a script"), 0644)

	// Create a hidden file (should be skipped)
	writeScript(t, dir, ".hidden.sh", `echo "[]"`)

	// Create a directory (should be skipped)
	os.Mkdir(filepath.Join(dir, "subdir"), 0755)

	p, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	count := p.ScriptCount()
	if count != 2 {
		t.Errorf("ScriptCount() = %d, want 2 (lamp, thermostat)", count)
	}
}

func TestExecPlugin_DiscoverScripts_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	p, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if p.ScriptCount() != 0 {
		t.Errorf("ScriptCount() = %d, want 0", p.ScriptCount())
	}
}

func TestExecPlugin_DiscoverScripts_NonexistentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")

	p, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Should not error, just have 0 scripts
	if p.ScriptCount() != 0 {
		t.Errorf("ScriptCount() = %d, want 0", p.ScriptCount())
	}
}

func TestExecPlugin_ListDevices(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows: bash scripts not supported")
	}

	dir := t.TempDir()

	// A script that outputs devices as JSON
	writeScript(t, dir, "smart-lamp.sh", `
read input
echo '[{"id":"desk-lamp","name":"Desk Lamp","type":"light","actions":["turn_on","turn_off"]},{"id":"floor-lamp","name":"Floor Lamp","type":"light","actions":["turn_on","turn_off","toggle"]}]'
`)

	p, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()
	devices, err := p.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices() error = %v", err)
	}

	if len(devices) != 2 {
		t.Fatalf("ListDevices() count = %d, want 2", len(devices))
	}

	// Device IDs should be prefixed with script name
	if devices[0].ID != "smart-lamp/desk-lamp" {
		t.Errorf("devices[0].ID = %s, want 'smart-lamp/desk-lamp'", devices[0].ID)
	}
	if devices[0].Name != "Desk Lamp" {
		t.Errorf("devices[0].Name = %s, want 'Desk Lamp'", devices[0].Name)
	}
	if devices[0].Type != "light" {
		t.Errorf("devices[0].Type = %s, want 'light'", devices[0].Type)
	}
	if devices[1].ID != "smart-lamp/floor-lamp" {
		t.Errorf("devices[1].ID = %s, want 'smart-lamp/floor-lamp'", devices[1].ID)
	}
}

func TestExecPlugin_ListDevices_MultipleScripts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows: bash scripts not supported")
	}

	dir := t.TempDir()

	writeScript(t, dir, "lights.sh", `
read input
echo '[{"id":"lamp","name":"Lamp","type":"light","actions":["turn_on","turn_off"]}]'
`)
	writeScript(t, dir, "locks.sh", `
read input
echo '[{"id":"front","name":"Front Door","type":"lock","actions":["lock","unlock"]}]'
`)

	p, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()
	devices, err := p.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices() error = %v", err)
	}

	if len(devices) != 2 {
		t.Fatalf("ListDevices() count = %d, want 2", len(devices))
	}
}

func TestExecPlugin_ListDevices_ScriptError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows: bash scripts not supported")
	}

	dir := t.TempDir()

	// A script that exits with error
	writeScript(t, dir, "broken.sh", `
echo "something went wrong" >&2
exit 1
`)

	p, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()
	devices, err := p.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices() error = %v, want nil (errors become pseudo-devices)", err)
	}

	if len(devices) != 1 {
		t.Fatalf("ListDevices() count = %d, want 1 (error pseudo-device)", len(devices))
	}

	if devices[0].Type != "error" {
		t.Errorf("error device Type = %s, want 'error'", devices[0].Type)
	}
	if devices[0].ID != "broken:_error" {
		t.Errorf("error device ID = %s, want 'broken:_error'", devices[0].ID)
	}
}

func TestExecPlugin_ListDevices_InvalidJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows: bash scripts not supported")
	}

	dir := t.TempDir()

	// A script that outputs invalid JSON
	writeScript(t, dir, "bad-json.sh", `
read input
echo 'this is not json'
`)

	p, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()
	devices, err := p.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices() error = %v, want nil (errors become pseudo-devices)", err)
	}

	if len(devices) != 1 {
		t.Fatalf("ListDevices() count = %d, want 1 (error pseudo-device)", len(devices))
	}

	if devices[0].Type != "error" {
		t.Errorf("error device Type = %s, want 'error'", devices[0].Type)
	}
	if !containsStr(devices[0].Name, "invalid JSON") {
		t.Errorf("error device Name = %q, want to contain 'invalid JSON'", devices[0].Name)
	}
}

func TestExecPlugin_Execute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows: bash scripts not supported")
	}

	dir := t.TempDir()

	// A script that handles execute commands
	writeScript(t, dir, "smart-lamp.sh", `
read input
echo '{"success":true,"message":"Desk lamp turned on","new_state":"on"}'
`)

	p, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()
	result, err := p.Execute(ctx, "smart-lamp/desk-lamp", "turn_on", map[string]any{"brightness": 255})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !result.Success {
		t.Error("Execute() Success = false, want true")
	}
	if result.Message != "Desk lamp turned on" {
		t.Errorf("Execute() Message = %q, want 'Desk lamp turned on'", result.Message)
	}
	if result.NewState != "on" {
		t.Errorf("Execute() NewState = %s, want 'on'", result.NewState)
	}
}

func TestExecPlugin_Execute_ScriptError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows: bash scripts not supported")
	}

	dir := t.TempDir()

	writeScript(t, dir, "failing.sh", `
echo "device not responding" >&2
exit 1
`)

	p, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()
	_, err = p.Execute(ctx, "failing/some-device", "turn_on", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if !containsStr(err.Error(), "device not responding") {
		t.Errorf("error = %q, want to contain 'device not responding'", err.Error())
	}
}

func TestExecPlugin_Execute_InvalidJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows: bash scripts not supported")
	}

	dir := t.TempDir()

	writeScript(t, dir, "badjson.sh", `
read input
echo 'not valid json'
`)

	p, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()
	_, err = p.Execute(ctx, "badjson/dev1", "turn_on", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want 'invalid JSON' error")
	}
	if !containsStr(err.Error(), "invalid JSON") {
		t.Errorf("error = %q, want to contain 'invalid JSON'", err.Error())
	}
}

func TestExecPlugin_Execute_UnknownScript(t *testing.T) {
	dir := t.TempDir()

	p, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()
	_, err = p.Execute(ctx, "nonexistent/device", "turn_on", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want 'unknown script' error")
	}
	if !containsStr(err.Error(), "unknown script") {
		t.Errorf("error = %q, want to contain 'unknown script'", err.Error())
	}
}

func TestExecPlugin_Execute_InvalidDeviceID(t *testing.T) {
	dir := t.TempDir()

	p, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()
	_, err = p.Execute(ctx, "no-slash-here", "turn_on", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want 'invalid exec device ID' error")
	}
	if !containsStr(err.Error(), "invalid exec device ID") {
		t.Errorf("error = %q, want to contain 'invalid exec device ID'", err.Error())
	}
}

func TestSplitScriptDeviceID(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		wantScript string
		wantDevice string
		wantErr    bool
	}{
		{
			name:       "normal",
			id:         "smart-lamp/desk-lamp",
			wantScript: "smart-lamp",
			wantDevice: "desk-lamp",
		},
		{
			name:       "nested slash",
			id:         "script/device/sub",
			wantScript: "script",
			wantDevice: "device/sub",
		},
		{
			name:    "no slash",
			id:      "noslash",
			wantErr: true,
		},
		{
			name:    "empty",
			id:      "",
			wantErr: true,
		},
		{
			name:    "slash at start",
			id:      "/device",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scriptName, deviceID, err := splitScriptDeviceID(tt.id)
			if tt.wantErr {
				if err == nil {
					t.Errorf("splitScriptDeviceID(%q) error = nil, want error", tt.id)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitScriptDeviceID(%q) error = %v", tt.id, err)
			}
			if scriptName != tt.wantScript {
				t.Errorf("scriptName = %s, want %s", scriptName, tt.wantScript)
			}
			if deviceID != tt.wantDevice {
				t.Errorf("deviceID = %s, want %s", deviceID, tt.wantDevice)
			}
		})
	}
}

func TestExecPlugin_Execute_WithData(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows: bash scripts not supported")
	}

	dir := t.TempDir()

	// Script that returns data in the result
	writeScript(t, dir, "thermostat.sh", `
read input
echo '{"success":true,"message":"Temperature set","new_state":"heating","data":{"target_temp":72,"current_temp":68}}'
`)

	p, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()
	result, err := p.Execute(ctx, "thermostat/main", "set_temperature", map[string]any{"temperature": 72})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !result.Success {
		t.Error("Execute() Success = false, want true")
	}
	if result.NewState != "heating" {
		t.Errorf("Execute() NewState = %s, want 'heating'", result.NewState)
	}
	if result.Data == nil {
		t.Fatal("Execute() Data is nil, want non-nil")
	}
	if tt, ok := result.Data["target_temp"].(float64); !ok || int(tt) != 72 {
		t.Errorf("Data[target_temp] = %v, want 72", result.Data["target_temp"])
	}
}

func TestExecPlugin_ScriptNameStripsExtension(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows: bash scripts not supported")
	}

	dir := t.TempDir()

	writeScript(t, dir, "my-device.sh", `echo '[]'`)
	writeScript(t, dir, "another.py", `echo '[]'`)

	p, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Verify scripts are indexed by name without extension
	if p.ScriptCount() != 2 {
		t.Fatalf("ScriptCount() = %d, want 2", p.ScriptCount())
	}

	// The scripts map should use names without extensions
	// We can verify by trying to execute with the stripped name
	ctx := context.Background()

	// This should find "my-device" (stripped from "my-device.sh")
	_, err = p.Execute(ctx, "my-device/test-dev", "list", nil)
	// The script doesn't return valid execute JSON, but the point is it found the script
	// (error should be about JSON parsing, not "unknown script")
	if err != nil && containsStr(err.Error(), "unknown script") {
		t.Error("script 'my-device' not found; extension was not stripped correctly")
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
