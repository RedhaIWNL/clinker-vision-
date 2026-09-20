package health

import (
	"net/http/httptest"
	"testing"
)

func TestReadinessTransitions(t *testing.T) {
	h := New()
	notReady := httptest.NewRecorder()
	h.Ready(notReady, httptest.NewRequest("GET", "/health/ready", nil))
	if notReady.Code != 503 {
		t.Fatalf("expected 503 before ready, got %d", notReady.Code)
	}

	h.SetReady(true)
	ready := httptest.NewRecorder()
	h.Ready(ready, httptest.NewRequest("GET", "/health/ready", nil))
	if ready.Code != 200 {
		t.Fatalf("expected 200 after ready, got %d", ready.Code)
	}
}
