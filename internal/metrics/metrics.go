package metrics

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

type Metrics struct {
	FramesDecoded      atomic.Uint64
	FramesSampled      atomic.Uint64
	FrameDrops         atomic.Uint64
	DecodeFailures     atomic.Uint64
	InferenceRequests  atomic.Uint64
	AlertsCreated      atomic.Uint64
	AlertsDroppedCap   atomic.Uint64
	DependencyFailures atomic.Uint64
}

func New() *Metrics {
	return &Metrics{}
}

func (m *Metrics) Handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP clinker_vision_up Whether the pipeline process is running.\n# TYPE clinker_vision_up gauge\nclinker_vision_up 1\n")
	writeCounter(w, "clinker_vision_frames_decoded_total", "Frames decoded from enabled camera sources.", m.FramesDecoded.Load())
	writeCounter(w, "clinker_vision_frames_sampled_total", "Frames sampled from enabled cameras.", m.FramesSampled.Load())
	writeCounter(w, "clinker_vision_frame_drops_total", "Frames dropped because a bounded queue was full.", m.FrameDrops.Load())
	writeCounter(w, "clinker_vision_decode_failures_total", "Camera source or frame decode failures.", m.DecodeFailures.Load())
	writeCounter(w, "clinker_vision_inference_requests_total", "Inference requests sent to the model service.", m.InferenceRequests.Load())
	writeCounter(w, "clinker_vision_alerts_created_total", "Alerts created for qualifying detections.", m.AlertsCreated.Load())
	writeCounter(w, "clinker_vision_alerts_dropped_cap_total", "Alert events dropped because the storage cap was reached.", m.AlertsDroppedCap.Load())
	writeCounter(w, "clinker_vision_dependency_failures_total", "Required dependency failures observed.", m.DependencyFailures.Load())
}

func writeCounter(w http.ResponseWriter, name, help string, value uint64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, value)
}
