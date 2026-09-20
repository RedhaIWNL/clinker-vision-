package alerting

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	inferencev1 "github.com/clinkervision/clinker-vision/internal/inference/gen"
	inferencev2 "github.com/clinkervision/clinker-vision/internal/inference/gen/v2"
	"github.com/clinkervision/clinker-vision/internal/ingest"
	"github.com/clinkervision/clinker-vision/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestProcessorFiltersAndCreatesOneAlertPerDetection(t *testing.T) {
	root := t.TempDir()
	alertStore := openProcessorStore(t, root)
	processor, err := NewProcessor(0.85, 85, filepath.Join(root, "evidence"), alertStore)
	if err != nil {
		t.Fatal(err)
	}
	processor.Now = func() time.Time { return time.Date(2026, 9, 17, 8, 1, 0, 0, time.UTC) }
	frame := processorFrame()
	response := processorResponse(frame.FrameID, 0.95, 0.80)
	alerts, err := processor.Process(context.Background(), frame, response)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 {
		t.Fatalf("got %d alerts, want one qualifying alert", len(alerts))
	}
	if alerts[0].FaultType != "CRACK" || alerts[0].ObservationTarget != "godet" || alerts[0].ModelVersion != "mock-test" {
		t.Fatalf("unexpected alert: %#v", alerts[0])
	}
	if _, err := os.Stat(filepath.Join(root, "evidence", alerts[0].EvidenceRef)); err != nil {
		t.Fatalf("evidence was not written: %v", err)
	}
	if count, err := alertStore.Count(context.Background()); err != nil || count != 1 {
		t.Fatalf("stored count=%d err=%v", count, err)
	}
}

func TestProcessorCreatesOneAlertForEachQualifyingDetection(t *testing.T) {
	root := t.TempDir()
	alertStore := openProcessorStore(t, root)
	processor, err := NewProcessor(0.85, 85, filepath.Join(root, "evidence"), alertStore)
	if err != nil {
		t.Fatal(err)
	}
	frame := processorFrame()
	response := processorResponse(frame.FrameID, 0.95, 0.91)
	alerts, err := processor.Process(context.Background(), frame, response)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 2 {
		t.Fatalf("got %d alerts, want two", len(alerts))
	}
	for _, alert := range alerts {
		if _, err := os.Stat(filepath.Join(root, "evidence", alert.EvidenceRef)); err != nil {
			t.Errorf("evidence %s missing: %v", alert.EvidenceRef, err)
		}
	}
}

func TestProcessorDoesNotPersistLowConfidenceDetection(t *testing.T) {
	root := t.TempDir()
	alertStore := openProcessorStore(t, root)
	processor, err := NewProcessor(0.85, 85, filepath.Join(root, "evidence"), alertStore)
	if err != nil {
		t.Fatal(err)
	}
	frame := processorFrame()
	alerts, err := processor.Process(context.Background(), frame, processorResponse(frame.FrameID, 0.84))
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 0 {
		t.Fatalf("got %d alerts for low confidence detection", len(alerts))
	}
	if count, err := alertStore.Count(context.Background()); err != nil || count != 0 {
		t.Fatalf("stored count=%d err=%v", count, err)
	}
}

func TestProcessorRemovesEvidenceWhenDatabaseInsertFails(t *testing.T) {
	root := t.TempDir()
	alertStore := openProcessorStore(t, root)
	if err := alertStore.Close(); err != nil {
		t.Fatal(err)
	}
	processor, err := NewProcessor(0.85, 85, filepath.Join(root, "evidence"), alertStore)
	if err != nil {
		t.Fatal(err)
	}
	frame := processorFrame()
	if _, err := processor.Process(context.Background(), frame, processorResponse(frame.FrameID, 0.95)); err == nil {
		t.Fatal("expected database insert failure")
	}
	var evidenceFiles int
	_ = filepath.Walk(filepath.Join(root, "evidence"), func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() && filepath.Ext(path) == ".jpg" {
			evidenceFiles++
		}
		return nil
	})
	if evidenceFiles != 0 {
		t.Fatalf("found %d orphan evidence files", evidenceFiles)
	}
}

func TestQualifyingDetectionsUsesInclusiveThreshold(t *testing.T) {
	response := &inferencev1.InferenceResponse{Detections: []*inferencev1.Detection{
		{Confidence: 0.85}, {Confidence: 0.849},
	}}
	if got := len(QualifyingDetections(response, 0.85)); got != 1 {
		t.Fatalf("got %d qualifying detections, want 1", got)
	}
}

func TestGodetEventsUpsertPendingToConfirmedAndUseCandidateFrame(t *testing.T) {
	root := t.TempDir()
	alertStore := openProcessorStore(t, root)
	frame := processorFrame()
	health := &inferencev2.Health{Ready: true, LoopLocked: true, Status: "ok"}
	godet := &inferencev2.GodetState{GodetId: 224, State: "pending", Lip: 79, LastSeenLoop: 3}
	event := &inferencev2.GodetAlertEvent{EventKey: "DAMAGE:224:3", Kind: "damage", GodetId: 224, LoopNo: 3, State: "pending", EvidenceFrameId: frame.FrameID, Measurements: map[string]float64{"drop": 47}}
	state := &inferencev2.GodetStateResponse{ModelVersion: "model-v2", Godets: []*inferencev2.GodetState{godet}, Events: []*inferencev2.GodetAlertEvent{event}, Health: health}
	if _, err := ProcessGodetState(context.Background(), state, frame, 85, filepath.Join(root, "evidence"), alertStore, func() time.Time { return frame.CapturedAt.Add(time.Minute) }); err != nil {
		t.Fatal(err)
	}
	godet.State = "confirmed"
	event.State = "confirmed"
	if _, err := ProcessGodetState(context.Background(), state, frame, 85, filepath.Join(root, "evidence"), alertStore, func() time.Time { return frame.CapturedAt.Add(2 * time.Minute) }); err != nil {
		t.Fatal(err)
	}
	if count, err := alertStore.Count(context.Background()); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	alerts, err := alertStore.ListAlerts(context.Background(), store.ListFilter{Limit: 10})
	if err != nil || len(alerts.Alerts) != 1 || alerts.Alerts[0].AlertState != "confirmed" || alerts.Alerts[0].EvidenceFrameID != frame.FrameID {
		t.Fatalf("alerts=%#v err=%v", alerts, err)
	}
}

func openProcessorStore(t *testing.T, root string) *store.Store {
	t.Helper()
	alertStore, err := store.Open(context.Background(), filepath.Join(root, "data", "alerts.db"), filepath.Join(root, "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = alertStore.Close() })
	return alertStore
}

func processorFrame() ingest.Frame {
	frame := image.NewRGBA(image.Rect(0, 0, 100, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 100; x++ {
			frame.SetRGBA(x, y, color.RGBA{R: 70, G: 70, B: 70, A: 255})
		}
	}
	var encoded bytes.Buffer
	_ = jpeg.Encode(&encoded, frame, &jpeg.Options{Quality: 95})
	return ingest.Frame{
		FrameID:    "550e8400-e29b-41d4-a716-446655440020",
		CameraID:   "CAM-1",
		ImageData:  encoded.Bytes(),
		CapturedAt: time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC),
		SequenceNo: 1,
	}
}

func processorResponse(frameID string, confidences ...float32) *inferencev1.InferenceResponse {
	detections := make([]*inferencev1.Detection, 0, len(confidences))
	for index, confidence := range confidences {
		detections = append(detections, &inferencev1.Detection{
			ObservationTarget: inferencev1.ObservationTarget_GODET,
			FaultType:         inferencev1.FaultType_CRACK,
			Confidence:        confidence,
			BoundingBox:       &inferencev1.BoundingBox{X: float32(0.1 + float64(index)*0.4), Y: 0.2, Width: 0.2, Height: 0.2},
		})
	}
	return &inferencev1.InferenceResponse{
		FrameId:      frameID,
		Detections:   detections,
		ModelVersion: "mock-test",
		ProcessedAt:  timestamppb.New(time.Date(2026, 9, 17, 8, 0, 1, 0, time.UTC)),
	}
}
