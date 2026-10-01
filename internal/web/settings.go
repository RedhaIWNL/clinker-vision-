package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

// Settings the operator changes in the web page. The model reads the same file (mounted read-only)
// at each wheel evaluation, once per chain loop, so a change applies from the next loop.
type Settings struct {
	// alert when this many godets in a row are exceeded without a wheel (today's rule: 5 or more -> 4)
	WheelMaxGapGodets int `json:"wheel_max_gap_godets"`
	// alert when two wheels are closer than this many godets (today's rule: within 3 godets -> 3)
	WheelMinSpacingGodets int `json:"wheel_min_spacing_godets"`
	// Camera hours set on the page (camera id -> hours). They replace the hours in config.yaml for that
	// camera; the pipeline reads them every 15 s, so a change applies without a restart. A camera that is
	// not here follows config.yaml.
	CameraHours map[string]CameraHours `json:"camera_hours,omitempty"`
}

// CameraHours: Mode "hours" (on from Start to Stop, HH:MM, may pass midnight), "always" or "off".
type CameraHours struct {
	Mode  string `json:"mode"`
	Start string `json:"start,omitempty"`
	Stop  string `json:"stop,omitempty"`
}

// CameraInfo describes a camera enabled in config.yaml, for the page: its hours from config.yaml.
type CameraInfo struct {
	ID          string      `json:"id"`
	ConfigHours CameraHours `json:"config_hours"`
}

func hhmm(v string) (int, bool) {
	if len(v) != 5 || v[2] != ':' {
		return 0, false
	}
	for i, c := range v {
		if i != 2 && (c < '0' || c > '9') {
			return 0, false
		}
	}
	h, m := int(v[0]-'0')*10+int(v[1]-'0'), int(v[3]-'0')*10+int(v[4]-'0')
	return h*60 + m, h <= 23 && m <= 59
}

func (h CameraHours) validate(id string) error {
	switch h.Mode {
	case "always", "off":
		return nil
	case "hours":
		a, okA := hhmm(h.Start)
		b, okB := hhmm(h.Stop)
		if !okA || !okB {
			return fmt.Errorf("%s: hours must be HH:MM (00:00 to 23:59)", id)
		}
		if a == b {
			return fmt.Errorf("%s: start and stop are the same; choose \"always on\" instead", id)
		}
		return nil
	}
	return fmt.Errorf("%s: mode must be hours, always or off", id)
}

// Minutes of the day (0..1439) when the camera is on; nil for "always".
func (h CameraHours) minutes() []bool {
	on := make([]bool, 1440)
	switch h.Mode {
	case "always":
		for i := range on {
			on[i] = true
		}
	case "hours":
		a, _ := hhmm(h.Start)
		b, _ := hhmm(h.Stop)
		for m := a; m != b; m = (m + 1) % 1440 {
			on[m] = true
		}
	}
	return on
}

// Overlaps lists the cameras whose page hours overlap (the plant server runs one camera at a time).
func (s Settings) Overlaps() []string {
	ids := make([]string, 0, len(s.CameraHours))
	for id := range s.CameraHours {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []string
	for i := range ids {
		a := s.CameraHours[ids[i]].minutes()
		for j := i + 1; j < len(ids); j++ {
			b := s.CameraHours[ids[j]].minutes()
			for m := range a {
				if a[m] && b[m] {
					out = append(out, ids[i]+" and "+ids[j])
					break
				}
			}
		}
	}
	return out
}

// DefaultSettings are the rules the wheel alerts were verified with (WHEELS_PLAN.md).
func DefaultSettings() Settings {
	return Settings{WheelMaxGapGodets: 4, WheelMinSpacingGodets: 3}
}

func (s Settings) validate() error {
	if s.WheelMaxGapGodets < 2 || s.WheelMaxGapGodets > 20 {
		return errors.New("maximum godets without a wheel must be between 2 and 20")
	}
	if s.WheelMinSpacingGodets < 1 || s.WheelMinSpacingGodets > 6 {
		return errors.New("minimum spacing between wheels must be between 1 and 6 godets")
	}
	if s.WheelMinSpacingGodets > s.WheelMaxGapGodets {
		return errors.New("the minimum spacing cannot be larger than the maximum gap")
	}
	for id, h := range s.CameraHours {
		if err := h.validate(id); err != nil {
			return err
		}
	}
	return nil
}

// LoadSettings reads the settings file; missing or unreadable values fall back to the defaults.
func LoadSettings(path string) Settings {
	out := DefaultSettings()
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var in Settings
	if json.Unmarshal(raw, &in) != nil || in.validate() != nil {
		return out
	}
	return in
}

func (a *API) GetSettings(w http.ResponseWriter, _ *http.Request) {
	if a.SettingsPath == "" {
		writeError(w, http.StatusNotFound, "settings are not available")
		return
	}
	writeJSON(w, http.StatusOK, a.settingsView(LoadSettings(a.SettingsPath)))
}

type settingsView struct {
	Settings
	Cameras  []CameraInfo `json:"cameras,omitempty"`
	Overlaps []string     `json:"overlaps,omitempty"`
}

func (a *API) settingsView(s Settings) settingsView {
	return settingsView{Settings: s, Cameras: a.Cameras, Overlaps: s.Overlaps()}
}

func (a *API) PutSettings(w http.ResponseWriter, r *http.Request) {
	if a.SettingsPath == "" {
		writeError(w, http.StatusNotFound, "settings are not available")
		return
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	// A change of one part (wheel limits, or camera hours) keeps the other part as saved.
	in := LoadSettings(a.SettingsPath)
	var change struct {
		Settings
		CameraHours map[string]*CameraHours `json:"camera_hours"`
	}
	change.Settings = in
	body, err := io.ReadAll(io.LimitReader(r.Body, 8192))
	if err != nil || json.Unmarshal(body, &change) != nil {
		writeError(w, http.StatusBadRequest, "settings must be JSON")
		return
	}
	in.WheelMaxGapGodets, in.WheelMinSpacingGodets = change.WheelMaxGapGodets, change.WheelMinSpacingGodets
	if change.CameraHours != nil {
		hours := make(map[string]CameraHours, len(in.CameraHours))
		for id, h := range in.CameraHours {
			hours[id] = h
		}
		for id, h := range change.CameraHours { // null = back to config.yaml
			if h == nil {
				delete(hours, id)
			} else {
				hours[id] = *h
			}
		}
		in.CameraHours = hours
	}
	if err := in.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := writeFileAtomic(a.SettingsPath, in); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the settings")
		return
	}
	writeJSON(w, http.StatusOK, a.settingsView(in))
}

func writeFileAtomic(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", " ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil { // the model (same user) reads it through a read-only mount
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace settings: %w", err)
	}
	return nil
}
