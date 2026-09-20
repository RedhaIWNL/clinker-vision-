package web

import (
	"net/http/httptest"
	"testing"

	"github.com/clinkervision/clinker-vision/internal/health"
	"github.com/clinkervision/clinker-vision/internal/metrics"
)

func TestNewHandlerRoutesFoundationEndpoints(t *testing.T) {
	h := health.New()
	h.SetReady(true)
	handler := NewHandler(h, metrics.New())

	for _, path := range []string{"/health/live", "/health/ready", "/metrics"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		if recorder.Code != 200 {
			t.Fatalf("GET %s returned %d", path, recorder.Code)
		}
	}
}
