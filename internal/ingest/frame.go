package ingest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var (
	ErrNoFrames      = errors.New("ffmpeg produced no frames")
	ErrTruncatedJPEG = errors.New("truncated JPEG frame")
)

type Frame struct {
	FrameID    string
	CameraID   string
	ImageData  []byte
	CapturedAt time.Time
	SequenceNo uint64
}

// Decoder invokes FFmpeg with the configured source and emits each decoded
// JPEG. The source is deliberately opaque to the decoder, so a local fixture
// and an RTSP URL use the same subprocess path.
type Decoder struct {
	CameraID     string
	Source       string
	FFmpegBinary string
	MaxFrames    int
	Clock        func() time.Time
	Command      func(ctx context.Context, name string, args ...string) *exec.Cmd
}

func (d Decoder) Run(ctx context.Context, emit func(Frame) error) error {
	if d.CameraID == "" {
		return errors.New("camera id is required")
	}
	if d.Source == "" {
		return errors.New("video source is required")
	}
	if emit == nil {
		return errors.New("frame handler is required")
	}

	binary := d.FFmpegBinary
	if binary == "" {
		binary = "ffmpeg"
	}
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	command := d.Command
	if command == nil {
		command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, name, args...)
		}
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
		"-i", d.Source,
		"-map", "0:v:0",
		"-an",
		"-c:v", "mjpeg",
		"-q:v", "3",
	}
	if d.MaxFrames > 0 {
		args = append(args, "-frames:v", strconv.Itoa(d.MaxFrames))
	}
	args = append(args, "-f", "image2pipe", "-")

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := command(runCtx, binary, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create ffmpeg stdout pipe: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	var sequence uint64
	decodeErr := decodeMJPEG(stdout, func(imageData []byte) error {
		sequence++
		frameID, err := newFrameID()
		if err != nil {
			cancel()
			return fmt.Errorf("generate frame id: %w", err)
		}
		if err := emit(Frame{
			FrameID:    frameID,
			CameraID:   d.CameraID,
			ImageData:  imageData,
			CapturedAt: clock().UTC(),
			SequenceNo: sequence,
		}); err != nil {
			cancel()
			return err
		}
		return nil
	})
	waitErr := cmd.Wait()

	if ctx.Err() != nil {
		return ctx.Err()
	}
	if decodeErr != nil {
		return decodeErr
	}
	if waitErr != nil {
		return newFFmpegError(waitErr, stderr.String(), d.Source)
	}
	if sequence == 0 {
		return ErrNoFrames
	}
	return nil
}

func newFrameID() (string, error) {
	var bytes [16]byte
	if _, err := io.ReadFull(rand.Reader, bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(bytes[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func newFFmpegError(exitErr error, stderr, source string) error {
	message := strings.TrimSpace(stderr)
	if source != "" {
		message = strings.ReplaceAll(message, source, "<video-source>")
	}
	if message == "" {
		return fmt.Errorf("ffmpeg exited: %w", exitErr)
	}
	if len(message) > 512 {
		message = message[len(message)-512:]
	}
	return fmt.Errorf("ffmpeg exited: %w: %s", exitErr, message)
}
