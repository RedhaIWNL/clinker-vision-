package alerting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/clinkervision/clinker-vision/internal/evidence"
	"github.com/clinkervision/clinker-vision/internal/inference"
	inferencev1 "github.com/clinkervision/clinker-vision/internal/inference/gen"
	"github.com/clinkervision/clinker-vision/internal/ingest"
	"github.com/clinkervision/clinker-vision/internal/store"
	"github.com/google/uuid"
)

type Processor struct {
	Threshold    float32
	JPEGQuality  int
	EvidenceRoot string
	Store        *store.Store
	Now          func() time.Time
}

func NewProcessor(threshold float32, jpegQuality int, evidenceRoot string, alertStore *store.Store) (*Processor, error) {
	if threshold < 0 || threshold > 1 {
		return nil, errors.New("confidence threshold must be between 0 and 1")
	}
	if jpegQuality < 1 || jpegQuality > 100 {
		return nil, errors.New("JPEG quality must be between 1 and 100")
	}
	if evidenceRoot == "" || alertStore == nil {
		return nil, errors.New("evidence root and alert store are required")
	}
	return &Processor{
		Threshold:    threshold,
		JPEGQuality:  jpegQuality,
		EvidenceRoot: evidenceRoot,
		Store:        alertStore,
		Now:          time.Now,
	}, nil
}

func (p *Processor) Process(ctx context.Context, frame ingest.Frame, response *inferencev1.InferenceResponse) ([]store.Alert, error) {
	validated, err := inference.ValidateResponseV1(frame.FrameID, response)
	if err != nil {
		return nil, err
	}
	qualifying := QualifyingDetections(validated, p.Threshold)
	if len(qualifying) == 0 {
		return nil, nil
	}
	encoded, err := evidence.RenderJPEG(frame.ImageData, validated.GetDetections(), p.JPEGQuality)
	if err != nil {
		return nil, fmt.Errorf("render alert evidence: %w", err)
	}

	now := p.Now
	if now == nil {
		now = time.Now
	}
	created := now()
	processed := validated.GetProcessedAt().AsTime()
	alerts := make([]store.Alert, 0, len(qualifying))
	for _, detection := range qualifying {
		alertID := uuid.New()
		evidenceRef, err := evidence.Reference(frame.CapturedAt, frame.CameraID, alertID.String())
		if err != nil {
			return alerts, err
		}
		if err := evidence.WriteAtomic(p.EvidenceRoot, evidenceRef, encoded); err != nil {
			return alerts, fmt.Errorf("write alert evidence: %w", err)
		}
		alert := store.Alert{
			AlertID:           alertID.String(),
			CapturedAt:        frame.CapturedAt,
			DetectedAt:        frame.CapturedAt,
			ProcessedAt:       &processed,
			CreatedAt:         created,
			CameraID:          frame.CameraID,
			ObservationTarget: strings.ToLower(detection.GetObservationTarget().String()),
			FaultType:         detection.GetFaultType().String(),
			Confidence:        detection.GetConfidence(),
			FrameID:           frame.FrameID,
			ModelVersion:      validated.GetModelVersion(),
			BoundingBox: store.BoundingBox{
				X:      detection.GetBoundingBox().GetX(),
				Y:      detection.GetBoundingBox().GetY(),
				Width:  detection.GetBoundingBox().GetWidth(),
				Height: detection.GetBoundingBox().GetHeight(),
			},
			EvidenceRef: evidenceRef,
		}
		if err := p.Store.InsertAlert(ctx, alert); err != nil {
			_ = evidence.Remove(p.EvidenceRoot, evidenceRef)
			return alerts, err
		}
		alerts = append(alerts, alert)
	}
	return alerts, nil
}

func QualifyingDetections(response *inferencev1.InferenceResponse, threshold float32) []*inferencev1.Detection {
	if response == nil {
		return nil
	}
	qualifying := make([]*inferencev1.Detection, 0, len(response.GetDetections()))
	for _, detection := range response.GetDetections() {
		if detection != nil && detection.GetConfidence() >= threshold {
			qualifying = append(qualifying, detection)
		}
	}
	return qualifying
}
