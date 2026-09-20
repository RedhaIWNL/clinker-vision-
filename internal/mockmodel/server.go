// Package mockmodel provides a deterministic v2 gRPC model implementation for
// pipeline integration tests and local development.
package mockmodel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	inferencev2 "github.com/clinkervision/clinker-vision/internal/inference/gen/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Mode string

const (
	ModeEmpty              Mode = "empty"
	ModeCrack              Mode = "crack"
	ModeMisalignment       Mode = "misalignment"
	ModeMultiple           Mode = "multiple"
	ModeLowConfidence      Mode = "low-confidence"
	ModeTimeout            Mode = "timeout"
	ModeUnavailable        Mode = "unavailable"
	ModeInvalidResponse    Mode = "invalid-response"
	ModeInvalidBoundingBox Mode = "invalid-bounding-box"
)

var supportedModes = map[Mode]struct{}{
	ModeEmpty: {}, ModeCrack: {}, ModeMisalignment: {}, ModeMultiple: {},
	ModeLowConfidence: {}, ModeTimeout: {}, ModeUnavailable: {},
	ModeInvalidResponse: {}, ModeInvalidBoundingBox: {},
}

func ParseMode(raw string) (Mode, error) {
	mode := Mode(raw)
	if _, ok := supportedModes[mode]; !ok {
		return "", fmt.Errorf("unsupported mock mode %q", raw)
	}
	return mode, nil
}

type Server struct {
	inferencev2.UnimplementedInferenceServiceServer
	mode         Mode
	modelVersion string
	timeoutDelay time.Duration
	now          func() time.Time
}

func New(mode Mode, modelVersion string, timeoutDelay time.Duration) (*Server, error) {
	if _, ok := supportedModes[mode]; !ok {
		return nil, fmt.Errorf("unsupported mock mode %q", mode)
	}
	if modelVersion == "" {
		return nil, errors.New("model version is required")
	}
	if timeoutDelay <= 0 {
		return nil, errors.New("timeout delay must be positive")
	}
	return &Server{mode: mode, modelVersion: modelVersion, timeoutDelay: timeoutDelay, now: time.Now}, nil
}

func (s *Server) Infer(stream inferencev2.InferenceService_InferServer) error {
	for {
		request, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if s.mode == ModeUnavailable {
			return status.Error(codes.Unavailable, "mock model is unavailable")
		}
		if s.mode == ModeTimeout {
			timer := time.NewTimer(s.timeoutDelay)
			select {
			case <-timer.C:
			case <-stream.Context().Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return stream.Context().Err()
			}
		}
		if err := stream.Send(s.response(request)); err != nil {
			return err
		}
	}
}

func (s *Server) GetGodetState(_ context.Context, _ *inferencev2.GodetStateRequest) (*inferencev2.GodetStateResponse, error) {
	return &inferencev2.GodetStateResponse{ModelVersion: s.modelVersion, Health: &inferencev2.Health{Ready: true, LoopLocked: true, Status: "ok", Detail: "mock"}}, nil
}

func (s *Server) response(request *inferencev2.InferenceRequest) *inferencev2.InferenceResponse {
	response := &inferencev2.InferenceResponse{FrameId: request.GetFrameId(), ModelVersion: s.modelVersion, ProcessedAt: timestamppb.New(s.now().UTC()), ScalarMeasurements: map[string]float32{
		"lit_L": 140, "lit_R": 135, "peak": 0.9, "dx": 0, "dy": 0, "slot": -1, "status_code": 0,
	}}
	switch s.mode {
	case ModeCrack, ModeMisalignment, ModeMultiple:
		response.Detections = []*inferencev2.Detection{damageDetection()}
		if s.mode == ModeMultiple {
			response.Detections = append(response.Detections, damageDetection())
		}
	case ModeLowConfidence:
		response.ScalarMeasurements["peak"] = 0.1
	case ModeInvalidResponse:
		response.ModelVersion = ""
	case ModeInvalidBoundingBox:
		response.Detections = []*inferencev2.Detection{{ObservationTarget: "GODET", FaultType: "DAMAGE", BoundingBox: &inferencev2.BoundingBox{X: 1.2, Y: -0.1, Width: -0.2, Height: 2}}}
	}
	return response
}

func damageDetection() *inferencev2.Detection {
	return &inferencev2.Detection{ObservationTarget: "GODET", FaultType: "DAMAGE", BoundingBox: &inferencev2.BoundingBox{X: 0.3806, Y: 0.1158, Width: 0.0536, Height: 0.1697}}
}
