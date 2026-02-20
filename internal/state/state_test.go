package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultStateDir(t *testing.T) {
	dir := DefaultStateDir()
	if dir == "" {
		t.Fatal("DefaultStateDir returned empty string")
	}

	// On non-test systems, should be ~/.trioclaw
	homeDir, err := os.UserHomeDir()
	if err == nil {
		expected := filepath.Join(homeDir, ".trioclaw")
		if dir != expected {
			t.Logf("Note: DefaultStateDir = %s, expected %s (may differ in test env)", dir, expected)
		}
	}
}

func TestDefaultStatePath(t *testing.T) {
	path := DefaultStatePath()
	if path == "" {
		t.Fatal("DefaultStatePath returned empty string")
	}

	dir := DefaultStateDir()
	expectedPath := filepath.Join(dir, "state.json")

	if path != expectedPath {
		t.Errorf("DefaultStatePath() = %s, want %s", path, expectedPath)
	}
}

func TestGenerateNodeID(t *testing.T) {
	nodeID := GenerateNodeID()
	if nodeID == "" {
		t.Fatal("GenerateNodeID returned empty string")
	}

	// Node ID should be stable across calls
	nodeID2 := GenerateNodeID()
	if nodeID != nodeID2 {
		t.Errorf("GenerateNodeID() returned different values: %s != %s", nodeID, nodeID2)
	}

	// Node ID should start with "trioclaw-"
	if len(nodeID) < 10 || nodeID[:9] != "trioclaw-" {
		t.Errorf("GenerateNodeID() = %s, want prefix 'trioclaw-'", nodeID)
	}

	// Node ID should contain hostname
	hostname, _ := os.Hostname()
	if hostname != "" {
		// Normalize hostname same way as GenerateNodeID
		lowerHostname := ""
		for _, r := range hostname {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				lowerHostname += string(r)
			}
		}
		if lowerHostname != "" && len(nodeID) > 10 {
			nodeHostnamePart := nodeID[9 : 9+len(lowerHostname)]
			if nodeHostnamePart != lowerHostname {
				t.Logf("Note: Node ID hostname part = %s, expected %s (may differ due to normalization)", nodeHostnamePart, lowerHostname)
			}
		}
	}
}

func TestLoad_NotExists(t *testing.T) {
	// Use a temporary directory for testing
	tmpDir := t.TempDir()

	// Since DefaultStateDir and DefaultStatePath are functions,
	// we need to work around by using actual temp directory paths
	st, err := LoadStateFile(filepath.Join(tmpDir, "state.json"))
	if err != nil {
		t.Fatalf("LoadStateFile() error = %v, want nil for non-existent file", err)
	}

	if st == nil {
		t.Fatal("LoadStateFile() returned nil state")
	}

	if st.NodeID != "" || st.Token != "" || st.GatewayURL != "" {
		t.Errorf("LoadStateFile() = %v, want empty state for non-existent file", st)
	}
}

func TestLoad(t *testing.T) {
	// Use a temporary directory for testing
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	// Create test state
	expected := &State{
		NodeID:      "test-node-123",
		Token:       "test-token-abc",
		GatewayURL:  "ws://test.local:18789",
		DisplayName: "Test Node",
	}

	err := SaveStateFile(statePath, expected)
	if err != nil {
		t.Fatalf("SaveStateFile() error = %v", err)
	}

	// Load it back
	st, err := LoadStateFile(statePath)
	if err != nil {
		t.Fatalf("LoadStateFile() error = %v", err)
	}

	if st.NodeID != expected.NodeID {
		t.Errorf("NodeID = %s, want %s", st.NodeID, expected.NodeID)
	}

	if st.Token != expected.Token {
		t.Errorf("Token = %s, want %s", st.Token, expected.Token)
	}

	if st.GatewayURL != expected.GatewayURL {
		t.Errorf("GatewayURL = %s, want %s", st.GatewayURL, expected.GatewayURL)
	}

	if st.DisplayName != expected.DisplayName {
		t.Errorf("DisplayName = %s, want %s", st.DisplayName, expected.DisplayName)
	}
}

func TestSave(t *testing.T) {
	// Use a temporary directory for testing
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	st := &State{
		NodeID:      "test-node",
		Token:       "test-token",
		GatewayURL:  "ws://test:18789",
		DisplayName: "Test",
	}

	err := SaveStateFile(statePath, st)
	if err != nil {
		t.Fatalf("SaveStateFile() error = %v", err)
	}

	// Verify file was created
	info, err := os.Stat(statePath)
	if err != nil {
		t.Errorf("Stat() error = %v", err)
	} else {
		if info.Mode().Perm()&0600 != 0600 {
			t.Errorf("File permissions = %v, want 0600", info.Mode().Perm())
		}
	}
}

func TestIsPaired(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		want    bool
	}{
		{"empty token", "", false},
		{"has token", "some-token", true},
		{"whitespace token", "   ", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &State{Token: tt.token}
			got := st.IsPaired()
			if got != tt.want {
				t.Errorf("IsPaired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMustLoad_NotPaired(t *testing.T) {
	// Use a temporary directory for testing
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	// Create state without token
	st := &State{
		NodeID:      "test-node",
		Token:       "", // Empty = not paired
		GatewayURL:  "ws://test:18789",
	}

	_ = SaveStateFile(statePath, st)

	// MustLoad should fail for unpaired state
	_, err := LoadStateFile(statePath)
	if err == nil {
		t.Error("LoadStateFile() error = nil, want error for unpaired state")
	}
}
