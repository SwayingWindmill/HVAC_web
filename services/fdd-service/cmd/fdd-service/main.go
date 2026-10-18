package main

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/services/fdd-service/internal/fdd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := fdd.OpenPostgres(ctx, requiredEnv("FDD_DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	evidence, err := fdd.NewClickHouseEvidence(
		requiredEnv("FDD_CLICKHOUSE_HTTP_URL"), requiredEnv("FDD_CLICKHOUSE_READER_USERNAME"), os.Getenv("FDD_CLICKHOUSE_READER_PASSWORD"), nil,
	)
	if err != nil {
		log.Fatal(err)
	}
	service, err := fdd.NewService(store, evidence, time.Now)
	if err != nil {
		log.Fatal(err)
	}
	// Only the Gateway, and the Operations Agent for a Site's findings, may call FDD, each over mutual
	// TLS with a context the Gateway signed (#595).
	serverTLS, err := workloadtls.NewServerTLSConfig(workloadtls.ServerConfig{
		CertificateFiles: workloadtls.CertificateFiles{CertificatePath: requiredEnv("FDD_TLS_CERT"), PrivateKeyPath: requiredEnv("FDD_TLS_KEY")},
		ClientCAPath:     requiredEnv("FDD_CLIENT_CA"),
	})
	if err != nil {
		log.Fatal(err)
	}
	gatewayKey, err := loadCertificatePublicKey(requiredEnv("FDD_GATEWAY_DELEGATION_CERT"))
	if err != nil {
		log.Fatal(err)
	}
	handler, err := fdd.NewHTTPHandler(service, fdd.AuthConfig{
		GatewayPublicKey: gatewayKey,
		GatewaySPIFFE:    envOrDefault("FDD_GATEWAY_SPIFFE", fdd.DefaultGatewaySPIFFEID),
		ReaderSPIFFE:     envOrDefault("FDD_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
	})
	if err != nil {
		log.Fatal(err)
	}
	queue, err := store.EvaluationQueue()
	if err != nil {
		log.Fatal(err)
	}
	workerID := envOrDefault("FDD_WORKER_ID", "fdd-service:"+hostname())
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			if runErr := fdd.RunScheduledEvaluations(ctx, queue, service, workerID, 8); runErr != nil {
				log.Printf("FDD scheduled evaluations failed: %v", runErr)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	server := &http.Server{
		Addr:              envOrDefault("FDD_HTTP_ADDR", ":19094"),
		Handler:           handler,
		TLSConfig:         serverTLS,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	// The container health check has no client certificate, so it asks a plain port that answers nothing else.
	diagnostics := &http.Server{
		Addr: envOrDefault("FDD_DIAGNOSTICS_ADDR", ":19096"), Handler: fdd.HealthHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if serveErr := server.ListenAndServeTLS("", ""); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Printf("fdd service stopped unexpectedly: %v", serveErr)
			stop()
		}
	}()
	go func() {
		if serveErr := diagnostics.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Printf("fdd diagnostics stopped unexpectedly: %v", serveErr)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	_ = diagnostics.Shutdown(shutdown)
}

func loadCertificatePublicKey(path string) (crypto.PublicKey, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("public key certificate is invalid")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	return certificate.PublicKey, nil
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
