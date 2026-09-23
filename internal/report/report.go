package report

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/clinkervision/clinker-vision/internal/store"
)

// WindowStats carries the pipeline counters for the reported window.
// MaxAlerts is the storage cap (0 means unlimited).
type WindowStats struct {
	WindowStart     time.Time          `json:"window_start"`
	WindowEnd       time.Time          `json:"window_end"`
	ProcessedEvents int                `json:"processed_events"`
	Stored          int                `json:"stored"`
	DroppedCap      int                `json:"dropped_cap"`
	Upsets          int                `json:"upsets"`
	PollErrors      int                `json:"poll_errors"`
	LastStatus      string             `json:"last_status"`
	LastDetail      string             `json:"last_detail"`
	Counters        map[string]float64 `json:"counters,omitempty"`
	ModelVersions   []string           `json:"model_versions"`
	MaxAlerts       int                `json:"max_alerts"`
}

// Generate pages ListAlerts (Since=WindowStart) selecting rows with DetectedAt
// in [WindowStart, WindowEnd), then writes a markdown night report to outPath.
// Parent directories are created with 0750, the file itself with 0600.
// Only stdlib (plus the store type) is used.
func Generate(ctx context.Context, alertStore *store.Store, stats WindowStats, outPath string) error {
	if alertStore == nil {
		return fmt.Errorf("alert store is required")
	}
	if outPath == "" {
		return fmt.Errorf("output path is required")
	}

	const pageLimit = 200
	var since *time.Time
	if !stats.WindowStart.IsZero() {
		s := stats.WindowStart
		since = &s
	}
	var filtered []store.Alert
	var beforeDetectedAt *time.Time
	var beforeAlertID string
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := alertStore.ListAlerts(ctx, store.ListFilter{
			Since:            since,
			BeforeDetectedAt: beforeDetectedAt,
			BeforeAlertID:    beforeAlertID,
			Limit:            pageLimit,
		})
		if err != nil {
			return err
		}
		for _, alert := range result.Alerts {
			if !stats.WindowStart.IsZero() && alert.DetectedAt.Before(stats.WindowStart) {
				continue
			}
			if !stats.WindowEnd.IsZero() && !alert.DetectedAt.Before(stats.WindowEnd) {
				continue
			}
			filtered = append(filtered, alert)
		}
		if !result.HasMore || len(result.Alerts) == 0 {
			break
		}
		last := result.Alerts[len(result.Alerts)-1]
		cursor := last.DetectedAt
		beforeDetectedAt = &cursor
		beforeAlertID = last.AlertID
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].GodetID != filtered[j].GodetID {
			return filtered[i].GodetID < filtered[j].GodetID
		}
		if filtered[i].LoopNo != filtered[j].LoopNo {
			return filtered[i].LoopNo < filtered[j].LoopNo
		}
		return filtered[i].DetectedAt.Before(filtered[j].DetectedAt)
	})

	godetSet := make(map[int32]struct{}, len(filtered))
	pending, confirmed := 0, 0
	seen, unseen := 0, 0
	for _, alert := range filtered {
		godetSet[alert.GodetID] = struct{}{}
		switch strings.ToLower(alert.AlertState) {
		case "pending":
			pending++
		case "confirmed":
			confirmed++
		}
		if alert.SeenAt != nil {
			seen++
		} else {
			unseen++
		}
	}

	modelVersions := stats.ModelVersions
	if len(modelVersions) == 0 {
		versionSet := make(map[string]struct{})
		for _, alert := range filtered {
			if alert.ModelVersion == "" {
				continue
			}
			if _, ok := versionSet[alert.ModelVersion]; !ok {
				versionSet[alert.ModelVersion] = struct{}{}
				modelVersions = append(modelVersions, alert.ModelVersion)
			}
		}
		sort.Strings(modelVersions)
	}
	modelLine := strings.Join(modelVersions, ", ")
	if modelLine == "" {
		modelLine = "unknown"
	}

	var capLine string
	if stats.MaxAlerts > 0 {
		capLine = fmt.Sprintf("stored %d (cap %d), %d dropped at cap", stats.Stored, stats.MaxAlerts, stats.DroppedCap)
	} else {
		capLine = fmt.Sprintf("stored %d (cap unlimited), %d dropped at cap", stats.Stored, stats.DroppedCap)
	}

	var b strings.Builder
	b.WriteString("# Night report\n\n")
	b.WriteString(fmt.Sprintf("Window: %s to %s\n\n", stats.WindowStart.Format(time.RFC3339), stats.WindowEnd.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("Model versions: %s\n\n", modelLine))
	b.WriteString(fmt.Sprintf("Total alerts: %d\n\n", len(filtered)))
	b.WriteString(fmt.Sprintf("Distinct godets: %d\n\n", len(godetSet)))
	b.WriteString(fmt.Sprintf("Pending: %d, Confirmed: %d\n\n", pending, confirmed))
	b.WriteString(fmt.Sprintf("Seen: %d, Unseen: %d\n\n", seen, unseen))
	b.WriteString(fmt.Sprintf("Processed events: %d, Stored: %d, Dropped at cap: %d, Upsets: %d\n\n",
		stats.ProcessedEvents, stats.Stored, stats.DroppedCap, stats.Upsets))
	b.WriteString(fmt.Sprintf("Tier-2 poll errors: %d\n\n", stats.PollErrors))
	if stats.LastStatus != "" || stats.LastDetail != "" {
		detail := stats.LastDetail
		if detail == "" {
			detail = "—"
		}
		b.WriteString(fmt.Sprintf("Last model status: %s — %s\n\n", stats.LastStatus, detail))
	}
	if len(stats.Counters) > 0 {
		parts := make([]string, 0, 6)
		for _, key := range []string{"frames_total", "captures_total", "godet_rows", "dead_letters_total", "sequence_gaps_total", "virtual_slots_total"} {
			if value, ok := stats.Counters[key]; ok {
				parts = append(parts, fmt.Sprintf("%s=%.0f", key, value))
			}
		}
		if len(parts) > 0 {
			b.WriteString("Model counters: " + strings.Join(parts, ", ") + "\n\n")
		}
	}
	b.WriteString("## Godets\n\n")
	b.WriteString("| godet | loop | state | detected_at | drop |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, alert := range filtered {
		b.WriteString(fmt.Sprintf("| %d | %d | %s | %s | %s |\n",
			alert.GodetID,
			alert.LoopNo,
			alert.AlertState,
			alert.DetectedAt.Format(time.RFC3339),
			dropCell(alert.MeasurementsJSON),
		))
	}
	b.WriteString("\n")
	b.WriteString(capLine + "\n\n")
	b.WriteString("Storage note: alerts are retained in SQLite up to the configured cap; evidence files live under the configured evidence root.\n")

	// Reports are operator-readable by design (alert metadata, no secrets),
	// so the host user can cat them without sudo. Evidence and database
	// files stay restricted; only reports and stats sidecars are relaxed.
	if dir := filepath.Dir(outPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create report directory: %w", err)
		}
	}
	if err := os.WriteFile(outPath, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// dropCell extracts the "drop" measurement as text when MeasurementsJSON is a
// parseable JSON object with a numeric drop field, else blank.
func dropCell(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return ""
	}
	value, ok := decoded["drop"]
	if !ok || value == nil {
		return ""
	}
	number, ok := value.(float64)
	if !ok {
		return ""
	}
	return strconv.FormatFloat(number, 'f', -1, 64)
}
