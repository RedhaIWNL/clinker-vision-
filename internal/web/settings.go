package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Settings the operator changes in the web page. The model reads the same file (mounted read-only)
// at each wheel evaluation, once per chain loop, so a change applies from the next loop.
type Settings struct {
	// alert when this many godets in a row are exceeded without a wheel (today's rule: 5 or more -> 4)
	WheelMaxGapGodets int `json:"wheel_max_gap_godets"`
	// alert when two wheels are closer than this many godets (today's rule: within 3 godets -> 3)
	WheelMinSpacingGodets int `json:"wheel_min_spacing_godets"`
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
	writeJSON(w, http.StatusOK, LoadSettings(a.SettingsPath))
}

func (a *API) PutSettings(w http.ResponseWriter, r *http.Request) {
	if a.SettingsPath == "" {
		writeError(w, http.StatusNotFound, "settings are not available")
		return
	}
	var in Settings
	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil || json.Unmarshal(body, &in) != nil {
		writeError(w, http.StatusBadRequest, "settings must be JSON")
		return
	}
	if err := in.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	if err := writeFileAtomic(a.SettingsPath, in); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the settings")
		return
	}
	writeJSON(w, http.StatusOK, in)
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
