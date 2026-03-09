// Package clip saves video clips on alert triggers.
//
// When an alert fires, the Recorder captures a clip from the camera source
// using ffmpeg, saves it to disk, and records the clip in SQLite.
//
// The clip duration = pre_seconds + post_seconds from config.
// Since we don't maintain a ring buffer (simpler approach), we record
// post_seconds starting from the alert time. Pre-seconds would require
// a persistent ring buffer per camera — deferred to a future version.
package clip

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/machinefi/trioclaw/internal/store"
)

// Recorder saves clips to disk and records them in the store.
type Recorder struct {
	clipDir     string
	postSeconds int
	store       *store.Store
}

// NewRecorder creates a clip recorder.
func NewRecorder(clipDir string, postSeconds int, s *store.Store) *Recorder {
	if postSeconds <= 0 {
		postSeconds = 15
	}
	return &Recorder{
		clipDir:     clipDir,
		postSeconds: postSeconds,
		store:       s,
	}
}

// SaveClip records a clip from the given source and links it to an event.
// This is called asynchronously from the alert handler.
func (r *Recorder) SaveClip(source, cameraID string, eventID int64) {
	go func() {
		if err := r.record(source, cameraID, eventID); err != nil {
			log.Printf("[clip] error saving clip for %s: %v", cameraID, err)
		}
	}()
}

func (r *Recorder) record(source, cameraID string, eventID int64) error {
	// Ensure clip directory exists
	if err := os.MkdirAll(r.clipDir, 0755); err != nil {
		return fmt.Errorf("create clip dir: %w", err)
	}

	// Generate filename: {cameraID}_{timestamp}.mp4
	ts := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("%s_%s.mp4", cameraID, ts)
	outPath := filepath.Join(r.clipDir, filename)

	duration := fmt.Sprintf("%d", r.postSeconds)

	// Record clip via ffmpeg
	// For RTSP: use -rtsp_transport tcp for reliability
	args := []string{
		"-y",               // overwrite
		"-rtsp_transport", "tcp",
		"-i", source,
		"-t", duration,     // duration in seconds
		"-c:v", "copy",     // copy video codec (fast, no re-encode)
		"-an",              // no audio
		"-movflags", "+faststart",
		outPath,
	}

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = nil
	cmd.Stderr = nil

	log.Printf("[clip] recording %ss clip from %s → %s", duration, cameraID, filename)

	if err := cmd.Run(); err != nil {
		// Clean up partial file
		os.Remove(outPath)
		return fmt.Errorf("ffmpeg record: %w", err)
	}

	// Check file size
	info, err := os.Stat(outPath)
	if err != nil || info.Size() == 0 {
		os.Remove(outPath)
		return fmt.Errorf("clip file empty or missing")
	}

	// Record in store
	_, err = r.store.InsertClip(&store.Clip{
		EventID:  eventID,
		Path:     outPath,
		Duration: r.postSeconds * 1000,
		Created:  time.Now(),
	})
	if err != nil {
		return fmt.Errorf("store clip: %w", err)
	}

	log.Printf("[clip] saved %s (%d bytes)", filename, info.Size())
	return nil
}
