package inference

import (
	"context"
	"errors"
	"math"
	"net"
	"sync"
	"testing"
	"time"

	inferencev2 "github.com/clinkervision/clinker-vision/internal/inference/gen/v2"
	"github.com/clinkervision/clinker-vision/internal/ingest"
	"github.com/clinkervision/clinker-vision/internal/mockmodel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestClientCorrelatesResponsesAndTracksModelVersion(t *testing.T) {
	client := newTestClient(t, mockmodel.ModeCrack, time.Second)
	frames := []ingest.Frame{validFrame("550e8400-e29b-41d4-a716-446655440000"), validFrame("550e8400-e29b-41d4-a716-446655440001")}
	responses := make([]*inferencev2.InferenceResponse, len(frames))
	var wait sync.WaitGroup
	for i := range frames {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			var err error
			responses[i], err = client.Infer(context.Background(), frames[i])
			if err != nil {
				t.Errorf("inference %d: %v", i, err)
			}
		}(i)
	}
	wait.Wait()
	for i, response := range responses {
		if response == nil || response.GetFrameId() != frames[i].FrameID {
			t.Fatalf("response %d=%#v", i, response)
		}
	}
	if got := client.ModelVersion(); got != "mock-test" {
		t.Fatalf("version=%q", got)
	}
}

func TestClientTimeoutAndUnavailable(t *testing.T) {
	client := newTestClient(t, mockmodel.ModeTimeout, 250*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err := client.Infer(ctx, validFrame("550e8400-e29b-41d4-a716-446655440002"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}

	client = newTestClient(t, mockmodel.ModeUnavailable, time.Second)
	_, err = client.Infer(context.Background(), validFrame("550e8400-e29b-41d4-a716-446655440003"))
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("got %v: %v", status.Code(err), err)
	}
}

func TestClientReconnectsAfterTransportFailure(t *testing.T) {
	service := &failOnceService{}
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	inferencev2.RegisterInferenceServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(conn)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	frame := validFrame("550e8400-e29b-41d4-a716-446655440007")
	if _, err := client.Infer(context.Background(), frame); status.Code(err) != codes.Unavailable {
		t.Fatalf("first stream error=%v", err)
	}
	if err := client.Reconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	response, err := client.Infer(context.Background(), frame)
	if err != nil || response.GetFrameId() != frame.FrameID {
		t.Fatalf("reconnected response=%#v err=%v", response, err)
	}
}

func TestRequestAndResponseValidation(t *testing.T) {
	if _, err := RequestFromFrame(validFrame("not-a-uuid")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	frame := validFrame("550e8400-e29b-41d4-a716-446655440004")
	frame.ImageData = nil
	if _, err := RequestFromFrame(frame); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	response := &inferencev2.InferenceResponse{FrameId: "frame", ModelVersion: "model-test", ProcessedAt: timestamppb.Now(), ScalarMeasurements: requiredScalars()}
	response.Detections = []*inferencev2.Detection{{ObservationTarget: "GODET", FaultType: "DAMAGE", BoundingBox: &inferencev2.BoundingBox{X: 0.9, Y: 0.1, Width: 0.4, Height: 0.2}}}
	validated, err := ValidateResponse("frame", response)
	if err != nil {
		t.Fatal(err)
	}
	box := validated.GetDetections()[0].GetBoundingBox()
	if math.Abs(float64(box.GetWidth()-0.1)) > 0.0001 || response.GetDetections()[0].GetBoundingBox().GetWidth() != 0.4 {
		t.Fatalf("box validation failed: %#v", box)
	}
	if _, err := ValidateResponse("frame", nil); !errors.Is(err, ErrInvalidResponse) {
		t.Fatal(err)
	}
	bad := &inferencev2.InferenceResponse{FrameId: "frame", ModelVersion: "m", ProcessedAt: timestamppb.Now(), ScalarMeasurements: requiredScalars()}
	bad.ScalarMeasurements["peak"] = float32(math.NaN())
	if _, err := ValidateResponse("frame", bad); !errors.Is(err, ErrInvalidResponse) {
		t.Fatal(err)
	}
}

func TestRetryableTransportErrorDoesNotReplayDeadlineFrames(t *testing.T) {
	if IsRetryableTransportError(context.DeadlineExceeded) {
		t.Fatal("deadline errors must not be replayed")
	}
	if !IsRetryableTransportError(status.Error(codes.Unavailable, "model restarted")) {
		t.Fatal("unavailable transport should be retried")
	}
	if !IsRetryableTransportError(ErrStreamUnavailable) {
		t.Fatal("missing stream should be retried")
	}
}

func TestClientGetGodetState(t *testing.T) {
	client := newTestClient(t, mockmodel.ModeEmpty, time.Second)
	state, err := client.GetGodetState(context.Background(), &inferencev2.GodetStateRequest{IncludeHistory: true})
	if err != nil || state.GetHealth().GetStatus() != "ok" {
		t.Fatalf("state=%#v err=%v", state, err)
	}
}

func TestMockClientRecordsRequests(t *testing.T) {
	client := &MockClient{Respond: func(context.Context, ingest.Frame) (*inferencev2.InferenceResponse, error) {
		return &inferencev2.InferenceResponse{FrameId: "550e8400-e29b-41d4-a716-446655440006", ModelVersion: "m", ProcessedAt: timestamppb.Now(), ScalarMeasurements: requiredScalars()}, nil
	}}
	frame := validFrame("550e8400-e29b-41d4-a716-446655440006")
	if _, err := client.Infer(context.Background(), frame); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.Requests) != 1 {
		t.Fatalf("requests=%#v", client.Requests)
	}
}

func newTestClient(t *testing.T, mode mockmodel.Mode, delay time.Duration) *Client {
	t.Helper()
	service, err := mockmodel.New(mode, "mock-test", delay)
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	inferencev2.RegisterInferenceServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(conn)
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func validFrame(id string) ingest.Frame {
	return ingest.Frame{FrameID: id, CameraID: "CAM-1", ImageData: []byte{0xff, 0xd8, 0xff, 0xd9}, CapturedAt: time.Now().UTC(), SequenceNo: 1}
}

func requiredScalars() map[string]float32 {
	return map[string]float32{"lit_L": 140, "lit_R": 135, "peak": 0.9, "dx": 0, "dy": 0, "slot": -1, "status_code": 0}
}

type failOnceService struct {
	inferencev2.UnimplementedInferenceServiceServer
	mu      sync.Mutex
	streams int
}

func (s *failOnceService) Infer(stream inferencev2.InferenceService_InferServer) error {
	s.mu.Lock()
	s.streams++
	streamNo := s.streams
	s.mu.Unlock()
	for {
		request, err := stream.Recv()
		if err != nil {
			return err
		}
		if streamNo == 1 {
			return status.Error(codes.Unavailable, "synthetic restart")
		}
		if err := stream.Send(&inferencev2.InferenceResponse{FrameId: request.GetFrameId(), ModelVersion: "reconnected", ProcessedAt: timestamppb.Now(), ScalarMeasurements: requiredScalars()}); err != nil {
			return err
		}
	}
}

func (s *failOnceService) GetGodetState(context.Context, *inferencev2.GodetStateRequest) (*inferencev2.GodetStateResponse, error) {
	return &inferencev2.GodetStateResponse{ModelVersion: "reconnected", Health: &inferencev2.Health{Ready: true, LoopLocked: true, Status: "ok"}}, nil
}
