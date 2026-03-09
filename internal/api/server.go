// Package api provides an HTTP REST API for trioclaw.
//
// Endpoints:
//
//	GET  /api/status          → service status overview
//	GET  /api/cameras         → list configured cameras
//	GET  /api/watches         → list active watches
//	GET  /api/events          → query events (with ?date=, ?camera=, ?limit=)
//	GET  /api/alerts          → query alerts (with ?date=, ?camera=, ?limit=)
//	GET  /api/alerts/recent   → N most recent alerts
//	GET  /api/stats           → aggregate statistics
//	GET  /api/clips/:id       → serve a saved clip file
//	GET  /healthz             → health check
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/machinefi/trioclaw/internal/config"
	"github.com/machinefi/trioclaw/internal/store"
	"github.com/machinefi/trioclaw/internal/triocore"
)

// Server is the HTTP API server.
type Server struct {
	cfg      *config.Config
	store    *store.Store
	watchMgr *triocore.Manager
	mux      *http.ServeMux
}

// NewServer creates a new API server.
func NewServer(cfg *config.Config, s *store.Store, watchMgr *triocore.Manager) *Server {
	srv := &Server{
		cfg:      cfg,
		store:    s,
		watchMgr: watchMgr,
		mux:      http.NewServeMux(),
	}
	srv.routes()
	return srv
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /api/status", s.handleStatus)
	s.mux.HandleFunc("GET /api/cameras", s.handleCameras)
	s.mux.HandleFunc("GET /api/watches", s.handleWatches)
	s.mux.HandleFunc("GET /api/events", s.handleEvents)
	s.mux.HandleFunc("GET /api/alerts", s.handleAlerts)
	s.mux.HandleFunc("GET /api/alerts/recent", s.handleRecentAlerts)
	s.mux.HandleFunc("GET /api/stats", s.handleStats)
	s.mux.HandleFunc("GET /api/clips/", s.handleClipFile)
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe(addr string) error {
	log.Printf("[api] listening on %s", addr)
	return http.ListenAndServe(addr, s.mux)
}

// handleHealthz returns 200 OK.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleStatus returns service overview.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.GetStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	watches := []watchEntry{}
	if s.watchMgr != nil {
		for _, e := range s.watchMgr.Status() {
			watches = append(watches, watchEntry{
				CameraID: e.CameraID,
				WatchID:  e.WatchID,
				Source:   maskSource(e.Source),
				State:    e.State,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"cameras":      len(s.cfg.Cameras),
		"watches":      watches,
		"total_events": stats.TotalEvents,
		"total_alerts": stats.TotalAlerts,
		"uptime":       time.Since(startTime).String(),
	})
}

// handleCameras returns configured cameras (credentials masked).
func (s *Server) handleCameras(w http.ResponseWriter, r *http.Request) {
	type camJSON struct {
		ID         string   `json:"id"`
		Name       string   `json:"name"`
		Source     string   `json:"source"`
		FPS        int      `json:"fps"`
		Conditions []string `json:"conditions"`
	}

	cams := make([]camJSON, 0, len(s.cfg.Cameras))
	for _, c := range s.cfg.Cameras {
		var conds []string
		for _, cond := range c.Conditions {
			conds = append(conds, cond.ID)
		}
		cams = append(cams, camJSON{
			ID:         c.ID,
			Name:       c.Name,
			Source:     maskSource(c.Source),
			FPS:        c.FPS,
			Conditions: conds,
		})
	}
	writeJSON(w, http.StatusOK, cams)
}

// handleWatches returns active watches.
func (s *Server) handleWatches(w http.ResponseWriter, r *http.Request) {
	if s.watchMgr == nil {
		writeJSON(w, http.StatusOK, []watchEntry{})
		return
	}
	entries := s.watchMgr.Status()
	watches := make([]watchEntry, 0, len(entries))
	for _, e := range entries {
		watches = append(watches, watchEntry{
			CameraID: e.CameraID,
			WatchID:  e.WatchID,
			Source:   maskSource(e.Source),
			State:    e.State,
		})
	}
	writeJSON(w, http.StatusOK, watches)
}

// handleEvents returns events, filtered by query params.
// ?date=2026-03-09  ?camera=front-door  ?limit=100
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	t, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid date format, use YYYY-MM-DD")
		return
	}

	events, err := s.store.EventsByDate(t)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Filter by camera if specified
	camera := r.URL.Query().Get("camera")
	if camera != "" {
		events = filterByCamera(events, camera)
	}

	// Apply limit
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 && limit < len(events) {
			events = events[:limit]
		}
	}

	writeJSON(w, http.StatusOK, eventsToJSON(events))
}

// handleAlerts returns triggered events.
func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	t, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid date format, use YYYY-MM-DD")
		return
	}

	alerts, err := s.store.AlertsByDate(t)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	camera := r.URL.Query().Get("camera")
	if camera != "" {
		alerts = filterByCamera(alerts, camera)
	}

	writeJSON(w, http.StatusOK, eventsToJSON(alerts))
}

// handleRecentAlerts returns N most recent alerts.
func (s *Server) handleRecentAlerts(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}

	alerts, err := s.store.RecentAlerts(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, eventsToJSON(alerts))
}

// handleStats returns aggregate statistics.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.GetStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Get per-camera alert counts for today
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	todayCounts, _ := s.store.AlertCountByCamera(dayStart, dayStart.Add(24*time.Hour))

	writeJSON(w, http.StatusOK, map[string]any{
		"total_events":     stats.TotalEvents,
		"total_alerts":     stats.TotalAlerts,
		"camera_count":     stats.CameraCount,
		"oldest_event":     formatTime(stats.OldestEvent),
		"newest_event":     formatTime(stats.NewestEvent),
		"today_by_camera":  todayCounts,
	})
}

// handleClipFile serves a clip file by path.
func (s *Server) handleClipFile(w http.ResponseWriter, r *http.Request) {
	// Extract filename from /api/clips/{filename}
	path := strings.TrimPrefix(r.URL.Path, "/api/clips/")
	if path == "" {
		writeError(w, http.StatusBadRequest, "clip path required")
		return
	}

	// Only serve from clips directory
	clipDir := s.cfg.Clips.Dir
	fullPath := fmt.Sprintf("%s/%s", clipDir, path)

	// Prevent directory traversal
	if strings.Contains(path, "..") {
		writeError(w, http.StatusForbidden, "invalid path")
		return
	}

	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "clip not found")
		return
	}

	http.ServeFile(w, r, fullPath)
}

// --- helpers ---

type watchEntry struct {
	CameraID string `json:"camera_id"`
	WatchID  string `json:"watch_id"`
	Source   string `json:"source"`
	State    string `json:"state"`
}

type eventJSON struct {
	ID          int64   `json:"id"`
	Timestamp   string  `json:"timestamp"`
	CameraID    string  `json:"camera_id"`
	WatchID     string  `json:"watch_id"`
	ConditionID string  `json:"condition_id"`
	Answer      string  `json:"answer"`
	Triggered   bool    `json:"triggered"`
	LatencyMs   float64 `json:"latency_ms"`
}

func eventsToJSON(events []store.Event) []eventJSON {
	out := make([]eventJSON, 0, len(events))
	for _, e := range events {
		out = append(out, eventJSON{
			ID:          e.ID,
			Timestamp:   e.Timestamp.Format(time.RFC3339),
			CameraID:    e.CameraID,
			WatchID:     e.WatchID,
			ConditionID: e.ConditionID,
			Answer:      e.Answer,
			Triggered:   e.Triggered,
			LatencyMs:   e.LatencyMs,
		})
	}
	return out
}

func filterByCamera(events []store.Event, camera string) []store.Event {
	var filtered []store.Event
	for _, e := range events {
		if e.CameraID == camera {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func maskSource(source string) string {
	if !strings.Contains(source, "@") {
		return source
	}
	idx := strings.Index(source, "://")
	if idx < 0 {
		return source
	}
	rest := source[idx+3:]
	atIdx := strings.Index(rest, "@")
	if atIdx < 0 {
		return source
	}
	return source[:idx+3] + "***:***@" + rest[atIdx+1:]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// startTime tracks when the server was created (for uptime).
var startTime = time.Now()
