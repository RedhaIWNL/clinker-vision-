package report

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/clinkervision/clinker-vision/internal/store"
)

func TestGenerateWritesMarkdown(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	alertStore, err := store.Open(ctx, filepath.Join(root, "data", "alerts.db"), filepath.Join(root, "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = alertStore.Close() })

	windowStart := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	seenAt := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

	alerts := []store.Alert{
		{
			AlertID: "550e8400-e29b-41d4-a716-446655440020", CapturedAt: windowStart.Add(10 * time.Hour), DetectedAt: windowStart.Add(10 * time.Hour), CreatedAt: windowStart.Add(10 * time.Hour),
			CameraID: "CAM-1", ObservationTarget: "godet", FaultType: "DAMAGE", Confidence: 0.9, FrameID: "550e8400-e29b-41d4-a716-446655440021", ModelVersion: "test-v1",
			BoundingBox:      store.BoundingBox{X: 0.38, Y: 0.11, Width: 0.05, Height: 0.16},
			EvidenceRef:      "2026/09/20/CAM-1/550e8400-e29b-41d4-a716-446655440020.jpg",
			GodetID:          101,
			AlertState:       "pending",
			LoopNo:           5,
			RuleID:           "DAMAGE",
			EventKey:         "DAMAGE:101:5",
			MeasurementsJSON: `{"lip_before":150.0,"lip_now":137.5,"drop":12.5}`,
		},
		{
			AlertID: "550e8400-e29b-41d4-a716-446655440022", CapturedAt: windowStart.Add(11 * time.Hour), DetectedAt: windowStart.Add(11 * time.Hour), CreatedAt: windowStart.Add(11 * time.Hour),
			CameraID: "CAM-1", ObservationTarget: "godet", FaultType: "DAMAGE", Confidence: 0.92, FrameID: "550e8400-e29b-41d4-a716-446655440023", ModelVersion: "test-v1",
			BoundingBox:      store.BoundingBox{X: 0.38, Y: 0.11, Width: 0.05, Height: 0.16},
			EvidenceRef:      "2026/09/20/CAM-1/550e8400-e29b-41d4-a716-446655440022.jpg",
			SeenAt:           &seenAt,
			GodetID:          101,
			AlertState:       "confirmed",
			LoopNo:           6,
			RuleID:           "DAMAGE",
			EventKey:         "DAMAGE:101:6",
			MeasurementsJSON: `{"lip_before":150.0,"lip_now":103.0,"drop":47}`,
		},
		{
			AlertID: "550e8400-e29b-41d4-a716-446655440024", CapturedAt: windowStart.Add(12 * time.Hour), DetectedAt: windowStart.Add(12 * time.Hour), CreatedAt: windowStart.Add(12 * time.Hour),
			CameraID: "CAM-1", ObservationTarget: "godet", FaultType: "DAMAGE", Confidence: 0.88, FrameID: "550e8400-e29b-41d4-a716-446655440025", ModelVersion: "test-v1",
			BoundingBox:      store.BoundingBox{X: 0.38, Y: 0.11, Width: 0.05, Height: 0.16},
			EvidenceRef:      "2026/09/20/CAM-1/550e8400-e29b-41d4-a716-446655440024.jpg",
			GodetID:          202,
			AlertState:       "pending",
			LoopNo:           3,
			RuleID:           "DAMAGE",
			EventKey:         "DAMAGE:202:3",
			MeasurementsJSON: `{"lip_before":150.0,"lip_now":142.75,"drop":7.25}`,
		},
	}
	for _, alert := range alerts {
		if err := alertStore.InsertAlert(ctx, alert); err != nil {
			t.Fatal(err)
		}
	}

	stats := WindowStats{
		WindowStart:     windowStart,
		WindowEnd:       windowEnd,
		ProcessedEvents: 3,
		Stored:          3,
		DroppedCap:      0,
		Upsets:          1,
		ModelVersions:   []string{"test-v1"},
		MaxAlerts:       1000,
	}
	outPath := filepath.Join(root, "reports", "night.md")
	if err := Generate(ctx, alertStore, stats, outPath); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"101",
		"202",
		"Total alerts: 3",
		"Distinct godets: 2",
		"Pending: 2",
		"Confirmed: 1",
		"Seen: 1",
		"Unseen: 2",
		"test-v1",
		"stored 3 (cap 1000)",
		"dropped at cap",
		"Storage note:",
		windowStart.Format(time.RFC3339),
		windowEnd.Format(time.RFC3339),
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("report missing %q:\n%s", want, content)
		}
	}
}
