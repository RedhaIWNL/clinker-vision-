package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestDefaultsScheduleAndMaxAlerts(t *testing.T) {
	cfg := Defaults()
	if cfg.Schedule.Enabled {
		t.Fatal("schedule should be disabled by default")
	}
	if cfg.Schedule.Start != "20:00" || cfg.Schedule.Stop != "04:00" || cfg.Schedule.Timezone != "Africa/Casablanca" {
		t.Fatalf("unexpected default schedule: %#v", cfg.Schedule)
	}
	if cfg.Retention.MaxAlerts != 0 {
		t.Fatalf("default max_alerts should be 0 (unlimited), got %d", cfg.Retention.MaxAlerts)
	}
}

func TestScheduleContains(t *testing.T) {
	loc := time.FixedZone("test", 0)
	casa, err := time.LoadLocation("Africa/Casablanca")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	mkTime := func(l *time.Location, hour, min int) time.Time {
		return time.Date(2026, 1, 15, hour, min, 0, 0, l)
	}
	tests := []struct {
		name  string
		sched ScheduleConfig
		at    time.Time
		want  bool
	}{
		{name: "wrap contains evening", sched: ScheduleConfig{Enabled: true, Start: "20:00", Stop: "04:00", Timezone: "Africa/Casablanca"}, at: mkTime(casa, 23, 0), want: true},
		{name: "wrap contains early morning", sched: ScheduleConfig{Enabled: true, Start: "20:00", Stop: "04:00", Timezone: "Africa/Casablanca"}, at: mkTime(casa, 2, 0), want: true},
		{name: "wrap excludes midday", sched: ScheduleConfig{Enabled: true, Start: "20:00", Stop: "04:00", Timezone: "Africa/Casablanca"}, at: mkTime(casa, 12, 0), want: false},
		{name: "wrap start inclusive", sched: ScheduleConfig{Enabled: true, Start: "20:00", Stop: "04:00", Timezone: "Africa/Casablanca"}, at: mkTime(casa, 20, 0), want: true},
		{name: "wrap stop exclusive", sched: ScheduleConfig{Enabled: true, Start: "20:00", Stop: "04:00", Timezone: "Africa/Casablanca"}, at: mkTime(casa, 4, 0), want: false},
		{name: "non-wrap contains midday", sched: ScheduleConfig{Enabled: true, Start: "09:00", Stop: "17:00", Timezone: "Africa/Casablanca"}, at: mkTime(casa, 12, 0), want: true},
		{name: "non-wrap excludes before", sched: ScheduleConfig{Enabled: true, Start: "09:00", Stop: "17:00", Timezone: "Africa/Casablanca"}, at: mkTime(casa, 8, 59), want: false},
		{name: "non-wrap excludes after", sched: ScheduleConfig{Enabled: true, Start: "09:00", Stop: "17:00", Timezone: "Africa/Casablanca"}, at: mkTime(casa, 17, 0), want: false},
		{name: "non-wrap start inclusive", sched: ScheduleConfig{Enabled: true, Start: "09:00", Stop: "17:00", Timezone: "Africa/Casablanca"}, at: mkTime(casa, 9, 0), want: true},
		{name: "disabled always runs midday", sched: ScheduleConfig{Enabled: false, Start: "20:00", Stop: "04:00", Timezone: "Africa/Casablanca"}, at: mkTime(casa, 12, 0), want: true},
		{name: "disabled always runs night", sched: ScheduleConfig{Enabled: false, Start: "20:00", Stop: "04:00", Timezone: "Africa/Casablanca"}, at: mkTime(casa, 23, 0), want: true},
		{name: "disabled ignores garbage", sched: ScheduleConfig{Enabled: false, Start: "bogus", Stop: "bogus", Timezone: "bogus"}, at: mkTime(loc, 12, 0), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sched.Contains(tt.at); got != tt.want {
				t.Fatalf("Contains(%v) = %v, want %v (sched=%#v)", tt.at, got, tt.want, tt.sched)
			}
		})
	}
}

func TestValidateRejectsBadScheduleAndMaxAlerts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "bad start hour", mutate: func(c *Config) {
			c.Schedule = ScheduleConfig{Enabled: true, Start: "25:00", Stop: "04:00", Timezone: "Africa/Casablanca"}
		}},
		{name: "bad start format", mutate: func(c *Config) {
			c.Schedule = ScheduleConfig{Enabled: true, Start: "20-00", Stop: "04:00", Timezone: "Africa/Casablanca"}
		}},
		{name: "bad start short", mutate: func(c *Config) {
			c.Schedule = ScheduleConfig{Enabled: true, Start: "4:00", Stop: "04:00", Timezone: "Africa/Casablanca"}
		}},
		{name: "bad stop minute", mutate: func(c *Config) {
			c.Schedule = ScheduleConfig{Enabled: true, Start: "20:00", Stop: "04:60", Timezone: "Africa/Casablanca"}
		}},
		{name: "bad stop empty", mutate: func(c *Config) {
			c.Schedule = ScheduleConfig{Enabled: true, Start: "20:00", Stop: "", Timezone: "Africa/Casablanca"}
		}},
		{name: "bad timezone", mutate: func(c *Config) {
			c.Schedule = ScheduleConfig{Enabled: true, Start: "20:00", Stop: "04:00", Timezone: "Not/AZone"}
		}},
		{name: "empty timezone", mutate: func(c *Config) {
			c.Schedule = ScheduleConfig{Enabled: true, Start: "20:00", Stop: "04:00", Timezone: ""}
		}},
		{name: "negative max_alerts", mutate: func(c *Config) { c.Retention.MaxAlerts = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", tt.name)
			}
		})
	}
}

func TestValidateAcceptsDisabledScheduleWithGarbage(t *testing.T) {
	cfg := validConfig()
	cfg.Schedule = ScheduleConfig{Enabled: false, Start: "bogus", Stop: "bogus", Timezone: "bogus"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled schedule should skip validation, got: %v", err)
	}
}

func TestLoadNewKeysFromYAML(t *testing.T) {
	yaml := validYAML() + "schedule:\n  enabled: true\n  start: \"20:00\"\n  stop: \"04:00\"\n  timezone: \"Africa/Casablanca\"\nretention:\n  days: 30\n  max_alerts: 1000\n"
	// validYAML already has a retention block; strip it to avoid duplication.
	yaml = strings.Replace(yaml, "retention:\n  days: 365\n", "", 1)
	cfg, err := Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("config with new keys was rejected: %v", err)
	}
	if !cfg.Schedule.Enabled || cfg.Schedule.Start != "20:00" || cfg.Schedule.Stop != "04:00" || cfg.Schedule.Timezone != "Africa/Casablanca" {
		t.Fatalf("schedule keys not loaded: %#v", cfg.Schedule)
	}
	if cfg.Retention.Days != 30 || cfg.Retention.MaxAlerts != 1000 {
		t.Fatalf("retention keys not loaded: %#v", cfg.Retention)
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
