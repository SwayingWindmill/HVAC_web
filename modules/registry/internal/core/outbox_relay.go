package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const (
	outboxRelayConsumerName     = "registry-lifecycle-relay"
	outboxDefaultLeaseDuration  = 30 * time.Second
	outboxDefaultPollInterval   = 500 * time.Millisecond
	outboxDefaultBatchSize      = 16
	outboxMaxDeliveryAttempts   = 10
	outboxTelemetrySyncPath     = "/internal/v1/telemetry/registry-sync:apply"
	outboxCommandCancelPath     = "/internal/v1/commands:cancelRetiredTarget"
	outboxConnectivityRetireFmt = "/internal/v1/gateways/%s:retire"
)

func truncateRelayUTF8(value string, maxBytes int) string {
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

var relayedOutboxEventTypes = []string{
	"registry.device.created",
	"registry.device.updated",
	"registry.point.created",
	"registry.point.updated",
	"registry.import.committed",
	"registry.retirement.completed",
	"registry.template-revision.released",
	"registry.template-assignment.changed",
}

// TelemetrySyncClient delivers authoritative device and point updates to Telemetry runtime.
type TelemetrySyncClient interface {
	SyncRegistry(ctx context.Context, request RelayTelemetrySyncRequest) error
}

// ConnectivityRetireClient notifies Connectivity to revoke retired gateways.
type ConnectivityRetireClient interface {
	RetireGateway(ctx context.Context, tenantID, gatewayID string) error
}

// CommandCancelClient notifies Command service to cancel pending commands for retired devices.
type CommandCancelClient interface {
	CancelRetiredTarget(ctx context.Context, request RelayCommandCancelRequest) error
}

// RelayTelemetrySyncRequest mirrors telemetry.RegistrySyncRequest across module boundaries.
type RelayTelemetrySyncRequest struct {
	TenantID      string                   `json:"tenantId"`
	SiteID        string                   `json:"siteId"`
	Devices       []RelayTelemetryDevice   `json:"devices,omitempty"`
	RetiredPoints []RelayTelemetryRetirePt `json:"retiredPoints,omitempty"`
}

type RelayTelemetryDevice struct {
	DeviceID              string                `json:"deviceId"`
	Status                string                `json:"status"`
	PresenceApplicability string                `json:"presenceApplicability,omitempty"`
	Points                []RelayTelemetryPoint `json:"points,omitempty"`
}

type RelayTelemetryPoint struct {
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

type RelayTelemetryRetirePt struct {
	DeviceID     string `json:"deviceId"`
	PointID      string `json:"pointId,omitempty"`
	TelemetryKey string `json:"telemetryKey"`
}

type RelayCommandCancelRequest struct {
	TenantID    string   `json:"tenantId"`
	SiteID      string   `json:"siteId"`
	DeviceIDs   []string `json:"deviceIds"`
	CausationID string   `json:"causationId"`
}

// OutboxRelayConfig configures HTTP endpoints, clients, and polling parameters for OutboxRelay.
type OutboxRelayConfig struct {
	TelemetryBaseURL    string
	TelemetryHTTPClient *http.Client
	TelemetryClient     TelemetrySyncClient

	ConnectivityBaseURL    string
	ConnectivityHTTPClient *http.Client
	ConnectivityClient     ConnectivityRetireClient

	CommandBaseURL          string
	CommandHTTPClient       *http.Client
	CommandClient           CommandCancelClient
	CommandCapabilityClient CommandCapabilitySyncClient

	LeaseOwner   string
	LeaseFor     time.Duration
	PollInterval time.Duration
	BatchSize    int
	Now          func() time.Time
	Logger       *slog.Logger
}

// OutboxRelay claims undelivered core_registry.domain_outbox_events rows and pushes
// authoritative downstream updates via mTLS endpoints.
type OutboxRelay struct {
	store             *PostgresStore
	telemetry         TelemetrySyncClient
	connectivity      ConnectivityRetireClient
	command           CommandCancelClient
	commandCapability CommandCapabilitySyncClient
	leaseOwner        string
	leaseFor          time.Duration
	pollInterval      time.Duration
	batchSize         int
	now               func() time.Time
	logger            *slog.Logger
}

type claimedOutboxEvent struct {
	EventID       string
	TenantID      string
	SiteID        string
	EventType     string
	SubjectID     string
	AggregateType string
	AggregateID   string
	Attempt       int
	CreatedAt     time.Time
	Payload       map[string]any
}

// NewOutboxRelay constructs an OutboxRelay backed by PostgresStore.
func NewOutboxRelay(store *PostgresStore, config OutboxRelayConfig) (*OutboxRelay, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("outbox relay requires configured postgres store")
	}
	telemetryClient := config.TelemetryClient
	if telemetryClient == nil && strings.TrimSpace(config.TelemetryBaseURL) != "" {
		httpClient := config.TelemetryHTTPClient
		if httpClient == nil {
			httpClient = newDefaultOutboxHTTPClient(10 * time.Second)
		}
		telemetryClient = &httpDownstreamClient{baseURL: strings.TrimRight(strings.TrimSpace(config.TelemetryBaseURL), "/"), client: httpClient}
	}
	connectivityClient := config.ConnectivityClient
	if connectivityClient == nil && strings.TrimSpace(config.ConnectivityBaseURL) != "" {
		httpClient := config.ConnectivityHTTPClient
		if httpClient == nil {
			httpClient = newDefaultOutboxHTTPClient(10 * time.Second)
		}
		connectivityClient = &httpDownstreamClient{baseURL: strings.TrimRight(strings.TrimSpace(config.ConnectivityBaseURL), "/"), client: httpClient}
	}
	commandClient := config.CommandClient
	if commandClient == nil && strings.TrimSpace(config.CommandBaseURL) != "" {
		httpClient := config.CommandHTTPClient
		if httpClient == nil {
			httpClient = newDefaultOutboxHTTPClient(10 * time.Second)
		}
		commandClient = &httpDownstreamClient{baseURL: strings.TrimRight(strings.TrimSpace(config.CommandBaseURL), "/"), client: httpClient}
	}

	commandCapabilityClient := config.CommandCapabilityClient
	if commandCapabilityClient == nil {
		if syncer, ok := commandClient.(CommandCapabilitySyncClient); ok {
			commandCapabilityClient = syncer
		}
	}

	leaseOwner := strings.TrimSpace(config.LeaseOwner)
	if leaseOwner == "" {
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "local"
		}
		leaseOwner = fmt.Sprintf("core-outbox-relay-%s-%d", hostname, os.Getpid())
	}
	leaseFor := config.LeaseFor
	if leaseFor <= 0 {
		leaseFor = outboxDefaultLeaseDuration
	}
	pollInterval := config.PollInterval
	if pollInterval <= 0 {
		pollInterval = outboxDefaultPollInterval
	}
	batchSize := config.BatchSize
	if batchSize <= 0 {
		batchSize = outboxDefaultBatchSize
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &OutboxRelay{
		store:             store,
		telemetry:         telemetryClient,
		connectivity:      connectivityClient,
		command:           commandClient,
		commandCapability: commandCapabilityClient,
		leaseOwner:        leaseOwner,
		leaseFor:          leaseFor,
		pollInterval:      pollInterval,
		batchSize:         batchSize,
		now:               now,
		logger:            logger,
	}, nil
}

// Run polls and dispatches outbox deliveries until ctx is cancelled.
func (r *OutboxRelay) Run(ctx context.Context) {
	for ctx.Err() == nil {
		processed, err := r.ProcessOnce(ctx)
		if err != nil && ctx.Err() == nil {
			r.logger.Warn("core_outbox_relay_cycle_failed",
				"error_code", "OUTBOX_RELAY_CYCLE_FAILED",
				"error", err.Error(),
			)
		}
		wait := r.pollInterval
		if err == nil && processed >= r.batchSize {
			wait = 20 * time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// ProcessOnce claims and dispatches one batch per tenant of ready outbox deliveries.
func (r *OutboxRelay) ProcessOnce(ctx context.Context) (int, error) {
	tenants, err := r.listTenants(ctx)
	if err != nil {
		return 0, err
	}
	totalProcessed := 0
	var errs []error
	for _, tenantID := range tenants {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		processed, tenantErr := r.processTenantOnce(ctx, tenantID)
		totalProcessed += processed
		if tenantErr != nil {
			r.logger.Warn("core_outbox_tenant_failed",
				"tenant_id", tenantID,
				"error_code", "OUTBOX_TENANT_PROCESS_FAILED",
				"error", tenantErr.Error(),
			)
			errs = append(errs, fmt.Errorf("tenant %s: %w", tenantID, tenantErr))
		}
	}
	return totalProcessed, errors.Join(errs...)
}

func (r *OutboxRelay) processTenantOnce(ctx context.Context, tenantID string) (int, error) {
	events, err := r.claimTenantBatch(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, event := range events {
		processed++
		if deliverErr := r.deliverEvent(ctx, event); deliverErr != nil {
			r.logger.Warn("core_outbox_delivery_failed",
				"event_id", event.EventID,
				"event_type", event.EventType,
				"attempt", event.Attempt,
				"error_code", "OUTBOX_DELIVERY_FAILED",
				"error", deliverErr.Error(),
			)
			var nextRetry *time.Time
			if event.Attempt < outboxMaxDeliveryAttempts {
				t := r.now().UTC().Add(relayRetryBackoff(event.Attempt))
				nextRetry = &t
			} else {
				r.logger.Error("core_outbox_delivery_exhausted",
					"event_id", event.EventID,
					"event_type", event.EventType,
					"attempt", event.Attempt,
					"error_code", "OUTBOX_DELIVERY_EXHAUSTED",
					"error", deliverErr.Error(),
				)
			}
			if markErr := r.markFailed(ctx, event, deliverErr.Error(), nextRetry); markErr != nil {
				return processed, markErr
			}
			continue
		}
		if markErr := r.markDelivered(ctx, event); markErr != nil {
			return processed, markErr
		}
	}
	return processed, nil
}

func (r *OutboxRelay) listTenants(ctx context.Context) ([]string, error) {
	tx, err := r.store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin outbox tenant list tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s1_core_writer`); err != nil {
		return nil, fmt.Errorf("set core writer role for outbox tenant list: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM iam.tenants ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list tenants for outbox relay: %w", err)
	}
	defer rows.Close()
	var tenants []string
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			return nil, err
		}
		tenants = append(tenants, tenantID)
	}
	return tenants, rows.Err()
}

func (r *OutboxRelay) withTenantWriterTx(ctx context.Context, tenantID string, access pgx.TxAccessMode, fn func(pgx.Tx) error) error {
	tx, err := r.store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: access})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s1_core_writer`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM core_registry.sites WHERE tenant_id = $1::uuid ORDER BY id`, tenantID)
	if err != nil {
		return err
	}
	var siteIDs []string
	for rows.Next() {
		var siteID string
		if scanErr := rows.Scan(&siteID); scanErr != nil {
			rows.Close()
			return scanErr
		}
		siteIDs = append(siteIDs, siteID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	authorized := "{}"
	if len(siteIDs) > 0 {
		authorized = "{" + strings.Join(siteIDs, ",") + "}"
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.authorized_site_ids', $1, true)`, authorized); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *OutboxRelay) claimTenantBatch(ctx context.Context, tenantID string) ([]claimedOutboxEvent, error) {
	now := r.now().UTC()
	leaseUntil := now.Add(r.leaseFor)
	var claimed []claimedOutboxEvent
	err := r.withTenantWriterTx(ctx, tenantID, pgx.ReadWrite, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.domain_event_deliveries (
  event_id, consumer_name, status, lease_owner, lease_until,
  attempt, next_retry_at, delivered_at, last_error, created_at, updated_at
)
SELECT e.id, $1, 'PENDING', NULL, NULL, 0, NULL, NULL, NULL, $2, $2
FROM core_registry.domain_outbox_events e
WHERE e.tenant_id = $3::uuid
  AND e.event_type = ANY($4::text[])
  AND NOT EXISTS (
    SELECT 1 FROM core_registry.domain_event_deliveries d
    WHERE d.event_id = e.id AND d.consumer_name = $1
  )
ON CONFLICT (event_id, consumer_name) DO NOTHING
`, outboxRelayConsumerName, now, tenantID, relayedOutboxEventTypes); err != nil {
			return fmt.Errorf("seed outbox deliveries: %w", err)
		}

		rows, err := tx.Query(ctx, `
WITH due AS (
  SELECT d.event_id
  FROM core_registry.domain_event_deliveries d
  JOIN core_registry.domain_outbox_events e ON e.id = d.event_id
  WHERE d.consumer_name = $1
    AND e.tenant_id = $2::uuid
    AND e.event_type = ANY($7::text[])
    AND d.attempt < $8
    AND (
      d.status = 'PENDING'
      OR (d.status = 'FAILED' AND d.next_retry_at IS NOT NULL AND d.next_retry_at <= $3)
      OR (d.status = 'LEASED' AND d.lease_until <= $3)
    )
  ORDER BY e.created_at ASC, e.id ASC
  LIMIT $4
  FOR UPDATE OF d SKIP LOCKED
),
claimed AS (
  UPDATE core_registry.domain_event_deliveries d
  SET status = 'LEASED',
      lease_owner = $5,
      lease_until = $6,
      attempt = d.attempt + 1,
      updated_at = $3
  FROM due
  WHERE d.event_id = due.event_id AND d.consumer_name = $1
  RETURNING d.event_id, d.attempt
)
SELECT c.event_id::text, c.attempt, COALESCE(e.site_id::text, ''), e.event_type, e.subject_id::text, e.aggregate_type, e.aggregate_id::text, e.created_at, e.payload
FROM claimed c
JOIN core_registry.domain_outbox_events e ON e.id = c.event_id
ORDER BY e.created_at ASC, e.id ASC
`, outboxRelayConsumerName, tenantID, now, r.batchSize, r.leaseOwner, leaseUntil, relayedOutboxEventTypes, outboxMaxDeliveryAttempts)
		if err != nil {
			return fmt.Errorf("claim outbox deliveries: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var ev claimedOutboxEvent
			var rawPayload []byte
			if scanErr := rows.Scan(&ev.EventID, &ev.Attempt, &ev.SiteID, &ev.EventType, &ev.SubjectID, &ev.AggregateType, &ev.AggregateID, &ev.CreatedAt, &rawPayload); scanErr != nil {
				return scanErr
			}
			ev.TenantID = tenantID
			if len(rawPayload) > 0 {
				if unmarshalErr := json.Unmarshal(rawPayload, &ev.Payload); unmarshalErr != nil {
					ev.Payload = map[string]any{}
				}
			}
			if ev.SiteID == "" {
				ev.SiteID = payloadString(ev.Payload, "siteId")
			}
			claimed = append(claimed, ev)
		}
		return rows.Err()
	})
	return claimed, err
}

func (r *OutboxRelay) markDelivered(ctx context.Context, event claimedOutboxEvent) error {
	now := r.now().UTC()
	return r.withTenantWriterTx(ctx, event.TenantID, pgx.ReadWrite, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
UPDATE core_registry.domain_event_deliveries
SET status = 'DELIVERED',
    lease_owner = NULL,
    lease_until = NULL,
    delivered_at = $3,
    last_error = NULL,
    updated_at = $3
WHERE event_id = $1::uuid
  AND consumer_name = $2
  AND status = 'LEASED'
  AND lease_owner = $4
`, event.EventID, outboxRelayConsumerName, now, r.leaseOwner)
		return err
	})
}

func (r *OutboxRelay) markFailed(ctx context.Context, event claimedOutboxEvent, errMsg string, nextRetry *time.Time) error {
	now := r.now().UTC()
	reason := truncateRelayUTF8(strings.TrimSpace(errMsg), 512)
	return r.withTenantWriterTx(ctx, event.TenantID, pgx.ReadWrite, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
UPDATE core_registry.domain_event_deliveries
SET status = 'FAILED',
    lease_owner = NULL,
    lease_until = NULL,
    next_retry_at = $3,
    last_error = $4,
    updated_at = $5
WHERE event_id = $1::uuid
  AND consumer_name = $2
  AND status = 'LEASED'
  AND lease_owner = $6
`, event.EventID, outboxRelayConsumerName, nextRetry, reason, now, r.leaseOwner)
		return err
	})
}

func (r *OutboxRelay) deliverEvent(ctx context.Context, event claimedOutboxEvent) error {
	switch event.EventType {
	case "registry.device.created", "registry.device.updated":
		return r.deliverDeviceSync(ctx, event)
	case "registry.point.created", "registry.point.updated":
		deviceID, pointKey, err := r.lookupPointDevice(ctx, event.TenantID, event.SubjectID)
		if err != nil {
			return err
		}
		if deviceID == "" {
			return nil
		}
		return r.deliverPointSync(ctx, event.TenantID, event.SiteID, deviceID, event.SubjectID, pointKey)
	case "registry.import.committed":
		return r.deliverSiteSync(ctx, event.TenantID, event.SiteID)
	case "registry.retirement.completed":
		return r.deliverRetirement(ctx, event)
	case "registry.template-revision.released":
		return r.deliverTemplateRevisionReleased(ctx, event)
	case "registry.template-assignment.changed":
		return r.deliverTemplateAssignmentChanged(ctx, event)
	default:
		return nil
	}
}

func (r *OutboxRelay) deliverTemplateRevisionReleased(ctx context.Context, event claimedOutboxEvent) error {
	revisionID := strings.TrimSpace(event.SubjectID)
	if revisionID == "" {
		revisionID = payloadStringField(event.Payload, "templateRevisionId")
	}
	return r.syncTemplateRevisionCapabilities(ctx, event.TenantID, revisionID, event.CreatedAt)
}

func (r *OutboxRelay) deliverTemplateAssignmentChanged(ctx context.Context, event claimedOutboxEvent) error {
	if revisionID := payloadStringField(event.Payload, "templateRevisionId"); revisionID != "" {
		if err := r.syncTemplateRevisionCapabilities(ctx, event.TenantID, revisionID, event.CreatedAt); err != nil {
			return err
		}
	}
	siteID := payloadStringField(event.Payload, "siteId")
	if siteID == "" {
		siteID = strings.TrimSpace(event.SiteID)
	}
	targetType := payloadStringField(event.Payload, "targetType")
	targetID := payloadStringField(event.Payload, "targetId")
	if strings.EqualFold(targetType, "DEVICE") && siteID != "" && targetID != "" && r.telemetry != nil {
		req, found, err := r.loadDeviceSyncRequest(ctx, event.TenantID, siteID, targetID)
		if err != nil || !found {
			return err
		}
		if err := r.telemetry.SyncRegistry(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

func payloadStringField(payload map[string]any, key string) string {
	if len(payload) == 0 {
		return ""
	}
	value, ok := payload[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func (r *OutboxRelay) syncTemplateRevisionCapabilities(ctx context.Context, tenantID, revisionID string, syncedAt time.Time) error {
	if r.commandCapability == nil || revisionID == "" {
		return nil
	}
	var profiles []TemplateCapabilityProfile
	err := r.withTenantWriterTx(ctx, tenantID, pgx.ReadOnly, func(tx pgx.Tx) error {
		var (
			templateKind string
			payloadRaw   []byte
		)
		err := tx.QueryRow(ctx, `
SELECT t.template_kind, tr.payload
FROM core_registry.registry_template_revisions AS tr
JOIN core_registry.registry_templates AS t
  ON t.id = tr.template_id AND t.tenant_id = tr.tenant_id
WHERE tr.tenant_id = $1::uuid AND tr.id = $2::uuid
`, tenantID, revisionID).Scan(&templateKind, &payloadRaw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var payloadMap map[string]any
		if len(payloadRaw) == 0 {
			return nil
		}
		if err := json.Unmarshal(payloadRaw, &payloadMap); err != nil {
			return err
		}
		activation, err := parseAndValidateTemplatePayload(TemplateKind(templateKind), payloadMap)
		if err != nil {
			return err
		}
		if activation != nil && len(activation.CapabilityProfiles) > 0 {
			profiles = activation.CapabilityProfiles
		}
		return nil
	})
	if err != nil || len(profiles) == 0 {
		return err
	}
	return r.commandCapability.SyncCapabilityProfiles(ctx, tenantID, syncedAt, profiles)
}

func (r *OutboxRelay) deliverDeviceSync(ctx context.Context, event claimedOutboxEvent) error {
	if event.SiteID == "" || event.SubjectID == "" {
		return nil
	}
	req, found, err := r.loadDeviceSyncRequest(ctx, event.TenantID, event.SiteID, event.SubjectID)
	if err != nil || !found {
		return err
	}
	if r.telemetry != nil {
		if err := r.telemetry.SyncRegistry(ctx, req); err != nil {
			return err
		}
	}
	if event.EventType == "registry.device.updated" && len(req.Devices) > 0 && strings.ToUpper(strings.TrimSpace(req.Devices[0].Status)) == "RETIRED" {
		return r.retireDeviceDownstream(ctx, event.TenantID, event.SiteID, event.SubjectID, event.EventID)
	}
	return nil
}

func (r *OutboxRelay) deliverPointSync(ctx context.Context, tenantID, siteID, deviceID, pointID, pointKey string) error {
	if r.telemetry == nil || siteID == "" || deviceID == "" {
		return nil
	}
	req, found, err := r.loadDeviceSyncRequest(ctx, tenantID, siteID, deviceID)
	if err != nil || !found {
		return err
	}
	for i := range req.Devices {
		filtered := make([]RelayTelemetryPoint, 0, len(req.Devices[i].Points))
		for _, pt := range req.Devices[i].Points {
			if pt.PointID == pointID && strings.ToUpper(pt.PointType) != "COMMAND" && strings.ToUpper(pt.Status) != "ACTIVE" {
				req.RetiredPoints = append(req.RetiredPoints, RelayTelemetryRetirePt{
					DeviceID:     deviceID,
					PointID:      pointID,
					TelemetryKey: pointKey,
				})
				continue
			}
			filtered = append(filtered, pt)
		}
		req.Devices[i].Points = filtered
	}
	return r.telemetry.SyncRegistry(ctx, req)
}

func (r *OutboxRelay) deliverSiteSync(ctx context.Context, tenantID, siteID string) error {
	if r.telemetry == nil || siteID == "" {
		return nil
	}
	req, err := r.loadSiteSyncRequest(ctx, tenantID, siteID)
	if err != nil {
		return err
	}
	if len(req.Devices) == 0 && len(req.RetiredPoints) == 0 {
		return nil
	}
	return r.telemetry.SyncRegistry(ctx, req)
}

func (r *OutboxRelay) retireDeviceDownstream(ctx context.Context, tenantID, siteID, deviceID, causationID string) error {
	if r.command != nil {
		if err := r.command.CancelRetiredTarget(ctx, RelayCommandCancelRequest{
			TenantID:    tenantID,
			SiteID:      siteID,
			DeviceIDs:   []string{deviceID},
			CausationID: causationID,
		}); err != nil {
			return err
		}
	}
	if r.connectivity != nil {
		deviceType, err := r.lookupDeviceType(ctx, tenantID, siteID, deviceID)
		if err != nil {
			return err
		}
		if strings.ToUpper(deviceType) == "GATEWAY" {
			if err := r.connectivity.RetireGateway(ctx, tenantID, deviceID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *OutboxRelay) deliverRetirement(ctx context.Context, event claimedOutboxEvent) error {
	resourceType := strings.ToUpper(payloadString(event.Payload, "resourceType"))
	resourceID := payloadString(event.Payload, "resourceId")
	siteID := event.SiteID
	if siteID == "" {
		siteID = payloadString(event.Payload, "siteId")
	}

	switch resourceType {
	case "POINT":
		if r.telemetry == nil || siteID == "" || resourceID == "" {
			return nil
		}
		deviceID, pointKey, pointType, err := r.lookupPointMeta(ctx, event.TenantID, resourceID)
		if err != nil || deviceID == "" || strings.ToUpper(pointType) == "COMMAND" {
			return err
		}
		return r.telemetry.SyncRegistry(ctx, RelayTelemetrySyncRequest{
			TenantID:      event.TenantID,
			SiteID:        siteID,
			RetiredPoints: []RelayTelemetryRetirePt{{DeviceID: deviceID, PointID: resourceID, TelemetryKey: pointKey}},
		})

	case "DEVICE":
		if siteID == "" || resourceID == "" {
			return nil
		}
		req, found, err := r.loadDeviceSyncRequest(ctx, event.TenantID, siteID, resourceID)
		if err != nil {
			return err
		}
		if r.telemetry != nil && found {
			if err := r.telemetry.SyncRegistry(ctx, req); err != nil {
				return err
			}
		}
		return r.retireDeviceDownstream(ctx, event.TenantID, siteID, resourceID, event.EventID)

	case "SITE":
		if siteID == "" {
			return nil
		}
		req, err := r.loadSiteSyncRequest(ctx, event.TenantID, siteID)
		if err != nil {
			return err
		}
		if r.telemetry != nil && (len(req.Devices) > 0 || len(req.RetiredPoints) > 0) {
			if err := r.telemetry.SyncRegistry(ctx, req); err != nil {
				return err
			}
		}
		deviceIDs, gatewayIDs, err := r.listSiteDeviceIDs(ctx, event.TenantID, siteID)
		if err != nil {
			return err
		}
		if r.command != nil && len(deviceIDs) > 0 {
			if err := r.command.CancelRetiredTarget(ctx, RelayCommandCancelRequest{
				TenantID:    event.TenantID,
				SiteID:      siteID,
				DeviceIDs:   deviceIDs,
				CausationID: event.EventID,
			}); err != nil {
				return err
			}
		}
		if r.connectivity != nil {
			for _, gwID := range gatewayIDs {
				if err := r.connectivity.RetireGateway(ctx, event.TenantID, gwID); err != nil {
					return err
				}
			}
		}
		return nil

	default:
		return nil
	}
}

func derivePresenceApplicability(deviceStatus string, hasOwnActiveNonCommand bool) string {
	if strings.ToUpper(strings.TrimSpace(deviceStatus)) != "ACTIVE" || !hasOwnActiveNonCommand {
		return "NOT_APPLICABLE"
	}
	return "APPLICABLE"
}

func loadDeviceSyncEntryInTx(ctx context.Context, tx pgx.Tx, tenantID, siteID, deviceID, status string) (RelayTelemetryDevice, error) {
	points, hasOwnActiveNonCommand, err := queryDevicePointsInTx(ctx, tx, tenantID, siteID, deviceID)
	if err != nil {
		return RelayTelemetryDevice{}, err
	}
	return RelayTelemetryDevice{
		DeviceID:              deviceID,
		Status:                status,
		PresenceApplicability: derivePresenceApplicability(status, hasOwnActiveNonCommand),
		Points:                points,
	}, nil
}

func (r *OutboxRelay) loadDeviceSyncRequest(ctx context.Context, tenantID, siteID, deviceID string) (RelayTelemetrySyncRequest, bool, error) {
	req := RelayTelemetrySyncRequest{TenantID: tenantID, SiteID: siteID}
	found := false
	err := r.withTenantWriterTx(ctx, tenantID, pgx.ReadOnly, func(tx pgx.Tx) error {
		var status string
		err := tx.QueryRow(ctx, `
SELECT status
FROM core_registry.devices
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid
`, tenantID, siteID, deviceID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		entry, err := loadDeviceSyncEntryInTx(ctx, tx, tenantID, siteID, deviceID, status)
		if err != nil {
			return err
		}
		req.Devices = []RelayTelemetryDevice{entry}
		return nil
	})
	return req, found, err
}

func (r *OutboxRelay) loadSiteSyncRequest(ctx context.Context, tenantID, siteID string) (RelayTelemetrySyncRequest, error) {
	req := RelayTelemetrySyncRequest{TenantID: tenantID, SiteID: siteID}
	err := r.withTenantWriterTx(ctx, tenantID, pgx.ReadOnly, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT id::text, status
FROM core_registry.devices
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
ORDER BY id
`, tenantID, siteID)
		if err != nil {
			return err
		}
		type devRow struct {
			id     string
			status string
		}
		var devs []devRow
		for rows.Next() {
			var d devRow
			if scanErr := rows.Scan(&d.id, &d.status); scanErr != nil {
				rows.Close()
				return scanErr
			}
			devs = append(devs, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, d := range devs {
			entry, err := loadDeviceSyncEntryInTx(ctx, tx, tenantID, siteID, d.id, d.status)
			if err != nil {
				return err
			}
			req.Devices = append(req.Devices, entry)
		}
		return nil
	})
	return req, err
}

func queryDevicePointsInTx(ctx context.Context, tx pgx.Tx, tenantID, siteID, deviceID string) ([]RelayTelemetryPoint, bool, error) {
	rows, err := tx.Query(ctx, `
SELECT id::text, COALESCE(sensor_id::text, ''), point_code, point_type, value_type,
       COALESCE(unit, ''), sample_interval_ms, stale_after_ms, status,
       COALESCE(LOWER(source_metadata->>'copied') = 'true', false),
       source_metadata
FROM core_registry.telemetry_points
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND reporting_device_id = $3::uuid
ORDER BY point_code ASC, id ASC
`, tenantID, siteID, deviceID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var points []RelayTelemetryPoint
	hasOwnActiveNonCommand := false
	for rows.Next() {
		var pt RelayTelemetryPoint
		var sampleMS, staleMS int64
		var copied bool
		var sourceMetaRaw []byte
		if scanErr := rows.Scan(&pt.PointID, &pt.SensorID, &pt.TelemetryKey, &pt.PointType, &pt.ValueType, &pt.Unit, &sampleMS, &staleMS, &pt.Status, &copied, &sourceMetaRaw); scanErr != nil {
			return nil, false, scanErr
		}
		pt.MinimumNumber, pt.MaximumNumber, pt.QualityChecks = extractPointQualityFromSourceMetadata(sourceMetaRaw, pt.ValueType)
		if strings.ToUpper(strings.TrimSpace(pt.PointType)) != "COMMAND" && strings.ToUpper(strings.TrimSpace(pt.Status)) == "ACTIVE" && !copied {
			hasOwnActiveNonCommand = true
		}
		sampleSec := millisToPositiveSeconds(sampleMS, 60)
		freshSec := millisToPositiveSeconds(staleMS, max(sampleSec*2, 300))
		if freshSec < sampleSec {
			freshSec = sampleSec
		}
		pt.SampleIntervalSeconds = sampleSec
		pt.ExpectedSampleIntervalSeconds = sampleSec
		pt.FreshWithinSeconds = freshSec
		points = append(points, pt)
	}
	return points, hasOwnActiveNonCommand, rows.Err()
}

func (r *OutboxRelay) lookupPointDevice(ctx context.Context, tenantID, pointID string) (string, string, error) {
	deviceID, pointCode, _, err := r.lookupPointMeta(ctx, tenantID, pointID)
	return deviceID, pointCode, err
}

func (r *OutboxRelay) lookupPointMeta(ctx context.Context, tenantID, pointID string) (string, string, string, error) {
	var deviceID, pointCode, pointType string
	err := r.withTenantWriterTx(ctx, tenantID, pgx.ReadOnly, func(tx pgx.Tx) error {
		qErr := tx.QueryRow(ctx, `
SELECT reporting_device_id::text, point_code, point_type
FROM core_registry.telemetry_points
WHERE tenant_id = $1::uuid AND id = $2::uuid
`, tenantID, pointID).Scan(&deviceID, &pointCode, &pointType)
		if errors.Is(qErr, pgx.ErrNoRows) {
			return nil
		}
		return qErr
	})
	return deviceID, pointCode, pointType, err
}

func (r *OutboxRelay) lookupDeviceType(ctx context.Context, tenantID, siteID, deviceID string) (string, error) {
	var deviceType string
	err := r.withTenantWriterTx(ctx, tenantID, pgx.ReadOnly, func(tx pgx.Tx) error {
		qErr := tx.QueryRow(ctx, `
SELECT device_type
FROM core_registry.devices
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid
`, tenantID, siteID, deviceID).Scan(&deviceType)
		if errors.Is(qErr, pgx.ErrNoRows) {
			return nil
		}
		return qErr
	})
	return deviceType, err
}

func (r *OutboxRelay) listSiteDeviceIDs(ctx context.Context, tenantID, siteID string) ([]string, []string, error) {
	var deviceIDs, gatewayIDs []string
	err := r.withTenantWriterTx(ctx, tenantID, pgx.ReadOnly, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT id::text, device_type
FROM core_registry.devices
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
ORDER BY id
`, tenantID, siteID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, dt string
			if scanErr := rows.Scan(&id, &dt); scanErr != nil {
				return scanErr
			}
			deviceIDs = append(deviceIDs, id)
			if strings.ToUpper(dt) == "GATEWAY" {
				gatewayIDs = append(gatewayIDs, id)
			}
		}
		return rows.Err()
	})
	return deviceIDs, gatewayIDs, err
}

type httpDownstreamClient struct {
	baseURL string
	client  *http.Client
}

func (c *httpDownstreamClient) SyncRegistry(ctx context.Context, request RelayTelemetrySyncRequest) error {
	return c.postJSON(ctx, outboxTelemetrySyncPath, request)
}

func (c *httpDownstreamClient) RetireGateway(ctx context.Context, tenantID, gatewayID string) error {
	return c.postJSON(ctx, fmt.Sprintf(outboxConnectivityRetireFmt, gatewayID), map[string]string{"tenantId": tenantID})
}

func (c *httpDownstreamClient) CancelRetiredTarget(ctx context.Context, request RelayCommandCancelRequest) error {
	return c.postJSON(ctx, outboxCommandCancelPath, request)
}

func (c *httpDownstreamClient) postJSON(ctx context.Context, path string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode outbox payload %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
	}()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("downstream %s returned status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func millisToPositiveSeconds(ms int64, fallback int) int {
	if ms <= 0 {
		return fallback
	}
	sec := int((ms + 999) / 1000)
	if sec < 1 {
		return 1
	}
	if sec > 604800 {
		return 604800
	}
	return sec
}

func relayRetryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 6 {
		shift = 6
	}
	backoff := time.Second << shift
	if backoff > time.Minute {
		backoff = time.Minute
	}
	return backoff
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	val, ok := payload[key]
	if !ok || val == nil {
		return ""
	}
	s, ok := val.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func newDefaultOutboxHTTPClient(timeout time.Duration) *http.Client {
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		transport = base.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.MaxConnsPerHost = 16
	transport.MaxIdleConns = 64
	transport.MaxIdleConnsPerHost = 16
	transport.IdleConnTimeout = 90 * time.Second
	transport.ResponseHeaderTimeout = 15 * time.Second
	return &http.Client{Timeout: timeout, Transport: transport}
}
