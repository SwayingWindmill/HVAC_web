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

type configuredProbePair struct {
	siteID string
	code   string
	config probePairConfig
}

// SweepProbeOffsets folds open idle-circulation zero-point windows for all configured
// supply/return probe pairs outside the synchronous observation ingest transaction.
func (store *PostgresStore) SweepProbeOffsets(ctx context.Context, evaluatedAt time.Time) (int, error) {
	if store == nil || store.pool == nil {
		return 0, errors.New("telemetry runtime store is closed")
	}
	if evaluatedAt.IsZero() {
		evaluatedAt = time.Now().UTC()
	} else {
		evaluatedAt = evaluatedAt.UTC()
	}
	pairs, err := store.listConfiguredProbePairs(ctx)
	if err != nil {
		return 0, err
	}
	estimated := 0
	for _, pair := range pairs {
		created, err := store.sweepOneProbePair(ctx, pair, evaluatedAt)
		if err != nil {
			return estimated, err
		}
		if created {
			estimated++
		}
	}
	return estimated, nil
}

func (store *PostgresStore) listConfiguredProbePairs(ctx context.Context) ([]configuredProbePair, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin probe pairs read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return nil, fmt.Errorf("activate telemetry runtime database identity: %w", err)
	}
	rows, err := tx.Query(ctx, `
SELECT site_id::text, pair_code, config
FROM telemetry_runtime.probe_pairs
ORDER BY site_id, pair_code
`)
	if err != nil {
		return nil, fmt.Errorf("query probe pairs: %w", err)
	}
	defer rows.Close()
	var pairs []configuredProbePair
	for rows.Next() {
		var item configuredProbePair
		var raw []byte
		if err := rows.Scan(&item.siteID, &item.code, &raw); err != nil {
			return nil, fmt.Errorf("scan probe pair: %w", err)
		}
		if err := json.Unmarshal(raw, &item.config); err != nil {
			return nil, fmt.Errorf("parse probe pair %s: %w", item.code, err)
		}
		pairs = append(pairs, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate probe pairs: %w", err)
	}
	return pairs, nil
}

func (store *PostgresStore) sweepOneProbePair(ctx context.Context, pair configuredProbePair, evaluatedAt time.Time) (bool, error) {
	for attempt := 0; attempt < 3; attempt++ {
		created, err := store.sweepOneProbePairOnce(ctx, pair, evaluatedAt)
		if err == nil || !retryableTelemetryTransaction(err) {
			return created, err
		}
	}
	return false, fmt.Errorf("probe offset sweep retry budget exhausted for %s", pair.code)
}

func (store *PostgresStore) sweepOneProbePairOnce(ctx context.Context, pair configuredProbePair, evaluatedAt time.Time) (bool, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, fmt.Errorf("begin probe offset sweep transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return false, fmt.Errorf("activate telemetry runtime database identity: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('telemetry-probe-pair:' || $1 || ':' || $2, 0))`, pair.siteID, pair.code); err != nil {
		return false, fmt.Errorf("lock probe pair %s: %w", pair.code, err)
	}
	for _, deviceID := range orderedProbeDeviceIDs(pair.config.Supply.DeviceID, pair.config.Return.DeviceID) {
		if err := lockDevice(ctx, tx, deviceID); err != nil {
			return false, err
		}
	}
	created, err := store.foldOnePair(ctx, tx, pair.siteID, pair.code, pair.config, evaluatedAt)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit probe offset sweep %s: %w", pair.code, err)
	}
	return created, nil
}

func orderedProbeDeviceIDs(supplyDeviceID, returnDeviceID string) []string {
	switch {
	case supplyDeviceID == "" && returnDeviceID == "":
		return nil
	case supplyDeviceID == "":
		return []string{returnDeviceID}
	case returnDeviceID == "" || supplyDeviceID == returnDeviceID:
		return []string{supplyDeviceID}
	case supplyDeviceID < returnDeviceID:
		return []string{supplyDeviceID, returnDeviceID}
	default:
		return []string{returnDeviceID, supplyDeviceID}
	}
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

func (store *PostgresStore) foldOnePair(ctx context.Context, tx pgx.Tx, siteID, code string, cfg probePairConfig, evaluatedAt time.Time) (bool, error) {
	sample, ok, err := idleSample(ctx, tx, cfg, evaluatedAt)
	if err != nil {
		return false, err
	}
	if !ok {
		sample = measurement.OffsetSample{At: evaluatedAt.UTC(), Eligible: false}
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
			return false, fmt.Errorf("parse probe window %s: %w", code, err)
		}
		window = measurement.OffsetWindow{
			Open: stored.Open, StartedAt: stored.StartedAt, LastAt: stored.LastAt, Count: stored.Count,
			SumOffset: stored.SumOffset, SumSq: stored.SumSq, SupplySum: stored.SupplySum, ReturnSum: stored.ReturnSum,
			SupplyMin: stored.SupplyMin, SupplyMax: stored.SupplyMax, ReturnMin: stored.ReturnMin, ReturnMax: stored.ReturnMax,
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("query probe window %s: %w", code, err)
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
		return false, fmt.Errorf("query previous probe offset %s: %w", code, err)
	}
	next, estimate := measurement.FoldOffset(cfg.offsetConfig(), window, previous, sample)
	if !sameOffsetWindow(window, next) {
		if err := saveProbeWindow(ctx, tx, siteID, code, next); err != nil {
			return false, err
		}
	}
	if estimate == nil {
		return false, nil
	}
	alarm := any(nil)
	if estimate.Alarm != "" {
		alarm = estimate.Alarm
	}
	tag, err := tx.Exec(ctx, `
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
		cfg.Supply.DeviceID, cfg.Supply.Key, cfg.Return.DeviceID, cfg.Return.Key)
	if err != nil {
		return false, fmt.Errorf("store probe offset %s: %w", code, err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if err := reflectProbeOffsetAlarm(ctx, tx, siteID, cfg.Supply.DeviceID, cfg.Supply.Key); err != nil {
		return false, err
	}
	if err := reflectProbeOffsetAlarm(ctx, tx, siteID, cfg.Return.DeviceID, cfg.Return.Key); err != nil {
		return false, err
	}
	for _, deviceID := range orderedProbeDeviceIDs(cfg.Supply.DeviceID, cfg.Return.DeviceID) {
		if _, err := store.evaluateAndPersistDeviceWithOptions(ctx, tx, deviceID, nil, evaluatedAt, true); err != nil {
			return false, err
		}
	}
	return true, nil
}

func sameOffsetWindow(left, right measurement.OffsetWindow) bool {
	return left.Open == right.Open &&
		left.Count == right.Count &&
		left.StartedAt.Equal(right.StartedAt) &&
		left.LastAt.Equal(right.LastAt) &&
		left.SumOffset == right.SumOffset &&
		left.SumSq == right.SumSq &&
		left.SupplySum == right.SupplySum &&
		left.ReturnSum == right.ReturnSum &&
		left.SupplyMin == right.SupplyMin &&
		left.SupplyMax == right.SupplyMax &&
		left.ReturnMin == right.ReturnMin &&
		left.ReturnMax == right.ReturnMax
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

func idleSample(ctx context.Context, tx pgx.Tx, cfg probePairConfig, evaluatedAt time.Time) (measurement.OffsetSample, bool, error) {
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
	if !evaluatedAt.IsZero() && (evaluatedAt.Sub(supply.At) > gap || evaluatedAt.Sub(ret.At) > gap) {
		return measurement.OffsetSample{At: evaluatedAt.UTC(), Eligible: false}, true, nil
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
