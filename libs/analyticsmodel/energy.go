package analyticsmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	_ "time/tzdata"
)

const (
	EnergySeriesAction = "analytics.energy-series.read"
	MinimumQueryRange  = time.Millisecond
	MaximumQueryRange  = 366 * 24 * time.Hour
)

type EnergyType string

const (
	EnergyTypeElectricity EnergyType = "electricity"
	EnergyTypeCooling     EnergyType = "cooling"
)

func (value EnergyType) Valid() bool {
	return value == EnergyTypeElectricity || value == EnergyTypeCooling
}

type Granularity string

const (
	GranularityHour  Granularity = "hour"
	GranularityDay   Granularity = "day"
	GranularityMonth Granularity = "month"
)

func (value Granularity) Valid() bool {
	switch value {
	case GranularityHour, GranularityDay, GranularityMonth:
		return true
	default:
		return false
	}
}

type QualityPolicy string

const (
	QualityPolicyValidOnly       QualityPolicy = "VALID_ONLY"
	QualityPolicyValidAndSuspect QualityPolicy = "VALID_AND_SUSPECT"
)

func (value QualityPolicy) Valid() bool {
	switch value {
	case QualityPolicyValidOnly, QualityPolicyValidAndSuspect:
		return true
	default:
		return false
	}
}

// EnergyBreakdown asks for the Site's sub-metered energy split out alongside its total.
type EnergyBreakdown string

const EnergyBreakdownDevice EnergyBreakdown = "device"

type EnergySeriesQuery struct {
	TenantID      string        `json:"tenantId"`
	SiteID        string        `json:"siteId"`
	EnergyType    EnergyType    `json:"energyType"`
	Granularity   Granularity   `json:"granularity"`
	Timezone      string        `json:"timezone"`
	From          time.Time     `json:"from"`
	To            time.Time     `json:"to"`
	QualityPolicy QualityPolicy `json:"qualityPolicy"`
	// Breakdown is empty for the Site total alone.
	Breakdown EnergyBreakdown `json:"breakdown,omitempty"`
}

func (query EnergySeriesQuery) Validate() error {
	if !validUUIDv7(query.TenantID) || !validUUIDv7(query.SiteID) {
		return errors.New("analytics tenant and site must be UUIDv7")
	}
	if !query.EnergyType.Valid() {
		return errors.New("analytics energy type is invalid")
	}
	if !query.Granularity.Valid() {
		return errors.New("analytics granularity is invalid")
	}
	if strings.TrimSpace(query.Timezone) == "" || query.Timezone == "Local" {
		return errors.New("analytics timezone is invalid")
	}
	if _, err := time.LoadLocation(query.Timezone); err != nil {
		return errors.New("analytics timezone is invalid")
	}
	if query.From.IsZero() || query.To.IsZero() || !query.From.Before(query.To) {
		return errors.New("analytics time range is invalid")
	}
	if query.To.Sub(query.From) < MinimumQueryRange {
		return errors.New("analytics time range is below storage precision")
	}
	if query.To.Sub(query.From) > MaximumQueryRange {
		return errors.New("analytics time range exceeds query budget")
	}
	if !query.QualityPolicy.Valid() {
		return errors.New("analytics quality policy is invalid")
	}
	if query.Breakdown != "" && query.Breakdown != EnergyBreakdownDevice {
		return errors.New("analytics breakdown is invalid")
	}
	return nil
}

func (query EnergySeriesQuery) ScopeDigest() (string, error) {
	if err := query.Validate(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(struct {
		Action        string          `json:"action"`
		TenantID      string          `json:"tenantId"`
		SiteID        string          `json:"siteId"`
		EnergyType    EnergyType      `json:"energyType"`
		Granularity   Granularity     `json:"granularity"`
		Timezone      string          `json:"timezone"`
		From          string          `json:"from"`
		To            string          `json:"to"`
		QualityPolicy QualityPolicy   `json:"qualityPolicy"`
		Breakdown     EnergyBreakdown `json:"breakdown,omitempty"`
	}{
		Action: EnergySeriesAction, TenantID: query.TenantID, SiteID: query.SiteID,
		EnergyType: query.EnergyType, Granularity: query.Granularity, Timezone: query.Timezone,
		From: query.From.UTC().Format(time.RFC3339Nano), To: query.To.UTC().Format(time.RFC3339Nano),
		QualityPolicy: query.QualityPolicy, Breakdown: query.Breakdown,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

type EnergySeriesPoint struct {
	PeriodStart time.Time `json:"periodStart"`
	PeriodEnd   time.Time `json:"periodEnd"`
	EnergyKWh   float64   `json:"energyKWh"`
}

type QualitySummary struct {
	Valid   int64 `json:"valid"`
	Suspect int64 `json:"suspect"`
	Invalid int64 `json:"invalid"`
}

type EnergySeriesMetadata struct {
	RequestedGranularity Granularity    `json:"requestedGranularity"`
	ActualGranularity    Granularity    `json:"actualGranularity"`
	DataWatermark        *time.Time     `json:"dataWatermark,omitempty"`
	AggregateWatermark   *time.Time     `json:"aggregateWatermark,omitempty"`
	DatasetRevision      string         `json:"datasetRevision"`
	Partial              bool           `json:"partial"`
	QualitySummary       QualitySummary `json:"qualitySummary"`
}

// EnergyDeviceSeries is one piece of equipment's sub-metered energy, already part of the Site's
// total. Equipment without a sub-meter has no series.
type EnergyDeviceSeries struct {
	DeviceID string              `json:"deviceId"`
	Points   []EnergySeriesPoint `json:"points"`
}

type EnergySeriesResponse struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Points        []EnergySeriesPoint  `json:"points"`
	Devices       []EnergyDeviceSeries `json:"devices,omitempty"`
	Metadata      EnergySeriesMetadata `json:"metadata"`
}

func validUUIDv7(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded, err := hex.DecodeString(compact)
	return err == nil && len(decoded) == 16 && decoded[6]>>4 == 7 && decoded[8]>>6 == 2
}
