package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerExportsCounters(t *testing.T) {
	m := New()
	m.FramesDecoded.Add(2)
	m.FramesSampled.Add(3)
	m.FrameDrops.Add(1)
	m.DecodeFailures.Add(1)
	recorder := httptest.NewRecorder()
	m.Handler(recorder, httptest.NewRequest("GET", "/metrics", nil))

	body := recorder.Body.String()
	for _, expected := range []string{
		"clinker_vision_up 1",
		"clinker_vision_frames_decoded_total 2",
		"clinker_vision_frames_sampled_total 3",
		"clinker_vision_frame_drops_total 1",
		"clinker_vision_decode_failures_total 1",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics missing %q:\n%s", expected, body)
		}
	}
}
