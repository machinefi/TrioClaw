package vision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewTrioClient(t *testing.T) {
	client := NewTrioClient("")
	if client == nil {
		t.Fatal("NewTrioClient() returned nil")
	}

	if client.baseURL != DefaultTrioAPIURL {
		t.Errorf("baseURL = %s, want %s", client.baseURL, DefaultTrioAPIURL)
	}
}

func TestNewTrioClient_CustomURL(t *testing.T) {
	customURL := "https://custom.trio.example.com"
	client := NewTrioClient(customURL)
	if client == nil {
		t.Fatal("NewTrioClient() returned nil")
	}

	if client.baseURL != customURL {
		t.Errorf("baseURL = %s, want %s", client.baseURL, customURL)
	}
}

func TestAnalyzeResult_JSON(t *testing.T) {
	result := AnalyzeResult{
		Triggered:   true,
		Explanation: "A person is visible in the frame",
		Confidence:  0.95,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded AnalyzeResult
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if decoded.Triggered != result.Triggered {
		t.Errorf("Triggered = %v, want %v", decoded.Triggered, result.Triggered)
	}

	if decoded.Explanation != result.Explanation {
		t.Errorf("Explanation = %v, want %v", decoded.Explanation, result.Explanation)
	}

	if decoded.Confidence != result.Confidence {
		t.Errorf("Confidence = %v, want %v", decoded.Confidence, result.Confidence)
	}
}

func TestHealthCheck_Success(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != healthCheckPath {
			t.Errorf("Path = %s, want %s", r.URL.Path, healthCheckPath)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create client with mock server URL
	client := NewTrioClient("")
	client.SetHTTPClient(server.Client())

	// Test health check
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.HealthCheck(ctx)
	if err != nil {
		t.Errorf("HealthCheck() error = %v, want nil", err)
	}
}

func TestHealthCheck_Failure(t *testing.T) {
	// Create mock server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewTrioClient("")
	client.SetHTTPClient(server.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.HealthCheck(ctx)
	if err == nil {
		t.Error("HealthCheck() error = nil, want error for 500 status")
	}
}

func TestAnalyze_Success(t *testing.T) {
	// Create mock server
	jpegData := make([]byte, 1024) // Simulated JPEG data

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method = %s, want %s", r.Method, http.MethodPost)
		}

		if r.URL.Path != checkOncePath {
			t.Errorf("Path = %s, want %s", r.URL.Path, checkOncePath)
		}

		// Parse request
		var req checkOnceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("Decode error = %v", err)
		}

		// Verify request contains base64 image
		if req.StreamURL == "" {
			t.Error("StreamURL is empty")
		}

		// Return mock response
		resp := checkOnceResponse{
			Triggered:   true,
			Explanation: "A person is standing at the door",
			Confidence:  0.92,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewTrioClient("")
	client.SetHTTPClient(server.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := client.Analyze(ctx, jpegData, "Is there a person?")
	if err != nil {
		t.Errorf("Analyze() error = %v, want nil", err)
	}

	if result.Triggered != true {
		t.Errorf("Triggered = %v, want true", result.Triggered)
	}

	if result.Explanation == "" {
		t.Error("Explanation is empty")
	}

	if result.Confidence <= 0 || result.Confidence > 1 {
		t.Errorf("Confidence = %v, want in [0, 1]", result.Confidence)
	}
}

func TestAnalyze_EmptyJPEG(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(checkOnceResponse{
			Triggered:   false,
			Explanation: "No person detected",
			Confidence:  0.1,
		})
	}))
	defer server.Close()

	client := NewTrioClient("")
	client.SetHTTPClient(server.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.Analyze(ctx, []byte{}, "What do you see?")
	if err == nil {
		t.Error("Analyze() error = nil, want error for empty JPEG")
	}
}

func TestAnalyze_EmptyQuestion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(checkOnceResponse{
			Triggered:   false,
			Explanation: "No person detected",
			Confidence:  0.1,
		})
	}))
	defer server.Close()

	client := NewTrioClient("")
	client.SetHTTPClient(server.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	jpegData := make([]byte, 1024)
	_, err := client.Analyze(ctx, jpegData, "")
	if err == nil {
		t.Error("Analyze() error = nil, want error for empty question")
	}
}

func TestAnalyze_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return error response from API
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(checkOnceResponse{
			Error: "Invalid image format",
		})
	}))
	defer server.Close()

	client := NewTrioClient("")
	client.SetHTTPClient(server.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	jpegData := make([]byte, 1024)
	_, err := client.Analyze(ctx, jpegData, "What do you see?")
	if err == nil {
		t.Error("Analyze() error = nil, want error for API error")
	}
}

func TestAnalyze_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewTrioClient("")
	client.SetHTTPClient(server.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	jpegData := make([]byte, 1024)
	_, err := client.Analyze(ctx, jpegData, "What do you see?")
	if err == nil {
		t.Error("Analyze() error = nil, want error for HTTP 500")
	}
}

func TestSetTimeout(t *testing.T) {
	client := NewTrioClient("")
	newTimeout := 60 * time.Second

	client.SetTimeout(newTimeout)

	if client.client.Timeout != newTimeout {
		t.Errorf("client.Timeout = %v, want %v", client.client.Timeout, newTimeout)
	}
}

func TestSetHTTPClient(t *testing.T) {
	client := NewTrioClient("")
	mockClient := &http.Client{}

	client.SetHTTPClient(mockClient)

	if client.client != mockClient {
		t.Error("SetHTTPClient() did not set the client")
	}
}
