package mockmodel

import (
	"context"
	"net"
	"testing"
	"time"

	inferencev2 "github.com/clinkervision/clinker-vision/internal/inference/gen/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestParseMode(t *testing.T) {
	for _, mode := range []string{"empty", "crack", "misalignment", "multiple", "low-confidence", "timeout", "unavailable", "invalid-response", "invalid-bounding-box"} {
		if _, err := ParseMode(mode); err != nil {
			t.Errorf("mode %q rejected: %v", mode, err)
		}
	}
	if _, err := ParseMode("unknown"); err == nil {
		t.Fatal("unknown mode was accepted")
	}
}

func TestDeterministicResponseModes(t *testing.T) {
	for _, test := range []struct {
		mode           Mode
		detections     int
		invalidVersion bool
		invalidBox     bool
	}{
		{ModeEmpty, 0, false, false}, {ModeCrack, 1, false, false}, {ModeMisalignment, 1, false, false},
		{ModeMultiple, 2, false, false}, {ModeLowConfidence, 0, false, false}, {ModeInvalidResponse, 0, true, false}, {ModeInvalidBoundingBox, 1, false, true},
	} {
		t.Run(string(test.mode), func(t *testing.T) {
			response, err := sendRequest(t, test.mode, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(response.GetDetections()) != test.detections {
				t.Fatalf("detections=%d want=%d", len(response.GetDetections()), test.detections)
			}
			if test.invalidVersion && response.GetModelVersion() != "" {
				t.Fatalf("version=%q", response.GetModelVersion())
			}
			if test.invalidBox && response.GetDetections()[0].GetBoundingBox().GetX() <= 1 {
				t.Fatal("invalid box became valid")
			}
		})
	}
}

func TestUnavailableModeReturnsUnavailable(t *testing.T) {
	_, err := sendRequest(t, ModeUnavailable, context.Background())
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("got %v: %v", status.Code(err), err)
	}
}

func TestTimeoutModeHonorsClientDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := sendRequest(t, ModeTimeout, ctx)
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("got %v: %v", status.Code(err), err)
	}
}

func sendRequest(t *testing.T, mode Mode, ctx context.Context) (*inferencev2.InferenceResponse, error) {
	t.Helper()
	service, err := New(mode, "mock-test", time.Second)
	if err != nil {
		return nil, err
	}
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	inferencev2.RegisterInferenceServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stream, err := inferencev2.NewInferenceServiceClient(conn).Infer(ctx)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(&inferencev2.InferenceRequest{FrameId: "550e8400-e29b-41d4-a716-446655440000", CameraId: "CAM-1", ImageData: []byte{0xff, 0xd8, 0xff, 0xd9}, CapturedAt: timestamppb.Now(), SequenceNo: 1}); err != nil {
		return nil, err
	}
	return stream.Recv()
}
