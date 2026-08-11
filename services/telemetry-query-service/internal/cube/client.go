package cube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/telemetry-query-service/internal/analytics"
)

const (
	loadPath                 = "/cubejs-api/v1/load"
	maximumCubeQueryDuration = 16 * time.Second
	maximumCubeResponseSize  = int64(8 << 20)
)

type TokenFactory interface {
	Token(context.Context, analytics.CallerContext, analyticsmodel.EnergySeriesQuery) (string, error)
}

type Config struct {
	BaseURL         string
	DatasetRevision string
	TokenFactory    TokenFactory
	HTTPClient      *http.Client
}

type Client struct {
	endpoint        *url.URL
	datasetRevision string
	tokenFactory    TokenFactory
	httpClient      *http.Client
}

type loadRequest struct {
	Query cubeQuery `json:"query"`
}

type cubeQuery struct {
	Measures       []string            `json:"measures"`
	Filters        []cubeFilter        `json:"filters"`
	TimeDimensions []cubeTimeDimension `json:"timeDimensions,omitempty"`
	Order          map[string]string   `json:"order,omitempty"`
	Timezone       string              `json:"timezone"`
	Limit          int                 `json:"limit"`
}

type cubeFilter struct {
	Member   string   `json:"member"`
	Operator string   `json:"operator"`
	Values   []string `json:"values"`
}

type cubeTimeDimension struct {
	Dimension   string   `json:"dimension"`
	DateRange   []string `json:"dateRange"`
	Granularity string   `json:"granularity"`
}

type loadResponse struct {
	Data  []map[string]json.RawMessage `json:"data"`
	Error string                       `json:"error"`
}

func NewClient(config Config) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("Cube base URL must be an HTTP(S) origin without user info or path")
	}
	if strings.TrimSpace(config.DatasetRevision) == "" || config.TokenFactory == nil {
		return nil, errors.New("Cube dataset revision and token factory are required")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + loadPath
	parsed.RawQuery = ""
	parsed.Fragment = ""
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		endpoint: parsed, datasetRevision: config.DatasetRevision, tokenFactory: config.TokenFactory, httpClient: client,
	}, nil
}

func (client *Client) QueryEnergySeries(ctx context.Context, caller analytics.CallerContext, productQuery analyticsmodel.EnergySeriesQuery) (analyticsmodel.EnergySeriesResponse, error) {
	if client == nil || client.endpoint == nil || client.tokenFactory == nil || client.httpClient == nil {
		return analyticsmodel.EnergySeriesResponse{}, errors.New("Cube client is closed")
	}
	if err := productQuery.Validate(); err != nil {
		return analyticsmodel.EnergySeriesResponse{}, err
	}
	queryContext, cancel := context.WithTimeout(ctx, maximumCubeQueryDuration)
	defer cancel()
	token, err := client.tokenFactory.Token(queryContext, caller, productQuery)
	if err != nil {
		return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("create Cube token: %w", err)
	}
	series, err := client.load(queryContext, token, buildSeriesQuery(productQuery))
	if err != nil {
		return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("query Cube energy series: %w", err)
	}
	metadata, err := client.load(queryContext, token, buildMetadataQuery(productQuery))
	if err != nil {
		return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("query Cube energy metadata: %w", err)
	}
	return mapEnergySeries(series, metadata, productQuery, client.datasetRevision)
}

func (client *Client) load(ctx context.Context, token string, query cubeQuery) (loadResponse, error) {
	body, err := json.Marshal(loadRequest{Query: query})
	if err != nil {
		return loadResponse{}, fmt.Errorf("encode Cube query: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return loadResponse{}, fmt.Errorf("create Cube request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", token)
	observability.InjectHTTP(ctx, request.Header)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return loadResponse{}, fmt.Errorf("call Cube: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximumCubeResponseSize+1))
	if err != nil {
		return loadResponse{}, fmt.Errorf("read Cube response: %w", err)
	}
	if int64(len(payload)) > maximumCubeResponseSize {
		return loadResponse{}, errors.New("Cube response exceeds the analytics response budget")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(payload))
		if len(message) > 512 {
			message = message[:512]
		}
		return loadResponse{}, fmt.Errorf("Cube returned %d: %s", response.StatusCode, message)
	}
	var decoded loadResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return loadResponse{}, fmt.Errorf("decode Cube response: %w", err)
	}
	if strings.TrimSpace(decoded.Error) != "" {
		return loadResponse{}, fmt.Errorf("Cube query failed: %s", decoded.Error)
	}
	return decoded, nil
}

func buildSeriesQuery(productQuery analyticsmodel.EnergySeriesQuery) cubeQuery {
	periodMember := "energy_usage.period_end." + string(productQuery.Granularity)
	return cubeQuery{
		Measures: []string{
			energyMeasure(productQuery.QualityPolicy),
			"energy_usage.valid_count",
			"energy_usage.suspect_count",
			"energy_usage.invalid_count",
		},
		Filters: energyFilters(productQuery),
		TimeDimensions: []cubeTimeDimension{{
			Dimension:   "energy_usage.period_end",
			DateRange:   []string{productQuery.From.UTC().Format(time.RFC3339Nano), productQuery.To.UTC().Add(-time.Millisecond).Format(time.RFC3339Nano)},
			Granularity: string(productQuery.Granularity),
		}},
		Order:    map[string]string{periodMember: "asc"},
		Timezone: productQuery.Timezone,
		Limit:    10000,
	}
}

func buildMetadataQuery(productQuery analyticsmodel.EnergySeriesQuery) cubeQuery {
	return cubeQuery{
		Measures: []string{
			"energy_usage.max_data_watermark",
			"energy_usage.max_dataset_revision",
		},
		Filters:  energyFilters(productQuery),
		Timezone: productQuery.Timezone,
		Limit:    1,
	}
}

func energyFilters(productQuery analyticsmodel.EnergySeriesQuery) []cubeFilter {
	return []cubeFilter{
		{Member: "energy_usage.organization_id", Operator: "equals", Values: []string{productQuery.OrganizationID}},
		{Member: "energy_usage.site_id", Operator: "equals", Values: []string{productQuery.SiteID}},
		{Member: "energy_usage.energy_type", Operator: "equals", Values: []string{string(productQuery.EnergyType)}},
	}
}

func energyMeasure(policy analyticsmodel.QualityPolicy) string {
	if policy == analyticsmodel.QualityPolicyValidAndSuspect {
		return "energy_usage.energy_valid_and_suspect_kwh"
	}
	return "energy_usage.energy_valid_kwh"
}

func mapEnergySeries(series, metadata loadResponse, productQuery analyticsmodel.EnergySeriesQuery, datasetRevision string) (analyticsmodel.EnergySeriesResponse, error) {
	location, err := time.LoadLocation(productQuery.Timezone)
	if err != nil {
		return analyticsmodel.EnergySeriesResponse{}, err
	}
	periodMember := "energy_usage.period_end." + string(productQuery.Granularity)
	points := make([]analyticsmodel.EnergySeriesPoint, 0, len(series.Data))
	quality := analyticsmodel.QualitySummary{}
	for _, row := range series.Data {
		periodValue, err := scalarString(row[periodMember])
		if err != nil {
			return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("decode Cube period: %w", err)
		}
		periodStart, err := parseCubeTime(periodValue, location)
		if err != nil {
			return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("decode Cube period: %w", err)
		}
		energy, err := scalarFloat(row[energyMeasure(productQuery.QualityPolicy)])
		if err != nil {
			return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("decode Cube energy: %w", err)
		}
		valid, err := scalarInt(row["energy_usage.valid_count"])
		if err != nil {
			return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("decode Cube valid count: %w", err)
		}
		suspect, err := scalarInt(row["energy_usage.suspect_count"])
		if err != nil {
			return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("decode Cube suspect count: %w", err)
		}
		invalid, err := scalarInt(row["energy_usage.invalid_count"])
		if err != nil {
			return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("decode Cube invalid count: %w", err)
		}
		quality.Valid += valid
		quality.Suspect += suspect
		quality.Invalid += invalid
		points = append(points, analyticsmodel.EnergySeriesPoint{
			PeriodStart: periodStart.UTC(),
			PeriodEnd:   periodEnd(periodStart.In(location), productQuery.Granularity).UTC(),
			EnergyKWh:   energy,
		})
	}
	sort.Slice(points, func(left, right int) bool { return points[left].PeriodStart.Before(points[right].PeriodStart) })
	maximumWatermark, maximumDatasetRevision, err := mapEnergyMetadata(metadata)
	if err != nil {
		return analyticsmodel.EnergySeriesResponse{}, err
	}
	resolvedDatasetRevision := datasetRevision + ":empty"
	partial := true
	var dataWatermark *time.Time
	var aggregateWatermark *time.Time
	if maximumDatasetRevision > 0 {
		resolvedDatasetRevision = fmt.Sprintf("%s:%d", datasetRevision, maximumDatasetRevision)
	}
	if !maximumWatermark.IsZero() {
		watermark := maximumWatermark.UTC()
		dataWatermark = &watermark
		aggregateWatermark = &watermark
		partial = watermark.Before(productQuery.To.UTC()) || !coversRequestedBuckets(points, productQuery)
	}
	responseMetadata := analyticsmodel.EnergySeriesMetadata{
		RequestedGranularity: productQuery.Granularity,
		ActualGranularity:    productQuery.Granularity,
		DataWatermark:        dataWatermark,
		AggregateWatermark:   aggregateWatermark,
		DatasetRevision:      resolvedDatasetRevision,
		Partial:              partial,
		QualitySummary:       quality,
	}
	return analyticsmodel.EnergySeriesResponse{SchemaVersion: 1, Points: points, Metadata: responseMetadata}, nil
}

func coversRequestedBuckets(points []analyticsmodel.EnergySeriesPoint, productQuery analyticsmodel.EnergySeriesQuery) bool {
	location, err := time.LoadLocation(productQuery.Timezone)
	if err != nil {
		return false
	}
	available := make(map[int64]struct{}, len(points))
	for _, point := range points {
		available[point.PeriodStart.UTC().UnixNano()] = struct{}{}
	}
	bucket := bucketStart(productQuery.From.In(location), productQuery.Granularity)
	end := productQuery.To.In(location)
	for bucket.Before(end) {
		if _, exists := available[bucket.UTC().UnixNano()]; !exists {
			return false
		}
		next := periodEnd(bucket, productQuery.Granularity)
		if !next.After(bucket) {
			return false
		}
		bucket = next
	}
	return true
}

func bucketStart(value time.Time, granularity analyticsmodel.Granularity) time.Time {
	year, month, day := value.Date()
	switch granularity {
	case analyticsmodel.GranularityHour:
		return time.Date(year, month, day, value.Hour(), 0, 0, 0, value.Location())
	case analyticsmodel.GranularityDay:
		return time.Date(year, month, day, 0, 0, 0, 0, value.Location())
	case analyticsmodel.GranularityMonth:
		return time.Date(year, month, 1, 0, 0, 0, 0, value.Location())
	default:
		return value
	}
}

func mapEnergyMetadata(decoded loadResponse) (time.Time, uint64, error) {
	if len(decoded.Data) == 0 {
		return time.Time{}, 0, nil
	}
	row := decoded.Data[0]
	watermarkValue, hasWatermark, err := optionalScalarString(row["energy_usage.max_data_watermark"])
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("decode Cube data watermark: %w", err)
	}
	revisionValue, hasRevision, err := optionalScalarString(row["energy_usage.max_dataset_revision"])
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("decode Cube dataset revision: %w", err)
	}
	if !hasWatermark && !hasRevision {
		return time.Time{}, 0, nil
	}
	if !hasWatermark || !hasRevision {
		return time.Time{}, 0, errors.New("Cube energy metadata is incomplete")
	}
	watermark, err := parseCubeTime(watermarkValue, time.UTC)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("decode Cube data watermark: %w", err)
	}
	revision, err := strconv.ParseUint(revisionValue, 10, 64)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("decode Cube dataset revision: %w", err)
	}
	return watermark.UTC(), revision, nil
}

func parseCubeTime(value string, _ *time.Location) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05.000", "2006-01-02T15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("unsupported Cube time format")
}

func periodEnd(start time.Time, granularity analyticsmodel.Granularity) time.Time {
	switch granularity {
	case analyticsmodel.GranularityHour:
		return start.Add(time.Hour)
	case analyticsmodel.GranularityDay:
		return start.AddDate(0, 0, 1)
	case analyticsmodel.GranularityMonth:
		return start.AddDate(0, 1, 0)
	default:
		return start
	}
}

func scalarString(raw json.RawMessage) (string, error) {
	value, exists, err := optionalScalarString(raw)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", errors.New("Cube scalar is missing")
	}
	return value, nil
}

func optionalScalarString(raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, true, nil
	}
	value = strings.TrimSpace(string(raw))
	if value == "" {
		return "", false, errors.New("Cube scalar is empty")
	}
	return value, true, nil
}

func scalarFloat(raw json.RawMessage) (float64, error) {
	value, err := scalarString(raw)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(value, 64)
}

func scalarInt(raw json.RawMessage) (int64, error) {
	value, err := scalarString(raw)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(value, 10, 64)
}

var _ analytics.EnergySeriesEngine = (*Client)(nil)
