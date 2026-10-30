package optimization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// 14 days of 15-minute buckets for a few dozen devices.
const maximumPlantHistoryResponseBytes = int64(16 << 20)

// PlantBuckets reads the Site's chiller power, chilled- and condenser-water temperatures and
// cooling load as per-device 15-minute means, as only what had arrived by the window's end.
func (reader *ClickHouseInputReader) PlantBuckets(ctx context.Context, query PlantHistoryQuery) ([]PlantBucket, error) {
	if reader == nil || reader.baseURL == nil || reader.client == nil {
		return nil, errors.New("optimization input reader is not configured")
	}
	if !query.From.Before(query.To) {
		return nil, errors.New("plant history window is empty")
	}
	keys := []string{chillerPowerKey, chilledWaterLeavingKey, chilledWaterEnteringKey, condenserWaterLeavingKey, plantCoolingLoadKey}
	mean := func(alias, key string) string {
		return fmt.Sprintf(" if(countIf(telemetry_key='%[1]s')>0, avgIf(value_number, telemetry_key='%[1]s'), NULL) AS %[2]s", key, alias)
	}
	sql := fmt.Sprintf(`SELECT
 formatDateTime(toStartOfFifteenMinutes(sampled_at),'%%Y-%%m-%%dT%%H:%%i:%%SZ','UTC') AS bucket,
 toString(device_id) AS deviceId,
%s,
%s,
%s,
%s,
%s
FROM telemetry_history.observations
WHERE tenant_id=toUUID('%s')
  AND site_id=toUUID('%s')
  AND telemetry_key IN ('%s','%s','%s','%s','%s')
  AND sampled_at >= parseDateTime64BestEffort('%s',3,'UTC')
  AND sampled_at < parseDateTime64BestEffort('%s',3,'UTC')
  AND received_at <= parseDateTime64BestEffort('%s',3,'UTC')
  AND acceptance_status IN ('ACCEPTED','OUT_OF_ORDER')
  AND value_number IS NOT NULL
  AND isFinite(value_number)
  AND device_id IS NOT NULL
GROUP BY bucket, deviceId
ORDER BY bucket, deviceId
FORMAT JSONEachRow`,
		mean("powerKW", chillerPowerKey), mean("chwLeavingC", chilledWaterLeavingKey), mean("chwEnteringC", chilledWaterEnteringKey),
		mean("cwLeavingC", condenserWaterLeavingKey), mean("coolingKW", plantCoolingLoadKey),
		clickHouseString(query.TenantID), clickHouseString(query.SiteID), keys[0], keys[1], keys[2], keys[3], keys[4],
		formatOptimizationTime(query.From), formatOptimizationTime(query.To), formatOptimizationTime(query.To))
	payload, err := reader.queryLimited(ctx, sql, maximumPlantHistoryResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("query plant history: %w", err)
	}
	return decodePlantBuckets(bytes.NewReader(payload))
}

func decodePlantBuckets(source io.Reader) ([]PlantBucket, error) {
	type row struct {
		Bucket       string   `json:"bucket"`
		DeviceID     string   `json:"deviceId"`
		PowerKW      *float64 `json:"powerKW"`
		ChwLeavingC  *float64 `json:"chwLeavingC"`
		ChwEnteringC *float64 `json:"chwEnteringC"`
		CwLeavingC   *float64 `json:"cwLeavingC"`
		CoolingKW    *float64 `json:"coolingKW"`
	}
	decoder := json.NewDecoder(source)
	buckets := []PlantBucket{}
	for {
		var value row
		err := decoder.Decode(&value)
		if errors.Is(err, io.EOF) {
			return buckets, nil
		}
		if err != nil {
			return nil, err
		}
		at, err := time.Parse(time.RFC3339, value.Bucket)
		if err != nil {
			return nil, err
		}
		buckets = append(buckets, PlantBucket{
			Bucket: at.UTC(), DeviceID: value.DeviceID, PowerKW: value.PowerKW, ChwLeavingC: value.ChwLeavingC,
			ChwEnteringC: value.ChwEnteringC, CwLeavingC: value.CwLeavingC, CoolingKW: value.CoolingKW,
		})
	}
}
