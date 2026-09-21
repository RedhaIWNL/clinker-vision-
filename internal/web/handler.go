package web

import (
	"net/http"

	"github.com/clinkervision/clinker-vision/internal/health"
	"github.com/clinkervision/clinker-vision/internal/metrics"
)

func NewHandler(healthHandler *health.Handler, metricHandler *metrics.Metrics, api *API, status *StatusStore) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", serveIndex)
	mux.HandleFunc("GET /health/live", healthHandler.Live)
	mux.HandleFunc("GET /health/ready", healthHandler.Ready)
	mux.HandleFunc("GET /metrics", metricHandler.Handler)
	mux.HandleFunc("GET /api/v1/status", status.ServeStatus)
	if api != nil {
		mux.HandleFunc("GET /api/v1/alerts", api.ListAlerts)
		mux.HandleFunc("GET /api/v1/alerts/{alert_id}", api.GetAlert)
		mux.HandleFunc("GET /api/v1/alerts/{alert_id}/evidence", api.ServeEvidence)
		mux.HandleFunc("POST /api/v1/alerts/{alert_id}/seen", api.MarkSeen)
	}
	return mux
}

func NewHandlerWithAPI(healthHandler *health.Handler, metricHandler *metrics.Metrics, api *API) http.Handler {
	return NewHandler(healthHandler, metricHandler, api, nil)
}
