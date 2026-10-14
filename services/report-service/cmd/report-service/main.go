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

	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
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
	hours, err := report.NewClickHouseHours(
		requiredEnv("REPORT_CLICKHOUSE_HTTP_URL"), requiredEnv("REPORT_CLICKHOUSE_READER_USERNAME"), os.Getenv("REPORT_CLICKHOUSE_READER_PASSWORD"), nil,
	)
	if err != nil {
		log.Fatal(err)
	}
	service, err := report.NewService(store, hours, time.Now, schedulerjob.UUIDv7)
	if err != nil {
		log.Fatal(err)
	}
	handler, err := report.NewHTTPHandler(service)
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
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Printf("report service stopped unexpectedly: %v", serveErr)
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
