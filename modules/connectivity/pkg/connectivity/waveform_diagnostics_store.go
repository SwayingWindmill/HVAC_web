package connectivity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

var (
	ErrWaveformInvalid    = errors.New("waveform diagnostics request is invalid")
	ErrWaveformNotFound   = errors.New("waveform diagnostics resource not found")
	ErrDiagnosticsOffline = errors.New("gateway diagnostics channel is offline or timed out")
)

type WaveformRuleRecord struct {
	ID                 string                          `json:"id"`
	TenantID           string                          `json:"tenantId"`
	SiteID             string                          `json:"siteId"`
	GatewayID          string                          `json:"gatewayId"`
	RuleName           string                          `json:"ruleName"`
	Enabled            bool                            `json:"enabled"`
	DeviceCode         string                          `json:"deviceCode"`
	PointCode          string                          `json:"pointCode"`
	TriggerType        edgecontrol.WaveformTriggerType `json:"triggerType"`
	ThresholdValue     float64                         `json:"thresholdValue"`
	DeltaWindowSeconds int                             `json:"deltaWindowSeconds"`
	PreSeconds         int                             `json:"preSeconds"`
	PostSeconds        int                             `json:"postSeconds"`
	SampleRateHz       int                             `json:"sampleRateHz"`
	PointCodes         []string                        `json:"pointCodes"`
	CooldownSeconds    int                             `json:"cooldownSeconds"`
	UpdatedBy          string                          `json:"updatedBy"`
	CreatedAt          time.Time                       `json:"createdAt"`
	UpdatedAt          time.Time                       `json:"updatedAt"`
}

type UpsertWaveformRuleInput struct {
	RuleName           string                          `json:"ruleName"`
	Enabled            bool                            `json:"enabled"`
	DeviceCode         string                          `json:"deviceCode"`
	PointCode          string                          `json:"pointCode"`
	TriggerType        edgecontrol.WaveformTriggerType `json:"triggerType"`
	ThresholdValue     float64                         `json:"thresholdValue"`
	DeltaWindowSeconds int                             `json:"deltaWindowSeconds"`
	PreSeconds         int                             `json:"preSeconds"`
	PostSeconds        int                             `json:"postSeconds"`
	SampleRateHz       int                             `json:"sampleRateHz"`
	PointCodes         []string                        `json:"pointCodes"`
	CooldownSeconds    int                             `json:"cooldownSeconds"`
}

type WaveformCaptureRecord struct {
	CaptureID     string                          `json:"captureId"`
	TenantID      string                          `json:"tenantId"`
	SiteID        string                          `json:"siteId"`
	GatewayID     string                          `json:"gatewayId"`
	RuleID        *string                         `json:"ruleId,omitempty"`
	TriggerSource string                          `json:"triggerSource"`
	TriggerReason string                          `json:"triggerReason"`
	Status        string                          `json:"status"`
	TriggeredAt   time.Time                       `json:"triggeredAt"`
	WindowStartAt time.Time                       `json:"windowStartAt"`
	WindowEndAt   time.Time                       `json:"windowEndAt"`
	SampleRateHz  int                             `json:"sampleRateHz"`
	SampleCount   int                             `json:"sampleCount"`
	Samples       []edgecontrol.WaveformSampleRow `json:"samples"`
	RequestedBy   string                          `json:"requestedBy"`
	ErrorMessage  *string                         `json:"errorMessage,omitempty"`
	CreatedAt     time.Time                       `json:"createdAt"`
	CompletedAt   *time.Time                      `json:"completedAt,omitempty"`
}

type TriggerWaveformCaptureInput struct {
	RuleID        *string  `json:"ruleId,omitempty"`
	DeviceCode    string   `json:"deviceCode,omitempty"`
	PointCode     string   `json:"pointCode,omitempty"`
	PointCodes    []string `json:"pointCodes"`
	PreSeconds    int      `json:"preSeconds"`
	PostSeconds   int      `json:"postSeconds"`
	SampleRateHz  int      `json:"sampleRateHz"`
	TriggerReason string   `json:"triggerReason"`
}

type PullGatewayLogsInput struct {
	MaxBytes     int `json:"maxBytes"`
	SinceMinutes int `json:"sinceMinutes"`
}

type GatewayLogBundleResult struct {
	RequestID     string    `json:"requestId"`
	GatewayID     string    `json:"gatewayId"`
	Status        string    `json:"status"`
	Content       string    `json:"content"`
	ReturnedBytes int       `json:"returnedBytes"`
	RedactedCount int       `json:"redactedCount"`
	Truncated     bool      `json:"truncated"`
	CollectedAt   time.Time `json:"collectedAt"`
	ErrorMessage  *string   `json:"errorMessage,omitempty"`
}

type GatewayHostMetricsRecord struct {
	ID                 string    `json:"id"`
	GatewayID          string    `json:"gatewayId"`
	CollectedAt        time.Time `json:"collectedAt"`
	CPUPercent         float64   `json:"cpuPercent"`
	MemoryUsedPercent  float64   `json:"memoryUsedPercent"`
	DiskUsedPercent    float64   `json:"diskUsedPercent"`
	UptimeSeconds      int64     `json:"uptimeSeconds"`
	RS485TxFrames      int64     `json:"rs485TxFrames"`
	RS485RxErrors      int64     `json:"rs485RxErrors"`
	RS485ErrorRatePPM  float64   `json:"rs485ErrorRatePpm"`
	MQTTRTTMs          float64   `json:"mqttRttMs"`
	MQTTTLSVersion     string    `json:"mqttTlsVersion"`
	MQTTReconnectCount int64     `json:"mqttReconnectCount"`
	ActiveRingPoints   int       `json:"activeRingPoints"`
	RingBufferBytes    int64     `json:"ringBufferBytes"`
}

type GatewayHostMetricsSummary struct {
	GatewayID string                     `json:"gatewayId"`
	Latest    *GatewayHostMetricsRecord  `json:"latest,omitempty"`
	History   []GatewayHostMetricsRecord `json:"history"`
}

// WaveformDiagnosticsStore extends diagnostics persistence without altering DiagnosticsStore.
type WaveformDiagnosticsStore interface {
	CheckGatewayScopeForDiagnostics(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) (tenantID, siteID string, err error)
	ListGatewayWaveformRules(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) ([]WaveformRuleRecord, error)
	CreateGatewayWaveformRule(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, input UpsertWaveformRuleInput) (WaveformRuleRecord, error)
	UpdateGatewayWaveformRule(ctx context.Context, claims registryauth.GrantClaims, gatewayID, ruleID string, input UpsertWaveformRuleInput) (WaveformRuleRecord, error)
	DeleteGatewayWaveformRule(ctx context.Context, claims registryauth.GrantClaims, gatewayID, ruleID string) error
	ListGatewayWaveformCaptures(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, limit int) ([]WaveformCaptureRecord, error)
	GetGatewayWaveformCapture(ctx context.Context, claims registryauth.GrantClaims, gatewayID, captureID string) (WaveformCaptureRecord, error)
	CreateManualWaveformCapture(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, input TriggerWaveformCaptureInput) (WaveformCaptureRecord, error)
	GetGatewayHostMetrics(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, limit int) (GatewayHostMetricsSummary, error)
	RecordDiagnosticsAudit(ctx context.Context, tenantID, siteID, gatewayID, action, actorID string, maxBytes, sinceMinutes, returnedBytes, redactedCount int, status, detail string) error
}

// DiagnosticsMQTTBridge publishes remote diagnostics commands and resolves synchronous log-pull replies.
type DiagnosticsMQTTBridge struct {
	mu        sync.Mutex
	publishFn func(context.Context, string, edgecontrol.DiagnosticsDownlinkPayload) error
	waiters   map[string]chan edgecontrol.DiagnosticsLogReplyPayload
}

func NewDiagnosticsMQTTBridge() *DiagnosticsMQTTBridge {
	return &DiagnosticsMQTTBridge{
		waiters: make(map[string]chan edgecontrol.DiagnosticsLogReplyPayload),
	}
}

func (b *DiagnosticsMQTTBridge) AttachPublisher(fn func(context.Context, string, edgecontrol.DiagnosticsDownlinkPayload) error) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.publishFn = fn
	b.mu.Unlock()
}

func (b *DiagnosticsMQTTBridge) CompleteLogReply(_ context.Context, _ string, reply edgecontrol.DiagnosticsLogReplyPayload) {
	if b == nil {
		return
	}
	b.mu.Lock()
	ch, ok := b.waiters[strings.TrimSpace(reply.RequestID)]
	if ok {
		delete(b.waiters, strings.TrimSpace(reply.RequestID))
	}
	b.mu.Unlock()
	if ok {
		select {
		case ch <- reply:
		default:
		}
	}
}

func (b *DiagnosticsMQTTBridge) PublishTriggerWaveform(ctx context.Context, gatewayID string, cmd edgecontrol.DiagnosticsDownlinkPayload) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	fn := b.publishFn
	b.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, gatewayID, cmd)
}

func (b *DiagnosticsMQTTBridge) RequestLogBundle(ctx context.Context, gatewayID string, cmd edgecontrol.DiagnosticsDownlinkPayload) (edgecontrol.DiagnosticsLogReplyPayload, error) {
	if b == nil {
		return edgecontrol.DiagnosticsLogReplyPayload{}, ErrDiagnosticsOffline
	}
	b.mu.Lock()
	fn := b.publishFn
	if fn == nil {
		b.mu.Unlock()
		return edgecontrol.DiagnosticsLogReplyPayload{}, ErrDiagnosticsOffline
	}
	ch := make(chan edgecontrol.DiagnosticsLogReplyPayload, 1)
	b.waiters[strings.TrimSpace(cmd.RequestID)] = ch
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.waiters, strings.TrimSpace(cmd.RequestID))
		b.mu.Unlock()
	}()

	if err := fn(ctx, gatewayID, cmd); err != nil {
		return edgecontrol.DiagnosticsLogReplyPayload{}, fmt.Errorf("%w: %v", ErrDiagnosticsOffline, err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	select {
	case reply := <-ch:
		return reply, nil
	case <-waitCtx.Done():
		return edgecontrol.DiagnosticsLogReplyPayload{}, ErrDiagnosticsOffline
	}
}

func (store *Store) writeAsTenant(ctx context.Context, tenantID string, write func(pgx.Tx) error) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return err
	}
	if err := write(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *Store) resolveGatewayScopeForDiagnostics(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) (string, string, error) {
	if store == nil || store.pool == nil {
		return "", "", errors.New("connectivity store is closed")
	}
	if !gatewayIDPattern.MatchString(gatewayID) {
		return "", "", ErrEnrollmentRejected
	}
	var tenantID, siteID string
	err := store.pool.QueryRow(ctx, `
SELECT tenant_id::text, site_id::text
FROM core_registry.gateway_directory_v1
WHERE gateway_id = $1::uuid AND status = 'ACTIVE'
`, gatewayID).Scan(&tenantID, &siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("read Registry gateway directory: %w", err)
	}
	if tenantID != claims.TenantID || !registryauth.ScopeAllows(claims, siteID) {
		return "", "", ErrCredentialForbidden
	}
	return tenantID, siteID, nil
}

func (store *Store) CheckGatewayScopeForDiagnostics(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) (string, string, error) {
	return store.resolveGatewayScopeForDiagnostics(ctx, claims, gatewayID)
}

func validateUpsertWaveformRuleInput(input UpsertWaveformRuleInput) (edgecontrol.WaveformRuleConfig, error) {
	cfg, err := edgecontrol.ValidateWaveformRuleConfig(edgecontrol.WaveformRuleConfig{
		RuleName:           input.RuleName,
		Enabled:            input.Enabled,
		DeviceCode:         input.DeviceCode,
		PointCode:          input.PointCode,
		TriggerType:        input.TriggerType,
		ThresholdValue:     input.ThresholdValue,
		DeltaWindowSeconds: input.DeltaWindowSeconds,
		PreSeconds:         input.PreSeconds,
		PostSeconds:        input.PostSeconds,
		SampleRateHz:       input.SampleRateHz,
		PointCodes:         input.PointCodes,
		CooldownSeconds:    input.CooldownSeconds,
	})
	if err != nil {
		return edgecontrol.WaveformRuleConfig{}, fmt.Errorf("%w: %v", ErrWaveformInvalid, err)
	}
	return cfg, nil
}

func (store *Store) ListGatewayWaveformRules(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) ([]WaveformRuleRecord, error) {
	tenantID, _, err := store.resolveGatewayScopeForDiagnostics(ctx, claims, gatewayID)
	if err != nil {
		return nil, err
	}
	items := make([]WaveformRuleRecord, 0)
	err = store.readAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, qErr := tx.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, gateway_id::text, rule_name, enabled,
       device_code, point_code, trigger_type, threshold_value, delta_window_seconds,
       pre_seconds, post_seconds, sample_rate_hz, point_codes_json, cooldown_seconds,
       updated_by, created_at, updated_at
FROM connectivity.gateway_waveform_rules
WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid
ORDER BY updated_at DESC, id DESC`, tenantID, gatewayID)
		if qErr != nil {
			return qErr
		}
		defer rows.Close()
		for rows.Next() {
			var rec WaveformRuleRecord
			var ptsJSON []byte
			var triggerType string
			if sErr := rows.Scan(
				&rec.ID, &rec.TenantID, &rec.SiteID, &rec.GatewayID, &rec.RuleName, &rec.Enabled,
				&rec.DeviceCode, &rec.PointCode, &triggerType, &rec.ThresholdValue, &rec.DeltaWindowSeconds,
				&rec.PreSeconds, &rec.PostSeconds, &rec.SampleRateHz, &ptsJSON, &rec.CooldownSeconds,
				&rec.UpdatedBy, &rec.CreatedAt, &rec.UpdatedAt,
			); sErr != nil {
				return sErr
			}
			rec.TriggerType = edgecontrol.WaveformTriggerType(triggerType)
			if len(ptsJSON) > 0 {
				_ = json.Unmarshal(ptsJSON, &rec.PointCodes)
			}
			rec.CreatedAt = rec.CreatedAt.UTC()
			rec.UpdatedAt = rec.UpdatedAt.UTC()
			items = append(items, rec)
		}
		return rows.Err()
	})
	return items, err
}

func (store *Store) CreateGatewayWaveformRule(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, input UpsertWaveformRuleInput) (WaveformRuleRecord, error) {
	tenantID, siteID, err := store.resolveGatewayScopeForDiagnostics(ctx, claims, gatewayID)
	if err != nil {
		return WaveformRuleRecord{}, err
	}
	cfg, err := validateUpsertWaveformRuleInput(input)
	if err != nil {
		return WaveformRuleRecord{}, err
	}
	now := store.clock().UTC()
	ruleID, err := newUUIDv7(now)
	if err != nil {
		return WaveformRuleRecord{}, err
	}
	ptsJSON, err := json.Marshal(cfg.PointCodes)
	if err != nil {
		return WaveformRuleRecord{}, err
	}
	actor := firstActorID(claims)
	rec := WaveformRuleRecord{
		ID:                 ruleID,
		TenantID:           tenantID,
		SiteID:             siteID,
		GatewayID:          gatewayID,
		RuleName:           cfg.RuleName,
		Enabled:            cfg.Enabled,
		DeviceCode:         cfg.DeviceCode,
		PointCode:          cfg.PointCode,
		TriggerType:        cfg.TriggerType,
		ThresholdValue:     cfg.ThresholdValue,
		DeltaWindowSeconds: cfg.DeltaWindowSeconds,
		PreSeconds:         cfg.PreSeconds,
		PostSeconds:        cfg.PostSeconds,
		SampleRateHz:       cfg.SampleRateHz,
		PointCodes:         cfg.PointCodes,
		CooldownSeconds:    cfg.CooldownSeconds,
		UpdatedBy:          actor,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	err = store.writeAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		_, eErr := tx.Exec(ctx, `
INSERT INTO connectivity.gateway_waveform_rules (
  id, tenant_id, site_id, gateway_id, rule_name, enabled, device_code, point_code,
  trigger_type, threshold_value, delta_window_seconds, pre_seconds, post_seconds,
  sample_rate_hz, point_codes_json, cooldown_seconds, updated_by, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8,
  $9, $10, $11, $12, $13, $14, $15::jsonb, $16, $17, $18, $19
)`, rec.ID, rec.TenantID, rec.SiteID, rec.GatewayID, rec.RuleName, rec.Enabled, rec.DeviceCode, rec.PointCode,
			string(rec.TriggerType), rec.ThresholdValue, rec.DeltaWindowSeconds, rec.PreSeconds, rec.PostSeconds,
			rec.SampleRateHz, string(ptsJSON), rec.CooldownSeconds, rec.UpdatedBy, rec.CreatedAt, rec.UpdatedAt)
		return eErr
	})
	return rec, err
}

func (store *Store) UpdateGatewayWaveformRule(ctx context.Context, claims registryauth.GrantClaims, gatewayID, ruleID string, input UpsertWaveformRuleInput) (WaveformRuleRecord, error) {
	if !gatewayIDPattern.MatchString(ruleID) {
		return WaveformRuleRecord{}, ErrWaveformNotFound
	}
	tenantID, _, err := store.resolveGatewayScopeForDiagnostics(ctx, claims, gatewayID)
	if err != nil {
		return WaveformRuleRecord{}, err
	}
	cfg, err := validateUpsertWaveformRuleInput(input)
	if err != nil {
		return WaveformRuleRecord{}, err
	}
	ptsJSON, err := json.Marshal(cfg.PointCodes)
	if err != nil {
		return WaveformRuleRecord{}, err
	}
	now := store.clock().UTC()
	actor := firstActorID(claims)
	var out WaveformRuleRecord
	err = store.writeAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var returnedPts []byte
		var trig string
		qErr := tx.QueryRow(ctx, `
UPDATE connectivity.gateway_waveform_rules
SET rule_name = $4, enabled = $5, device_code = $6, point_code = $7,
    trigger_type = $8, threshold_value = $9, delta_window_seconds = $10,
    pre_seconds = $11, post_seconds = $12, sample_rate_hz = $13,
    point_codes_json = $14::jsonb, cooldown_seconds = $15, updated_by = $16, updated_at = $17
WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid AND id = $3::uuid
RETURNING id::text, tenant_id::text, site_id::text, gateway_id::text, rule_name, enabled,
          device_code, point_code, trigger_type, threshold_value, delta_window_seconds,
          pre_seconds, post_seconds, sample_rate_hz, point_codes_json, cooldown_seconds,
          updated_by, created_at, updated_at`,
			tenantID, gatewayID, ruleID, cfg.RuleName, cfg.Enabled, cfg.DeviceCode, cfg.PointCode,
			string(cfg.TriggerType), cfg.ThresholdValue, cfg.DeltaWindowSeconds, cfg.PreSeconds, cfg.PostSeconds,
			cfg.SampleRateHz, string(ptsJSON), cfg.CooldownSeconds, actor, now,
		).Scan(
			&out.ID, &out.TenantID, &out.SiteID, &out.GatewayID, &out.RuleName, &out.Enabled,
			&out.DeviceCode, &out.PointCode, &trig, &out.ThresholdValue, &out.DeltaWindowSeconds,
			&out.PreSeconds, &out.PostSeconds, &out.SampleRateHz, &returnedPts, &out.CooldownSeconds,
			&out.UpdatedBy, &out.CreatedAt, &out.UpdatedAt,
		)
		if errors.Is(qErr, pgx.ErrNoRows) {
			return ErrWaveformNotFound
		}
		if qErr != nil {
			return qErr
		}
		out.TriggerType = edgecontrol.WaveformTriggerType(trig)
		_ = json.Unmarshal(returnedPts, &out.PointCodes)
		out.CreatedAt = out.CreatedAt.UTC()
		out.UpdatedAt = out.UpdatedAt.UTC()
		return nil
	})
	return out, err
}

func (store *Store) DeleteGatewayWaveformRule(ctx context.Context, claims registryauth.GrantClaims, gatewayID, ruleID string) error {
	if !gatewayIDPattern.MatchString(ruleID) {
		return ErrWaveformNotFound
	}
	tenantID, _, err := store.resolveGatewayScopeForDiagnostics(ctx, claims, gatewayID)
	if err != nil {
		return err
	}
	return store.writeAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		tag, dErr := tx.Exec(ctx, `
DELETE FROM connectivity.gateway_waveform_rules
WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid AND id = $3::uuid`, tenantID, gatewayID, ruleID)
		if dErr != nil {
			return dErr
		}
		if tag.RowsAffected() == 0 {
			return ErrWaveformNotFound
		}
		return nil
	})
}

func (store *Store) ListGatewayWaveformCaptures(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, limit int) ([]WaveformCaptureRecord, error) {
	tenantID, _, err := store.resolveGatewayScopeForDiagnostics(ctx, claims, gatewayID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	items := make([]WaveformCaptureRecord, 0)
	err = store.readAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, qErr := tx.Query(ctx, `
SELECT capture_id::text, tenant_id::text, site_id::text, gateway_id::text, rule_id::text,
       trigger_source, trigger_reason, status, triggered_at, window_start_at, window_end_at,
       sample_rate_hz, sample_count, '[]'::jsonb, requested_by, error_message, created_at, completed_at
FROM connectivity.gateway_waveform_captures
WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid
ORDER BY triggered_at DESC, capture_id DESC
LIMIT $3`, tenantID, gatewayID, limit)
		if qErr != nil {
			return qErr
		}
		defer rows.Close()
		for rows.Next() {
			rec, sErr := scanWaveformCaptureRow(rows)
			if sErr != nil {
				return sErr
			}
			items = append(items, rec)
		}
		return rows.Err()
	})
	return items, err
}

func (store *Store) GetGatewayWaveformCapture(ctx context.Context, claims registryauth.GrantClaims, gatewayID, captureID string) (WaveformCaptureRecord, error) {
	if !gatewayIDPattern.MatchString(captureID) {
		return WaveformCaptureRecord{}, ErrWaveformNotFound
	}
	tenantID, _, err := store.resolveGatewayScopeForDiagnostics(ctx, claims, gatewayID)
	if err != nil {
		return WaveformCaptureRecord{}, err
	}
	var rec WaveformCaptureRecord
	err = store.readAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
SELECT capture_id::text, tenant_id::text, site_id::text, gateway_id::text, rule_id::text,
       trigger_source, trigger_reason, status, triggered_at, window_start_at, window_end_at,
       sample_rate_hz, sample_count, samples_json, requested_by, error_message, created_at, completed_at
FROM connectivity.gateway_waveform_captures
WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid AND capture_id = $3::uuid`, tenantID, gatewayID, captureID)
		var sErr error
		rec, sErr = scanWaveformCaptureRow(row)
		if errors.Is(sErr, pgx.ErrNoRows) {
			return ErrWaveformNotFound
		}
		return sErr
	})
	return rec, err
}

func (store *Store) CreateManualWaveformCapture(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, input TriggerWaveformCaptureInput) (WaveformCaptureRecord, error) {
	tenantID, siteID, err := store.resolveGatewayScopeForDiagnostics(ctx, claims, gatewayID)
	if err != nil {
		return WaveformCaptureRecord{}, err
	}
	preSec := input.PreSeconds
	if preSec == 0 {
		preSec = 30
	}
	postSec := input.PostSeconds
	if postSec == 0 {
		postSec = 30
	}
	if preSec < edgecontrol.WaveformSampleMinPreSeconds || preSec > edgecontrol.WaveformSampleMaxPreSeconds {
		return WaveformCaptureRecord{}, fmt.Errorf("%w: preSeconds must be 5..300", ErrWaveformInvalid)
	}
	if postSec < edgecontrol.WaveformSampleMinPostSeconds || postSec > edgecontrol.WaveformSampleMaxPostSeconds {
		return WaveformCaptureRecord{}, fmt.Errorf("%w: postSeconds must be 5..300", ErrWaveformInvalid)
	}
	rateHz := input.SampleRateHz
	if rateHz == 0 {
		rateHz = 1
	}
	if rateHz < edgecontrol.WaveformSampleMinRateHz || rateHz > edgecontrol.WaveformSampleMaxRateHz {
		return WaveformCaptureRecord{}, fmt.Errorf("%w: sampleRateHz must be 1..10", ErrWaveformInvalid)
	}
	reason := strings.TrimSpace(input.TriggerReason)
	if reason == "" {
		reason = "运维手动下发瞬态高频波形抓拍"
	}
	if len(reason) > 512 {
		return WaveformCaptureRecord{}, fmt.Errorf("%w: triggerReason too long", ErrWaveformInvalid)
	}
	now := store.clock().UTC()
	captureID, err := newUUIDv7(now)
	if err != nil {
		return WaveformCaptureRecord{}, err
	}
	actor := firstActorID(claims)
	rec := WaveformCaptureRecord{
		CaptureID:     captureID,
		TenantID:      tenantID,
		SiteID:        siteID,
		GatewayID:     gatewayID,
		RuleID:        input.RuleID,
		TriggerSource: "MANUAL",
		TriggerReason: reason,
		Status:        "PENDING",
		TriggeredAt:   now,
		WindowStartAt: now.Add(-time.Duration(preSec) * time.Second),
		WindowEndAt:   now.Add(time.Duration(postSec) * time.Second),
		SampleRateHz:  rateHz,
		SampleCount:   0,
		Samples:       []edgecontrol.WaveformSampleRow{},
		RequestedBy:   actor,
		CreatedAt:     now,
	}
	err = store.writeAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		_, eErr := tx.Exec(ctx, `
INSERT INTO connectivity.gateway_waveform_captures (
  capture_id, tenant_id, site_id, gateway_id, rule_id, trigger_source, trigger_reason,
  status, triggered_at, window_start_at, window_end_at, sample_rate_hz, sample_count,
  samples_json, requested_by, created_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, NULLIF($5, '')::uuid, 'MANUAL', $6,
  'PENDING', $7, $8, $9, $10, 0, '[]'::jsonb, $11, $12
)`, rec.CaptureID, rec.TenantID, rec.SiteID, rec.GatewayID, derefString(rec.RuleID),
			rec.TriggerReason, rec.TriggeredAt, rec.WindowStartAt, rec.WindowEndAt, rec.SampleRateHz, rec.RequestedBy, rec.CreatedAt)
		return eErr
	})
	return rec, err
}

func (store *Store) RecordWaveformBurstUplink(ctx context.Context, tenantID, siteID, gatewayID string, burst edgecontrol.WaveformBurstPayload) error {
	if store == nil || store.pool == nil {
		return errors.New("connectivity store is closed")
	}
	now := store.clock().UTC()
	samplesJSON, err := json.Marshal(burst.Samples)
	if err != nil {
		return err
	}
	triggeredAt := time.UnixMilli(burst.TriggeredAtMs).UTC()
	winStart := time.UnixMilli(burst.WindowStartMs).UTC()
	winEnd := time.UnixMilli(burst.WindowEndMs).UTC()
	status := strings.TrimSpace(burst.Status)
	if status == "" {
		status = "COMPLETED"
	}
	source := strings.TrimSpace(burst.TriggerSource)
	if source != "MANUAL" {
		source = "AUTO_RULE"
	}
	return store.writeAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		_, eErr := tx.Exec(ctx, `
INSERT INTO connectivity.gateway_waveform_captures (
  capture_id, tenant_id, site_id, gateway_id, rule_id, trigger_source, trigger_reason,
  status, triggered_at, window_start_at, window_end_at, sample_rate_hz, sample_count,
  samples_json, requested_by, error_message, created_at, completed_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, NULLIF($5, '')::uuid, $6, $7,
  $8, $9, $10, $11, $12, $13, $14::jsonb, $15, NULLIF($16, ''), $17, $17
)
ON CONFLICT (capture_id) DO UPDATE SET
  status = EXCLUDED.status,
  sample_count = EXCLUDED.sample_count,
  samples_json = EXCLUDED.samples_json,
  error_message = EXCLUDED.error_message,
  completed_at = EXCLUDED.completed_at`,
			burst.CaptureID, tenantID, siteID, gatewayID, strings.TrimSpace(burst.RuleID),
			source, fallbackReason(burst.TriggerReason), status, triggeredAt, winStart, winEnd,
			max(burst.SampleRateHz, 1), len(burst.Samples), string(samplesJSON),
			fallbackActor(burst.RequestedBy), strings.TrimSpace(burst.ErrorMessage), now)
		return eErr
	})
}

func (store *Store) RecordGatewayHostMetrics(ctx context.Context, tenantID, siteID, gatewayID string, m edgecontrol.GatewayHostMetricsPayload) error {
	if store == nil || store.pool == nil {
		return errors.New("connectivity store is closed")
	}
	now := store.clock().UTC()
	collectedAt := time.UnixMilli(m.CollectedAtMs).UTC()
	if m.CollectedAtMs <= 0 {
		collectedAt = now
	}
	id, err := newUUIDv7(now)
	if err != nil {
		return err
	}
	return store.writeAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		_, eErr := tx.Exec(ctx, `
INSERT INTO connectivity.gateway_host_metrics (
  id, tenant_id, site_id, gateway_id, collected_at, cpu_percent, memory_used_percent, disk_used_percent,
  uptime_seconds, rs485_tx_frames, rs485_rx_errors, rs485_error_rate_ppm, mqtt_rtt_ms,
  mqtt_tls_version, mqtt_reconnect_count, active_ring_points, ring_buffer_bytes
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8,
  $9, $10, $11, $12, $13, $14, $15, $16, $17
)`, id, tenantID, siteID, gatewayID, collectedAt, m.CPUPercent, m.MemoryUsedPercent, m.DiskUsedPercent,
			m.UptimeSeconds, m.RS485TxFrames, m.RS485RxErrors, m.RS485ErrorRatePPM, m.MQTTRTTMs,
			m.MQTTTLSVersion, m.MQTTReconnectCount, m.ActiveRingPoints, m.RingBufferBytes)
		return eErr
	})
}

func (store *Store) GetGatewayHostMetrics(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, limit int) (GatewayHostMetricsSummary, error) {
	tenantID, _, err := store.resolveGatewayScopeForDiagnostics(ctx, claims, gatewayID)
	if err != nil {
		return GatewayHostMetricsSummary{}, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 60 {
		limit = 60
	}
	history := make([]GatewayHostMetricsRecord, 0)
	err = store.readAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, qErr := tx.Query(ctx, `
SELECT id::text, gateway_id::text, collected_at, cpu_percent, memory_used_percent, disk_used_percent,
       uptime_seconds, rs485_tx_frames, rs485_rx_errors, rs485_error_rate_ppm, mqtt_rtt_ms,
       mqtt_tls_version, mqtt_reconnect_count, active_ring_points, ring_buffer_bytes
FROM connectivity.gateway_host_metrics
WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid
ORDER BY collected_at DESC, id DESC
LIMIT $3`, tenantID, gatewayID, limit)
		if qErr != nil {
			return qErr
		}
		defer rows.Close()
		for rows.Next() {
			var rec GatewayHostMetricsRecord
			if sErr := rows.Scan(
				&rec.ID, &rec.GatewayID, &rec.CollectedAt, &rec.CPUPercent, &rec.MemoryUsedPercent,
				&rec.DiskUsedPercent, &rec.UptimeSeconds, &rec.RS485TxFrames, &rec.RS485RxErrors,
				&rec.RS485ErrorRatePPM, &rec.MQTTRTTMs, &rec.MQTTTLSVersion, &rec.MQTTReconnectCount,
				&rec.ActiveRingPoints, &rec.RingBufferBytes,
			); sErr != nil {
				return sErr
			}
			rec.CollectedAt = rec.CollectedAt.UTC()
			history = append(history, rec)
		}
		return rows.Err()
	})
	if err != nil {
		return GatewayHostMetricsSummary{}, err
	}
	var latest *GatewayHostMetricsRecord
	if len(history) > 0 {
		first := history[0]
		latest = &first
	}
	return GatewayHostMetricsSummary{
		GatewayID: gatewayID,
		Latest:    latest,
		History:   history,
	}, nil
}

func (store *Store) RecordDiagnosticsAudit(ctx context.Context, tenantID, siteID, gatewayID, action, actorID string, maxBytes, sinceMinutes, returnedBytes, redactedCount int, status, detail string) error {
	if store == nil || store.pool == nil {
		return nil
	}
	now := store.clock().UTC()
	id, err := newUUIDv7(now)
	if err != nil {
		return err
	}
	if len(detail) > 1024 {
		detail = detail[:1024]
	}
	return store.writeAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		_, eErr := tx.Exec(ctx, `
INSERT INTO connectivity.gateway_diagnostics_audit (
  id, tenant_id, site_id, gateway_id, action, actor_id, max_bytes, since_minutes,
  returned_bytes, redacted_count, status, detail, created_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10, $11, $12, $13
)`, id, tenantID, siteID, gatewayID, action, fallbackActor(actorID), maxBytes, sinceMinutes, returnedBytes, redactedCount, status, detail, now)
		return eErr
	})
}

func scanWaveformCaptureRow(row rowScanner) (WaveformCaptureRecord, error) {
	var rec WaveformCaptureRecord
	var ruleID *string
	var samplesJSON []byte
	if err := row.Scan(
		&rec.CaptureID, &rec.TenantID, &rec.SiteID, &rec.GatewayID, &ruleID,
		&rec.TriggerSource, &rec.TriggerReason, &rec.Status, &rec.TriggeredAt,
		&rec.WindowStartAt, &rec.WindowEndAt, &rec.SampleRateHz, &rec.SampleCount,
		&samplesJSON, &rec.RequestedBy, &rec.ErrorMessage, &rec.CreatedAt, &rec.CompletedAt,
	); err != nil {
		return WaveformCaptureRecord{}, err
	}
	rec.RuleID = ruleID
	rec.Samples = []edgecontrol.WaveformSampleRow{}
	if len(samplesJSON) > 0 {
		_ = json.Unmarshal(samplesJSON, &rec.Samples)
	}
	rec.TriggeredAt = rec.TriggeredAt.UTC()
	rec.WindowStartAt = rec.WindowStartAt.UTC()
	rec.WindowEndAt = rec.WindowEndAt.UTC()
	rec.CreatedAt = rec.CreatedAt.UTC()
	if rec.CompletedAt != nil {
		t := rec.CompletedAt.UTC()
		rec.CompletedAt = &t
	}
	return rec, nil
}

func firstActorID(claims registryauth.GrantClaims) string {
	if s := strings.TrimSpace(claims.PrincipalID); s != "" {
		return s
	}
	return "operator"
}

func fallbackActor(s string) string {
	if t := strings.TrimSpace(s); t != "" {
		return t
	}
	return "edge-daemon"
}

func fallbackReason(s string) string {
	if t := strings.TrimSpace(s); t != "" {
		return t
	}
	return "自动触发瞬态波形抓拍"
}

func derefString(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return strings.TrimSpace(*ptr)
}
