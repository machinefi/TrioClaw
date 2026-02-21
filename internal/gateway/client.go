// client.go implements a WebSocket connection to the OpenClaw Gateway.
//
// Responsibilities:
//   - WebSocket connect/reconnect with exponential backoff
//   - Challenge-response handshake
//   - Pairing protocol (first-time device registration)
//   - Hello authentication (subsequent connections with saved token)
//   - Ping/pong keepalive (30s interval)
//   - Frame read loop → dispatch to Handler
//   - Graceful shutdown on context cancellation
//
// Usage:
//
//	client := gateway.NewClient("ws://host:18789", "saved-token")
//	handler := gateway.NewHandler(devices, trioClient)
//	err := client.Run(ctx, handler) // blocks until ctx cancelled
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// pingInterval is the interval between ping messages
	pingInterval = 30 * time.Second

	// maxBackoff is the maximum reconnection delay
	maxBackoff = 15 * time.Second

	// pairTimeout is how long to wait for pairing approval
	pairTimeout = 5 * time.Minute

	// writeTimeout is the timeout for writing to the WebSocket
	writeTimeout = 5 * time.Second

	// readTimeout is the timeout for reading from the WebSocket
	readTimeout = 60 * time.Second
)

// Client manages the WebSocket connection to the OpenClaw Gateway.
type Client struct {
	url   string       // ws://host:18789
	token string       // device token from pairing (empty if not yet paired)
	nodeID string       // this node's ID

	conn      *websocket.Conn
	connMu    sync.Mutex
	connected bool

	pairCh     chan pairResult // channel for pairing result
	cancelPing context.CancelFunc
}

type pairResult struct {
	token string
	err   error
}

// NewClient creates a new Gateway client.
// token can be empty for initial pairing.
func NewClient(url string, token string) *Client {
	return &Client{
		url:      url,
		token:     token,
		pairCh:    make(chan pairResult, 1),
		connected: false,
	}
}

// SetNodeID sets the node ID for this client.
func (c *Client) SetNodeID(nodeID string) {
	c.nodeID = nodeID
}

// Pair sends a pairing request and waits for operator approval.
//
// Flow:
//  1. WebSocket dial to gateway URL
//  2. Receive connect.challenge event, extract nonce
//  3. Send "connect" request (without auth token)
//  4. Send "node.pair.request" with displayName, caps, commands
//  5. Wait for "device.pair.resolved" event (up to 5 minute timeout)
//  6. On "approved": extract and return the device token
//  7. On "rejected" or timeout: return error
//
// The caller should save the returned token via state.Save().
func (c *Client) Pair(ctx context.Context, displayName string, caps []string, commands []string) (string, error) {
	// Generate a random request ID
	reqID := generateID()

	// Create context with timeout
	pairCtx, cancel := context.WithTimeout(ctx, pairTimeout)
	defer cancel()

	// Connect to gateway
	if err := c.connect(pairCtx, false); err != nil {
		return "", fmt.Errorf("failed to connect to gateway: %w", err)
	}

	// Clear pair channel
	select {
	case <-c.pairCh:
	default:
	}

	// Send node.pair.request
	pairReq := PairRequestParams{
		NodeID:      c.nodeID,
		DisplayName: displayName,
		Platform:    getPlatform(),
		Version:     "0.1.0",
		DeviceFamily: "trioclaw",
		Caps:        caps,
		Commands:    commands,
		Silent:      false,
	}

	params, _ := json.Marshal(pairReq)
	req := ReqFrame{
		Type:   "req",
		ID:     reqID,
		Method: "node.pair.request",
		Params: params,
	}

	if err := c.sendFrame(req); err != nil {
		return "", fmt.Errorf("failed to send pair request: %w", err)
	}

	// Wait for pairing result
	select {
	case result := <-c.pairCh:
		if result.err != nil {
			return "", result.err
		}
		return result.token, nil
	case <-pairCtx.Done():
		c.Close()
		return "", fmt.Errorf("pairing timed out (operator may not have approved within %v)", pairTimeout)
	}
}

// Run connects to the gateway and enters the main event loop.
// Blocks until ctx is cancelled. Handles reconnection automatically.
//
// Flow:
//  1. WebSocket dial
//  2. Receive connect.challenge, send "connect" with auth token
//  3. Receive hello-ok → connected
//  4. Start ping goroutine (every 30s)
//  5. Read frames in a loop:
//     - event "node.invoke.request" → handler.HandleInvoke()
//     - event "tick" → update last-seen timestamp
//     - req "ping" → reply with pong
//  6. On connection error → reconnect with backoff (1s, 2s, 4s, 8s, 15s max)
//  7. On ctx.Done() → close WebSocket, return nil
func (c *Client) Run(ctx context.Context, handler *Handler) error {
	// Set node ID on handler
	handler.SetNodeID(c.nodeID)

	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			// Context cancelled, exit
			c.Close()
			return nil

		default:
			// Try to connect
			if err := c.connect(ctx, true); err != nil {
				fmt.Printf("Connection failed: %v, retrying in %v...\n", err, backoff)
				select {
				case <-time.After(backoff):
					backoff = time.Duration(float64(backoff) * 1.5)
					if backoff > maxBackoff {
						backoff = maxBackoff
					}
				case <-ctx.Done():
					return nil
				}
				continue
			}

			// Connected successfully
			fmt.Println("Connected to Gateway")
			backoff = time.Second

			// Start ping goroutine
			pingCtx, pingCancel := context.WithCancel(ctx)
			c.cancelPing = pingCancel
			go c.pingLoop(pingCtx)

			// Read loop
			if err := c.readLoop(ctx, handler); err != nil {
				fmt.Printf("Disconnected: %v\n", err)
			}

			// Stop ping
			pingCancel()

			// Reset for reconnection
			c.closeConnection()
		}
	}
}

// Close gracefully disconnects from the gateway.
func (c *Client) Close() error {
	c.closeConnection()
	if c.cancelPing != nil {
		c.cancelPing()
	}
	return nil
}

// SendInvokeResult sends the result of an invoke command back to the gateway.
//
// This is called by the Handler after processing a command.
// Sends a "req" frame with method "node.invoke.result".
func (c *Client) SendInvokeResult(result InvokeResult) error {
	req := ReqFrame{
		Type:   "req",
		ID:     generateID(),
		Method: "node.invoke.result",
	}

	params, _ := json.Marshal(result)
	req.Params = params

	return c.sendFrame(req)
}

// SendEvent sends a proactive event to the gateway.
//
// Used for vision triggers, status updates, etc.
// Example: agent.request event when vision.watch detects something.
func (c *Client) SendEvent(event string, payload interface{}) error {
	// Double-encode payload
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	frame := EventFrame{
		Type:        "event",
		Event:       event,
		PayloadJSON: string(payloadJSON),
	}

	return c.sendFrame(frame)
}

// sendFrame sends a raw JSON frame over the WebSocket (thread-safe).
func (c *Client) sendFrame(frame interface{}) error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.conn == nil {
		return fmt.Errorf("not connected")
	}

	c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.conn.WriteJSON(frame)
}

// connect performs WebSocket dial + challenge/hello handshake.
// If withAuth is true, uses the saved token for authentication.
func (c *Client) connect(ctx context.Context, withAuth bool) error {
	dialer := websocket.DefaultDialer

	conn, _, err := dialer.DialContext(ctx, c.url, nil)
	if err != nil {
		return fmt.Errorf("websocket dial failed: %w", err)
	}

	// Set connection
	c.connMu.Lock()
	c.conn = conn
	c.conn.SetReadDeadline(time.Now().Add(readTimeout))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(readTimeout))
		return nil
	})
	c.connMu.Unlock()

	// Read first frame - should be connect.challenge
	frame, err := c.readFrame()
	if err != nil {
		return fmt.Errorf("failed to read challenge: %w", err)
	}

	if frame["type"] != "event" || frame["event"] != "connect.challenge" {
		return fmt.Errorf("expected connect.challenge event, got: %v", frame)
	}

	// Send connect request
	caps, commands := nodeCapabilities()
	connectParams := ConnectParams{
		MinProtocol: 3,
		MaxProtocol: 3,
		Client: ClientInfo{
			ID:              "trioclaw",
			Version:         "0.1.0",
			Platform:        getPlatform(),
			DeviceFamily:    "trioclaw",
			ModelIdentifier: c.nodeID,
			Mode:            "node",
		},
		Role:      "node",
		Caps:      caps,
		Commands:  commands,
		Auth:      nil, // Will be set if withAuth is true
	}

	if withAuth && c.token != "" {
		connectParams.Auth = &AuthInfo{Token: c.token}
	}

	params, _ := json.Marshal(connectParams)
	req := ReqFrame{
		Type:   "req",
		ID:     generateID(),
		Method: "connect",
		Params: params,
	}

	if err := c.sendFrame(req); err != nil {
		return fmt.Errorf("failed to send connect: %w", err)
	}

	// Read response - should be hello-ok
	resFrame, err := c.readFrame()
	if err != nil {
		return fmt.Errorf("failed to read hello: %w", err)
	}

	if resFrame["type"] != "res" {
		return fmt.Errorf("expected res, got: %v", resFrame["type"])
	}

	ok, _ := resFrame["ok"].(bool)
	if !ok {
		// Extract error message
		if payload, exists := resFrame["payload"]; exists {
			if payloadStr, ok := payload.(string); ok {
				return fmt.Errorf("connect failed: %s", payloadStr)
			}
		}
		return fmt.Errorf("connect failed")
	}

	// Check for hello-ok in payload
	if payload, exists := resFrame["payload"]; exists {
		if payloadMap, ok := payload.(map[string]interface{}); ok {
			if typ, exists := payloadMap["type"]; exists && typ == "hello-ok" {
				c.connected = true
				return nil
			}
		}
	}

	return fmt.Errorf("expected hello-ok response")
}

// readLoop reads frames from the WebSocket and dispatches them.
func (c *Client) readLoop(ctx context.Context, handler *Handler) error {
	for {
		select {
		case <-ctx.Done():
			return nil

		default:
			frame, err := c.readFrame()
			if err != nil {
				return err
			}

			// Reset read deadline on successful read (readFrame already has lock protection)
			if c.conn != nil {
				c.conn.SetReadDeadline(time.Now().Add(readTimeout))
			}

			// Handle frame by type
			frameType, _ := frame["type"].(string)
			switch frameType {
			case "event":
				c.handleEvent(ctx, frame, handler)
			case "req":
				c.handleRequest(ctx, frame)
			case "res":
				// Ignore responses (we only care about events and requests as a node)
			}
		}
	}
}

// readFrame reads the next frame from the WebSocket and returns it
// as a raw JSON map for type-based dispatch.
func (c *Client) readFrame() (map[string]interface{}, error) {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.conn == nil {
		return nil, fmt.Errorf("not connected")
	}

	var frame map[string]interface{}
	if err := c.conn.ReadJSON(&frame); err != nil {
		return nil, err
	}

	return frame, nil
}

// handleEvent handles incoming event frames.
func (c *Client) handleEvent(ctx context.Context, frame map[string]interface{}, handler *Handler) {
	event, _ := frame["event"].(string)

	switch event {
	case "node.invoke.request":
		// Parse invoke request
		payloadJSON, _ := frame["payloadJSON"].(string)

		var req InvokeRequest
		if err := json.Unmarshal([]byte(payloadJSON), &req); err != nil {
			fmt.Printf("Failed to parse invoke request: %v\n", err)
			return
		}

		// Dispatch to handler asynchronously to avoid blocking WebSocket read loop
		go func() {
			result := handler.HandleInvoke(ctx, req)
			if err := c.SendInvokeResult(result); err != nil {
				fmt.Printf("Failed to send invoke result: %v\n", err)
			}
		}()

	case "device.pair.resolved":
		// Handle pairing result
		payloadJSON, _ := frame["payloadJSON"].(string)

		var payload struct {
			Status string `json:"status"` // "approved" or "rejected"
			Token  string `json:"token"`  // device token (if approved)
		}
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			c.pairCh <- pairResult{err: fmt.Errorf("failed to parse pair result: %w", err)}
			return
		}

		if payload.Status == "approved" && payload.Token != "" {
			c.pairCh <- pairResult{token: payload.Token}
		} else {
			c.pairCh <- pairResult{err: fmt.Errorf("pairing %s", payload.Status)}
		}

	case "tick":
		// Gateway heartbeat - ignore

	default:
		// Unknown event
		fmt.Printf("Unknown event: %s\n", event)
	}
}

// handleRequest handles incoming request frames.
func (c *Client) handleRequest(ctx context.Context, frame map[string]interface{}) {
	method, _ := frame["method"].(string)

	if method == "ping" {
		// Respond to ping with pong - use ResFrame for responses
		id, _ := frame["id"].(string)
		res := ResFrame{
			Type: "res",
			ID:   id,
			OK:   true,
		}
		_ = c.sendFrame(res)
	}
}

// pingLoop sends ping messages at regular intervals.
func (c *Client) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.connMu.Lock()
			if c.conn != nil {
				c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
				_ = c.conn.WriteMessage(websocket.PingMessage, nil)
			}
			c.connMu.Unlock()
		}
	}
}

// closeConnection closes the WebSocket connection.
func (c *Client) closeConnection() {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
		c.connected = false
	}
}

// nodeCapabilities returns caps + commands to advertise to the gateway.
func nodeCapabilities() (caps []string, commands []string) {
	caps = []string{"camera"}

	commands = []string{
		"camera.snap",    // standard: capture a photo
		"camera.list",    // standard: list cameras
		"camera.clip",    // standard: record video clip
		"vision.analyze", // trioclaw: snap + VLM analysis
	}
	return
}

// generateID generates a random request ID.
func generateID() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// getPlatform returns the current platform string.
func getPlatform() string {
	return getRuntimeGOOS()
}

// getRuntimeGOOS is a wrapper for testing.
var getRuntimeGOOS = func() string {
	return getRuntimeGOOSDefault()
}

func getRuntimeGOOSDefault() string {
	// Determine platform
	switch {
	case isDarwin():
		return "darwin"
	case isLinux():
		return "linux"
	default:
		return "unknown"
	}
}

func isDarwin() bool {
	return runtime.GOOS == "darwin"
}

func isLinux() bool {
	return runtime.GOOS == "linux"
}
