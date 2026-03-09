// Package notify dispatches alert notifications to configured channels.
//
// Channels: webhook (POST JSON), telegram (bot API), slack (webhook).
// Each channel implements the Notifier interface.
//
// Usage:
//
//	d := notify.NewDispatcher()
//	d.Register(notify.NewWebhook(cfg))
//	d.Register(notify.NewTelegram(cfg))
//	d.Dispatch(ctx, []string{"webhook", "telegram"}, alert)
package notify

import (
	"context"
	"log"
	"sync"
	"time"
)

// Alert is the payload passed to notifiers.
type Alert struct {
	CameraID    string
	CameraName  string
	ConditionID string
	Question    string
	Answer      string
	Timestamp   time.Time
	FrameJPEG   []byte // optional: the triggering frame (decoded from base64)
}

// Notifier sends an alert to a specific channel.
type Notifier interface {
	Name() string
	SendAlert(ctx context.Context, alert Alert) error
}

// Dispatcher routes alerts to notifiers by action name.
type Dispatcher struct {
	notifiers map[string]Notifier

	// conditionActions maps "cameraID/conditionID" -> action names
	actions map[string][]string
	mu      sync.RWMutex
}

// NewDispatcher creates an empty dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		notifiers: make(map[string]Notifier),
		actions:   make(map[string][]string),
	}
}

// Register adds a notifier. Its Name() determines the action key.
func (d *Dispatcher) Register(n Notifier) {
	d.notifiers[n.Name()] = n
	log.Printf("[notify] registered %s notifier", n.Name())
}

// SetActions maps a camera/condition pair to a list of action names.
func (d *Dispatcher) SetActions(cameraID, conditionID string, actions []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.actions[cameraID+"/"+conditionID] = actions
}

// GetActions returns the actions for a camera/condition pair.
func (d *Dispatcher) GetActions(cameraID, conditionID string) []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.actions[cameraID+"/"+conditionID]
}

// Dispatch sends the alert to each named action channel.
// Unknown actions are logged and skipped. Errors are logged, not returned.
func (d *Dispatcher) Dispatch(ctx context.Context, actions []string, alert Alert) {
	for _, action := range actions {
		n, ok := d.notifiers[action]
		if !ok {
			log.Printf("[notify] unknown action %q, skipping", action)
			continue
		}
		go func(n Notifier) {
			if err := n.SendAlert(ctx, alert); err != nil {
				log.Printf("[notify:%s] error: %v", n.Name(), err)
			} else {
				log.Printf("[notify:%s] sent alert for %s/%s", n.Name(), alert.CameraID, alert.ConditionID)
			}
		}(n)
	}
}

// DispatchForCondition looks up actions by cameraID/conditionID and dispatches.
func (d *Dispatcher) DispatchForCondition(ctx context.Context, cameraID, conditionID string, alert Alert) {
	actions := d.GetActions(cameraID, conditionID)
	if len(actions) == 0 {
		return
	}
	d.Dispatch(ctx, actions, alert)
}

// HasNotifiers returns true if any notifiers are registered.
func (d *Dispatcher) HasNotifiers() bool {
	return len(d.notifiers) > 0
}
