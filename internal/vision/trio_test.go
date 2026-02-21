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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != healthCheckPath {
			t.Errorf("Path = %s, want %s", r.URL.Path, healthCheckPath)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewTrioClient(server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.HealthCheck(ctx)
	if err != nil {
		t.Errorf("HealthCheck() error = %v, want nil", err)
	}
}

func TestHealthCheck_Failure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewTrioClient(server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.HealthCheck(ctx)
	if err == nil {
		t.Error("HealthCheck() error = nil, want error for 500 status")
	}
}

func TestAnalyze_Success(t *testing.T) {
	jpegData := make([]byte, 1024)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method = %s, want %s", r.Method, http.MethodPost)
		}

		if r.URL.Path != analyzeFramePath {
			t.Errorf("Path = %s, want %s", r.URL.Path, analyzeFramePath)
		}

		// Parse request
		var req analyzeFrameRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("Decode error = %v", err)
		}

		if req.FrameB64 == "" {
			t.Error("FrameB64 is empty")
		}
		if req.Question == "" {
			t.Error("Question is empty")
		}

		// Return mock response
		triggered := true
		resp := analyzeFrameResponse{
			Answer:    "A person is standing at the door",
			Triggered: &triggered,
			LatencyMs: 150,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewTrioClient(server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := client.Analyze(ctx, jpegData, "Is there a person?")
	if err != nil {
		t.Fatalf("Analyze() error = %v, want nil", err)
	}

	if result.Triggered != true {
		t.Errorf("Triggered = %v, want true", result.Triggered)
	}

	if result.Explanation == "" {
		t.Error("Explanation is empty")
	}
}

func TestAnalyze_EmptyJPEG(t *testing.T) {
	client := NewTrioClient("http://localhost:9999")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.Analyze(ctx, []byte{}, "What do you see?")
	if err == nil {
		t.Error("Analyze() error = nil, want error for empty JPEG")
	}
}

func TestAnalyze_EmptyQuestion(t *testing.T) {
	client := NewTrioClient("http://localhost:9999")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	jpegData := make([]byte, 1024)
	_, err := client.Analyze(ctx, jpegData, "")
	if err == nil {
		t.Error("Analyze() error = nil, want error for empty question")
	}
}

func TestAnalyze_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewTrioClient(server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	jpegData := make([]byte, 1024)
	_, err := client.Analyze(ctx, jpegData, "What do you see?")
	if err == nil {
		t.Error("Analyze() error = nil, want error for HTTP 500")
	}
}

func TestAnalyze_NullTriggered(t *testing.T) {
	jpegData := make([]byte, 1024)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return response with null triggered (open-ended question)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"answer":"The scene shows a park with trees","triggered":null,"latency_ms":200}`))
	}))
	defer server.Close()

	client := NewTrioClient(server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := client.Analyze(ctx, jpegData, "What do you see?")
	if err != nil {
		t.Fatalf("Analyze() error = %v, want nil", err)
	}

	if result.Triggered != false {
		t.Errorf("Triggered = %v, want false for null triggered", result.Triggered)
	}

	if result.Explanation != "The scene shows a park with trees" {
		t.Errorf("Explanation = %q, want description", result.Explanation)
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
