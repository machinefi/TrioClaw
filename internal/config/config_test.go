package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEmpty(t *testing.T) {
	cfg, err := LoadFrom("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrioCore.URL != "http://localhost:8000" {
		t.Errorf("expected default trio-core URL, got %s", cfg.TrioCore.URL)
	}
	if cfg.Clips.PreSeconds != 15 {
		t.Errorf("expected default pre_seconds=15, got %d", cfg.Clips.PreSeconds)
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg := &Config{}
	cfg.TrioCore.URL = "http://localhost:9000"
	cfg.Cameras = []CameraConfig{
		{
			ID:     "front-door",
			Name:   "Front Door",
			Source: "rtsp://admin:pass@192.168.1.10:554/stream",
			FPS:    2,
			Conditions: []ConditionConfig{
				{ID: "person", Question: "Is there a person?", Actions: []string{"telegram"}},
			},
		},
	}

	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}

	if loaded.TrioCore.URL != "http://localhost:9000" {
		t.Errorf("trio-core URL = %s, want http://localhost:9000", loaded.TrioCore.URL)
	}
	if len(loaded.Cameras) != 1 {
		t.Fatalf("cameras count = %d, want 1", len(loaded.Cameras))
	}
	if loaded.Cameras[0].ID != "front-door" {
		t.Errorf("camera ID = %s, want front-door", loaded.Cameras[0].ID)
	}
	if loaded.Cameras[0].FPS != 2 {
		t.Errorf("camera FPS = %d, want 2", loaded.Cameras[0].FPS)
	}
	if len(loaded.Cameras[0].Conditions) != 1 {
		t.Fatalf("conditions count = %d, want 1", len(loaded.Cameras[0].Conditions))
	}
	if loaded.Cameras[0].Conditions[0].Question != "Is there a person?" {
		t.Errorf("condition question = %s", loaded.Cameras[0].Conditions[0].Question)
	}
}

func TestAddRemoveCamera(t *testing.T) {
	cfg := &Config{}

	cam := CameraConfig{ID: "test", Name: "Test", Source: "rtsp://test"}
	if err := cfg.AddCamera(cam); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Cameras) != 1 {
		t.Fatalf("expected 1 camera, got %d", len(cfg.Cameras))
	}

	// Duplicate should fail
	if err := cfg.AddCamera(cam); err == nil {
		t.Error("expected error on duplicate camera ID")
	}

	// Get camera
	got := cfg.GetCamera("test")
	if got == nil {
		t.Fatal("GetCamera returned nil")
	}
	if got.Name != "Test" {
		t.Errorf("name = %s, want Test", got.Name)
	}

	// Remove
	if err := cfg.RemoveCamera("test"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Cameras) != 0 {
		t.Errorf("expected 0 cameras after remove, got %d", len(cfg.Cameras))
	}

	// Remove nonexistent
	if err := cfg.RemoveCamera("nope"); err == nil {
		t.Error("expected error on nonexistent camera")
	}
}

func TestFillDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// Write minimal YAML
	os.WriteFile(path, []byte("trio_core:\n  url: http://myhost:8000\ncameras:\n  - id: x\n    name: X\n    source: rtsp://test\n"), 0644)

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}

	// FPS should default to 1
	if cfg.Cameras[0].FPS != 1 {
		t.Errorf("FPS = %d, want 1", cfg.Cameras[0].FPS)
	}
	// Clips dir should have default
	if cfg.Clips.Dir == "" {
		t.Error("Clips.Dir should have a default")
	}
}
