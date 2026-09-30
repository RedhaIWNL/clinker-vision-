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
	type demo struct {
		cam, target, fault string
		godet, loop        int32
		seen               bool
		mins               int
		box                store.BoundingBox
		meas               string
	}
	plate := store.BoundingBox{X: 0.49, Y: 0.64, Width: 0.1, Height: 0.2}
	strip := store.BoundingBox{X: 0.3, Y: 0, Width: 0.4, Height: 1}
	demos := []demo{
		{"CAM-1", "godet", "DAMAGE", 44, 12, false, 4, plate, `{"damage_type":3,"cut":0.93,"out_of_line":0.88,"severity":0.97,"passes_seen":4}`},
		{"CAM-1", "godet", "DAMAGE", 86, 12, false, 9, plate, `{"damage_type":1,"cut":0.96,"out_of_line":0.12,"severity":0.91,"passes_seen":3}`},
		{"CAM-1", "godet", "DAMAGE", 87, 11, true, 60, plate, `{"damage_type":2,"cut":0.20,"out_of_line":0.94,"severity":0.88,"passes_seen":5}`},
		{"CAM-1", "galet", "WHEEL_GAP", 216, 12, false, 14, strip, `{"first_godet":216,"last_godet":221,"godets":6,"wheels":0}`},
		{"CAM-1", "galet", "WHEEL_DENSITY", 299, 12, false, 15, strip, `{"first_godet":299,"last_godet":302,"godets":4,"wheels":3,"min_spacing":1}`},
		{"CAM-1", "galet", "WHEEL_MISSING", 1055, 12, false, 16, strip, `{"first_godet":1055,"last_godet":1055,"godets":1,"wheels":0}`},
		{"CAM-1", "galet", "WHEEL_GAP", 564, 11, false, 40, strip, `{"first_godet":564,"last_godet":567,"godets":4,"wheels":0}`},
		{"CAM-3", "galet", "WHEEL_DENSITY", 195, 8, false, 30, strip, `{"first_godet":195,"last_godet":199,"godets":5,"wheels":3,"min_spacing":2}`},
		{"CAM-4", "godet", "DAMAGE", 227, 3, false, 25, store.BoundingBox{X: 0.55, Y: 0.35, Width: 0.12, Height: 0.2}, `{"severity":2.4,"passes_seen":3}`},
	}
	for _, d := range demos {
		alertID := uuid.NewString()
		detected := now.Add(-time.Duration(d.mins) * time.Minute)
		ref := fmt.Sprintf("2026/09/30/%s/%s.jpg", d.cam, alertID)
		if err := evidence.WriteAtomic(evidenceRoot, ref, mockJPEG(d.godet, "confirmed")); err != nil {
			logger.Error("evidence seed failed", "reason", err)
			os.Exit(1)
		}
		var seenAt *time.Time
		if d.seen {
			at := detected.Add(9 * time.Minute)
			seenAt = &at
		}
		alert := store.Alert{
			AlertID: alertID, CapturedAt: detected, DetectedAt: detected, CreatedAt: detected,
			CameraID: d.cam, ObservationTarget: d.target, FaultType: d.fault,
			FrameID: uuid.NewString(), ModelVersion: "2319148e+preview+c1n-20260929",
			BoundingBox: d.box, EvidenceRef: ref, SeenAt: seenAt, GodetID: d.godet, AlertState: "confirmed",
			LoopNo: d.loop, RuleID: d.fault, EventKey: fmt.Sprintf("%s:%s:%d:%d", d.cam, d.fault, d.godet, d.loop),
			EvidenceFrameID: uuid.NewString(), MeasurementsJSON: d.meas,
		}
		if err := alertStore.InsertAlert(ctx, alert); err != nil {
			logger.Error("seed failed", "reason", err)
			os.Exit(1)
		}
	}
	seeds := demos

	healthHandler := health.New()
	healthHandler.SetReady(true)
	status := web.NewStatusStore()
	polled := now
	for _, c := range []struct{ id, st, detail string }{{"CAM-1", "ok", "all clear"}, {"CAM-3", "standby", "outside operating window 09:00-12:30"}, {"CAM-4", "standby", "outside operating window 12:30-16:00"}} {
		c := c
		status.UpdateCamera(c.id, func(s *web.ModelStatus) {
			s.Status, s.Detail, s.Ready, s.LoopLocked = c.st, c.detail, c.st == "ok", c.st == "ok"
			s.Counters = map[string]float64{"frames_total": 184023}
		})
	}
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

	api := web.NewAPI(alertStore, evidenceRoot)
	api.SettingsPath = filepath.Join(root, "settings.json")
	server := &http.Server{
		Addr:              "127.0.0.1:8080",
		Handler:           web.NewHandler(healthHandler, metrics.New(), api, status),
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
