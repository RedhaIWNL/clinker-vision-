package alerting

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/clinkervision/clinker-vision/internal/evidence"
	inferencev2 "github.com/clinkervision/clinker-vision/internal/inference/gen/v2"
	"github.com/clinkervision/clinker-vision/internal/ingest"
	"github.com/clinkervision/clinker-vision/internal/store"
	"github.com/google/uuid"
)

var fixedGodetBox = evidence.BoundingBox{X: 0.3806, Y: 0.1158, Width: 0.0536, Height: 0.1697}

// eventKinds maps a Tier-2 event kind to what is stored: the observation target and the fault
// type. Wheel (galet) kinds come from the side-plate cameras' wheel tracker.
var eventKinds = map[string]struct{ target, fault string }{
	"damage":        {"godet", "DAMAGE"},
	"wheel_gap":     {"galet", "WHEEL_GAP"},     // 5 or more godets in a row without a wheel
	"wheel_density": {"galet", "WHEEL_DENSITY"}, // 2 or more wheels within 3 godets
	"wheel_missing": {"galet", "WHEEL_MISSING"}, // a known wheel absent on 2 passes
}

// EventKey is the stable deduplication key of a godet damage event. CAM-1 keeps its
// original DAMAGE:<godet>:<loop> keys; other cameras are prefixed so equal godet and
// loop numbers on two cameras never share (and overwrite) one alert.
func EventKey(cameraID string, godetID, loop int32) string {
	if cameraID == "" || cameraID == "CAM-1" {
		return fmt.Sprintf("DAMAGE:%d:%d", godetID, loop)
	}
	return fmt.Sprintf("%s:DAMAGE:%d:%d", cameraID, godetID, loop)
}

// eventBox is the box drawn on the evidence: the event's own fault-spot box when the
// model sends one (CAM-4), else CAM-1's fixed ROI indicator.
func eventBox(event *inferencev2.GodetAlertEvent) evidence.BoundingBox {
	b := event.GetEvidenceBox()
	if b == nil || b.GetWidth() <= 0 || b.GetHeight() <= 0 {
		return fixedGodetBox
	}
	x := clamp01(float64(b.GetX()))
	y := clamp01(float64(b.GetY()))
	w := math.Min(float64(b.GetWidth()), 1-x)
	h := math.Min(float64(b.GetHeight()), 1-y)
	if w <= 0 || h <= 0 {
		return fixedGodetBox
	}
	return evidence.BoundingBox{X: float32(x), Y: float32(y), Width: float32(w), Height: float32(h)}
}

func clamp01(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}

// ProcessGodetState consumes Tier-2 state. Tier-1 Infer responses are never
// passed here and therefore cannot create operator alerts.
func ProcessGodetState(ctx context.Context, state *inferencev2.GodetStateResponse, frame ingest.Frame, quality int, evidenceRoot string, alertStore *store.Store, now func() time.Time) ([]store.Alert, error) {
	if state == nil || state.GetHealth() == nil || alertStore == nil {
		return nil, fmt.Errorf("godet state, health, and store are required")
	}
	if now == nil {
		now = time.Now
	}
	if frame.FrameID == "" || len(frame.ImageData) == 0 {
		return nil, fmt.Errorf("evidence frame is required")
	}
	if len(state.GetEvents()) > 0 {
		godets := make(map[int32]*inferencev2.GodetState, len(state.GetGodets()))
		for _, godet := range state.GetGodets() {
			if godet != nil {
				godets[godet.GetGodetId()] = godet
			}
		}
		created := make([]store.Alert, 0, len(state.GetEvents()))
		for _, event := range state.GetEvents() {
			godet := godets[event.GetGodetId()]
			alert, err := processGodetEvent(ctx, state, event, godet, frame, quality, evidenceRoot, alertStore, now)
			if err != nil {
				return created, err
			}
			if alert != nil {
				created = append(created, *alert)
			}
		}
		return created, nil
	}
	var created []store.Alert
	for _, godet := range state.GetGodets() {
		if godet == nil || (godet.GetState() != "pending" && godet.GetState() != "confirmed") || godet.GetLastSeenLoop() < 0 {
			continue
		}
		event := &inferencev2.GodetAlertEvent{EventKey: EventKey(frame.CameraID, godet.GetGodetId(), godet.GetLastSeenLoop()), Kind: "damage", GodetId: godet.GetGodetId(), LoopNo: godet.GetLastSeenLoop(), State: strings.ToLower(godet.GetState()), EvidenceFrameId: frame.FrameID}
		alert, err := processGodetEvent(ctx, state, event, godet, frame, quality, evidenceRoot, alertStore, now)
		if err != nil {
			return created, err
		}
		if alert != nil {
			created = append(created, *alert)
		}
	}
	return created, nil
}

func processGodetEvent(ctx context.Context, state *inferencev2.GodetStateResponse, event *inferencev2.GodetAlertEvent, godet *inferencev2.GodetState, frame ingest.Frame, quality int, evidenceRoot string, alertStore *store.Store, now func() time.Time) (*store.Alert, error) {
	kind, known := eventKinds[event.GetKind()]
	if event == nil || !known || (event.GetState() != "pending" && event.GetState() != "confirmed") || event.GetGodetId() <= 0 || event.GetLoopNo() < 0 {
		return nil, nil
	}
	if godet == nil {
		return nil, fmt.Errorf("godet %d is missing from state response", event.GetGodetId())
	}
	if now == nil {
		now = time.Now
	}
	eventKey := event.GetEventKey()
	if eventKey == "" {
		eventKey = EventKey(frame.CameraID, event.GetGodetId(), event.GetLoopNo())
	}
	alertID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(eventKey))
	capturedAt := frame.CapturedAt
	if capturedAt.IsZero() {
		capturedAt = now().UTC()
	}
	seenNow := now().UTC()
	measurementValues := make(map[string]any, len(event.GetMeasurements())+4)
	for key, value := range event.GetMeasurements() {
		measurementValues[key] = value
	}
	switch {
	case kind.target == "galet":
		// wheel events carry their own measurements (first/last godet, godets, wheels)
	case frame.CameraID == "CAM-3" || frame.CameraID == "CAM-4": // side-plate damage
		measurementValues["severity"] = godet.GetSeverity()
		measurementValues["passes_seen"] = godet.GetPassesSeen()
	default:
		measurementValues["lip"] = godet.GetLip()
		measurementValues["near_plate"] = godet.GetNearPlate()
	}
	if want := event.GetEvidenceFrameId(); want != "" && want != frame.FrameID {
		// the named frame was no longer cached: the picture shows the live view, not the fault
		measurementValues["evidence"] = "latest_frame"
	}
	measurementValues["state"] = event.GetState()
	measurementValues["last_seen_loop"] = event.GetLoopNo()
	measurements, err := json.Marshal(measurementValues)
	if err != nil {
		return nil, err
	}
	ref, err := evidence.Reference(capturedAt, frame.CameraID, alertID.String())
	if err != nil {
		return nil, err
	}
	box := eventBox(event)
	jpegData, err := evidence.RenderJPEGWithBoxes(frame.ImageData, []evidence.BoxOverlay{{FaultType: kind.fault, BoundingBox: box}}, quality)
	if err != nil {
		return nil, fmt.Errorf("render godet evidence: %w", err)
	}
	if err := evidence.WriteAtomic(evidenceRoot, ref, jpegData); err != nil {
		return nil, fmt.Errorf("write godet evidence: %w", err)
	}
	evidenceFrameID := event.GetEvidenceFrameId()
	if evidenceFrameID == "" || evidenceFrameID != frame.FrameID {
		evidenceFrameID = frame.FrameID
	}
	alert := store.Alert{AlertID: alertID.String(), CapturedAt: capturedAt, DetectedAt: seenNow, CreatedAt: seenNow, CameraID: frame.CameraID, ObservationTarget: kind.target, FaultType: kind.fault, FrameID: frame.FrameID, ModelVersion: state.GetModelVersion(), BoundingBox: store.BoundingBox{X: box.X, Y: box.Y, Width: box.Width, Height: box.Height}, EvidenceRef: ref, GodetID: event.GetGodetId(), AlertState: strings.ToLower(event.GetState()), LoopNo: event.GetLoopNo(), RuleID: kind.fault, EventKey: eventKey, EvidenceFrameID: evidenceFrameID, MeasurementsJSON: string(measurements)}
	if err := alertStore.UpsertGodetAlert(ctx, alert); err != nil {
		_ = evidence.Remove(evidenceRoot, ref)
		return nil, err
	}
	return &alert, nil
}
