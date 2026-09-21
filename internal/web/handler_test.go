package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/clinkervision/clinker-vision/internal/health"
	"github.com/clinkervision/clinker-vision/internal/metrics"
)

func TestNewHandlerRoutesFoundationEndpoints(t *testing.T) {
	h := health.New()
	h.SetReady(true)
	handler := NewHandler(h, metrics.New(), nil, nil)

	for _, path := range []string{"/health/live", "/health/ready", "/metrics"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		if recorder.Code != 200 {
			t.Fatalf("GET %s returned %d", path, recorder.Code)
		}
	}
}

func TestStatusEndpointReportsStartingThenUpdate(t *testing.T) {
	h := health.New()
	store := NewStatusStore()
	handler := NewHandler(h, metrics.New(), nil, store)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/status returned %d", recorder.Code)
	}
	var starting ModelStatus
	if err := json.NewDecoder(recorder.Body).Decode(&starting); err != nil {
		t.Fatal(err)
	}
	if starting.Status != "starting" || starting.Ready {
		t.Fatalf("unexpected starting snapshot: %#v", starting)
	}

	now := time.Now().UTC()
	store.Update(func(s *ModelStatus) {
		s.Ready = true
		s.LoopLocked = true
		s.Status = "ok"
		s.Detail = "all clear"
		s.ModelVersion = "test-version"
		s.LastPollAt = &now
		s.Counters = map[string]float64{"frames_total": 25}
	})
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	var got ModelStatus
	if err := json.NewDecoder(recorder.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Ready || !got.LoopLocked || got.Status != "ok" || got.ModelVersion != "test-version" {
		t.Fatalf("unexpected updated snapshot: %#v", got)
	}
	if got.LastPollAt == nil || !got.LastPollAt.Equal(now) || got.Counters["frames_total"] != 25 {
		t.Fatalf("snapshot lost poll metadata: %#v", got)
	}
}

func TestStatusEndpointNilStoreStillServes(t *testing.T) {
	handler := NewHandler(health.New(), metrics.New(), nil, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/status with nil store returned %d", recorder.Code)
	}
}
