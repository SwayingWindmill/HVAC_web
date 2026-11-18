package fdd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// A day's window of a loop and a dozen gate devices is about 17k rows of ~300 bytes.
const maximumEvidenceResponseBytes = int64(8 << 20)

var telemetryKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9_.:-]{1,128}$`)

// ClickHouseEvidence reads a loop's window from the Telemetry history owner tables with FDD's
// own read-only account (ADR 0020); the query, not the account, scopes it to a Tenant and Site.
type ClickHouseEvidence struct {
	baseURL  *url.URL
	username string
	password string
	client   *http.Client
}

func newDefaultClickHouseHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.MaxIdleConns = 64
	transport.MaxIdleConnsPerHost = 32
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ExpectContinueTimeout = 1 * time.Second
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64<<10))
	_ = body.Close()
}

func NewClickHouseEvidence(baseURL, username, password string, client *http.Client) (*ClickHouseEvidence, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed == nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" {
		return nil, errors.New("FDD ClickHouse URL must be an HTTP(S) origin")
	}
	if strings.TrimSpace(username) == "" {
		return nil, errors.New("FDD ClickHouse reader username is required")
	}
	if client == nil {
		client = newDefaultClickHouseHTTPClient(10 * time.Second)
	}
	return &ClickHouseEvidence{baseURL: parsed, username: strings.TrimSpace(username), password: password, client: client}, nil
}

// Ping verifies ClickHouse reader connectivity via a lightweight SELECT 1 query.
func (evidence *ClickHouseEvidence) Ping(ctx context.Context) error {
	if evidence == nil || evidence.client == nil || evidence.baseURL == nil {
		return errors.New("FDD ClickHouse reader is not initialized")
	}
	endpoint := *evidence.baseURL
	query := endpoint.Query()
	query.Set("query", "SELECT 1")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("build FDD ClickHouse ping request: %w", err)
	}
	request.SetBasicAuth(evidence.username, evidence.password)
	response, err := evidence.client.Do(request)
	if err != nil {
		return fmt.Errorf("ping FDD ClickHouse: %w", err)
	}
	defer drainAndClose(response.Body)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("ping FDD ClickHouse returned %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

// CloseIdleConnections releases idle HTTP keep-alive connections held by this client.
func (evidence *ClickHouseEvidence) CloseIdleConnections() {
	if evidence != nil && evidence.client != nil {
		evidence.client.CloseIdleConnections()
	}
}

// WindowSamples reads each device's last authoritative observation of each key per minute: either
// the explicit (device_id, telemetry_key) pairs resolved from SiteTwin, or the default loop/gate keys.
func (evidence *ClickHouseEvidence) WindowSamples(ctx context.Context, query WindowQuery) ([]Sample, error) {
	if !uuidPattern.MatchString(query.TenantID) || !uuidPattern.MatchString(query.SiteID) {
		return nil, errors.New("FDD evidence scope must be UUIDs")
	}
	if query.LoopDeviceID != "" && !uuidPattern.MatchString(query.LoopDeviceID) {
		return nil, errors.New("FDD evidence scope must be UUIDs")
	}
	for _, value := range query.GateDeviceIDs {
		if !uuidPattern.MatchString(value) {
			return nil, errors.New("FDD evidence scope must be UUIDs")
		}
	}
	if !query.To.After(query.From) {
		return nil, errors.New("FDD evidence window is invalid")
	}

	var scope string
	if len(query.Points) > 0 {
		var err error
		scope, err = buildPointScope(query.Points)
		if err != nil {
			return nil, err
		}
	} else {
		if !uuidPattern.MatchString(query.LoopDeviceID) {
			return nil, errors.New("FDD evidence scope must be UUIDs")
		}
		scope = fmt.Sprintf("(device_id=toUUID('%s') AND telemetry_key IN ('%s','%s','%s'))", query.LoopDeviceID, supplyTemperatureKey, returnTemperatureKey, coolingLoadKey)
		if len(query.GateDeviceIDs) > 0 {
			devices := make([]string, 0, len(query.GateDeviceIDs))
			for _, device := range query.GateDeviceIDs {
				devices = append(devices, "toUUID('"+device+"')")
			}
			scope += fmt.Sprintf(" OR (device_id IN (%s) AND telemetry_key IN ('%s','%s'))", strings.Join(devices, ","), powerTotalKey, runStateKey)
		}
	}
	// Aliases differ from column names so WHERE reads the columns, not the aggregates.
	latest := "(sampled_at,received_at,source_offset)"
	sql := fmt.Sprintf(`SELECT toString(device_id) AS device,telemetry_key AS key,
 formatDateTime(toStartOfMinute(sampled_at),'%%Y-%%m-%%dT%%H:%%i:%%SZ','UTC') AS minute,
 argMax(toString(observation_id),%[1]s) AS observation,argMax(assumeNotNull(point_type),%[1]s) AS kind,
 argMax(assumeNotNull(value_type),%[1]s) AS type,argMax(ifNull(unit,''),%[1]s) AS unit_code,argMax(quality,%[1]s) AS sample_quality,
 argMax(value_number,%[1]s) AS number,argMax(value_string,%[1]s) AS text
FROM telemetry_history.observations
WHERE tenant_id=toUUID('%[2]s') AND site_id=toUUID('%[3]s') AND (%[4]s)
  AND sampled_at >= parseDateTime64BestEffort('%[5]s',3,'UTC') AND sampled_at < parseDateTime64BestEffort('%[6]s',3,'UTC')
  AND received_at <= parseDateTime64BestEffort('%[6]s',3,'UTC')
  AND acceptance_status IN ('ACCEPTED','OUT_OF_ORDER') AND point_type IS NOT NULL AND value_type IS NOT NULL
GROUP BY device_id,telemetry_key,toStartOfMinute(sampled_at)
ORDER BY minute,device,key
FORMAT JSONEachRow`, latest, query.TenantID, query.SiteID, scope,
		query.From.UTC().Format(time.RFC3339Nano), query.To.UTC().Format(time.RFC3339Nano))
	body, err := evidence.query(ctx, sql)
	if err != nil {
		return nil, err
	}
	return decodeSamples(body)
}

// decodeSamples reads WindowSamples' JSONEachRow rows; replay fixtures are the same rows.
func decodeSamples(body []byte) ([]Sample, error) {
	var samples []Sample
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var row struct {
			Device      string   `json:"device"`
			Key         string   `json:"key"`
			Minute      string   `json:"minute"`
			Observation string   `json:"observation"`
			Kind        string   `json:"kind"`
			Type        string   `json:"type"`
			Unit        string   `json:"unit_code"`
			Quality     string   `json:"sample_quality"`
			Number      *float64 `json:"number"`
			Text        *string  `json:"text"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, fmt.Errorf("decode FDD evidence: %w", err)
		}
		minute, parseErr := time.Parse(time.RFC3339, row.Minute)
		if parseErr != nil {
			return nil, fmt.Errorf("decode FDD evidence time: %w", parseErr)
		}
		sample := Sample{DeviceID: row.Device, Key: row.Key, Minute: minute, ObservationID: row.Observation, PointType: row.Kind, ValueType: row.Type, Unit: row.Unit, Quality: row.Quality}
		if row.Number != nil {
			sample.Number, sample.HasNumber = *row.Number, true
		}
		if row.Text != nil {
			sample.Text = *row.Text
		}
		samples = append(samples, sample)
	}
	return samples, scanner.Err()
}

func buildPointScope(points []PointRef) (string, error) {
	byDevice := make(map[string]map[string]struct{})
	for _, pt := range points {
		if !uuidPattern.MatchString(pt.DeviceID) {
			return "", errors.New("FDD evidence point deviceId must be a UUID")
		}
		if !telemetryKeyPattern.MatchString(pt.Key) {
			return "", fmt.Errorf("FDD evidence telemetry key %q is invalid", pt.Key)
		}
		keys := byDevice[pt.DeviceID]
		if keys == nil {
			keys = make(map[string]struct{})
			byDevice[pt.DeviceID] = keys
		}
		keys[pt.Key] = struct{}{}
	}
	if len(byDevice) == 0 {
		return "", errors.New("FDD evidence query has no points")
	}
	devices := make([]string, 0, len(byDevice))
	for deviceID := range byDevice {
		devices = append(devices, deviceID)
	}
	sort.Strings(devices)

	clauses := make([]string, 0, len(devices))
	for _, deviceID := range devices {
		keySet := byDevice[deviceID]
		keys := make([]string, 0, len(keySet))
		for key := range keySet {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		quoted := make([]string, 0, len(keys))
		for _, key := range keys {
			quoted = append(quoted, "'"+key+"'")
		}
		clauses = append(clauses, fmt.Sprintf("(device_id=toUUID('%s') AND telemetry_key IN (%s))", deviceID, strings.Join(quoted, ",")))
	}
	return strings.Join(clauses, " OR "), nil
}

func (evidence *ClickHouseEvidence) query(ctx context.Context, sql string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, evidence.baseURL.String(), strings.NewReader(sql))
	if err != nil {
		return nil, err
	}
	request.SetBasicAuth(evidence.username, evidence.password)
	response, err := evidence.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query FDD evidence: %w", err)
	}
	defer drainAndClose(response.Body)
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumEvidenceResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maximumEvidenceResponseBytes {
		return nil, errors.New("FDD evidence response is too large")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query FDD evidence: ClickHouse status %d", response.StatusCode)
	}
	return body, nil
}
