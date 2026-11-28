package telemetryhistorymodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

const (
	MaximumCounterAuditRange = 31 * 24 * time.Hour
	MaximumCounterAuditLimit = 500
	DefaultCounterAuditLimit = 200
)

type CounterAuditQuery struct {
	TenantID      string    `json:"tenantId"`
	SiteID        string    `json:"siteId"`
	From          time.Time `json:"from"`
	To            time.Time `json:"to"`
	DeviceID      *string   `json:"deviceId,omitempty"`
	PointID       *string   `json:"pointId,omitempty"`
	AnomaliesOnly *bool     `json:"anomaliesOnly,omitempty"`
	Limit         int       `json:"limit"`
}

func (query CounterAuditQuery) Validate() error {
	if !uuidv7.IsValidAnyCase(query.TenantID) || !uuidv7.IsValidAnyCase(query.SiteID) {
		return errors.New("counter audit scope identifiers must be UUIDv7 values")
	}
	if query.From.IsZero() || query.To.IsZero() {
		return errors.New("counter audit from and to timestamps are required")
	}
	_, fromOffset := query.From.Zone()
	_, toOffset := query.To.Zone()
	if fromOffset != 0 || toOffset != 0 {
		return errors.New("counter audit timestamps must use UTC")
	}
	if !query.From.Before(query.To) {
		return errors.New("counter audit from must be before to")
	}
	if query.To.Sub(query.From) > MaximumCounterAuditRange {
		return errors.New("counter audit window exceeds 31 days")
	}
	if query.DeviceID != nil && !uuidv7.IsValidAnyCase(*query.DeviceID) {
		return errors.New("counter audit deviceId must be a UUIDv7 value")
	}
	if query.PointID != nil && !uuidv7.IsValidAnyCase(*query.PointID) {
		return errors.New("counter audit pointId must be a UUIDv7 value")
	}
	if query.Limit < 0 || query.Limit > MaximumCounterAuditLimit {
		return errors.New("counter audit limit must be between 1 and 500")
	}
	return nil
}

func (query CounterAuditQuery) Canonical() (CounterAuditQuery, error) {
	if err := query.Validate(); err != nil {
		return CounterAuditQuery{}, err
	}
	canonical := CounterAuditQuery{
		TenantID: query.TenantID,
		SiteID:   query.SiteID,
		From:     query.From.UTC(),
		To:       query.To.UTC(),
		DeviceID: copyString(query.DeviceID),
		PointID:  copyString(query.PointID),
		Limit:    query.Limit,
	}
	if query.AnomaliesOnly != nil {
		value := *query.AnomaliesOnly
		canonical.AnomaliesOnly = &value
	}
	if canonical.Limit == 0 {
		canonical.Limit = DefaultCounterAuditLimit
	}
	return canonical, nil
}

func (query CounterAuditQuery) ScopeDigest() (string, error) {
	canonical, err := query.Canonical()
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return "counter-audit:" + hex.EncodeToString(digest[:]), nil
}

type CounterAuditSummary struct {
	TotalSamples         int64 `json:"totalSamples"`
	AnomalyCount         int64 `json:"anomalyCount"`
	ZeroDropCount        int64 `json:"zeroDropCount"`
	SpikeCount           int64 `json:"spikeCount"`
	ResetCount           int64 `json:"resetCount"`
	RolloverCount        int64 `json:"rolloverCount"`
	InvalidDecreaseCount int64 `json:"invalidDecreaseCount"`
}

type CounterAuditItem struct {
	PointID         string    `json:"pointId"`
	DeviceID        string    `json:"deviceId"`
	TelemetryKey    string    `json:"telemetryKey"`
	PeriodStart     time.Time `json:"periodStart"`
	PeriodEnd       time.Time `json:"periodEnd"`
	RawPrevious     *float64  `json:"rawPrevious"`
	RawCurrent      float64   `json:"rawCurrent"`
	CleanedDeltaKWh *float64  `json:"cleanedDeltaKWh"`
	TransitionType  string    `json:"transitionType"`
	Quality         string    `json:"quality"`
	QualityReasons  []string  `json:"qualityReasons"`
	Unit            string    `json:"unit"`
}

type CounterAuditResponse struct {
	SiteID    string              `json:"siteId"`
	From      time.Time           `json:"from"`
	To        time.Time           `json:"to"`
	Summary   CounterAuditSummary `json:"summary"`
	Items     []CounterAuditItem  `json:"items"`
	Truncated bool                `json:"truncated"`
}

func (response CounterAuditResponse) ValidateFor(canonical CounterAuditQuery) error {
	normalized, err := canonical.Canonical()
	if err != nil {
		return err
	}
	if response.SiteID != normalized.SiteID {
		return errors.New("counter audit response siteId does not match query")
	}
	if response.From.IsZero() || response.To.IsZero() {
		return errors.New("counter audit response bounds are required")
	}
	_, fromOffset := response.From.Zone()
	_, toOffset := response.To.Zone()
	if fromOffset != 0 || toOffset != 0 {
		return errors.New("counter audit response timestamps must use UTC")
	}
	if !response.From.Equal(normalized.From) || !response.To.Equal(normalized.To) {
		return errors.New("counter audit response bounds do not match canonical query")
	}
	if err := validateCounterAuditSummary(response.Summary); err != nil {
		return err
	}
	if response.Items == nil {
		return errors.New("counter audit response items must be initialized")
	}
	if len(response.Items) > normalized.Limit {
		return errors.New("counter audit response exceeds canonical limit")
	}
	for index, item := range response.Items {
		if err := validateCounterAuditItem(item, normalized); err != nil {
			return fmt.Errorf("counter audit item[%d]: %w", index, err)
		}
	}
	return nil
}

func validateCounterAuditSummary(summary CounterAuditSummary) error {
	if summary.TotalSamples < 0 ||
		summary.AnomalyCount < 0 ||
		summary.ZeroDropCount < 0 ||
		summary.SpikeCount < 0 ||
		summary.ResetCount < 0 ||
		summary.RolloverCount < 0 ||
		summary.InvalidDecreaseCount < 0 {
		return errors.New("counter audit summary counts must be non-negative")
	}
	if summary.AnomalyCount > summary.TotalSamples {
		return errors.New("counter audit anomalyCount cannot exceed totalSamples")
	}
	return nil
}

func validateCounterAuditItem(item CounterAuditItem, normalized CounterAuditQuery) error {
	if !uuidv7.IsValidAnyCase(item.PointID) || !uuidv7.IsValidAnyCase(item.DeviceID) {
		return errors.New("pointId and deviceId must be UUIDv7 values")
	}
	if normalized.DeviceID != nil && item.DeviceID != *normalized.DeviceID {
		return errors.New("deviceId does not match requested filter")
	}
	if normalized.PointID != nil && item.PointID != *normalized.PointID {
		return errors.New("pointId does not match requested filter")
	}
	if strings.TrimSpace(item.TelemetryKey) == "" {
		return errors.New("telemetryKey is required")
	}
	if item.PeriodStart.IsZero() || item.PeriodEnd.IsZero() {
		return errors.New("periodStart and periodEnd are required")
	}
	_, startOffset := item.PeriodStart.Zone()
	_, endOffset := item.PeriodEnd.Zone()
	if startOffset != 0 || endOffset != 0 {
		return errors.New("period timestamps must use UTC")
	}
	if item.PeriodEnd.Before(item.PeriodStart) {
		return errors.New("periodEnd cannot precede periodStart")
	}
	if item.PeriodEnd.Before(normalized.From) || !item.PeriodEnd.Before(normalized.To) {
		return errors.New("periodEnd falls outside requested window")
	}
	if item.RawPrevious != nil && (math.IsNaN(*item.RawPrevious) || math.IsInf(*item.RawPrevious, 0)) {
		return errors.New("rawPrevious must be finite")
	}
	if math.IsNaN(item.RawCurrent) || math.IsInf(item.RawCurrent, 0) {
		return errors.New("rawCurrent must be finite")
	}
	if item.CleanedDeltaKWh != nil && (math.IsNaN(*item.CleanedDeltaKWh) || math.IsInf(*item.CleanedDeltaKWh, 0) || *item.CleanedDeltaKWh < 0) {
		return errors.New("cleanedDeltaKWh must be finite and non-negative")
	}
	if strings.TrimSpace(item.TransitionType) == "" {
		return errors.New("transitionType is required")
	}
	if !Quality(item.Quality).Valid() {
		return errors.New("quality is invalid")
	}
	if item.QualityReasons == nil {
		return errors.New("qualityReasons must be initialized")
	}
	for _, reason := range item.QualityReasons {
		if !qualityReasonPattern.MatchString(reason) {
			return errors.New("qualityReasons contains invalid code")
		}
	}
	return nil
}
