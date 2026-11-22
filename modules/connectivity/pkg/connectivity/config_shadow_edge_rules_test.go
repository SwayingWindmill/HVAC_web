package connectivity

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func sampleValidEdgeRuleBundle() *edgecontrol.EdgeRuleBundle {
	trig := 45.0
	rst := 43.0
	bundle := &edgecontrol.EdgeRuleBundle{
		SchemaVersion:  edgecontrol.EdgeRuleSchemaVersion,
		SiteID:         "0194f000-2000-7000-8000-000000000001",
		BundleRevision: 3,
		BundleDigest:   strings.Repeat("a", 64),
		CompiledAt:     "2026-11-22T00:00:00Z",
		Rules: []edgecontrol.CompiledEdgeRule{{
			RuleID:           "0194f000-4000-7000-8000-000000000001",
			AlarmType:        "CHILLER_HIGH_TEMP",
			Title:            "Chiller High Supply Temp",
			Severity:         "CRITICAL",
			ExecutionTarget:  edgecontrol.EdgeTargetCloudAndEdge,
			QualityPolicy:    edgecontrol.EdgeQualityValidOnly,
			FreshnessSeconds: 30,
			TriggerMode:      edgecontrol.EdgeTriggerSimple,
			Raise: edgecontrol.EdgeCondition{
				Kind:      edgecontrol.EdgeConditionHysteresis,
				Input:     "0194f000-5000-7000-8000-000000000001/chilled_water_supply_temperature",
				Direction: edgecontrol.EdgeDirectionAbove,
				Trigger:   &trig,
				Reset:     &rst,
			},
			Clear: edgecontrol.EdgeCondition{
				Kind:     edgecontrol.EdgeConditionCompare,
				Input:    "0194f000-5000-7000-8000-000000000001/chilled_water_supply_temperature",
				Operator: edgecontrol.EdgeCompareLTE,
				Value:    &edgecontrol.EdgeTypedValue{Type: edgecontrol.EdgeValueNumber, Number: 43},
			},
		}},
	}
	digest, err := bundle.ComputeDigest()
	if err != nil {
		panic(err)
	}
	bundle.EdgeBundleDigest = digest
	return bundle
}

func TestConfigShadowEdgeRuleBundleValidationDriftAndTriState(t *testing.T) {
	t.Parallel()

	valid := sampleValidEdgeRuleBundle()
	cleaned, err := validateEdgeRuleBundle(valid)
	if err != nil || cleaned == nil || cleaned == valid {
		t.Fatalf("validateEdgeRuleBundle() = %+v, err=%v", cleaned, err)
	}

	tampered := valid.Clone()
	tampered.EdgeBundleDigest = strings.Repeat("f", 64)
	if _, err := validateEdgeRuleBundle(tampered); !errors.Is(err, ErrConfigShadowInvalid) {
		t.Fatalf("expected ErrConfigShadowInvalid for digest mismatch, got %v", err)
	}

	if d := ComputeConfigShadowDrift(
		&GatewayDesiredConfig{EdgeRuleBundle: valid},
		&GatewayReportedConfig{EdgeRuleBundle: valid.Clone()},
	); len(d) != 0 {
		t.Fatalf("expected zero drift when edgeRuleBundle matches, got %+v", d)
	}

	if d := ComputeConfigShadowDrift(&GatewayDesiredConfig{EdgeRuleBundle: valid}, &GatewayReportedConfig{}); len(d) != 1 || d[0].Section != DriftSectionEdgeRules || d[0].Target != "edgeRuleBundle" || d[0].Reason != DriftReasonMissingOnEdge {
		t.Fatalf("unexpected missing drift: %+v", d)
	}
	if d := ComputeConfigShadowDrift(&GatewayDesiredConfig{}, &GatewayReportedConfig{EdgeRuleBundle: valid}); len(d) != 1 || d[0].Section != DriftSectionEdgeRules || d[0].Target != "edgeRuleBundle" || d[0].Reason != DriftReasonUnexpectedOnEdge {
		t.Fatalf("unexpected unexpected drift: %+v", d)
	}

	mismatched := valid.Clone()
	mismatched.BundleRevision = 4
	d2, _ := mismatched.ComputeDigest()
	mismatched.EdgeBundleDigest = d2
	if d := ComputeConfigShadowDrift(
		&GatewayDesiredConfig{EdgeRuleBundle: valid},
		&GatewayReportedConfig{EdgeRuleBundle: mismatched},
	); len(d) != 1 || d[0].Section != DriftSectionEdgeRules || d[0].Reason != DriftReasonValueMismatch || !strings.Contains(d[0].Detail, valid.EdgeBundleDigest[:12]) || !strings.Contains(d[0].Detail, d2[:12]) {
		t.Fatalf("unexpected mismatch drift: %+v", d)
	}

	if _, _, err := validateReportDriftDetails([]DriftDetail{{
		Section: DriftSectionEdgeRules,
		Target:  "edgeRuleBundle",
		Reason:  DriftReasonEdgeApplyFailed,
		Detail:  "bundle apply failed",
	}}); err != nil {
		t.Fatalf("expected EDGE_RULES drift section allowed, got %v", err)
	}

	var omitted UpdateDesiredConfigInput
	if err := json.Unmarshal([]byte(`{"reason":"omit"}`), &omitted); err != nil || omitted.EdgeRuleBundle.IsSet() || !omitted.EdgeRuleBundle.IsZero() {
		t.Fatalf("expected omitted edgeRuleBundle, got %+v err=%v", omitted.EdgeRuleBundle, err)
	}
	omittedBytes, _ := json.Marshal(omitted)
	if strings.Contains(string(omittedBytes), "edgeRuleBundle") {
		t.Fatalf("omitted edgeRuleBundle must not marshal key, got %s", string(omittedBytes))
	}

	var cleared UpdateDesiredConfigInput
	if err := json.Unmarshal([]byte(`{"edgeRuleBundle":null,"reason":"clear"}`), &cleared); err != nil || !cleared.EdgeRuleBundle.IsSet() || cleared.EdgeRuleBundle.Value != nil || cleared.EdgeRuleBundle.IsZero() {
		t.Fatalf("expected explicit null edgeRuleBundle, got %+v err=%v", cleared.EdgeRuleBundle, err)
	}
	clearedBytes, _ := json.Marshal(cleared)
	if !strings.Contains(string(clearedBytes), `"edgeRuleBundle":null`) {
		t.Fatalf("explicit null edgeRuleBundle must marshal null, got %s", string(clearedBytes))
	}

	payload, _ := json.Marshal(map[string]any{"edgeRuleBundle": valid, "reason": "set"})
	var replaced UpdateDesiredConfigInput
	if err := json.Unmarshal(payload, &replaced); err != nil || !replaced.EdgeRuleBundle.IsSet() || replaced.EdgeRuleBundle.Value == nil || replaced.EdgeRuleBundle.Value.EdgeBundleDigest != valid.EdgeBundleDigest {
		t.Fatalf("expected object edgeRuleBundle, got %+v err=%v", replaced.EdgeRuleBundle, err)
	}

	var unknownField UpdateDesiredConfigInput
	if err := json.Unmarshal([]byte(`{"edgeRuleBundle":{"schemaVersion":1,"extraField":1},"reason":"bad"}`), &unknownField); err == nil {
		t.Fatalf("expected unknown field inside edgeRuleBundle to be rejected")
	}
}

type stubSiteEdgeRuleShadowStore struct {
	status SiteEdgeRuleSyncStatus
	err    error
	calls  int
}

func (s *stubSiteEdgeRuleShadowStore) GetGatewayConfigShadowForOperator(context.Context, registryauth.GrantClaims, string) (GatewayConfigShadow, error) {
	return GatewayConfigShadow{}, nil
}
func (s *stubSiteEdgeRuleShadowStore) UpdateGatewayDesiredConfig(context.Context, registryauth.GrantClaims, string, UpdateDesiredConfigInput) (GatewayConfigShadow, error) {
	return GatewayConfigShadow{}, nil
}
func (s *stubSiteEdgeRuleShadowStore) PullGatewayDesiredConfig(context.Context, string, int64) (GatewayShadowPullResponse, error) {
	return GatewayShadowPullResponse{}, nil
}
func (s *stubSiteEdgeRuleShadowStore) ReportGatewayAppliedConfig(context.Context, string, GatewayShadowReportInput) (GatewayConfigShadow, error) {
	return GatewayConfigShadow{}, nil
}
func (s *stubSiteEdgeRuleShadowStore) GetSiteEdgeRuleSyncStatus(_ context.Context, tenantID, siteID string, isGatewayOnline func(string) bool) (SiteEdgeRuleSyncStatus, error) {
	s.calls++
	status := s.status
	status.TenantID = tenantID
	status.SiteID = siteID
	if isGatewayOnline != nil {
		for i := range status.Gateways {
			status.Gateways[i].Online = isGatewayOnline(status.Gateways[i].GatewayID)
		}
	}
	return status, s.err
}

func TestBuildGatewayEdgeRuleSyncSummaryAndInternalHTTPSPIFFE(t *testing.T) {
	bundle := sampleValidEdgeRuleBundle()
	edgeOnly := bundle.Rules[0]
	edgeOnly.RuleID = "0194f000-4000-7000-8000-000000000002"
	edgeOnly.ExecutionTarget = edgecontrol.EdgeTargetEdge
	bundle.Rules = append(bundle.Rules, edgeOnly)
	dig, _ := bundle.ComputeDigest()
	bundle.EdgeBundleDigest = dig

	summary := buildGatewayEdgeRuleSyncSummary(
		"0194f000-1000-7000-8000-000000000001",
		"0194f000-3000-7000-8000-000000000001",
		&GatewayDesiredConfig{DesiredRevision: 2, EdgeRuleBundle: bundle},
		&GatewayReportedConfig{ReportedRevision: 2, AppliedStatus: AppliedStatusApplied, EdgeRuleBundle: bundle.Clone()},
		func(gw string) bool { return gw == "0194f000-3000-7000-8000-000000000001" },
	)
	if !summary.Online || !summary.EdgeRulesSynced || summary.DesiredBundleRevision != 3 || summary.ReportedBundleRevision != 3 {
		t.Fatalf("unexpected synced summary: %+v", summary)
	}
	if !slices.Equal(summary.ReportedEdgeOnlyRuleIDs, []string{"0194f000-4000-7000-8000-000000000002"}) {
		t.Fatalf("ReportedEdgeOnlyRuleIDs=%v", summary.ReportedEdgeOnlyRuleIDs)
	}

	rejected := buildGatewayEdgeRuleSyncSummary(
		"0194f000-1000-7000-8000-000000000001",
		"0194f000-3000-7000-8000-000000000001",
		&GatewayDesiredConfig{DesiredRevision: 2, EdgeRuleBundle: bundle},
		&GatewayReportedConfig{ReportedRevision: 2, AppliedStatus: AppliedStatusRejected, EdgeRuleBundle: bundle.Clone()},
		nil,
	)
	if rejected.EdgeRulesSynced {
		t.Fatalf("REJECTED report must not mark EdgeRulesSynced=true")
	}

	stub := &stubSiteEdgeRuleShadowStore{status: SiteEdgeRuleSyncStatus{SchemaVersion: 1, Gateways: []GatewayEdgeRuleSyncSummary{summary}}}
	h := NewConfigShadowHandlerWithRuntimePresence(stub, nil, nil, nil, nil, func(string) bool { return true })

	reqNoCert := httptest.NewRequest(http.MethodGet, "/internal/v1/sites/0194f000-2000-7000-8000-000000000001/edge-rule-sync-status?tenantId=0194f000-1000-7000-8000-000000000001", nil)
	recNoCert := httptest.NewRecorder()
	h.ServeHTTP(recNoCert, reqNoCert)
	if recNoCert.Code != http.StatusForbidden {
		t.Fatalf("missing cert status=%d want 403", recNoCert.Code)
	}

	wrongURI, _ := url.Parse("spiffe://hvac.local/platform-core-service")
	reqWrongCert := reqNoCert.Clone(context.Background())
	reqWrongCert.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{URIs: []*url.URL{wrongURI}}}}}
	recWrongCert := httptest.NewRecorder()
	h.ServeHTTP(recWrongCert, reqWrongCert)
	if recWrongCert.Code != http.StatusForbidden {
		t.Fatalf("non-alarm cert status=%d want 403", recWrongCert.Code)
	}

	alarmURI, _ := url.Parse("spiffe://hvac.local/alarm-service")
	reqOK := reqNoCert.Clone(context.Background())
	reqOK.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{URIs: []*url.URL{alarmURI}}}}}
	recOK := httptest.NewRecorder()
	h.ServeHTTP(recOK, reqOK)
	if recOK.Code != http.StatusOK || stub.calls != 1 {
		t.Fatalf("alarm cert status=%d calls=%d body=%s", recOK.Code, stub.calls, recOK.Body.String())
	}
}
