package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/services/optimization-service/internal/optimization"
)

func main() {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, requiredEnv("OPTIMIZATION_POSTGRES_DSN"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	publication, err := optimization.NewPostgresStore(pool)
	if err != nil {
		log.Fatal(err)
	}
	evaluations, err := optimization.NewClickHouseSink(
		requiredEnv("OPTIMIZATION_CLICKHOUSE_HTTP_URL"),
		envOrDefault("OPTIMIZATION_CLICKHOUSE_USERNAME", "optimization_service_writer"),
		os.Getenv("OPTIMIZATION_CLICKHOUSE_PASSWORD"), nil,
	)
	if err != nil {
		log.Fatal(err)
	}
	service, err := optimization.NewDefaultService(publication, evaluations, time.Now)
	if err != nil {
		log.Fatal(err)
	}
	handler, err := optimization.NewHTTPHandler(service)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			reconcileCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, reconcileErr := service.Reconcile(reconcileCtx, time.Now().UTC().Add(-2*time.Minute), 100)
			cancel()
			if reconcileErr != nil {
				log.Printf("optimization reconciliation failed: %v", reconcileErr)
			}
		}
	}()

	server := &http.Server{
		Addr:              envOrDefault("OPTIMIZATION_HTTP_ADDR", ":19093"),
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("optimization-service listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}
