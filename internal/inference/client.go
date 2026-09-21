package inference

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	inferencev2 "github.com/clinkervision/clinker-vision/internal/inference/gen/v2"
	"github.com/clinkervision/clinker-vision/internal/ingest"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrClientClosed      = errors.New("inference client is closed")
	ErrInvalidRequest    = errors.New("invalid inference request")
	ErrInvalidResponse   = errors.New("invalid inference response")
	ErrUnknownResponse   = errors.New("response did not match a pending frame")
	ErrStreamUnavailable = errors.New("inference stream is unavailable")
)

type responseResult struct {
	response *inferencev2.InferenceResponse
	err      error
}

type InferenceClient interface {
	Infer(context.Context, ingest.Frame) (*inferencev2.InferenceResponse, error)
	Close() error
}

type Client struct {
	stream  inferencev2.InferenceService_InferClient
	service inferencev2.InferenceServiceClient
	conn    *grpc.ClientConn
	address string

	sendMu    sync.Mutex
	streamMu  sync.RWMutex
	serviceMu sync.RWMutex
	pendingMu sync.Mutex
	pending   map[string]chan responseResult
	versionMu sync.RWMutex
	version   string
	closeOnce sync.Once
	closed    chan struct{}
	receiveWG sync.WaitGroup
}

func Dial(ctx context.Context, address string) (*Client, error) {
	if address == "" {
		return nil, errors.New("model address is required")
	}
	conn, err := grpc.DialContext(ctx, address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial model service: %w", err)
	}
	client, err := New(conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	client.conn = conn
	client.address = address
	return client, nil
}

func New(conn *grpc.ClientConn) (*Client, error) {
	if conn == nil {
		return nil, errors.New("gRPC connection is required")
	}
	service := inferencev2.NewInferenceServiceClient(conn)
	client := &Client{
		service: service,
		pending: make(map[string]chan responseResult),
		closed:  make(chan struct{}),
	}
	if err := client.openStream(); err != nil {
		return nil, err
	}
	return client, nil
}

func (c *Client) getService() inferencev2.InferenceServiceClient {
	c.serviceMu.RLock()
	defer c.serviceMu.RUnlock()
	return c.service
}

func (c *Client) openStream() error {
	stream, err := c.getService().Infer(context.Background())
	if err != nil {
		return fmt.Errorf("open inference stream: %w", err)
	}
	c.streamMu.Lock()
	c.stream = stream
	c.streamMu.Unlock()
	c.receiveWG.Add(1)
	go c.receive(stream)
	return nil
}

func (c *Client) Infer(ctx context.Context, frame ingest.Frame) (*inferencev2.InferenceResponse, error) {
	request, err := RequestFromFrame(frame)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := make(chan responseResult, 1)
	c.pendingMu.Lock()
	select {
	case <-c.closed:
		c.pendingMu.Unlock()
		return nil, ErrClientClosed
	default:
	}
	if _, exists := c.pending[request.GetFrameId()]; exists {
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("frame %s already has a pending request", request.GetFrameId())
	}
	c.pending[request.GetFrameId()] = result
	c.pendingMu.Unlock()

	c.sendMu.Lock()
	c.streamMu.RLock()
	stream := c.stream
	c.streamMu.RUnlock()
	if stream == nil {
		c.sendMu.Unlock()
		c.removePending(request.GetFrameId())
		return nil, ErrStreamUnavailable
	}
	err = stream.Send(request)
	c.sendMu.Unlock()
	if err != nil {
		c.removePending(request.GetFrameId())
		return nil, err
	}

	select {
	case result := <-result:
		return result.response, result.err
	case <-ctx.Done():
		c.removePending(request.GetFrameId())
		return nil, ctx.Err()
	case <-c.closed:
		c.removePending(request.GetFrameId())
		return nil, ErrClientClosed
	}
}

// IsRetryableTransportError distinguishes a broken gRPC transport from a
// per-frame deadline/dead-letter. The model consumes sequence positions for
// deadline-exceeded frames, so blindly replaying those frames would shift the
// positional detector and is unsafe.
func IsRetryableTransportError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, ErrStreamUnavailable) || errors.Is(err, io.EOF) {
		return true
	}
	code := status.Code(err)
	return code == codes.Unavailable || code == codes.ResourceExhausted || code == codes.Aborted
}

func (c *Client) receive(stream inferencev2.InferenceService_InferClient) {
	defer c.receiveWG.Done()
	for {
		response, err := stream.Recv()
		if err != nil {
			c.streamMu.RLock()
			active := c.stream == stream
			c.streamMu.RUnlock()
			if active {
				c.failPending(err)
			}
			return
		}
		c.handleResponse(response)
	}
}

func (c *Client) handleResponse(response *inferencev2.InferenceResponse) {
	frameID := response.GetFrameId()
	c.pendingMu.Lock()
	result, ok := c.pending[frameID]
	if ok {
		delete(c.pending, frameID)
	}
	c.pendingMu.Unlock()
	if !ok {
		return
	}

	validated, err := ValidateResponse(frameID, response)
	if err == nil {
		c.versionMu.Lock()
		c.version = validated.GetModelVersion()
		c.versionMu.Unlock()
	}
	result <- responseResult{response: validated, err: err}
}

func (c *Client) failPending(err error) {
	c.pendingMu.Lock()
	pending := c.pending
	c.pending = make(map[string]chan responseResult)
	c.pendingMu.Unlock()
	for _, result := range pending {
		result <- responseResult{err: err}
	}
}

func (c *Client) removePending(frameID string) {
	c.pendingMu.Lock()
	delete(c.pending, frameID)
	c.pendingMu.Unlock()
}

func (c *Client) ModelVersion() string {
	c.versionMu.RLock()
	defer c.versionMu.RUnlock()
	return c.version
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.sendMu.Lock()
		c.streamMu.RLock()
		stream := c.stream
		c.streamMu.RUnlock()
		if stream != nil {
			_ = stream.CloseSend()
		}
		c.sendMu.Unlock()
	})
	done := make(chan struct{})
	go func() { c.receiveWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Reconnect replaces the bidi stream after a transport failure. Existing
// pending requests are failed so their callers can retry them; the pipeline's
// queue remains the bounded in-process outbox and applies backpressure while
// this operation is retried.
//
// Docker recreates change the model IP behind the stable DNS name, so a new
// stream on a stale ClientConn can keep dialing the old IP. Reconnect first
// resets the gRPC backoff (forcing re-resolution on the next attempt) and,
// if the stream still cannot be opened and the client was built with Dial
// (address known), it redials a fresh ClientConn so DNS is resolved again.
func (c *Client) Reconnect(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	select {
	case <-c.closed:
		return ErrClientClosed
	default:
	}
	if c.conn != nil {
		c.conn.ResetConnectBackoff()
	}
	stream, err := c.getService().Infer(context.Background())
	if err == nil {
		c.streamMu.Lock()
		old := c.stream
		c.stream = stream
		c.streamMu.Unlock()
		c.failPending(errors.New("inference stream replaced"))
		if old != nil {
			_ = old.CloseSend()
		}
		c.receiveWG.Add(1)
		go c.receive(stream)
		return nil
	}
	if c.address == "" {
		return fmt.Errorf("reconnect inference stream: %w", err)
	}
	return c.redialLocked(ctx, fmt.Errorf("reconnect inference stream: %w", err))
}

// redialLocked dials a fresh ClientConn for the stored address and swaps it
// in. Caller must hold sendMu.
func (c *Client) redialLocked(ctx context.Context, cause error) error {
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	newConn, err := grpc.DialContext(dialCtx, c.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("redial model service %s: %w (caused by %v)", c.address, err, cause)
	}
	newService := inferencev2.NewInferenceServiceClient(newConn)
	stream, err := newService.Infer(context.Background())
	if err != nil {
		_ = newConn.Close()
		return fmt.Errorf("redial inference stream: %w (caused by %v)", err, cause)
	}
	c.serviceMu.Lock()
	oldService := c.service
	c.service = newService
	c.serviceMu.Unlock()
	_ = oldService
	c.streamMu.Lock()
	oldStream := c.stream
	c.stream = stream
	c.streamMu.Unlock()
	oldConn := c.conn
	c.conn = newConn
	c.failPending(errors.New("inference client redialed"))
	if oldStream != nil {
		_ = oldStream.CloseSend()
	}
	if oldConn != nil {
		_ = oldConn.Close()
	}
	c.receiveWG.Add(1)
	go c.receive(stream)
	return nil
}

func RequestFromFrame(frame ingest.Frame) (*inferencev2.InferenceRequest, error) {
	if _, err := uuid.Parse(frame.FrameID); err != nil {
		return nil, fmt.Errorf("%w: frame_id must be a UUID", ErrInvalidRequest)
	}
	if frame.CameraID == "" {
		return nil, fmt.Errorf("%w: camera_id is required", ErrInvalidRequest)
	}
	if len(frame.ImageData) == 0 {
		return nil, fmt.Errorf("%w: image_data is required", ErrInvalidRequest)
	}
	request := &inferencev2.InferenceRequest{
		FrameId:    frame.FrameID,
		CameraId:   frame.CameraID,
		ImageData:  append([]byte(nil), frame.ImageData...),
		CapturedAt: timestamppb.New(frame.CapturedAt),
		SequenceNo: frame.SequenceNo,
	}
	if err := request.GetCapturedAt().CheckValid(); err != nil {
		return nil, fmt.Errorf("%w: captured_at: %v", ErrInvalidRequest, err)
	}
	return request, nil
}

func ValidateResponse(expectedFrameID string, response *inferencev2.InferenceResponse) (*inferencev2.InferenceResponse, error) {
	if response == nil {
		return nil, fmt.Errorf("%w: response is nil", ErrInvalidResponse)
	}
	if response.GetFrameId() != expectedFrameID {
		return nil, fmt.Errorf("%w: frame_id mismatch", ErrInvalidResponse)
	}
	if response.GetModelVersion() == "" {
		return nil, fmt.Errorf("%w: model_version is required", ErrInvalidResponse)
	}
	if err := response.GetProcessedAt().CheckValid(); err != nil {
		return nil, fmt.Errorf("%w: processed_at: %v", ErrInvalidResponse, err)
	}
	for name, value := range response.GetScalarMeasurements() {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, fmt.Errorf("%w: scalar_measurements[%s] is not finite", ErrInvalidResponse, name)
		}
	}
	for _, name := range []string{"peak", "dx", "dy", "slot"} {
		if _, ok := response.GetScalarMeasurements()[name]; !ok {
			return nil, fmt.Errorf("%w: scalar_measurements[%s] is required", ErrInvalidResponse, name)
		}
	}
	if statusCode, ok := response.GetScalarMeasurements()["status_code"]; ok && (statusCode < 0 || statusCode > 3 || math.Trunc(float64(statusCode)) != float64(statusCode)) {
		return nil, fmt.Errorf("%w: scalar_measurements[status_code] must be an integer from 0 through 3", ErrInvalidResponse)
	}

	validated := proto.Clone(response).(*inferencev2.InferenceResponse)
	for index, detection := range validated.GetDetections() {
		if detection == nil {
			return nil, fmt.Errorf("%w: detection %d is nil", ErrInvalidResponse, index)
		}
		if detection.GetObservationTarget() != "GODET" {
			return nil, fmt.Errorf("%w: detection %d has unknown target", ErrInvalidResponse, index)
		}
		if detection.GetFaultType() != "DAMAGE" {
			return nil, fmt.Errorf("%w: detection %d has unknown fault", ErrInvalidResponse, index)
		}
		box := detection.GetBoundingBox()
		if box == nil {
			return nil, fmt.Errorf("%w: detection %d bounding_box is required", ErrInvalidResponse, index)
		}
		if !finite(box.GetX()) || !finite(box.GetY()) || !finite(box.GetWidth()) || !finite(box.GetHeight()) {
			return nil, fmt.Errorf("%w: detection %d bounding_box is not finite", ErrInvalidResponse, index)
		}
		box.X = clamp(box.GetX(), 0, 1)
		box.Y = clamp(box.GetY(), 0, 1)
		box.Width = clamp(box.GetWidth(), 0, 1-box.GetX())
		box.Height = clamp(box.GetHeight(), 0, 1-box.GetY())
		if box.GetWidth() <= 0 || box.GetHeight() <= 0 {
			return nil, fmt.Errorf("%w: detection %d bounding_box has no area", ErrInvalidResponse, index)
		}
	}
	return validated, nil
}

func finite(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}

func clamp(value, min, max float32) float32 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// GetGodetState retrieves the Tier-2 model state. The pipeline must use this
// state as its alert source; Tier-1 Infer responses are measurements only.
func (c *Client) GetGodetState(ctx context.Context, request *inferencev2.GodetStateRequest) (*inferencev2.GodetStateResponse, error) {
	if request == nil {
		request = &inferencev2.GodetStateRequest{IncludeHistory: true}
	}
	select {
	case <-c.closed:
		return nil, ErrClientClosed
	default:
	}
	response, err := c.getService().GetGodetState(ctx, request)
	if err != nil {
		return nil, err
	}
	if err := ValidateGodetStateResponse(response); err != nil {
		return nil, err
	}
	c.versionMu.Lock()
	c.version = response.GetModelVersion()
	c.versionMu.Unlock()
	return response, nil
}

func ValidateGodetStateResponse(response *inferencev2.GodetStateResponse) error {
	if response == nil {
		return fmt.Errorf("%w: godet state response is nil", ErrInvalidResponse)
	}
	if response.GetModelVersion() == "" {
		return fmt.Errorf("%w: godet state model_version is required", ErrInvalidResponse)
	}
	if response.GetHealth() == nil {
		return fmt.Errorf("%w: godet state health is required", ErrInvalidResponse)
	}
	for name, value := range response.GetHealth().GetCounters() {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("%w: health.counters[%s] is not finite", ErrInvalidResponse, name)
		}
	}
	for index, godet := range response.GetGodets() {
		if godet == nil || godet.GetGodetId() <= 0 {
			return fmt.Errorf("%w: godet state %d has invalid godet_id", ErrInvalidResponse, index)
		}
		if !finite(godet.GetLip()) {
			return fmt.Errorf("%w: godet state %d lip is not finite", ErrInvalidResponse, index)
		}
	}
	return nil
}

type MockClient struct {
	mu       sync.Mutex
	Respond  func(context.Context, ingest.Frame) (*inferencev2.InferenceResponse, error)
	Requests []ingest.Frame
}

func (m *MockClient) Infer(ctx context.Context, frame ingest.Frame) (*inferencev2.InferenceResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.Requests = append(m.Requests, frame)
	respond := m.Respond
	m.mu.Unlock()
	if respond == nil {
		return nil, errors.New("mock inference responder is not configured")
	}
	return respond(ctx, frame)
}

func (m *MockClient) Close() error { return nil }

var _ InferenceClient = (*Client)(nil)
var _ InferenceClient = (*MockClient)(nil)
