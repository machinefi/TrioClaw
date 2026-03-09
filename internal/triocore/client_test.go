package triocore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStartWatch_SSEParsing(t *testing.T) {
	// Mock trio-core server that sends SSE events
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/watch" {
			http.NotFound(w, r)
			return
		}
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}

		// Verify request body
		var req WatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if req.Source == "" {
			http.Error(w, "source required", 400)
			return
		}

		// Send SSE events
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(200)

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", 500)
			return
		}

		// Status event
		fmt.Fprintf(w, "event: status\ndata: {\"watch_id\":\"w_test\",\"state\":\"running\"}\n\n")
		flusher.Flush()

		// Result event
		fmt.Fprintf(w, "event: result\ndata: {\"watch_id\":\"w_test\",\"ts\":\"2026-03-08T21:01:30Z\",\"conditions\":[{\"id\":\"person\",\"triggered\":false,\"answer\":\"No\"}],\"metrics\":{\"latency_ms\":150,\"tok_s\":70,\"frames_analyzed\":4}}\n\n")
		flusher.Flush()

		// Alert event
		fmt.Fprintf(w, "event: alert\ndata: {\"watch_id\":\"w_test\",\"ts\":\"2026-03-08T21:05:12Z\",\"conditions\":[{\"id\":\"person\",\"triggered\":true,\"answer\":\"Yes, a person\"}],\"metrics\":{\"latency_ms\":200,\"tok_s\":65,\"frames_analyzed\":4}}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewClient(server.URL)

	var events []SSEEvent
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := client.StartWatch(ctx, WatchRequest{
		Source:     "rtsp://test",
		Conditions: []WatchCondition{{ID: "person", Question: "Is there a person?"}},
		FPS:       1,
	}, func(event SSEEvent) {
		events = append(events, event)
	})

	if err != nil {
		t.Fatal(err)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	// Check status event
	if events[0].Type != "status" {
		t.Errorf("event[0] type = %s, want status", events[0].Type)
	}
	var status StatusEvent
	json.Unmarshal(events[0].Data, &status)
	if status.WatchID != "w_test" {
		t.Errorf("watch_id = %s, want w_test", status.WatchID)
	}

	// Check result event
	if events[1].Type != "result" {
		t.Errorf("event[1] type = %s, want result", events[1].Type)
	}
	var result ResultEvent
	json.Unmarshal(events[1].Data, &result)
	if result.Conditions[0].Triggered {
		t.Error("expected triggered=false for result")
	}

	// Check alert event
	if events[2].Type != "alert" {
		t.Errorf("event[2] type = %s, want alert", events[2].Type)
	}
	var alert AlertEvent
	json.Unmarshal(events[2].Data, &alert)
	if !alert.Conditions[0].Triggered {
		t.Error("expected triggered=true for alert")
	}
	if alert.Conditions[0].Answer != "Yes, a person" {
		t.Errorf("answer = %s", alert.Conditions[0].Answer)
	}
}

func TestListWatches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/watch" && r.Method == "GET" {
			json.NewEncoder(w).Encode([]WatchInfo{
				{WatchID: "w_1", Source: "rtsp://test", State: "running", Checks: 100, Alerts: 3},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	watches, err := client.ListWatches(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(watches) != 1 {
		t.Fatalf("expected 1 watch, got %d", len(watches))
	}
	if watches[0].Checks != 100 {
		t.Errorf("checks = %d, want 100", watches[0].Checks)
	}
}

func TestStopWatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/watch/w_1" && r.Method == "DELETE" {
			json.NewEncoder(w).Encode(map[string]any{"status": "stopped"})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	if err := client.StopWatch(context.Background(), "w_1"); err != nil {
		t.Fatal(err)
	}
}

func TestHealthCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(200)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	if err := client.HealthCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
}
