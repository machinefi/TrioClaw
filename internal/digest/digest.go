// Package digest generates daily summaries of monitoring events.
//
// Runs on a cron schedule (default 10 PM). Collects the day's alerts from
// SQLite, formats them as a prompt, sends to an LLM (local trio-core or
// cloud API), and pushes the summary via notification channels.
package digest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/machinefi/trioclaw/internal/notify"
	"github.com/machinefi/trioclaw/internal/store"
)

// Runner generates and sends daily digests.
type Runner struct {
	store      *store.Store
	dispatcher *notify.Dispatcher
	pushTo     []string // notification channel names

	llm       string // "local", "claude", "openai"
	llmURL    string // trio-core URL for local LLM
	apiKey    string // cloud API key (claude or openai)
	httpClient *http.Client
}

// Config for the digest runner.
type Config struct {
	LLM    string // "local", "claude", "openai"
	LLMURL string // trio-core base URL (for "local")
	APIKey string // API key (for "claude" or "openai")
	PushTo []string
}

// NewRunner creates a digest runner.
func NewRunner(s *store.Store, dispatcher *notify.Dispatcher, cfg Config) *Runner {
	return &Runner{
		store:      s,
		dispatcher: dispatcher,
		pushTo:     cfg.PushTo,
		llm:        cfg.LLM,
		llmURL:     cfg.LLMURL,
		apiKey:     cfg.APIKey,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// RunOnce generates and sends a digest for the given date.
func (r *Runner) RunOnce(ctx context.Context, date time.Time) error {
	alerts, err := r.store.AlertsByDate(date)
	if err != nil {
		return fmt.Errorf("query alerts: %w", err)
	}

	if len(alerts) == 0 {
		summary := fmt.Sprintf("Daily Summary (%s): No alerts today. All cameras quiet.",
			date.Format("2006-01-02"))
		return r.send(ctx, summary)
	}

	prompt := formatPrompt(alerts, date)

	summary, err := r.callLLM(ctx, prompt)
	if err != nil {
		// Fallback: send a plain text summary if LLM fails
		log.Printf("[digest] LLM call failed: %v, using plain summary", err)
		summary = formatPlainSummary(alerts, date)
	}

	return r.send(ctx, summary)
}

// RunSchedule starts a background loop that runs the digest at the specified hour.
// hourMinute is "HH:MM" in local time, e.g. "22:00".
func (r *Runner) RunSchedule(ctx context.Context, hourMinute string) {
	parts := strings.SplitN(hourMinute, ":", 2)
	if len(parts) != 2 {
		log.Printf("[digest] invalid schedule %q, using 22:00", hourMinute)
		parts = []string{"22", "00"}
	}

	hour := parseInt(parts[0], 22)
	minute := parseInt(parts[1], 0)

	log.Printf("[digest] scheduled daily at %02d:%02d", hour, minute)

	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			log.Printf("[digest] generating daily digest for %s", now.Format("2006-01-02"))
			if err := r.RunOnce(ctx, now); err != nil {
				log.Printf("[digest] error: %v", err)
			}
		}
	}
}

func (r *Runner) send(ctx context.Context, summary string) error {
	if len(r.pushTo) == 0 {
		log.Printf("[digest] no push_to channels configured, logging only")
		log.Printf("[digest] %s", summary)
		return nil
	}

	r.dispatcher.Dispatch(ctx, r.pushTo, notify.Alert{
		CameraID:    "digest",
		CameraName:  "Daily Digest",
		ConditionID: "summary",
		Answer:      summary,
		Timestamp:   time.Now(),
	})
	return nil
}

func (r *Runner) callLLM(ctx context.Context, prompt string) (string, error) {
	switch r.llm {
	case "local":
		return r.callLocal(ctx, prompt)
	case "claude":
		return r.callClaude(ctx, prompt)
	case "openai":
		return r.callOpenAI(ctx, prompt)
	default:
		return "", fmt.Errorf("unknown LLM type: %s", r.llm)
	}
}

// callLocal calls trio-core's OpenAI-compatible /v1/chat/completions endpoint.
func (r *Runner) callLocal(ctx context.Context, prompt string) (string, error) {
	url := strings.TrimRight(r.llmURL, "/") + "/v1/chat/completions"
	return r.callChatCompletions(ctx, url, "", prompt)
}

// callOpenAI calls OpenAI's /v1/chat/completions.
func (r *Runner) callOpenAI(ctx context.Context, prompt string) (string, error) {
	return r.callChatCompletions(ctx, "https://api.openai.com/v1/chat/completions", r.apiKey, prompt)
}

// callClaude calls Claude via Anthropic's Messages API.
func (r *Runner) callClaude(ctx context.Context, prompt string) (string, error) {
	body := map[string]any{
		"model":      "claude-sonnet-4-20250514",
		"max_tokens": 1024,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	bodyJSON, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(bodyJSON))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", r.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("claude API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("claude API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode claude response: %w", err)
	}

	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty claude response")
	}
	return result.Content[0].Text, nil
}

// callChatCompletions calls an OpenAI-compatible /v1/chat/completions endpoint.
// model is optional — empty string omits it (local LLM will use its default).
func (r *Runner) callChatCompletions(ctx context.Context, url, apiKey, prompt string) (string, error) {
	body := map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 1024,
	}
	// Only set model for cloud APIs; local LLM uses its default model
	if apiKey != "" {
		body["model"] = "gpt-4o-mini"
	}

	bodyJSON, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyJSON))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("chat completions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("chat completions returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode chat response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty chat response")
	}
	return result.Choices[0].Message.Content, nil
}

// formatPrompt builds the LLM prompt from today's alerts.
func formatPrompt(alerts []store.Event, date time.Time) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Summarize today's monitoring events (%s). ", date.Format("2006-01-02")))
	b.WriteString("Be concise. Group related events. Note any patterns or anomalies.\n\n")
	b.WriteString("Events:\n")

	for _, a := range alerts {
		ts := a.Timestamp.Local().Format("15:04:05")
		b.WriteString(fmt.Sprintf("[%s] %s/%s: %s\n", ts, a.CameraID, a.ConditionID, a.Answer))
	}

	return b.String()
}

// formatPlainSummary generates a simple summary without LLM.
func formatPlainSummary(alerts []store.Event, date time.Time) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Daily Summary (%s)\n", date.Format("2006-01-02")))
	b.WriteString(fmt.Sprintf("%d alerts total\n\n", len(alerts)))

	// Group by camera
	byCam := make(map[string][]store.Event)
	for _, a := range alerts {
		byCam[a.CameraID] = append(byCam[a.CameraID], a)
	}

	// Sort camera IDs for deterministic output
	camIDs := make([]string, 0, len(byCam))
	for id := range byCam {
		camIDs = append(camIDs, id)
	}
	sort.Strings(camIDs)

	for _, camID := range camIDs {
		events := byCam[camID]
		b.WriteString(fmt.Sprintf("%s: %d alerts\n", camID, len(events)))
		for _, e := range events {
			ts := e.Timestamp.Local().Format("15:04")
			b.WriteString(fmt.Sprintf("  [%s] %s: %s\n", ts, e.ConditionID, e.Answer))
		}
	}

	return b.String()
}

func parseInt(s string, fallback int) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
	}
	return n
}
