package optimization

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	HasEvaluation(context.Context, Evaluation) (bool, error)
}

type ClickHouseSink struct {
	baseURL  *url.URL
	username string
	password string
	client   *http.Client
}

func newDefaultOptimizationClickHouseHTTPClient(timeout time.Duration) *http.Client {
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		transport = base.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.MaxIdleConns = 64
	transport.MaxIdleConnsPerHost = 32
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ExpectContinueTimeout = time.Second
	return &http.Client{Timeout: timeout, Transport: transport}
}

func NewClickHouseSink(baseURL, username, password string, client *http.Client) (*ClickHouseSink, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("optimization ClickHouse URL must be absolute http/https")
	}
	if client == nil {
		client = newDefaultOptimizationClickHouseHTTPClient(10 * time.Second)
	}
	return &ClickHouseSink{baseURL: parsed, username: username, password: password, client: client}, nil
}

func (sink *ClickHouseSink) InsertEvaluation(ctx context.Context, evaluation Evaluation) error {
	evaluation.GeneratedAt = evaluation.GeneratedAt.UTC()
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(evaluation); err != nil {
		return err
	}
	token := fmt.Sprintf("%x", sha256.Sum256(body.Bytes()))
	endpoint := *sink.baseURL
	query := endpoint.Query()
	query.Set("query", "INSERT INTO analytics.optimization_evaluations FORMAT JSONEachRow")
	query.Set("date_time_input_format", "best_effort")
	query.Set("async_insert", "1")
	query.Set("wait_for_async_insert", "1")
	query.Set("async_insert_deduplicate", "1")
	query.Set("insert_deduplicate", "1")
	query.Set("insert_deduplication_token", token)
	query.Set("wait_end_of_query", "1")
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
		_, _ = io.Copy(io.Discard, response.Body)
		return fmt.Errorf("insert optimization evaluation returned %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

func evaluationWhereClause(evaluation Evaluation) (string, error) {
	if !uuidPattern.MatchString(evaluation.EvaluationID) {
		return "", errors.New("optimization evaluation id must be a UUID")
	}
	predicates := make([]string, 0, 4)
	if evaluation.TenantID != "" {
		if !uuidPattern.MatchString(evaluation.TenantID) {
			return "", errors.New("optimization tenant id must be a UUID")
		}
		predicates = append(predicates, fmt.Sprintf("tenant_id=toUUID('%s')", evaluation.TenantID))
	}
	if evaluation.SiteID != "" {
		if !uuidPattern.MatchString(evaluation.SiteID) {
			return "", errors.New("optimization site id must be a UUID")
		}
		predicates = append(predicates, fmt.Sprintf("site_id=toUUID('%s')", evaluation.SiteID))
	}
	if evaluation.OptimizationRunID != "" {
		if !uuidPattern.MatchString(evaluation.OptimizationRunID) {
			return "", errors.New("optimization run id must be a UUID")
		}
		predicates = append(predicates, fmt.Sprintf("optimization_run_id=toUUID('%s')", evaluation.OptimizationRunID))
	}
	predicates = append(predicates, fmt.Sprintf("evaluation_id=toUUID('%s')", evaluation.EvaluationID))
	return strings.Join(predicates, " AND "), nil
}

func (sink *ClickHouseSink) HasEvaluation(ctx context.Context, evaluation Evaluation) (bool, error) {
	where, err := evaluationWhereClause(evaluation)
	if err != nil {
		return false, err
	}
	endpoint := *sink.baseURL
	query := endpoint.Query()
	query.Set("query", fmt.Sprintf("SELECT count() FROM analytics.optimization_evaluations WHERE %s FORMAT TabSeparated", where))
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
	_, _ = io.Copy(io.Discard, response.Body)
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
