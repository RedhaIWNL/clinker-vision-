package evidence

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	inferencev1 "github.com/clinkervision/clinker-vision/internal/inference/gen"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"google.golang.org/protobuf/proto"
)

var (
	ErrInvalidEvidenceRef = errors.New("invalid evidence reference")
	ErrInvalidDetection   = errors.New("invalid detection for evidence")
)

var safePathPart = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type Overlay struct {
	FaultType   string
	Confidence  float32
	BoundingBox *inferencev1.BoundingBox
}

type BoxOverlay struct {
	FaultType   string
	Confidence  *float32
	BoundingBox BoundingBox
}

type BoundingBox struct {
	X, Y, Width, Height float32
}

// RenderJPEGWithBoxes renders evidence for model contracts that do not expose
// a confidence score. A nil Confidence is intentionally omitted from the label.
func RenderJPEGWithBoxes(source []byte, overlays []BoxOverlay, quality int) ([]byte, error) {
	if quality < 1 || quality > 100 {
		return nil, fmt.Errorf("JPEG quality must be between 1 and 100")
	}
	decoded, err := jpeg.Decode(bytes.NewReader(source))
	if err != nil {
		return nil, fmt.Errorf("decode source JPEG: %w", err)
	}
	frame := image.NewRGBA(decoded.Bounds())
	draw.Draw(frame, frame.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	for index, overlay := range overlays {
		if err := drawBoxOverlay(frame, overlay, index); err != nil {
			return nil, err
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, frame, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("encode evidence JPEG: %w", err)
	}
	return output.Bytes(), nil
}

func RenderJPEG(source []byte, detections []*inferencev1.Detection, quality int) ([]byte, error) {
	if quality < 1 || quality > 100 {
		return nil, fmt.Errorf("JPEG quality must be between 1 and 100")
	}
	decoded, err := jpeg.Decode(bytes.NewReader(source))
	if err != nil {
		return nil, fmt.Errorf("decode source JPEG: %w", err)
	}
	frame := image.NewRGBA(decoded.Bounds())
	draw.Draw(frame, frame.Bounds(), decoded, decoded.Bounds().Min, draw.Src)

	for index, detection := range detections {
		if err := drawDetection(frame, detection, index); err != nil {
			return nil, err
		}
	}

	var output bytes.Buffer
	if err := jpeg.Encode(&output, frame, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("encode evidence JPEG: %w", err)
	}
	return output.Bytes(), nil
}

func WriteAtomic(root, evidenceRef string, jpegData []byte) error {
	path, err := SafePath(root, evidenceRef)
	if err != nil {
		return err
	}
	if len(jpegData) == 0 {
		return errors.New("evidence JPEG is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create evidence directory: %w", err)
	}
	if err := ensureResolvedWithinRoot(root, path); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".evidence-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary evidence file: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("protect temporary evidence file: %w", err)
	}
	if _, err := temporary.Write(jpegData); err != nil {
		return fmt.Errorf("write temporary evidence file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary evidence file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary evidence file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("atomically publish evidence file: %w", err)
	}
	removeTemporary = false
	return nil
}

func Remove(root, evidenceRef string) error {
	path, err := SafePath(root, evidenceRef)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat evidence: %w", err)
	}
	if err := ensureResolvedWithinRoot(root, path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove evidence: %w", err)
	}
	return nil
}

func SafePath(root, evidenceRef string) (string, error) {
	if root == "" || evidenceRef == "" || filepath.IsAbs(evidenceRef) {
		return "", ErrInvalidEvidenceRef
	}
	clean := filepath.Clean(evidenceRef)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrInvalidEvidenceRef
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve evidence root: %w", err)
	}
	path := filepath.Join(rootAbs, clean)
	relative, err := filepath.Rel(rootAbs, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ErrInvalidEvidenceRef
	}
	if err := ensureResolvedWithinRoot(rootAbs, path); err != nil {
		return "", err
	}
	return path, nil
}

func ensureResolvedWithinRoot(root, path string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve evidence root: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("resolve evidence root symlinks: %w", err)
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("resolve evidence directory symlinks: %w", err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedParent)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ErrInvalidEvidenceRef
	}
	return nil
}

func Reference(capturedAt time.Time, cameraID, alertID string) (string, error) {
	if !safePathPart.MatchString(cameraID) || !safePathPart.MatchString(alertID) {
		return "", ErrInvalidEvidenceRef
	}
	date := capturedAt
	return fmt.Sprintf("%04d/%02d/%02d/%s/%s.jpg", date.Year(), date.Month(), date.Day(), cameraID, alertID), nil
}

func drawDetection(frame *image.RGBA, detection *inferencev1.Detection, index int) error {
	if detection == nil || detection.GetBoundingBox() == nil {
		return fmt.Errorf("%w: detection %d has no bounding box", ErrInvalidDetection, index)
	}
	box := proto.Clone(detection.GetBoundingBox()).(*inferencev1.BoundingBox)
	box.X = clamp(box.GetX(), 0, 1)
	box.Y = clamp(box.GetY(), 0, 1)
	box.Width = clamp(box.GetWidth(), 0, 1-box.GetX())
	box.Height = clamp(box.GetHeight(), 0, 1-box.GetY())
	if box.GetWidth() <= 0 || box.GetHeight() <= 0 {
		return fmt.Errorf("%w: detection %d has no visible area", ErrInvalidDetection, index)
	}
	bounds := frame.Bounds()
	x0 := bounds.Min.X + int(float32(bounds.Dx())*box.GetX())
	y0 := bounds.Min.Y + int(float32(bounds.Dy())*box.GetY())
	x1 := bounds.Min.X + int(float32(bounds.Dx())*(box.GetX()+box.GetWidth()))
	y1 := bounds.Min.Y + int(float32(bounds.Dy())*(box.GetY()+box.GetHeight()))
	red := image.NewUniform(color.RGBA{R: 220, G: 20, B: 20, A: 255})
	for offset := 0; offset < 3; offset++ {
		draw.Draw(frame, image.Rect(x0-offset, y0-offset, x1+offset+1, y0-offset+1), red, image.Point{}, draw.Src)
		draw.Draw(frame, image.Rect(x0-offset, y1+offset, x1+offset+1, y1+offset+1), red, image.Point{}, draw.Src)
		draw.Draw(frame, image.Rect(x0-offset, y0-offset, x0-offset+1, y1+offset+1), red, image.Point{}, draw.Src)
		draw.Draw(frame, image.Rect(x1+offset, y0-offset, x1+offset+1, y1+offset+1), red, image.Point{}, draw.Src)
	}

	label := fmt.Sprintf("%s %.2f", detection.GetFaultType().String(), detection.GetConfidence())
	labelWidth := len(label) * 7
	labelHeight := 13
	labelX := x0
	labelY := y0 - labelHeight
	if labelY < bounds.Min.Y {
		labelY = y0
	}
	background := image.NewUniform(color.RGBA{R: 0, G: 0, B: 0, A: 220})
	draw.Draw(frame, image.Rect(labelX, labelY, labelX+labelWidth+4, labelY+labelHeight), background, image.Point{}, draw.Over)
	drawer := font.Drawer{
		Dst:  frame,
		Src:  image.NewUniform(color.White),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(labelX+2, labelY+11),
	}
	drawer.DrawString(label)
	return nil
}

func drawBoxOverlay(frame *image.RGBA, overlay BoxOverlay, index int) error {
	box := overlay.BoundingBox
	box.X = clamp(box.X, 0, 1)
	box.Y = clamp(box.Y, 0, 1)
	box.Width = clamp(box.Width, 0, 1-box.X)
	box.Height = clamp(box.Height, 0, 1-box.Y)
	if box.Width <= 0 || box.Height <= 0 {
		return fmt.Errorf("%w: overlay %d has no visible area", ErrInvalidDetection, index)
	}
	bounds := frame.Bounds()
	x0 := bounds.Min.X + int(float32(bounds.Dx())*box.X)
	y0 := bounds.Min.Y + int(float32(bounds.Dy())*box.Y)
	x1 := bounds.Min.X + int(float32(bounds.Dx())*(box.X+box.Width))
	y1 := bounds.Min.Y + int(float32(bounds.Dy())*(box.Y+box.Height))
	red := image.NewUniform(color.RGBA{R: 220, G: 20, B: 20, A: 255})
	for offset := 0; offset < 3; offset++ {
		draw.Draw(frame, image.Rect(x0-offset, y0-offset, x1+offset+1, y0-offset+1), red, image.Point{}, draw.Src)
		draw.Draw(frame, image.Rect(x0-offset, y1+offset, x1+offset+1, y1+offset+1), red, image.Point{}, draw.Src)
		draw.Draw(frame, image.Rect(x0-offset, y0-offset, x0-offset+1, y1+offset+1), red, image.Point{}, draw.Src)
		draw.Draw(frame, image.Rect(x1+offset, y0-offset, x1+offset+1, y1+offset+1), red, image.Point{}, draw.Src)
	}
	label := strings.ToUpper(overlay.FaultType)
	if overlay.Confidence != nil {
		label = fmt.Sprintf("%s %.2f", label, *overlay.Confidence)
	}
	labelWidth := len(label) * 7
	labelHeight := 13
	labelX, labelY := x0, y0-labelHeight
	if labelY < bounds.Min.Y {
		labelY = y0
	}
	draw.Draw(frame, image.Rect(labelX, labelY, labelX+labelWidth+4, labelY+labelHeight), image.NewUniform(color.RGBA{A: 220}), image.Point{}, draw.Over)
	drawer := font.Drawer{Dst: frame, Src: image.NewUniform(color.White), Face: basicfont.Face7x13, Dot: fixed.P(labelX+2, labelY+11)}
	drawer.DrawString(label)
	return nil
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
