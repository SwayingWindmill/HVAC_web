package forecast

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ClickHouseConfig struct {
	BaseURL    string
	Database   string
	Table      string
	Username   string
	Password   string
	HTTPClient *http.Client
}

type ClickHouseSink struct {
	baseURL  *url.URL
	database string
	table    string
	username string
	password string
	client   *http.Client
}

func NewClickHouseSink(config ClickHouseConfig) (*ClickHouseSink, error) {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("forecast ClickHouse URL must be absolute http/https")
	}
	database := config.Database
	if database == "" {
		database = "analytics"
	}
	table := config.Table
	if table == "" {
		table = "forecast_series"
	}
	if !identifier(database) || !identifier(table) {
		return nil, errors.New("forecast ClickHouse database/table identifier is invalid")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &ClickHouseSink{baseURL: parsed, database: database, table: table, username: config.Username, password: config.Password, client: client}, nil
}

func (sink *ClickHouseSink) InsertForecastPoints(ctx context.Context, points []Point) error {
	if len(points) == 0 {
		return errors.New("forecast result batch cannot be empty")
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	for _, point := range points {
		if point.Quality != "VALID" && point.Quality != "DEGRADED" && point.Quality != "FALLBACK" && point.Quality != "INVALID" {
			return errors.New("forecast result quality is invalid")
		}
		if !point.ForecastFor.After(point.ForecastOrigin) {
			return errors.New("forecast_for must be after forecast_origin")
		}
		if err := encoder.Encode(point); err != nil {
			return fmt.Errorf("encode forecast result: %w", err)
		}
	}
	endpoint := *sink.baseURL
	query := endpoint.Query()
	query.Set("database", sink.database)
	query.Set("query", fmt.Sprintf("INSERT INTO %s.%s FORMAT JSONEachRow", sink.database, sink.table))
	query.Set("date_time_input_format", "best_effort")
	query.Set("async_insert", "1")
	query.Set("wait_for_async_insert", "1")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), &body)
	if err != nil {
		return fmt.Errorf("build forecast ClickHouse request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-ndjson")
	if sink.username != "" {
		request.SetBasicAuth(sink.username, sink.password)
	}
	response, err := sink.client.Do(request)
	if err != nil {
		return fmt.Errorf("insert forecast ClickHouse result: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("insert forecast ClickHouse result returned %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

func (sink *ClickHouseSink) HasForecastJob(ctx context.Context, forecastJobID string, expectedCount int) (bool, error) {
	if expectedCount <= 0 {
		return false, errors.New("forecast expected result count must be positive")
	}
	endpoint := *sink.baseURL
	query := endpoint.Query()
	query.Set("database", sink.database)
	query.Set("query", fmt.Sprintf("SELECT count() FROM %s.%s WHERE forecast_job_id=toUUID('%s') FORMAT TabSeparated", sink.database, sink.table, strings.ReplaceAll(forecastJobID, "'", "''")))
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return false, err
	}
	if sink.username != "" {
		request.SetBasicAuth(sink.username, sink.password)
	}
	response, err := sink.client.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return false, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, fmt.Errorf("query forecast ClickHouse publication returned %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		return false, fmt.Errorf("decode forecast ClickHouse publication count: %w", err)
	}
	return count == expectedCount, nil
}

func identifier(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}
