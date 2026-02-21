// Package homeassistant implements a TrioClaw plugin for Home Assistant.
//
// Home Assistant exposes a REST API that can control 2000+ device integrations
// (lights, switches, locks, climate, covers, media players, etc.).
//
// Configuration:
//
//	trioclaw run --ha-url http://homeassistant.local:8123 --ha-token <long-lived-access-token>
//
// How to get a token:
//
//	Home Assistant → Profile → Long-Lived Access Tokens → Create Token
//
// API reference: https://developers.home-assistant.io/docs/api/rest
package homeassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/machinefi/trioclaw/internal/plugin"
)

// HAPlugin controls devices through Home Assistant's REST API.
type HAPlugin struct {
	baseURL string       // e.g. "http://homeassistant.local:8123"
	token   string       // Long-lived access token
	client  *http.Client
}

// New creates a Home Assistant plugin.
func New(baseURL, token string) *HAPlugin {
	// Strip trailing slash
	baseURL = strings.TrimRight(baseURL, "/")

	return &HAPlugin{
		baseURL: baseURL,
		token:   token,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *HAPlugin) Name() string { return "ha" }

// haState is a single entity state from HA's /api/states endpoint.
type haState struct {
	EntityID   string         `json:"entity_id"`
	State      string         `json:"state"`
	Attributes map[string]any `json:"attributes"`
}

// ListDevices fetches all controllable entities from Home Assistant.
// Filters to actionable domains (light, switch, lock, climate, cover, fan,
// media_player, scene, script, automation).
func (p *HAPlugin) ListDevices(ctx context.Context) ([]plugin.Device, error) {
	body, err := p.get(ctx, "/api/states")
	if err != nil {
		return nil, fmt.Errorf("failed to list HA states: %w", err)
	}

	var states []haState
	if err := json.Unmarshal(body, &states); err != nil {
		return nil, fmt.Errorf("failed to parse HA states: %w", err)
	}

	var devices []plugin.Device
	for _, s := range states {
		domain := domainOf(s.EntityID)
		if !isControllable(domain) {
			continue
		}

		name := ""
		if fn, ok := s.Attributes["friendly_name"].(string); ok {
			name = fn
		} else {
			name = s.EntityID
		}

		devices = append(devices, plugin.Device{
			ID:      s.EntityID,
			Name:    name,
			Type:    domain,
			State:   s.State,
			Actions: actionsForDomain(domain),
			Metadata: filterMetadata(s.Attributes),
		})
	}

	return devices, nil
}

// Execute calls a Home Assistant service on an entity.
//
// Examples:
//
//	Execute(ctx, "light.living_room", "turn_on", {"brightness": 200})
//	Execute(ctx, "switch.porch", "turn_off", nil)
//	Execute(ctx, "lock.front_door", "lock", nil)
//	Execute(ctx, "climate.thermostat", "set_temperature", {"temperature": 72})
func (p *HAPlugin) Execute(ctx context.Context, deviceID string, action string, params map[string]any) (*plugin.Result, error) {
	domain := domainOf(deviceID)
	if domain == "" {
		return nil, fmt.Errorf("invalid entity ID: %s", deviceID)
	}

	// Build service call payload
	payload := map[string]any{
		"entity_id": deviceID,
	}
	for k, v := range params {
		payload[k] = v
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	// POST /api/services/{domain}/{action}
	path := fmt.Sprintf("/api/services/%s/%s", domain, action)
	respBody, err := p.post(ctx, path, payloadBytes)
	if err != nil {
		return nil, fmt.Errorf("HA service call failed: %w", err)
	}

	// HA returns an array of changed states
	var changedStates []haState
	json.Unmarshal(respBody, &changedStates)

	newState := ""
	if len(changedStates) > 0 {
		for _, s := range changedStates {
			if s.EntityID == deviceID {
				newState = s.State
				break
			}
		}
	}

	return &plugin.Result{
		Success:  true,
		Message:  fmt.Sprintf("%s.%s executed on %s", domain, action, deviceID),
		NewState: newState,
	}, nil
}

// --- HTTP helpers ---

func (p *HAPlugin) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", p.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HA API returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}

func (p *HAPlugin) post(ctx context.Context, path string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HA API returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}

// --- Domain helpers ---

// domainOf extracts the domain from an entity_id ("light.living_room" → "light").
func domainOf(entityID string) string {
	if i := strings.IndexByte(entityID, '.'); i > 0 {
		return entityID[:i]
	}
	return ""
}

// isControllable returns true for HA domains that represent controllable devices.
func isControllable(domain string) bool {
	switch domain {
	case "light", "switch", "lock", "climate", "cover", "fan",
		"media_player", "scene", "script", "automation", "vacuum",
		"humidifier", "water_heater", "input_boolean":
		return true
	}
	return false
}

// actionsForDomain returns the common actions for a device domain.
func actionsForDomain(domain string) []string {
	switch domain {
	case "light":
		return []string{"turn_on", "turn_off", "toggle"}
	case "switch", "input_boolean":
		return []string{"turn_on", "turn_off", "toggle"}
	case "lock":
		return []string{"lock", "unlock"}
	case "climate":
		return []string{"set_temperature", "set_hvac_mode", "turn_on", "turn_off"}
	case "cover":
		return []string{"open_cover", "close_cover", "stop_cover"}
	case "fan":
		return []string{"turn_on", "turn_off", "set_percentage"}
	case "media_player":
		return []string{"turn_on", "turn_off", "media_play", "media_pause", "volume_set"}
	case "scene":
		return []string{"turn_on"}
	case "script":
		return []string{"turn_on"}
	case "automation":
		return []string{"trigger", "turn_on", "turn_off"}
	case "vacuum":
		return []string{"start", "stop", "return_to_base"}
	default:
		return []string{"turn_on", "turn_off"}
	}
}

// filterMetadata picks relevant attributes for the device listing.
func filterMetadata(attrs map[string]any) map[string]any {
	relevant := map[string]bool{
		"brightness": true, "color_temp": true, "rgb_color": true,
		"temperature": true, "current_temperature": true, "hvac_mode": true,
		"percentage": true, "position": true, "volume_level": true,
		"media_title": true, "source": true,
	}

	filtered := make(map[string]any)
	for k, v := range attrs {
		if relevant[k] {
			filtered[k] = v
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
