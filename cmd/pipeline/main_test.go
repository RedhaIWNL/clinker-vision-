package main

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/clinkervision/clinker-vision/internal/config"
	"github.com/clinkervision/clinker-vision/internal/evidence"
	"github.com/clinkervision/clinker-vision/internal/health"
	"github.com/clinkervision/clinker-vision/internal/metrics"
	"github.com/clinkervision/clinker-vision/internal/store"
	"github.com/google/uuid"
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

func testRetentionStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "alerts.db")
	evidenceRoot := filepath.Join(root, "evidence")
	s, err := store.Open(context.Background(), dbPath, evidenceRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, evidenceRoot
}

func testRetentionAlert(detected time.Time, eventKey string) store.Alert {
	now := detected.UTC()
	return store.Alert{
		AlertID: uuid.NewString(), CapturedAt: now, DetectedAt: now, CreatedAt: now,
		CameraID: "CAM-1", ObservationTarget: "godet", FaultType: "DAMAGE",
		FrameID: uuid.NewString(), ModelVersion: "test-retention",
		BoundingBox:      store.BoundingBox{X: 0.2, Y: 0.3, Width: 0.18, Height: 0.12},
		EvidenceRef:      "2026/09/17/CAM-1/" + uuid.NewString() + ".jpg",
		GodetID:          7,
		AlertState:       "confirmed",
		LoopNo:           3,
		RuleID:           "DAMAGE",
		EventKey:         eventKey,
		MeasurementsJSON: `{"severity":2.1}`,
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The old global 1000-cap blinded the system: with max_alerts=0 every new key
// must pass, no matter how many rows exist.
func TestGateStorageUnlimitedNeverCaps(t *testing.T) {
	ctx := context.Background()
	s, _ := testRetentionStore(t)
	cfg := config.Defaults()
	cfg.Retention.MaxAlerts = 0
	e := &laneEnv{cfg: cfg, store: s}
	for i := 0; i < 3; i++ {
		a := testRetentionAlert(time.Now().UTC(), "UNLIMITED:"+uuid.NewString())
		if err := s.UpsertGodetAlert(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	allow, upsert, err := e.gateStorage(ctx, "", "UNLIMITED:new-key")
	if err != nil || !allow || upsert {
		t.Fatalf("unlimited cap must allow new keys: allow=%v upsert=%v err=%v", allow, upsert, err)
	}
}

// A positive max_alerts is an emergency fuse only: new keys stop, known-key
// upserts (pending->confirmed) still pass.
func TestGateStorageCappedStillAllowsUpserts(t *testing.T) {
	ctx := context.Background()
	s, _ := testRetentionStore(t)
	cfg := config.Defaults()
	cfg.Retention.MaxAlerts = 1
	e := &laneEnv{cfg: cfg, store: s}
	known := testRetentionAlert(time.Now().UTC(), "FUSE:known")
	if err := s.UpsertGodetAlert(ctx, known); err != nil {
		t.Fatal(err)
	}
	if allow, _, _ := e.gateStorage(ctx, "", "FUSE:fresh"); allow {
		t.Fatal("capped store must deny fresh keys at the cap")
	}
	if allow, upsert, _ := e.gateStorage(ctx, "", "FUSE:known"); !allow || !upsert {
		t.Fatalf("capped store must allow known-key upserts: allow=%v upsert=%v", allow, upsert)
	}
	if allow, upsert, _ := e.gateStorage(ctx, "pending", "FUSE:fresh"); !allow || !upsert {
		t.Fatalf("in-memory seen state must allow upserts: allow=%v upsert=%v", allow, upsert)
	}
}

// 30-day retention: rows + JPEGs older than the cutoff go, recent rows stay,
// metrics record the run. Reports are never touched by the janitor.
func TestRunRetentionOnceDeletesOnlyExpired(t *testing.T) {
	ctx := context.Background()
	s, evidenceRoot := testRetentionStore(t)
	oldAlert := testRetentionAlert(time.Now().UTC().AddDate(0, 0, -31), "RET:old")
	newAlert := testRetentionAlert(time.Now().UTC().AddDate(0, 0, -5), "RET:new")
	for _, a := range []store.Alert{oldAlert, newAlert} {
		if err := evidence.WriteAtomic(evidenceRoot, a.EvidenceRef, []byte("fake-jpeg")); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertGodetAlert(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	meter := metrics.New()
	deleted, err := runRetentionOnce(ctx, s, 30, testLogger(), meter)
	if err != nil || deleted != 1 {
		t.Fatalf("expected 1 deletion, got deleted=%d err=%v", deleted, err)
	}
	if n, _ := s.Count(ctx); n != 1 {
		t.Fatalf("expected 1 row left, got %d", n)
	}
	if _, err := s.GetAlert(ctx, oldAlert.AlertID); err == nil {
		t.Fatal("expired alert still readable")
	}
	if _, err := s.GetAlert(ctx, newAlert.AlertID); err != nil {
		t.Fatalf("recent alert wrongly deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(evidenceRoot, oldAlert.EvidenceRef)); !os.IsNotExist(err) {
		t.Fatalf("expired evidence still on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(evidenceRoot, newAlert.EvidenceRef)); err != nil {
		t.Fatalf("recent evidence wrongly removed: %v", err)
	}
	if meter.AlertsExpired.Load() != 1 || meter.RetentionRuns.Load() != 1 {
		t.Fatalf("metrics not recorded: expired=%d runs=%d",
			meter.AlertsExpired.Load(), meter.RetentionRuns.Load())
	}
	// Second run is a no-op but still counts as a run.
	deleted, err = runRetentionOnce(ctx, s, 30, testLogger(), meter)
	if err != nil || deleted != 0 {
		t.Fatalf("second run: deleted=%d err=%v", deleted, err)
	}
	if meter.RetentionRuns.Load() != 2 {
		t.Fatalf("expected 2 runs, got %d", meter.RetentionRuns.Load())
	}
}
