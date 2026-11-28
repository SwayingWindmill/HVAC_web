package optimization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/clickhousehttp"
)

type EvaluationSink interface {
	InsertEvaluation(context.Context, Evaluation) error
	HasEvaluation(context.Context, Evaluation) (bool, error)
}

type ClickHouseSink struct {
	baseURL      *url.URL
	username     string
	password     string
	readUsername string
	readPassword string
	client       *http.Client
	ch           *clickhousehttp.Client
	readCh       *clickhousehttp.Client
}

func NewClickHouseSink(baseURL, username, password string, client *http.Client) (*ClickHouseSink, error) {
	parsed, err := clickhousehttp.ParseBaseURL(baseURL, false)
	if err != nil {
		return nil, errors.New("optimization ClickHouse URL must be absolute http/https")
	}
	if client == nil {
		client = clickhousehttp.NewPooledHTTPClient(clickhousehttp.ServiceTransportConfig(10 * time.Second))
	}
	creds := clickhousehttp.Credentials{Username: username, Password: password}
	return &ClickHouseSink{
		baseURL:  parsed,
		username: username,
		password: password,
		client:   client,
		ch:       clickhousehttp.NewClient(parsed, client, creds),
	}, nil
}

// WithReader configures separate read credentials (e.g. optimization_service_reader)
// for SELECT queries such as HasEvaluation while keeping InsertEvaluation on the writer role.
func (sink *ClickHouseSink) WithReader(username, password string) *ClickHouseSink {
	if sink == nil {
		return nil
	}
	sink.readUsername = strings.TrimSpace(username)
	sink.readPassword = password
	if sink.ch != nil {
		sink.readCh = sink.ch.WithCredentials(sink.readUsername, password)
	}
	return sink
}

// Ping executes a lightweight SELECT 1 query against ClickHouse for readiness checks.
func (sink *ClickHouseSink) Ping(ctx context.Context) error {
	if sink == nil || sink.ch == nil {
		return errors.New("optimization ClickHouse sink is not initialized")
	}
	if err := sink.ch.Ping(ctx, false); err != nil {
		var statusErr *clickhousehttp.StatusError
		if errors.As(err, &statusErr) {
			return fmt.Errorf("ping optimization ClickHouse returned %d: %s", statusErr.StatusCode, statusErr.TrimmedBody(512))
		}
		return fmt.Errorf("ping optimization ClickHouse: %w", err)
	}
	return nil
}

// CloseIdleConnections releases idle HTTP keep-alive connections held by this sink.
func (sink *ClickHouseSink) CloseIdleConnections() {
	if sink != nil && sink.ch != nil {
		sink.ch.CloseIdleConnections()
	}
}

func (sink *ClickHouseSink) InsertEvaluation(ctx context.Context, evaluation Evaluation) error {
	evaluation.GeneratedAt = evaluation.GeneratedAt.UTC()
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(evaluation); err != nil {
		return err
	}
	raw := body.Bytes()
	token := clickhousehttp.SHA256Hex(raw)
	params := url.Values{
		"query":                      {"INSERT INTO analytics.optimization_evaluations FORMAT JSONEachRow"},
		"date_time_input_format":     {"best_effort"},
		"async_insert":               {"1"},
		"wait_for_async_insert":      {"1"},
		"async_insert_deduplicate":   {"1"},
		"insert_deduplicate":         {"1"},
		"insert_deduplication_token": {token},
		"wait_end_of_query":          {"1"},
	}
	if err := sink.ch.Insert(ctx, clickhousehttp.InsertRequest{
		Params:        params,
		Body:          raw,
		MaxErrorBytes: 2048,
	}); err != nil {
		var statusErr *clickhousehttp.StatusError
		if errors.As(err, &statusErr) {
			return fmt.Errorf("insert optimization evaluation returned %d: %s", statusErr.StatusCode, statusErr.TrimmedBody(512))
		}
		return fmt.Errorf("insert optimization evaluation: %w", err)
	}
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
	readClient := sink.ch
	if sink.readUsername != "" && sink.readCh != nil {
		readClient = sink.readCh
	}
	body, err := readClient.Query(ctx, clickhousehttp.QueryRequest{
		Method:           http.MethodGet,
		SQL:              fmt.Sprintf("SELECT count() FROM analytics.optimization_evaluations WHERE %s FORMAT TabSeparated", where),
		MaxResponseBytes: 4096,
		TruncateAtLimit:  true,
	})
	if err != nil {
		var statusErr *clickhousehttp.StatusError
		if errors.As(err, &statusErr) {
			return false, fmt.Errorf("query optimization evaluation returned %d: %s", statusErr.StatusCode, statusErr.TrimmedBody(512))
		}
		return false, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		return false, err
	}
	return count == 1, nil
}
