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
	"github.com/quanlaihe/hvac-web/services/mv-service/internal/mv"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := mv.OpenPostgres(ctx, requiredEnv("MV_DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	hours, err := mv.NewClickHouseHours(
		requiredEnv("MV_CLICKHOUSE_HTTP_URL"), requiredEnv("MV_CLICKHOUSE_READER_USERNAME"), os.Getenv("MV_CLICKHOUSE_READER_PASSWORD"), nil,
	)
	if err != nil {
		log.Fatal(err)
	}
	service, err := mv.NewService(store, hours, store, time.Now, schedulerjob.UUIDv7)
	if err != nil {
		log.Fatal(err)
	}
	handler, err := mv.NewHTTPHandler(service)
	if err != nil {
		log.Fatal(err)
	}
	queue, err := store.EvaluationQueue()
	if err != nil {
		log.Fatal(err)
	}
	workerID := envOrDefault("MV_WORKER_ID", "mv-service:"+hostname())
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if runErr := mv.RunScheduledEvaluations(ctx, queue, service, workerID, 8); runErr != nil {
				log.Printf("M&V scheduled evaluations failed: %v", runErr)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	server := &http.Server{
		Addr:              envOrDefault("MV_HTTP_ADDR", ":19096"),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// Implementing a measure fits fourteen days of hours read from ClickHouse.
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	go func() {
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Printf("mv service stopped unexpectedly: %v", serveErr)
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
