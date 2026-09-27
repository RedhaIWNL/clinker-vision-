package main

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/clinkervision/clinker-vision/internal/health"
)

func TestNightReportPaths(t *testing.T) {
	start := time.Date(2026, 9, 21, 20, 0, 0, 0, time.UTC)
	statsPath, reportPath := nightReportPaths("/srv/clinker-vision/data/alerts.db", start)
	if statsPath != filepath.Join("/srv/clinker-vision/data", "night-2026-09-21.stats.json") {
		t.Fatalf("stats path = %q", statsPath)
	}
	if reportPath != filepath.Join("/srv/clinker-vision/data", "reports", "night-2026-09-21.md") {
		t.Fatalf("report path = %q", reportPath)
	}
}

func TestWindowTrackerCapMirror(t *testing.T) {
	tracker := newWindowTracker()
	tracker.reset(time.Now(), 998)
	tracker.noteStored(2)
	snap := tracker.snapshot()
	if snap.storedTotal != 1000 || snap.stored != 2 {
		t.Fatalf("unexpected snapshot: %#v", snap)
	}
	if snap.capReached {
		t.Fatal("cap should not be reached yet")
	}
	tracker.noteDropped()
	if snap := tracker.snapshot(); !snap.capReached || snap.droppedCap != 1 {
		t.Fatalf("drop not recorded: %#v", snap)
	}
}

func TestWindowReportPathsPerCamera(t *testing.T) {
	start := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	stats, rep := windowReportPaths("/srv/clinker-vision/data/alerts.db", "CAM-4", start)
	if stats != filepath.Join("/srv/clinker-vision/data", "CAM-4-day-2026-09-28.stats.json") ||
		rep != filepath.Join("/srv/clinker-vision/data", "reports", "CAM-4-day-2026-09-28.md") {
		t.Fatalf("CAM-4 paths = %q %q", stats, rep)
	}
	s1, r1 := windowReportPaths("/srv/clinker-vision/data/alerts.db", "CAM-1", start)
	n1, m1 := nightReportPaths("/srv/clinker-vision/data/alerts.db", start)
	if s1 != n1 || r1 != m1 {
		t.Fatal("CAM-1 must keep the night-<date> names")
	}
}

func TestReadinessCountsOnlyCamerasInsideTheirWindow(t *testing.T) {
	h := health.New()
	e := &laneEnv{health: h}
	ready := func() bool {
		rec := httptest.NewRecorder()
		h.Ready(rec, httptest.NewRequest("GET", "/health/ready", nil))
		return rec.Code == 200
	}
	e.setCameraReady("CAM-1", false, false) // night camera in standby by day
	if ready() {
		t.Fatal("no camera in window: not ready")
	}
	e.setCameraReady("CAM-4", true, true)
	if !ready() {
		t.Fatal("CAM-4 ready and CAM-1 in standby: ready")
	}
	e.setCameraReady("CAM-1", false, true) // windows overlap, CAM-1 still locking
	if ready() {
		t.Fatal("a camera in its window that is not ready makes the system not ready")
	}
}
