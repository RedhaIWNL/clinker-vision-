package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsValidate(t *testing.T) {
	cfg := Defaults()
	cfg.Cameras[0].NVRRTSPURL = "rtsp://nvr.example.local:554/cam/realmonitor?channel=1"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("defaults should validate after setting CAM-1 URL: %v", err)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := writeConfig(t, "unknown: true\n")
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
}

func TestLoadValidConfig(t *testing.T) {
	cfg, err := func() (Config, error) {
		return Load(writeConfig(t, validYAML()))
	}()
	if err != nil {
		t.Fatalf("valid config was rejected: %v", err)
	}
	if cfg.Server.BindAddress != "127.0.0.1" || cfg.Model.ConfidenceThreshold != 0.85 {
		t.Fatalf("unexpected loaded config: %#v", cfg)
	}
}

func TestLoadRejectsInsecurePermissions(t *testing.T) {
	path := writeConfig(t, validYAML())
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("expected permissions error, got %v", err)
	}
}

func TestValidateBindRules(t *testing.T) {
	cfg := validConfig()
	cfg.Server.BindAddress = "127.0.0.1"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("127.0.0.1 must be accepted: %v", err)
	}
	cfg.Server.BindAddress = "0.0.0.0"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("0.0.0.0 must be accepted for the container listener: %v", err)
	}
	cfg.Server.BindAddress = "192.168.1.13"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected non-local bind address to be rejected")
	}
}

func TestValidateRejectsMVPFutureCamera(t *testing.T) {
	cfg := validConfig()
	cfg.Cameras[1].Enabled = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected future camera to be rejected when enabled")
	}
}

func TestValidateRejectsInvalidThreshold(t *testing.T) {
	cfg := validConfig()
	cfg.Model.ConfidenceThreshold = 1.01
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid confidence threshold to be rejected")
	}
}

func TestValidateRejectsMalformedRTSPURL(t *testing.T) {
	cfg := validConfig()
	cfg.Cameras[0].NVRRTSPURL = "https://nvr.example.local/stream"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected malformed RTSP URL to be rejected")
	}
}

func validConfig() Config {
	cfg := Defaults()
	cfg.Cameras[0].NVRRTSPURL = "rtsp://user:password@nvr.example.local:554/cam/realmonitor?channel=1"
	return cfg
}

func validYAML() string {
	return `server:
  bind_address: "127.0.0.1"
  web_port: 8080
cameras:
  - id: "CAM-1"
    enabled: true
    nvr_rtsp_url: "rtsp://nvr.example.local:554/cam/realmonitor?channel=1"
    sample_interval_seconds: 2
    queue_capacity: 2
  - {id: "CAM-2", enabled: false, sample_interval_seconds: 2, queue_capacity: 2}
  - {id: "CAM-3", enabled: false, sample_interval_seconds: 2, queue_capacity: 2}
  - {id: "CAM-4", enabled: false, sample_interval_seconds: 2, queue_capacity: 2}
  - {id: "CAM-5", enabled: false, sample_interval_seconds: 2, queue_capacity: 2}
  - {id: "CAM-6", enabled: false, sample_interval_seconds: 2, queue_capacity: 2}
model:
  grpc_address: "model:50051"
  request_timeout_seconds: 5
  confidence_threshold: 0.85
failure:
  dependency_timeout_seconds: 300
storage:
  sqlite_path: "/srv/clinker-vision/data/alerts.db"
  evidence_path: "/srv/clinker-vision/evidence"
  jpeg_quality: 85
retention:
  days: 365
`
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
