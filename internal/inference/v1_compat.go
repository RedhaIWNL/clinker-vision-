package inference

import (
	"fmt"
	"math"

	inferencev1 "github.com/clinkervision/clinker-vision/internal/inference/gen"
	"google.golang.org/protobuf/proto"
)

// ValidateResponseV1 is retained only for the legacy alerting unit tests and
// migration compatibility. The production pipeline uses the v2 client and
// Tier-2 godet state.
func ValidateResponseV1(expectedFrameID string, response *inferencev1.InferenceResponse) (*inferencev1.InferenceResponse, error) {
	if response == nil || response.GetFrameId() != expectedFrameID || response.GetModelVersion() == "" || response.GetProcessedAt().CheckValid() != nil {
		return nil, fmt.Errorf("%w: malformed v1 response", ErrInvalidResponse)
	}
	validated := proto.Clone(response).(*inferencev1.InferenceResponse)
	for i, detection := range validated.GetDetections() {
		if detection == nil || detection.GetBoundingBox() == nil || !validV1Target(detection.GetObservationTarget()) || !validV1Fault(detection.GetFaultType()) {
			return nil, fmt.Errorf("%w: malformed v1 detection %d", ErrInvalidResponse, i)
		}
		if detection.GetConfidence() < 0 || detection.GetConfidence() > 1 || math.IsNaN(float64(detection.GetConfidence())) || math.IsInf(float64(detection.GetConfidence()), 0) {
			return nil, fmt.Errorf("%w: malformed v1 confidence", ErrInvalidResponse)
		}
		box := detection.GetBoundingBox()
		if !finite(box.GetX()) || !finite(box.GetY()) || !finite(box.GetWidth()) || !finite(box.GetHeight()) || box.GetX() < 0 || box.GetY() < 0 || box.GetWidth() <= 0 || box.GetHeight() <= 0 || box.GetX()+box.GetWidth() > 1 || box.GetY()+box.GetHeight() > 1 {
			return nil, fmt.Errorf("%w: malformed v1 bounding box", ErrInvalidResponse)
		}
	}
	return validated, nil
}

func validV1Target(value inferencev1.ObservationTarget) bool {
	return value == inferencev1.ObservationTarget_GODET || value == inferencev1.ObservationTarget_GALET || value == inferencev1.ObservationTarget_CLINKER_LEVEL
}
func validV1Fault(value inferencev1.FaultType) bool {
	return value != inferencev1.FaultType_FAULT_TYPE_UNSPECIFIED
}
