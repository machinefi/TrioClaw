// Package config handles YAML-based configuration for TrioClaw.
//
// Config file: ~/.trioclaw/config.yaml
//
// Manages cameras, trio-core connection, notification channels,
// clip recording settings, and daily digest preferences.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration for TrioClaw.
type Config struct {
	// TrioCore connection settings
	TrioCore TrioCoreConfig `yaml:"trio_core"`

	// Camera definitions
	Cameras []CameraConfig `yaml:"cameras"`

	// Notification channels
	Notifications NotificationConfig `yaml:"notifications,omitempty"`

	// Clip recording settings
	Clips ClipConfig `yaml:"clips,omitempty"`

	// Daily digest settings
	Digest DigestConfig `yaml:"digest,omitempty"`
}

// TrioCoreConfig is the connection settings for the local trio-core server.
type TrioCoreConfig struct {
	URL string `yaml:"url"` // e.g. http://localhost:8000
}

// CameraConfig defines a single camera and its watch conditions.
type CameraConfig struct {
	ID         string            `yaml:"id"`         // unique identifier, e.g. "front-door"
	Name       string            `yaml:"name"`       // human-readable, e.g. "Front Door"
	Source     string            `yaml:"source"`     // RTSP URL, device path, or device index
	FPS        int               `yaml:"fps,omitempty"` // max check rate hint (default: 1)
	Conditions []ConditionConfig `yaml:"conditions"` // what to watch for
}

// ConditionConfig defines a single condition to monitor.
type ConditionConfig struct {
	ID       string   `yaml:"id"`       // e.g. "person", "package"
	Question string   `yaml:"question"` // question for the VLM
	Actions  []string `yaml:"actions"`  // notification channels to trigger
}

// NotificationConfig holds all notification channel settings.
type NotificationConfig struct {
	Telegram *TelegramConfig `yaml:"telegram,omitempty"`
	Slack    *SlackConfig    `yaml:"slack,omitempty"`
	Webhook  *WebhookConfig  `yaml:"webhook,omitempty"`
}

// TelegramConfig for Telegram bot notifications.
type TelegramConfig struct {
	BotToken string `yaml:"bot_token"`
	ChatID   string `yaml:"chat_id"`
}

// SlackConfig for Slack webhook notifications.
type SlackConfig struct {
	WebhookURL string `yaml:"webhook_url"`
}

// WebhookConfig for generic webhook notifications.
type WebhookConfig struct {
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers,omitempty"`
}

// ClipConfig for clip recording.
type ClipConfig struct {
	Dir         string `yaml:"dir,omitempty"`          // default: ~/.trioclaw/clips/
	PreSeconds  int    `yaml:"pre_seconds,omitempty"`  // seconds before trigger (default: 15)
	PostSeconds int    `yaml:"post_seconds,omitempty"` // seconds after trigger (default: 15)
}

// DigestConfig for daily digest.
type DigestConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Schedule string   `yaml:"schedule,omitempty"` // cron expression (default: "0 22 * * *")
	LLM      string   `yaml:"llm,omitempty"`      // "local", "claude", "openai"
	PushTo   []string `yaml:"push_to,omitempty"`  // notification channels
}

// DefaultConfigDir returns ~/.trioclaw/
func DefaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".trioclaw"
	}
	return filepath.Join(home, ".trioclaw")
}

// DefaultConfigPath returns ~/.trioclaw/config.yaml
func DefaultConfigPath() string {
	return filepath.Join(DefaultConfigDir(), "config.yaml")
}

// Load reads config from the default path. Returns empty config if file doesn't exist.
func Load() (*Config, error) {
	return LoadFrom(DefaultConfigPath())
}

// LoadFrom reads config from a specific path.
func LoadFrom(path string) (*Config, error) {
	cfg := &Config{}
	cfg.setDefaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.fillDefaults()
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	cfg.fillDefaults()
	return cfg, nil
}

// Save writes the config to the default path.
func (c *Config) Save() error {
	return c.SaveTo(DefaultConfigPath())
}

// SaveTo writes the config to a specific path.
func (c *Config) SaveTo(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// AddCamera adds a camera to the config. Returns error if ID already exists.
func (c *Config) AddCamera(cam CameraConfig) error {
	for _, existing := range c.Cameras {
		if existing.ID == cam.ID {
			return fmt.Errorf("camera %q already exists", cam.ID)
		}
	}
	c.Cameras = append(c.Cameras, cam)
	return nil
}

// RemoveCamera removes a camera by ID. Returns error if not found.
func (c *Config) RemoveCamera(id string) error {
	for i, cam := range c.Cameras {
		if cam.ID == id {
			c.Cameras = append(c.Cameras[:i], c.Cameras[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("camera %q not found", id)
}

// GetCamera returns a camera by ID.
func (c *Config) GetCamera(id string) *CameraConfig {
	for i := range c.Cameras {
		if c.Cameras[i].ID == id {
			return &c.Cameras[i]
		}
	}
	return nil
}

func (c *Config) setDefaults() {
	c.TrioCore.URL = "http://localhost:8000"
}

func (c *Config) fillDefaults() {
	if c.TrioCore.URL == "" {
		c.TrioCore.URL = "http://localhost:8000"
	}
	for i := range c.Cameras {
		if c.Cameras[i].FPS <= 0 {
			c.Cameras[i].FPS = 1
		}
	}
	if c.Clips.Dir == "" {
		c.Clips.Dir = filepath.Join(DefaultConfigDir(), "clips")
	}
	if c.Clips.PreSeconds <= 0 {
		c.Clips.PreSeconds = 15
	}
	if c.Clips.PostSeconds <= 0 {
		c.Clips.PostSeconds = 15
	}
	if c.Digest.Schedule == "" {
		c.Digest.Schedule = "0 22 * * *"
	}
	if c.Digest.LLM == "" {
		c.Digest.LLM = "local"
	}
}
