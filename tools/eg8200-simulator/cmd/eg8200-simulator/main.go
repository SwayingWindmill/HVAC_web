package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/tools/eg8200-simulator/internal/simulator"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	configPath := flag.String("config", strings.TrimSpace(os.Getenv("EG8200_SIMULATOR_CONFIG")), "path to the simulator JSON config")
	diagnosticsAddress := flag.String("diagnostics-addr", envOr("EG8200_SIMULATOR_DIAGNOSTICS_ADDR", ":19092"), "health server listen address")
	flag.Parse()
	if strings.TrimSpace(*configPath) == "" {
		logger.Error("eg8200_config_required")
		os.Exit(2)
	}
	configFile, err := os.Open(*configPath)
	if err != nil {
		logger.Error("eg8200_config_open_failed", "error", err.Error())
		os.Exit(1)
	}
	config, err := simulator.DecodeConfig(configFile)
	_ = configFile.Close()
	if err != nil {
		logger.Error("eg8200_config_invalid", "error", err.Error())
		os.Exit(1)
	}
	credentials, err := resolveCredentials(config.Credentials)
	if err != nil {
		logger.Error("eg8200_credentials_invalid", "error", err.Error())
		os.Exit(1)
	}
	providerClient, err := simulator.NewThingsBoardClient(
		config.ThingsBoardBaseURL,
		credentials,
		&http.Client{Timeout: 35 * time.Second},
	)
	if err != nil {
		logger.Error("eg8200_thingsboard_client_invalid", "error", err.Error())
		os.Exit(1)
	}
	plant := simulator.NewPlant(config.Plant, time.Now().UTC())
	runtime, err := simulator.NewRuntime(config, plant, providerClient, logger)
	if err != nil {
		logger.Error("eg8200_runtime_invalid", "error", err.Error())
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	diagnostics := diagnosticsServer(*diagnosticsAddress, runtime)
	errCh := make(chan error, 2)
	go func() {
		logger.Info("eg8200_diagnostics_started", "address", *diagnosticsAddress)
		if serveErr := diagnostics.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()
	go func() {
		logger.Info("eg8200_simulator_started", "gateway_id", config.GatewayID, "device_count", len(config.Plant.DeviceIDs()), "publish_interval", config.PublishInterval)
		errCh <- runtime.Run(ctx)
	}()

	select {
	case <-ctx.Done():
	case runErr := <-errCh:
		if runErr != nil {
			logger.Error("eg8200_simulator_failed", "error", runErr.Error())
		}
		cancel()
	}
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = diagnostics.Shutdown(shutdownContext)
	logger.Info("eg8200_simulator_stopped")
}

func resolveCredentials(bindings map[string]string) (map[string]string, error) {
	resolved := make(map[string]string, len(bindings))
	for deviceID, envName := range bindings {
		envName = strings.TrimSpace(envName)
		value := strings.TrimSpace(os.Getenv(envName))
		if value == "" {
			return nil, fmt.Errorf("credential environment variable %s is empty for %s", envName, deviceID)
		}
		resolved[deviceID] = value
	}
	return resolved, nil
}

func diagnosticsServer(address string, runtime *simulator.Runtime) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/health/ready", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !runtime.Ready() {
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte("not ready\n"))
			return
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ready\n"))
	})
	return &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
