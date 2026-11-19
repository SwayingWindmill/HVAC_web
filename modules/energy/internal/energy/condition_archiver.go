package energy

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	minWetBulbC              = -40.0
	maxWetBulbC              = 50.0
	minDryBulbC              = -50.0
	maxDryBulbC              = 60.0
	wetDryPsychrometricSlack = 0.5
	maxPlantLoadRatio        = 1.5
)

var conditionAnchorUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ConditionSampleInput supplies raw plant operating condition readings to be aggregated into
// hourly SiteConditionAnchor rows.
type ConditionSampleInput struct {
	TenantID          string
	SiteID            string
	Hour              time.Time
	OutdoorWetBulbC   *float64
	OutdoorDryBulbC   *float64
	CoolingKWh        *float64
	RatedCapacityKW   *float64
	ClosedLoopTicks   int
	TotalControlTicks int
	CalcVersion       uint64
	UpdatedAt         time.Time
}

// SiteConditionAnchor stores one hourly operating condition anchor matching
// analytics.site_condition_anchors_hourly.
type SiteConditionAnchor struct {
	TenantID          string
	SiteID            string
	Hour              time.Time
	OutdoorWetBulbC   *float64
	OutdoorDryBulbC   *float64
	PlantLoadRatio    *float64
	AIClosedLoopShare *float64
	SampleCount       uint32
	CalcVersion       uint64
	UpdatedAt         time.Time
}

type conditionGroupKey struct {
	tenantID string
	siteID   string
	hourUnix int64
}

type conditionAccumulator struct {
	tenantID          string
	siteID            string
	hour              time.Time
	wetBulbSum        float64
	wetBulbCount      uint32
	dryBulbSum        float64
	dryBulbCount      uint32
	plrSum            float64
	plrCount          uint32
	closedLoopTicks   int64
	totalControlTicks int64
	sampleCount       uint32
	calcVersion       uint64
	updatedAt         time.Time
}

// BuildSiteConditionAnchors validates, filters, and aggregates plant operating condition
// inputs by (TenantID, SiteID, Hour.UTC().Truncate(time.Hour)) in deterministic order.
//
// Inputs with invalid UUIDs or zero Hour are skipped. Temperature readings outside physical
// bounds (wet-bulb [-40, 50] C, dry-bulb [-50, 60] C) or non-finite values are ignored. When
// both wet-bulb and dry-bulb exist on a single sample and DryBulb < WetBulb - 0.5 C, both
// temperature readings on that inconsistent sample are discarded. PlantLoadRatio is computed as
// coolingKWh / ratedCapacityKW clamped to [0, 1.5], and AIClosedLoopShare as
// closedLoopTicks / totalControlTicks clamped to [0, 1].
func BuildSiteConditionAnchors(inputs []ConditionSampleInput) []SiteConditionAnchor {
	groups := make(map[conditionGroupKey]*conditionAccumulator)
	for _, in := range inputs {
		tenantID := strings.ToLower(strings.TrimSpace(in.TenantID))
		siteID := strings.ToLower(strings.TrimSpace(in.SiteID))
		if !conditionAnchorUUIDPattern.MatchString(tenantID) || !conditionAnchorUUIDPattern.MatchString(siteID) || in.Hour.IsZero() {
			continue
		}
		hourUTC := in.Hour.UTC().Truncate(time.Hour)
		key := conditionGroupKey{
			tenantID: tenantID,
			siteID:   siteID,
			hourUnix: hourUTC.Unix(),
		}
		acc := groups[key]
		if acc == nil {
			acc = &conditionAccumulator{
				tenantID: tenantID,
				siteID:   siteID,
				hour:     hourUTC,
			}
			groups[key] = acc
		}
		acc.addSample(in)
	}

	anchors := make([]SiteConditionAnchor, 0, len(groups))
	for _, acc := range groups {
		anchors = append(anchors, acc.buildAnchor())
	}
	sort.Slice(anchors, func(i, j int) bool {
		if anchors[i].TenantID != anchors[j].TenantID {
			return anchors[i].TenantID < anchors[j].TenantID
		}
		if anchors[i].SiteID != anchors[j].SiteID {
			return anchors[i].SiteID < anchors[j].SiteID
		}
		return anchors[i].Hour.Before(anchors[j].Hour)
	})
	return anchors
}

func (acc *conditionAccumulator) addSample(in ConditionSampleInput) {
	acc.sampleCount++
	if in.CalcVersion > acc.calcVersion {
		acc.calcVersion = in.CalcVersion
	}
	if !in.UpdatedAt.IsZero() {
		updated := in.UpdatedAt.UTC()
		if acc.updatedAt.IsZero() || updated.After(acc.updatedAt) {
			acc.updatedAt = updated
		}
	}

	wb, wbOK := finiteInRange(in.OutdoorWetBulbC, minWetBulbC, maxWetBulbC)
	db, dbOK := finiteInRange(in.OutdoorDryBulbC, minDryBulbC, maxDryBulbC)
	if wbOK && dbOK && db < wb-wetDryPsychrometricSlack {
		wbOK = false
		dbOK = false
	}
	if wbOK {
		acc.wetBulbSum += wb
		acc.wetBulbCount++
	}
	if dbOK {
		acc.dryBulbSum += db
		acc.dryBulbCount++
	}

	if in.CoolingKWh != nil && in.RatedCapacityKW != nil {
		cooling := *in.CoolingKWh
		rated := *in.RatedCapacityKW
		if isFiniteFloat(cooling) && cooling >= 0 && isFiniteFloat(rated) && rated > 0 {
			plr := clampFloat(cooling/rated, 0, maxPlantLoadRatio)
			acc.plrSum += plr
			acc.plrCount++
		}
	}

	if in.TotalControlTicks > 0 {
		closed := in.ClosedLoopTicks
		if closed < 0 {
			closed = 0
		}
		acc.closedLoopTicks += int64(closed)
		acc.totalControlTicks += int64(in.TotalControlTicks)
	}
}

func (acc *conditionAccumulator) buildAnchor() SiteConditionAnchor {
	anchor := SiteConditionAnchor{
		TenantID:    acc.tenantID,
		SiteID:      acc.siteID,
		Hour:        acc.hour,
		SampleCount: acc.sampleCount,
		CalcVersion: acc.calcVersion,
		UpdatedAt:   acc.updatedAt,
	}
	if acc.wetBulbCount > 0 {
		anchor.OutdoorWetBulbC = float64Ptr(acc.wetBulbSum / float64(acc.wetBulbCount))
	}
	if acc.dryBulbCount > 0 {
		anchor.OutdoorDryBulbC = float64Ptr(acc.dryBulbSum / float64(acc.dryBulbCount))
	}
	if acc.plrCount > 0 {
		anchor.PlantLoadRatio = float64Ptr(acc.plrSum / float64(acc.plrCount))
	}
	if acc.totalControlTicks > 0 {
		share := clampFloat(float64(acc.closedLoopTicks)/float64(acc.totalControlTicks), 0, 1)
		anchor.AIClosedLoopShare = float64Ptr(share)
	}
	return anchor
}

func finiteInRange(val *float64, minVal, maxVal float64) (float64, bool) {
	if val == nil || !isFiniteFloat(*val) || *val < minVal || *val > maxVal {
		return 0, false
	}
	return *val, true
}

func isFiniteFloat(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func clampFloat(v, minVal, maxVal float64) float64 {
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

func float64Ptr(v float64) *float64 {
	return &v
}
