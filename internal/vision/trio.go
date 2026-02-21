// Package vision provides a Trio API client for VLM (Vision Language Model) analysis.
//
// TrioClaw does NOT run any AI locally. All visual understanding goes through
// Trio API (trio.machinefi.com). This keeps the binary small and the local
// machine requirements minimal.
//
// Trio API endpoints used:
//   POST /check-once  — single frame + condition → yes/no + explanation
//   GET  /healthz     — health check
package vision

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

// DefaultTrioAPIURL is the production Trio API endpoint.
const DefaultTrioAPIURL = "https://trio.machinefi.com"

const (
	defaultTimeout   = 30 * time.Second
	healthCheckPath  = "/healthz"
	checkOncePath    = "/check-once"
)

// TrioClient is an HTTP client for the Trio API.
type TrioClient struct {
	baseURL string // e.g. "https://trio.machinefi.com"
	client  *http.Client
}

// NewTrioClient creates a Trio API client.
// If baseURL is empty, uses DefaultTrioAPIURL.
func NewTrioClient(baseURL string) *TrioClient {
	if baseURL == "" {
		baseURL = DefaultTrioAPIURL
	}

	return &TrioClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: defaultTimeout,
		},
	}
}

// AnalyzeResult is the response from a vision analysis.
type AnalyzeResult struct {
	Triggered   bool    `json:"triggered"`   // whether the condition was met (yes/no)
	Explanation string  `json:"explanation"` // natural language description
	Confidence  float64 `json:"confidence"`  // 0.0-1.0
}

// checkOnceRequest is the request body for /check-once.
type checkOnceRequest struct {
	StreamURL string `json:"stream_url"` // data:image/jpeg;base64,{encoded}
	Condition string `json:"condition"`   // the question to ask about the frame
}

// checkOnceResponse is the response from /check-once.
type checkOnceResponse struct {
	Triggered   bool    `json:"triggered"`
	Explanation string  `json:"explanation"`
	Confidence  float64 `json:"confidence"`
	Error       string  `json:"error,omitempty"`
}

// Analyze sends a JPEG frame to the Trio API for VLM analysis.
//
// jpeg: JPEG-encoded image bytes
// question: yes/no question or open-ended question
//   - "is there a person at the door?"
//   - "what do you see?"
//   - "is it raining?"
//
// Calls POST /api/v1/check-once with:
//
//	{
//	  "stream_url": "data:image/jpeg;base64,{encoded}",
//	  "condition": "{question}"
//	}
//
// Returns the VLM's analysis.
func (c *TrioClient) Analyze(ctx context.Context, jpeg []byte, question string) (*AnalyzeResult, error) {
	// Validate inputs
	if len(jpeg) == 0 {
		return nil, fmt.Errorf("jpeg data is empty")
	}
	if question == "" {
		return nil, fmt.Errorf("question is empty")
	}

	// Base64-encode the JPEG
	encoded := base64.StdEncoding.EncodeToString(jpeg)

	// Build request body
	reqBody := checkOnceRequest{
		StreamURL: "data:image/jpeg;base64," + encoded,
		Condition: question,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	url := c.baseURL + checkOncePath
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call Trio API: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Trio API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var apiResp checkOnceResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Check for API-level errors
	if apiResp.Error != "" {
		return nil, fmt.Errorf("Trio API error: %s", apiResp.Error)
	}

	return &AnalyzeResult{
		Triggered:   apiResp.Triggered,
		Explanation: apiResp.Explanation,
		Confidence:  apiResp.Confidence,
	}, nil
}

// HealthCheck verifies that the Trio API is reachable.
// Returns nil if GET /healthz returns 200.
func (c *TrioClient) HealthCheck(ctx context.Context) error {
	url := c.baseURL + healthCheckPath

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach Trio API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Trio API health check failed: status %d", resp.StatusCode)
	}

	return nil
}

// SetTimeout sets the HTTP client timeout.
func (c *TrioClient) SetTimeout(timeout time.Duration) {
	c.client.Timeout = timeout
}

// SetHTTPClient allows setting a custom http.Client (for testing with mocks).
func (c *TrioClient) SetHTTPClient(client *http.Client) {
	c.client = client
}
