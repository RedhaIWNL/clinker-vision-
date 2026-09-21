package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/clinkervision/clinker-vision/internal/evidence"
)

func TestStoreInsertReadAndIdempotentSeen(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	alertStore := openTestStore(t, root)
	alert := validAlert()
	if err := alertStore.InsertAlert(ctx, alert); err != nil {
		t.Fatal(err)
	}
	got, err := alertStore.GetAlert(ctx, alert.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FrameID != alert.FrameID || got.EvidenceRef != alert.EvidenceRef || got.Confidence != alert.Confidence {
		t.Fatalf("stored alert mismatch: %#v", got)
	}
	seenAt := time.Date(2026, 9, 17, 9, 0, 0, 0, time.FixedZone("CET", 3600))
	first, err := alertStore.MarkSeen(ctx, alert.AlertID, seenAt)
	if err != nil {
		t.Fatal(err)
	}
	later := seenAt.Add(time.Hour)
	second, err := alertStore.MarkSeen(ctx, alert.AlertID, later)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equal(seenAt) || !second.Equal(seenAt) {
		t.Fatalf("seen_at was not shared/idempotent: first=%v second=%v", first, second)
	}
	if count, err := alertStore.Count(ctx); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestStoreRejectsPathTraversal(t *testing.T) {
	alertStore := openTestStore(t, t.TempDir())
	alert := validAlert()
	alert.EvidenceRef = "../../outside.jpg"
	if err := alertStore.InsertAlert(context.Background(), alert); err == nil {
		t.Fatal("path traversal evidence reference was accepted")
	}
}

func TestStoreCleanupRemovesOldAlertAndEvidence(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	alertStore := openTestStore(t, root)
	alert := validAlert()
	alert.CapturedAt = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	alert.DetectedAt = alert.CapturedAt
	if err := evidence.WriteAtomic(filepath.Join(root, "evidence"), alert.EvidenceRef, []byte("old evidence")); err != nil {
		t.Fatal(err)
	}
	if err := alertStore.InsertAlert(ctx, alert); err != nil {
		t.Fatal(err)
	}
	deleted, err := alertStore.CleanupBefore(ctx, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || deleted != 1 {
		t.Fatalf("cleanup deleted=%d err=%v", deleted, err)
	}
	if _, err := alertStore.GetAlert(ctx, alert.AlertID); err == nil {
		t.Fatalf("expired alert still readable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "evidence", alert.EvidenceRef)); !os.IsNotExist(err) {
		t.Fatalf("expired evidence still exists: %v", err)
	}
}

func TestStoreListAlertsFiltersOrdersAndPaginates(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	alertStore := openTestStore(t, root)
	base := validAlert()
	alerts := []Alert{base}
	alerts = append(alerts, Alert{
		AlertID: "550e8400-e29b-41d4-a716-446655440012", CapturedAt: base.CapturedAt.Add(time.Minute), DetectedAt: base.DetectedAt.Add(time.Minute), CreatedAt: base.CreatedAt.Add(time.Minute),
		CameraID: "CAM-1", ObservationTarget: "godet", FaultType: "MISALIGNMENT", Confidence: 0.9, FrameID: "550e8400-e29b-41d4-a716-446655440013", ModelVersion: "mock-test", BoundingBox: base.BoundingBox, EvidenceRef: "2026/09/17/CAM-1/550e8400-e29b-41d4-a716-446655440012.jpg",
	})
	alerts = append(alerts, Alert{
		AlertID: "550e8400-e29b-41d4-a716-446655440014", CapturedAt: base.CapturedAt.Add(2 * time.Minute), DetectedAt: base.DetectedAt.Add(2 * time.Minute), CreatedAt: base.CreatedAt.Add(2 * time.Minute),
		CameraID: "CAM-2", ObservationTarget: "galet", FaultType: "CRACK", Confidence: 0.88, FrameID: "550e8400-e29b-41d4-a716-446655440015", ModelVersion: "mock-test", BoundingBox: base.BoundingBox, EvidenceRef: "2026/09/17/CAM-2/550e8400-e29b-41d4-a716-446655440014.jpg",
	})
	for _, alert := range alerts {
		if err := alertStore.InsertAlert(ctx, alert); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := alertStore.MarkSeen(ctx, alerts[1].AlertID, base.DetectedAt.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	result, err := alertStore.ListAlerts(ctx, ListFilter{CameraID: "CAM-1", Limit: 1})
	if err != nil || len(result.Alerts) != 1 || !result.HasMore || result.Alerts[0].AlertID != alerts[1].AlertID {
		t.Fatalf("first page=%#v err=%v", result, err)
	}
	result, err = alertStore.ListAlerts(ctx, ListFilter{CameraID: "CAM-1", UnseenOnly: true, Limit: 10})
	if err != nil || len(result.Alerts) != 1 || result.Alerts[0].AlertID != alerts[0].AlertID {
		t.Fatalf("unseen result=%#v err=%v", result, err)
	}
}

func TestStoreMigratesLegacyConfidenceAndTier2Columns(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "data", "alerts.db")
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o750); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE alerts (
  alert_id TEXT PRIMARY KEY, captured_at TEXT NOT NULL, detected_at TEXT NOT NULL,
  processed_at TEXT, created_at TEXT NOT NULL, camera_id TEXT NOT NULL,
  observation_target TEXT NOT NULL, fault_type TEXT NOT NULL,
  confidence REAL NOT NULL, frame_id TEXT NOT NULL, model_version TEXT NOT NULL,
  bbox_x REAL NOT NULL, bbox_y REAL NOT NULL, bbox_w REAL NOT NULL, bbox_h REAL NOT NULL,
  scalar_value REAL, evidence_ref TEXT NOT NULL UNIQUE, seen_at TEXT)`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO alerts VALUES
('550e8400-e29b-41d4-a716-446655440010','2026-09-17T08:00:00Z','2026-09-17T08:00:00Z',NULL,'2026-09-17T08:00:00Z','CAM-1','godet','CRACK',0.9,'550e8400-e29b-41d4-a716-446655440011','old',0.2,0.3,0.18,0.12,NULL,'2026/09/17/CAM-1/550e8400-e29b-41d4-a716-446655440010.jpg',NULL)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	alertStore, err := Open(ctx, databasePath, filepath.Join(root, "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	defer alertStore.Close()
	legacy, err := alertStore.GetAlert(ctx, "550e8400-e29b-41d4-a716-446655440010")
	if err != nil || !legacy.ConfidenceKnown || legacy.Confidence != 0.9 {
		t.Fatalf("legacy alert=%#v err=%v", legacy, err)
	}
	newAlert := validAlert()
	newAlert.AlertID = "550e8400-e29b-41d4-a716-446655440016"
	newAlert.FrameID = "550e8400-e29b-41d4-a716-446655440017"
	newAlert.Confidence = 0
	newAlert.ConfidenceKnown = false
	newAlert.AlertState = "pending"
	newAlert.GodetID = 224
	newAlert.LoopNo = 0
	newAlert.EventKey = "DAMAGE:224:0"
	newAlert.EvidenceRef = "2026/09/17/CAM-1/550e8400-e29b-41d4-a716-446655440016.jpg"
	if err := alertStore.UpsertGodetAlert(ctx, newAlert); err != nil {
		t.Fatal(err)
	}
	got, err := alertStore.GetAlert(ctx, newAlert.AlertID)
	if err != nil || got.ConfidenceKnown || got.LoopNo != 0 || got.EventKey != newAlert.EventKey {
		t.Fatalf("migrated Tier-2 alert=%#v err=%v", got, err)
	}
}

func TestStoreCountAndHasEventKey(t *testing.T) {
	ctx := context.Background()
	alertStore := openTestStore(t, t.TempDir())
	first := validAlert()
	first.EventKey = "DAMAGE:224:0"
	second := validAlert()
	second.AlertID = "550e8400-e29b-41d4-a716-446655440012"
	second.FrameID = "550e8400-e29b-41d4-a716-446655440013"
	second.EvidenceRef = "2026/09/17/CAM-1/550e8400-e29b-41d4-a716-446655440012.jpg"
	if err := alertStore.InsertAlert(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := alertStore.InsertAlert(ctx, second); err != nil {
		t.Fatal(err)
	}
	if count, err := alertStore.Count(ctx); err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	found, err := alertStore.HasEventKey(ctx, first.EventKey)
	if err != nil || !found {
		t.Fatalf("HasEventKey(known)=%v err=%v", found, err)
	}
	found, err = alertStore.HasEventKey(ctx, "DAMAGE:999:0")
	if err != nil || found {
		t.Fatalf("HasEventKey(unknown)=%v err=%v", found, err)
	}
	found, err = alertStore.HasEventKey(ctx, "")
	if err != nil || found {
		t.Fatalf("HasEventKey(empty)=%v err=%v", found, err)
	}
}

func openTestStore(t *testing.T, root string) *Store {
	t.Helper()
	alertStore, err := Open(context.Background(), filepath.Join(root, "data", "alerts.db"), filepath.Join(root, "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = alertStore.Close() })
	return alertStore
}

func validAlert() Alert {
	now := time.Date(2026, 9, 17, 8, 0, 0, 0, time.FixedZone("CET", 3600))
	return Alert{
		AlertID:           "550e8400-e29b-41d4-a716-446655440010",
		CapturedAt:        now,
		DetectedAt:        now,
		CreatedAt:         now,
		CameraID:          "CAM-1",
		ObservationTarget: "godet",
		FaultType:         "CRACK",
		Confidence:        0.95,
		FrameID:           "550e8400-e29b-41d4-a716-446655440011",
		ModelVersion:      "mock-test",
		BoundingBox:       BoundingBox{X: 0.2, Y: 0.3, Width: 0.18, Height: 0.12},
		EvidenceRef:       "2026/09/17/CAM-1/550e8400-e29b-41d4-a716-446655440010.jpg",
	}
}
