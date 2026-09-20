package main

import (
	"flag"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	inferencev2 "github.com/clinkervision/clinker-vision/internal/inference/gen/v2"
	"github.com/clinkervision/clinker-vision/internal/logging"
	"github.com/clinkervision/clinker-vision/internal/mockmodel"
	"google.golang.org/grpc"
)

func main() {
	address := flag.String("addr", ":50051", "gRPC listen address")
	rawMode := flag.String("mode", string(mockmodel.ModeEmpty), "deterministic response mode")
	modelVersion := flag.String("model-version", "mock-2026.09.1", "model version returned in valid responses")
	timeoutDelay := flag.Duration("timeout-delay", 10*time.Second, "delay used by timeout mode")
	flag.Parse()

	logger := logging.NewJSON(os.Stdout, slog.LevelInfo)
	mode, err := mockmodel.ParseMode(*rawMode)
	if err != nil {
		logger.Error("mock model configuration rejected", "component", "mock-model", "reason", err.Error())
		os.Exit(1)
	}
	service, err := mockmodel.New(mode, *modelVersion, *timeoutDelay)
	if err != nil {
		logger.Error("mock model configuration rejected", "component", "mock-model", "reason", err.Error())
		os.Exit(1)
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		logger.Error("mock model listen failed", "component", "mock-model", "reason", err.Error())
		os.Exit(1)
	}

	grpcServer := grpc.NewServer()
	inferencev2.RegisterInferenceServiceServer(grpcServer, service)
	logger.Info("mock model ready", "component", "mock-model", "mode", mode, "model_version", *modelVersion)

	serveErr := make(chan error, 1)
	go func() { serveErr <- grpcServer.Serve(listener) }()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-serveErr:
		logger.Error("mock model stopped unexpectedly", "component", "mock-model", "reason", err.Error())
		os.Exit(1)
	case signal := <-stop:
		logger.Info("mock model shutdown requested", "component", "mock-model", "signal", signal.String())
		stopped := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			grpcServer.Stop()
		}
	}
}
