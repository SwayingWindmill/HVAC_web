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
	"github.com/quanlaihe/hvac-web/modules/telemetry/internal/analytics"
)

const (
	loadPath                 = "/cubejs-api/v1/load"
	maximumCubeQueryDuration = 16 * time.Second
	maximumCubeResponseSize  = int64(8 << 20)
)

type TokenFactory interface {
	Token(ctx context.Context, caller analytics.CallerContext, tenantID, siteID string) (string, error)
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
	Dimensions     []string            `json:"dimensions,omitempty"`
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
	location, err := time.LoadLocation(productQuery.Timezone)
	if err != nil {
		return analyticsmodel.EnergySeriesResponse{}, err
	}
	queryContext, cancel := context.WithTimeout(ctx, maximumCubeQueryDuration)
	defer cancel()
	token, err := client.tokenFactory.Token(queryContext, caller, productQuery.TenantID, productQuery.SiteID)
	if err != nil {
		return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("create Cube token: %w", err)
	}
	series, err := client.load(queryContext, token, buildSeriesQuery(productQuery, location))
	if err != nil {
		return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("query Cube energy series: %w", err)
	}
	metadata, err := client.load(queryContext, token, buildMetadataQuery(productQuery, location))
	if err != nil {
		return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("query Cube energy metadata: %w", err)
	}
	response, err := mapEnergySeries(series, metadata, productQuery, location, client.datasetRevision)
	if err != nil {
		return analyticsmodel.EnergySeriesResponse{}, err
	}
	if productQuery.EnergyType == analyticsmodel.EnergyTypeElectricity {
		costs, err := client.load(queryContext, token, buildCostQuery(productQuery, location))
		if err != nil {
			return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("query Cube energy cost: %w", err)
		}
		if err := attachCosts(response.Points, costs, productQuery, location); err != nil {
			return analyticsmodel.EnergySeriesResponse{}, err
		}
	}
	if productQuery.Breakdown != analyticsmodel.EnergyBreakdownDevice {
		return response, nil
	}
	devices, err := client.load(queryContext, token, buildDeviceBreakdownQuery(productQuery, location))
	if err != nil {
		return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("query Cube energy breakdown: %w", err)
	}
	response.Devices, err = mapDeviceBreakdown(devices, productQuery, location)
	if err != nil {
		return analyticsmodel.EnergySeriesResponse{}, err
	}
	return response, nil
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

// Cube reads dateRange as wall time in the query timezone and ignores any offset,
// so an RFC 3339 instant would shift the range by the zone's UTC offset.
const cubeWallTime = "2006-01-02T15:04:05.000"

// The Query Service reads Cube's public views only (ADR 0023): site_energy holds the Site's
// total, equipment_energy its equipment sub-meters, each already scoped by its cube.
const (
	siteView      = "site_energy"
	equipmentView = "equipment_energy"
	costView      = "site_energy_cost"
)

func member(view, name string) string { return view + "." + name }

func periodMember(view string, granularity analyticsmodel.Granularity) string {
	return member(view, "period_end") + "." + string(granularity)
}

func buildSeriesQuery(productQuery analyticsmodel.EnergySeriesQuery, location *time.Location) cubeQuery {
	return cubeQuery{
		Measures: []string{
			energyMeasure(siteView, productQuery.QualityPolicy),
			member(siteView, "valid_count"),
			member(siteView, "suspect_count"),
			member(siteView, "invalid_count"),
		},
		Filters:        energyFilters(siteView, productQuery),
		TimeDimensions: periodDimension(siteView, productQuery, location),
		Order:          map[string]string{periodMember(siteView, productQuery.Granularity): "asc"},
		Timezone:       productQuery.Timezone,
		Limit:          10000,
	}
}

// buildCostQuery prices the window's electricity per bucket and Tariff period; the rows of a
// bucket with energy no period covers carry an empty period.
func buildCostQuery(productQuery analyticsmodel.EnergySeriesQuery, location *time.Location) cubeQuery {
	suffix := qualitySuffix(productQuery.QualityPolicy)
	return cubeQuery{
		Measures: []string{
			member(costView, "cost_"+suffix+"_cny"),
			member(costView, "priced_"+suffix+"_kwh"),
			member(costView, "unpriced_"+suffix+"_kwh"),
		},
		Filters:        []cubeFilter{{Member: member(costView, "site_id"), Operator: "equals", Values: []string{productQuery.SiteID}}},
		Dimensions:     []string{member(costView, "tariff_period")},
		TimeDimensions: periodDimension(costView, productQuery, location),
		Timezone:       productQuery.Timezone,
		Limit:          50000,
	}
}

func qualitySuffix(policy analyticsmodel.QualityPolicy) string {
	if policy == analyticsmodel.QualityPolicyValidAndSuspect {
		return "valid_and_suspect"
	}
	return "valid"
}

var tariffPeriodOrder = map[string]int{"SUPER_PEAK": 0, "PEAK": 1, "FLAT": 2, "VALLEY": 3}

// attachCosts gives every electricity point its cost, peak periods first. A point no cost row
// reaches had no energy under the quality policy to price.
func attachCosts(points []analyticsmodel.EnergySeriesPoint, decoded loadResponse, productQuery analyticsmodel.EnergySeriesQuery, location *time.Location) error {
	suffix := qualitySuffix(productQuery.QualityPolicy)
	period := periodMember(costView, productQuery.Granularity)
	byStart := make(map[int64]*analyticsmodel.EnergyCost, len(points))
	for index := range points {
		points[index].Cost = &analyticsmodel.EnergyCost{Periods: []analyticsmodel.PeriodCost{}}
		byStart[points[index].PeriodStart.UnixNano()] = points[index].Cost
	}
	for _, row := range decoded.Data {
		periodValue, err := scalarString(row[period])
		if err != nil {
			return fmt.Errorf("decode Cube cost period: %w", err)
		}
		periodStart, err := parseCubeTime(periodValue, location)
		if err != nil {
			return fmt.Errorf("decode Cube cost period: %w", err)
		}
		cost, ok := byStart[periodStart.UTC().UnixNano()]
		if !ok {
			continue
		}
		code, err := scalarString(row[member(costView, "tariff_period")])
		if err != nil {
			return fmt.Errorf("decode Cube Tariff period: %w", err)
		}
		amount, err := optionalFloat(row[member(costView, "cost_"+suffix+"_cny")])
		if err != nil {
			return fmt.Errorf("decode Cube energy cost: %w", err)
		}
		priced, err := optionalFloat(row[member(costView, "priced_"+suffix+"_kwh")])
		if err != nil {
			return fmt.Errorf("decode Cube priced energy: %w", err)
		}
		unpriced, err := optionalFloat(row[member(costView, "unpriced_"+suffix+"_kwh")])
		if err != nil {
			return fmt.Errorf("decode Cube unpriced energy: %w", err)
		}
		cost.AmountCNY += amount
		cost.UnpricedKWh += unpriced
		if code != "" && priced > 0 {
			cost.Periods = append(cost.Periods, analyticsmodel.PeriodCost{PeriodCode: code, EnergyKWh: priced, AmountCNY: amount})
		}
	}
	for _, cost := range byStart {
		sort.Slice(cost.Periods, func(left, right int) bool {
			return tariffPeriodOrder[cost.Periods[left].PeriodCode] < tariffPeriodOrder[cost.Periods[right].PeriodCode]
		})
	}
	return nil
}

// optionalFloat reads a Cube measure that is null when nothing in the row counts toward it.
func optionalFloat(raw json.RawMessage) (float64, error) {
	value, present, err := optionalScalarString(raw)
	if err != nil || !present {
		return 0, err
	}
	return strconv.ParseFloat(value, 64)
}

func buildDeviceBreakdownQuery(productQuery analyticsmodel.EnergySeriesQuery, location *time.Location) cubeQuery {
	return cubeQuery{
		Measures:       []string{energyMeasure(equipmentView, productQuery.QualityPolicy)},
		Filters:        energyFilters(equipmentView, productQuery),
		Dimensions:     []string{member(equipmentView, "equipment_id")},
		TimeDimensions: periodDimension(equipmentView, productQuery, location),
		Timezone:       productQuery.Timezone,
		Limit:          50000,
	}
}

func periodDimension(view string, productQuery analyticsmodel.EnergySeriesQuery, location *time.Location) []cubeTimeDimension {
	return []cubeTimeDimension{{
		Dimension:   member(view, "period_end"),
		DateRange:   []string{productQuery.From.In(location).Format(cubeWallTime), productQuery.To.In(location).Add(-time.Millisecond).Format(cubeWallTime)},
		Granularity: string(productQuery.Granularity),
	}}
}

// buildMetadataQuery reads the watermark and dataset revision from the window's start on, so its
// cost follows the window rather than the Site's whole history. A Site without facts since then has
// no watermark for the window, which callers already show as no data.
func buildMetadataQuery(productQuery analyticsmodel.EnergySeriesQuery, location *time.Location) cubeQuery {
	return cubeQuery{
		Measures: []string{
			member(siteView, "max_data_watermark"),
			member(siteView, "max_dataset_revision"),
		},
		Filters: append(energyFilters(siteView, productQuery), cubeFilter{
			Member: member(siteView, "period_end"), Operator: "afterOrOnDate", Values: []string{productQuery.From.In(location).Format(cubeWallTime)},
		}),
		Timezone: productQuery.Timezone,
		Limit:    1,
	}
}

// The Tenant comes from the token: Cube's row policy applies it, and the views do not expose it.
func energyFilters(view string, productQuery analyticsmodel.EnergySeriesQuery) []cubeFilter {
	return []cubeFilter{
		{Member: member(view, "site_id"), Operator: "equals", Values: []string{productQuery.SiteID}},
		{Member: member(view, "energy_type"), Operator: "equals", Values: []string{string(productQuery.EnergyType)}},
	}
}

func mapDeviceBreakdown(decoded loadResponse, productQuery analyticsmodel.EnergySeriesQuery, location *time.Location) ([]analyticsmodel.EnergyDeviceSeries, error) {
	period := periodMember(equipmentView, productQuery.Granularity)
	byDevice := map[string][]analyticsmodel.EnergySeriesPoint{}
	for _, row := range decoded.Data {
		deviceID, err := scalarString(row[member(equipmentView, "equipment_id")])
		if err != nil {
			return nil, fmt.Errorf("decode Cube breakdown device: %w", err)
		}
		periodValue, err := scalarString(row[period])
		if err != nil {
			return nil, fmt.Errorf("decode Cube breakdown period: %w", err)
		}
		periodStart, err := parseCubeTime(periodValue, location)
		if err != nil {
			return nil, fmt.Errorf("decode Cube breakdown period: %w", err)
		}
		energyValue, hasEnergy, err := optionalScalarString(row[energyMeasure(equipmentView, productQuery.QualityPolicy)])
		if err != nil {
			return nil, fmt.Errorf("decode Cube breakdown energy: %w", err)
		}
		if !hasEnergy {
			continue
		}
		energy, err := strconv.ParseFloat(energyValue, 64)
		if err != nil {
			return nil, fmt.Errorf("decode Cube breakdown energy: %w", err)
		}
		byDevice[deviceID] = append(byDevice[deviceID], analyticsmodel.EnergySeriesPoint{
			PeriodStart: periodStart.UTC(),
			PeriodEnd:   periodEnd(periodStart.In(location), productQuery.Granularity).UTC(),
			EnergyKWh:   energy,
		})
	}
	devices := make([]analyticsmodel.EnergyDeviceSeries, 0, len(byDevice))
	for deviceID, points := range byDevice {
		sort.Slice(points, func(left, right int) bool { return points[left].PeriodStart.Before(points[right].PeriodStart) })
		devices = append(devices, analyticsmodel.EnergyDeviceSeries{DeviceID: deviceID, Points: points})
	}
	sort.Slice(devices, func(left, right int) bool { return devices[left].DeviceID < devices[right].DeviceID })
	return devices, nil
}

func energyMeasure(view string, policy analyticsmodel.QualityPolicy) string {
	if policy == analyticsmodel.QualityPolicyValidAndSuspect {
		return member(view, "energy_valid_and_suspect_kwh")
	}
	return member(view, "energy_valid_kwh")
}

func mapEnergySeries(series, metadata loadResponse, productQuery analyticsmodel.EnergySeriesQuery, location *time.Location, datasetRevision string) (analyticsmodel.EnergySeriesResponse, error) {
	period := periodMember(siteView, productQuery.Granularity)
	points := make([]analyticsmodel.EnergySeriesPoint, 0, len(series.Data))
	quality := analyticsmodel.QualitySummary{}
	for _, row := range series.Data {
		periodValue, err := scalarString(row[period])
		if err != nil {
			return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("decode Cube period: %w", err)
		}
		periodStart, err := parseCubeTime(periodValue, location)
		if err != nil {
			return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("decode Cube period: %w", err)
		}
		valid, err := scalarInt(row[member(siteView, "valid_count")])
		if err != nil {
			return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("decode Cube valid count: %w", err)
		}
		suspect, err := scalarInt(row[member(siteView, "suspect_count")])
		if err != nil {
			return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("decode Cube suspect count: %w", err)
		}
		invalid, err := scalarInt(row[member(siteView, "invalid_count")])
		if err != nil {
			return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("decode Cube invalid count: %w", err)
		}
		quality.Valid += valid
		quality.Suspect += suspect
		quality.Invalid += invalid
		// Cube answers null when the policy excludes every interval in the bucket: it has no
		// energy, so it is left out and its intervals still count in the quality summary.
		energyValue, hasEnergy, err := optionalScalarString(row[energyMeasure(siteView, productQuery.QualityPolicy)])
		if err != nil {
			return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("decode Cube energy: %w", err)
		}
		if !hasEnergy {
			continue
		}
		energy, err := strconv.ParseFloat(energyValue, 64)
		if err != nil {
			return analyticsmodel.EnergySeriesResponse{}, fmt.Errorf("decode Cube energy: %w", err)
		}
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
		partial = watermark.Before(productQuery.To.UTC()) || !coversRequestedBuckets(points, productQuery, location)
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

func coversRequestedBuckets(points []analyticsmodel.EnergySeriesPoint, productQuery analyticsmodel.EnergySeriesQuery, location *time.Location) bool {
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
	watermarkValue, hasWatermark, err := optionalScalarString(row[member(siteView, "max_data_watermark")])
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("decode Cube data watermark: %w", err)
	}
	revisionValue, hasRevision, err := optionalScalarString(row[member(siteView, "max_dataset_revision")])
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

// An offset-less Cube time is wall time in location: the query timezone for time
// dimension buckets, UTC for measures over time columns.
func parseCubeTime(value string, location *time.Location) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed, nil
	}
	for _, layout := range []string{cubeWallTime, "2006-01-02T15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, value, location); err == nil {
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

func scalarInt(raw json.RawMessage) (int64, error) {
	value, err := scalarString(raw)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(value, 10, 64)
}

var _ analytics.EnergySeriesEngine = (*Client)(nil)
