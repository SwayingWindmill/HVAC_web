package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/measurement"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

// foldProbeOffsets updates every configured supply/return pair this sample belongs to.
// History replay does not call it. The estimate is stored and never written back onto the temperatures.
func loadProbeOffsetSuggestions(ctx context.Context, tx pgx.Tx, deviceID string) (map[string][]telemetryapi.ProbeOffsetSuggestion, error) {
	rows, err := tx.Query(ctx, `
SELECT pair_code, offset_k, stddev_k, sample_count, window_started_at, window_ended_at, applied, alarm,
       supply_device_id::text, supply_key, return_device_id::text, return_key
FROM (
  SELECT DISTINCT ON (pair_code)
    pair_code, offset_k, stddev_k, sample_count, window_started_at, window_ended_at, applied, alarm,
    supply_device_id, supply_key, return_device_id, return_key
  FROM telemetry_runtime.probe_offset_estimates
  WHERE supply_device_id = $1::uuid OR return_device_id = $1::uuid
  ORDER BY pair_code, window_ended_at DESC
) latest
`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("query probe offset suggestions: %w", err)
	}
	defer rows.Close()
	suggestions := map[string][]telemetryapi.ProbeOffsetSuggestion{}
	for rows.Next() {
		var code, supplyDevice, supplyKey, returnDevice, returnKey string
		var alarm *string
		var offset, stddev float64
		var count int
		var started, ended time.Time
		var applied bool
		if err := rows.Scan(&code, &offset, &stddev, &count, &started, &ended, &applied, &alarm, &supplyDevice, &supplyKey, &returnDevice, &returnKey); err != nil {
			return nil, fmt.Errorf("scan probe offset suggestion: %w", err)
		}
		suggestion := telemetryapi.ProbeOffsetSuggestion{
			PairCode: code, OffsetK: offset, StddevK: stddev, SampleCount: count,
			WindowStartedAt: instant(started), WindowEndedAt: instant(ended),
			Suggested: true, Applied: applied, Alarm: alarm,
		}
		if supplyDevice == deviceID {
			suggestions[supplyKey] = append(suggestions[supplyKey], suggestion)
		}
		if returnDevice == deviceID && returnKey != supplyKey {
			suggestions[returnKey] = append(suggestions[returnKey], suggestion)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate probe offset suggestions: %w", err)
	}
	if len(suggestions) == 0 {
		return nil, nil
	}
	return suggestions, nil
}

func foldProbeOffsets(ctx context.Context, tx pgx.Tx, siteID, deviceID, key string) error {
	if siteID == "" || deviceID == "" || key == "" {
		return nil
	}
	rows, err := tx.Query(ctx, `
SELECT pair_code, config
FROM telemetry_runtime.probe_pairs
WHERE site_id = $1::uuid
`, siteID)
	if err != nil {
		return fmt.Errorf("query probe pairs: %w", err)
	}
	type configured struct {
		code   string
		config probePairConfig
	}
	var pairs []configured
	for rows.Next() {
		var code string
		var raw []byte
		if err := rows.Scan(&code, &raw); err != nil {
			rows.Close()
			return fmt.Errorf("scan probe pair: %w", err)
		}
		var config probePairConfig
		if err := json.Unmarshal(raw, &config); err != nil {
			rows.Close()
			return fmt.Errorf("parse probe pair %s: %w", code, err)
		}
		pairs = append(pairs, configured{code: code, config: config})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate probe pairs: %w", err)
	}
	rows.Close()

	for _, pair := range pairs {
		if !pair.config.mentions(deviceID, key) {
			continue
		}
		if err := foldOnePair(ctx, tx, siteID, pair.code, pair.config); err != nil {
			return err
		}
	}
	return nil
}

type probePairConfig struct {
	Supply         pointRefJSON    `json:"supply"`
	Return         pointRefJSON    `json:"return"`
	Pumps          []operatingJSON `json:"pumps"`
	Chillers       []operatingJSON `json:"chillers"`
	StableBandK    float64         `json:"stableBandK"`
	MinimumMinutes int             `json:"minimumMinutes"`
	MinimumSamples int             `json:"minimumSamples"`
	MaxGapMinutes  int             `json:"maxGapMinutes"`
	OffsetAlarmK   float64         `json:"offsetAlarmK"`
	DriftAlarmK    float64         `json:"driftAlarmK"`
}

func (cfg probePairConfig) mentions(device, key string) bool {
	if cfg.Supply.DeviceID == device && cfg.Supply.Key == key || cfg.Return.DeviceID == device && cfg.Return.Key == key {
		return true
	}
	for _, item := range append(append([]operatingJSON{}, cfg.Pumps...), cfg.Chillers...) {
		if item.DeviceID == device && item.Key == key {
			return true
		}
	}
	return false
}

func (cfg probePairConfig) offsetConfig() measurement.OffsetConfig {
	return measurement.OffsetConfig{
		StableBandK:     cfg.StableBandK,
		MinimumDuration: time.Duration(cfg.MinimumMinutes) * time.Minute,
		MinimumSamples:  cfg.MinimumSamples,
		MaxGap:          time.Duration(cfg.MaxGapMinutes) * time.Minute,
		OffsetAlarmK:    cfg.OffsetAlarmK,
		DriftAlarmK:     cfg.DriftAlarmK,
	}
}

type storedOffsetWindow struct {
	Open      bool      `json:"open"`
	StartedAt time.Time `json:"startedAt"`
	LastAt    time.Time `json:"lastAt"`
	Count     int       `json:"count"`
	SumOffset float64   `json:"sumOffset"`
	SumSq     float64   `json:"sumSq"`
	SupplySum float64   `json:"supplySum"`
	ReturnSum float64   `json:"returnSum"`
	SupplyMin float64   `json:"supplyMin"`
	SupplyMax float64   `json:"supplyMax"`
	ReturnMin float64   `json:"returnMin"`
	ReturnMax float64   `json:"returnMax"`
}

func foldOnePair(ctx context.Context, tx pgx.Tx, siteID, code string, cfg probePairConfig) error {
	sample, ok, err := idleSample(ctx, tx, cfg)
	if err != nil {
		return err
	}
	if !ok {
		sample = measurement.OffsetSample{At: time.Now().UTC(), Eligible: false}
	}
	var window measurement.OffsetWindow
	var state []byte
	err = tx.QueryRow(ctx, `
SELECT state FROM telemetry_runtime.probe_pair_windows
WHERE site_id = $1::uuid AND pair_code = $2
`, siteID, code).Scan(&state)
	if err == nil {
		var stored storedOffsetWindow
		if err := json.Unmarshal(state, &stored); err != nil {
			return fmt.Errorf("parse probe window %s: %w", code, err)
		}
		window = measurement.OffsetWindow{
			Open: stored.Open, StartedAt: stored.StartedAt, LastAt: stored.LastAt, Count: stored.Count,
			SumOffset: stored.SumOffset, SumSq: stored.SumSq, SupplySum: stored.SupplySum, ReturnSum: stored.ReturnSum,
			SupplyMin: stored.SupplyMin, SupplyMax: stored.SupplyMax, ReturnMin: stored.ReturnMin, ReturnMax: stored.ReturnMax,
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("query probe window %s: %w", code, err)
	}
	var previous *float64
	var prior float64
	err = tx.QueryRow(ctx, `
SELECT offset_k FROM telemetry_runtime.probe_offset_estimates
WHERE site_id = $1::uuid AND pair_code = $2
ORDER BY window_ended_at DESC
LIMIT 1
`, siteID, code).Scan(&prior)
	if err == nil {
		previous = &prior
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("query previous probe offset %s: %w", code, err)
	}
	if !ok {
		sample = measurement.OffsetSample{At: time.Now().UTC(), Eligible: false}
	}
	next, estimate := measurement.FoldOffset(cfg.offsetConfig(), window, previous, sample)
	if err := saveProbeWindow(ctx, tx, siteID, code, next); err != nil {
		return err
	}
	if estimate == nil {
		return nil
	}
	alarm := any(nil)
	if estimate.Alarm != "" {
		alarm = estimate.Alarm
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO telemetry_runtime.probe_offset_estimates (
  site_id, pair_code, window_started_at, window_ended_at, sample_count,
  offset_k, stddev_k, supply_mean_c, return_mean_c, applied, alarm,
  supply_device_id, supply_key, return_device_id, return_key
) VALUES (
  $1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, false, $10,
  $11::uuid, $12, $13::uuid, $14
)
ON CONFLICT (site_id, pair_code, window_started_at) DO NOTHING
`, siteID, code, estimate.StartedAt.UTC(), estimate.EndedAt.UTC(), estimate.Count,
		estimate.OffsetK, estimate.StddevK, estimate.SupplyMeanC, estimate.ReturnMeanC, alarm,
		cfg.Supply.DeviceID, cfg.Supply.Key, cfg.Return.DeviceID, cfg.Return.Key); err != nil {
		return fmt.Errorf("store probe offset %s: %w", code, err)
	}
	if err := reflectProbeOffsetAlarm(ctx, tx, siteID, cfg.Supply.DeviceID, cfg.Supply.Key); err != nil {
		return err
	}
	return reflectProbeOffsetAlarm(ctx, tx, siteID, cfg.Return.DeviceID, cfg.Return.Key)
}

func saveProbeWindow(ctx context.Context, tx pgx.Tx, siteID, code string, window measurement.OffsetWindow) error {
	if !window.Open {
		_, err := tx.Exec(ctx, `
DELETE FROM telemetry_runtime.probe_pair_windows
WHERE site_id = $1::uuid AND pair_code = $2
`, siteID, code)
		if err != nil {
			return fmt.Errorf("clear probe window %s: %w", code, err)
		}
		return nil
	}
	raw, err := json.Marshal(storedOffsetWindow{
		Open: true, StartedAt: window.StartedAt.UTC(), LastAt: window.LastAt.UTC(), Count: window.Count,
		SumOffset: window.SumOffset, SumSq: window.SumSq, SupplySum: window.SupplySum, ReturnSum: window.ReturnSum,
		SupplyMin: window.SupplyMin, SupplyMax: window.SupplyMax, ReturnMin: window.ReturnMin, ReturnMax: window.ReturnMax,
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO telemetry_runtime.probe_pair_windows (site_id, pair_code, state, updated_at)
VALUES ($1::uuid, $2, $3::jsonb, clock_timestamp())
ON CONFLICT (site_id, pair_code) DO UPDATE SET state = EXCLUDED.state, updated_at = EXCLUDED.updated_at
`, siteID, code, raw)
	if err != nil {
		return fmt.Errorf("save probe window %s: %w", code, err)
	}
	return nil
}

func idleSample(ctx context.Context, tx pgx.Tx, cfg probePairConfig) (measurement.OffsetSample, bool, error) {
	if len(cfg.Pumps) == 0 || len(cfg.Chillers) == 0 {
		return measurement.OffsetSample{}, false, nil
	}
	supply, ok, err := readLatestSample(ctx, tx, cfg.Supply.DeviceID, cfg.Supply.Key)
	if err != nil || !ok || !supply.Good || supply.Number == nil {
		return measurement.OffsetSample{}, false, err
	}
	ret, ok, err := readLatestSample(ctx, tx, cfg.Return.DeviceID, cfg.Return.Key)
	if err != nil || !ok || !ret.Good || ret.Number == nil {
		return measurement.OffsetSample{}, false, err
	}
	gap := cfg.offsetConfig().MaxGap
	if gap <= 0 {
		gap = measurement.DefaultOffsetMaxGap
	}
	if supply.At.IsZero() || ret.At.IsZero() || supply.At.Sub(ret.At) > gap || ret.At.Sub(supply.At) > gap {
		return measurement.OffsetSample{At: later(supply.At, ret.At), Eligible: false}, true, nil
	}
	for _, pump := range cfg.Pumps {
		current, found, err := readLatestSample(ctx, tx, pump.DeviceID, pump.Key)
		if err != nil || !found || !current.Good || current.Text != pump.Equals {
			return measurement.OffsetSample{At: later(supply.At, ret.At), Eligible: false}, true, err
		}
	}
	for _, chiller := range cfg.Chillers {
		current, found, err := readLatestSample(ctx, tx, chiller.DeviceID, chiller.Key)
		if err != nil || !found || !current.Good || current.Text == "" || current.Text == chiller.Equals {
			return measurement.OffsetSample{At: later(supply.At, ret.At), Eligible: false}, true, err
		}
	}
	at := later(supply.At, ret.At)
	return measurement.OffsetSample{At: at, SupplyC: *supply.Number, ReturnC: *ret.Number, Eligible: true}, true, nil
}

type numberedSample struct {
	Number *float64
	Text   string
	Good   bool
	At     time.Time
}

func readLatestSample(ctx context.Context, tx pgx.Tx, device, key string) (numberedSample, bool, error) {
	var raw []byte
	var quality string
	var sampled time.Time
	err := tx.QueryRow(ctx, `
SELECT value, quality, sampled_at
FROM telemetry_runtime.latest_accepted_telemetry
WHERE device_id = $1::uuid AND telemetry_key = $2
`, device, key).Scan(&raw, &quality, &sampled)
	if errors.Is(err, pgx.ErrNoRows) {
		return numberedSample{}, false, nil
	}
	if err != nil {
		return numberedSample{}, false, fmt.Errorf("query probe input %s/%s: %w", device, key, err)
	}
	sample := numberedSample{Text: jsonText(raw), Good: quality == string(QualityGood), At: sampled.UTC()}
	if number, ok := numericJSON(raw); ok {
		sample.Number = &number
	}
	return sample, true, nil
}

func later(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}

func reflectProbeOffsetAlarm(ctx context.Context, tx pgx.Tx, siteID, device, key string) error {
	var alarming bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM (
    SELECT DISTINCT ON (pair_code) alarm
    FROM telemetry_runtime.probe_offset_estimates
    WHERE site_id = $1::uuid
      AND ((supply_device_id = $2::uuid AND supply_key = $3) OR (return_device_id = $2::uuid AND return_key = $3))
    ORDER BY pair_code, window_ended_at DESC
  ) latest
  WHERE alarm IS NOT NULL AND alarm <> ''
)
`, siteID, device, key).Scan(&alarming); err != nil {
		return fmt.Errorf("query probe offset alarm %s/%s: %w", device, key, err)
	}
	var status *string
	var score *int16
	var reasons []string
	err := tx.QueryRow(ctx, `
SELECT health_status, health_score, health_reasons
FROM telemetry_runtime.latest_accepted_telemetry
WHERE device_id = $1::uuid AND telemetry_key = $2
`, device, key).Scan(&status, &score, &reasons)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("query probe offset health %s/%s: %w", device, key, err)
	}
	next := make([]string, 0, len(reasons)+1)
	for _, reason := range reasons {
		if reason != string(QualityReasonProbeOffset) {
			next = append(next, reason)
		}
	}
	if alarming {
		next = append(next, string(QualityReasonProbeOffset))
	}
	healthStatus := ""
	healthScore := 100
	if status != nil {
		healthStatus = *status
	}
	if score != nil {
		healthScore = int(*score)
	}
	if healthStatus == "" || healthStatus == HealthHealthy {
		healthStatus = HealthHealthy
		healthScore = 100
	}
	if _, err := tx.Exec(ctx, `
UPDATE telemetry_runtime.latest_accepted_telemetry
SET health_status = $3, health_score = $4, health_reasons = $5, health_at = clock_timestamp()
WHERE device_id = $1::uuid AND telemetry_key = $2
`, device, key, healthStatus, healthScore, next); err != nil {
		return fmt.Errorf("update probe offset health %s/%s: %w", device, key, err)
	}
	return nil
}
