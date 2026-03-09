package notify

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// WebhookNotifier sends alerts as JSON POST requests.
type WebhookNotifier struct {
	url     string
	headers map[string]string
	client  *http.Client
}

// NewWebhook creates a webhook notifier.
func NewWebhook(url string, headers map[string]string) *WebhookNotifier {
	return &WebhookNotifier{
		url:     url,
		headers: headers,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (w *WebhookNotifier) Name() string { return "webhook" }

// webhookPayload is the JSON body sent to the webhook URL.
type webhookPayload struct {
	CameraID    string `json:"camera_id"`
	CameraName  string `json:"camera_name,omitempty"`
	ConditionID string `json:"condition_id"`
	Answer      string `json:"answer"`
	Timestamp   string `json:"timestamp"`
	FrameB64    string `json:"frame_b64,omitempty"`
}

func (w *WebhookNotifier) SendAlert(ctx context.Context, alert Alert) error {
	payload := webhookPayload{
		CameraID:    alert.CameraID,
		CameraName:  alert.CameraName,
		ConditionID: alert.ConditionID,
		Answer:      alert.Answer,
		Timestamp:   alert.Timestamp.UTC().Format(time.RFC3339),
	}
	if len(alert.FrameJPEG) > 0 {
		payload.FrameB64 = base64.StdEncoding.EncodeToString(alert.FrameJPEG)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", w.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range w.headers {
		req.Header.Set(k, v)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook POST: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}
