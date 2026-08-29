package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/tools/eg8200-simulator/internal/simulator"
)

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	configPath := flag.String("plant-config", strings.TrimSpace(os.Getenv("EG8200_SIMULATOR_CONFIG")), "path to the Virtual Central Plant JSON config")
	modbusAddress := flag.String("modbus-addr", envOr("EG8200_ATV630_MODBUS_ADDR", ":1502"), "Virtual ATV630 Modbus/TCP listen address")
	diagnosticsAddress := flag.String("diagnostics-addr", envOr("EG8200_ATV630_DIAGNOSTICS_ADDR", ":19096"), "Virtual ATV630 diagnostics listen address")
	flag.Parse()
	if strings.TrimSpace(*configPath) == "" {
		logger.Error("virtual_atv630_config_required")
		os.Exit(2)
	}

	file, err := os.Open(*configPath)
	if err != nil {
		logger.Error("virtual_atv630_config_open_failed", "error", err.Error())
		os.Exit(1)
	}
	config, err := simulator.DecodeConfig(file)
	_ = file.Close()
	if err != nil {
		logger.Error("virtual_atv630_config_invalid", "error", err.Error())
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	plant := simulator.NewPlant(config.Plant, config.Scenario, time.Now().UTC())
	server, err := simulator.NewVirtualATV630Server(*modbusAddress, plant)
	if err != nil {
		logger.Error("virtual_atv630_modbus_invalid", "error", err.Error())
		os.Exit(1)
	}
	if err := server.Start(); err != nil {
		logger.Error("virtual_atv630_modbus_start_failed", "error", err.Error())
		os.Exit(1)
	}
	defer func() { _ = server.Stop() }()

	diagnostics := virtualDiagnosticsServer(*diagnosticsAddress, plant)
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("virtual_atv630_diagnostics_failed", "error", err.Error())
			cancel()
		}
	}()

	interval := config.Interval()
	logger.Info("virtual_atv630_started", "modbus_address", *modbusAddress, "diagnostics_address", *diagnosticsAddress, "tick_interval", interval.String())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = diagnostics.Shutdown(shutdownContext)
			shutdownCancel()
			return
		case <-ticker.C:
			plant.Tick(interval)
		}
	}
}

func virtualDiagnosticsServer(address string, plant *simulator.Plant) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/ready", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ready\n"))
	})
	mux.HandleFunc("GET /acceptance/chwp", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(plant.Snapshot().Devices["CHWP-01"])
	})
	mux.HandleFunc("PUT /acceptance/chwp/stuck-high", func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Active bool `json:"active"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			http.Error(writer, "invalid disturbance payload", http.StatusBadRequest)
			return
		}
		plant.SetCHWPStuckHighDisturbance(body.Active)
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PUT /acceptance/chwp/fault", func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Code string `json:"code"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			http.Error(writer, "invalid fault payload", http.StatusBadRequest)
			return
		}
		if !plant.SetFault("CHWP-01", strings.TrimSpace(body.Code)) {
			http.Error(writer, "CHWP-01 is unavailable", http.StatusConflict)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	return &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
