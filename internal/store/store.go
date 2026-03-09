// Package store provides SQLite-based event storage for TrioClaw.
//
// Tables:
//   - events: every inference result and alert from trio-core
//   - clips: saved video clips linked to events
//
// Used for:
//   - Daily digest generation (query day's events)
//   - Event history / trend analysis
//   - Linking clips to triggered events
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the SQLite event store.
type Store struct {
	db *sql.DB
}

// Event is a single inference result or alert stored in the database.
type Event struct {
	ID          int64
	Timestamp   time.Time
	CameraID    string
	WatchID     string
	ConditionID string
	Question    string
	Answer      string
	Triggered   bool
	LatencyMs   float64
	FramesUsed  int
}

// Clip is a saved video clip linked to an event.
type Clip struct {
	ID       int64
	EventID  int64
	Path     string
	Duration int // milliseconds
	Created  time.Time
}

// DefaultDBPath returns ~/.trioclaw/events.db
func DefaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "events.db"
	}
	return filepath.Join(home, ".trioclaw", "events.db")
}

// Open creates or opens the SQLite database and runs migrations.
func Open(dbPath string) (*Store, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Enable WAL mode for better concurrent read/write
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// InsertEvent stores an inference result or alert.
func (s *Store) InsertEvent(e *Event) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO events (timestamp, camera_id, watch_id, condition_id, question, answer, triggered, latency_ms, frames_used)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Timestamp.UTC().Format(time.RFC3339),
		e.CameraID, e.WatchID, e.ConditionID,
		e.Question, e.Answer, e.Triggered,
		e.LatencyMs, e.FramesUsed,
	)
	if err != nil {
		return 0, fmt.Errorf("insert event: %w", err)
	}
	return result.LastInsertId()
}

// InsertAlert is a convenience method that inserts triggered events only.
func (s *Store) InsertAlert(e *Event) (int64, error) {
	e.Triggered = true
	return s.InsertEvent(e)
}

// InsertClip stores a clip record linked to an event.
func (s *Store) InsertClip(clip *Clip) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO clips (event_id, path, duration_ms, created)
		 VALUES (?, ?, ?, ?)`,
		clip.EventID, clip.Path, clip.Duration,
		clip.Created.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("insert clip: %w", err)
	}
	return result.LastInsertId()
}

// EventsByDate returns all events for a given date (local time zone).
func (s *Store) EventsByDate(date time.Time) ([]Event, error) {
	start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	end := start.Add(24 * time.Hour)

	return s.queryEvents(
		`SELECT id, timestamp, camera_id, watch_id, condition_id, question, answer, triggered, latency_ms, frames_used
		 FROM events WHERE timestamp >= ? AND timestamp < ? ORDER BY timestamp ASC`,
		start.UTC().Format(time.RFC3339),
		end.UTC().Format(time.RFC3339),
	)
}

// AlertsByDate returns only triggered events for a given date.
func (s *Store) AlertsByDate(date time.Time) ([]Event, error) {
	start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	end := start.Add(24 * time.Hour)

	return s.queryEvents(
		`SELECT id, timestamp, camera_id, watch_id, condition_id, question, answer, triggered, latency_ms, frames_used
		 FROM events WHERE triggered = 1 AND timestamp >= ? AND timestamp < ? ORDER BY timestamp ASC`,
		start.UTC().Format(time.RFC3339),
		end.UTC().Format(time.RFC3339),
	)
}

// RecentAlerts returns the N most recent triggered events.
func (s *Store) RecentAlerts(limit int) ([]Event, error) {
	return s.queryEvents(
		`SELECT id, timestamp, camera_id, watch_id, condition_id, question, answer, triggered, latency_ms, frames_used
		 FROM events WHERE triggered = 1 ORDER BY timestamp DESC LIMIT ?`,
		limit,
	)
}

// AlertCountByCamera returns alert counts per camera for a date range.
func (s *Store) AlertCountByCamera(start, end time.Time) (map[string]int, error) {
	rows, err := s.db.Query(
		`SELECT camera_id, COUNT(*) FROM events
		 WHERE triggered = 1 AND timestamp >= ? AND timestamp < ?
		 GROUP BY camera_id`,
		start.UTC().Format(time.RFC3339),
		end.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("query alert counts: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var cameraID string
		var count int
		if err := rows.Scan(&cameraID, &count); err != nil {
			return nil, err
		}
		result[cameraID] = count
	}
	return result, rows.Err()
}

// Stats returns basic statistics.
type Stats struct {
	TotalEvents  int
	TotalAlerts  int
	CameraCount  int
	OldestEvent  time.Time
	NewestEvent  time.Time
}

// GetStats returns overall store statistics.
func (s *Store) GetStats() (*Stats, error) {
	stats := &Stats{}

	err := s.db.QueryRow("SELECT COUNT(*) FROM events").Scan(&stats.TotalEvents)
	if err != nil {
		return nil, err
	}

	err = s.db.QueryRow("SELECT COUNT(*) FROM events WHERE triggered = 1").Scan(&stats.TotalAlerts)
	if err != nil {
		return nil, err
	}

	err = s.db.QueryRow("SELECT COUNT(DISTINCT camera_id) FROM events").Scan(&stats.CameraCount)
	if err != nil {
		return nil, err
	}

	var oldest, newest sql.NullString
	s.db.QueryRow("SELECT MIN(timestamp) FROM events").Scan(&oldest)
	s.db.QueryRow("SELECT MAX(timestamp) FROM events").Scan(&newest)

	if oldest.Valid {
		stats.OldestEvent, _ = time.Parse(time.RFC3339, oldest.String)
	}
	if newest.Valid {
		stats.NewestEvent, _ = time.Parse(time.RFC3339, newest.String)
	}

	return stats, nil
}

func (s *Store) queryEvents(query string, args ...any) ([]Event, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		var ts string
		if err := rows.Scan(
			&e.ID, &ts, &e.CameraID, &e.WatchID, &e.ConditionID,
			&e.Question, &e.Answer, &e.Triggered,
			&e.LatencyMs, &e.FramesUsed,
		); err != nil {
			return nil, err
		}
		e.Timestamp, _ = time.Parse(time.RFC3339, ts)
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS events (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp    TEXT NOT NULL,
			camera_id    TEXT NOT NULL,
			watch_id     TEXT NOT NULL DEFAULT '',
			condition_id TEXT NOT NULL,
			question     TEXT NOT NULL DEFAULT '',
			answer       TEXT NOT NULL DEFAULT '',
			triggered    BOOLEAN NOT NULL DEFAULT 0,
			latency_ms   REAL NOT NULL DEFAULT 0,
			frames_used  INTEGER NOT NULL DEFAULT 0
		);

		CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);
		CREATE INDEX IF NOT EXISTS idx_events_camera ON events(camera_id);
		CREATE INDEX IF NOT EXISTS idx_events_triggered ON events(triggered);

		CREATE TABLE IF NOT EXISTS clips (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id    INTEGER NOT NULL REFERENCES events(id),
			path        TEXT NOT NULL,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			created     TEXT NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_clips_event ON clips(event_id);
	`)
	return err
}
