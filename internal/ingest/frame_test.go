package ingest

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDecodeMJPEGEmitsCompleteFrames(t *testing.T) {
	first := fakeJPEG("first")
	second := fakeJPEG("second")
	var got [][]byte
	if err := decodeMJPEG(bytes.NewReader(append(first, second...)), func(imageData []byte) error {
		got = append(got, imageData)
		return nil
	}); err != nil {
		t.Fatalf("decodeMJPEG returned error: %v", err)
	}
	if len(got) != 2 || !bytes.Equal(got[0], first) || !bytes.Equal(got[1], second) {
		t.Fatalf("unexpected frames: %q", got)
	}
}

func TestDecodeMJPEGRejectsTruncatedFrame(t *testing.T) {
	err := decodeMJPEG(bytes.NewReader([]byte{0xff, 0xd8, 0x01, 0x02}), func([]byte) error { return nil })
	if !errors.Is(err, ErrTruncatedJPEG) {
		t.Fatalf("expected ErrTruncatedJPEG, got %v", err)
	}
}

func TestDecoderAssignsTraceableMetadata(t *testing.T) {
	input := append(fakeJPEG("one"), fakeJPEG("two")...)
	clock := time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC)
	current := clock
	var frames []Frame
	decoder := Decoder{
		CameraID: "CAM-1",
		Source:   "fixture.mp4",
		Command: func(context.Context, string, ...string) *exec.Cmd {
			return helperCommand(input)
		},
		Clock: func() time.Time {
			value := current
			current = current.Add(2 * time.Second)
			return value
		},
	}
	if err := decoder.Run(context.Background(), func(frame Frame) error {
		frames = append(frames, frame)
		return nil
	}); err != nil {
		t.Fatalf("decoder returned error: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(frames))
	}
	if frames[0].FrameID == "" || frames[0].FrameID == frames[1].FrameID {
		t.Fatalf("frame IDs are not unique: %q, %q", frames[0].FrameID, frames[1].FrameID)
	}
	if frames[0].CameraID != "CAM-1" || frames[1].CameraID != "CAM-1" {
		t.Fatalf("unexpected camera IDs: %q, %q", frames[0].CameraID, frames[1].CameraID)
	}
	if frames[0].SequenceNo != 1 || frames[1].SequenceNo != 2 {
		t.Fatalf("unexpected sequence numbers: %d, %d", frames[0].SequenceNo, frames[1].SequenceNo)
	}
	if !frames[0].CapturedAt.Before(frames[1].CapturedAt) {
		t.Fatalf("timestamps are not increasing: %s, %s", frames[0].CapturedAt, frames[1].CapturedAt)
	}
}

func TestDecoderReturnsDecodeFailure(t *testing.T) {
	decoder := Decoder{
		CameraID: "CAM-1",
		Source:   "fixture.mp4",
		Command: func(context.Context, string, ...string) *exec.Cmd {
			return helperCommand([]byte{0xff, 0xd8, 0x01})
		},
	}
	err := decoder.Run(context.Background(), func(Frame) error { return nil })
	if !errors.Is(err, ErrTruncatedJPEG) {
		t.Fatalf("expected decode failure, got %v", err)
	}
}

func TestDecoderStopsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	decoder := Decoder{
		CameraID: "CAM-1",
		Source:   "fixture.mp4",
		Command: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sleep", "30")
		},
	}
	done := make(chan error, 1)
	go func() { done <- decoder.Run(ctx, func(Frame) error { return nil }) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("decoder did not stop after context cancellation")
	}
}

func TestDecoderReadsCAM1Fixture(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	fixture := filepath.Join("..", "..", "testdata", "video", "cam-1.mp4")
	decoder := Decoder{
		CameraID:     "CAM-1",
		Source:       fixture,
		FFmpegBinary: ffmpeg,
		MaxFrames:    3,
	}
	var frames []Frame
	if err := decoder.Run(context.Background(), func(frame Frame) error {
		frames = append(frames, frame)
		return nil
	}); err != nil {
		t.Fatalf("fixture decode failed: %v", err)
	}
	if len(frames) != 3 {
		t.Fatalf("expected 3 fixture frames, got %d", len(frames))
	}
	for index, frame := range frames {
		if frame.SequenceNo != uint64(index+1) || frame.CameraID != "CAM-1" {
			t.Fatalf("unexpected metadata for frame %d: %#v", index, frame)
		}
		if !bytes.HasPrefix(frame.ImageData, []byte{0xff, 0xd8}) || !bytes.HasSuffix(frame.ImageData, []byte{0xff, 0xd9}) {
			t.Fatalf("frame %d is not a complete JPEG", index)
		}
	}
}

func fakeJPEG(payload string) []byte {
	return append([]byte{0xff, 0xd8}, append([]byte(payload), 0xff, 0xd9)...)
}

func helperCommand(output []byte) *exec.Cmd {
	return exec.Command("printf", "%s", string(output))
}
