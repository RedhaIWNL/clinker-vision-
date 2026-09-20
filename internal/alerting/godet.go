package alerting

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/clinkervision/clinker-vision/internal/evidence"
	inferencev2 "github.com/clinkervision/clinker-vision/internal/inference/gen/v2"
	"github.com/clinkervision/clinker-vision/internal/ingest"
	"github.com/clinkervision/clinker-vision/internal/store"
	"github.com/google/uuid"
)

var fixedGodetBox = evidence.BoundingBox{X: 0.3806, Y: 0.1158, Width: 0.0536, Height: 0.1697}

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
		event := &inferencev2.GodetAlertEvent{EventKey: fmt.Sprintf("DAMAGE:%d:%d", godet.GetGodetId(), godet.GetLastSeenLoop()), Kind: "damage", GodetId: godet.GetGodetId(), LoopNo: godet.GetLastSeenLoop(), State: strings.ToLower(godet.GetState()), EvidenceFrameId: frame.FrameID}
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
	if event == nil || event.GetKind() != "damage" || (event.GetState() != "pending" && event.GetState() != "confirmed") || event.GetGodetId() <= 0 || event.GetLoopNo() < 0 {
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
		eventKey = fmt.Sprintf("DAMAGE:%d:%d", event.GetGodetId(), event.GetLoopNo())
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
	measurementValues["lip"] = godet.GetLip()
	measurementValues["near_plate"] = godet.GetNearPlate()
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
	jpegData, err := evidence.RenderJPEGWithBoxes(frame.ImageData, []evidence.BoxOverlay{{FaultType: "DAMAGE", BoundingBox: fixedGodetBox}}, quality)
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
	alert := store.Alert{AlertID: alertID.String(), CapturedAt: capturedAt, DetectedAt: seenNow, CreatedAt: seenNow, CameraID: frame.CameraID, ObservationTarget: "godet", FaultType: "DAMAGE", FrameID: frame.FrameID, ModelVersion: state.GetModelVersion(), BoundingBox: store.BoundingBox{X: fixedGodetBox.X, Y: fixedGodetBox.Y, Width: fixedGodetBox.Width, Height: fixedGodetBox.Height}, EvidenceRef: ref, GodetID: event.GetGodetId(), AlertState: strings.ToLower(event.GetState()), LoopNo: event.GetLoopNo(), RuleID: "DAMAGE", EventKey: eventKey, EvidenceFrameID: evidenceFrameID, MeasurementsJSON: string(measurements)}
	if err := alertStore.UpsertGodetAlert(ctx, alert); err != nil {
		_ = evidence.Remove(evidenceRoot, ref)
		return nil, err
	}
	return &alert, nil
}
