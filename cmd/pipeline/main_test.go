package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/clinkervision/clinker-vision/internal/config"
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

func TestWindowReportPathsCAM3(t *testing.T) {
	_, rep := windowReportPaths("/srv/clinker-vision/data/alerts.db", "CAM-3", time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC))
	if rep != filepath.Join("/srv/clinker-vision/data", "reports", "CAM-3-day-2026-09-28.md") {
		t.Fatalf("CAM-3 report = %q", rep)
	}
}

func TestCameraHoursFromPage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	cam := config.CameraConfig{ID: "CAM-1", Enabled: true,
		Schedule: &config.ScheduleConfig{Enabled: true, Start: "16:00", Stop: "09:00", Timezone: "Africa/Casablanca"}}
	e := &laneEnv{loc: time.UTC, settingsPath: path}
	if s, off, page := e.cameraHours(cam); off || page || s.Start != "16:00" || s.Stop != "08:00" {
		t.Fatalf("no page hours: the default hours 16:00-08:00 expected (not config.yaml), got %+v off=%v page=%v", s, off, page)
	}
	other := config.CameraConfig{ID: "CAM-2", Enabled: true,
		Schedule: &config.ScheduleConfig{Enabled: true, Start: "10:00", Stop: "11:00", Timezone: "Africa/Casablanca"}}
	if s, _, _ := e.cameraHours(other); s.Start != "10:00" {
		t.Fatalf("a camera without default hours follows config.yaml: %+v", s)
	}
	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"wheel_max_gap_godets":4,"wheel_min_spacing_godets":3,"camera_hours":{"CAM-1":{"mode":"off"}}}`)
	if on, detail := e.inWindow(cam); on || !strings.Contains(detail, "switched off") {
		t.Fatalf("off: %v %q", on, detail)
	}
	write(`{"wheel_max_gap_godets":4,"wheel_min_spacing_godets":3,"camera_hours":{"CAM-1":{"mode":"always"}}}`)
	if on, _ := e.inWindow(cam); !on {
		t.Fatal("always on must run")
	}
	now := time.Now().In(mustLoc(t, "Africa/Casablanca"))
	in := now.Add(-time.Hour).Format("15:04") // a window around now, and one that ended an hour ago
	out := now.Add(-2 * time.Hour).Format("15:04")
	write(`{"wheel_max_gap_godets":4,"wheel_min_spacing_godets":3,"camera_hours":{"CAM-1":{"mode":"hours","start":"` + in + `","stop":"` + now.Add(time.Hour).Format("15:04") + `"}}}`)
	if on, detail := e.inWindow(cam); !on {
		t.Fatalf("inside page hours: %q", detail)
	}
	write(`{"wheel_max_gap_godets":4,"wheel_min_spacing_godets":3,"camera_hours":{"CAM-1":{"mode":"hours","start":"` + out + `","stop":"` + in + `"}}}`)
	if on, detail := e.inWindow(cam); on || !strings.Contains(detail, "camera hours panel") {
		t.Fatalf("outside page hours: %v %q", on, detail)
	}
}

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
