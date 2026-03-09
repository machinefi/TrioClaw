// manager.go manages multiple concurrent watches — one per camera.
//
// Supports both static watches (from config at startup) and dynamic watches
// (added at runtime via gateway invoke commands).
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
	reconnectDelay    = 5 * time.Second
	maxReconnectDelay = 60 * time.Second
)

// AlertListener is called when a condition triggers on any camera.
type AlertListener func(cameraID string, alert AlertEvent)

// ResultListener is called on every inference result from any camera.
type ResultListener func(cameraID string, result ResultEvent)

// WatchEntry describes an active watch for status reporting.
type WatchEntry struct {
	CameraID string
	WatchID  string
	Source   string
	State    string // "running", "connecting", "stopped"
}

// Manager coordinates watches across multiple cameras.
type Manager struct {
	client  *Client
	cameras []config.CameraConfig
	mainCtx context.Context // set during Run()

	alertListeners  []AlertListener
	resultListeners []ResultListener
	listenerMu      sync.Mutex

	// Track active watch state
	watchIDs     map[string]string              // cameraID -> watchID
	watchSources map[string]string              // cameraID -> source URL
	watchCancels map[string]context.CancelFunc  // cameraID -> cancel (for dynamic watches)
	watchMu      sync.Mutex

	wg sync.WaitGroup // tracks all watch goroutines
}

// NewManager creates a watch manager.
func NewManager(client *Client, cameras []config.CameraConfig) *Manager {
	return &Manager{
		client:       client,
		cameras:      cameras,
		watchIDs:     make(map[string]string),
		watchSources: make(map[string]string),
		watchCancels: make(map[string]context.CancelFunc),
	}
}

// OnAlert registers a listener for alert events.
func (m *Manager) OnAlert(fn AlertListener) {
	m.listenerMu.Lock()
	defer m.listenerMu.Unlock()
	m.alertListeners = append(m.alertListeners, fn)
}

// OnResult registers a listener for result events.
func (m *Manager) OnResult(fn ResultListener) {
	m.listenerMu.Lock()
	defer m.listenerMu.Unlock()
	m.resultListeners = append(m.resultListeners, fn)
}

// Run starts watches for all configured cameras and blocks until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) error {
	m.mainCtx = ctx

	for _, cam := range m.cameras {
		m.startWatch(ctx, cam)
	}

	if len(m.cameras) == 0 {
		log.Println("[manager] no cameras configured, waiting for dynamic watches")
	}

	// Block until shutdown
	<-ctx.Done()

	// Wait for all watch goroutines to finish
	m.wg.Wait()
	return nil
}

// StartDynamicWatch adds a new watch at runtime (e.g., from gateway invoke).
// Returns error if a watch with this cameraID already exists.
func (m *Manager) StartDynamicWatch(cam config.CameraConfig) error {
	m.watchMu.Lock()
	if _, exists := m.watchCancels[cam.ID]; exists {
		m.watchMu.Unlock()
		return fmt.Errorf("watch %q already active", cam.ID)
	}
	m.watchMu.Unlock()

	if m.mainCtx == nil {
		return fmt.Errorf("manager not running")
	}

	m.startWatch(m.mainCtx, cam)
	return nil
}

// StopDynamicWatch stops a watch by camera ID.
func (m *Manager) StopDynamicWatch(ctx context.Context, cameraID string) error {
	m.watchMu.Lock()
	cancel, exists := m.watchCancels[cameraID]
	watchID := m.watchIDs[cameraID]
	m.watchMu.Unlock()

	if !exists {
		return fmt.Errorf("watch %q not found", cameraID)
	}

	// Cancel the goroutine
	cancel()

	// Tell trio-core to stop the watch
	if watchID != "" {
		_ = m.client.StopWatch(ctx, watchID)
	}

	// Clean up
	m.watchMu.Lock()
	delete(m.watchIDs, cameraID)
	delete(m.watchSources, cameraID)
	delete(m.watchCancels, cameraID)
	m.watchMu.Unlock()

	log.Printf("[watch:%s] stopped", cameraID)
	return nil
}

// Status returns all active watches.
func (m *Manager) Status() []WatchEntry {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()

	var entries []WatchEntry
	for camID, source := range m.watchSources {
		state := "connecting"
		if _, hasWatchID := m.watchIDs[camID]; hasWatchID {
			state = "running"
		}
		entries = append(entries, WatchEntry{
			CameraID: camID,
			WatchID:  m.watchIDs[camID],
			Source:   source,
			State:    state,
		})
	}
	return entries
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

// startWatch spawns a goroutine for a camera watch with its own cancellable context.
func (m *Manager) startWatch(parentCtx context.Context, cam config.CameraConfig) {
	ctx, cancel := context.WithCancel(parentCtx)

	m.watchMu.Lock()
	m.watchCancels[cam.ID] = cancel
	m.watchSources[cam.ID] = cam.Source
	m.watchMu.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		m.runCamera(ctx, cam)
	}()
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
			return
		}

		if err != nil {
			log.Printf("[watch:%s] error: %v, reconnecting in %v", cam.ID, err, delay)
		} else {
			log.Printf("[watch:%s] stream ended, reconnecting in %v", cam.ID, reconnectDelay)
			delay = reconnectDelay
		}

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
		m.listenerMu.Lock()
		listeners := make([]ResultListener, len(m.resultListeners))
		copy(listeners, m.resultListeners)
		m.listenerMu.Unlock()
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
		m.listenerMu.Lock()
		listeners := make([]AlertListener, len(m.alertListeners))
		copy(listeners, m.alertListeners)
		m.listenerMu.Unlock()
		for _, fn := range listeners {
			fn(cameraID, alert)
		}

	default:
		log.Printf("[watch:%s] unknown event type: %s", cameraID, event.Type)
	}
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
