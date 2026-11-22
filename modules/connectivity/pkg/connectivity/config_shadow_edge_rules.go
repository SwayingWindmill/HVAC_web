package connectivity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	DriftSectionEdgeRules = "EDGE_RULES"

	maxEdgeRuleBundleJSONBytes = 262144
)

// OptionalEdgeRuleBundle represents tri-state JSON presence for PUT partial
// updates: omitted (Set=false, Value=nil), explicit JSON null (Set=true,
// Value=nil) to clear, or a replacement edge rule bundle object (Value!=nil).
type OptionalEdgeRuleBundle struct {
	Set   bool
	Value *edgecontrol.EdgeRuleBundle
}

func (o OptionalEdgeRuleBundle) IsSet() bool {
	return o.Set || o.Value != nil
}

func (o OptionalEdgeRuleBundle) IsZero() bool {
	return !o.Set && o.Value == nil
}

func (o OptionalEdgeRuleBundle) MarshalJSON() ([]byte, error) {
	if o.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

func (o *OptionalEdgeRuleBundle) UnmarshalJSON(data []byte) error {
	if o == nil {
		return errors.New("OptionalEdgeRuleBundle: UnmarshalJSON on nil pointer")
	}
	trimmed := bytes.TrimSpace(data)
	o.Set = true
	if bytes.Equal(trimmed, []byte("null")) {
		o.Value = nil
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var bundle edgecontrol.EdgeRuleBundle
	if err := dec.Decode(&bundle); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON content in edgeRuleBundle")
	}
	o.Value = &bundle
	return nil
}

func validateEdgeRuleBundle(input *edgecontrol.EdgeRuleBundle) (*edgecontrol.EdgeRuleBundle, error) {
	if input == nil {
		return nil, nil
	}
	if err := input.Validate(); err != nil {
		return nil, fmt.Errorf("%w: edgeRuleBundle: %v", ErrConfigShadowInvalid, err)
	}
	return input.Clone(), nil
}

func shortEdgeBundleDigest(digest string) string {
	if len(digest) <= 12 {
		return digest
	}
	return digest[:12]
}

func diffEdgeRuleBundle(desired, reported *edgecontrol.EdgeRuleBundle) []DriftDetail {
	switch {
	case desired == nil && reported == nil:
		return nil
	case desired != nil && reported == nil:
		return []DriftDetail{{
			Section: DriftSectionEdgeRules,
			Target:  "edgeRuleBundle",
			Reason:  DriftReasonMissingOnEdge,
			Detail: clampUTF8Runes(
				fmt.Sprintf("desired edge rule bundle (rev=%d digest=%s rules=%d) is missing on edge", desired.BundleRevision, shortEdgeBundleDigest(desired.EdgeBundleDigest), len(desired.Rules)),
				maxDriftDetailRunes,
			),
		}}
	case desired == nil && reported != nil:
		return []DriftDetail{{
			Section: DriftSectionEdgeRules,
			Target:  "edgeRuleBundle",
			Reason:  DriftReasonUnexpectedOnEdge,
			Detail: clampUTF8Runes(
				fmt.Sprintf("unexpected edge rule bundle (rev=%d digest=%s rules=%d) present on edge", reported.BundleRevision, shortEdgeBundleDigest(reported.EdgeBundleDigest), len(reported.Rules)),
				maxDriftDetailRunes,
			),
		}}
	case desired.EdgeBundleDigest != reported.EdgeBundleDigest || desired.BundleRevision != reported.BundleRevision || len(desired.Rules) != len(reported.Rules):
		return []DriftDetail{{
			Section: DriftSectionEdgeRules,
			Target:  "edgeRuleBundle",
			Reason:  DriftReasonValueMismatch,
			Detail: clampUTF8Runes(
				fmt.Sprintf("edge rule bundle mismatch (desired rev=%d digest=%s rules=%d, reported rev=%d digest=%s rules=%d)",
					desired.BundleRevision, shortEdgeBundleDigest(desired.EdgeBundleDigest), len(desired.Rules),
					reported.BundleRevision, shortEdgeBundleDigest(reported.EdgeBundleDigest), len(reported.Rules),
				),
				maxDriftDetailRunes,
			),
		}}
	default:
		return nil
	}
}

type GatewayEdgeRuleSyncSummary struct {
	GatewayID                string     `json:"gatewayId"`
	Online                   bool       `json:"online"`
	SyncStatus               string     `json:"syncStatus"`
	AppliedStatus            string     `json:"appliedStatus,omitempty"`
	EdgeRulesSynced          bool       `json:"edgeRulesSynced"`
	DesiredBundleRevision    int64      `json:"desiredBundleRevision"`
	DesiredEdgeBundleDigest  string     `json:"desiredEdgeBundleDigest,omitempty"`
	ReportedBundleRevision   int64      `json:"reportedBundleRevision"`
	ReportedEdgeBundleDigest string     `json:"reportedEdgeBundleDigest,omitempty"`
	ReportedRuleIDs          []string   `json:"reportedRuleIds"`
	ReportedEdgeOnlyRuleIDs  []string   `json:"reportedEdgeOnlyRuleIds"`
	ReportedAt               *time.Time `json:"reportedAt,omitempty"`
}

type SiteEdgeRuleSyncStatus struct {
	SchemaVersion int                          `json:"schemaVersion"`
	TenantID      string                       `json:"tenantId"`
	SiteID        string                       `json:"siteId"`
	EvaluatedAt   time.Time                    `json:"evaluatedAt"`
	Gateways      []GatewayEdgeRuleSyncSummary `json:"gateways"`
}

func hasEdgeRuleDrift(details []DriftDetail) bool {
	for _, detail := range details {
		if detail.Section == DriftSectionEdgeRules {
			return true
		}
	}
	return false
}

func buildGatewayEdgeRuleSyncSummary(
	tenantID, gatewayID string,
	desired *GatewayDesiredConfig,
	reported *GatewayReportedConfig,
	isGatewayOnline func(string) bool,
) GatewayEdgeRuleSyncSummary {
	shadow := buildGatewayConfigShadow(tenantID, gatewayID, desired, reported)
	summary := GatewayEdgeRuleSyncSummary{
		GatewayID:               gatewayID,
		Online:                  isGatewayOnline != nil && isGatewayOnline(gatewayID),
		SyncStatus:              shadow.SyncStatus,
		ReportedRuleIDs:         []string{},
		ReportedEdgeOnlyRuleIDs: []string{},
	}
	if desired != nil && desired.EdgeRuleBundle != nil {
		summary.DesiredBundleRevision = int64(desired.EdgeRuleBundle.BundleRevision)
		summary.DesiredEdgeBundleDigest = desired.EdgeRuleBundle.EdgeBundleDigest
	}
	if reported != nil {
		summary.AppliedStatus = reported.AppliedStatus
		t := reported.ReportedAt.UTC()
		summary.ReportedAt = &t
		if reported.EdgeRuleBundle != nil {
			summary.ReportedBundleRevision = int64(reported.EdgeRuleBundle.BundleRevision)
			summary.ReportedEdgeBundleDigest = reported.EdgeRuleBundle.EdgeBundleDigest
			allSeen := make(map[string]struct{}, len(reported.EdgeRuleBundle.Rules))
			edgeOnlySeen := make(map[string]struct{}, len(reported.EdgeRuleBundle.Rules))
			for _, rule := range reported.EdgeRuleBundle.Rules {
				id := strings.ToLower(strings.TrimSpace(rule.RuleID))
				if id == "" {
					continue
				}
				allSeen[id] = struct{}{}
				if rule.ExecutionTarget == edgecontrol.EdgeTargetEdge {
					edgeOnlySeen[id] = struct{}{}
				}
			}
			summary.ReportedRuleIDs = sortedMapKeys(allSeen)
			summary.ReportedEdgeOnlyRuleIDs = sortedMapKeys(edgeOnlySeen)
		}
	}
	summary.EdgeRulesSynced = desired != nil &&
		desired.EdgeRuleBundle != nil &&
		reported != nil &&
		reported.EdgeRuleBundle != nil &&
		reported.AppliedStatus != AppliedStatusRejected &&
		len(diffEdgeRuleBundle(desired.EdgeRuleBundle, reported.EdgeRuleBundle)) == 0 &&
		!hasEdgeRuleDrift(reported.DriftDetails)
	return summary
}

func sortedMapKeys(items map[string]struct{}) []string {
	if len(items) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(items))
	for k := range items {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// GetSiteEdgeRuleSyncStatus returns edge rule bundle shadow convergence and runtime
// gateway online status for all ACTIVE gateways bound to a Site.
func (store *Store) GetSiteEdgeRuleSyncStatus(
	ctx context.Context,
	tenantID, siteID string,
	isGatewayOnline func(string) bool,
) (SiteEdgeRuleSyncStatus, error) {
	if store == nil || store.pool == nil {
		return SiteEdgeRuleSyncStatus{}, errors.New("connectivity store is closed")
	}
	tenantID = strings.ToLower(strings.TrimSpace(tenantID))
	siteID = strings.ToLower(strings.TrimSpace(siteID))
	if !gatewayIDPattern.MatchString(tenantID) || !gatewayIDPattern.MatchString(siteID) {
		return SiteEdgeRuleSyncStatus{}, fmt.Errorf("%w: tenantId and siteId must be valid UUIDs", ErrConfigShadowInvalid)
	}
	summaries := []GatewayEdgeRuleSyncSummary{}
	if err := store.readAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT gateway_id::text
FROM core_registry.gateway_directory_v1
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND status = 'ACTIVE'
ORDER BY gateway_id ASC
`, tenantID, siteID)
		if err != nil {
			return fmt.Errorf("list site active gateways: %w", err)
		}
		var gatewayIDs []string
		for rows.Next() {
			var gwID string
			if scanErr := rows.Scan(&gwID); scanErr != nil {
				rows.Close()
				return fmt.Errorf("scan site active gateway: %w", scanErr)
			}
			gatewayIDs = append(gatewayIDs, gwID)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return fmt.Errorf("iterate site active gateways: %w", rowsErr)
		}
		rows.Close()

		summaries = make([]GatewayEdgeRuleSyncSummary, 0, len(gatewayIDs))
		for _, gwID := range gatewayIDs {
			desired, err := loadDesiredConfigInTx(ctx, tx, tenantID, gwID, false)
			if err != nil {
				return err
			}
			reported, err := loadReportedConfigInTx(ctx, tx, tenantID, gwID, false)
			if err != nil {
				return err
			}
			summaries = append(summaries, buildGatewayEdgeRuleSyncSummary(tenantID, gwID, desired, reported, isGatewayOnline))
		}
		return nil
	}); err != nil {
		return SiteEdgeRuleSyncStatus{}, err
	}
	return SiteEdgeRuleSyncStatus{
		SchemaVersion: 1,
		TenantID:      tenantID,
		SiteID:        siteID,
		EvaluatedAt:   time.Now().UTC(),
		Gateways:      summaries,
	}, nil
}
