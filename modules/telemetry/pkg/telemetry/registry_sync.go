package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const InternalRegistrySyncPath = "/internal/v1/telemetry/registry-sync:apply"

var registryUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// RegistrySyncRequest pushes authoritative Registry device and point policy state to Telemetry.
type RegistrySyncRequest struct {
	TenantID      string                 `json:"tenantId"`
	SiteID        string                 `json:"siteId"`
	Devices       []RegistryDeviceSync   `json:"devices,omitempty"`
	RetiredPoints []RegistryRetiredPoint `json:"retiredPoints,omitempty"`
}

// RegistryDeviceSync carries one Registry device's current lifecycle and non-command points.
type RegistryDeviceSync struct {
	DeviceID              string              `json:"deviceId"`
	Status                string              `json:"status"`
	PresenceApplicability string              `json:"presenceApplicability,omitempty"`
	Points                []RegistryPointSync `json:"points,omitempty"`
}

// RegistryPointSync describes a telemetry point policy derived from Registry metadata.
type RegistryPointSync struct {
	PointID                       string         `json:"pointId"`
	SensorID                      string         `json:"sensorId,omitempty"`
	TelemetryKey                  string         `json:"telemetryKey"`
	PointType                     string         `json:"pointType"`
	ValueType                     string         `json:"valueType"`
	Unit                          string         `json:"unit,omitempty"`
	SampleIntervalSeconds         int            `json:"sampleIntervalSeconds,omitempty"`
	FreshWithinSeconds            int            `json:"freshWithinSeconds,omitempty"`
	ExpectedSampleIntervalSeconds int            `json:"expectedSampleIntervalSeconds,omitempty"`
	Status                        string         `json:"status"`
	MinimumNumber                 *float64       `json:"minimumNumber,omitempty"`
	MaximumNumber                 *float64       `json:"maximumNumber,omitempty"`
	QualityChecks                 map[string]any `json:"qualityChecks,omitempty"`
}

// RegistryRetiredPoint identifies a point that must be marked unconfigured in Telemetry.
type RegistryRetiredPoint struct {
	DeviceID     string `json:"deviceId"`
	PointID      string `json:"pointId,omitempty"`
	TelemetryKey string `json:"telemetryKey"`
}

// RegistrySyncResult summarizes the applied Telemetry policy updates.
type RegistrySyncResult struct {
	SyncedDevices int `json:"syncedDevices"`
	RetiredPoints int `json:"retiredPoints"`
}

// RegistrySyncer persists authoritative Registry synchronization updates into Telemetry runtime state.
type RegistrySyncer interface {
	ApplyRegistrySync(context.Context, RegistrySyncRequest) (RegistrySyncResult, error)
}

var ErrRegistrySyncInvalid = errors.New("telemetry registry sync request is invalid")

func (request RegistrySyncRequest) validate() error {
	if !registryUUIDPattern.MatchString(strings.TrimSpace(request.TenantID)) || !registryUUIDPattern.MatchString(strings.TrimSpace(request.SiteID)) {
		return ErrRegistrySyncInvalid
	}
	for _, dev := range request.Devices {
		if !registryUUIDPattern.MatchString(strings.TrimSpace(dev.DeviceID)) {
			return ErrRegistrySyncInvalid
		}
		switch strings.ToUpper(strings.TrimSpace(dev.Status)) {
		case "ACTIVE", "INACTIVE", "RETIRED":
		default:
			return ErrRegistrySyncInvalid
		}
		if app := strings.TrimSpace(dev.PresenceApplicability); app != "" && app != "APPLICABLE" && app != "NOT_APPLICABLE" {
			return ErrRegistrySyncInvalid
		}
		for _, pt := range dev.Points {
			if !telemetryKeyPattern.MatchString(strings.TrimSpace(pt.TelemetryKey)) {
				return ErrRegistrySyncInvalid
			}
			switch strings.ToUpper(strings.TrimSpace(pt.Status)) {
			case "ACTIVE", "INACTIVE", "RETIRED":
			default:
				return ErrRegistrySyncInvalid
			}
			if strings.ToUpper(strings.TrimSpace(pt.PointType)) == "COMMAND" {
				continue
			}
			valueType := strings.ToUpper(strings.TrimSpace(pt.ValueType))
			switch valueType {
			case "NUMBER", "STRING", "BOOLEAN", "JSON":
			default:
				return ErrRegistrySyncInvalid
			}
			if _, _, _, err := extractRegistryPointQuality(pt, valueType); err != nil {
				return err
			}
		}
	}
	for _, rp := range request.RetiredPoints {
		if !registryUUIDPattern.MatchString(strings.TrimSpace(rp.DeviceID)) || !telemetryKeyPattern.MatchString(strings.TrimSpace(rp.TelemetryKey)) {
			return ErrRegistrySyncInvalid
		}
	}
	return nil
}

func extractRegistryPointQuality(pt RegistryPointSync, valueType string) (any, any, any, error) {
	var minNum, maxNum any
	if valueType == "NUMBER" {
		if pt.MinimumNumber != nil {
			if math.IsNaN(*pt.MinimumNumber) || math.IsInf(*pt.MinimumNumber, 0) {
				return nil, nil, nil, fmt.Errorf("%w: invalid minimumNumber", ErrRegistrySyncInvalid)
			}
			v := *pt.MinimumNumber
			minNum = v
		}
		if pt.MaximumNumber != nil {
			if math.IsNaN(*pt.MaximumNumber) || math.IsInf(*pt.MaximumNumber, 0) {
				return nil, nil, nil, fmt.Errorf("%w: invalid maximumNumber", ErrRegistrySyncInvalid)
			}
			v := *pt.MaximumNumber
			maxNum = v
		}
		if pt.MinimumNumber != nil && pt.MaximumNumber != nil && *pt.MaximumNumber < *pt.MinimumNumber {
			return nil, nil, nil, fmt.Errorf("%w: maximumNumber < minimumNumber", ErrRegistrySyncInvalid)
		}
	}
	var qualityChecksParam any
	if len(pt.QualityChecks) > 0 {
		qcRaw, err := json.Marshal(pt.QualityChecks)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%w: marshal qualityChecks: %v", ErrRegistrySyncInvalid, err)
		}
		if len(qcRaw) > 4096 {
			return nil, nil, nil, fmt.Errorf("%w: qualityChecks exceeds 4096 bytes", ErrRegistrySyncInvalid)
		}
		if _, err := parseQualityChecks(qcRaw); err != nil {
			return nil, nil, nil, fmt.Errorf("%w: invalid qualityChecks: %v", ErrRegistrySyncInvalid, err)
		}
		qualityChecksParam = string(qcRaw)
	}
	return minNum, maxNum, qualityChecksParam, nil
}

func normalizeFreshnessTiming(pt RegistryPointSync) (sampleSec, freshSec int) {
	sampleSec = pt.ExpectedSampleIntervalSeconds
	if sampleSec <= 0 {
		sampleSec = pt.SampleIntervalSeconds
	}
	if sampleSec <= 0 {
		sampleSec = 60
	}
	if sampleSec > 604800 {
		sampleSec = 604800
	}
	freshSec = pt.FreshWithinSeconds
	if freshSec < sampleSec {
		freshSec = sampleSec * 2
		if freshSec < 300 {
			freshSec = 300
		}
	}
	if freshSec > 604800 {
		freshSec = 604800
	}
	return sampleSec, freshSec
}

func truncateTelemetryUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	cut := 0
	for cut < len(value) {
		_, size := utf8.DecodeRuneInString(value[cut:])
		if cut+size > maxBytes {
			break
		}
		cut += size
	}
	return value[:cut]
}

// ApplyRegistrySync upserts Telemetry devices, default presence/coverage policies, and freshness policies.
func (store *PostgresStore) ApplyRegistrySync(ctx context.Context, request RegistrySyncRequest) (RegistrySyncResult, error) {
	if store == nil || store.pool == nil {
		return RegistrySyncResult{}, errors.New("telemetry store is not configured")
	}
	if err := request.validate(); err != nil {
		return RegistrySyncResult{}, err
	}
	now := time.Now().UTC()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return RegistrySyncResult{}, fmt.Errorf("begin registry sync tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return RegistrySyncResult{}, fmt.Errorf("set telemetry runtime role: %w", err)
	}

	tenantID := strings.TrimSpace(request.TenantID)
	siteID := strings.TrimSpace(request.SiteID)

	deviceSet := make(map[string]struct{}, len(request.Devices)+len(request.RetiredPoints))
	syncedSet := make(map[string]bool, len(request.Devices))
	for _, dev := range request.Devices {
		id := strings.TrimSpace(dev.DeviceID)
		deviceSet[id] = struct{}{}
		syncedSet[id] = true
	}
	for _, rp := range request.RetiredPoints {
		deviceSet[strings.TrimSpace(rp.DeviceID)] = struct{}{}
	}
	lockedIDs := make([]string, 0, len(deviceSet))
	for id := range deviceSet {
		lockedIDs = append(lockedIDs, id)
	}
	sort.Strings(lockedIDs)
	for _, id := range lockedIDs {
		if err := lockDevice(ctx, tx, id); err != nil {
			return RegistrySyncResult{}, err
		}
	}

	devicesToEvaluate := make(map[string]bool, len(lockedIDs))
	for _, dev := range request.Devices {
		deviceID := strings.TrimSpace(dev.DeviceID)
		status := strings.ToUpper(strings.TrimSpace(dev.Status))
		applicability := strings.TrimSpace(dev.PresenceApplicability)
		if status == "RETIRED" {
			applicability = "NOT_APPLICABLE"
		} else if applicability == "" {
			applicability = "APPLICABLE"
		}

		var prevApplicability string
		prevErr := tx.QueryRow(ctx, `SELECT presence_applicability FROM telemetry_runtime.devices WHERE device_id = $1::uuid`, deviceID).Scan(&prevApplicability)
		if prevErr != nil {
			if !errors.Is(prevErr, pgx.ErrNoRows) {
				return RegistrySyncResult{}, fmt.Errorf("load telemetry device %s: %w", deviceID, prevErr)
			}
			devicesToEvaluate[deviceID] = true
		} else if prevApplicability != applicability {
			devicesToEvaluate[deviceID] = true
		}

		if _, err := tx.Exec(ctx, `
INSERT INTO telemetry_runtime.devices (device_id, tenant_id, site_id, presence_applicability, updated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5)
ON CONFLICT (device_id) DO UPDATE SET
  tenant_id = EXCLUDED.tenant_id,
  site_id = EXCLUDED.site_id,
  presence_applicability = EXCLUDED.presence_applicability,
  updated_at = EXCLUDED.updated_at
`, deviceID, tenantID, siteID, applicability, now); err != nil {
			return RegistrySyncResult{}, fmt.Errorf("upsert telemetry device %s: %w", deviceID, err)
		}

		if _, err := tx.Exec(ctx, `
INSERT INTO telemetry_runtime.presence_policies (
  device_id, policy_revision, online_within_seconds, offline_after_seconds,
  coverage_required, accepted_signal_types, max_future_clock_skew_seconds,
  max_source_lag_seconds, updated_at
) VALUES (
  $1::uuid, 1, 120, 300, true,
  ARRAY['SOURCE_ACTIVITY', 'EXPLICIT_CONNECT', 'EXPLICIT_DISCONNECT']::text[],
  60, 86400, $2
)
ON CONFLICT (device_id) DO NOTHING
`, deviceID, now); err != nil {
			return RegistrySyncResult{}, fmt.Errorf("ensure telemetry presence policy %s: %w", deviceID, err)
		}

		if _, err := tx.Exec(ctx, `
INSERT INTO telemetry_runtime.observation_coverage (
  device_id, available, continuous_since, reason_code, source_revision, updated_at
) VALUES (
  $1::uuid, true, $2, NULL, 1, $2
)
ON CONFLICT (device_id) DO NOTHING
`, deviceID, now); err != nil {
			return RegistrySyncResult{}, fmt.Errorf("ensure telemetry observation coverage %s: %w", deviceID, err)
		}

		for _, pt := range dev.Points {
			if strings.ToUpper(strings.TrimSpace(pt.PointType)) == "COMMAND" {
				continue
			}
			key := strings.TrimSpace(pt.TelemetryKey)
			valueType := strings.ToUpper(strings.TrimSpace(pt.ValueType))
			ptStatus := strings.ToUpper(strings.TrimSpace(pt.Status))
			configured := status == "ACTIVE" && ptStatus == "ACTIVE"
			sampleSec, freshSec := normalizeFreshnessTiming(pt)

			var expectedUnit any
			if unit := truncateTelemetryUTF8(strings.TrimSpace(pt.Unit), 64); unit != "" {
				expectedUnit = unit
			}

			minNum, maxNum, qualityChecksParam, err := extractRegistryPointQuality(pt, valueType)
			if err != nil {
				return RegistrySyncResult{}, fmt.Errorf("telemetry point %s/%s: %w", deviceID, key, err)
			}

			tag, err := tx.Exec(ctx, `
INSERT INTO telemetry_runtime.freshness_policies (
  device_id, telemetry_key, policy_revision, fresh_within_seconds, configured,
  expected_sample_interval_seconds, value_type, expected_unit,
  minimum_number, maximum_number, quality_checks, updated_at
) VALUES (
  $1::uuid, $2, 1, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11
)
ON CONFLICT (device_id, telemetry_key) DO UPDATE SET
  policy_revision = telemetry_runtime.freshness_policies.policy_revision + 1,
  fresh_within_seconds = EXCLUDED.fresh_within_seconds,
  configured = EXCLUDED.configured,
  expected_sample_interval_seconds = EXCLUDED.expected_sample_interval_seconds,
  value_type = EXCLUDED.value_type,
  expected_unit = EXCLUDED.expected_unit,
  minimum_number = CASE
    WHEN EXCLUDED.value_type <> 'NUMBER' THEN NULL
    WHEN EXCLUDED.minimum_number IS NOT NULL THEN EXCLUDED.minimum_number
    WHEN EXCLUDED.maximum_number IS NOT NULL AND telemetry_runtime.freshness_policies.minimum_number > EXCLUDED.maximum_number THEN NULL
    ELSE telemetry_runtime.freshness_policies.minimum_number
  END,
  maximum_number = CASE
    WHEN EXCLUDED.value_type <> 'NUMBER' THEN NULL
    WHEN EXCLUDED.maximum_number IS NOT NULL THEN EXCLUDED.maximum_number
    WHEN EXCLUDED.minimum_number IS NOT NULL AND telemetry_runtime.freshness_policies.maximum_number < EXCLUDED.minimum_number THEN NULL
    ELSE telemetry_runtime.freshness_policies.maximum_number
  END,
  quality_checks = COALESCE(EXCLUDED.quality_checks, telemetry_runtime.freshness_policies.quality_checks),
  updated_at = EXCLUDED.updated_at
WHERE (
  telemetry_runtime.freshness_policies.configured,
  telemetry_runtime.freshness_policies.expected_sample_interval_seconds,
  telemetry_runtime.freshness_policies.fresh_within_seconds,
  telemetry_runtime.freshness_policies.value_type,
  telemetry_runtime.freshness_policies.expected_unit,
  telemetry_runtime.freshness_policies.minimum_number,
  telemetry_runtime.freshness_policies.maximum_number,
  telemetry_runtime.freshness_policies.quality_checks
) IS DISTINCT FROM (
  EXCLUDED.configured,
  EXCLUDED.expected_sample_interval_seconds,
  EXCLUDED.fresh_within_seconds,
  EXCLUDED.value_type,
  EXCLUDED.expected_unit,
  CASE
    WHEN EXCLUDED.value_type <> 'NUMBER' THEN NULL
    WHEN EXCLUDED.minimum_number IS NOT NULL THEN EXCLUDED.minimum_number
    WHEN EXCLUDED.maximum_number IS NOT NULL AND telemetry_runtime.freshness_policies.minimum_number > EXCLUDED.maximum_number THEN NULL
    ELSE telemetry_runtime.freshness_policies.minimum_number
  END,
  CASE
    WHEN EXCLUDED.value_type <> 'NUMBER' THEN NULL
    WHEN EXCLUDED.maximum_number IS NOT NULL THEN EXCLUDED.maximum_number
    WHEN EXCLUDED.minimum_number IS NOT NULL AND telemetry_runtime.freshness_policies.maximum_number < EXCLUDED.minimum_number THEN NULL
    ELSE telemetry_runtime.freshness_policies.maximum_number
  END,
  COALESCE(EXCLUDED.quality_checks, telemetry_runtime.freshness_policies.quality_checks)
)
`, deviceID, key, freshSec, configured, sampleSec, valueType, expectedUnit, minNum, maxNum, qualityChecksParam, now)
			if err != nil {
				return RegistrySyncResult{}, fmt.Errorf("upsert telemetry freshness policy %s/%s: %w", deviceID, key, err)
			}
			if tag.RowsAffected() > 0 {
				devicesToEvaluate[deviceID] = true
			}
		}

		if len(dev.Points) > 0 || status == "RETIRED" {
			devicesToEvaluate[deviceID] = true
		}
	}

	for _, rp := range request.RetiredPoints {
		rpDeviceID := strings.TrimSpace(rp.DeviceID)
		rpKey := strings.TrimSpace(rp.TelemetryKey)
		if _, err := tx.Exec(ctx, `
UPDATE telemetry_runtime.freshness_policies
SET configured = false, policy_revision = policy_revision + 1, updated_at = $3
WHERE device_id = $1::uuid AND telemetry_key = $2 AND configured IS DISTINCT FROM false
`, rpDeviceID, rpKey, now); err != nil {
			return RegistrySyncResult{}, fmt.Errorf("retire telemetry point %s/%s: %w", rp.DeviceID, rp.TelemetryKey, err)
		}
		devicesToEvaluate[rpDeviceID] = true
	}

	for _, deviceID := range lockedIDs {
		if !devicesToEvaluate[deviceID] {
			continue
		}
		if !syncedSet[deviceID] {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM telemetry_runtime.devices WHERE device_id = $1::uuid)`, deviceID).Scan(&exists); err != nil {
				return RegistrySyncResult{}, fmt.Errorf("check telemetry device existence %s: %w", deviceID, err)
			}
			if !exists {
				continue
			}
		}
		if _, err := store.evaluateAndPersistDevice(ctx, tx, deviceID, nil, now); err != nil {
			return RegistrySyncResult{}, fmt.Errorf("evaluate telemetry device %s: %w", deviceID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return RegistrySyncResult{}, fmt.Errorf("commit registry sync: %w", err)
	}
	return RegistrySyncResult{
		SyncedDevices: len(request.Devices),
		RetiredPoints: len(request.RetiredPoints),
	}, nil
}

func (h *handler) handleRegistrySync(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeProblem(writer, request, http.StatusMethodNotAllowed, "TELEMETRY_METHOD_NOT_ALLOWED", "This telemetry route only supports POST.", false)
		return
	}
	if hasForgedIdentityHeader(request.Header) {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_FORGED_IDENTITY_HEADER", "Caller-supplied identity headers are not accepted.", false)
		return
	}
	peer, ok := verifiedPeerSPIFFE(request)
	if !ok {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_WORKLOAD_IDENTITY_INVALID", "A verified client mTLS SPIFFE identity is required.", false)
		return
	}
	if _, allowed := h.allowedRegistrySyncSPIFFEs[peer]; !allowed {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not authorized for registry sync.", false)
		return
	}
	if h.registrySyncer == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_RUNTIME_UNAVAILABLE", "Telemetry registry sync is not configured.", true)
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, 1<<20)
	var input RegistrySyncRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "The registry sync payload is invalid.", false)
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "Trailing content is not allowed in registry sync payload.", false)
		return
	}
	if err := input.validate(); err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "The registry sync request failed validation.", false)
		return
	}

	result, err := h.registrySyncer.ApplyRegistrySync(request.Context(), input)
	if err != nil {
		if errors.Is(err, ErrRegistrySyncInvalid) {
			writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "The registry sync request failed validation.", false)
			return
		}
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_RUNTIME_UNAVAILABLE", "Telemetry registry sync could not be applied.", true)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}
