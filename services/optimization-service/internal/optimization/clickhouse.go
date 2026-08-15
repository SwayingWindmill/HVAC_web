package optimization

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

type EvaluationSink interface {
	InsertEvaluation(context.Context, Evaluation) error
	HasEvaluation(context.Context, string) (bool, error)
}

type ClickHouseSink struct {
	baseURL  *url.URL
	username string
	password string
	client   *http.Client
}

func NewClickHouseSink(baseURL, username, password string, client *http.Client) (*ClickHouseSink, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("optimization ClickHouse URL must be absolute http/https")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &ClickHouseSink{baseURL: parsed, username: username, password: password, client: client}, nil
}

func (sink *ClickHouseSink) InsertEvaluation(ctx context.Context, evaluation Evaluation) error {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(evaluation); err != nil {
		return err
	}
	endpoint := *sink.baseURL
	query := endpoint.Query()
	query.Set("query", "INSERT INTO analytics.optimization_evaluations FORMAT JSONEachRow")
	query.Set("date_time_input_format", "best_effort")
	query.Set("async_insert", "1")
	query.Set("wait_for_async_insert", "1")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), &body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-ndjson")
	if sink.username != "" {
		request.SetBasicAuth(sink.username, sink.password)
	}
	response, err := sink.client.Do(request)
	if err != nil {
		return fmt.Errorf("insert optimization evaluation: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("insert optimization evaluation returned %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

func (sink *ClickHouseSink) HasEvaluation(ctx context.Context, evaluationID string) (bool, error) {
	endpoint := *sink.baseURL
	query := endpoint.Query()
	query.Set("query", fmt.Sprintf("SELECT count() FROM analytics.optimization_evaluations WHERE evaluation_id=toUUID('%s') FORMAT TabSeparated", strings.ReplaceAll(evaluationID, "'", "''")))
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
	if response.StatusCode/100 != 2 {
		return false, fmt.Errorf("query optimization evaluation returned %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		return false, err
	}
	return count == 1, nil
}
