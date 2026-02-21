// Package plugin defines the interface for device control plugins.
//
// TrioClaw's "hands" — plugins let the AI agent control smart devices,
// run scripts, and interact with the physical world.
//
// Built-in plugins:
//   - homeassistant: Control any device through Home Assistant's REST API
//   - execplugin:    Run shell scripts/binaries for custom device control
//
// Third-party plugins can be added by:
//   1. Implementing the Plugin interface in Go (compile-time)
//   2. Dropping a script into ~/.trioclaw/plugins/ (runtime, via execplugin)
package plugin

import "context"

// Plugin is the interface that all device control plugins must implement.
type Plugin interface {
	// Name returns a unique identifier for this plugin (e.g. "homeassistant", "hue").
	Name() string

	// ListDevices returns all devices this plugin can control.
	ListDevices(ctx context.Context) ([]Device, error)

	// Execute performs an action on a device.
	//   deviceID: which device to control (from ListDevices)
	//   action:   what to do ("turn_on", "turn_off", "set_brightness", etc.)
	//   params:   action-specific parameters (e.g. {"brightness": 128})
	Execute(ctx context.Context, deviceID string, action string, params map[string]any) (*Result, error)
}

// Device represents a controllable device.
type Device struct {
	ID       string         `json:"id"`                 // unique ID within this plugin
	Name     string         `json:"name"`               // human-readable name
	Plugin   string         `json:"plugin"`             // which plugin owns this device
	Type     string         `json:"type"`               // "light", "switch", "lock", "climate", etc.
	State    string         `json:"state,omitempty"`    // current state ("on", "off", "locked", etc.)
	Actions  []string       `json:"actions,omitempty"`  // available actions
	Metadata map[string]any `json:"metadata,omitempty"` // extra info (brightness, color, temperature, etc.)
}

// Result is the outcome of an Execute call.
type Result struct {
	Success  bool           `json:"success"`
	Message  string         `json:"message,omitempty"`
	NewState string         `json:"new_state,omitempty"` // state after execution
	Data     map[string]any `json:"data,omitempty"`      // extra response data
}
