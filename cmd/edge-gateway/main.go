package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/quanlaihe/hvac-web/cmd/edge-gateway/internal/edgedaemon"
)

const defaultOTAStateDir = "/opt/hvac-edge-gateway/ota"

var version = "0.1.0"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "edge-gateway: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("edge-gateway", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "/opt/hvac-edge-gateway/config.json", "path to edge-gateway JSON config")
	showVersion := fs.Bool("version", false, "print edge-gateway version and exit")
	validateConfig := fs.Bool("validate-config", false, "validate config file and exit")
	healthCheckOnly := fs.Bool("health-check", false, "run pre-switch boot verification probe and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *showVersion {
		_, err := fmt.Fprintln(stdout, version)
		return err
	}

	cfg, err := edgedaemon.LoadConfig(*configPath, version)
	if err != nil {
		if !*validateConfig && !*healthCheckOnly {
			rollbackOnEarlyStartupError(*configPath, nil, stderr, err)
		}
		return err
	}
	if *validateConfig {
		_, err := fmt.Fprintln(stdout, "config valid")
		return err
	}

	if *healthCheckOnly {
		tmpStateDir, err := os.MkdirTemp("", "edge-gateway-health-check-*")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(tmpStateDir) }()
		// Pre-switch OTA probes share the live config.json, so isolate runtime
		// state, sockets, and hardware access away from the running daemon.
		cfg.StateDir = tmpStateDir
		cfg.OTA.Dir = filepath.Join(tmpStateDir, "ota")
		cfg.HealthListenAddr = ""
		cfg.Tunnel.Enabled = false
		cfg.MQTTBrokerURL = ""
		cfg.SerialBuses = nil
		cfg.Drivers = nil
		cfg.FieldPlant = nil
		cfg.FieldPlantFile = ""
	}

	daemonCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	daemon, err := edgedaemon.NewDaemon(cfg, edgedaemon.DaemonOptions{
		ProbeBinary: func(probeCtx context.Context, binaryPath string) error {
			cmd := exec.CommandContext(probeCtx, binaryPath, "--health-check", "--config", *configPath)
			return cmd.Run()
		},
		OnOTASwitchBoot: stopSignals,
	})
	if err != nil {
		if !*healthCheckOnly {
			rollbackOnEarlyStartupError(*configPath, &cfg, stderr, err)
		}
		return err
	}

	if *healthCheckOnly {
		checkErr := daemon.CheckBootHealth(daemonCtx)
		_ = daemon.Close()
		if checkErr != nil {
			return checkErr
		}
		_, err := fmt.Fprintln(stdout, "health check passed")
		return err
	}

	rolledBack, err := daemon.VerifyOTAOnBoot(daemonCtx)
	if err != nil {
		_ = daemon.Close()
		return err
	}
	if rolledBack {
		_ = daemon.Close()
		return fmt.Errorf("OTA startup verification failed; rolled back to previous slot for restart")
	}

	return daemon.Run(daemonCtx)
}

func rollbackOnEarlyStartupError(configPath string, loadedCfg *edgedaemon.Config, stderr io.Writer, startupErr error) {
	otaDir := resolveOTADirForEarlyRollback(configPath, loadedCfg)
	rolledBack, rbErr := edgedaemon.RollbackPendingOTAOnEarlyFailure(otaDir, startupErr.Error())
	if rbErr != nil {
		_, _ = fmt.Fprintf(stderr, "edge-gateway: early OTA rollback failed in %s: %v\n", otaDir, rbErr)
		return
	}
	if rolledBack {
		_, _ = fmt.Fprintf(stderr, "edge-gateway: rolled back pending OTA slot in %s after startup failure\n", otaDir)
	}
}

func resolveOTADirForEarlyRollback(configPath string, loadedCfg *edgedaemon.Config) string {
	var candidates []string
	addCandidate := func(dir string) {
		if trimmed := strings.TrimSpace(dir); trimmed != "" {
			candidates = append(candidates, trimmed)
		}
	}

	if loadedCfg != nil {
		addCandidate(loadedCfg.OTA.Dir)
		if strings.TrimSpace(loadedCfg.StateDir) != "" {
			addCandidate(filepath.Join(strings.TrimSpace(loadedCfg.StateDir), "ota"))
		}
	}

	if raw, err := os.ReadFile(strings.TrimSpace(configPath)); err == nil {
		var partial struct {
			StateDir string `json:"stateDir"`
			OTA      struct {
				Dir string `json:"dir"`
			} `json:"ota"`
		}
		if json.Unmarshal(raw, &partial) == nil {
			addCandidate(partial.OTA.Dir)
			if strings.TrimSpace(partial.StateDir) != "" {
				addCandidate(filepath.Join(strings.TrimSpace(partial.StateDir), "ota"))
			}
		}
	}

	if trimmedCfg := strings.TrimSpace(configPath); trimmedCfg != "" {
		cfgDir := filepath.Dir(trimmedCfg)
		addCandidate(filepath.Join(cfgDir, "ota"))
		addCandidate(filepath.Join(cfgDir, "state", "ota"))
	}
	addCandidate(defaultOTAStateDir)

	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "ota-state.json")); err == nil {
			return dir
		}
	}
	return candidates[0]
}
