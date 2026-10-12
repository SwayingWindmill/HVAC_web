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
	"strings"
	"time"
)

const maximumEvidenceResponseBytes = int64(1 << 16)

// ClickHouseEvidence reads a loop's readings from the Telemetry history owner tables with FDD's
// own read-only account (ADR 0020); the query, not the account, scopes it to a Tenant and Site.
type ClickHouseEvidence struct {
	baseURL  *url.URL
	username string
	password string
	client   *http.Client
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
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &ClickHouseEvidence{baseURL: parsed, username: strings.TrimSpace(username), password: password, client: client}, nil
}

func (evidence *ClickHouseEvidence) LatestLoopReadings(ctx context.Context, query LoopQuery) (LoopReadings, error) {
	for _, value := range []string{query.TenantID, query.SiteID, query.DeviceID} {
		if !uuidPattern.MatchString(value) {
			return LoopReadings{}, errors.New("FDD evidence scope must be UUIDs")
		}
	}
	if !query.To.After(query.From) {
		return LoopReadings{}, errors.New("FDD evidence window is invalid")
	}
	sql := fmt.Sprintf(`SELECT telemetry_key,toString(observation_id) AS observation_id,assumeNotNull(point_type) AS point_type,
 assumeNotNull(value_type) AS value_type,assumeNotNull(unit) AS unit,quality,assumeNotNull(value_number) AS value,
 formatDateTime(sampled_at,'%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ','UTC') AS sampled_at
FROM (
 SELECT *,row_number() OVER (PARTITION BY telemetry_key ORDER BY sampled_at DESC,received_at DESC,source_offset DESC,toString(observation_id) ASC) AS rank
 FROM telemetry_history.observations
 WHERE tenant_id=toUUID('%s') AND site_id=toUUID('%s') AND device_id=toUUID('%s')
   AND telemetry_key IN ('%s','%s')
   AND sampled_at >= parseDateTime64BestEffort('%s',3,'UTC') AND sampled_at <= parseDateTime64BestEffort('%s',3,'UTC')
   AND received_at <= parseDateTime64BestEffort('%s',3,'UTC')
   AND acceptance_status IN ('ACCEPTED','OUT_OF_ORDER')
   AND value_number IS NOT NULL AND point_type IS NOT NULL AND value_type IS NOT NULL AND unit IS NOT NULL
)
WHERE rank=1
FORMAT JSONEachRow`, query.TenantID, query.SiteID, query.DeviceID, supplyTemperatureKey, returnTemperatureKey,
		query.From.UTC().Format(time.RFC3339Nano), query.To.UTC().Format(time.RFC3339Nano), query.To.UTC().Format(time.RFC3339Nano))
	body, err := evidence.query(ctx, sql)
	if err != nil {
		return LoopReadings{}, err
	}
	var readings LoopReadings
	var hasSupply, hasReturn bool
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var row struct {
			TelemetryKey  string  `json:"telemetry_key"`
			ObservationID string  `json:"observation_id"`
			PointType     string  `json:"point_type"`
			ValueType     string  `json:"value_type"`
			Unit          string  `json:"unit"`
			Quality       string  `json:"quality"`
			Value         float64 `json:"value"`
			SampledAt     string  `json:"sampled_at"`
		}
		if err = json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return LoopReadings{}, fmt.Errorf("decode FDD evidence: %w", err)
		}
		sampledAt, parseErr := time.Parse(time.RFC3339Nano, row.SampledAt)
		if parseErr != nil {
			return LoopReadings{}, fmt.Errorf("decode FDD evidence time: %w", parseErr)
		}
		reading := Reading{ObservationID: row.ObservationID, PointType: row.PointType, ValueType: row.ValueType, Unit: row.Unit, Quality: row.Quality, Value: row.Value, SampledAt: sampledAt}
		switch row.TelemetryKey {
		case supplyTemperatureKey:
			readings.Supply, hasSupply = reading, true
		case returnTemperatureKey:
			readings.Return, hasReturn = reading, true
		}
	}
	if err = scanner.Err(); err != nil {
		return LoopReadings{}, err
	}
	if !hasSupply || !hasReturn {
		return LoopReadings{}, ErrNoLoopEvidence
	}
	return readings, nil
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
	defer response.Body.Close()
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
