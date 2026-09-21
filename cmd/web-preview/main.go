// Command web-preview serves the Clinker Vision viewer with generated mock
// data for UI review. No camera, model, or Docker needed:
//
//	go run ./cmd/web-preview
//
// Then open http://127.0.0.1:8080 in a browser. Data lives in a temp dir
// and is discarded on exit. Localhost only, like production.
package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/clinkervision/clinker-vision/internal/evidence"
	"github.com/clinkervision/clinker-vision/internal/health"
	"github.com/clinkervision/clinker-vision/internal/metrics"
	"github.com/clinkervision/clinker-vision/internal/store"
	"github.com/clinkervision/clinker-vision/internal/web"
	"github.com/google/uuid"
)

type seed struct {
	godet int32
	loop  int32
	state string
	seen  bool
	mins  int
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	root, err := os.MkdirTemp("", "clinker-preview-*")
	if err != nil {
		logger.Error("temp dir failed", "reason", err)
		os.Exit(1)
	}
	defer os.RemoveAll(root)
	evidenceRoot := filepath.Join(root, "evidence")

	ctx := context.Background()
	alertStore, err := store.Open(ctx, filepath.Join(root, "alerts.db"), evidenceRoot)
	if err != nil {
		logger.Error("store failed", "reason", err)
		os.Exit(1)
	}
	defer alertStore.Close()

	now := time.Now().UTC()
	seeds := []seed{
		{godet: 812, loop: 41, state: "confirmed", seen: false, mins: 6},
		{godet: 812, loop: 40, state: "confirmed", seen: true, mins: 38},
		{godet: 207, loop: 41, state: "pending", seen: false, mins: 11},
		{godet: 1150, loop: 41, state: "pending", seen: false, mins: 17},
		{godet: 44, loop: 40, state: "confirmed", seen: false, mins: 52},
		{godet: 963, loop: 40, state: "pending", seen: true, mins: 71},
		{godet: 531, loop: 39, state: "confirmed", seen: true, mins: 96},
		{godet: 388, loop: 39, state: "pending", seen: false, mins: 121},
	}
	for _, s := range seeds {
		alertID := uuid.NewString()
		detected := now.Add(-time.Duration(s.mins) * time.Minute)
		ref := fmt.Sprintf("2026/09/21/CAM-1/%s.jpg", alertID)
		if err := evidence.WriteAtomic(evidenceRoot, ref, mockJPEG(s.godet, s.state)); err != nil {
			logger.Error("evidence seed failed", "reason", err)
			os.Exit(1)
		}
		var seenAt *time.Time
		if s.seen {
			at := detected.Add(9 * time.Minute)
			seenAt = &at
		}
		alert := store.Alert{
			AlertID: alertID, CapturedAt: detected, DetectedAt: detected, CreatedAt: detected,
			CameraID: "CAM-1", ObservationTarget: "godet", FaultType: "DAMAGE",
			FrameID:          uuid.NewString(),
			ModelVersion:     "498184ea+preview+thr-v1",
			BoundingBox:      store.BoundingBox{X: 0.3806, Y: 0.1158, Width: 0.0536, Height: 0.1697},
			EvidenceRef:      ref,
			SeenAt:           seenAt,
			GodetID:          s.godet,
			AlertState:       s.state,
			LoopNo:           s.loop,
			RuleID:           "short-lip",
			EventKey:         fmt.Sprintf("DAMAGE:%d:%d", s.godet, s.loop),
			EvidenceFrameID:  uuid.NewString(),
			MeasurementsJSON: fmt.Sprintf(`{"lip_before":150.0,"lip_now":%.1f,"drop":%.1f}`, 150.0-float64(s.godet%37), float64(s.godet%37)),
		}
		if err := alertStore.InsertAlert(ctx, alert); err != nil {
			logger.Error("seed failed", "reason", err)
			os.Exit(1)
		}
	}

	healthHandler := health.New()
	healthHandler.SetReady(true)
	status := web.NewStatusStore()
	polled := now
	status.Update(func(s *web.ModelStatus) {
		s.Ready = true
		s.LoopLocked = true
		s.Status = "ok"
		s.Detail = "all clear"
		s.ModelVersion = "498184ea+preview+thr-v1"
		s.LastPollAt = &polled
		s.Counters = map[string]float64{
			"frames_total": 184023, "captures_total": 9120, "godet_rows": 8455,
			"sequence_gaps_total": 1, "dead_letters_total": 3,
		}
	})

	server := &http.Server{
		Addr:              "127.0.0.1:8080",
		Handler:           web.NewHandler(healthHandler, metrics.New(), web.NewAPI(alertStore, evidenceRoot), status),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	logger.Info("preview ready", "url", "http://127.0.0.1:8080", "alerts", len(seeds))
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server failed", "reason", err)
		os.Exit(1)
	}
}

// mockJPEG renders a dark synthetic frame with the fixed ROI box drawn in,
// so evidence thumbnails look like the real overlay style.
func mockJPEG(godet int32, state string) []byte {
	const w, h = 640, 360
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	base := uint8(86 + godet%26)
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: base + 6, G: base + 8, B: base + 10, A: 255}}, image.Point{}, draw.Src)
	// Two horizontal "chain bars" for texture.
	for y := 90; y < 270; y += 36 {
		for x := 0; x < w; x++ {
			v := uint8(104 + ((x*7 + y*13 + int(godet)) % 52))
			img.Set(x, y, color.RGBA{R: v, G: v + 4, B: v + 8, A: 255})
		}
	}
	// Fixed ROI box (same normalized rect as production overlays).
	fw, fh := float64(w), float64(h)
	x0, y0 := int(0.3806*fw), int(0.1158*fh)
	x1, y1 := x0+int(0.0536*fw), y0+int(0.1697*fh)
	box := color.RGBA{R: 240, G: 162, B: 46, A: 255}
	if state == "confirmed" {
		box = color.RGBA{R: 240, G: 82, B: 77, A: 255}
	}
	for x := x0; x <= x1; x++ {
		img.Set(x, y0, box)
		img.Set(x, y1, box)
	}
	for y := y0; y <= y1; y++ {
		img.Set(x0, y, box)
		img.Set(x1, y, box)
	}
	var buf []byte
	w2 := &sliceWriter{buf: &buf}
	_ = jpeg.Encode(w2, img, &jpeg.Options{Quality: 85})
	return buf
}

type sliceWriter struct {
	buf *[]byte
}

func (s *sliceWriter) Write(p []byte) (int, error) {
	*s.buf = append(*s.buf, p...)
	return len(p), nil
}
