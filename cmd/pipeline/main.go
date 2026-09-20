package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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
	modelClient, err := inference.Dial(context.Background(), cfg.Model.GRPCAddress)
	if err != nil {
		logger.Error("model client could not start", "component", "inference", "reason", err.Error())
		os.Exit(1)
	}
	defer modelClient.Close()
	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.BindAddress, cfg.Server.WebPort),
		Handler:           web.NewHandler(healthHandler, metricHandler, web.NewAPI(alertStore, cfg.Storage.EvidencePath)),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second,
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dependencyTimeout := time.Duration(cfg.Failure.DependencyTimeoutSeconds) * time.Second
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
	runtimes := make([]*cameraRuntime, 0, len(cfg.Cameras))
	for _, camera := range cfg.Cameras {
		if !camera.Enabled {
			continue
		}
		queue, err := framequeue.NewFrameQueue(camera.QueueCapacity)
		if err != nil {
			logger.Error("camera queue could not start", "component", "queue", "camera_id", camera.ID, "reason", err.Error())
			os.Exit(1)
		}
		runtimes = append(runtimes, &cameraRuntime{camera: camera, queue: queue, evidence: make(map[string]ingest.Frame)})
	}
	if len(runtimes) == 0 {
		logger.Error("no enabled camera configured", "component", "pipeline")
		os.Exit(1)
	}

	logger.Info("dense pipeline starting", "component", "pipeline", "bind_address", cfg.Server.BindAddress, "web_port", cfg.Server.WebPort, "camera_count", len(runtimes))
	var runtimeWG sync.WaitGroup
	for _, runtime := range runtimes {
		runtime := runtime
		runtimeWG.Add(3)
		go func() {
			defer runtimeWG.Done()
			defer runtime.queue.Close()
			decoder := ingest.Decoder{CameraID: runtime.camera.ID, Source: runtime.camera.NVRRTSPURL}
			runErr := decoder.Run(runCtx, func(frame ingest.Frame) error {
				metricHandler.FramesDecoded.Add(1)
				metricHandler.FramesSampled.Add(1) // retained metric name; dense lane sends every frame
				runtime.setLatest(frame)
				if err := runtime.queue.PushWait(runCtx, frame); err != nil {
					return err
				}
				return nil
			})
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				metricHandler.DecodeFailures.Add(1)
				logger.Error("camera ingestion stopped", "component", "ingest", "camera_id", runtime.camera.ID, "reason", runErr.Error())
				timer := time.NewTimer(dependencyTimeout)
				select {
				case <-runCtx.Done():
					timer.Stop()
				case <-timer.C:
					reportFatal(fmt.Errorf("%w: camera %s: %v", errDependencyTimeout, runtime.camera.ID, runErr))
				}
			}
		}()

		go func() {
			defer runtimeWG.Done()
			for {
				frame, err := runtime.queue.Pop(runCtx)
				if err != nil {
					if !errors.Is(err, context.Canceled) && !errors.Is(err, framequeue.ErrClosed) {
						logger.Error("camera queue stopped", "component", "queue", "camera_id", runtime.camera.ID, "reason", err.Error())
					}
					return
				}
				metricHandler.InferenceRequests.Add(1)
				response, inferenceErr := inferWithRetry(runCtx, modelClient, frame, time.Duration(cfg.Model.RequestTimeoutSeconds)*time.Second, dependencyTimeout, logger)
				if inferenceErr != nil {
					if errors.Is(inferenceErr, errDependencyTimeout) {
						reportFatal(inferenceErr)
						return
					}
					metricHandler.DependencyFailures.Add(1)
					if !errors.Is(inferenceErr, context.Canceled) {
						logger.Error("inference failed", "component", "inference", "camera_id", frame.CameraID, "frame_id", frame.FrameID, "sequence_no", frame.SequenceNo, "reason", inferenceErr.Error())
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
				ctx, cancel := context.WithTimeout(runCtx, time.Duration(cfg.Model.RequestTimeoutSeconds)*time.Second)
				state, err := modelClient.GetGodetState(ctx, nil)
				cancel()
				if err != nil {
					healthHandler.SetReady(false)
					metricHandler.DependencyFailures.Add(1)
					logger.Warn("godet state poll failed", "component", "alerting", "reason", err.Error())
					if stateFailureSince.IsZero() {
						stateFailureSince = time.Now()
					}
					if dependencyTimeout > 0 && time.Since(stateFailureSince) >= dependencyTimeout {
						reportFatal(fmt.Errorf("%w: godet state: %v", errDependencyTimeout, err))
					}
					return
				}
				stateFailureSince = time.Time{}
				healthHandler.SetReady(state.GetHealth().GetReady())
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
						frame := runtime.evidenceFrame(event.GetEvidenceFrameId())
						if frame.FrameID == "" {
							frame = latest
						}
						godet := godets[event.GetGodetId()]
						one := &inferencev2.GodetStateResponse{ModelVersion: state.GetModelVersion(), Godets: []*inferencev2.GodetState{godet}, Events: []*inferencev2.GodetAlertEvent{event}, Health: state.GetHealth()}
						alerts, processErr := alerting.ProcessGodetState(runCtx, one, frame, cfg.Storage.JPEGQuality, cfg.Storage.EvidencePath, alertStore, time.Now)
						if processErr != nil {
							logger.Error("godet alert processing failed", "component", "alerting", "event_key", key, "reason", processErr.Error())
							reportFatal(fmt.Errorf("alert persistence: %w", processErr))
							continue
						}
						seen[key] = event.GetState()
						if len(alerts) > 0 {
							metricHandler.AlertsCreated.Add(uint64(len(alerts)))
							logger.Info("godet alert upserted", "component", "alerting", "event_key", key, "state", event.GetState(), "loop", event.GetLoopNo())
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
					one := &inferencev2.GodetStateResponse{ModelVersion: state.GetModelVersion(), Godets: []*inferencev2.GodetState{godet}, Health: state.GetHealth()}
					alerts, processErr := alerting.ProcessGodetState(runCtx, one, frame, cfg.Storage.JPEGQuality, cfg.Storage.EvidencePath, alertStore, time.Now)
					if processErr != nil {
						logger.Error("godet alert processing failed", "component", "alerting", "godet_id", godet.GetGodetId(), "reason", processErr.Error())
						reportFatal(fmt.Errorf("alert persistence: %w", processErr))
						continue
					}
					seen[key] = godet.GetState()
					if len(alerts) > 0 {
						metricHandler.AlertsCreated.Add(uint64(len(alerts)))
						logger.Info("godet alert upserted", "component", "alerting", "godet_id", godet.GetGodetId(), "state", godet.GetState(), "loop", godet.GetLastSeenLoop())
					}
				}
			}
			poll()
			ticker := time.NewTicker(time.Duration(cfg.Model.StatePollSeconds) * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-runCtx.Done():
					return
				case <-ticker.C:
					poll()
				}
			}
		}()
	}

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
		for _, runtime := range runtimes {
			runtime.queue.Close()
		}
		runtimeWG.Wait()
		logger.Error("HTTP server stopped unexpectedly", "component", "web", "reason", err.Error())
		os.Exit(1)
	case err := <-fatalErrors:
		healthHandler.SetReady(false)
		logger.Error("fatal dependency failure", "component", "pipeline", "reason", err.Error())
		cancel()
		for _, runtime := range runtimes {
			runtime.queue.Close()
		}
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
			logger.Error("HTTP server shutdown failed", "component", "web", "reason", shutdownErr.Error())
		}
		shutdownCancel()
		runtimeWG.Wait()
		os.Exit(1)
	case signal := <-stop:
		healthHandler.SetReady(false)
		logger.Info("shutdown requested", "component", "pipeline", "signal", signal.String())
		cancel()
		for _, runtime := range runtimes {
			runtime.queue.Close()
		}
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("HTTP server shutdown failed", "component", "web", "reason", err.Error())
			os.Exit(1)
		}
		runtimeWG.Wait()
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
