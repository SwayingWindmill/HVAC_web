package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/energyseries"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/services/report-service/internal/report"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := report.OpenPostgres(ctx, requiredEnv("REPORT_DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	// The Site's energy comes from the Telemetry Query Service; report-service presents its own
	// workload identity to IAM and to the query service (ADR 0017, ADR 0023).
	series, err := energyseries.NewWorkload(energyseries.WorkloadConfig{
		CertificatePath: requiredEnv("REPORT_TLS_CERT"), PrivateKeyPath: requiredEnv("REPORT_TLS_KEY"), CAPath: requiredEnv("REPORT_TLS_CA"),
		IAMURL: requiredEnv("REPORT_IAM_URL"), QueryURL: requiredEnv("REPORT_QUERY_URL"),
	})
	if err != nil {
		log.Fatal(err)
	}
	hours := report.NewQueryHours(series)
	service, err := report.NewService(store, hours, time.Now, schedulerjob.UUIDv7)
	if err != nil {
		log.Fatal(err)
	}
	// Only the Gateway may call report-service, over mutual TLS with a context it signed (#613).
	serverTLS, err := workloadtls.NewServerTLSConfig(workloadtls.ServerConfig{
		CertificateFiles: workloadtls.CertificateFiles{CertificatePath: requiredEnv("REPORT_TLS_CERT"), PrivateKeyPath: requiredEnv("REPORT_TLS_KEY")},
		ClientCAPath:     requiredEnv("REPORT_TLS_CA"),
	})
	if err != nil {
		log.Fatal(err)
	}
	gatewayKey, err := identitycontext.LoadCertificatePublicKey(requiredEnv("REPORT_GATEWAY_DELEGATION_CERT"))
	if err != nil {
		log.Fatal(err)
	}
	handler, err := report.NewHTTPHandler(service, report.AuthConfig{
		GatewayPublicKey: gatewayKey,
		GatewaySPIFFE:    envOrDefault("REPORT_GATEWAY_SPIFFE", report.DefaultGatewaySPIFFEID),
	})
	if err != nil {
		log.Fatal(err)
	}
	queue, err := store.GenerationQueue()
	if err != nil {
		log.Fatal(err)
	}
	workerID := envOrDefault("REPORT_WORKER_ID", "report-service:"+hostname())
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if runErr := report.RunScheduledGenerations(ctx, queue, service, workerID, 4); runErr != nil {
				log.Printf("report generations failed: %v", runErr)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	server := &http.Server{
		Addr:              envOrDefault("REPORT_HTTP_ADDR", ":19097"),
		Handler:           handler,
		TLSConfig:         serverTLS,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	// The container health check has no client certificate, so it asks a plain port that answers nothing else.
	diagnostics := &http.Server{
		Addr: envOrDefault("REPORT_DIAGNOSTICS_ADDR", ":19102"), Handler: report.HealthHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	go func() {
		if serveErr := server.ListenAndServeTLS("", ""); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Printf("report service stopped unexpectedly: %v", serveErr)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	_ = diagnostics.Shutdown(shutdown)
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return name
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
