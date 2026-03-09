// smoke_test.go verifies that all trioclaw subsystems are wired correctly
// and work together end-to-end with mock services (no real cameras, no real gateway).
//
// Run with: go test ./e2e/ -run TestSmoke -v
package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/machinefi/trioclaw/internal/api"
	"github.com/machinefi/trioclaw/internal/config"
	"github.com/machinefi/trioclaw/internal/digest"
	"github.com/machinefi/trioclaw/internal/notify"
	"github.com/machinefi/trioclaw/internal/store"
	"github.com/machinefi/trioclaw/internal/triocore"
)

// =============================================================================
// Smoke Test: Full service stack
// =============================================================================

// TestSmoke_FullStack verifies the complete trioclaw service stack wires up:
// config → store → watch manager → notifications → API → digest.
// Uses mock trio-core SSE server to simulate real camera monitoring.
func TestSmoke_FullStack(t *testing.T) {
	tmpDir := t.TempDir()

	// --- Setup mock trio-core that returns SSE events ---
	trioCoreServer := newMockTrioCore(t)
	defer trioCoreServer.Close()

	// --- Config ---
	cfg := &config.Config{
		TrioCore: config.TrioCoreConfig{URL: trioCoreServer.URL},
		Cameras: []config.CameraConfig{
			{
				ID:     "front-door",
				Name:   "Front Door",
				Source: "rtsp://admin:pass@192.168.1.10:554/stream",
				FPS:    1,
				Conditions: []config.ConditionConfig{
					{ID: "person", Question: "Is there a person?", Actions: []string{"webhook", "telegram"}},
					{ID: "package", Question: "Is there a package?", Actions: []string{"webhook"}},
				},
			},
			{
				ID:     "garage",
				Name:   "Garage",
				Source: "rtsp://192.168.1.20:554/stream",
				FPS:    1,
				Conditions: []config.ConditionConfig{
					{ID: "car", Question: "Is the garage door open?", Actions: []string{"slack"}},
				},
			},
		},
		Digest: config.DigestConfig{
			Enabled:  true,
			Schedule: "0 22 * * *",
			LLM:      "local",
			PushTo:   []string{"webhook"},
		},
	}

	// --- SQLite store ---
	eventStore, err := store.Open(filepath.Join(tmpDir, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer eventStore.Close()

	// --- Notification dispatcher ---
	var webhookCalls int32
	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&webhookCalls, 1)
		w.WriteHeader(200)
	}))
	defer webhookServer.Close()

	dispatcher := notify.NewDispatcher()
	dispatcher.Register(notify.NewWebhook(webhookServer.URL, nil))
	dispatcher.Register(notify.NewSlack(webhookServer.URL)) // reuse same mock

	// Register condition actions
	for _, cam := range cfg.Cameras {
		for _, cond := range cam.Conditions {
			if len(cond.Actions) > 0 {
				dispatcher.SetActions(cam.ID, cond.ID, cond.Actions)
			}
		}
	}

	// --- Watch manager ---
	tcClient := triocore.NewClient(cfg.TrioCore.URL)
	watchMgr := triocore.NewManager(tcClient, cfg.Cameras)

	// Verify watch manager was created
	if watchMgr == nil {
		t.Fatal("watch manager is nil")
	}

	// --- API server ---
	apiSrv := api.NewServer(cfg, eventStore, watchMgr)
	if apiSrv == nil {
		t.Fatal("API server is nil")
	}

	// --- Verify all the pieces ---

	t.Run("config", func(t *testing.T) {
		if len(cfg.Cameras) != 2 {
			t.Errorf("cameras = %d, want 2", len(cfg.Cameras))
		}
		if cfg.Cameras[0].ID != "front-door" {
			t.Errorf("camera[0].ID = %s", cfg.Cameras[0].ID)
		}
		if len(cfg.Cameras[0].Conditions) != 2 {
			t.Errorf("conditions = %d, want 2", len(cfg.Cameras[0].Conditions))
		}
	})

	t.Run("store_operations", func(t *testing.T) {
		// Insert events
		_, err := eventStore.InsertEvent(&store.Event{
			Timestamp:   time.Now(),
			CameraID:    "front-door",
			WatchID:     "w_123",
			ConditionID: "person",
			Answer:      "No person visible",
			Triggered:   false,
			LatencyMs:   250,
			FramesUsed:  4,
		})
		if err != nil {
			t.Fatal(err)
		}

		// Insert alert
		eventID, err := eventStore.InsertAlert(&store.Event{
			Timestamp:   time.Now(),
			CameraID:    "front-door",
			WatchID:     "w_123",
			ConditionID: "person",
			Answer:      "Yes, a person at the door",
			Triggered:   true,
			LatencyMs:   242,
			FramesUsed:  4,
		})
		if err != nil {
			t.Fatal(err)
		}
		if eventID <= 0 {
			t.Error("expected positive eventID")
		}

		// Query stats
		stats, err := eventStore.GetStats()
		if err != nil {
			t.Fatal(err)
		}
		if stats.TotalEvents != 2 {
			t.Errorf("total_events = %d, want 2", stats.TotalEvents)
		}
		if stats.TotalAlerts != 1 {
			t.Errorf("total_alerts = %d, want 1", stats.TotalAlerts)
		}

		// Query recent alerts
		alerts, err := eventStore.RecentAlerts(10)
		if err != nil {
			t.Fatal(err)
		}
		if len(alerts) != 1 {
			t.Errorf("recent_alerts = %d, want 1", len(alerts))
		}
		if alerts[0].Answer != "Yes, a person at the door" {
			t.Errorf("answer = %s", alerts[0].Answer)
		}

		// Query by date
		todayEvents, err := eventStore.EventsByDate(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if len(todayEvents) != 2 {
			t.Errorf("today_events = %d, want 2", len(todayEvents))
		}

		// Alert count by camera
		now := time.Now()
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		counts, err := eventStore.AlertCountByCamera(dayStart, dayStart.Add(24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if counts["front-door"] != 1 {
			t.Errorf("front-door alerts = %d, want 1", counts["front-door"])
		}
	})

	t.Run("notification_dispatch", func(t *testing.T) {
		// Verify actions are registered
		actions := dispatcher.GetActions("front-door", "person")
		if len(actions) != 2 {
			t.Errorf("front-door/person actions = %d, want 2", len(actions))
		}

		actions = dispatcher.GetActions("garage", "car")
		if len(actions) != 1 || actions[0] != "slack" {
			t.Errorf("garage/car actions = %v, want [slack]", actions)
		}

		// No actions for unconfigured conditions
		actions = dispatcher.GetActions("front-door", "nonexistent")
		if len(actions) != 0 {
			t.Errorf("nonexistent actions = %v, want []", actions)
		}

		// Dispatch a notification
		before := atomic.LoadInt32(&webhookCalls)
		dispatcher.DispatchForCondition(context.Background(), "front-door", "person", notify.Alert{
			CameraID:    "front-door",
			CameraName:  "Front Door",
			ConditionID: "person",
			Answer:      "Person detected",
			Timestamp:   time.Now(),
		})
		time.Sleep(100 * time.Millisecond) // wait for async dispatch
		after := atomic.LoadInt32(&webhookCalls)
		if after <= before {
			t.Error("webhook was not called after dispatch")
		}
	})

	t.Run("api_healthz", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/healthz", nil)
		w := httptest.NewRecorder()
		apiSrv.Handler().ServeHTTP(w, req)

		if w.Code != 200 {
			t.Errorf("status = %d", w.Code)
		}
	})

	t.Run("api_cameras_masked", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/cameras", nil)
		w := httptest.NewRecorder()
		apiSrv.Handler().ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}

		body := w.Body.String()
		if strings.Contains(body, "admin:pass") {
			t.Error("credentials leaked in /api/cameras response")
		}
		if !strings.Contains(body, "***:***") {
			t.Error("credentials not masked in /api/cameras response")
		}
		// Second camera has no auth — should be unchanged
		if !strings.Contains(body, "192.168.1.20:554") {
			t.Error("non-auth camera source missing")
		}
	})

	t.Run("api_status", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/status", nil)
		w := httptest.NewRecorder()
		apiSrv.Handler().ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}

		var status map[string]any
		json.NewDecoder(w.Body).Decode(&status)

		if status["cameras"].(float64) != 2 {
			t.Errorf("cameras = %v", status["cameras"])
		}
		if status["total_events"].(float64) != 2 {
			t.Errorf("total_events = %v", status["total_events"])
		}
	})

	t.Run("api_alerts_recent", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/alerts/recent?limit=5", nil)
		w := httptest.NewRecorder()
		apiSrv.Handler().ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}

		var alerts []map[string]any
		json.NewDecoder(w.Body).Decode(&alerts)
		if len(alerts) != 1 {
			t.Errorf("alerts = %d, want 1", len(alerts))
		}
	})

	t.Run("api_stats", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/stats", nil)
		w := httptest.NewRecorder()
		apiSrv.Handler().ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}

		var stats map[string]any
		json.NewDecoder(w.Body).Decode(&stats)
		if stats["total_events"].(float64) != 2 {
			t.Errorf("total_events = %v", stats["total_events"])
		}
		if stats["total_alerts"].(float64) != 1 {
			t.Errorf("total_alerts = %v", stats["total_alerts"])
		}
		// today_by_camera should exist
		byCam, ok := stats["today_by_camera"].(map[string]any)
		if !ok {
			t.Error("today_by_camera missing or wrong type")
		} else if byCam["front-door"].(float64) != 1 {
			t.Errorf("today front-door = %v", byCam["front-door"])
		}
	})

	t.Run("api_events_by_date", func(t *testing.T) {
		date := time.Now().Format("2006-01-02")
		req := httptest.NewRequest("GET", "/api/events?date="+date, nil)
		w := httptest.NewRecorder()
		apiSrv.Handler().ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}

		var events []map[string]any
		json.NewDecoder(w.Body).Decode(&events)
		if len(events) != 2 {
			t.Errorf("events = %d, want 2", len(events))
		}
	})

	t.Run("api_events_filter_camera", func(t *testing.T) {
		date := time.Now().Format("2006-01-02")
		req := httptest.NewRequest("GET", "/api/events?date="+date+"&camera=garage", nil)
		w := httptest.NewRecorder()
		apiSrv.Handler().ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}

		var events []map[string]any
		json.NewDecoder(w.Body).Decode(&events)
		if len(events) != 0 {
			t.Errorf("garage events = %d, want 0", len(events))
		}
	})

	t.Run("api_bad_date", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/events?date=not-a-date", nil)
		w := httptest.NewRecorder()
		apiSrv.Handler().ServeHTTP(w, req)

		if w.Code != 400 {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})

	t.Run("api_watches_empty", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/watches", nil)
		w := httptest.NewRecorder()
		apiSrv.Handler().ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}

		var watches []any
		json.NewDecoder(w.Body).Decode(&watches)
		if len(watches) != 0 {
			t.Errorf("watches = %d, want 0 (not started yet)", len(watches))
		}
	})

}

// =============================================================================
// Smoke Test: Digest end-to-end
// =============================================================================

func TestSmoke_Digest(t *testing.T) {
	tmpDir := t.TempDir()

	// Open store and insert alerts
	eventStore, err := store.Open(filepath.Join(tmpDir, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer eventStore.Close()

	now := time.Now()
	for i := 0; i < 5; i++ {
		eventStore.InsertAlert(&store.Event{
			Timestamp:   now.Add(-time.Duration(i) * 10 * time.Minute),
			CameraID:    "front-door",
			ConditionID: "person",
			Answer:      fmt.Sprintf("Alert %d: person at door", i+1),
			Triggered:   true,
		})
	}
	eventStore.InsertAlert(&store.Event{
		Timestamp:   now.Add(-30 * time.Minute),
		CameraID:    "garage",
		ConditionID: "car",
		Answer:      "Garage door open",
		Triggered:   true,
	})

	// Mock LLM server
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request format
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		messages, ok := req["messages"].([]any)
		if !ok || len(messages) == 0 {
			t.Error("LLM request missing messages")
			w.WriteHeader(400)
			return
		}

		msg := messages[0].(map[string]any)
		prompt := msg["content"].(string)

		// Verify prompt contains our alerts
		if !strings.Contains(prompt, "front-door") {
			t.Error("prompt missing front-door camera")
		}
		if !strings.Contains(prompt, "person") {
			t.Error("prompt missing person condition")
		}

		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{
					"content": "Today: 5 person detections at front door, 1 garage door alert. Peak activity around morning hours.",
				}},
			},
		})
	}))
	defer llmServer.Close()

	// Setup dispatcher with mock notifier
	var digestSent atomic.Int32
	var digestContent string
	dispatcher := notify.NewDispatcher()
	dispatcher.Register(&mockNotifier{
		name: "test",
		onSend: func(a notify.Alert) {
			digestSent.Add(1)
			digestContent = a.Answer
		},
	})

	runner := digest.NewRunner(eventStore, dispatcher, digest.Config{
		LLM:    "local",
		LLMURL: llmServer.URL,
		PushTo: []string{"test"},
	})

	t.Run("digest_with_alerts", func(t *testing.T) {
		err := runner.RunOnce(context.Background(), now)
		if err != nil {
			t.Fatal(err)
		}

		time.Sleep(100 * time.Millisecond)
		if digestSent.Load() != 1 {
			t.Errorf("digest sent = %d, want 1", digestSent.Load())
		}
		if !strings.Contains(digestContent, "person") {
			t.Errorf("digest content missing person: %s", digestContent)
		}
	})

	t.Run("digest_no_alerts_different_day", func(t *testing.T) {
		digestSent.Store(0)
		// Query a day with no alerts
		err := runner.RunOnce(context.Background(), now.Add(-30*24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}

		time.Sleep(100 * time.Millisecond)
		if digestSent.Load() != 1 {
			t.Errorf("digest sent = %d, want 1 (no-alerts message)", digestSent.Load())
		}
	})
}

// =============================================================================
// Smoke Test: SSE client parsing
// =============================================================================

func TestSmoke_SSEClient(t *testing.T) {
	// Mock trio-core SSE server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/watch" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
			w.WriteHeader(405)
			return
		}

		// Verify request body
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		if req["source"] == nil {
			t.Error("request missing source")
		}
		if req["conditions"] == nil {
			t.Error("request missing conditions")
		}

		// Return SSE stream
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(200)

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer doesn't support flushing")
		}

		// Send status event
		fmt.Fprintf(w, "event: status\ndata: {\"watch_id\":\"w_test\",\"state\":\"running\"}\n\n")
		flusher.Flush()

		// Send result event
		fmt.Fprintf(w, "event: result\ndata: {\"watch_id\":\"w_test\",\"ts\":\"2026-03-09T21:01:30Z\",\"conditions\":[{\"id\":\"person\",\"triggered\":false,\"answer\":\"No\"}],\"metrics\":{\"latency_ms\":250,\"tok_s\":70,\"frames_analyzed\":4}}\n\n")
		flusher.Flush()

		// Send alert event with frame
		fakeJPEG := base64.StdEncoding.EncodeToString([]byte("fake-jpeg-data"))
		fmt.Fprintf(w, "event: alert\ndata: {\"watch_id\":\"w_test\",\"ts\":\"2026-03-09T21:05:12Z\",\"conditions\":[{\"id\":\"person\",\"triggered\":true,\"answer\":\"Yes, person at door\"}],\"metrics\":{\"latency_ms\":242,\"tok_s\":71,\"frames_analyzed\":4},\"frame_b64\":\"%s\"}\n\n", fakeJPEG)
		flusher.Flush()

		// Keep connection open briefly then close
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	client := triocore.NewClient(server.URL)

	t.Run("start_watch", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var gotStatus, gotResult, gotAlert atomic.Bool

		err := client.StartWatch(ctx, triocore.WatchRequest{
			Source: "rtsp://test:test@192.168.1.1:554/stream",
			Conditions: []triocore.WatchCondition{
				{ID: "person", Question: "Is there a person?"},
			},
			FPS:    1,
			Stream: true,
		}, func(event triocore.SSEEvent) {
			switch event.Type {
			case "status":
				gotStatus.Store(true)
				var s triocore.StatusEvent
				json.Unmarshal(event.Data, &s)
				if s.WatchID != "w_test" {
					t.Errorf("watchID = %s", s.WatchID)
				}
			case "result":
				gotResult.Store(true)
				var r triocore.ResultEvent
				json.Unmarshal(event.Data, &r)
				if len(r.Conditions) == 0 {
					t.Error("result has no conditions")
				} else if r.Conditions[0].Answer != "No" {
					t.Errorf("answer = %s", r.Conditions[0].Answer)
				}
			case "alert":
				gotAlert.Store(true)
				var a triocore.AlertEvent
				json.Unmarshal(event.Data, &a)
				if len(a.Conditions) == 0 {
					t.Error("alert has no conditions")
				} else if !a.Conditions[0].Triggered {
					t.Error("alert condition not triggered")
				}
				if a.FrameB64 == "" {
					t.Error("alert missing frame")
				}
			}
		})
		// StartWatch blocks until stream ends or ctx cancelled — err may be nil or context
		if err != nil && ctx.Err() == nil {
			t.Logf("StartWatch returned: %v", err)
		}

		if !gotStatus.Load() {
			t.Error("never received status event")
		}
		if !gotResult.Load() {
			t.Error("never received result event")
		}
		if !gotAlert.Load() {
			t.Error("never received alert event")
		}
	})
}

// =============================================================================
// Smoke Test: Watch manager with mock trio-core
// =============================================================================

func TestSmoke_WatchManager(t *testing.T) {
	// SSE server that sends one alert then closes
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/watch" && r.Method == "POST" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			flusher := w.(http.Flusher)

			fmt.Fprintf(w, "event: status\ndata: {\"watch_id\":\"w_mgr\",\"state\":\"running\"}\n\n")
			flusher.Flush()

			fmt.Fprintf(w, "event: result\ndata: {\"watch_id\":\"w_mgr\",\"ts\":\"%s\",\"conditions\":[{\"id\":\"person\",\"triggered\":false,\"answer\":\"No\"}],\"metrics\":{\"latency_ms\":200}}\n\n", time.Now().UTC().Format(time.RFC3339))
			flusher.Flush()

			fmt.Fprintf(w, "event: alert\ndata: {\"watch_id\":\"w_mgr\",\"ts\":\"%s\",\"conditions\":[{\"id\":\"person\",\"triggered\":true,\"answer\":\"Person!\"}],\"metrics\":{\"latency_ms\":210}}\n\n", time.Now().UTC().Format(time.RFC3339))
			flusher.Flush()

			time.Sleep(200 * time.Millisecond)
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	cameras := []config.CameraConfig{
		{
			ID:     "test-cam",
			Name:   "Test Camera",
			Source: "rtsp://fake/stream",
			FPS:    1,
			Conditions: []config.ConditionConfig{
				{ID: "person", Question: "Is there a person?"},
			},
		},
	}

	client := triocore.NewClient(server.URL)
	mgr := triocore.NewManager(client, cameras)

	var resultCount, alertCount atomic.Int32
	mgr.OnResult(func(cameraID string, result triocore.ResultEvent) {
		resultCount.Add(1)
		if cameraID != "test-cam" {
			t.Errorf("result cameraID = %s", cameraID)
		}
	})
	mgr.OnAlert(func(cameraID string, alert triocore.AlertEvent) {
		alertCount.Add(1)
		if cameraID != "test-cam" {
			t.Errorf("alert cameraID = %s", cameraID)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go mgr.Run(ctx)

	// Wait for events to propagate
	time.Sleep(1 * time.Second)

	if resultCount.Load() == 0 {
		t.Error("no results received from watch manager")
	}
	if alertCount.Load() == 0 {
		t.Error("no alerts received from watch manager")
	}

	// Check status
	entries := mgr.Status()
	// May or may not still be running depending on timing
	t.Logf("active watches: %d", len(entries))

	cancel()
}

// =============================================================================
// Smoke Test: Config round-trip
// =============================================================================

func TestSmoke_ConfigRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	// Create config
	cfg := &config.Config{
		TrioCore: config.TrioCoreConfig{URL: "http://localhost:8000"},
		Cameras: []config.CameraConfig{
			{
				ID:     "front-door",
				Name:   "Front Door",
				Source: "rtsp://admin:pass@192.168.1.10:554/stream",
				FPS:    1,
				Conditions: []config.ConditionConfig{
					{ID: "person", Question: "Is there a person?", Actions: []string{"telegram", "webhook"}},
				},
			},
		},
		Notifications: config.NotificationConfig{
			Telegram: &config.TelegramConfig{BotToken: "123:ABC", ChatID: "-100123"},
			Webhook:  &config.WebhookConfig{URL: "https://example.com/hook", Headers: map[string]string{"X-Auth": "secret"}},
		},
		Digest: config.DigestConfig{Enabled: true, Schedule: "0 22 * * *", LLM: "local", PushTo: []string{"telegram"}},
	}

	// Save
	if err := cfg.SaveTo(cfgPath); err != nil {
		t.Fatal(err)
	}

	// Load back
	loaded, err := config.LoadFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	// Verify round-trip
	if loaded.TrioCore.URL != cfg.TrioCore.URL {
		t.Errorf("trio_core.url = %s", loaded.TrioCore.URL)
	}
	if len(loaded.Cameras) != 1 {
		t.Fatalf("cameras = %d", len(loaded.Cameras))
	}
	if loaded.Cameras[0].Source != cfg.Cameras[0].Source {
		t.Errorf("source = %s", loaded.Cameras[0].Source)
	}
	if loaded.Cameras[0].Conditions[0].Actions[0] != "telegram" {
		t.Errorf("action = %s", loaded.Cameras[0].Conditions[0].Actions[0])
	}
	if loaded.Notifications.Telegram == nil {
		t.Error("telegram config lost")
	}
	if loaded.Notifications.Webhook.Headers["X-Auth"] != "secret" {
		t.Error("webhook headers lost")
	}
	if !loaded.Digest.Enabled {
		t.Error("digest.enabled lost")
	}
	if loaded.Digest.LLM != "local" {
		t.Errorf("digest.llm = %s", loaded.Digest.LLM)
	}

	// Camera management
	if err := loaded.AddCamera(config.CameraConfig{ID: "garage", Name: "Garage", Source: "rtsp://192.168.1.20:554/stream"}); err != nil {
		t.Fatal(err)
	}
	if len(loaded.Cameras) != 2 {
		t.Errorf("cameras after add = %d", len(loaded.Cameras))
	}

	// Duplicate add should fail
	if err := loaded.AddCamera(config.CameraConfig{ID: "garage"}); err == nil {
		t.Error("duplicate add should fail")
	}

	// Remove
	if err := loaded.RemoveCamera("garage"); err != nil {
		t.Fatal(err)
	}
	if len(loaded.Cameras) != 1 {
		t.Errorf("cameras after remove = %d", len(loaded.Cameras))
	}

	// Remove nonexistent should fail
	if err := loaded.RemoveCamera("nonexistent"); err == nil {
		t.Error("remove nonexistent should fail")
	}
}

// =============================================================================
// Smoke Test: Notification channels
// =============================================================================

func TestSmoke_Notifications(t *testing.T) {
	var webhookBody []byte
	var slackBody []byte
	var telegramPath string

	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer webhookSrv.Close()

	slackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slackBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer slackSrv.Close()

	telegramSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		telegramPath = r.URL.Path
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer telegramSrv.Close()

	dispatcher := notify.NewDispatcher()
	dispatcher.Register(notify.NewWebhook(webhookSrv.URL, map[string]string{"X-Custom": "test"}))
	dispatcher.Register(notify.NewSlack(slackSrv.URL))

	alert := notify.Alert{
		CameraID:    "front-door",
		CameraName:  "Front Door",
		ConditionID: "person",
		Answer:      "Person detected at the front door",
		Timestamp:   time.Now(),
		FrameJPEG:   []byte("fake-jpeg"),
	}

	t.Run("webhook", func(t *testing.T) {
		dispatcher.Dispatch(context.Background(), []string{"webhook"}, alert)
		time.Sleep(100 * time.Millisecond)

		if len(webhookBody) == 0 {
			t.Fatal("webhook not called")
		}

		var payload map[string]any
		json.Unmarshal(webhookBody, &payload)
		if payload["camera_id"] != "front-door" {
			t.Errorf("camera_id = %v", payload["camera_id"])
		}
		if payload["answer"] != "Person detected at the front door" {
			t.Errorf("answer = %v", payload["answer"])
		}
		// Should include base64 frame
		if payload["frame_b64"] == nil || payload["frame_b64"] == "" {
			t.Error("webhook missing frame_b64")
		}
	})

	t.Run("slack", func(t *testing.T) {
		dispatcher.Dispatch(context.Background(), []string{"slack"}, alert)
		time.Sleep(100 * time.Millisecond)

		if len(slackBody) == 0 {
			t.Fatal("slack not called")
		}

		var payload map[string]string
		json.Unmarshal(slackBody, &payload)
		if payload["text"] == "" {
			t.Error("slack text empty")
		}
		if !strings.Contains(payload["text"], "Front Door") {
			t.Errorf("slack text missing camera name: %s", payload["text"])
		}
	})

	t.Run("telegram_text", func(t *testing.T) {
		// Telegram needs a real URL format, test just the notifier creation
		tg := notify.NewTelegram("TEST_TOKEN", "-100123")
		if tg.Name() != "telegram" {
			t.Errorf("name = %s", tg.Name())
		}
		_ = telegramSrv
		_ = telegramPath
	})

	t.Run("unknown_action", func(t *testing.T) {
		// Should not panic
		dispatcher.Dispatch(context.Background(), []string{"nonexistent"}, alert)
	})
}

// =============================================================================
// Smoke Test: CLI commands exist
// =============================================================================

func TestSmoke_CLI(t *testing.T) {
	binPath := buildTrioclaw(t)

	t.Run("run_help_has_listen_flag", func(t *testing.T) {
		stdout, _, err := runCommand(t, binPath, "run", "--help")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout, "--listen") {
			t.Error("run --help missing --listen flag")
		}
		if !strings.Contains(stdout, "--config") {
			t.Error("run --help missing --config flag")
		}
		if !strings.Contains(stdout, "--trio-api") {
			t.Error("run --help missing --trio-api flag")
		}
		if !strings.Contains(stdout, "--ha-url") {
			t.Error("run --help missing --ha-url flag")
		}
	})

	t.Run("camera_subcommands", func(t *testing.T) {
		stdout, _, err := runCommand(t, binPath, "camera", "--help")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout, "add") {
			t.Error("camera --help missing 'add'")
		}
		if !strings.Contains(stdout, "remove") {
			t.Error("camera --help missing 'remove'")
		}
		if !strings.Contains(stdout, "list") {
			t.Error("camera --help missing 'list'")
		}
	})

	t.Run("camera_add_flags", func(t *testing.T) {
		stdout, _, err := runCommand(t, binPath, "camera", "add", "--help")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout, "--id") {
			t.Error("camera add missing --id")
		}
		if !strings.Contains(stdout, "--source") {
			t.Error("camera add missing --source")
		}
		if !strings.Contains(stdout, "--question") {
			t.Error("camera add missing --question")
		}
	})

	t.Run("status_command", func(t *testing.T) {
		stdout, _, err := runCommand(t, binPath, "status")
		// May succeed or fail depending on local state, just check it runs
		if err != nil {
			t.Logf("status exit error (expected if no config): %v", err)
		}
		// Should at least output something
		if stdout == "" && err == nil {
			t.Error("status produced no output")
		}
	})

	t.Run("version", func(t *testing.T) {
		stdout, _, err := runCommand(t, binPath, "version")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout, "TrioClaw version") {
			t.Error("version output wrong")
		}
	})
}

// =============================================================================
// Helpers
// =============================================================================

func newMockTrioCore(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/watch" && r.Method == "POST":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			flusher := w.(http.Flusher)
			fmt.Fprintf(w, "event: status\ndata: {\"watch_id\":\"w_mock\",\"state\":\"running\"}\n\n")
			flusher.Flush()
			time.Sleep(100 * time.Millisecond)
		case r.URL.Path == "/v1/watch" && r.Method == "GET":
			json.NewEncoder(w).Encode([]any{})
		case r.URL.Path == "/healthz":
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
}

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
