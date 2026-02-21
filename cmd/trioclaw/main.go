// Package main is the CLI entry point for TrioClaw.
//
// Commands:
//   trioclaw pair    --gateway ws://host:18789   Pair with OpenClaw Gateway
//   trioclaw run     [--camera rtsp://...]        Start as OpenClaw node
//   trioclaw snap    [--camera ...] [--analyze q] One-shot capture + optional VLM
//   trioclaw doctor                               Check dependencies & devices
//   trioclaw update                               Self-update to latest release
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/machinefi/trioclaw/internal/capture"
	"github.com/machinefi/trioclaw/internal/gateway"
	pluginpkg "github.com/machinefi/trioclaw/internal/plugin"
	"github.com/machinefi/trioclaw/internal/plugin/execplugin"
	ha "github.com/machinefi/trioclaw/internal/plugin/homeassistant"
	"github.com/machinefi/trioclaw/internal/state"
	"github.com/machinefi/trioclaw/internal/vision"
	"github.com/spf13/cobra"
)

var (
	version = "0.1.0"
	commit  = "unknown"
)

// =============================================================================
// Root command
// =============================================================================

var rootCmd = &cobra.Command{
	Use:   "trioclaw",
	Short: "TrioClaw — AI vision & sensing node for OpenClaw",
	Long:  "Give any AI agent eyes, ears, and senses. Connects cameras, microphones, and smart devices to OpenClaw.",
}

// =============================================================================
// trioclaw pair --gateway ws://host:18789 [--name "Front Door"]
//
// Connects to the Gateway, sends a pairing request, waits for operator approval,
// and saves the device token to ~/.trioclaw/state.json.
// =============================================================================

var pairGatewayURL string
var pairDisplayName string

var pairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Pair with an OpenClaw Gateway",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPair(cmd.Context())
	},
}

func runPair(ctx context.Context) error {
	// Validate gateway URL
	if pairGatewayURL == "" {
		return fmt.Errorf("--gateway is required")
	}

	// Load or create state
	st, err := state.Load()
	if err != nil {
		return fmt.Errorf("failed to load state: %w", err)
	}

	// Generate node ID if not set
	if st.NodeID == "" {
		st.NodeID = state.GenerateNodeID()
	}

	// Set display name
	displayName := pairDisplayName
	if displayName == "" {
		displayName = st.NodeID
	}

	// Get capabilities and commands
	caps, commands := nodeCapabilities()

	// Create client (no token for pairing)
	client := gateway.NewClient(pairGatewayURL, "")
	client.SetNodeID(st.NodeID)

	fmt.Printf("Connecting to Gateway: %s\n", pairGatewayURL)
	fmt.Printf("Node ID: %s\n", st.NodeID)
	fmt.Printf("Node Name: %s\n", displayName)

	// Send pairing request
	fmt.Println("\nWaiting for operator approval...")
	fmt.Println("Run 'openclaw devices approve' on the gateway to approve this device.")

	token, err := client.Pair(ctx, displayName, caps, commands)
	if err != nil {
		return fmt.Errorf("pairing failed: %w", err)
	}

	// Save the token
	st.Token = token
	st.GatewayURL = pairGatewayURL
	st.DisplayName = displayName

	if err := state.Save(st); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}

	fmt.Println("\n✓ Pairing successful!")
	fmt.Printf("Token saved to: %s\n", state.DefaultStatePath())
	fmt.Println("\nYou can now run: trioclaw run")

	return nil
}

// =============================================================================
// trioclaw run [--camera rtsp://...] [--camera /dev/video0]
//
// Main operating mode. Connects to Gateway as a node, registers all available
// devices (cameras + mic), and handles invoke commands from the agent.
//
// Lifecycle:
//   1. Load state (must be paired already)
//   2. Discover local devices via capture.ListDevices()
//   3. Connect to Gateway, authenticate with saved token
//   4. Register capabilities: camera.snap, camera.list, vision.analyze
//   5. Enter main loop: handle invokes, send pings, reconnect on disconnect
//   6. On SIGINT/SIGTERM: graceful shutdown
// =============================================================================

var runCameras []string // additional camera sources (rtsp:// or device paths)
var runHAURL string     // Home Assistant URL
var runHAToken string   // Home Assistant long-lived access token
var runPluginDir string // custom plugin scripts directory

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start as an OpenClaw node",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRun(cmd.Context())
	},
}

func runRun(ctx context.Context) error {
	// Load state (must be paired)
	st, err := state.MustLoad()
	if err != nil {
		return err
	}

	fmt.Printf("Node ID: %s\n", st.NodeID)
	fmt.Printf("Gateway: %s\n", st.GatewayURL)

	// Discover local devices
	devices, err := capture.ListDevices()
	if err != nil {
		fmt.Printf("Warning: failed to list devices: %v\n", err)
	} else {
		fmt.Printf("Found %d device(s)\n", len(devices))
		for _, dev := range devices {
			fmt.Printf("  - %s: %s (%s)\n", dev.ID, dev.Name, dev.Type)
		}
	}

	// Add extra cameras
	for _, cam := range runCameras {
		fmt.Printf("  - extra: %s (RTSP)\n", cam)
	}

	// Create Trio API client
	trioClient := vision.NewTrioClient("")

	// Create handler
	handler := gateway.NewHandler(devices, trioClient, runCameras)

	// Setup plugins (Hands)
	registry := pluginpkg.NewRegistry()

	// Home Assistant plugin
	if runHAURL != "" && runHAToken != "" {
		haPlugin := ha.New(runHAURL, runHAToken)
		if err := registry.Register(haPlugin); err != nil {
			return fmt.Errorf("failed to register HA plugin: %w", err)
		}
		fmt.Printf("  + Home Assistant: %s\n", runHAURL)
	}

	// Exec plugin (user scripts)
	execPlugin, err := execplugin.New(runPluginDir)
	if err != nil {
		fmt.Printf("Warning: exec plugin init failed: %v\n", err)
	} else {
		if err := registry.Register(execPlugin); err != nil {
			fmt.Printf("Warning: failed to register exec plugin: %v\n", err)
		}
		if execPlugin.ScriptCount() > 0 {
			fmt.Printf("  + Exec plugins: %d script(s)\n", execPlugin.ScriptCount())
		}
	}

	handler.SetPlugins(registry)

	// Create client
	client := gateway.NewClient(st.GatewayURL, st.Token)
	client.SetNodeID(st.NodeID)

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
	}()

	// Run the client (blocks until context cancelled)
	fmt.Println("\nStarting node...")
	if err := client.Run(ctx, handler); err != nil {
		return fmt.Errorf("run error: %w", err)
	}

	return nil
}

// =============================================================================
// trioclaw snap [--camera source] [--analyze "question"] [--output file.jpg]
//
// Standalone mode (no Gateway needed). Captures a single frame, optionally
// runs VLM analysis via Trio API, prints result.
//
// Examples:
//   trioclaw snap                          → capture webcam, save frame.jpg
//   trioclaw snap --analyze "what is this" → capture + VLM analysis, print answer
//   trioclaw snap --camera rtsp://...      → capture from RTSP camera
// =============================================================================

var snapCamera string
var snapAnalyze string
var snapOutput string
var snapTrioAPI string

var snapCmd = &cobra.Command{
	Use:   "snap",
	Short: "Capture a frame and optionally analyze it",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSnap(cmd.Context())
	},
}

func runSnap(ctx context.Context) error {
	// Determine camera source
	source := snapCamera
	if source == "" {
		source = "0" // default webcam
	}

	// Capture frame
	fmt.Printf("Capturing from camera: %s\n", source)
	frame, err := capture.CaptureFrameCtx(ctx, source)
	if err != nil {
		return fmt.Errorf("failed to capture frame: %w", err)
	}

	fmt.Printf("Captured %d bytes\n", len(frame.JPEG))

	// Save to file if specified or default
	outputFile := snapOutput
	if outputFile == "" {
		outputFile = "frame.jpg"
	}

	if err := os.WriteFile(outputFile, frame.JPEG, 0644); err != nil {
		return fmt.Errorf("failed to save frame: %w", err)
	}
	fmt.Printf("Saved to: %s\n", outputFile)

	// Analyze if requested
	if snapAnalyze != "" {
		fmt.Printf("\nAnalyzing: %s\n", snapAnalyze)

		// Create Trio client
		trioClient := vision.NewTrioClient(snapTrioAPI)

		// Analyze
		result, err := trioClient.Analyze(ctx, frame.JPEG, snapAnalyze)
		if err != nil {
			return fmt.Errorf("failed to analyze: %w", err)
		}

		fmt.Printf("\nResult:\n")
		fmt.Printf("  %s\n", result.Explanation)
		fmt.Printf("  Confidence: %.2f\n", result.Confidence)
	} else if snapTrioAPI != "" {
		// Just test connectivity
		fmt.Printf("\nTesting Trio API connection: %s\n", snapTrioAPI)
		trioClient := vision.NewTrioClient(snapTrioAPI)

		if err := trioClient.HealthCheck(ctx); err != nil {
			return fmt.Errorf("Trio API health check failed: %w", err)
		}
		fmt.Println("✓ Trio API is reachable")
	}

	return nil
}

// =============================================================================
// trioclaw doctor
//
// Checks all dependencies and available devices. Exits 0 if everything is OK.
//
// Checks:
//   ✓/✗ ffmpeg binary found (with version)
//   ✓/✗ Cameras detected (list them)
//   ✓/✗ Microphone detected
//   ✓/✗ Trio API reachable (trio.machinefi.com/healthz)
//   ✓/✗ Gateway configured (state.json exists with token)
// =============================================================================

var doctorTrioAPI string

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check dependencies and devices",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDoctor(cmd.Context())
	},
}

func runDoctor(ctx context.Context) error {
	fmt.Println("TrioClaw Doctor")
	fmt.Println("===============")
	fmt.Println()

	allOK := true

	// Check ffmpeg
	fmt.Print("ffmpeg: ")
	ffmpegVer, err := capture.CheckFFmpeg()
	if err != nil {
		fmt.Printf("✗ %v\n", err)
		allOK = false
	} else {
		fmt.Printf("✓ %s\n", ffmpegVer)
	}

	// Check devices
	fmt.Print("\nDevices:\n")
	devices, err := capture.ListDevices()
	if err != nil {
		fmt.Printf("  ✗ Failed to list devices: %v\n", err)
		allOK = false
	} else if len(devices) == 0 {
		fmt.Printf("  ✗ No devices found\n")
		allOK = false
	} else {
		hasVideo := false
		hasAudio := false
		for _, dev := range devices {
			sym := "  "
			if dev.Type == "video" {
				sym = "📷 "
				hasVideo = true
			} else if dev.Type == "audio" {
				sym = "🎤 "
				hasAudio = true
			}
			fmt.Printf("  %s%s: %s (%s)\n", sym, dev.ID, dev.Name, dev.Platform)
		}
		if !hasVideo {
			fmt.Println("  ✗ No cameras found")
			allOK = false
		}
		if !hasAudio {
			fmt.Println("  ⚠ No microphones found (optional)")
		}
	}

	// Check Trio API
	fmt.Print("\nTrio API: ")
	trioAPI := doctorTrioAPI
	if trioAPI == "" {
		trioAPI = vision.DefaultTrioAPIURL
	}
	trioClient := vision.NewTrioClient(trioAPI)

	// Use a shorter timeout for health check
	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := trioClient.HealthCheck(healthCtx); err != nil {
		fmt.Printf("✗ %v\n", err)
		allOK = false
	} else {
		fmt.Printf("✓ %s\n", trioAPI)
	}

	// Check state
	fmt.Print("\nGateway: ")
	st, err := state.Load()
	if err != nil {
		fmt.Printf("✗ Failed to load state: %v\n", err)
		allOK = false
	} else if !st.IsPaired() {
		fmt.Println("✗ Not paired (run 'trioclaw pair --gateway <url>')")
	} else {
		fmt.Printf("✓ Paired to %s\n", st.GatewayURL)
		fmt.Printf("  Node ID: %s\n", st.NodeID)
		fmt.Printf("  Display Name: %s\n", st.DisplayName)
	}

	// Summary
	fmt.Println()
	if allOK {
		fmt.Println("✓ All checks passed!")
		return nil
	}

	fmt.Println("✗ Some checks failed")
	return fmt.Errorf("doctor checks failed")
}

// =============================================================================
// trioclaw version
// =============================================================================

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("TrioClaw version %s\n", version)
		if commit != "unknown" {
			fmt.Printf("Commit: %s\n", commit)
		}
		fmt.Printf("Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		fmt.Printf("Go version: %s\n", runtime.Version())
	},
}

// =============================================================================
// trioclaw update
//
// Self-update: checks GitHub for the latest release and replaces the current
// binary. Uses the same install.sh logic but as a native Go command.
// =============================================================================

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update TrioClaw to the latest version",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUpdate()
	},
}

func runUpdate() error {
	fmt.Printf("Current version: %s\n", version)

	// Fetch latest release from GitHub API
	fmt.Println("Checking for updates...")

	resp, err := http.Get("https://api.github.com/repos/machinefi/TrioClaw/releases/latest")
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("GitHub API returned %d — no releases published yet?", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("failed to parse release info: %w", err)
	}

	latest := release.TagName
	// Strip leading "v" for comparison
	latestClean := latest
	if len(latestClean) > 0 && latestClean[0] == 'v' {
		latestClean = latestClean[1:]
	}

	if latestClean == version {
		fmt.Printf("Already up to date (%s)\n", version)
		return nil
	}

	fmt.Printf("New version available: %s → %s\n", version, latest)

	// Build download URL
	platform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	filename := fmt.Sprintf("trioclaw-%s%s", platform, suffix)
	downloadURL := fmt.Sprintf("https://github.com/machinefi/TrioClaw/releases/download/%s/%s", latest, filename)

	fmt.Printf("Downloading %s...\n", downloadURL)

	// Download to temp file
	dlResp, err := http.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != 200 {
		return fmt.Errorf("download failed (HTTP %d). Asset '%s' may not exist for this release", dlResp.StatusCode, filename)
	}

	tmpFile, err := os.CreateTemp("", "trioclaw-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := io.Copy(tmpFile, dlResp.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("download failed: %w", err)
	}
	tmpFile.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Find current binary path
	currentBin, err := os.Executable()
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("could not find current binary path: %w", err)
	}

	// Replace current binary
	// Try direct rename first (same filesystem), fall back to sudo
	if err := os.Rename(tmpPath, currentBin); err != nil {
		// Likely a permission issue — try sudo mv
		fmt.Println("Requires elevated permissions...")
		sudoCmd := exec.Command("sudo", "mv", tmpPath, currentBin)
		sudoCmd.Stdin = os.Stdin
		sudoCmd.Stdout = os.Stdout
		sudoCmd.Stderr = os.Stderr
		if err := sudoCmd.Run(); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("failed to install update: %w", err)
		}
	}

	fmt.Printf("\nUpdated to %s\n", latest)
	return nil
}

// =============================================================================
// Init: wire up all commands and flags
// =============================================================================

func init() {
	// pair command flags
	pairCmd.Flags().StringVar(&pairGatewayURL, "gateway", "", "OpenClaw Gateway WebSocket URL (required)")
	pairCmd.Flags().StringVar(&pairDisplayName, "name", "", "Display name for this node (default: hostname)")
	pairCmd.MarkFlagRequired("gateway")

	// run command flags
	runCmd.Flags().StringArrayVar(&runCameras, "camera", nil, "Additional camera source (rtsp:// URL or device path)")
	runCmd.Flags().StringVar(&runHAURL, "ha-url", "", "Home Assistant URL (e.g. http://homeassistant.local:8123)")
	runCmd.Flags().StringVar(&runHAToken, "ha-token", "", "Home Assistant long-lived access token")
	runCmd.Flags().StringVar(&runPluginDir, "plugin-dir", "", "Directory for exec plugin scripts (default: ~/.trioclaw/plugins/)")

	// snap command flags
	snapCmd.Flags().StringVar(&snapCamera, "camera", "", "Camera source (default: built-in webcam)")
	snapCmd.Flags().StringVar(&snapAnalyze, "analyze", "", "Question to ask about the captured frame (uses Trio API)")
	snapCmd.Flags().StringVar(&snapOutput, "output", "", "Output file path (default: frame.jpg)")
	snapCmd.Flags().StringVar(&snapTrioAPI, "trio-api", "", "Trio API URL (default: https://trio.machinefi.com)")

	// doctor command flags
	doctorCmd.Flags().StringVar(&doctorTrioAPI, "trio-api", "", "Trio API URL to check (default: https://trio.machinefi.com)")

	// Add commands
	rootCmd.AddCommand(pairCmd, runCmd, snapCmd, doctorCmd, versionCmd, updateCmd)
}

// nodeCapabilities returns caps and commands to advertise during pairing.
func nodeCapabilities() (caps []string, commands []string) {
	caps = []string{"camera", "device"}
	commands = []string{
		"camera.snap",
		"camera.list",
		"camera.clip",
		"vision.analyze",
		"device.list",
		"device.control",
	}
	return
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
