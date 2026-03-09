package triocore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsReachable(t *testing.T) {
	// Healthy server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	if !isReachable(context.Background(), srv.URL) {
		t.Error("healthy server should be reachable")
	}

	// Unreachable
	if isReachable(context.Background(), "http://127.0.0.1:1") {
		t.Error("unreachable server should not be reachable")
	}
}

func TestEnsureTrioCore_AlreadyRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	result, err := EnsureTrioCore(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if result.URL != srv.URL {
		t.Errorf("URL = %s, want %s", result.URL, srv.URL)
	}
	if result.Process != nil {
		t.Error("should not start a process when already running")
	}
	if result.IsCloud {
		t.Error("should not be cloud when existing server is used")
	}
}

func TestDetectPython(t *testing.T) {
	// This test just verifies it doesn't panic — result depends on system
	py := detectPython()
	t.Logf("detected python: %q", py)
}

func TestDetectPackageManager(t *testing.T) {
	bin, isUV := detectPackageManager()
	t.Logf("detected package manager: %q (uv=%v)", bin, isUV)
}
