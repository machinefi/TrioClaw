// Package state manages persistent node state stored at ~/.trioclaw/state.json.
//
// State includes:
//   - nodeId:      unique identifier for this node (generated once, stable across restarts)
//   - token:       device token issued by gateway during pairing
//   - gatewayURL:  last-used gateway WebSocket URL
//   - displayName: human-readable name ("Front Door Camera")
//
// State file location: ~/.trioclaw/state.json
// Directory is created automatically if it doesn't exist.
package state

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultStateDir is the directory for TrioClaw state files.
// Resolves to ~/.trioclaw/
func DefaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".trioclaw"
	}
	return filepath.Join(home, ".trioclaw")
}

// DefaultStatePath is the full path to the state file.
func DefaultStatePath() string {
	return filepath.Join(DefaultStateDir(), "state.json")
}

// State is the persistent node state.
type State struct {
	NodeID      string `json:"nodeId"`      // stable unique ID (e.g. "trioclaw-a1b2c3d4")
	Token       string `json:"token"`       // device token from gateway pairing
	GatewayURL  string `json:"gatewayUrl"`  // ws://host:18789
	DisplayName string `json:"displayName"` // human-readable name
}

// Load reads state from ~/.trioclaw/state.json.
// Returns empty State (not error) if file doesn't exist.
func Load() (*State, error) {
	path := DefaultStatePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{}, nil
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}

	return &st, nil
}

// MustLoad reads state and fails if not paired (no token).
// Use this in commands that require an existing pairing (e.g. "run").
func MustLoad() (*State, error) {
	st, err := Load()
	if err != nil {
		return nil, err
	}
	if !st.IsPaired() {
		return nil, fmt.Errorf("not paired with a gateway. Run 'trioclaw pair --gateway <url>' first")
	}
	return st, nil
}

// Save writes state to ~/.trioclaw/state.json.
// Creates the directory if it doesn't exist.
func Save(st *State) error {
	stateDir := DefaultStateDir()
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	path := DefaultStatePath()
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}

// GenerateNodeID creates a stable node ID based on hostname + machine-id.
//
// Format: "trioclaw-{hostname}-{machine_id_first_8_chars}"
// Example: "trioclaw-macbook-a1b2c3d4"
//
// Falls back to random suffix if machine-id is unavailable.
func GenerateNodeID() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	// Normalize hostname (remove spaces, special chars)
	hostname = strings.ToLower(strings.ReplaceAll(hostname, " ", "-"))
	hostname = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, hostname)
	if hostname == "" {
		hostname = "unknown"
	}

	// Get machine ID (platform-specific)
	machineID := getMachineID()
	if machineID == "" {
		// Generate random 8-char hex string as fallback
		machineID = generateRandomID(8)
	} else if len(machineID) > 8 {
		machineID = machineID[:8]
	}

	return fmt.Sprintf("trioclaw-%s-%s", hostname, machineID)
}

// getMachineID retrieves the machine ID in a platform-specific way.
func getMachineID() string {
	switch runtime.GOOS {
	case "linux":
		// Try /etc/machine-id (systemd systems)
		if data, err := os.ReadFile("/etc/machine-id"); err == nil {
			id := strings.TrimSpace(string(data))
			if id != "" {
				return id
			}
		}
		// Try /var/lib/dbus/machine-id (older systems)
		if data, err := os.ReadFile("/var/lib/dbus/machine-id"); err == nil {
			id := strings.TrimSpace(string(data))
			if id != "" {
				return id
			}
		}

	case "darwin":
		// Use IOPlatformSerialNumber on macOS
		cmd := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice")
		if output, err := cmd.Output(); err == nil {
			// Parse output for IOPlatformSerialNumber
			lines := strings.Split(string(output), "\n")
			for _, line := range lines {
				if strings.Contains(line, "IOPlatformSerialNumber") {
					parts := strings.Split(line, `"`)
					if len(parts) >= 2 {
						serial := strings.TrimSpace(parts[1])
						if serial != "" {
							// Hash the serial to get a stable ID
							hash := md5.Sum([]byte(serial))
							return hex.EncodeToString(hash[:])[:16]
						}
					}
				}
			}
		}
	}

	return ""
}

// generateRandomID generates a random hex string of specified length.
func generateRandomID(length int) string {
	if length <= 0 {
		return ""
	}
	// Use /dev/urandom on Unix systems, or fall back to hostname hash
	data := make([]byte, length/2+1)
	if f, err := os.Open("/dev/urandom"); err == nil {
		f.Read(data)
		f.Close()
	} else {
		// Fallback: hash hostname
		hostname, _ := os.Hostname()
		hash := md5.Sum([]byte(hostname))
		data = hash[:]
	}
	return hex.EncodeToString(data)[:length]
}

// IsPaired returns true if state has a valid gateway token.
func (s *State) IsPaired() bool {
	return strings.TrimSpace(s.Token) != ""
}

// LoadStateFile loads state from a specific file path (for testing).
func LoadStateFile(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{}, nil
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}

	return &st, nil
}

// SaveStateFile writes state to a specific file path (for testing).
func SaveStateFile(path string, st *State) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}
