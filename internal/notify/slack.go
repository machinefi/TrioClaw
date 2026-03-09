package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SlackNotifier sends alerts via Slack incoming webhook.
type SlackNotifier struct {
	webhookURL string
	client     *http.Client
}

// NewSlack creates a Slack notifier.
func NewSlack(webhookURL string) *SlackNotifier {
	return &SlackNotifier{
		webhookURL: webhookURL,
		client:     &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *SlackNotifier) Name() string { return "slack" }

func (s *SlackNotifier) SendAlert(ctx context.Context, alert Alert) error {
	name := alert.CameraName
	if name == "" {
		name = alert.CameraID
	}
	ts := alert.Timestamp.Local().Format("15:04:05")

	text := fmt.Sprintf("*%s* [%s]\n>%s: %s", name, ts, alert.ConditionID, alert.Answer)

	payload := map[string]string{"text": text}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", s.webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("slack webhook: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack returned %d", resp.StatusCode)
	}
	return nil
}
