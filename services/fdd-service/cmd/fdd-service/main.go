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
	handler, err := fdd.NewHTTPHandler(service)
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
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Printf("fdd service stopped unexpectedly: %v", serveErr)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
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
