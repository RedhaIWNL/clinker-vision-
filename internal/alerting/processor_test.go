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

func TestCAM4EventUsesItsOwnBoxKeyAndMeasurements(t *testing.T) {
	root := t.TempDir()
	alertStore := openProcessorStore(t, root)
	cam1 := processorFrame()
	cam4 := processorFrame()
	cam4.CameraID = "CAM-4"
	cam4.FrameID = "550e8400-e29b-41d4-a716-446655440044"
	health := &inferencev2.Health{Ready: true, LoopLocked: true, Status: "ok"}
	now := func() time.Time { return cam1.CapturedAt.Add(time.Minute) }
	// CAM-1 godet 227 loop 3 and CAM-4 godet 227 loop 3 must stay two alerts.
	g1 := &inferencev2.GodetState{GodetId: 227, State: "confirmed", Lip: 60, LastSeenLoop: 3}
	e1 := &inferencev2.GodetAlertEvent{EventKey: EventKey("CAM-1", 227, 3), Kind: "damage", GodetId: 227, LoopNo: 3, State: "confirmed", EvidenceFrameId: cam1.FrameID}
	if _, err := ProcessGodetState(context.Background(), &inferencev2.GodetStateResponse{ModelVersion: "m1", Godets: []*inferencev2.GodetState{g1}, Events: []*inferencev2.GodetAlertEvent{e1}, Health: health}, cam1, 85, filepath.Join(root, "evidence"), alertStore, now); err != nil {
		t.Fatal(err)
	}
	g4 := &inferencev2.GodetState{GodetId: 227, State: "confirmed", Severity: 2.4, PassesSeen: 3, LastSeenLoop: 3}
	e4 := &inferencev2.GodetAlertEvent{EventKey: EventKey("CAM-4", 227, 3), Kind: "damage", GodetId: 227, LoopNo: 3, State: "confirmed", EvidenceFrameId: cam4.FrameID,
		EvidenceBox: &inferencev2.BoundingBox{X: 0.3, Y: 0.6, Width: 0.1, Height: 0.17}}
	if _, err := ProcessGodetState(context.Background(), &inferencev2.GodetStateResponse{ModelVersion: "m4", Godets: []*inferencev2.GodetState{g4}, Events: []*inferencev2.GodetAlertEvent{e4}, Health: health}, cam4, 85, filepath.Join(root, "evidence"), alertStore, now); err != nil {
		t.Fatal(err)
	}
	if e4.GetEventKey() != "CAM-4:DAMAGE:227:3" || e1.GetEventKey() != "DAMAGE:227:3" {
		t.Fatalf("keys %q %q", e1.GetEventKey(), e4.GetEventKey())
	}
	list, err := alertStore.ListAlerts(context.Background(), store.ListFilter{CameraID: "CAM-4", Limit: 10})
	if err != nil || len(list.Alerts) != 1 {
		t.Fatalf("CAM-4 alerts=%#v err=%v", list, err)
	}
	a := list.Alerts[0]
	if a.BoundingBox.X != 0.3 || a.BoundingBox.Height != 0.17 {
		t.Fatalf("CAM-4 box not used: %+v", a.BoundingBox)
	}
	if !bytes.Contains([]byte(a.MeasurementsJSON), []byte(`"severity"`)) || bytes.Contains([]byte(a.MeasurementsJSON), []byte(`"lip"`)) {
		t.Fatalf("CAM-4 measurements = %s", a.MeasurementsJSON)
	}
	if n, _ := alertStore.Count(context.Background()); n != 2 {
		t.Fatalf("want 2 alerts (one per camera), got %d", n)
	}
	one, err := alertStore.ListAlerts(context.Background(), store.ListFilter{CameraID: "CAM-1", Limit: 10})
	if err != nil || len(one.Alerts) != 1 || one.Alerts[0].BoundingBox.X != fixedGodetBox.X {
		t.Fatalf("CAM-1 alert changed: %#v err=%v", one, err)
	}
	if state, found, err := alertStore.EventState(context.Background(), "CAM-4:DAMAGE:227:3"); err != nil || !found || state != "confirmed" {
		t.Fatalf("EventState = %q %v %v", state, found, err)
	}
}

func TestEvidenceFallbackIsMarked(t *testing.T) {
	root := t.TempDir()
	alertStore := openProcessorStore(t, root)
	live := processorFrame()
	live.CameraID = "CAM-4"
	g := &inferencev2.GodetState{GodetId: 5, State: "confirmed", Severity: 2, LastSeenLoop: 1}
	e := &inferencev2.GodetAlertEvent{EventKey: EventKey("CAM-4", 5, 1), Kind: "damage", GodetId: 5, LoopNo: 1, State: "confirmed", EvidenceFrameId: "550e8400-e29b-41d4-a716-446655449999"}
	if _, err := ProcessGodetState(context.Background(), &inferencev2.GodetStateResponse{ModelVersion: "m4", Godets: []*inferencev2.GodetState{g}, Events: []*inferencev2.GodetAlertEvent{e}, Health: &inferencev2.Health{}}, live, 85, filepath.Join(root, "evidence"), alertStore, time.Now); err != nil {
		t.Fatal(err)
	}
	list, _ := alertStore.ListAlerts(context.Background(), store.ListFilter{Limit: 10})
	if len(list.Alerts) != 1 || !bytes.Contains([]byte(list.Alerts[0].MeasurementsJSON), []byte(`"evidence":"latest_frame"`)) {
		t.Fatalf("fallback not marked: %#v", list.Alerts)
	}
}

func TestWheelEventIsStoredAsGaletAlert(t *testing.T) {
	root := t.TempDir()
	alertStore := openProcessorStore(t, root)
	strip := processorFrame() // stands for the strip picture the model hands back
	strip.CameraID = "CAM-4"
	g := &inferencev2.GodetState{GodetId: 113, LastSeenLoop: 2}
	e := &inferencev2.GodetAlertEvent{EventKey: "CAM-4:WHEEL_GAP:113", Kind: "wheel_gap", GodetId: 113, LoopNo: 2, State: "confirmed",
		EvidenceFrameId: strip.FrameID, EvidenceBox: &inferencev2.BoundingBox{X: 0.3, Y: 0, Width: 0.45, Height: 1},
		Measurements: map[string]float64{"first_godet": 113, "last_godet": 119, "godets": 7, "wheels": 0}}
	if _, err := ProcessGodetState(context.Background(), &inferencev2.GodetStateResponse{ModelVersion: "m4", Godets: []*inferencev2.GodetState{g}, Events: []*inferencev2.GodetAlertEvent{e}, Health: &inferencev2.Health{}}, strip, 85, filepath.Join(root, "evidence"), alertStore, time.Now); err != nil {
		t.Fatal(err)
	}
	list, err := alertStore.ListAlerts(context.Background(), store.ListFilter{Limit: 10})
	if err != nil || len(list.Alerts) != 1 {
		t.Fatalf("alerts=%#v err=%v", list, err)
	}
	a := list.Alerts[0]
	if a.ObservationTarget != "galet" || a.FaultType != "WHEEL_GAP" || a.RuleID != "WHEEL_GAP" || a.GodetID != 113 {
		t.Fatalf("wheel alert stored as %+v", a)
	}
	if !bytes.Contains([]byte(a.MeasurementsJSON), []byte(`"last_godet":119`)) || bytes.Contains([]byte(a.MeasurementsJSON), []byte(`"severity"`)) {
		t.Fatalf("measurements = %s", a.MeasurementsJSON)
	}
	if a.BoundingBox.Width != 0.45 {
		t.Fatalf("box = %+v", a.BoundingBox)
	}
	unknown := &inferencev2.GodetAlertEvent{EventKey: "X", Kind: "something_else", GodetId: 5, LoopNo: 1, State: "confirmed"}
	if alerts, err := ProcessGodetState(context.Background(), &inferencev2.GodetStateResponse{ModelVersion: "m4", Godets: []*inferencev2.GodetState{{GodetId: 5}}, Events: []*inferencev2.GodetAlertEvent{unknown}, Health: &inferencev2.Health{}}, strip, 85, filepath.Join(root, "evidence"), alertStore, time.Now); err != nil || len(alerts) != 0 {
		t.Fatalf("unknown kinds must be ignored: %v %v", alerts, err)
	}
}

func TestSidePlateEventWithoutBoxGetsNoOldFixedBox(t *testing.T) {
	// 2026-09-30: new Camera 1 alerts arrived without a box and were drawn with the old CAM-1 fixed
	// box, which sits on the roof of the re-aimed view. A side-plate event without a box is stored as
	// the whole frame and nothing is drawn.
	root := t.TempDir()
	alertStore := openProcessorStore(t, root)
	live := processorFrame()
	live.CameraID = "CAM-1"
	g := &inferencev2.GodetState{GodetId: 44, State: "confirmed", LastSeenLoop: 2}
	e := &inferencev2.GodetAlertEvent{EventKey: "CAM-1:DAMAGE:44:2", Kind: "damage", GodetId: 44, LoopNo: 2, State: "confirmed",
		Measurements: map[string]float64{"severity": 0.9, "damage_type": 3}}
	if _, err := ProcessGodetState(context.Background(), &inferencev2.GodetStateResponse{ModelVersion: "m1", Godets: []*inferencev2.GodetState{g}, Events: []*inferencev2.GodetAlertEvent{e}, Health: &inferencev2.Health{}}, live, 85, filepath.Join(root, "evidence"), alertStore, time.Now); err != nil {
		t.Fatal(err)
	}
	list, _ := alertStore.ListAlerts(context.Background(), store.ListFilter{Limit: 10})
	if len(list.Alerts) != 1 {
		t.Fatalf("alerts = %#v", list.Alerts)
	}
	if b := list.Alerts[0].BoundingBox; b.X != 0 || b.Y != 0 || b.Width != 1 || b.Height != 1 {
		t.Fatalf("box = %+v, want the whole frame (not the old fixed CAM-1 box)", b)
	}
}
