// Package main is the entry point for TrioClaw.
//
// Commands:
//   trioclaw pair      --gateway ws://host:18789   Pair with OpenClaw Gateway
//   trioclaw run       [--config config.yaml]       Start as long-running service
//   trioclaw snap      [--camera ...] [--analyze q] One-shot capture + optional VLM
//   trioclaw camera    add|remove|list              Manage cameras
//   trioclaw doctor                                 Check dependencies & devices
//   trioclaw update                                 Self-update to latest release
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"encoding/base64"

	"github.com/machinefi/trioclaw/internal/capture"
	"github.com/machinefi/trioclaw/internal/config"
	"github.com/machinefi/trioclaw/internal/gateway"
	"github.com/machinefi/trioclaw/internal/notify"
	pluginpkg "github.com/machinefi/trioclaw/internal/plugin"
	"github.com/machinefi/trioclaw/internal/plugin/execplugin"
	ha "github.com/machinefi/trioclaw/internal/plugin/homeassistant"
	"github.com/machinefi/trioclaw/internal/state"
	"github.com/machinefi/trioclaw/internal/store"
	"github.com/machinefi/trioclaw/internal/triocore"
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
// trioclaw run [--config config.yaml] [--camera rtsp://...]
//
// Long-running service mode. Starts two subsystems in parallel:
//   1. Watch Manager: connects to trio-core SSE for each configured camera,
//      stores results/alerts in SQLite, pushes alerts to gateway
//   2. Gateway Client: connects to OpenClaw gateway (if paired),
//      handles invoke commands from agents
//
// Lifecycle:
//   1. Load YAML config (~/.trioclaw/config.yaml)
//   2. Open SQLite event store
//   3. Start watch manager (SSE per camera → SQLite + gateway alerts)
//   4. Connect to OpenClaw gateway (if paired)
//   5. Block until SIGINT/SIGTERM → graceful shutdown
// =============================================================================

var runConfigPath string // config file path
var runCameras []string  // additional camera sources (rtsp:// or device paths)
var runTrioAPI string    // Trio API URL (overrides config)
var runHAURL string      // Home Assistant URL
var runHAToken string    // Home Assistant long-lived access token
var runPluginDir string  // custom plugin scripts directory

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start TrioClaw service (trio-core SSE + OpenClaw gateway)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRun(cmd.Context())
	},
}

func runRun(ctx context.Context) error {
	// Load config
	var cfg *config.Config
	var err error
	if runConfigPath != "" {
		cfg, err = config.LoadFrom(runConfigPath)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// CLI flag overrides
	if runTrioAPI != "" {
		cfg.TrioCore.URL = runTrioAPI
	}

	// Add CLI --camera flags as cameras (for backward compat)
	for i, cam := range runCameras {
		_ = cfg.AddCamera(config.CameraConfig{
			ID:     fmt.Sprintf("cli-%d", i),
			Name:   fmt.Sprintf("CLI Camera %d", i+1),
			Source: cam,
			FPS:    1,
		})
	}

	fmt.Printf("trio-core: %s\n", cfg.TrioCore.URL)
	fmt.Printf("cameras:   %d configured\n", len(cfg.Cameras))
	for _, cam := range cfg.Cameras {
		fmt.Printf("  - %s: %s (%s)\n", cam.ID, cam.Name, maskCredentials(cam.Source))
	}

	// Open event store
	eventStore, err := store.Open(store.DefaultDBPath())
	if err != nil {
		return fmt.Errorf("open event store: %w", err)
	}
	defer eventStore.Close()
	fmt.Printf("events db: %s\n", store.DefaultDBPath())

	// Discover local devices (for gateway handler)
	devices, err := capture.ListDevices()
	if err != nil {
		fmt.Printf("warning: failed to list devices: %v\n", err)
	} else {
		fmt.Printf("devices:   %d found\n", len(devices))
	}

	// Create Trio API client (for gateway invoke handler, backward compat)
	trioClient := vision.NewTrioClient(runTrioAPI)

	// Create gateway handler
	handler := gateway.NewHandler(devices, trioClient, runCameras)

	// Setup plugins (Hands)
	registry := pluginpkg.NewRegistry()
	if runHAURL != "" && runHAToken != "" {
		haPlugin := ha.New(runHAURL, runHAToken)
		if err := registry.Register(haPlugin); err != nil {
			return fmt.Errorf("register HA plugin: %w", err)
		}
		fmt.Printf("plugin:    Home Assistant (%s)\n", runHAURL)
	}
	execPlugin, err := execplugin.New(runPluginDir)
	if err == nil {
		_ = registry.Register(execPlugin)
		if execPlugin.ScriptCount() > 0 {
			fmt.Printf("plugin:    exec (%d scripts)\n", execPlugin.ScriptCount())
		}
	}
	handler.SetPlugins(registry)

	// Setup signal handling
	ctx, cancel := context.WithCancel(ctx)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nshutting down...")
		cancel()
	}()

	// ---------------------------------------------------------------
	// Setup notification dispatcher
	// ---------------------------------------------------------------
	dispatcher := notify.NewDispatcher()
	if cfg.Notifications.Webhook != nil {
		dispatcher.Register(notify.NewWebhook(cfg.Notifications.Webhook.URL, cfg.Notifications.Webhook.Headers))
	}
	if cfg.Notifications.Telegram != nil {
		dispatcher.Register(notify.NewTelegram(cfg.Notifications.Telegram.BotToken, cfg.Notifications.Telegram.ChatID))
	}
	if cfg.Notifications.Slack != nil {
		dispatcher.Register(notify.NewSlack(cfg.Notifications.Slack.WebhookURL))
	}

	// Build condition -> actions lookup from config
	cameraNames := make(map[string]string) // cameraID -> display name
	for _, cam := range cfg.Cameras {
		cameraNames[cam.ID] = cam.Name
		for _, cond := range cam.Conditions {
			if len(cond.Actions) > 0 {
				dispatcher.SetActions(cam.ID, cond.ID, cond.Actions)
			}
		}
	}

	// ---------------------------------------------------------------
	// Start trio-core watch manager (SSE streams for all cameras)
	// ---------------------------------------------------------------
	tcClient := triocore.NewClient(cfg.TrioCore.URL)
	watchMgr := triocore.NewManager(tcClient, cfg.Cameras)

	// Store all results in SQLite
	watchMgr.OnResult(func(cameraID string, result triocore.ResultEvent) {
		for _, cond := range result.Conditions {
			_, err := eventStore.InsertEvent(&store.Event{
				Timestamp:   parseTS(result.Timestamp),
				CameraID:    cameraID,
				WatchID:     result.WatchID,
				ConditionID: cond.ID,
				Answer:      cond.Answer,
				Triggered:   cond.Triggered,
				LatencyMs:   result.Metrics.LatencyMs,
				FramesUsed:  result.Metrics.FramesAnalyzed,
			})
			if err != nil {
				log.Printf("[store] insert result error: %v", err)
			}
		}
	})

	// Store alerts + notify + push to gateway
	watchMgr.OnAlert(func(cameraID string, alert triocore.AlertEvent) {
		// Decode frame for notifications
		var frameJPEG []byte
		if alert.FrameB64 != "" {
			frameJPEG, _ = base64.StdEncoding.DecodeString(alert.FrameB64)
		}

		for _, cond := range alert.Conditions {
			if !cond.Triggered {
				continue
			}

			// Store in SQLite
			_, err := eventStore.InsertAlert(&store.Event{
				Timestamp:   parseTS(alert.Timestamp),
				CameraID:    cameraID,
				WatchID:     alert.WatchID,
				ConditionID: cond.ID,
				Answer:      cond.Answer,
				Triggered:   true,
				LatencyMs:   alert.Metrics.LatencyMs,
				FramesUsed:  alert.Metrics.FramesAnalyzed,
			})
			if err != nil {
				log.Printf("[store] insert alert error: %v", err)
			}

			// Dispatch notifications
			dispatcher.DispatchForCondition(ctx, cameraID, cond.ID, notify.Alert{
				CameraID:    cameraID,
				CameraName:  cameraNames[cameraID],
				ConditionID: cond.ID,
				Answer:      cond.Answer,
				Timestamp:   parseTS(alert.Timestamp),
				FrameJPEG:   frameJPEG,
			})
		}

		// Push alert to OpenClaw gateway (if connected)
		if gwClient != nil {
			_ = gwClient.SendEvent("vision.alert", map[string]any{
				"camera_id":  cameraID,
				"watch_id":   alert.WatchID,
				"ts":         alert.Timestamp,
				"conditions": alert.Conditions,
				"frame_b64":  alert.FrameB64,
			})
		}
	})

	// Pass watch manager and dispatcher to gateway handler
	handler.SetWatchManager(watchMgr)
	handler.SetDispatcher(dispatcher)

	// Run watch manager in background
	go func() {
		if err := watchMgr.Run(ctx); err != nil {
			log.Printf("[watch-manager] error: %v", err)
		}
	}()
	fmt.Println("\nstarted watch manager")

	// ---------------------------------------------------------------
	// Start OpenClaw gateway connection (if paired)
	// ---------------------------------------------------------------
	st, err := state.Load()
	if err == nil && st.IsPaired() {
		gwClient = gateway.NewClient(st.GatewayURL, st.Token)
		gwClient.SetNodeID(st.NodeID)

		fmt.Printf("\ngateway:   %s (node: %s)\n", st.GatewayURL, st.NodeID)

		go func() {
			if err := gwClient.Run(ctx, handler); err != nil {
				log.Printf("[gateway] error: %v", err)
			}
		}()
		fmt.Println("started gateway connection")
	} else {
		fmt.Println("\ngateway:   not paired (run 'trioclaw pair' to connect)")
	}

	fmt.Println("\ntrioclaw is running. Press Ctrl+C to stop.")

	// Block until shutdown
	<-ctx.Done()
	return nil
}

// gwClient is the active gateway client (nil if not paired).
// Used to push proactive events from watch alerts.
var gwClient *gateway.Client

func parseTS(ts string) time.Time {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Now()
	}
	return t
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
// trioclaw camera add|remove|list
//
// Manage cameras in config.yaml.
// =============================================================================

var cameraCmd = &cobra.Command{
	Use:   "camera",
	Short: "Manage cameras",
}

var cameraAddID string
var cameraAddName string
var cameraAddSource string
var cameraAddQuestion string

var cameraAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a camera",
	Long:  "Add a camera to config. Example: trioclaw camera add --id front-door --name \"Front Door\" --source rtsp://admin:pass@192.168.1.10:554/stream",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		cam := config.CameraConfig{
			ID:     cameraAddID,
			Name:   cameraAddName,
			Source: cameraAddSource,
			FPS:    1,
		}

		// Add default condition if question provided
		if cameraAddQuestion != "" {
			cam.Conditions = []config.ConditionConfig{
				{
					ID:       "default",
					Question: cameraAddQuestion,
				},
			}
		}

		if err := cfg.AddCamera(cam); err != nil {
			return err
		}

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Printf("Added camera %q (%s)\n", cam.ID, maskCredentials(cam.Source))
		fmt.Printf("Config saved to: %s\n", config.DefaultConfigPath())
		return nil
	},
}

var cameraRemoveCmd = &cobra.Command{
	Use:   "remove [camera-id]",
	Short: "Remove a camera",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		if err := cfg.RemoveCamera(args[0]); err != nil {
			return err
		}

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Printf("Removed camera %q\n", args[0])
		return nil
	},
}

var cameraListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured cameras",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		if len(cfg.Cameras) == 0 {
			fmt.Println("No cameras configured.")
			fmt.Println("Add one: trioclaw camera add --id my-cam --name \"My Camera\" --source rtsp://...")
			return nil
		}

		fmt.Printf("Cameras (%d):\n", len(cfg.Cameras))
		for _, cam := range cfg.Cameras {
			// Mask credentials in source URL for display
			displaySource := maskCredentials(cam.Source)
			fmt.Printf("  %s\n", cam.ID)
			fmt.Printf("    name:   %s\n", cam.Name)
			fmt.Printf("    source: %s\n", displaySource)
			if len(cam.Conditions) > 0 {
				fmt.Printf("    conditions:\n")
				for _, cond := range cam.Conditions {
					actions := ""
					if len(cond.Actions) > 0 {
						actions = fmt.Sprintf(" -> [%s]", strings.Join(cond.Actions, ", "))
					}
					fmt.Printf("      - %s: %q%s\n", cond.ID, cond.Question, actions)
				}
			}
		}
		return nil
	},
}

// maskCredentials replaces user:pass in URLs with ***:***
func maskCredentials(source string) string {
	if !strings.Contains(source, "@") {
		return source
	}
	// Find :// then mask until @
	idx := strings.Index(source, "://")
	if idx < 0 {
		return source
	}
	prefix := source[:idx+3]
	rest := source[idx+3:]
	atIdx := strings.Index(rest, "@")
	if atIdx < 0 {
		return source
	}
	return prefix + "***:***@" + rest[atIdx+1:]
}

// =============================================================================
// trioclaw status
//
// Show current service status, active watches, recent alerts.
// =============================================================================

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show service status and recent activity",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Load config
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		fmt.Printf("trio-core: %s\n", cfg.TrioCore.URL)
		fmt.Printf("cameras:   %d configured\n", len(cfg.Cameras))

		// Load state
		st, err := state.Load()
		if err == nil && st.IsPaired() {
			fmt.Printf("gateway:   %s (paired)\n", st.GatewayURL)
		} else {
			fmt.Println("gateway:   not paired")
		}

		// Show store stats
		eventStore, err := store.Open(store.DefaultDBPath())
		if err != nil {
			fmt.Printf("events db: error: %v\n", err)
			return nil
		}
		defer eventStore.Close()

		stats, err := eventStore.GetStats()
		if err != nil {
			fmt.Printf("events db: error: %v\n", err)
			return nil
		}

		fmt.Printf("\nevents:    %d total, %d alerts, %d cameras\n",
			stats.TotalEvents, stats.TotalAlerts, stats.CameraCount)

		// Show recent alerts
		alerts, err := eventStore.RecentAlerts(5)
		if err == nil && len(alerts) > 0 {
			fmt.Println("\nrecent alerts:")
			for _, a := range alerts {
				fmt.Printf("  [%s] %s/%s: %s\n",
					a.Timestamp.Local().Format("2006-01-02 15:04:05"),
					a.CameraID, a.ConditionID, a.Answer)
			}
		}

		return nil
	},
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
	runCmd.Flags().StringVar(&runConfigPath, "config", "", "Config file path (default: ~/.trioclaw/config.yaml)")
	runCmd.Flags().StringArrayVar(&runCameras, "camera", nil, "Additional camera source (rtsp:// URL or device path)")
	runCmd.Flags().StringVar(&runTrioAPI, "trio-api", "", "Trio-core URL (overrides config)")
	runCmd.Flags().StringVar(&runHAURL, "ha-url", "", "Home Assistant URL (e.g. http://homeassistant.local:8123)")
	runCmd.Flags().StringVar(&runHAToken, "ha-token", "", "Home Assistant long-lived access token")
	runCmd.Flags().StringVar(&runPluginDir, "plugin-dir", "", "Directory for exec plugin scripts (default: ~/.trioclaw/plugins/)")

	// camera command flags
	cameraAddCmd.Flags().StringVar(&cameraAddID, "id", "", "Camera ID (required)")
	cameraAddCmd.Flags().StringVar(&cameraAddName, "name", "", "Camera display name (required)")
	cameraAddCmd.Flags().StringVar(&cameraAddSource, "source", "", "Camera source: RTSP URL or device path (required)")
	cameraAddCmd.Flags().StringVar(&cameraAddQuestion, "question", "", "Default question to monitor (optional)")
	cameraAddCmd.MarkFlagRequired("id")
	cameraAddCmd.MarkFlagRequired("name")
	cameraAddCmd.MarkFlagRequired("source")

	cameraCmd.AddCommand(cameraAddCmd, cameraRemoveCmd, cameraListCmd)

	// snap command flags
	snapCmd.Flags().StringVar(&snapCamera, "camera", "", "Camera source (default: built-in webcam)")
	snapCmd.Flags().StringVar(&snapAnalyze, "analyze", "", "Question to ask about the captured frame (uses Trio API)")
	snapCmd.Flags().StringVar(&snapOutput, "output", "", "Output file path (default: frame.jpg)")
	snapCmd.Flags().StringVar(&snapTrioAPI, "trio-api", "", "Trio API URL (default: https://trio.machinefi.com)")

	// doctor command flags
	doctorCmd.Flags().StringVar(&doctorTrioAPI, "trio-api", "", "Trio API URL to check (default: https://trio.machinefi.com)")

	// Add commands
	rootCmd.AddCommand(pairCmd, runCmd, snapCmd, cameraCmd, statusCmd, doctorCmd, versionCmd, updateCmd)
}

// nodeCapabilities returns caps and commands to advertise during pairing.
func nodeCapabilities() (caps []string, commands []string) {
	caps = []string{"camera", "vision", "device"}
	commands = []string{
		"camera.snap",
		"camera.list",
		"camera.clip",
		"vision.analyze",
		"vision.watch",
		"vision.watch.stop",
		"vision.status",
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
