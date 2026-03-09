// Package triocore setup manages the trio-core lifecycle.
//
// When trioclaw starts, it checks if trio-core is reachable.
// If not, it prompts the user to either:
//   - Install and run trio-core locally (requires Python + pip/uv)
//   - Use the cloud trio-core at trio.machinefi.com
//
// For local installation, it:
//   1. Detects Python (python3) and package manager (uv > pip)
//   2. Installs trio-core via pip/uv
//   3. Starts trio-core as a managed subprocess
//   4. Waits for healthz to respond
//   5. Kills the subprocess on context cancellation
package triocore

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	cloudTrioCoreURL   = "https://trio.machinefi.com"
	defaultLocalURL    = "http://localhost:8000"
	trioCorePackage    = "trio-core"
	healthWaitTimeout  = 30 * time.Second
	healthPollInterval = 500 * time.Millisecond
)

// SetupResult is returned by EnsureTrioCore.
type SetupResult struct {
	URL     string       // the trio-core URL to use
	Process *os.Process  // non-nil if we started a local process (caller should manage lifecycle)
	IsCloud bool         // true if using cloud
}

// EnsureTrioCore checks if trio-core is reachable at the given URL.
// If not, prompts the user to install locally or use cloud.
// Returns the URL to use and optionally a managed process.
func EnsureTrioCore(ctx context.Context, configURL string) (*SetupResult, error) {
	// First try the configured URL
	if isReachable(ctx, configURL) {
		return &SetupResult{URL: configURL}, nil
	}

	fmt.Printf("\ntrio-core is not reachable at %s\n", configURL)
	fmt.Println()
	fmt.Println("trio-core is the AI inference engine that powers vision monitoring.")
	fmt.Println("You have two options:")
	fmt.Println()
	fmt.Println("  1) Install locally  — runs on your machine, needs Python 3.10+")
	fmt.Println("                        best for: GPU acceleration, privacy, no internet needed")
	fmt.Println()
	fmt.Println("  2) Use cloud        — connects to trio.machinefi.com")
	fmt.Println("                        best for: quick start, no setup, no GPU needed")
	fmt.Println()

	choice := promptChoice("Choose [1/2] (default: 2): ", "2")

	switch choice {
	case "1":
		return setupLocal(ctx)
	default:
		// Verify cloud is reachable
		if !isReachable(ctx, cloudTrioCoreURL) {
			return nil, fmt.Errorf("cloud trio-core at %s is not reachable — check your internet connection", cloudTrioCoreURL)
		}
		fmt.Printf("Using cloud trio-core: %s\n", cloudTrioCoreURL)
		return &SetupResult{URL: cloudTrioCoreURL, IsCloud: true}, nil
	}
}

// setupLocal installs trio-core locally and starts it as a subprocess.
func setupLocal(ctx context.Context) (*SetupResult, error) {
	// Step 1: Check Python
	pythonBin := detectPython()
	if pythonBin == "" {
		return nil, fmt.Errorf("Python 3.10+ is required but not found.\n" +
			"Install Python: https://www.python.org/downloads/\n" +
			"Or use cloud trio-core: trioclaw run --trio-api " + cloudTrioCoreURL)
	}
	fmt.Printf("Found: %s\n", pythonBin)

	// Step 2: Check if trio-core is already installed
	if !isTrioCoreInstalled(pythonBin) {
		fmt.Println("\nInstalling trio-core...")
		if err := installTrioCore(pythonBin); err != nil {
			return nil, fmt.Errorf("failed to install trio-core: %w\n"+
				"Try manually: pip install trio-core", err)
		}
		fmt.Println("trio-core installed successfully")
	} else {
		fmt.Println("trio-core is already installed")
	}

	// Step 3: Start trio-core
	fmt.Println("\nStarting trio-core on :8000...")
	proc, err := startTrioCore(ctx, pythonBin)
	if err != nil {
		return nil, fmt.Errorf("failed to start trio-core: %w", err)
	}

	// Step 4: Wait for healthz
	if err := waitForHealth(ctx, defaultLocalURL); err != nil {
		proc.Kill()
		return nil, fmt.Errorf("trio-core started but not responding: %w", err)
	}

	fmt.Println("trio-core is running")
	return &SetupResult{URL: defaultLocalURL, Process: proc}, nil
}

// detectPython finds a suitable Python 3 binary.
func detectPython() string {
	for _, name := range []string{"python3", "python"} {
		bin, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		// Check version >= 3.10
		out, err := exec.Command(bin, "--version").Output()
		if err != nil {
			continue
		}
		version := strings.TrimSpace(string(out))
		if strings.HasPrefix(version, "Python 3.") {
			// Extract minor version
			parts := strings.Split(version, ".")
			if len(parts) >= 2 {
				minor := 0
				fmt.Sscanf(parts[1], "%d", &minor)
				if minor >= 10 {
					return bin
				}
			}
		}
	}
	return ""
}

// detectPackageManager finds uv or pip.
func detectPackageManager() (bin string, isUV bool) {
	if uv, err := exec.LookPath("uv"); err == nil {
		return uv, true
	}
	if pip, err := exec.LookPath("pip3"); err == nil {
		return pip, false
	}
	if pip, err := exec.LookPath("pip"); err == nil {
		return pip, false
	}
	return "", false
}

// isTrioCoreInstalled checks if trio-core package is installed.
func isTrioCoreInstalled(pythonBin string) bool {
	cmd := exec.Command(pythonBin, "-c", "import trio_core; print('ok')")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "ok"
}

// installTrioCore installs the trio-core package.
func installTrioCore(pythonBin string) error {
	pkgMgr, isUV := detectPackageManager()

	var cmd *exec.Cmd
	if isUV {
		cmd = exec.Command(pkgMgr, "pip", "install", trioCorePackage)
	} else if pkgMgr != "" {
		cmd = exec.Command(pkgMgr, "install", trioCorePackage)
	} else {
		// Fallback to python -m pip
		cmd = exec.Command(pythonBin, "-m", "pip", "install", trioCorePackage)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// startTrioCore starts trio-core as a background process.
func startTrioCore(ctx context.Context, pythonBin string) (*os.Process, error) {
	// Try `trio-core serve` first (if installed as CLI), then `python -m trio_core`
	var cmd *exec.Cmd

	if trioBin, err := exec.LookPath("trio-core"); err == nil {
		cmd = exec.CommandContext(ctx, trioBin, "serve", "--port", "8000")
	} else {
		cmd = exec.CommandContext(ctx, pythonBin, "-m", "trio_core", "serve", "--port", "8000")
	}

	// Pipe output for debugging
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Log trio-core output in background
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			log.Printf("[trio-core] %s", scanner.Text())
		}
	}()
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Printf("[trio-core] %s", scanner.Text())
		}
	}()

	return cmd.Process, nil
}

// waitForHealth polls /healthz until it responds or times out.
func waitForHealth(ctx context.Context, baseURL string) error {
	deadline := time.After(healthWaitTimeout)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timeout waiting for trio-core at %s (waited %v)", baseURL, healthWaitTimeout)
		default:
			if isReachable(ctx, baseURL) {
				return nil
			}
			time.Sleep(healthPollInterval)
		}
	}
}

// isReachable checks if a trio-core instance is reachable via /healthz.
func isReachable(ctx context.Context, baseURL string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/healthz", nil)
	if err != nil {
		return false
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// promptChoice reads a single line from stdin.
func promptChoice(prompt, defaultVal string) string {
	fmt.Print(prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text != "" {
			return text
		}
	}
	return defaultVal
}

// StopProcess gracefully stops a managed trio-core process.
func StopProcess(proc *os.Process) {
	if proc == nil {
		return
	}
	log.Println("[trio-core] stopping...")
	proc.Signal(os.Interrupt)
	// Give it a moment to clean up
	done := make(chan struct{})
	go func() {
		proc.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		proc.Kill()
	}
}
