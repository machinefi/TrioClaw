// Package execplugin implements a TrioClaw plugin that runs external scripts.
//
// Users drop scripts into ~/.trioclaw/plugins/ to control custom devices.
// Each script is a plugin that communicates via JSON over stdin/stdout.
//
// Script protocol:
//
//	# List devices:
//	echo '{"command":"list"}' | ~/.trioclaw/plugins/my-device.sh
//	# stdout: [{"id":"desk-lamp","name":"Desk Lamp","type":"light","actions":["turn_on","turn_off"]}]
//
//	# Execute action:
//	echo '{"command":"execute","device_id":"desk-lamp","action":"turn_on","params":{}}' | ~/.trioclaw/plugins/my-device.sh
//	# stdout: {"success":true,"message":"Desk lamp turned on","new_state":"on"}
//
// Scripts must be executable and named *.sh, *.py, or have no extension.
package execplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/machinefi/trioclaw/internal/plugin"
)

// ExecPlugin discovers and runs scripts in a directory.
type ExecPlugin struct {
	dir     string            // directory to scan for scripts
	scripts map[string]string // script name → full path
}

// New creates an exec plugin that scans the given directory for scripts.
// If dir is empty, defaults to ~/.trioclaw/plugins/.
func New(dir string) (*ExecPlugin, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot determine home directory: %w", err)
		}
		dir = filepath.Join(home, ".trioclaw", "plugins")
	}

	p := &ExecPlugin{
		dir:     dir,
		scripts: make(map[string]string),
	}

	p.discoverScripts()
	return p, nil
}

func (p *ExecPlugin) Name() string { return "exec" }

// discoverScripts scans the plugin directory for executable scripts.
func (p *ExecPlugin) discoverScripts() {
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		return // directory doesn't exist yet, that's fine
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		// Skip hidden files and non-script files
		if strings.HasPrefix(name, ".") {
			continue
		}

		fullPath := filepath.Join(p.dir, name)
		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Must be executable
		if info.Mode()&0111 == 0 {
			continue
		}

		// Strip extension for the script name
		scriptName := strings.TrimSuffix(name, filepath.Ext(name))
		p.scripts[scriptName] = fullPath
	}
}

// scriptInput is the JSON sent to scripts on stdin.
type scriptInput struct {
	Command  string         `json:"command"`             // "list" or "execute"
	DeviceID string         `json:"device_id,omitempty"` // for "execute"
	Action   string         `json:"action,omitempty"`    // for "execute"
	Params   map[string]any `json:"params,omitempty"`    // for "execute"
}

// ListDevices calls each discovered script with {"command":"list"}
// and aggregates the results.
func (p *ExecPlugin) ListDevices(ctx context.Context) ([]plugin.Device, error) {
	var all []plugin.Device

	for scriptName, scriptPath := range p.scripts {
		input := scriptInput{Command: "list"}
		output, err := p.runScript(ctx, scriptPath, input)
		if err != nil {
			// Include error as a pseudo-device so the user can see what's wrong
			all = append(all, plugin.Device{
				ID:   scriptName + ":_error",
				Name: fmt.Sprintf("[%s] error: %v", scriptName, err),
				Type: "error",
			})
			continue
		}

		var devices []plugin.Device
		if err := json.Unmarshal(output, &devices); err != nil {
			all = append(all, plugin.Device{
				ID:   scriptName + ":_error",
				Name: fmt.Sprintf("[%s] invalid JSON output", scriptName),
				Type: "error",
			})
			continue
		}

		// Prefix device IDs with script name
		for i := range devices {
			devices[i].ID = scriptName + "/" + devices[i].ID
		}
		all = append(all, devices...)
	}

	return all, nil
}

// Execute calls the appropriate script with the execute command.
// deviceID format: "scriptName/localDeviceID"
func (p *ExecPlugin) Execute(ctx context.Context, deviceID string, action string, params map[string]any) (*plugin.Result, error) {
	scriptName, localID, err := splitScriptDeviceID(deviceID)
	if err != nil {
		return nil, err
	}

	scriptPath, ok := p.scripts[scriptName]
	if !ok {
		return nil, fmt.Errorf("unknown script: %s", scriptName)
	}

	input := scriptInput{
		Command:  "execute",
		DeviceID: localID,
		Action:   action,
		Params:   params,
	}

	output, err := p.runScript(ctx, scriptPath, input)
	if err != nil {
		return nil, fmt.Errorf("script %s failed: %w", scriptName, err)
	}

	var result plugin.Result
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("script %s returned invalid JSON: %w", scriptName, err)
	}

	return &result, nil
}

// ScriptCount returns how many scripts were discovered.
func (p *ExecPlugin) ScriptCount() int {
	return len(p.scripts)
}

// runScript executes a script with JSON input on stdin, returns stdout.
func (p *ExecPlugin) runScript(ctx context.Context, scriptPath string, input scriptInput) ([]byte, error) {
	inputBytes, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	// Enforce a timeout
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, scriptPath)
	cmd.Stdin = bytes.NewReader(inputBytes)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = err.Error()
		}
		return nil, fmt.Errorf("%s", strings.TrimSpace(errMsg))
	}

	return stdout.Bytes(), nil
}

// splitScriptDeviceID splits "scriptName/deviceID" into parts.
func splitScriptDeviceID(id string) (scriptName, deviceID string, err error) {
	if i := strings.IndexByte(id, '/'); i > 0 {
		return id[:i], id[i+1:], nil
	}
	return "", "", fmt.Errorf("invalid exec device ID %q: expected \"script/device_id\"", id)
}
