package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestNewJSONEmitsStructuredRecord(t *testing.T) {
	var output bytes.Buffer
	logger := NewJSON(&output, slog.LevelInfo)
	logger.Info("foundation started", "component", "pipeline", "camera_id", "CAM-1")

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("log is not JSON: %v", err)
	}
	if record["msg"] != "foundation started" || record["camera_id"] != "CAM-1" {
		t.Fatalf("unexpected structured log record: %#v", record)
	}
}
