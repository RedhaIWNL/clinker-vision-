package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/clinkervision/clinker-vision/internal/alerting"
	"github.com/clinkervision/clinker-vision/internal/config"
	"github.com/clinkervision/clinker-vision/internal/health"
	"github.com/clinkervision/clinker-vision/internal/inference"
	inferencev2 "github.com/clinkervision/clinker-vision/internal/inference/gen/v2"
	"github.com/clinkervision/clinker-vision/internal/ingest"
	"github.com/clinkervision/clinker-vision/internal/logging"
	"github.com/clinkervision/clinker-vision/internal/metrics"
	framequeue "github.com/clinkervision/clinker-vision/internal/queue"
	"github.com/clinkervision/clinker-vision/internal/report"
	"github.com/clinkervision/clinker-vision/internal/store"
	"github.com/clinkervision/clinker-vision/internal/web"
)

type cameraRuntime struct {
	camera        config.CameraConfig
	queue         *framequeue.FrameQueue
	mu            sync.RWMutex
	latest        ingest.Frame
	evidenceMu    sync.Mutex
	evidence      map[string]ingest.Frame
	evidenceOrder []string
}

var errDependencyTimeout = errors.New("dependency unavailable past configured timeout")

func (r *cameraRuntime) setLatest(frame ingest.Frame) {
	r.mu.Lock()
	r.latest = frame
	r.mu.Unlock()
}

func (r *cameraRuntime) latestFrame() ingest.Frame {
	r.mu.RLock()
	defer r.mu.RUnlock()
	frame := r.latest
	frame.ImageData = append([]byte(nil), frame.ImageData...)
	return frame
}

// Candidate frames are sparse: the model only returns a fixed-ROI detection
// when a Tier-1 frame is relevant to a possible damage event. Retain those
// frames so a loop-delayed Tier-2 event can point back to the actual candidate
// instead of forcing the operator evidence to use an unrelated live frame.
func (r *cameraRuntime) cacheEvidence(frame ingest.Frame) {
	if frame.FrameID == "" || len(frame.ImageData) == 0 {
		return
	}
	r.evidenceMu.Lock()
	defer r.evidenceMu.Unlock()
	if r.evidence == nil {
		r.evidence = make(map[string]ingest.Frame)
	}
	if _, exists := r.evidence[frame.FrameID]; exists {
		return
	}
	frame.ImageData = append([]byte(nil), frame.ImageData...)
	r.evidence[frame.FrameID] = frame
	r.evidenceOrder = append(r.evidenceOrder, frame.FrameID)
	const maxCandidateFrames = 512
	if len(r.evidenceOrder) > maxCandidateFrames {
		oldest := r.evidenceOrder[0]
		r.evidenceOrder = r.evidenceOrder[1:]
		delete(r.evidence, oldest)
	}
}

func (r *cameraRuntime) evidenceFrame(frameID string) ingest.Frame {
	r.evidenceMu.Lock()
	defer r.evidenceMu.Unlock()
	frame, ok := r.evidence[frameID]
	if !ok {
		return ingest.Frame{}
	}
	frame.ImageData = append([]byte(nil), frame.ImageData...)
	return frame
}

// windowTracker accumulates per-window Tier-2 counters for the dawn report
// and the storage-cap gate. Safe for concurrent use by the poll loops.
type windowTracker struct {
	mu          sync.Mutex
	start       time.Time
	processed   int
	stored      int
	upserts     int
	droppedCap  int
	storedTotal int
	capReached  bool
	versions    map[string]struct{}
}

func newWindowTracker() *windowTracker {
	return &windowTracker{versions: make(map[string]struct{})}
}

func (t *windowTracker) reset(start time.Time, storedTotal int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.start = start
	t.processed, t.stored, t.upserts, t.droppedCap = 0, 0, 0, 0
	t.storedTotal = storedTotal
	t.capReached = false
	t.versions = make(map[string]struct{})
}

func (t *windowTracker) started() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.start.IsZero()
}

func (t *windowTracker) noteProcessed() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.processed++
}

func (t *windowTracker) noteStored(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stored += n
	t.storedTotal += n
}

func (t *windowTracker) noteUpserts(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.upserts += n
}

func (t *windowTracker) noteDropped() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.droppedCap++
	t.capReached = true
}

func (t *windowTracker) noteVersion(version string) {
	if version == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.versions[version] = struct{}{}
}

type windowSnapshot struct {
	start       time.Time
	processed   int
	stored      int
	upserts     int
	droppedCap  int
	storedTotal int
	capReached  bool
	versions    []string
}

func (t *windowTracker) snapshot() windowSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	versions := make([]string, 0, len(t.versions))
	for v := range t.versions {
		versions = append(versions, v)
	}
	sortStrings(versions)
	return windowSnapshot{
		start: t.start, processed: t.processed, stored: t.stored,
		upserts: t.upserts, droppedCap: t.droppedCap, storedTotal: t.storedTotal,
		capReached: t.capReached, versions: versions,
	}
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// laneEnv bundles the dependencies a detection lane needs so runWindow can
// build, run, and tear down lanes repeatedly across operating windows.
type laneEnv struct {
	cfg            config.Config
	logger         *slog.Logger
	health         *health.Handler
	metrics        *metrics.Metrics
	status         *web.StatusStore
	store          *store.Store
	model          *inference.Client
	loc            *time.Location
	reportFatal    func(error)
	tracker        *windowTracker
	dependencyTO   time.Duration
	requestTimeout time.Duration
	statePoll      time.Duration
}

func (e *laneEnv) maxAlerts() int {
	return e.cfg.Retention.MaxAlerts
}

// gateStorage enforces the stop-at-cap rule: already-known event keys
// (pending-to-confirmed upserts) always pass since they do not grow storage;
// new keys pass only while the stored total is below the cap (cap <= 0 means
// unlimited). Returns (allow, upsert).
func (e *laneEnv) gateStorage(ctx context.Context, seenState, eventKey string) (bool, bool, error) {
	if e.maxAlerts() <= 0 {
		return true, seenState != "", nil
	}
	if seenState != "" {
		return true, true, nil
	}
	known, err := e.store.HasEventKey(ctx, eventKey)
	if err != nil {
		return false, false, err
	}
	if known {
		return true, true, nil
	}
	total, err := e.store.Count(ctx)
	if err != nil {
		return false, false, err
	}
	if total >= e.maxAlerts() {
		return false, false, nil
	}
	return true, false, nil
}

// nightReportPaths derives the stats sidecar and markdown report paths from
// the SQLite location: <dbdir>/night-<date>.stats.json and
// <dbdir>/reports/night-<date>.md.
func nightReportPaths(sqlitePath string, windowStart time.Time) (statsPath, reportPath string) {
	dir := filepath.Dir(sqlitePath)
	day := windowStart.Format("2006-01-02")
	return filepath.Join(dir, "night-"+day+".stats.json"),
		filepath.Join(dir, "reports", "night-"+day+".md")
}

func sleepOrDone(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func inferWithRetry(ctx context.Context, client *inference.Client, frame ingest.Frame, timeout, dependencyTimeout time.Duration, logger *slog.Logger) (*inferencev2.InferenceResponse, error) {
	backoff := 100 * time.Millisecond
	failedSince := time.Time{}
	for {
		requestCtx, cancel := context.WithTimeout(ctx, timeout)
		response, err := client.Infer(requestCtx, frame)
		cancel()
		if err == nil {
			return response, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, inference.ErrInvalidRequest) || !inference.IsRetryableTransportError(err) {
			return nil, err
		}
		if failedSince.IsZero() {
			failedSince = time.Now()
		}
		if dependencyTimeout > 0 && time.Since(failedSince) >= dependencyTimeout {
			return nil, fmt.Errorf("%w: model inference", errDependencyTimeout)
		}
		logger.Warn("inference stream unavailable; retrying frame", "component", "inference", "frame_id", frame.FrameID, "reason", err.Error())
		if reconnectErr := client.Reconnect(ctx); reconnectErr != nil {
			logger.Warn("inference reconnect failed", "component", "inference", "reason", reconnectErr.Error())
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		if backoff < 2*time.Second {
			backoff *= 2
		}
	}
}

func dialModelWithRetry(ctx context.Context, address string, dependencyTimeout time.Duration, logger *slog.Logger) (*inference.Client, error) {
	backoff := time.Second
	deadline := time.Now().Add(dependencyTimeout)
	for {
		client, err := inference.Dial(ctx, address)
		if err == nil {
			return client, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if dependencyTimeout > 0 && time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: model dial %s: %v", errDependencyTimeout, address, err)
		}
		logger.Warn("model client could not start; retrying", "component", "inference", "reason", err.Error())
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
}

func (e *laneEnv) supervise(parent context.Context) {
	if !e.cfg.Schedule.Enabled {
		e.tracker.reset(time.Now(), 0)
		e.runWindow(parent, false)
		return
	}
	idleLogged := false
	for parent.Err() == nil {
		now := time.Now().In(e.loc)
		if !e.cfg.Schedule.Contains(now) {
			e.health.SetReady(false)
			detail := fmt.Sprintf("outside operating window %s-%s %s",
				e.cfg.Schedule.Start, e.cfg.Schedule.Stop, e.cfg.Schedule.Timezone)
			e.status.Update(func(s *web.ModelStatus) {
				s.Ready = false
				s.LoopLocked = false
				s.Status = "standby"
				s.Detail = detail
			})
			if !idleLogged {
				e.logger.Info("lanes idle outside operating window", "component", "pipeline", "detail", detail)
				idleLogged = true
			}
			if !sleepOrDone(parent, 30*time.Second) {
				return
			}
			continue
		}
		idleLogged = false
		e.runWindow(parent, true)
	}
}

// runWindow builds fresh lanes, runs them until parent is done (or, for a
// scheduled window, until the window closes), then tears them down. When the
// window was scheduled, a stats sidecar and markdown report are written.
func (e *laneEnv) runWindow(parent context.Context, scheduled bool) {
	laneCtx, laneCancel := context.WithCancel(parent)
	defer laneCancel()
	runtimes := make([]*cameraRuntime, 0, len(e.cfg.Cameras))
	for _, camera := range e.cfg.Cameras {
		if !camera.Enabled {
			continue
		}
		queue, err := framequeue.NewFrameQueue(camera.QueueCapacity)
		if err != nil {
			e.logger.Error("camera queue could not start", "component", "queue", "camera_id", camera.ID, "reason", err.Error())
			e.reportFatal(fmt.Errorf("camera queue %s: %w", camera.ID, err))
			return
		}
		runtimes = append(runtimes, &cameraRuntime{camera: camera, queue: queue, evidence: make(map[string]ingest.Frame)})
	}
	if len(runtimes) == 0 {
		e.logger.Error("no enabled camera configured", "component", "pipeline")
		e.reportFatal(errors.New("no enabled camera configured"))
		return
	}
	storedTotal := 0
	if n, err := e.store.Count(parent); err != nil {
		e.logger.Error("storage count failed", "component", "store", "reason", err.Error())
	} else {
		storedTotal = n
	}
	e.tracker.reset(time.Now(), storedTotal)
	e.logger.Info("lanes starting", "component", "pipeline",
		"camera_count", len(runtimes), "stored_total", storedTotal, "max_alerts", e.maxAlerts())
	var runtimeWG sync.WaitGroup
	for _, runtime := range runtimes {
		runtime := runtime
		runtimeWG.Add(3)
		go func() {
			defer runtimeWG.Done()
			defer runtime.queue.Close()
			decoder := ingest.Decoder{CameraID: runtime.camera.ID, Source: runtime.camera.NVRRTSPURL}
			runErr := decoder.Run(laneCtx, func(frame ingest.Frame) error {
				e.metrics.FramesDecoded.Add(1)
				e.metrics.FramesSampled.Add(1) // retained metric name; dense lane sends every frame
				runtime.setLatest(frame)
				if err := runtime.queue.PushWait(laneCtx, frame); err != nil {
					return err
				}
				return nil
			})
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				e.metrics.DecodeFailures.Add(1)
				e.logger.Error("camera ingestion stopped", "component", "ingest", "camera_id", runtime.camera.ID, "reason", runErr.Error())
				timer := time.NewTimer(e.dependencyTO)
				select {
				case <-laneCtx.Done():
					timer.Stop()
				case <-timer.C:
					e.reportFatal(fmt.Errorf("%w: camera %s: %v", errDependencyTimeout, runtime.camera.ID, runErr))
				}
			}
		}()

		go func() {
			defer runtimeWG.Done()
			for {
				frame, err := runtime.queue.Pop(laneCtx)
				if err != nil {
					if !errors.Is(err, context.Canceled) && !errors.Is(err, framequeue.ErrClosed) {
						e.logger.Error("camera queue stopped", "component", "queue", "camera_id", runtime.camera.ID, "reason", err.Error())
					}
					return
				}
				e.metrics.InferenceRequests.Add(1)
				response, inferenceErr := inferWithRetry(laneCtx, e.model, frame, e.requestTimeout, e.dependencyTO, e.logger)
				if inferenceErr != nil {
					if errors.Is(inferenceErr, errDependencyTimeout) {
						e.reportFatal(inferenceErr)
						return
					}
					e.metrics.DependencyFailures.Add(1)
					if !errors.Is(inferenceErr, context.Canceled) {
						e.logger.Error("inference failed", "component", "inference", "camera_id", frame.CameraID, "frame_id", frame.FrameID, "sequence_no", frame.SequenceNo, "reason", inferenceErr.Error())
					}
				} else if len(response.GetDetections()) > 0 {
					runtime.cacheEvidence(frame)
				}
			}
		}()

		go func() {
			defer runtimeWG.Done()
			seen := make(map[string]string)
			var stateFailureSince time.Time
			poll := func() {
				ctx, cancel := context.WithTimeout(laneCtx, e.requestTimeout)
				state, err := e.model.GetGodetState(ctx, nil)
				cancel()
				polledAt := time.Now().UTC()
				if err != nil {
					e.health.SetReady(false)
					e.metrics.DependencyFailures.Add(1)
					e.logger.Warn("godet state poll failed", "component", "alerting", "reason", err.Error())
					e.status.Update(func(s *web.ModelStatus) {
						at := polledAt
						s.Ready = false
						s.LastPollAt = &at
						s.LastError = err.Error()
					})
					if stateFailureSince.IsZero() {
						stateFailureSince = time.Now()
					}
					if e.dependencyTO > 0 && time.Since(stateFailureSince) >= e.dependencyTO {
						e.reportFatal(fmt.Errorf("%w: godet state: %v", errDependencyTimeout, err))
					}
					return
				}
				stateFailureSince = time.Time{}
				e.health.SetReady(state.GetHealth().GetReady())
				e.tracker.noteVersion(state.GetModelVersion())
				counters := make(map[string]float64, len(state.GetHealth().GetCounters()))
				for k, v := range state.GetHealth().GetCounters() {
					counters[k] = v
				}
				snap := e.tracker.snapshot()
				e.status.Update(func(s *web.ModelStatus) {
					at := polledAt
					s.Ready = state.GetHealth().GetReady()
					s.LoopLocked = state.GetHealth().GetLoopLocked()
					s.Status = state.GetHealth().GetStatus()
					s.Detail = state.GetHealth().GetDetail()
					s.ModelVersion = state.GetModelVersion()
					s.Counters = counters
					s.StoredAlerts = snap.storedTotal
					s.MaxAlerts = e.maxAlerts()
					if snap.capReached {
						if s.Detail == "" {
							s.Detail = "storage cap reached"
						} else {
							s.Detail += " · storage cap reached"
						}
					}
					s.LastPollAt = &at
					s.LastError = ""
				})
				latest := runtime.latestFrame()
				if latest.FrameID == "" {
					return
				}
				if len(state.GetEvents()) > 0 {
					godets := make(map[int32]*inferencev2.GodetState, len(state.GetGodets()))
					for _, godet := range state.GetGodets() {
						if godet != nil {
							godets[godet.GetGodetId()] = godet
						}
					}
					for _, event := range state.GetEvents() {
						if event == nil || (event.GetState() != "pending" && event.GetState() != "confirmed") {
							continue
						}
						key := event.GetEventKey()
						if key == "" {
							key = fmt.Sprintf("DAMAGE:%d:%d", event.GetGodetId(), event.GetLoopNo())
						}
						if seen[key] == event.GetState() {
							continue
						}
						allow, upsert, gateErr := e.gateStorage(laneCtx, seen[key], key)
						if gateErr != nil {
							e.logger.Error("storage gate failed", "component", "alerting", "event_key", key, "reason", gateErr.Error())
							e.reportFatal(fmt.Errorf("alert persistence: %w", gateErr))
							continue
						}
						e.tracker.noteProcessed()
						if !allow {
							e.tracker.noteDropped()
							e.metrics.AlertsDroppedCap.Add(1)
							e.logger.Warn("storage cap reached; dropping event", "component", "alerting", "event_key", key)
							continue
						}
						frame := runtime.evidenceFrame(event.GetEvidenceFrameId())
						if frame.FrameID == "" {
							frame = latest
						}
						godet := godets[event.GetGodetId()]
						one := &inferencev2.GodetStateResponse{ModelVersion: state.GetModelVersion(), Godets: []*inferencev2.GodetState{godet}, Events: []*inferencev2.GodetAlertEvent{event}, Health: state.GetHealth()}
						alerts, processErr := alerting.ProcessGodetState(laneCtx, one, frame, e.cfg.Storage.JPEGQuality, e.cfg.Storage.EvidencePath, e.store, time.Now)
						if processErr != nil {
							e.logger.Error("godet alert processing failed", "component", "alerting", "event_key", key, "reason", processErr.Error())
							e.reportFatal(fmt.Errorf("alert persistence: %w", processErr))
							continue
						}
						seen[key] = event.GetState()
						if len(alerts) > 0 {
							e.metrics.AlertsCreated.Add(uint64(len(alerts)))
							if upsert {
								e.tracker.noteUpserts(len(alerts))
							} else {
								e.tracker.noteStored(len(alerts))
							}
							e.logger.Info("godet alert upserted", "component", "alerting", "event_key", key, "state", event.GetState(), "loop", event.GetLoopNo())
						}
					}
					return
				}
				frame := latest
				for _, godet := range state.GetGodets() {
					if godet == nil || (godet.GetState() != "pending" && godet.GetState() != "confirmed") {
						continue
					}
					key := fmt.Sprintf("%d:%d", godet.GetGodetId(), godet.GetLastSeenLoop())
					if seen[key] == godet.GetState() {
						continue
					}
					eventKey := fmt.Sprintf("DAMAGE:%d:%d", godet.GetGodetId(), godet.GetLastSeenLoop())
					allow, upsert, gateErr := e.gateStorage(laneCtx, seen[key], eventKey)
					if gateErr != nil {
						e.logger.Error("storage gate failed", "component", "alerting", "godet_id", godet.GetGodetId(), "reason", gateErr.Error())
						e.reportFatal(fmt.Errorf("alert persistence: %w", gateErr))
						continue
					}
					e.tracker.noteProcessed()
					if !allow {
						e.tracker.noteDropped()
						e.metrics.AlertsDroppedCap.Add(1)
						e.logger.Warn("storage cap reached; dropping event", "component", "alerting", "godet_id", godet.GetGodetId())
						continue
					}
					one := &inferencev2.GodetStateResponse{ModelVersion: state.GetModelVersion(), Godets: []*inferencev2.GodetState{godet}, Health: state.GetHealth()}
					alerts, processErr := alerting.ProcessGodetState(laneCtx, one, frame, e.cfg.Storage.JPEGQuality, e.cfg.Storage.EvidencePath, e.store, time.Now)
					if processErr != nil {
						e.logger.Error("godet alert processing failed", "component", "alerting", "godet_id", godet.GetGodetId(), "reason", processErr.Error())
						e.reportFatal(fmt.Errorf("alert persistence: %w", processErr))
						continue
					}
					seen[key] = godet.GetState()
					if len(alerts) > 0 {
						e.metrics.AlertsCreated.Add(uint64(len(alerts)))
						if upsert {
							e.tracker.noteUpserts(len(alerts))
						} else {
							e.tracker.noteStored(len(alerts))
						}
						e.logger.Info("godet alert upserted", "component", "alerting", "godet_id", godet.GetGodetId(), "state", godet.GetState(), "loop", godet.GetLastSeenLoop())
					}
				}
			}
			poll()
			ticker := time.NewTicker(e.statePoll)
			defer ticker.Stop()
			for {
				select {
				case <-laneCtx.Done():
					return
				case <-ticker.C:
					poll()
				}
			}
		}()
	}
	if scheduled {
		go e.watchWindow(laneCtx, laneCancel)
	}
	<-laneCtx.Done()
	for _, rt := range runtimes {
		rt.queue.Close()
	}
	runtimeWG.Wait()
	if scheduled && e.tracker.started() {
		e.finishWindow()
	}
}

// watchWindow ends the lane when the operating window closes.
func (e *laneEnv) watchWindow(laneCtx context.Context, laneCancel context.CancelFunc) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-laneCtx.Done():
			return
		case <-ticker.C:
			if !e.cfg.Schedule.Contains(time.Now().In(e.loc)) {
				e.logger.Info("operating window closed; stopping lanes", "component", "pipeline")
				laneCancel()
				return
			}
		}
	}
}

// finishWindow persists the window stats sidecar and generates the markdown
// night report next to the SQLite database.
func (e *laneEnv) finishWindow() {
	snap := e.tracker.snapshot()
	if snap.start.IsZero() {
		return
	}
	end := time.Now()
	stats := report.WindowStats{
		WindowStart: snap.start, WindowEnd: end,
		ProcessedEvents: snap.processed, Stored: snap.stored,
		DroppedCap: snap.droppedCap, Upsets: snap.upserts,
		ModelVersions: snap.versions, MaxAlerts: e.maxAlerts(),
	}
	statsPath, reportPath := nightReportPaths(e.cfg.Storage.SQLitePath, snap.start)
	if data, err := json.MarshalIndent(stats, "", "  "); err != nil {
		e.logger.Error("window stats encode failed", "component", "pipeline", "reason", err.Error())
	} else if err := os.MkdirAll(filepath.Dir(statsPath), 0o750); err != nil {
		e.logger.Error("window stats directory failed", "component", "pipeline", "reason", err.Error())
	} else if err := os.WriteFile(statsPath, data, 0o600); err != nil {
		e.logger.Error("window stats write failed", "component", "pipeline", "reason", err.Error())
	} else {
		e.logger.Info("window stats written", "component", "pipeline", "path", statsPath)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := report.Generate(ctx, e.store, stats, reportPath); err != nil {
		e.logger.Error("night report failed", "component", "pipeline", "reason", err.Error())
	} else {
		e.logger.Info("night report written", "component", "pipeline", "path", reportPath,
			"stored", snap.stored, "dropped_cap", snap.droppedCap)
	}
}

func main() {
	configPath := flag.String("config", "/etc/clinker-vision/config.yaml", "path to the protected YAML configuration")
	healthcheck := flag.Bool("healthcheck", false, "check the local liveness endpoint and exit")
	flag.Parse()
	if *healthcheck {
		if err := runHealthcheck("http://127.0.0.1:8080/health/live"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	logger := logging.NewJSON(os.Stdout, slog.LevelInfo)
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("configuration rejected", "component", "config", "reason", err.Error())
		os.Exit(1)
	}
	healthHandler := health.New()
	metricHandler := metrics.New()
	alertStore, err := store.Open(context.Background(), cfg.Storage.SQLitePath, cfg.Storage.EvidencePath)
	if err != nil {
		logger.Error("alert store could not start", "component", "store", "reason", err.Error())
		os.Exit(1)
	}
	defer alertStore.Close()
	dependencyTimeout := time.Duration(cfg.Failure.DependencyTimeoutSeconds) * time.Second
	// Container override: the config file stays 127.0.0.1 (bare-metal
	// default), but inside Docker the pipeline must listen on 0.0.0.0 or
	// the host NAT to the container IP gets RST (container-loopback only).
	// External exposure is still localhost-only via the compose port
	// publish 127.0.0.1:8080:8080.
	if override := os.Getenv("PIPELINE_BIND_ADDRESS"); override != "" {
		if override != "127.0.0.1" && override != "0.0.0.0" {
			logger.Error("invalid bind override", "component", "config", "reason", "PIPELINE_BIND_ADDRESS must be 127.0.0.1 or 0.0.0.0")
			os.Exit(1)
		}
		cfg.Server.BindAddress = override
	}
	modelClient, err := dialModelWithRetry(context.Background(), cfg.Model.GRPCAddress, dependencyTimeout, logger)
	if err != nil {
		logger.Error("model client could not start", "component", "inference", "reason", err.Error())
		os.Exit(1)
	}
	defer modelClient.Close()
	statusStore := web.NewStatusStore()
	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.BindAddress, cfg.Server.WebPort),
		Handler:           web.NewHandler(healthHandler, metricHandler, web.NewAPI(alertStore, cfg.Storage.EvidencePath), statusStore),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second,
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fatalErrors := make(chan error, 1)
	reportFatal := func(err error) {
		if err == nil {
			return
		}
		select {
		case fatalErrors <- err:
		default:
		}
	}
	loc := time.UTC
	if cfg.Schedule.Enabled {
		loaded, err := time.LoadLocation(cfg.Schedule.Timezone)
		if err != nil {
			logger.Error("schedule timezone invalid", "component", "config", "reason", err.Error())
			os.Exit(1)
		}
		loc = loaded
	}
	enabledCount := 0
	for _, camera := range cfg.Cameras {
		if camera.Enabled {
			enabledCount++
		}
	}
	if enabledCount == 0 {
		logger.Error("no enabled camera configured", "component", "pipeline")
		os.Exit(1)
	}
	tracker := newWindowTracker()
	env := &laneEnv{
		cfg: cfg, logger: logger, health: healthHandler, metrics: metricHandler,
		status: statusStore, store: alertStore, model: modelClient, loc: loc,
		reportFatal: reportFatal, tracker: tracker,
		dependencyTO:   dependencyTimeout,
		requestTimeout: time.Duration(cfg.Model.RequestTimeoutSeconds) * time.Second,
		statePoll:      time.Duration(cfg.Model.StatePollSeconds) * time.Second,
	}
	logger.Info("pipeline starting", "component", "pipeline",
		"bind_address", cfg.Server.BindAddress, "web_port", cfg.Server.WebPort,
		"camera_count", enabledCount, "schedule_enabled", cfg.Schedule.Enabled)
	var supWG sync.WaitGroup
	supWG.Add(1)
	go func() {
		defer supWG.Done()
		env.supervise(runCtx)
	}()

	serverErrors := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErrors <- err
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-serverErrors:
		healthHandler.SetReady(false)
		cancel()
		supWG.Wait()
		logger.Error("HTTP server stopped unexpectedly", "component", "web", "reason", err.Error())
		os.Exit(1)
	case err := <-fatalErrors:
		healthHandler.SetReady(false)
		logger.Error("fatal dependency failure", "component", "pipeline", "reason", err.Error())
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
			logger.Error("HTTP server shutdown failed", "component", "web", "reason", shutdownErr.Error())
		}
		shutdownCancel()
		supWG.Wait()
		os.Exit(1)
	case signal := <-stop:
		healthHandler.SetReady(false)
		logger.Info("shutdown requested", "component", "pipeline", "signal", signal.String())
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("HTTP server shutdown failed", "component", "web", "reason", err.Error())
			os.Exit(1)
		}
		supWG.Wait()
	}
}

func runHealthcheck(endpoint string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("healthcheck failed: %w", err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		return fmt.Errorf("healthcheck response failed: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck returned HTTP %d", response.StatusCode)
	}
	return nil
}
