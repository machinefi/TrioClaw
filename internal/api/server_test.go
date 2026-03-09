package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/machinefi/trioclaw/internal/config"
	"github.com/machinefi/trioclaw/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Cameras = []config.CameraConfig{
		{ID: "cam0", Name: "Front Door", Source: "rtsp://admin:pass@192.168.1.10:554/stream", FPS: 1},
	}
	cfg.Clips.Dir = os.TempDir()
	return cfg
}

func TestHealthz(t *testing.T) {
	s := testStore(t)
	srv := NewServer(testConfig(), s, nil)

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("status = %s, want ok", body["status"])
	}
}

func TestCameras(t *testing.T) {
	s := testStore(t)
	srv := NewServer(testConfig(), s, nil)

	req := httptest.NewRequest("GET", "/api/cameras", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var cams []map[string]any
	json.NewDecoder(w.Body).Decode(&cams)
	if len(cams) != 1 {
		t.Fatalf("cameras = %d, want 1", len(cams))
	}
	// Credentials should be masked
	source := cams[0]["source"].(string)
	if source == "rtsp://admin:pass@192.168.1.10:554/stream" {
		t.Error("source URL credentials not masked")
	}
	if source != "rtsp://***:***@192.168.1.10:554/stream" {
		t.Errorf("source = %s, want masked URL", source)
	}
}

func TestRecentAlerts(t *testing.T) {
	s := testStore(t)
	// Insert some alerts
	for i := 0; i < 3; i++ {
		s.InsertAlert(&store.Event{
			Timestamp:   time.Now().Add(-time.Duration(i) * time.Minute),
			CameraID:    "cam0",
			ConditionID: "person",
			Answer:      "Yes",
			Triggered:   true,
		})
	}

	srv := NewServer(testConfig(), s, nil)

	req := httptest.NewRequest("GET", "/api/alerts/recent?limit=2", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var alerts []eventJSON
	json.NewDecoder(w.Body).Decode(&alerts)
	if len(alerts) != 2 {
		t.Errorf("alerts = %d, want 2", len(alerts))
	}
}

func TestStats(t *testing.T) {
	s := testStore(t)
	s.InsertEvent(&store.Event{
		Timestamp:   time.Now(),
		CameraID:    "cam0",
		ConditionID: "person",
		Answer:      "No",
	})
	s.InsertAlert(&store.Event{
		Timestamp:   time.Now(),
		CameraID:    "cam0",
		ConditionID: "person",
		Answer:      "Yes",
	})

	srv := NewServer(testConfig(), s, nil)

	req := httptest.NewRequest("GET", "/api/stats", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var stats map[string]any
	json.NewDecoder(w.Body).Decode(&stats)
	if stats["total_events"].(float64) != 2 {
		t.Errorf("total_events = %v, want 2", stats["total_events"])
	}
	if stats["total_alerts"].(float64) != 1 {
		t.Errorf("total_alerts = %v, want 1", stats["total_alerts"])
	}
}

func TestWatchesEmpty(t *testing.T) {
	s := testStore(t)
	srv := NewServer(testConfig(), s, nil)

	req := httptest.NewRequest("GET", "/api/watches", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var watches []watchEntry
	json.NewDecoder(w.Body).Decode(&watches)
	if len(watches) != 0 {
		t.Errorf("watches = %d, want 0", len(watches))
	}
}

func TestMaskSource(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"rtsp://admin:pass@192.168.1.1:554/stream", "rtsp://***:***@192.168.1.1:554/stream"},
		{"rtsp://192.168.1.1:554/stream", "rtsp://192.168.1.1:554/stream"},
		{"/dev/video0", "/dev/video0"},
		{"0", "0"},
	}
	for _, tt := range tests {
		got := maskSource(tt.input)
		if got != tt.want {
			t.Errorf("maskSource(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
