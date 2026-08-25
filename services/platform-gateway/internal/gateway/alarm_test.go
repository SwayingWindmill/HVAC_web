package gateway

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/services/alarm-service/pkg/alarmservice"
)

const (
	gatewayAlarmSiteID = "01910000-0001-7000-8000-000000000001"
	gatewayAlarmID     = "01910000-1000-7000-8000-000000000001"
)

func TestMatchPublicAlarmRoutesUsesDomainV1Shape(t *testing.T) {
	tests := []struct {
		path     string
		template string
		action   alarmauth.Action
		alarmID  string
	}{
		{path: "/api/v1/alarms", template: "/api/v1/alarms", action: alarmauth.ActionRead},
		{path: "/api/v1/alarms/" + gatewayAlarmID, template: "/api/v1/alarms/{alarmId}", action: alarmauth.ActionRead, alarmID: gatewayAlarmID},
		{path: "/api/v1/alarms/" + gatewayAlarmID + "/ack", template: "/api/v1/alarms/{alarmId}/ack", action: alarmauth.ActionAck, alarmID: gatewayAlarmID},
	}
	for _, test := range tests {
		route, ok := matchPublicAlarmRoute(test.path)
		if !ok || route.template != test.template || route.action != test.action || route.alarmID != test.alarmID {
			t.Fatalf("path=%s route=%#v matched=%v", test.path, route, ok)
		}
	}
	for _, legacy := range []string{
		"/api/v1/sites/" + gatewayAlarmSiteID + "/alarms",
		"/api/v1/sites/" + gatewayAlarmSiteID + "/alarms/" + gatewayAlarmID,
		"/api/v1/alarms/" + gatewayAlarmID + ":acknowledge",
	} {
		if route, ok := matchPublicAlarmRoute(legacy); ok {
			t.Fatalf("legacy Alarm route remained public: %s -> %#v", legacy, route)
		}
	}
}

func TestValidatePublicAlarmQueryRequiresSiteAndCursorBounds(t *testing.T) {
	valid := url.Values{
		"siteId":       {gatewayAlarmSiteID},
		"severity":     {"MINOR"},
		"condition":    {"ACTIVE"},
		"acknowledged": {"true"},
		"suppressed":   {"false"},
		"limit":        {"200"},
		"cursor":       {"opaque-cursor"},
	}
	siteID, limit, ok := validatePublicAlarmQuery(valid)
	if !ok || siteID != gatewayAlarmSiteID || limit != 200 {
		t.Fatalf("valid Alarm query rejected: site=%q limit=%d ok=%v", siteID, limit, ok)
	}

	for name, query := range map[string]url.Values{
		"missing site":      {"limit": {"50"}},
		"limit zero":        {"siteId": {gatewayAlarmSiteID}, "limit": {"0"}},
		"limit high":        {"siteId": {gatewayAlarmSiteID}, "limit": {"201"}},
		"unknown":           {"siteId": {gatewayAlarmSiteID}, "deviceId": {"caller-supplied"}},
		"invalid condition": {"siteId": {gatewayAlarmSiteID}, "condition": {"ACKNOWLEDGED"}},
		"invalid boolean":   {"siteId": {gatewayAlarmSiteID}, "acknowledged": {"yes"}},
		"long cursor":       {"siteId": {gatewayAlarmSiteID}, "cursor": {strings.Repeat("x", 4097)}},
	} {
		if _, _, ok := validatePublicAlarmQuery(query); ok {
			t.Fatalf("invalid Alarm query accepted: %s", name)
		}
	}
}

func TestDirectAlarmAdapterResolvesAndExecutes(t *testing.T) {
	tenantID := "01910000-0000-7000-8000-000000000001"
	ctx := context.WithValue(context.Background(), routeSessionContextKey, bffSession{Session: sessionstore.Session{TenantID: tenantID}})
	siteID := gatewayAlarmSiteID

	pub := alarmservice.Publication{
		AlarmType:       "SUPPLY_TEMPERATURE_DRIFT",
		SourceType:      alarmmodel.SourceSiteRule,
		SourceReference: "rule:central-plant-temperature-drift:v3",
		RuleRevision:    "alarm-policy-10",
		Title:           "Supply temperature drift",
		Summary:         "Supply temperature is outside the governed operating band.",
		Severity:        alarmmodel.SeverityMajor,
		OccurredAt:      "2026-08-24T01:00:00Z",
		Evidence:        []alarmmodel.EvidenceReference{{Kind: "telemetry-snapshot", Reference: "snapshot:publication", CapturedAt: "2026-08-24T01:00:00Z"}},
		ActorType:       "WORKLOAD",
		ActorID:         "alarm-evaluator",
		CorrelationID:   "publication-1",
	}

	store, err := alarmservice.NewMemoryStore(nil)
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	alarm, err := store.Publish(ctx, tenantID, siteID, pub)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	adapter := newDirectAlarmAdapter(store)

	// Test ResolveScope
	scope, failure := adapter.ResolveScope(ctx, tenantID, alarm.AlarmID, "")
	if failure != nil {
		t.Fatalf("ResolveScope failed: %#v", failure)
	}
	if scope.TenantID != tenantID || scope.SiteID != siteID {
		t.Fatalf("unexpected scope: %#v", scope)
	}

	// Test ResolveScope unknown
	_, failure = adapter.ResolveScope(ctx, tenantID, "01910000-9999-7000-8000-000000000001", "")
	if failure == nil || failure.status != 404 {
		t.Fatalf("expected 404 for unknown alarm, got: %#v", failure)
	}

	// Test Execute List
	reqList, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/alarms?siteId="+siteID, nil)
	listBytes, status, failure := adapter.Execute(ctx, reqList, publicAlarmRoute{template: "/api/v1/alarms", siteID: siteID, action: alarmauth.ActionRead}, "")
	if failure != nil || status != http.StatusOK {
		t.Fatalf("List failed: status=%d failure=%#v", status, failure)
	}
	if len(listBytes) == 0 {
		t.Fatal("empty list response")
	}

	// Test Execute Get
	reqGet, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/alarms/"+alarm.AlarmID, nil)
	getBytes, status, failure := adapter.Execute(ctx, reqGet, publicAlarmRoute{template: "/api/v1/alarms/{alarmId}", siteID: siteID, alarmID: alarm.AlarmID, action: alarmauth.ActionRead}, "")
	if failure != nil || status != http.StatusOK {
		t.Fatalf("Get failed: status=%d failure=%#v", status, failure)
	}
	if len(getBytes) == 0 {
		t.Fatal("empty get response")
	}

	// Test Execute Ack
	ackBody := strings.NewReader(`{"reason":"Acknowledged by operator"}`)
	reqAck, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/alarms/"+alarm.AlarmID+"/ack", ackBody)
	reqAck.Header.Set("Idempotency-Key", "idemp-test-ack-001")
	reqAck.Header.Set("Content-Type", "application/json")
	ackBytes, status, failure := adapter.Execute(ctx, reqAck, publicAlarmRoute{template: "/api/v1/alarms/{alarmId}/ack", siteID: siteID, alarmID: alarm.AlarmID, action: alarmauth.ActionAck}, "")
	if failure != nil || status != http.StatusOK {
		t.Fatalf("Ack failed: status=%d failure=%#v", status, failure)
	}
	if len(ackBytes) == 0 {
		t.Fatal("empty ack response")
	}
}

