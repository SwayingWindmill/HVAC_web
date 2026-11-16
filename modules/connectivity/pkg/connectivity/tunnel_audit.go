package connectivity

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/tunnel"
)

var tunnelAuditUUIDv7Pattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-7[0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

func truncateTunnelAuditUTF8(value string, maxBytes int) string {
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

// RecordTunnelAudit durably records one gateway tunnel session or stream event.
func (store *Store) RecordTunnelAudit(ctx context.Context, event tunnel.AuditEvent) error {
	if store == nil || store.pool == nil {
		return errors.New("connectivity store is not configured")
	}
	gatewayID := strings.TrimSpace(event.GatewayID)
	sessionID := strings.TrimSpace(event.SessionID)
	eventType := strings.TrimSpace(event.EventType)
	if !tunnelAuditUUIDv7Pattern.MatchString(gatewayID) || !tunnelAuditUUIDv7Pattern.MatchString(sessionID) {
		return fmt.Errorf("invalid tunnel audit identifiers: gateway=%q session=%q", gatewayID, sessionID)
	}
	switch eventType {
	case tunnel.AuditSessionOpened, tunnel.AuditSessionClosed, tunnel.AuditStreamOpened, tunnel.AuditStreamRejected:
	default:
		return fmt.Errorf("unsupported tunnel audit event type %q", eventType)
	}

	occurredAt := event.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	auditID, err := newUUIDv7(occurredAt)
	if err != nil {
		return fmt.Errorf("generate tunnel audit id: %w", err)
	}

	var tenantID any
	var resolvedTenant string
	err = store.pool.QueryRow(ctx, `
SELECT tenant_id::text
FROM core_registry.gateway_directory_v1
WHERE gateway_id = $1::uuid
LIMIT 1
`, gatewayID).Scan(&resolvedTenant)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("resolve gateway tenant for tunnel audit: %w", err)
	}
	if err == nil && strings.TrimSpace(resolvedTenant) != "" {
		tenantID = strings.TrimSpace(resolvedTenant)
	}

	var streamID any
	if event.StreamID != nil && *event.StreamID >= 0 {
		streamID = *event.StreamID
	}
	var targetPort any
	if event.TargetPort != nil && *event.TargetPort >= 1 {
		targetPort = int32(*event.TargetPort)
	}
	remoteAddr := truncateTunnelAuditUTF8(strings.TrimSpace(event.RemoteAddr), 256)
	reason := truncateTunnelAuditUTF8(strings.TrimSpace(event.Reason), 512)
	bytesTX := event.BytesTX
	if bytesTX < 0 {
		bytesTX = 0
	}
	bytesRX := event.BytesRX
	if bytesRX < 0 {
		bytesRX = 0
	}

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin tunnel audit transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if resolvedTenant != "" {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, resolvedTenant); err != nil {
			return fmt.Errorf("set tunnel audit tenant scope: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO connectivity.gateway_tunnel_audit (
  id, tenant_id, gateway_id, session_id, event_type,
  stream_id, target_port, remote_addr, reason, bytes_tx, bytes_rx, occurred_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5,
  $6, $7, $8, $9, $10, $11, $12
)
`, auditID, tenantID, gatewayID, sessionID, eventType, streamID, targetPort, remoteAddr, reason, bytesTX, bytesRX, occurredAt); err != nil {
		return fmt.Errorf("insert gateway tunnel audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit gateway tunnel audit: %w", err)
	}
	return nil
}
