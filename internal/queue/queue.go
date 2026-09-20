package queue

import (
	"context"
	"errors"
	"sync"

	"github.com/clinkervision/clinker-vision/internal/ingest"
)

var ErrClosed = errors.New("frame queue is closed")

type FrameQueue struct {
	mu       sync.Mutex
	capacity int
	items    []ingest.Frame
	changed  chan struct{}
	closed   bool
}

func NewFrameQueue(capacity int) (*FrameQueue, error) {
	if capacity <= 0 {
		return nil, errors.New("queue capacity must be positive")
	}
	return &FrameQueue{
		capacity: capacity,
		changed:  make(chan struct{}),
		items:    make([]ingest.Frame, 0, capacity),
	}, nil
}

// Push never waits for a consumer. If the queue is full, the oldest frame is
// removed before the new frame is appended.
func (q *FrameQueue) Push(frame ingest.Frame) (dropped bool, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false, ErrClosed
	}
	if len(q.items) == q.capacity {
		q.items[0] = ingest.Frame{}
		q.items = q.items[1:]
		dropped = true
	}
	q.items = append(q.items, frame)
	q.signalLocked()
	return dropped, nil
}

// PushWait appends a frame without dropping data. When the queue is full it
// waits for a consumer, allowing the upstream decoder to apply backpressure.
func (q *FrameQueue) PushWait(ctx context.Context, frame ingest.Frame) error {
	for {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			return ErrClosed
		}
		if len(q.items) < q.capacity {
			q.items = append(q.items, frame)
			q.signalLocked()
			q.mu.Unlock()
			return nil
		}
		changed := q.changed
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (q *FrameQueue) Pop(ctx context.Context) (ingest.Frame, error) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			frame := q.items[0]
			q.items[0] = ingest.Frame{}
			q.items = q.items[1:]
			q.signalLocked()
			q.mu.Unlock()
			return frame, nil
		}
		if q.closed {
			q.mu.Unlock()
			return ingest.Frame{}, ErrClosed
		}
		changed := q.changed
		q.mu.Unlock()

		select {
		case <-ctx.Done():
			return ingest.Frame{}, ctx.Err()
		case <-changed:
		}
	}
}

func (q *FrameQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	q.signalLocked()
}

func (q *FrameQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (q *FrameQueue) signalLocked() {
	close(q.changed)
	q.changed = make(chan struct{})
}
