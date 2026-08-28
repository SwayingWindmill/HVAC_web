package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
	"github.com/quanlaihe/hvac-web/services/fdd-service/internal/fdd"
)

const (
	deviceHistoryPath        = "/internal/v1/telemetry/device-history"
	maximumHistoryResponse   = int64(2 << 20)
	defaultHistoryQueryDelay = 5 * time.Second
)

type telemetryHistoryClient struct {
	baseURL    string
	httpClient *http.Client
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := fdd.OpenPostgres(ctx, requiredEnv("FDD_DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	history, err := newTelemetryHistoryClient()
	if err != nil {
		log.Fatal(err)
	}
	service, err := fdd.NewService(store, history, time.Now)
	if err != nil {
		log.Fatal(err)
	}
	handler, err := fdd.NewHTTPHandler(service)
	if err != nil {
		log.Fatal(err)
	}
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

func newTelemetryHistoryClient() (*telemetryHistoryClient, error) {
	baseURL, err := telemetryHistoryBaseURL(requiredEnv("FDD_HISTORY_QUERY_URL"))
	if err != nil {
		return nil, err
	}
	certificate, err := tls.LoadX509KeyPair(requiredEnv("FDD_HISTORY_TLS_CERT"), requiredEnv("FDD_HISTORY_TLS_KEY"))
	if err != nil {
		return nil, fmt.Errorf("load FDD telemetry history workload identity: %w", err)
	}
	roots, err := loadCertPool(requiredEnv("FDD_HISTORY_SERVER_CA"))
	if err != nil {
		return nil, fmt.Errorf("load FDD telemetry history server CA: %w", err)
	}
	return &telemetryHistoryClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: defaultHistoryQueryDelay,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS13,
				RootCAs:    roots, Certificates: []tls.Certificate{certificate},
				ServerName: envOrDefault("FDD_HISTORY_SERVER_NAME", "telemetry-query-service"),
			}},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (client *telemetryHistoryClient) QueryDeviceHistory(ctx context.Context, query telemetryhistorymodel.DeviceHistoryQuery, delegationGrant string) (telemetryhistorymodel.DeviceHistoryResponse, error) {
	body, err := json.Marshal(query)
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryResponse{}, fmt.Errorf("encode telemetry history query: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+deviceHistoryPath, bytes.NewReader(body))
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryResponse{}, fmt.Errorf("create telemetry history query: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Delegation-Grant", delegationGrant)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryResponse{}, fmt.Errorf("execute telemetry history query: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return telemetryhistorymodel.DeviceHistoryResponse{}, fmt.Errorf("telemetry history query rejected with status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximumHistoryResponse+1))
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryResponse{}, fmt.Errorf("read telemetry history response: %w", err)
	}
	if int64(len(raw)) > maximumHistoryResponse {
		return telemetryhistorymodel.DeviceHistoryResponse{}, errors.New("telemetry history response exceeds limit")
	}
	var result telemetryhistorymodel.DeviceHistoryResponse
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return telemetryhistorymodel.DeviceHistoryResponse{}, fmt.Errorf("decode telemetry history response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return telemetryhistorymodel.DeviceHistoryResponse{}, errors.New("telemetry history response contains trailing JSON")
	}
	return result, nil
}

func telemetryHistoryBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errors.New("FDD telemetry history query URL must use HTTPS")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func loadCertPool(path string) (*x509.CertPool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(content) {
		return nil, errors.New("certificate pool is empty")
	}
	return pool, nil
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
