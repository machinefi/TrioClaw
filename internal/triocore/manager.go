// manager.go manages multiple concurrent watches — one per camera.
//
// It reads CameraConfig entries, starts a watch for each, handles
// reconnection, and routes SSE events to registered listeners.
package triocore

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/machinefi/trioclaw/internal/config"
)

const (
	// reconnectDelay between reconnection attempts
	reconnectDelay = 5 * time.Second

	// maxReconnectDelay caps the backoff
	maxReconnectDelay = 60 * time.Second
)

// AlertListener is called when a condition triggers on any camera.
type AlertListener func(cameraID string, alert AlertEvent)

// ResultListener is called on every inference result from any camera.
type ResultListener func(cameraID string, result ResultEvent)

// Manager coordinates watches across multiple cameras.
type Manager struct {
	client    *Client
	cameras   []config.CameraConfig

	alertListeners  []AlertListener
	resultListeners []ResultListener
	mu              sync.Mutex

	// Track active watch IDs per camera
	watchIDs map[string]string // cameraID -> watchID
	watchMu  sync.Mutex
}

// NewManager creates a watch manager.
func NewManager(client *Client, cameras []config.CameraConfig) *Manager {
	return &Manager{
		client:   client,
		cameras:  cameras,
		watchIDs: make(map[string]string),
	}
}

// OnAlert registers a listener for alert events.
func (m *Manager) OnAlert(fn AlertListener) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alertListeners = append(m.alertListeners, fn)
}

// OnResult registers a listener for result events.
func (m *Manager) OnResult(fn ResultListener) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resultListeners = append(m.resultListeners, fn)
}

// Run starts watches for all configured cameras and blocks until ctx is cancelled.
// Each camera runs in its own goroutine with automatic reconnection.
func (m *Manager) Run(ctx context.Context) error {
	if len(m.cameras) == 0 {
		log.Println("[manager] no cameras configured")
		<-ctx.Done()
		return nil
	}

	var wg sync.WaitGroup

	for _, cam := range m.cameras {
		wg.Add(1)
		go func(cam config.CameraConfig) {
			defer wg.Done()
			m.runCamera(ctx, cam)
		}(cam)
	}

	wg.Wait()
	return nil
}

// runCamera maintains a persistent watch for a single camera with reconnection.
func (m *Manager) runCamera(ctx context.Context, cam config.CameraConfig) {
	delay := reconnectDelay

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		log.Printf("[watch:%s] connecting to trio-core for %s", cam.ID, cam.Source)

		// Build conditions
		conditions := make([]WatchCondition, len(cam.Conditions))
		for i, cond := range cam.Conditions {
			conditions[i] = WatchCondition{
				ID:       cond.ID,
				Question: cond.Question,
			}
		}

		req := WatchRequest{
			Source:     cam.Source,
			Conditions: conditions,
			FPS:       cam.FPS,
		}

		err := m.client.StartWatch(ctx, req, func(event SSEEvent) {
			m.handleEvent(cam.ID, event)
		})

		if ctx.Err() != nil {
			return // shutting down
		}

		if err != nil {
			log.Printf("[watch:%s] error: %v, reconnecting in %v", cam.ID, err, delay)
		} else {
			// Stream ran successfully then ended — reset backoff
			log.Printf("[watch:%s] stream ended, reconnecting in %v", cam.ID, reconnectDelay)
			delay = reconnectDelay
		}

		// Wait before reconnecting
		select {
		case <-time.After(delay):
			delay = time.Duration(float64(delay) * 1.5)
			if delay > maxReconnectDelay {
				delay = maxReconnectDelay
			}
		case <-ctx.Done():
			return
		}
	}
}

// handleEvent dispatches SSE events to registered listeners.
func (m *Manager) handleEvent(cameraID string, event SSEEvent) {
	switch event.Type {
	case "status":
		var status StatusEvent
		if err := json.Unmarshal(event.Data, &status); err != nil {
			log.Printf("[watch:%s] failed to parse status: %v", cameraID, err)
			return
		}
		// Track watch ID
		m.watchMu.Lock()
		m.watchIDs[cameraID] = status.WatchID
		m.watchMu.Unlock()

		log.Printf("[watch:%s] status: %s (watch_id=%s)", cameraID, status.State, status.WatchID)

	case "result":
		var result ResultEvent
		if err := json.Unmarshal(event.Data, &result); err != nil {
			log.Printf("[watch:%s] failed to parse result: %v", cameraID, err)
			return
		}

		m.mu.Lock()
		listeners := make([]ResultListener, len(m.resultListeners))
		copy(listeners, m.resultListeners)
		m.mu.Unlock()

		for _, fn := range listeners {
			fn(cameraID, result)
		}

	case "alert":
		var alert AlertEvent
		if err := json.Unmarshal(event.Data, &alert); err != nil {
			log.Printf("[watch:%s] failed to parse alert: %v", cameraID, err)
			return
		}

		log.Printf("[watch:%s] ALERT: %s", cameraID, formatTriggered(alert.Conditions))

		m.mu.Lock()
		listeners := make([]AlertListener, len(m.alertListeners))
		copy(listeners, m.alertListeners)
		m.mu.Unlock()

		for _, fn := range listeners {
			fn(cameraID, alert)
		}

	default:
		log.Printf("[watch:%s] unknown event type: %s", cameraID, event.Type)
	}
}

// GetWatchID returns the active watch ID for a camera.
func (m *Manager) GetWatchID(cameraID string) string {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	return m.watchIDs[cameraID]
}

// ActiveWatches returns all camera IDs with active watches.
func (m *Manager) ActiveWatches() map[string]string {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	result := make(map[string]string, len(m.watchIDs))
	for k, v := range m.watchIDs {
		result[k] = v
	}
	return result
}

func formatTriggered(conditions []ConditionResult) string {
	var triggered []string
	for _, c := range conditions {
		if c.Triggered {
			triggered = append(triggered, fmt.Sprintf("%s: %s", c.ID, c.Answer))
		}
	}
	if len(triggered) == 0 {
		return "(no conditions triggered)"
	}
	return fmt.Sprintf("%v", triggered)
}
