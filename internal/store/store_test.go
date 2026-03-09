package store

import (
	"path/filepath"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestInsertAndQuery(t *testing.T) {
	s := testStore(t)

	now := time.Now().UTC()

	// Insert a regular result
	id, err := s.InsertEvent(&Event{
		Timestamp:   now,
		CameraID:    "front-door",
		WatchID:     "w_123",
		ConditionID: "person",
		Question:    "Is there a person?",
		Answer:      "No",
		Triggered:   false,
		LatencyMs:   150.5,
		FramesUsed:  4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Errorf("expected id=1, got %d", id)
	}

	// Insert an alert
	alertID, err := s.InsertAlert(&Event{
		Timestamp:   now.Add(time.Minute),
		CameraID:    "front-door",
		WatchID:     "w_123",
		ConditionID: "person",
		Answer:      "Yes, a person at the door",
		LatencyMs:   200,
		FramesUsed:  4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if alertID != 2 {
		t.Errorf("expected id=2, got %d", alertID)
	}

	// Query by date
	events, err := s.EventsByDate(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// Query alerts only
	alerts, err := s.AlertsByDate(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Answer != "Yes, a person at the door" {
		t.Errorf("alert answer = %s", alerts[0].Answer)
	}
}

func TestRecentAlerts(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC()

	for i := 0; i < 10; i++ {
		s.InsertAlert(&Event{
			Timestamp:   now.Add(time.Duration(i) * time.Minute),
			CameraID:    "cam0",
			ConditionID: "test",
			Answer:      "alert",
		})
	}

	recent, err := s.RecentAlerts(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 3 {
		t.Fatalf("expected 3 recent alerts, got %d", len(recent))
	}
	// Most recent first
	if recent[0].Timestamp.Before(recent[1].Timestamp) {
		t.Error("expected newest first")
	}
}

func TestAlertCountByCamera(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC()

	s.InsertAlert(&Event{Timestamp: now, CameraID: "cam0", ConditionID: "x", Answer: "a"})
	s.InsertAlert(&Event{Timestamp: now, CameraID: "cam0", ConditionID: "y", Answer: "b"})
	s.InsertAlert(&Event{Timestamp: now, CameraID: "cam1", ConditionID: "x", Answer: "c"})

	counts, err := s.AlertCountByCamera(now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if counts["cam0"] != 2 {
		t.Errorf("cam0 count = %d, want 2", counts["cam0"])
	}
	if counts["cam1"] != 1 {
		t.Errorf("cam1 count = %d, want 1", counts["cam1"])
	}
}

func TestGetStats(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC()

	s.InsertEvent(&Event{Timestamp: now, CameraID: "cam0", ConditionID: "x", Answer: "no"})
	s.InsertAlert(&Event{Timestamp: now.Add(time.Minute), CameraID: "cam1", ConditionID: "x", Answer: "yes"})

	stats, err := s.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalEvents != 2 {
		t.Errorf("total events = %d, want 2", stats.TotalEvents)
	}
	if stats.TotalAlerts != 1 {
		t.Errorf("total alerts = %d, want 1", stats.TotalAlerts)
	}
	if stats.CameraCount != 2 {
		t.Errorf("camera count = %d, want 2", stats.CameraCount)
	}
}

func TestInsertClip(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC()

	eventID, _ := s.InsertAlert(&Event{
		Timestamp: now, CameraID: "cam0", ConditionID: "x", Answer: "yes",
	})

	clipID, err := s.InsertClip(&Clip{
		EventID:  eventID,
		Path:     "/clips/event-1.mp4",
		Duration: 30000,
		Created:  now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if clipID != 1 {
		t.Errorf("clip id = %d, want 1", clipID)
	}
}
