package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	DefaultBindAddress                   = "127.0.0.1"
	DefaultWebPort                       = 8080
	DefaultSampleIntervalSeconds         = 2
	DefaultQueueCapacity                 = 64
	DefaultRequestTimeoutSeconds         = 5
	DefaultStatePollSeconds              = 1
	DefaultMaxJPEGBytes                  = 8000000
	DefaultConfidenceThreshold           = 0.85
	DefaultDependencyTimeoutSeconds      = 300
	DefaultJPEGQuality                   = 85
	DefaultRetentionDays                 = 365
	DefaultModelAddress                  = "model:50051"
	DefaultSQLitePath                    = "/srv/clinker-vision/data/alerts.db"
	DefaultEvidencePath                  = "/srv/clinker-vision/evidence"
	defaultProtectedConfigPermissionMask = 0o077
)

var requiredCameraIDs = []string{"CAM-1", "CAM-2", "CAM-3", "CAM-4", "CAM-5", "CAM-6"}

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Cameras   []CameraConfig  `yaml:"cameras"`
	Model     ModelConfig     `yaml:"model"`
	Failure   FailureConfig   `yaml:"failure"`
	Storage   StorageConfig   `yaml:"storage"`
	Retention RetentionConfig `yaml:"retention"`
}

type ServerConfig struct {
	BindAddress string `yaml:"bind_address"`
	WebPort     int    `yaml:"web_port"`
}

type CameraConfig struct {
	ID                    string `yaml:"id"`
	Enabled               bool   `yaml:"enabled"`
	NVRRTSPURL            string `yaml:"nvr_rtsp_url"`
	SampleIntervalSeconds int    `yaml:"sample_interval_seconds"`
	QueueCapacity         int    `yaml:"queue_capacity"`
}

type ModelConfig struct {
	GRPCAddress           string  `yaml:"grpc_address"`
	RequestTimeoutSeconds int     `yaml:"request_timeout_seconds"`
	StatePollSeconds      int     `yaml:"state_poll_seconds"`
	MaxJPEGBytes          int     `yaml:"max_jpeg_bytes"`
	ConfidenceThreshold   float64 `yaml:"confidence_threshold"`
}

type FailureConfig struct {
	DependencyTimeoutSeconds int `yaml:"dependency_timeout_seconds"`
}

type StorageConfig struct {
	SQLitePath   string `yaml:"sqlite_path"`
	EvidencePath string `yaml:"evidence_path"`
	JPEGQuality  int    `yaml:"jpeg_quality"`
}

type RetentionConfig struct {
	Days int `yaml:"days"`
}

func Defaults() Config {
	cameras := make([]CameraConfig, 0, len(requiredCameraIDs))
	for _, id := range requiredCameraIDs {
		cameras = append(cameras, CameraConfig{
			ID:                    id,
			Enabled:               id == "CAM-1",
			SampleIntervalSeconds: DefaultSampleIntervalSeconds,
			QueueCapacity:         DefaultQueueCapacity,
		})
	}

	return Config{
		Server: ServerConfig{
			BindAddress: DefaultBindAddress,
			WebPort:     DefaultWebPort,
		},
		Cameras: cameras,
		Model: ModelConfig{
			GRPCAddress:           DefaultModelAddress,
			RequestTimeoutSeconds: DefaultRequestTimeoutSeconds,
			StatePollSeconds:      DefaultStatePollSeconds,
			MaxJPEGBytes:          DefaultMaxJPEGBytes,
			ConfidenceThreshold:   DefaultConfidenceThreshold,
		},
		Failure: FailureConfig{
			DependencyTimeoutSeconds: DefaultDependencyTimeoutSeconds,
		},
		Storage: StorageConfig{
			SQLitePath:   DefaultSQLitePath,
			EvidencePath: DefaultEvidencePath,
			JPEGQuality:  DefaultJPEGQuality,
		},
		Retention: RetentionConfig{Days: DefaultRetentionDays},
	}
}

func Load(path string) (Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Config{}, fmt.Errorf("stat config: %w", err)
	}
	if info.Mode().Perm()&defaultProtectedConfigPermissionMask != 0 {
		return Config{}, errors.New("config file permissions must not allow group or other access")
	}

	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	cfg := Defaults()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := ensureSingleDocument(decoder); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func ensureSingleDocument(decoder *yaml.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("config must contain exactly one YAML document")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode trailing config: %w", err)
	}
	return nil
}

func (c Config) Validate() error {
	if c.Server.BindAddress == "" {
		return errors.New("server.bind_address is required")
	}
	parsedIP := net.ParseIP(c.Server.BindAddress)
	if parsedIP == nil || (!parsedIP.Equal(net.ParseIP("127.0.0.1")) && !parsedIP.Equal(net.ParseIP("0.0.0.0"))) {
		return errors.New("server.bind_address must be 127.0.0.1 or 0.0.0.0 (0.0.0.0 is for the container only; the host publish stays localhost-only)")
	}
	if c.Server.WebPort < 1 || c.Server.WebPort > 65535 {
		return errors.New("server.web_port must be between 1 and 65535")
	}

	if err := validateCameras(c.Cameras); err != nil {
		return err
	}
	if c.Model.GRPCAddress == "" {
		return errors.New("model.grpc_address is required")
	}
	if c.Model.RequestTimeoutSeconds <= 0 {
		return errors.New("model.request_timeout_seconds must be positive")
	}
	if c.Model.StatePollSeconds <= 0 {
		return errors.New("model.state_poll_seconds must be positive")
	}
	if c.Model.MaxJPEGBytes <= 0 {
		return errors.New("model.max_jpeg_bytes must be positive")
	}
	if c.Model.ConfidenceThreshold < 0 || c.Model.ConfidenceThreshold > 1 {
		return errors.New("model.confidence_threshold must be between 0 and 1")
	}
	if c.Failure.DependencyTimeoutSeconds <= 0 {
		return errors.New("failure.dependency_timeout_seconds must be positive")
	}
	if !isAbsolutePath(c.Storage.SQLitePath) {
		return errors.New("storage.sqlite_path must be an absolute path")
	}
	if !isAbsolutePath(c.Storage.EvidencePath) {
		return errors.New("storage.evidence_path must be an absolute path")
	}
	if c.Storage.JPEGQuality < 1 || c.Storage.JPEGQuality > 100 {
		return errors.New("storage.jpeg_quality must be between 1 and 100")
	}
	if c.Retention.Days <= 0 {
		return errors.New("retention.days must be positive")
	}
	return nil
}

func validateCameras(cameras []CameraConfig) error {
	if len(cameras) != len(requiredCameraIDs) {
		return fmt.Errorf("cameras must contain exactly %d entries", len(requiredCameraIDs))
	}

	want := make(map[string]bool, len(requiredCameraIDs))
	for _, id := range requiredCameraIDs {
		want[id] = true
	}
	seen := make(map[string]bool, len(cameras))
	for _, camera := range cameras {
		if !want[camera.ID] {
			return fmt.Errorf("unknown camera id %q", camera.ID)
		}
		if seen[camera.ID] {
			return fmt.Errorf("duplicate camera id %q", camera.ID)
		}
		seen[camera.ID] = true
		if camera.SampleIntervalSeconds <= 0 {
			return fmt.Errorf("camera %s sample_interval_seconds must be positive", camera.ID)
		}
		if camera.QueueCapacity <= 0 {
			return fmt.Errorf("camera %s queue_capacity must be positive", camera.ID)
		}
		if camera.Enabled {
			if camera.ID != "CAM-1" {
				return fmt.Errorf("camera %s must be disabled in the MVP", camera.ID)
			}
			if err := validateRTSPURL(camera.NVRRTSPURL); err != nil {
				return fmt.Errorf("camera %s: %w", camera.ID, err)
			}
		}
	}

	for _, id := range requiredCameraIDs {
		if !seen[id] {
			return fmt.Errorf("missing camera id %q", id)
		}
	}
	return nil
}

func validateRTSPURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "rtsp" || parsed.Host == "" {
		return errors.New("nvr_rtsp_url must be a valid rtsp URL")
	}
	return nil
}

func isAbsolutePath(path string) bool {
	return strings.HasPrefix(path, "/") && path != "/"
}

func RequiredCameraIDs() []string {
	ids := append([]string(nil), requiredCameraIDs...)
	sort.Strings(ids)
	return ids
}
