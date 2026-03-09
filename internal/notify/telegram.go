package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

const telegramAPIBase = "https://api.telegram.org/bot"

// TelegramNotifier sends alerts via Telegram Bot API.
type TelegramNotifier struct {
	botToken string
	chatID   string
	client   *http.Client
}

// NewTelegram creates a Telegram notifier.
func NewTelegram(botToken, chatID string) *TelegramNotifier {
	return &TelegramNotifier{
		botToken: botToken,
		chatID:   chatID,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (t *TelegramNotifier) Name() string { return "telegram" }

func (t *TelegramNotifier) SendAlert(ctx context.Context, alert Alert) error {
	caption := formatTelegramCaption(alert)

	if len(alert.FrameJPEG) > 0 {
		return t.sendPhoto(ctx, caption, alert.FrameJPEG)
	}
	return t.sendMessage(ctx, caption)
}

// sendMessage sends a text message via Telegram Bot API.
func (t *TelegramNotifier) sendMessage(ctx context.Context, text string) error {
	url := telegramAPIBase + t.botToken + "/sendMessage"

	payload := map[string]string{
		"chat_id":    t.chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram sendMessage: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram returned %d", resp.StatusCode)
	}
	return nil
}

// sendPhoto sends a photo with caption via Telegram Bot API.
func (t *TelegramNotifier) sendPhoto(ctx context.Context, caption string, jpeg []byte) error {
	url := telegramAPIBase + t.botToken + "/sendPhoto"

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	w.WriteField("chat_id", t.chatID)
	w.WriteField("caption", caption)
	w.WriteField("parse_mode", "Markdown")

	part, err := w.CreateFormFile("photo", "alert.jpg")
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(jpeg); err != nil {
		return fmt.Errorf("write photo: %w", err)
	}
	w.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram sendPhoto: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram returned %d", resp.StatusCode)
	}
	return nil
}

func formatTelegramCaption(alert Alert) string {
	name := alert.CameraName
	if name == "" {
		name = alert.CameraID
	}
	ts := alert.Timestamp.Local().Format("15:04:05")
	return fmt.Sprintf("*%s* [%s]\n%s: %s", name, ts, alert.ConditionID, alert.Answer)
}
