package evidence

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	inferencev1 "github.com/clinkervision/clinker-vision/internal/inference/gen"
)

func TestRenderJPEGOverlaysAllDetections(t *testing.T) {
	input := testJPEG(t, 100, 80)
	detections := []*inferencev1.Detection{
		{FaultType: inferencev1.FaultType_CRACK, Confidence: 0.95, BoundingBox: &inferencev1.BoundingBox{X: 0.1, Y: 0.1, Width: 0.3, Height: 0.3}},
		{FaultType: inferencev1.FaultType_MISALIGNMENT, Confidence: 0.91, BoundingBox: &inferencev1.BoundingBox{X: 0.6, Y: 0.5, Width: 0.2, Height: 0.2}},
	}
	output, err := RenderJPEG(input, detections, 85)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(output))
	if err != nil {
		t.Fatalf("output is not JPEG: %v", err)
	}
	if decoded.Bounds().Dx() != 100 || decoded.Bounds().Dy() != 80 {
		t.Fatalf("overlay changed frame dimensions: %v", decoded.Bounds())
	}
	if r, g, b, _ := decoded.At(10, 8).RGBA(); r <= g || r <= b {
		t.Fatalf("expected red overlay near first box, got %#v", color.RGBA64{R: uint16(r), G: uint16(g), B: uint16(b)})
	}
}

func TestWriteAtomicAndSafePath(t *testing.T) {
	root := t.TempDir()
	ref, err := Reference(time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC), "CAM-1", "550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(root, ref, []byte("jpeg-data")); err != nil {
		t.Fatal(err)
	}
	path, err := SafePath(root, ref)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "jpeg-data" {
		t.Fatalf("unexpected evidence contents: %q", contents)
	}
	for _, invalid := range []string{"../outside.jpg", "../../etc/passwd", "/tmp/outside.jpg", ".", ""} {
		if _, err := SafePath(root, invalid); err == nil {
			t.Errorf("path %q was accepted", invalid)
		}
	}
}

func TestWriteAtomicRejectsSymlinkedEvidenceDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(root, "escape/outside.jpg", []byte("not allowed")); err == nil {
		t.Fatal("symlinked evidence directory was accepted")
	}
}

func TestReferenceRejectsPathComponents(t *testing.T) {
	for _, cameraID := range []string{"../CAM-1", "CAM/1", ""} {
		if _, err := Reference(time.Now(), cameraID, "550e8400-e29b-41d4-a716-446655440000"); err == nil {
			t.Errorf("camera ID %q was accepted", cameraID)
		}
	}
	if _, err := Reference(time.Now(), "CAM-1", strings.Repeat("a", 20)); err != nil {
		t.Fatalf("safe alert ID rejected: %v", err)
	}
}

func testJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			frame.SetRGBA(x, y, color.RGBA{R: 80, G: 80, B: 80, A: 255})
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, frame, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
