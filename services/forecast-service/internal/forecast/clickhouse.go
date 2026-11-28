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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/clickhousehttp"
)

type ClickHouseConfig struct {
	BaseURL  string
	Database string
	Table    string
	Username string
	Password string
	// The writer may only insert, so published forecasts are read back as the reader.
	ReaderUsername string
	ReaderPassword string
	HTTPClient     *http.Client
}

type ClickHouseSink struct {
	baseURL        *url.URL
	database       string
	table          string
	username       string
	password       string
	readerUsername string
	readerPassword string
	client         *http.Client
	ch             *clickhousehttp.Client
	readCh         *clickhousehttp.Client
}

func NewClickHouseSink(config ClickHouseConfig) (*ClickHouseSink, error) {
	parsed, err := clickhousehttp.ParseBaseURL(config.BaseURL, false)
	if err != nil {
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
		client = clickhousehttp.NewPooledHTTPClient(clickhousehttp.ServiceTransportConfig(10 * time.Second))
	}
	writeClient := clickhousehttp.NewClient(parsed, client, clickhousehttp.Credentials{
		Username: config.Username,
		Password: config.Password,
	})
	readUser, readPass := config.ReaderUsername, config.ReaderPassword
	if readUser == "" {
		readClient := clickhousehttp.NewClient(parsed, client, clickhousehttp.Credentials{})
		return &ClickHouseSink{
			baseURL: parsed, database: database, table: table,
			username: config.Username, password: config.Password,
			readerUsername: config.ReaderUsername, readerPassword: config.ReaderPassword,
			client: client, ch: writeClient, readCh: readClient,
		}, nil
	}
	readClient := writeClient.WithCredentials(readUser, readPass)
	return &ClickHouseSink{
		baseURL: parsed, database: database, table: table,
		username: config.Username, password: config.Password,
		readerUsername: config.ReaderUsername, readerPassword: config.ReaderPassword,
		client: client, ch: writeClient, readCh: readClient,
	}, nil
}

func (sink *ClickHouseSink) CloseIdleConnections() {
	if sink != nil && sink.ch != nil {
		sink.ch.CloseIdleConnections()
	}
}

func (sink *ClickHouseSink) Ping(ctx context.Context) error {
	if sink == nil || sink.ch == nil {
		return errors.New("forecast ClickHouse sink is unavailable")
	}
	pingClient := sink.ch
	if sink.readerUsername != "" && sink.readCh != nil {
		pingClient = sink.readCh
	}
	if err := pingClient.Ping(ctx, false); err != nil {
		var statusErr *clickhousehttp.StatusError
		if errors.As(err, &statusErr) {
			return fmt.Errorf("ping forecast ClickHouse returned %d: %s", statusErr.StatusCode, statusErr.TrimmedBody(512))
		}
		return fmt.Errorf("ping forecast ClickHouse: %w", err)
	}
	return nil
}

func (sink *ClickHouseSink) InsertForecastPoints(ctx context.Context, points []Point) error {
	if len(points) == 0 {
		return errors.New("forecast result batch cannot be empty")
	}
	ordered := slices.Clone(points)
	for i := range ordered {
		ordered[i].ForecastOrigin = ordered[i].ForecastOrigin.UTC()
		ordered[i].ForecastFor = ordered[i].ForecastFor.UTC()
		ordered[i].GeneratedAt = ordered[i].GeneratedAt.UTC()
		if ordered[i].Quality != "VALID" && ordered[i].Quality != "DEGRADED" && ordered[i].Quality != "FALLBACK" && ordered[i].Quality != "INVALID" {
			return errors.New("forecast result quality is invalid")
		}
		if !ordered[i].ForecastFor.After(ordered[i].ForecastOrigin) {
			return errors.New("forecast_for must be after forecast_origin")
		}
	}
	slices.SortFunc(ordered, func(a, b Point) int {
		if !a.ForecastFor.Equal(b.ForecastFor) {
			if a.ForecastFor.Before(b.ForecastFor) {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ForecastID, b.ForecastID)
	})
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	for _, point := range ordered {
		if err := encoder.Encode(point); err != nil {
			return fmt.Errorf("encode forecast result: %w", err)
		}
	}
	raw := body.Bytes()
	token := clickhousehttp.SHA256Hex(raw)
	params := url.Values{
		"database":                   {sink.database},
		"query":                      {fmt.Sprintf("INSERT INTO %s.%s FORMAT JSONEachRow", sink.database, sink.table)},
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
			return fmt.Errorf("insert forecast ClickHouse result returned %d: %s", statusErr.StatusCode, statusErr.TrimmedBody(512))
		}
		return fmt.Errorf("insert forecast ClickHouse result: %w", err)
	}
	return nil
}

func forecastJobWhereClause(scope ForecastJobScope, qualifier string) (string, error) {
	if !uuidPattern.MatchString(scope.ForecastJobID) {
		return "", errors.New("forecast job id must be a UUID")
	}
	predicates := make([]string, 0, 7)
	if scope.TenantID != "" {
		if !uuidPattern.MatchString(scope.TenantID) {
			return "", errors.New("forecast tenant id must be a UUID")
		}
		predicates = append(predicates, fmt.Sprintf("%stenant_id=toUUID('%s')", qualifier, scope.TenantID))
	}
	if scope.SiteID != "" {
		if !uuidPattern.MatchString(scope.SiteID) {
			return "", errors.New("forecast site id must be a UUID")
		}
		predicates = append(predicates, fmt.Sprintf("%ssite_id=toUUID('%s')", qualifier, scope.SiteID))
	}
	if scope.Target != "" {
		if scope.Target != "SITE_LOAD" && scope.Target != "PV_GENERATION" {
			return "", errors.New("forecast target is invalid")
		}
		predicates = append(predicates, fmt.Sprintf("%starget='%s'", qualifier, scope.Target))
	}
	if scope.SubjectType != "" {
		if !identifier(strings.ToLower(scope.SubjectType)) {
			return "", errors.New("forecast subject type is invalid")
		}
		predicates = append(predicates, fmt.Sprintf("%ssubject_type='%s'", qualifier, scope.SubjectType))
	}
	if scope.SubjectID != "" {
		if !uuidPattern.MatchString(scope.SubjectID) {
			return "", errors.New("forecast subject id must be a UUID")
		}
		predicates = append(predicates, fmt.Sprintf("%ssubject_id=toUUID('%s')", qualifier, scope.SubjectID))
	}
	if !scope.ForecastOrigin.IsZero() {
		predicates = append(predicates, fmt.Sprintf("%sforecast_origin=parseDateTime64BestEffort('%s',3,'UTC')", qualifier, scope.ForecastOrigin.UTC().Format(time.RFC3339Nano)))
	}
	predicates = append(predicates, fmt.Sprintf("%sforecast_job_id=toUUID('%s')", qualifier, scope.ForecastJobID))
	return strings.Join(predicates, " AND "), nil
}

func (sink *ClickHouseSink) ReadForecastJob(ctx context.Context, scope ForecastJobScope) ([]Point, error) {
	where, err := forecastJobWhereClause(scope, "fact.")
	if err != nil {
		return nil, err
	}
	sql := fmt.Sprintf(`SELECT
 toString(forecast_id) AS forecast_id,toString(tenant_id) AS tenant_id,toString(site_id) AS site_id,subject_type,toString(subject_id) AS subject_id,target,
 toString(forecast_job_id) AS forecast_job_id,toString(forecast_snapshot_id) AS forecast_snapshot_id,toString(deployment_id) AS deployment_id,toString(model_id) AS model_id,
 toString(model_version_id) AS model_version_id,model_version,toString(feature_set_version_id) AS feature_set_version_id,feature_set_version,toString(input_snapshot_id) AS input_snapshot_id,
 toString(topology_version_id) AS topology_version_id,formatDateTime(forecast_origin,'%%FT%%TZ','UTC') AS forecast_origin,formatDateTime(forecast_for,'%%FT%%TZ','UTC') AS forecast_for,
 horizon_minutes,value,unit,lower_bound,upper_bound,quantile,quality,formatDateTime(generated_at,'%%FT%%TZ','UTC') AS generated_at
 FROM %s.%s AS fact WHERE %s ORDER BY fact.forecast_for FORMAT JSONEachRow`, sink.database, sink.table, where)
	payload, err := sink.readCh.Query(ctx, clickhousehttp.QueryRequest{
		Method:           http.MethodGet,
		SQL:              sql,
		Params:           url.Values{"database": {sink.database}},
		MaxResponseBytes: 32 << 20,
	})
	if err != nil {
		var statusErr *clickhousehttp.StatusError
		if errors.As(err, &statusErr) {
			return nil, fmt.Errorf("query forecast ClickHouse result returned %d: %s", statusErr.StatusCode, statusErr.TrimmedBody(512))
		}
		return nil, fmt.Errorf("query forecast ClickHouse result: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	points := make([]Point, 0)
	for {
		var point Point
		if err = decoder.Decode(&point); errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode forecast ClickHouse result: %w", err)
		}
		points = append(points, point)
	}
	return points, nil
}

func (sink *ClickHouseSink) HasForecastJob(ctx context.Context, scope ForecastJobScope, expectedCount int) (bool, error) {
	if expectedCount <= 0 {
		return false, errors.New("forecast expected result count must be positive")
	}
	where, err := forecastJobWhereClause(scope, "")
	if err != nil {
		return false, err
	}
	body, err := sink.readCh.Query(ctx, clickhousehttp.QueryRequest{
		Method:           http.MethodGet,
		SQL:              fmt.Sprintf("SELECT count() FROM %s.%s WHERE %s FORMAT TabSeparated", sink.database, sink.table, where),
		Params:           url.Values{"database": {sink.database}},
		MaxResponseBytes: 4096,
		TruncateAtLimit:  true,
	})
	if err != nil {
		var statusErr *clickhousehttp.StatusError
		if errors.As(err, &statusErr) {
			return false, fmt.Errorf("query forecast ClickHouse publication returned %d: %s", statusErr.StatusCode, statusErr.TrimmedBody(512))
		}
		return false, err
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
