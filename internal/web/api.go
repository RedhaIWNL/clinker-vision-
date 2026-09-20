package web

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/clinkervision/clinker-vision/internal/evidence"
	"github.com/clinkervision/clinker-vision/internal/store"
	"github.com/google/uuid"
)

type API struct {
	Store        *store.Store
	EvidenceRoot string
	Now          func() time.Time
}

func NewAPI(alertStore *store.Store, evidenceRoot string) *API {
	return &API{Store: alertStore, EvidenceRoot: evidenceRoot, Now: time.Now}
}

type bboxResponse struct {
	X float32 `json:"x"`
	Y float32 `json:"y"`
	W float32 `json:"w"`
	H float32 `json:"h"`
}

type alertResponse struct {
	AlertID           string       `json:"alert_id"`
	CameraID          string       `json:"camera_id"`
	ObservationTarget string       `json:"observation_target"`
	FaultType         string       `json:"fault_type"`
	Confidence        *float32     `json:"confidence,omitempty"`
	CapturedAt        time.Time    `json:"captured_at"`
	DetectedAt        time.Time    `json:"detected_at"`
	FrameID           string       `json:"frame_id"`
	ModelVersion      string       `json:"model_version"`
	BoundingBox       bboxResponse `json:"bbox"`
	EvidenceRef       string       `json:"evidence_ref"`
	EvidenceURL       string       `json:"evidence_url"`
	SeenAt            *time.Time   `json:"seen_at"`
	GodetID           int32        `json:"godet_id,omitempty"`
	AlertState        string       `json:"state,omitempty"`
	LoopNo            int32        `json:"loop_no,omitempty"`
	RuleID            string       `json:"rule_id,omitempty"`
	EvidenceFrameID   string       `json:"evidence_frame_id,omitempty"`
	MeasurementsJSON  string       `json:"measurements_json,omitempty"`
}

type listResponse struct {
	Items      []alertResponse `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

type seenResponse struct {
	AlertID string    `json:"alert_id"`
	SeenAt  time.Time `json:"seen_at"`
}

type alertCursor struct {
	DetectedAt string `json:"detected_at"`
	AlertID    string `json:"alert_id"`
}

func (a *API) ListAlerts(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := 50
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "limit must be an integer")
			return
		}
		limit = parsed
	}
	unseenOnly := false
	if raw := query.Get("unseen_only"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "unseen_only must be a boolean")
			return
		}
		unseenOnly = parsed
	}
	var since *time.Time
	if raw := query.Get("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be RFC3339")
			return
		}
		since = &parsed
	}
	var beforeDetectedAt *time.Time
	var beforeAlertID string
	if raw := query.Get("cursor"); raw != "" {
		cursor, err := decodeCursor(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "cursor is invalid")
			return
		}
		parsed, err := time.Parse(time.RFC3339Nano, cursor.DetectedAt)
		if err != nil || uuid.Validate(cursor.AlertID) != nil {
			writeError(w, http.StatusBadRequest, "cursor is invalid")
			return
		}
		beforeDetectedAt = &parsed
		beforeAlertID = cursor.AlertID
	}
	result, err := a.Store.ListAlerts(r.Context(), store.ListFilter{
		CameraID: query.Get("camera_id"), FaultType: strings.ToUpper(query.Get("fault_type")),
		UnseenOnly: unseenOnly, Since: since, BeforeDetectedAt: beforeDetectedAt,
		BeforeAlertID: beforeAlertID, Limit: limit,
	})
	if err != nil {
		if strings.Contains(err.Error(), "limit must") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "could not list alerts")
		return
	}
	response := listResponse{Items: make([]alertResponse, 0, len(result.Alerts))}
	for _, alert := range result.Alerts {
		response.Items = append(response.Items, a.alertResponse(alert))
	}
	if result.HasMore && len(result.Alerts) > 0 {
		last := result.Alerts[len(result.Alerts)-1]
		raw, err := encodeCursor(alertCursor{DetectedAt: last.DetectedAt.Format(time.RFC3339Nano), AlertID: last.AlertID})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not create cursor")
			return
		}
		response.NextCursor = &raw
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *API) GetAlert(w http.ResponseWriter, r *http.Request) {
	alert, err := a.findAlert(r)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.alertResponse(alert))
}

func (a *API) ServeEvidence(w http.ResponseWriter, r *http.Request) {
	alert, err := a.findAlert(r)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !strings.EqualFold(filepath.Ext(alert.EvidenceRef), ".jpg") {
		writeError(w, http.StatusNotFound, "evidence not found")
		return
	}
	path, err := evidence.SafePath(a.EvidenceRoot, alert.EvidenceRef)
	if err != nil {
		writeError(w, http.StatusNotFound, "evidence not found")
		return
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "evidence not found")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "evidence not found")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}

func (a *API) MarkSeen(w http.ResponseWriter, r *http.Request) {
	alertID := r.PathValue("alert_id")
	if uuid.Validate(alertID) != nil {
		writeError(w, http.StatusNotFound, "alert not found")
		return
	}
	seenAt, err := a.Store.MarkSeen(r.Context(), alertID, a.Now())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, seenResponse{AlertID: alertID, SeenAt: *seenAt})
}

func (a *API) findAlert(r *http.Request) (store.Alert, error) {
	alertID := r.PathValue("alert_id")
	if uuid.Validate(alertID) != nil {
		return store.Alert{}, sql.ErrNoRows
	}
	return a.Store.GetAlert(r.Context(), alertID)
}

func (a *API) alertResponse(alert store.Alert) alertResponse {
	var confidence *float32
	if alert.ConfidenceKnown {
		value := alert.Confidence
		confidence = &value
	}
	return alertResponse{AlertID: alert.AlertID, CameraID: alert.CameraID, ObservationTarget: alert.ObservationTarget,
		FaultType: alert.FaultType, Confidence: confidence, CapturedAt: alert.CapturedAt, DetectedAt: alert.DetectedAt,
		FrameID: alert.FrameID, ModelVersion: alert.ModelVersion,
		BoundingBox: bboxResponse{X: alert.BoundingBox.X, Y: alert.BoundingBox.Y, W: alert.BoundingBox.Width, H: alert.BoundingBox.Height},
		EvidenceRef: alert.EvidenceRef, EvidenceURL: "/api/v1/alerts/" + alert.AlertID + "/evidence", SeenAt: alert.SeenAt,
		GodetID: alert.GodetID, AlertState: alert.AlertState, LoopNo: alert.LoopNo, RuleID: alert.RuleID,
		EvidenceFrameID: alert.EvidenceFrameID, MeasurementsJSON: alert.MeasurementsJSON}
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "alert not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "alert operation failed")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func encodeCursor(cursor alertCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("marshal cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeCursor(value string) (alertCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return alertCursor{}, err
	}
	var cursor alertCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.DetectedAt == "" || cursor.AlertID == "" {
		return alertCursor{}, errors.New("malformed cursor")
	}
	return cursor, nil
}
