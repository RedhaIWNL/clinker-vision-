package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/clinkervision/clinker-vision/internal/evidence"
	"github.com/clinkervision/clinker-vision/internal/health"
	"github.com/clinkervision/clinker-vision/internal/metrics"
	"github.com/clinkervision/clinker-vision/internal/store"
)

func TestAlertAPIContract(t *testing.T) {
	root := t.TempDir()
	alertStore, err := store.Open(context.Background(), filepath.Join(root, "alerts.db"), filepath.Join(root, "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	defer alertStore.Close()
	older := webTestAlert("550e8400-e29b-41d4-a716-446655440020", "550e8400-e29b-41d4-a716-446655440021", "CAM-1", "CRACK", time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC))
	newer := webTestAlert("550e8400-e29b-41d4-a716-446655440022", "550e8400-e29b-41d4-a716-446655440023", "CAM-1", "MISALIGNMENT", older.DetectedAt.Add(time.Minute))
	otherCamera := webTestAlert("550e8400-e29b-41d4-a716-446655440024", "550e8400-e29b-41d4-a716-446655440025", "CAM-2", "CRACK", newer.DetectedAt.Add(time.Minute))
	for _, alert := range []store.Alert{older, newer, otherCamera} {
		if err := evidence.WriteAtomic(filepath.Join(root, "evidence"), alert.EvidenceRef, []byte("jpeg-"+alert.AlertID)); err != nil {
			t.Fatal(err)
		}
		if err := alertStore.InsertAlert(context.Background(), alert); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := alertStore.MarkSeen(context.Background(), newer.AlertID, newer.DetectedAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	api := NewAPI(alertStore, filepath.Join(root, "evidence"))
	api.Now = func() time.Time { return now }
	h := health.New()
	h.SetReady(true)
	handler := NewHandler(h, metrics.New(), api)

	response := doAPIRequest(t, handler, http.MethodGet, "/api/v1/alerts?camera_id=CAM-1&limit=1")
	var page listResponse
	decodeResponse(t, response, &page)
	if len(page.Items) != 1 || page.Items[0].AlertID != newer.AlertID || page.NextCursor == nil {
		t.Fatalf("unexpected first page: %#v", page)
	}
	response = doAPIRequest(t, handler, http.MethodGet, "/api/v1/alerts?camera_id=CAM-1&limit=1&cursor="+*page.NextCursor)
	decodeResponse(t, response, &page)
	if len(page.Items) != 1 || page.Items[0].AlertID != older.AlertID || page.NextCursor != nil {
		t.Fatalf("unexpected second page: %#v", page)
	}

	response = doAPIRequest(t, handler, http.MethodGet, "/api/v1/alerts?camera_id=CAM-1&unseen_only=true")
	decodeResponse(t, response, &page)
	if len(page.Items) != 1 || page.Items[0].AlertID != older.AlertID || page.Items[0].SeenAt != nil {
		t.Fatalf("unexpected unseen page: %#v", page)
	}

	response = doAPIRequest(t, handler, http.MethodGet, "/api/v1/alerts/"+older.AlertID)
	var detail alertResponse
	decodeResponse(t, response, &detail)
	if detail.EvidenceURL != "/api/v1/alerts/"+older.AlertID+"/evidence" || detail.BoundingBox.W != older.BoundingBox.Width {
		t.Fatalf("unexpected detail: %#v", detail)
	}

	response = doAPIRequest(t, handler, http.MethodGet, detail.EvidenceURL)
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "image/jpeg" || string(body) != "jpeg-"+older.AlertID {
		t.Fatalf("unexpected evidence response: status=%d type=%q body=%q", response.StatusCode, response.Header.Get("Content-Type"), body)
	}

	response = doAPIRequest(t, handler, http.MethodPost, "/api/v1/alerts/"+older.AlertID+"/seen")
	var seen seenResponse
	decodeResponse(t, response, &seen)
	if seen.AlertID != older.AlertID || !seen.SeenAt.Equal(now) {
		t.Fatalf("unexpected seen response: %#v", seen)
	}
	now = now.Add(time.Hour)
	response = doAPIRequest(t, handler, http.MethodPost, "/api/v1/alerts/"+older.AlertID+"/seen")
	decodeResponse(t, response, &seen)
	if !seen.SeenAt.Equal(time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("mark seen was not idempotent: %v", seen.SeenAt)
	}

	response = doAPIRequest(t, handler, http.MethodGet, "/")
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("viewer response: status=%d type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	response.Body.Close()
}

func TestAlertAPIHidesMissingAndInvalidEvidence(t *testing.T) {
	root := t.TempDir()
	alertStore, err := store.Open(context.Background(), filepath.Join(root, "alerts.db"), filepath.Join(root, "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	defer alertStore.Close()
	api := NewAPI(alertStore, filepath.Join(root, "evidence"))
	handler := NewHandler(health.New(), metrics.New(), api)
	for _, path := range []string{"/api/v1/alerts/not-a-uuid", "/api/v1/alerts/550e8400-e29b-41d4-a716-446655440099/evidence"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s returned %d", path, recorder.Code)
		}
	}
}

func decodeResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected HTTP status: %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func doAPIRequest(t *testing.T, handler http.Handler, method, path string) *http.Response {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder.Result()
}

func webTestAlert(alertID, frameID, cameraID, faultType string, detectedAt time.Time) store.Alert {
	return store.Alert{
		AlertID: alertID, CapturedAt: detectedAt, DetectedAt: detectedAt, CreatedAt: detectedAt,
		CameraID: cameraID, ObservationTarget: "godet", FaultType: faultType, Confidence: 0.93,
		FrameID: frameID, ModelVersion: "mock-test", BoundingBox: store.BoundingBox{X: 0.2, Y: 0.3, Width: 0.18, Height: 0.12},
		EvidenceRef: "2026/09/17/" + cameraID + "/" + alertID + ".jpg",
	}
}
