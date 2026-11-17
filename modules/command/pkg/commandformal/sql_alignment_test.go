package commandformal

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

func readRepoSource(t *testing.T, relFromPkg string) string {
	t.Helper()
	bytes, err := os.ReadFile(filepath.Clean(relFromPkg))
	if err != nil {
		t.Fatalf("read %s: %v", relFromPkg, err)
	}
	return string(bytes)
}

func TestPostgresClaimDispatchBlockingStatusesMatchTransitionModel(t *testing.T) {
	source := readRepoSource(t, filepath.Join("..", "commandservice", "postgres_dispatch.go"))
	re := regexp.MustCompile(`earlier\.status IN \(([A-Z_',\s]+)\)`)
	match := re.FindStringSubmatch(source)
	if len(match) != 2 {
		t.Fatalf("expected earlier.status IN (...) SQL predicate in postgres_dispatch.go")
	}

	rawItems := strings.Split(match[1], ",")
	sqlSet := make(map[commandmodel.IntentStatus]bool, len(rawItems))
	for _, raw := range rawItems {
		trimmed := strings.TrimSpace(raw)
		trimmed = strings.Trim(trimmed, "'")
		if trimmed != "" {
			sqlSet[commandmodel.IntentStatus(trimmed)] = true
		}
	}

	modelStatuses := commandservice.BlockingEarlierIntentStatuses()
	if len(sqlSet) != len(modelStatuses) {
		t.Fatalf("blocking status count mismatch: sql=%d model=%d", len(sqlSet), len(modelStatuses))
	}
	for _, status := range modelStatuses {
		if !sqlSet[status] {
			t.Fatalf("blocking status %s present in BlockingEarlierIntentStatuses() but missing from postgres_dispatch.go SQL", status)
		}
	}

	requiredSnippets := []string{
		"NOT (d.frozen_control_groups ? $3)",
		"attemptStatus != string(commandmodel.AttemptPrepared) || intent.Status != commandmodel.IntentDispatching",
		"DecideDispatchResolution(",
		"ON o.command_id = i.command_id AND o.delivered_at IS NULL AND o.lease_owner = a.lease_owner",
		"FOR UPDATE OF i, a, o",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(source, snippet) {
			t.Fatalf("postgres_dispatch.go missing expected safety guard %q", snippet)
		}
	}

	expireStart := strings.Index(source, "func (store *PostgresStore) expireGovernanceInvalidQueued(")
	expireEnd := strings.Index(source, "func (store *PostgresStore) expireUnapprovedCommands(")
	if expireStart < 0 || expireEnd <= expireStart {
		t.Fatalf("unable to locate expireGovernanceInvalidQueued in postgres_dispatch.go")
	}
	expireBody := source[expireStart:expireEnd]
	if strings.Contains(expireBody, "JOIN command_runtime.command_dispatch_outbox") || strings.Contains(expireBody, "FOR UPDATE OF i, o") {
		t.Fatalf("expireGovernanceInvalidQueued must lock command_intents without joining outbox rows to prevent duplicate 23505 version transitions")
	}
	for _, snippet := range []string{
		"FOR UPDATE OF i SKIP LOCKED",
		"WHERE tenant_id = $1::uuid AND command_id = $2::uuid AND delivered_at IS NULL",
	} {
		if !strings.Contains(expireBody, snippet) {
			t.Fatalf("expireGovernanceInvalidQueued missing %q", snippet)
		}
	}
}

func TestPostgresVerificationAndReconcileShareTransitionDecisions(t *testing.T) {
	verifySrc := readRepoSource(t, filepath.Join("..", "commandservice", "postgres_verification.go"))
	if !strings.Contains(verifySrc, "DecideVerificationResolutionWithEdge(") {
		t.Fatalf("postgres_verification.go must delegate to DecideVerificationResolutionWithEdge")
	}
	reconcileSrc := readRepoSource(t, filepath.Join("..", "commandservice", "postgres_reconcile.go"))
	if !strings.Contains(reconcileSrc, "DecideOperatorReconcile(") {
		t.Fatalf("postgres_reconcile.go must delegate to DecideOperatorReconcile")
	}
	cancelSrc := readRepoSource(t, filepath.Join("..", "commandservice", "postgres_cancel.go"))
	requiredCancelSnippets := []string{
		"DecideCommandCancel(",
		"SET delivered_at = $3, lease_until = NULL",
		"AND status IN ('AWAITING_APPROVAL', 'QUEUED')",
		"IsActiveAbortableSequence(",
		"AND i.status = 'DISPATCHING'",
		"AND a.status = 'ACKNOWLEDGED'",
		"verification_lease_owner = NULL, verification_lease_until = NULL",
		`"ACTIVE_SEQUENCE_ABORTED"`,
	}
	for _, snippet := range requiredCancelSnippets {
		if !strings.Contains(cancelSrc, snippet) {
			t.Fatalf("postgres_cancel.go missing expected transition/outbox guard %q", snippet)
		}
	}
	if commandmodel.TerminalPreSendRejection("DISPATCH_SAFETY_EVALUATION_FAILED") {
		t.Fatalf("DISPATCH_SAFETY_EVALUATION_FAILED must remain retryable under PRE_SEND_ONLY")
	}
	for _, terminalCode := range []string{"DISPATCH_SAFETY_STATE_UNAVAILABLE", "DISPATCH_SAFETY_DEVICE_NOT_ONLINE", "DISPATCH_SAFETY_INTERLOCK_VIOLATED"} {
		if !commandmodel.TerminalPreSendRejection(terminalCode) {
			t.Fatalf("%s must remain a terminal pre-send rejection", terminalCode)
		}
	}
}

func TestConnectivityReplyRecoveryQueryMatchesModel(t *testing.T) {
	storeSrc := readRepoSource(t, filepath.Join("..", "..", "..", "connectivity", "pkg", "connectivity", "store.go"))
	requiredSQL := []string{
		"AND state = 'REPLIED'",
		"ORDER BY replied_at, attempt_id",
		"LIMIT $2",
	}
	for _, snippet := range requiredSQL {
		if !strings.Contains(storeSrc, snippet) {
			t.Fatalf("connectivity/store.go RecoverCommandReplies missing %q", snippet)
		}
	}

	for _, snippet := range []string{
		"isRetryableSerializableError(",
		"runSerializableTenantOp(",
		`"40001"`,
		`"40P01"`,
	} {
		if !strings.Contains(storeSrc, snippet) {
			t.Fatalf("connectivity/store.go missing serializable retry guard %q", snippet)
		}
	}

	connectorSrc := readRepoSource(t, filepath.Join("..", "mqttconnector", "connector.go"))
	if !strings.Contains(connectorSrc, "replyRecoveryBatchLimit      = 50") {
		t.Fatalf("mqttconnector/connector.go must define replyRecoveryBatchLimit = 50")
	}
	if !strings.Contains(connectorSrc, "DrainTenantReplies(recoveryContext, tenantID, replyRecoveryBatchLimit)") {
		t.Fatalf("mqttconnector/connector.go recoverReplies must drain all batches via DrainTenantReplies with batch limit 50")
	}
	for _, snippet := range []string{
		"EnableManualAcknowledgment: true",
		"checkSubackReasons(",
		"maxCommandReplyPayloadBytes",
	} {
		if !strings.Contains(connectorSrc, snippet) {
			t.Fatalf("mqttconnector/connector.go missing custody guard %q", snippet)
		}
	}
	completeStart := strings.Index(connectorSrc, "func (connector *Connector) completeRecoveredReply(")
	completeEnd := strings.Index(connectorSrc, "func (connector *Connector) completeOutcomeUnknown(")
	if completeStart < 0 || completeEnd <= completeStart {
		t.Fatalf("unable to locate completeRecoveredReply in connector.go")
	}
	completeBody := connectorSrc[completeStart:completeEnd]
	if strings.Contains(completeBody, "ConnectorRequestCommitted") {
		t.Fatalf("completeRecoveredReply must propagate EvidenceStore.Complete errors instead of fabricating ConnectorRequestCommitted")
	}

	outboxSrc := readRepoSource(t, filepath.Join("..", "commandservice", "control_alarm_outbox.go"))
	for _, snippet := range []string{
		"ON CONFLICT (command_id, event_kind) DO NOTHING",
		"AND lease_owner = $3",
		"AND attempts = $4",
		"context.WithoutCancel(ctx)",
	} {
		if !strings.Contains(outboxSrc, snippet) {
			t.Fatalf("control_alarm_outbox.go missing expected durability/fencing guard %q", snippet)
		}
	}
}
