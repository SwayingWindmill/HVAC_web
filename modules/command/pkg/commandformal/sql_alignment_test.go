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
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(source, snippet) {
			t.Fatalf("postgres_dispatch.go missing expected safety guard %q", snippet)
		}
	}
}

func TestPostgresVerificationAndReconcileShareTransitionDecisions(t *testing.T) {
	verifySrc := readRepoSource(t, filepath.Join("..", "commandservice", "postgres_verification.go"))
	if !strings.Contains(verifySrc, "DecideVerificationResolution(") {
		t.Fatalf("postgres_verification.go must delegate to DecideVerificationResolution")
	}
	reconcileSrc := readRepoSource(t, filepath.Join("..", "commandservice", "postgres_reconcile.go"))
	if !strings.Contains(reconcileSrc, "DecideOperatorReconcile(") {
		t.Fatalf("postgres_reconcile.go must delegate to DecideOperatorReconcile")
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

	connectorSrc := readRepoSource(t, filepath.Join("..", "mqttconnector", "connector.go"))
	if !strings.Contains(connectorSrc, "RecoverTenantReplies(recoveryContext, tenantID, 50)") {
		t.Fatalf("mqttconnector/connector.go recoverReplies must call RecoverTenantReplies with batch limit 50")
	}
}
