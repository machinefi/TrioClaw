// Package triocore implements an SSE client for trio-core's /v1/watch API.
//
// It connects to a local trio-core server, sends camera RTSP URLs and
// conditions, and receives a stream of SSE events:
//   - event: status  — watch state changes (connecting, running)
//   - event: result  — periodic inference results (every few seconds)
//   - event: alert   — triggered condition with frame
//
// The client handles reconnection automatically.
package triocore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// WatchRequest is sent to POST /v1/watch to start monitoring.
type WatchRequest struct {
	Source     string             `json:"source"`     // RTSP URL or device path
	Conditions []WatchCondition  `json:"conditions"` // what to watch for
	FPS       int                `json:"fps"`        // max check rate hint
	Stream    bool               `json:"stream"`     // always true for SSE
}

// WatchCondition is a single condition to monitor.
type WatchCondition struct {
	ID       string `json:"id"`
	Question string `json:"question"`
}

// SSEEvent represents a parsed SSE event from trio-core.
type SSEEvent struct {
	Type string          // "status", "result", "alert"
	Data json.RawMessage // raw JSON payload
}

// StatusEvent is the payload for "status" events.
type StatusEvent struct {
	WatchID    string `json:"watch_id"`
	State      string `json:"state"`      // "connecting", "running", "stopped", "error"
	Resolution string `json:"resolution,omitempty"`
	Model      string `json:"model,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ResultEvent is the payload for "result" events (periodic inference).
type ResultEvent struct {
	WatchID    string             `json:"watch_id"`
	Timestamp  string             `json:"ts"`
	Conditions []ConditionResult  `json:"conditions"`
	Metrics    InferenceMetrics   `json:"metrics"`
}

// AlertEvent is the payload for "alert" events (triggered condition).
type AlertEvent struct {
	WatchID    string             `json:"watch_id"`
	Timestamp  string             `json:"ts"`
	Conditions []ConditionResult  `json:"conditions"`
	Metrics    InferenceMetrics   `json:"metrics"`
	FrameB64   string             `json:"frame_b64,omitempty"` // base64 JPEG of triggering frame
}

// ConditionResult is the result for a single condition.
type ConditionResult struct {
	ID        string `json:"id"`
	Triggered bool   `json:"triggered"`
	Answer    string `json:"answer"`
}

// InferenceMetrics from trio-core.
type InferenceMetrics struct {
	LatencyMs      float64 `json:"latency_ms"`
	TokensPerSec   float64 `json:"tok_s"`
	FramesAnalyzed int     `json:"frames_analyzed"`
}

// WatchInfo describes an active watch (from GET /v1/watch).
type WatchInfo struct {
	WatchID    string            `json:"watch_id"`
	Source     string            `json:"source"`
	State      string            `json:"state"`
	Conditions []WatchCondition  `json:"conditions"`
	UptimeS    int               `json:"uptime_s"`
	Checks     int               `json:"checks"`
	Alerts     int               `json:"alerts"`
}

// EventHandler is called for each SSE event received from trio-core.
type EventHandler func(event SSEEvent)

// Client connects to a trio-core server.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a trio-core client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			// No timeout — SSE streams are long-lived.
			// Individual requests use context deadlines.
		},
	}
}

// StartWatch sends POST /v1/watch and streams SSE events to the handler.
// Blocks until the context is cancelled or the stream ends.
// Returns nil on graceful shutdown (context cancelled).
func (c *Client) StartWatch(ctx context.Context, req WatchRequest, handler EventHandler) error {
	req.Stream = true

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal watch request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/watch", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil // graceful shutdown
		}
		return fmt.Errorf("connect to trio-core: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("trio-core returned %d: %s", resp.StatusCode, string(respBody))
	}

	return c.readSSE(ctx, resp.Body, handler)
}

// ListWatches returns all active watches from GET /v1/watch.
func (c *Client) ListWatches(ctx context.Context) ([]WatchInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/v1/watch", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list watches: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list watches: HTTP %d", resp.StatusCode)
	}

	var watches []WatchInfo
	if err := json.NewDecoder(resp.Body).Decode(&watches); err != nil {
		return nil, fmt.Errorf("decode watches: %w", err)
	}
	return watches, nil
}

// StopWatch sends DELETE /v1/watch/{watchID}.
func (c *Client) StopWatch(ctx context.Context, watchID string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", c.baseURL+"/v1/watch/"+watchID, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("stop watch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stop watch: HTTP %d", resp.StatusCode)
	}
	return nil
}

// HealthCheck tests connectivity to trio-core via GET /healthz.
func (c *Client) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/healthz", nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("trio-core health check: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("trio-core returned %d", resp.StatusCode)
	}
	return nil
}

// readSSE parses an SSE stream and dispatches events to the handler.
func (c *Client) readSSE(ctx context.Context, r io.Reader, handler EventHandler) error {
	scanner := bufio.NewScanner(r)
	// SSE can have large data fields (base64 frames)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var eventType string
	var dataLines []string

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		line := scanner.Text()

		if line == "" {
			// Empty line = end of event
			if eventType != "" && len(dataLines) > 0 {
				data := strings.Join(dataLines, "\n")
				handler(SSEEvent{
					Type: eventType,
					Data: json.RawMessage(data),
				})
			}
			eventType = ""
			dataLines = nil
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		} else if strings.HasPrefix(line, ":") {
			// SSE comment, ignore
		}
	}

	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("SSE read error: %w", err)
	}

	return nil // stream ended
}
