package plugin

import (
	"context"
	"fmt"
	"sync"
)

// Registry manages all registered plugins and provides a unified interface
// for listing devices and executing commands across all plugins.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
}

// NewRegistry creates an empty plugin registry.
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]Plugin),
	}
}

// Register adds a plugin to the registry.
// Returns an error if a plugin with the same name is already registered.
func (r *Registry) Register(p Plugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := p.Name()
	if _, exists := r.plugins[name]; exists {
		return fmt.Errorf("plugin already registered: %s", name)
	}

	r.plugins[name] = p
	return nil
}

// PluginNames returns the names of all registered plugins.
func (r *Registry) PluginNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.plugins))
	for name := range r.plugins {
		names = append(names, name)
	}
	return names
}

// ListAllDevices returns devices from all registered plugins.
// Device IDs are prefixed with the plugin name: "pluginName:deviceID".
func (r *Registry) ListAllDevices(ctx context.Context) ([]Device, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var all []Device
	for _, p := range r.plugins {
		devices, err := p.ListDevices(ctx)
		if err != nil {
			// Log but don't fail — other plugins may work fine
			all = append(all, Device{
				ID:     "_error",
				Name:   fmt.Sprintf("[%s] error: %v", p.Name(), err),
				Plugin: p.Name(),
				Type:   "error",
			})
			continue
		}
		for i := range devices {
			devices[i].Plugin = p.Name()
			// Prefix ID with plugin name for globally unique addressing
			devices[i].ID = p.Name() + ":" + devices[i].ID
		}
		all = append(all, devices...)
	}
	return all, nil
}

// Execute routes a command to the appropriate plugin.
// deviceID must be in "pluginName:localDeviceID" format.
func (r *Registry) Execute(ctx context.Context, deviceID string, action string, params map[string]any) (*Result, error) {
	pluginName, localID, err := splitDeviceID(deviceID)
	if err != nil {
		return nil, err
	}

	r.mu.RLock()
	p, exists := r.plugins[pluginName]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("unknown plugin: %s", pluginName)
	}

	return p.Execute(ctx, localID, action, params)
}

// splitDeviceID splits "pluginName:deviceID" into its parts.
func splitDeviceID(id string) (pluginName, deviceID string, err error) {
	for i, c := range id {
		if c == ':' {
			return id[:i], id[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("invalid device ID %q: expected format \"plugin:device_id\"", id)
}
