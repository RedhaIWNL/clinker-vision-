package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/clinkervision/clinker-vision/internal/ingest"
)

func TestFrameQueueDropsOldestAndKeepsNewest(t *testing.T) {
	queue, err := NewFrameQueue(2)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"frame-1", "frame-2"} {
		if _, err := queue.Push(ingest.Frame{FrameID: id}); err != nil {
			t.Fatal(err)
		}
	}
	dropped, err := queue.Push(ingest.Frame{FrameID: "frame-3"})
	if err != nil || !dropped {
		t.Fatalf("expected oldest frame to be dropped, dropped=%t err=%v", dropped, err)
	}

	first, err := queue.Pop(context.Background())
	if err != nil || first.FrameID != "frame-2" {
		t.Fatalf("expected frame-2 first, got %#v err=%v", first, err)
	}
	second, err := queue.Pop(context.Background())
	if err != nil || second.FrameID != "frame-3" {
		t.Fatalf("expected newest frame-3 second, got %#v err=%v", second, err)
	}
}

func TestFrameQueuePushDoesNotWaitForConsumer(t *testing.T) {
	queue, err := NewFrameQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := queue.Push(ingest.Frame{FrameID: "frame-1"})
		finished <- err
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("push blocked without a consumer")
	}
}

func TestFrameQueuePopCanBeCanceled(t *testing.T) {
	queue, err := NewFrameQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := queue.Pop(ctx)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pop did not stop after context cancellation")
	}
}

func TestFrameQueueCloseWakesConsumer(t *testing.T) {
	queue, err := NewFrameQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := queue.Pop(context.Background())
		done <- err
	}()
	queue.Close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("expected ErrClosed, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pop did not stop after queue close")
	}
	if _, err := queue.Push(ingest.Frame{FrameID: "late"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected closed queue push error, got %v", err)
	}
}
