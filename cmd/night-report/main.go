package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/clinkervision/clinker-vision/internal/logging"
	"github.com/clinkervision/clinker-vision/internal/report"
	"github.com/clinkervision/clinker-vision/internal/store"
)

func main() {
	dbPath := flag.String("db", "", "path to the SQLite database")
	evidenceRoot := flag.String("evidence", "", "path to the evidence root")
	outPath := flag.String("out", "", "output markdown path")
	startRaw := flag.String("start", "", "window start (RFC3339)")
	endRaw := flag.String("end", "", "window end (RFC3339)")
	statsPath := flag.String("stats", "", "optional JSON sidecar unmarshalled into report.WindowStats")
	maxAlerts := flag.Int("max-alerts", 0, "storage cap override (0 means unlimited)")
	flag.Parse()

	logger := logging.NewJSON(os.Stdout, slog.LevelInfo)

	if *dbPath == "" || *evidenceRoot == "" || *outPath == "" {
		logger.Error("night report configuration rejected", "reason", "-db, -evidence, and -out are required")
		os.Exit(1)
	}

	var stats report.WindowStats
	if *statsPath != "" {
		data, err := os.ReadFile(*statsPath)
		if err != nil {
			if !os.IsNotExist(err) {
				logger.Error("night report stats unreadable", "reason", err.Error())
				os.Exit(1)
			}
		} else if err := json.Unmarshal(data, &stats); err != nil {
			logger.Error("night report stats rejected", "reason", err.Error())
			os.Exit(1)
		}
	}

	if *startRaw != "" {
		start, err := parseTime(*startRaw)
		if err != nil {
			logger.Error("night report start rejected", "reason", err.Error())
			os.Exit(1)
		}
		stats.WindowStart = start
	}
	if *endRaw != "" {
		end, err := parseTime(*endRaw)
		if err != nil {
			logger.Error("night report end rejected", "reason", err.Error())
			os.Exit(1)
		}
		stats.WindowEnd = end
	}
	if *maxAlerts != 0 {
		stats.MaxAlerts = *maxAlerts
	}

	if stats.WindowStart.IsZero() || stats.WindowEnd.IsZero() {
		logger.Error("night report window rejected", "reason", "window start and end are required via -start/-end or -stats")
		os.Exit(1)
	}

	ctx := context.Background()
	alertStore, err := store.Open(ctx, *dbPath, *evidenceRoot)
	if err != nil {
		logger.Error("alert store could not start", "component", "store", "reason", err.Error())
		os.Exit(1)
	}
	defer alertStore.Close()

	if err := report.Generate(ctx, alertStore, stats, *outPath); err != nil {
		logger.Error("night report failed", "reason", err.Error())
		os.Exit(1)
	}
	logger.Info("night report written", "out", *outPath)
}

func parseTime(raw string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed, nil
	}
	return time.Parse(time.RFC3339, raw)
}
