package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/clinkervision/clinker-vision/internal/evidence"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type BoundingBox struct {
	X      float32
	Y      float32
	Width  float32
	Height float32
}

type Alert struct {
	AlertID           string
	CapturedAt        time.Time
	DetectedAt        time.Time
	ProcessedAt       *time.Time
	CreatedAt         time.Time
	CameraID          string
	ObservationTarget string
	FaultType         string
	Confidence        float32
	ConfidenceKnown   bool
	FrameID           string
	ModelVersion      string
	BoundingBox       BoundingBox
	EvidenceRef       string
	SeenAt            *time.Time
	GodetID           int32
	AlertState        string
	LoopNo            int32
	RuleID            string
	EventKey          string
	EvidenceFrameID   string
	MeasurementsJSON  string
}

type Store struct {
	db           *sql.DB
	evidenceRoot string
}

type ListFilter struct {
	CameraID         string
	FaultType        string
	UnseenOnly       bool
	Since            *time.Time
	BeforeDetectedAt *time.Time
	BeforeAlertID    string
	Limit            int
}

type ListResult struct {
	Alerts  []Alert
	HasMore bool
}

func Open(ctx context.Context, databasePath, evidenceRoot string) (*Store, error) {
	if databasePath == "" || evidenceRoot == "" {
		return nil, errors.New("database and evidence paths are required")
	}
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	store := &Store{db: db, evidenceRoot: evidenceRoot}
	if err := store.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) initialize(ctx context.Context) error {
	for _, statement := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		createAlertsTable,
		"CREATE INDEX IF NOT EXISTS idx_alerts_detected ON alerts(detected_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_alerts_camera_detected ON alerts(camera_id, detected_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_alerts_seen_detected ON alerts(seen_at, detected_at DESC)",
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize SQLite: %w", err)
		}
	}
	return s.migrateAlerts(ctx)
}

const createAlertsTable = `CREATE TABLE IF NOT EXISTS alerts (
  alert_id TEXT PRIMARY KEY,
  captured_at TEXT NOT NULL,
  detected_at TEXT NOT NULL,
  processed_at TEXT,
  created_at TEXT NOT NULL,
  camera_id TEXT NOT NULL CHECK (camera_id IN ('CAM-1','CAM-2','CAM-3','CAM-4','CAM-5','CAM-6')),
  observation_target TEXT NOT NULL CHECK (observation_target IN ('godet','galet','clinker_level')),
  fault_type TEXT NOT NULL,
  confidence REAL CHECK (confidence >= 0 AND confidence <= 1),
  frame_id TEXT NOT NULL,
  model_version TEXT NOT NULL,
  bbox_x REAL NOT NULL,
  bbox_y REAL NOT NULL,
  bbox_w REAL NOT NULL,
  bbox_h REAL NOT NULL,
  scalar_value REAL,
  evidence_ref TEXT NOT NULL UNIQUE,
  seen_at TEXT,
  godet_id INTEGER,
  alert_state TEXT,
  loop_no INTEGER,
  rule_id TEXT,
  event_key TEXT UNIQUE,
  evidence_frame_id TEXT,
  measurements_json TEXT
)`

// migrateAlerts keeps the existing MVP database usable after the v2 model
// adds nullable confidence and Tier-2 metadata. Older databases had a
// NOT NULL confidence column, so adding columns alone would make a valid
// model alert impossible to store without fabricating a confidence value.
func (s *Store) migrateAlerts(ctx context.Context) error {
	columns, err := s.alertColumns(ctx, "alerts")
	if err != nil {
		return err
	}
	if columns["confidence"] && !columns["confidence_nullable"] {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE alerts RENAME TO alerts_legacy"); err != nil {
			return fmt.Errorf("prepare alerts migration: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, createAlertsTable); err != nil {
			return fmt.Errorf("create migrated alerts table: %w", err)
		}
		const copyAlerts = `INSERT INTO alerts
  (alert_id, captured_at, detected_at, processed_at, created_at, camera_id,
   observation_target, fault_type, confidence, frame_id, model_version,
   bbox_x, bbox_y, bbox_w, bbox_h, scalar_value, evidence_ref, seen_at)
  SELECT alert_id, captured_at, detected_at, processed_at, created_at, camera_id,
   observation_target, fault_type, confidence, frame_id, model_version,
   bbox_x, bbox_y, bbox_w, bbox_h, scalar_value, evidence_ref, seen_at
  FROM alerts_legacy`
		if _, err := s.db.ExecContext(ctx, copyAlerts); err != nil {
			return fmt.Errorf("copy alerts during migration: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, "DROP TABLE alerts_legacy"); err != nil {
			return fmt.Errorf("finish alerts migration: %w", err)
		}
		return nil
	}

	for name, definition := range map[string]string{
		"godet_id":          "INTEGER",
		"alert_state":       "TEXT",
		"loop_no":           "INTEGER",
		"rule_id":           "TEXT",
		"event_key":         "TEXT",
		"evidence_frame_id": "TEXT",
		"measurements_json": "TEXT",
	} {
		if columns[name] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE alerts ADD COLUMN "+name+" "+definition); err != nil {
			return fmt.Errorf("add alerts.%s: %w", name, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, "CREATE UNIQUE INDEX IF NOT EXISTS idx_alerts_event_key ON alerts(event_key) WHERE event_key IS NOT NULL"); err != nil {
		return fmt.Errorf("create event key index: %w", err)
	}
	return nil
}

// alertColumns returns both presence and nullability for the one column whose
// nullability changes across the v1/v2 store contract.
func (s *Store) alertColumns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, fmt.Errorf("inspect alerts schema: %w", err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("read alerts schema: %w", err)
		}
		columns[name] = true
		if name == "confidence" && notNull == 0 {
			columns["confidence_nullable"] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate alerts schema: %w", err)
	}
	return columns, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) InsertAlert(ctx context.Context, alert Alert) error {
	if err := s.validateAlert(alert); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO alerts
  (alert_id, captured_at, detected_at, processed_at, created_at, camera_id,
   observation_target, fault_type, confidence, frame_id, model_version,
	   bbox_x, bbox_y, bbox_w, bbox_h, evidence_ref, seen_at, godet_id,
   alert_state, loop_no, rule_id, event_key, evidence_frame_id, measurements_json)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		alert.AlertID,
		formatTime(alert.CapturedAt),
		formatTime(alert.DetectedAt),
		formatOptionalTime(alert.ProcessedAt),
		formatTime(alert.CreatedAt),
		alert.CameraID,
		alert.ObservationTarget,
		alert.FaultType,
		confidenceValue(alert),
		alert.FrameID,
		alert.ModelVersion,
		alert.BoundingBox.X,
		alert.BoundingBox.Y,
		alert.BoundingBox.Width,
		alert.BoundingBox.Height,
		alert.EvidenceRef,
		formatOptionalTime(alert.SeenAt),
		optionalInt(alert.GodetID), alert.AlertState, optionalInt(alert.LoopNo), optionalString(alert.RuleID), optionalString(alert.EventKey), optionalString(alert.EvidenceFrameID), optionalString(alert.MeasurementsJSON),
	)
	if err != nil {
		return fmt.Errorf("insert alert: %w", err)
	}
	return nil
}

// UpsertGodetAlert stores a Tier-2 event. event_key is the stable deduplication
// key supplied/derived from the model, so pending-to-confirmed updates do not
// create duplicate operator alerts.
func (s *Store) UpsertGodetAlert(ctx context.Context, alert Alert) error {
	if alert.EventKey == "" {
		return errors.New("event_key is required for a godet alert")
	}
	if err := s.validateAlert(alert); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO alerts
  (alert_id, captured_at, detected_at, processed_at, created_at, camera_id,
   observation_target, fault_type, confidence, frame_id, model_version,
   bbox_x, bbox_y, bbox_w, bbox_h, evidence_ref, seen_at, godet_id,
   alert_state, loop_no, rule_id, event_key, evidence_frame_id, measurements_json)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
  ON CONFLICT(event_key) DO UPDATE SET
   captured_at=excluded.captured_at, detected_at=excluded.detected_at,
   processed_at=excluded.processed_at, camera_id=excluded.camera_id,
   observation_target=excluded.observation_target, fault_type=excluded.fault_type,
   confidence=excluded.confidence, frame_id=excluded.frame_id,
   model_version=excluded.model_version, bbox_x=excluded.bbox_x,
   bbox_y=excluded.bbox_y, bbox_w=excluded.bbox_w, bbox_h=excluded.bbox_h,
   evidence_ref=excluded.evidence_ref, godet_id=excluded.godet_id,
   alert_state=excluded.alert_state, loop_no=excluded.loop_no,
   rule_id=excluded.rule_id, evidence_frame_id=excluded.evidence_frame_id,
   measurements_json=excluded.measurements_json`,
		alert.AlertID, formatTime(alert.CapturedAt), formatTime(alert.DetectedAt), formatOptionalTime(alert.ProcessedAt), formatTime(alert.CreatedAt), alert.CameraID, alert.ObservationTarget, alert.FaultType, confidenceValue(alert), alert.FrameID, alert.ModelVersion, alert.BoundingBox.X, alert.BoundingBox.Y, alert.BoundingBox.Width, alert.BoundingBox.Height, alert.EvidenceRef, formatOptionalTime(alert.SeenAt), optionalInt(alert.GodetID), alert.AlertState, alert.LoopNo, optionalString(alert.RuleID), alert.EventKey, optionalString(alert.EvidenceFrameID), optionalString(alert.MeasurementsJSON))
	if err != nil {
		return fmt.Errorf("upsert godet alert: %w", err)
	}
	return nil
}

func (s *Store) GetAlert(ctx context.Context, alertID string) (Alert, error) {
	row := s.db.QueryRowContext(ctx, alertQuery+" WHERE alert_id = ?", alertID)
	return scanAlert(row)
}

func (s *Store) ListAlerts(ctx context.Context, filter ListFilter) (ListResult, error) {
	if filter.Limit < 1 || filter.Limit > 200 {
		return ListResult{}, errors.New("alert limit must be between 1 and 200")
	}
	query := alertQuery + " WHERE 1=1"
	args := make([]any, 0, 8)
	if filter.CameraID != "" {
		query += " AND camera_id = ?"
		args = append(args, filter.CameraID)
	}
	if filter.FaultType != "" {
		query += " AND fault_type = ?"
		args = append(args, filter.FaultType)
	}
	if filter.UnseenOnly {
		query += " AND seen_at IS NULL"
	}
	if filter.Since != nil {
		query += " AND detected_at >= ?"
		args = append(args, formatTime(*filter.Since))
	}
	if filter.BeforeDetectedAt != nil && filter.BeforeAlertID != "" {
		query += " AND (detected_at < ? OR (detected_at = ? AND alert_id < ?))"
		args = append(args, formatTime(*filter.BeforeDetectedAt), formatTime(*filter.BeforeDetectedAt), filter.BeforeAlertID)
	}
	query += " ORDER BY detected_at DESC, alert_id DESC LIMIT ?"
	args = append(args, filter.Limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return ListResult{}, fmt.Errorf("list alerts: %w", err)
	}
	defer rows.Close()
	result := ListResult{Alerts: make([]Alert, 0, filter.Limit)}
	for rows.Next() {
		alert, err := scanAlert(rows)
		if err != nil {
			return ListResult{}, fmt.Errorf("read listed alert: %w", err)
		}
		if len(result.Alerts) == filter.Limit {
			result.HasMore = true
			break
		}
		result.Alerts = append(result.Alerts, alert)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, fmt.Errorf("iterate listed alerts: %w", err)
	}
	return result, nil
}

func (s *Store) Count(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM alerts").Scan(&count); err != nil {
		return 0, fmt.Errorf("count alerts: %w", err)
	}
	return count, nil
}

func (s *Store) HasEventKey(ctx context.Context, key string) (bool, error) {
	if key == "" {
		return false, nil
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT 1 FROM alerts WHERE event_key = ? LIMIT 1", key).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("has event key: %w", err)
	}
	return true, nil
}

func (s *Store) MarkSeen(ctx context.Context, alertID string, seenAt time.Time) (*time.Time, error) {
	if _, err := s.db.ExecContext(ctx, "UPDATE alerts SET seen_at = COALESCE(seen_at, ?) WHERE alert_id = ?", formatTime(seenAt), alertID); err != nil {
		return nil, fmt.Errorf("mark alert seen: %w", err)
	}
	var value sql.NullString
	if err := s.db.QueryRowContext(ctx, "SELECT seen_at FROM alerts WHERE alert_id = ?", alertID).Scan(&value); err != nil {
		return nil, fmt.Errorf("read seen alert: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, fmt.Errorf("parse seen_at: %w", err)
	}
	return &parsed, nil
}

func (s *Store) CleanupBefore(ctx context.Context, cutoff time.Time) (int, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT alert_id, evidence_ref FROM alerts WHERE detected_at < ?", formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("find expired alerts: %w", err)
	}
	var expired []struct {
		id  string
		ref string
	}
	for rows.Next() {
		var item struct {
			id  string
			ref string
		}
		if err := rows.Scan(&item.id, &item.ref); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("read expired alert: %w", err)
		}
		if _, err := evidence.SafePath(s.evidenceRoot, item.ref); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("expired evidence path: %w", err)
		}
		expired = append(expired, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("iterate expired alerts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close expired alerts: %w", err)
	}
	for _, item := range expired {
		if err := evidence.Remove(s.evidenceRoot, item.ref); err != nil {
			return 0, err
		}
	}
	result, err := s.db.ExecContext(ctx, "DELETE FROM alerts WHERE detected_at < ?", formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("delete expired alerts: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted alerts: %w", err)
	}
	return int(deleted), nil
}

func (s *Store) validateAlert(alert Alert) error {
	if _, err := uuid.Parse(alert.AlertID); err != nil {
		return fmt.Errorf("alert_id must be a UUID: %w", err)
	}
	if _, err := uuid.Parse(alert.FrameID); err != nil {
		return errors.New("frame_id must be a UUID")
	}
	if alert.CapturedAt.IsZero() || alert.DetectedAt.IsZero() || alert.CreatedAt.IsZero() {
		return errors.New("captured_at, detected_at, and created_at are required")
	}
	if alert.CameraID != "CAM-1" && alert.CameraID != "CAM-2" && alert.CameraID != "CAM-3" && alert.CameraID != "CAM-4" && alert.CameraID != "CAM-5" && alert.CameraID != "CAM-6" {
		return errors.New("invalid camera_id")
	}
	if alert.ObservationTarget != "godet" && alert.ObservationTarget != "galet" && alert.ObservationTarget != "clinker_level" {
		return errors.New("invalid observation_target")
	}
	if alert.FaultType == "" || alert.ModelVersion == "" {
		return errors.New("fault_type and model_version are required")
	}
	if alert.Confidence < 0 || alert.Confidence > 1 {
		return errors.New("confidence must be between 0 and 1")
	}
	if alert.BoundingBox.X < 0 || alert.BoundingBox.X > 1 || alert.BoundingBox.Y < 0 || alert.BoundingBox.Y > 1 || alert.BoundingBox.Width <= 0 || alert.BoundingBox.Height <= 0 || alert.BoundingBox.X+alert.BoundingBox.Width > 1 || alert.BoundingBox.Y+alert.BoundingBox.Height > 1 {
		return errors.New("bounding box must be normalized and within the frame")
	}
	if _, err := evidence.SafePath(s.evidenceRoot, alert.EvidenceRef); err != nil {
		return fmt.Errorf("evidence_ref: %w", err)
	}
	return nil
}

const alertQuery = `SELECT alert_id, captured_at, detected_at, processed_at,
  created_at, camera_id, observation_target, fault_type, confidence, frame_id,
  model_version, bbox_x, bbox_y, bbox_w, bbox_h, evidence_ref, seen_at,
  godet_id, alert_state, loop_no, rule_id, event_key, evidence_frame_id, measurements_json
  FROM alerts`

type scanner interface{ Scan(...any) error }

func scanAlert(row scanner) (Alert, error) {
	var alert Alert
	var capturedAt, detectedAt, createdAt string
	var processedAt, seenAt, alertState, ruleID, eventKey, evidenceFrameID, measurementsJSON sql.NullString
	var confidence sql.NullFloat64
	var godetID, loopNo sql.NullInt64
	if err := row.Scan(&alert.AlertID, &capturedAt, &detectedAt, &processedAt, &createdAt, &alert.CameraID, &alert.ObservationTarget, &alert.FaultType, &confidence, &alert.FrameID, &alert.ModelVersion, &alert.BoundingBox.X, &alert.BoundingBox.Y, &alert.BoundingBox.Width, &alert.BoundingBox.Height, &alert.EvidenceRef, &seenAt, &godetID, &alertState, &loopNo, &ruleID, &eventKey, &evidenceFrameID, &measurementsJSON); err != nil {
		return Alert{}, err
	}
	if confidence.Valid {
		alert.Confidence = float32(confidence.Float64)
		alert.ConfidenceKnown = true
	}
	if godetID.Valid {
		alert.GodetID = int32(godetID.Int64)
	}
	if loopNo.Valid {
		alert.LoopNo = int32(loopNo.Int64)
	}
	alert.AlertState, alert.RuleID, alert.EventKey, alert.EvidenceFrameID, alert.MeasurementsJSON = alertState.String, ruleID.String, eventKey.String, evidenceFrameID.String, measurementsJSON.String
	var err error
	if alert.CapturedAt, err = time.Parse(time.RFC3339Nano, capturedAt); err != nil {
		return Alert{}, err
	}
	if alert.DetectedAt, err = time.Parse(time.RFC3339Nano, detectedAt); err != nil {
		return Alert{}, err
	}
	if alert.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return Alert{}, err
	}
	if processedAt.Valid {
		parsed, err := time.Parse(time.RFC3339Nano, processedAt.String)
		if err != nil {
			return Alert{}, err
		}
		alert.ProcessedAt = &parsed
	}
	if seenAt.Valid {
		parsed, err := time.Parse(time.RFC3339Nano, seenAt.String)
		if err != nil {
			return Alert{}, err
		}
		alert.SeenAt = &parsed
	}
	return alert, nil
}

func formatTime(value time.Time) string { return value.Format(time.RFC3339Nano) }

func formatOptionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func confidenceValue(alert Alert) any {
	if alert.ConfidenceKnown || alert.Confidence != 0 {
		return alert.Confidence
	}
	return nil
}

func optionalInt(value int32) any {
	if value == 0 {
		return nil
	}
	return value
}
func optionalString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
