package main

import (
	"path/filepath"
	"testing"
	"time"
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
