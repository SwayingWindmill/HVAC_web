package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/quanlaihe/hvac-web/cmd/edge-gateway/internal/edgedaemon"
)

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
		cfg.SongyaxuanPlant = nil
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
