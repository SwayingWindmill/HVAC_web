package clickhouse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

type conditionAnchorInsertRow struct {
	TenantID          string   `json:"tenant_id"`
	SiteID            string   `json:"site_id"`
	Hour              string   `json:"hour"`
	OutdoorWetBulbC   *float64 `json:"outdoor_wet_bulb_c"`
	OutdoorDryBulbC   *float64 `json:"outdoor_dry_bulb_c"`
	PlantLoadRatio    *float64 `json:"plant_load_ratio"`
	AIClosedLoopShare *float64 `json:"ai_closed_loop_share"`
	SampleCount       uint32   `json:"sample_count"`
	CalcVersion       uint64   `json:"calc_version"`
	UpdatedAt         string   `json:"updated_at"`
}

// InsertConditionAnchors writes hourly operating condition anchors into
// analytics.site_condition_anchors_hourly using append-only JSONEachRow inserts.
func (writer *Writer) InsertConditionAnchors(ctx context.Context, anchors []energy.SiteConditionAnchor) error {
	if writer == nil || writer.endpoint == nil || writer.httpClient == nil {
		return errors.New("ClickHouse analytics writer is closed")
	}
	if len(anchors) == 0 {
		return nil
	}
	if len(anchors) > maximumBatchSize {
		return errors.New("ClickHouse analytics condition anchors batch exceeds 4096")
	}

	ordered := append([]energy.SiteConditionAnchor(nil), anchors...)
	sort.Slice(ordered, func(left, right int) bool {
		a, b := ordered[left], ordered[right]
		if a.TenantID != b.TenantID {
			return a.TenantID < b.TenantID
		}
		if a.SiteID != b.SiteID {
			return a.SiteID < b.SiteID
		}
		return a.Hour.Before(b.Hour)
	})

	var body bytes.Buffer
	digest := sha256.New()
	fallbackNow := time.Now().UTC()

	for _, anchor := range ordered {
		if err := validateConditionAnchor(anchor); err != nil {
			return err
		}
		hourUTC := anchor.Hour.UTC().Truncate(time.Hour)
		updatedAt := anchor.UpdatedAt.UTC()
		if updatedAt.IsZero() {
			updatedAt = fallbackNow
		}
		encoded, err := json.Marshal(conditionAnchorInsertRow{
			TenantID:          anchor.TenantID,
			SiteID:            anchor.SiteID,
			Hour:              hourUTC.Format("2006-01-02 15:04:05"),
			OutdoorWetBulbC:   anchor.OutdoorWetBulbC,
			OutdoorDryBulbC:   anchor.OutdoorDryBulbC,
			PlantLoadRatio:    anchor.PlantLoadRatio,
			AIClosedLoopShare: anchor.AIClosedLoopShare,
			SampleCount:       anchor.SampleCount,
			CalcVersion:       anchor.CalcVersion,
			UpdatedAt:         clickHouseTime(updatedAt),
		})
		if err != nil {
			return fmt.Errorf("encode site condition anchor: %w", err)
		}
		body.Write(encoded)
		body.WriteByte('\n')
		fmt.Fprintf(digest, "%s|%s|%d|%s|%s|%s|%s|%d|%d\n",
			anchor.TenantID,
			anchor.SiteID,
			hourUTC.Unix(),
			formatOptionalFloatDigest(anchor.OutdoorWetBulbC),
			formatOptionalFloatDigest(anchor.OutdoorDryBulbC),
			formatOptionalFloatDigest(anchor.PlantLoadRatio),
			formatOptionalFloatDigest(anchor.AIClosedLoopShare),
			anchor.SampleCount,
			anchor.CalcVersion,
		)
	}

	db := strings.TrimSpace(writer.database)
	if db == "" {
		db = "analytics"
	}

	return writer.insert(ctx, "condition anchors insert", url.Values{
		"query":                      {"INSERT INTO " + db + ".site_condition_anchors_hourly FORMAT JSONEachRow"},
		"date_time_input_format":     {"best_effort"},
		"insert_deduplication_token": {hex.EncodeToString(digest.Sum(nil))},
		"async_insert":               {"1"},
		"wait_for_async_insert":      {"1"},
		"async_insert_deduplicate":   {"1"},
		"wait_end_of_query":          {"1"},
	}, body.Bytes())
}

func validateConditionAnchor(anchor energy.SiteConditionAnchor) error {
	if strings.TrimSpace(anchor.TenantID) == "" || strings.TrimSpace(anchor.SiteID) == "" {
		return errors.New("ClickHouse condition anchor tenant_id and site_id are required")
	}
	if anchor.Hour.IsZero() {
		return errors.New("ClickHouse condition anchor hour is required")
	}
	for _, value := range []*float64{
		anchor.OutdoorWetBulbC,
		anchor.OutdoorDryBulbC,
		anchor.PlantLoadRatio,
		anchor.AIClosedLoopShare,
	} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0)) {
			return errors.New("ClickHouse condition anchor metrics must be finite")
		}
	}
	return nil
}

func formatOptionalFloatDigest(val *float64) string {
	if val == nil {
		return "null"
	}
	return strconv.FormatFloat(*val, 'g', -1, 64)
}
