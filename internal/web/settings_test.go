package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/clinkervision/clinker-vision/internal/health"
	"github.com/clinkervision/clinker-vision/internal/metrics"
	"github.com/clinkervision/clinker-vision/internal/store"
)

func TestWheelLimitSettings(t *testing.T) {
	root := t.TempDir()
	alertStore, err := store.Open(context.Background(), filepath.Join(root, "alerts.db"), filepath.Join(root, "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	defer alertStore.Close()
	api := NewAPI(alertStore, filepath.Join(root, "evidence"))
	api.SettingsPath = filepath.Join(root, "settings.json")
	handler := NewHandler(health.New(), metrics.New(), api, nil)
	do := func(method, body string) (int, string) {
		req := httptest.NewRequest(method, "/api/v1/settings", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	if code, body := do(http.MethodGet, ""); code != 200 || !strings.Contains(body, `"wheel_max_gap_godets":4`) || !strings.Contains(body, `"wheel_min_spacing_godets":3`) {
		t.Fatalf("defaults: %d %s", code, body) // today's verified rules until changed
	}
	if code, body := do(http.MethodPut, `{"wheel_max_gap_godets":6,"wheel_min_spacing_godets":2}`); code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	raw, err := os.ReadFile(api.SettingsPath)
	if err != nil || !strings.Contains(string(raw), `"wheel_max_gap_godets": 6`) {
		t.Fatalf("file: %s %v", raw, err) // the file the model reads
	}
	for _, bad := range []string{`{"wheel_max_gap_godets":0,"wheel_min_spacing_godets":2}`, `{"wheel_max_gap_godets":3,"wheel_min_spacing_godets":5}`, `nonsense`} {
		if code, _ := do(http.MethodPut, bad); code != http.StatusBadRequest {
			t.Fatalf("%s accepted: %d", bad, code)
		}
	}
	if _, body := do(http.MethodGet, ""); !strings.Contains(body, `"wheel_max_gap_godets":6`) {
		t.Fatalf("a refused change must keep the saved limits: %s", body)
	}
}
