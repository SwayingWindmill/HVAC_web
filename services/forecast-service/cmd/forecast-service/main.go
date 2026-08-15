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
	"github.com/quanlaihe/hvac-web/services/forecast-service/internal/forecast"
)

func main() {
	ctx := context.Background()
	clickHouseURL := requiredEnv("FORECAST_CLICKHOUSE_HTTP_URL")
	sink, err := forecast.NewClickHouseSink(forecast.ClickHouseConfig{
		BaseURL:  clickHouseURL,
		Database: envOrDefault("FORECAST_CLICKHOUSE_DATABASE", "analytics"),
		Table:    envOrDefault("FORECAST_CLICKHOUSE_TABLE", "forecast_series"),
		Username: envOrDefault("FORECAST_CLICKHOUSE_USERNAME", "forecast_service_writer"),
		Password: os.Getenv("FORECAST_CLICKHOUSE_PASSWORD"),
	})
	if err != nil {
		log.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, requiredEnv("FORECAST_POSTGRES_DSN"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	publication, err := forecast.NewPostgresStore(pool)
	if err != nil {
		log.Fatal(err)
	}
	service, err := forecast.NewService(sink, publication, time.Now)
	if err != nil {
		log.Fatal(err)
	}
	handler, err := forecast.NewHTTPHandler(service)
	if err != nil {
		log.Fatal(err)
	}

	// Cross-store repair is part of the Forecast runtime itself. It only repairs
	// stale PERSISTING jobs after proving the expected result count in ClickHouse.
	reconcileEvery := 30 * time.Second
	go func() {
		ticker := time.NewTicker(reconcileEvery)
		defer ticker.Stop()
		for range ticker.C {
			reconcileCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, reconcileErr := service.Reconcile(reconcileCtx, time.Now().UTC().Add(-2*time.Minute), 100)
			cancel()
			if reconcileErr != nil {
				log.Printf("forecast reconciliation failed: %v", reconcileErr)
			}
		}
	}()

	server := &http.Server{
		Addr:              envOrDefault("FORECAST_HTTP_ADDR", ":19092"),
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("forecast-service listening on %s", server.Addr)
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
