package digest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/machinefi/trioclaw/internal/notify"
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

func TestFormatPrompt(t *testing.T) {
	alerts := []store.Event{
		{Timestamp: time.Date(2026, 3, 9, 8, 12, 0, 0, time.Local), CameraID: "front-door", ConditionID: "person", Answer: "Yes, a person at the door"},
		{Timestamp: time.Date(2026, 3, 9, 9, 30, 0, 0, time.Local), CameraID: "garage", ConditionID: "car", Answer: "Garage door is open"},
	}
	prompt := formatPrompt(alerts, time.Date(2026, 3, 9, 0, 0, 0, 0, time.Local))

	if !strings.Contains(prompt, "2026-03-09") {
		t.Error("prompt missing date")
	}
	if !strings.Contains(prompt, "front-door") {
		t.Error("prompt missing camera")
	}
	if !strings.Contains(prompt, "person at the door") {
		t.Error("prompt missing answer")
	}
}

func TestFormatPlainSummary(t *testing.T) {
	alerts := []store.Event{
		{Timestamp: time.Date(2026, 3, 9, 8, 12, 0, 0, time.Local), CameraID: "cam0", ConditionID: "person", Answer: "Yes"},
		{Timestamp: time.Date(2026, 3, 9, 8, 15, 0, 0, time.Local), CameraID: "cam0", ConditionID: "person", Answer: "Yes again"},
		{Timestamp: time.Date(2026, 3, 9, 9, 0, 0, 0, time.Local), CameraID: "cam1", ConditionID: "car", Answer: "Open"},
	}
	summary := formatPlainSummary(alerts, time.Date(2026, 3, 9, 0, 0, 0, 0, time.Local))

	if !strings.Contains(summary, "3 alerts total") {
		t.Errorf("summary missing total: %s", summary)
	}
	if !strings.Contains(summary, "cam0: 2 alerts") {
		t.Errorf("summary missing cam0 count: %s", summary)
	}
	if !strings.Contains(summary, "cam1: 1 alerts") {
		t.Errorf("summary missing cam1 count: %s", summary)
	}
}

func TestRunOnceNoAlerts(t *testing.T) {
	s := testStore(t)
	d := notify.NewDispatcher()

	runner := NewRunner(s, d, Config{LLM: "local", LLMURL: "http://localhost:8000", PushTo: nil})

	err := runner.RunOnce(context.Background(), time.Now())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunOnceWithLocalLLM(t *testing.T) {
	s := testStore(t)

	// Insert alerts for today
	now := time.Now()
	s.InsertAlert(&store.Event{
		Timestamp:   now,
		CameraID:    "cam0",
		ConditionID: "person",
		Answer:      "A person detected",
		Triggered:   true,
	})

	// Mock LLM server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "Today 1 person was detected at cam0."}},
			},
		})
	}))
	defer server.Close()

	var sentAlert notify.Alert
	mock := &mockNotifier{name: "test", onSend: func(a notify.Alert) { sentAlert = a }}
	d := notify.NewDispatcher()
	d.Register(mock)

	runner := NewRunner(s, d, Config{
		LLM:    "local",
		LLMURL: server.URL,
		PushTo: []string{"test"},
	})

	err := runner.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Wait for async dispatch
	time.Sleep(50 * time.Millisecond)

	if sentAlert.Answer == "" {
		t.Error("expected digest to be sent")
	}
	if !strings.Contains(sentAlert.Answer, "person") {
		t.Errorf("expected digest to mention person, got: %s", sentAlert.Answer)
	}
}

func TestParseInt(t *testing.T) {
	if parseInt("22", 0) != 22 {
		t.Error("parseInt(22) failed")
	}
	if parseInt("abc", 10) != 10 {
		t.Error("parseInt fallback failed")
	}
	if parseInt("0", 5) != 0 {
		t.Error("parseInt(0) failed")
	}
}

// mockNotifier for testing
type mockNotifier struct {
	name   string
	onSend func(notify.Alert)
}

func (m *mockNotifier) Name() string { return m.name }
func (m *mockNotifier) SendAlert(ctx context.Context, alert notify.Alert) error {
	if m.onSend != nil {
		m.onSend(alert)
	}
	return nil
}
