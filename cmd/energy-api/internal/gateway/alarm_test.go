package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
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
		{path: "/api/v1/alarm-rules", template: "/api/v1/alarm-rules", action: alarmauth.ActionRead},
		{path: "/api/v1/alarm-rules/simulate", template: "/api/v1/alarm-rules/simulate", action: alarmauth.ActionRead},
		{path: "/api/v1/alarms/" + gatewayAlarmID, template: "/api/v1/alarms/{alarmId}", action: alarmauth.ActionRead, alarmID: gatewayAlarmID},
		{path: "/api/v1/alarms/" + gatewayAlarmID + "/ack", template: "/api/v1/alarms/{alarmId}/ack", action: alarmauth.ActionAck, alarmID: gatewayAlarmID},
		{path: "/api/v1/alarms/" + gatewayAlarmID + "/assign", template: "/api/v1/alarms/{alarmId}/assign", action: alarmauth.ActionAssign, alarmID: gatewayAlarmID},
		{path: "/api/v1/alarms/" + gatewayAlarmID + "/unassign", template: "/api/v1/alarms/{alarmId}/unassign", action: alarmauth.ActionUnassign, alarmID: gatewayAlarmID},
		{path: "/api/v1/alarms/" + gatewayAlarmID + "/suppress", template: "/api/v1/alarms/{alarmId}/suppress", action: alarmauth.ActionSuppress, alarmID: gatewayAlarmID},
		{path: "/api/v1/alarms/" + gatewayAlarmID + "/unsuppress", template: "/api/v1/alarms/{alarmId}/unsuppress", action: alarmauth.ActionUnsuppress, alarmID: gatewayAlarmID},
		{path: "/api/v1/alarms/" + gatewayAlarmID + "/evaluations", template: "/api/v1/alarms/{alarmId}/evaluations", action: alarmauth.ActionRead, alarmID: gatewayAlarmID},
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

func TestValidatePublicAlarmEvaluationsQueryBounds(t *testing.T) {
	if limit, ok := validatePublicAlarmEvaluationsQuery(url.Values{}); !ok || limit != alarmservice.DefaultAlarmEvaluationsLimit {
		t.Fatalf("empty query got (%d, %v)", limit, ok)
	}
	if limit, ok := validatePublicAlarmEvaluationsQuery(url.Values{"limit": {"100"}}); !ok || limit != 100 {
		t.Fatalf("limit=100 got (%d, %v)", limit, ok)
	}
	for _, invalid := range []url.Values{
		{"limit": {"0"}},
		{"limit": {"101"}},
		{"limit": {"abc"}},
		{"limit": {"10", "20"}},
		{"siteId": {gatewayAlarmSiteID}},
	} {
		if _, ok := validatePublicAlarmEvaluationsQuery(invalid); ok {
			t.Fatalf("query %v should be rejected", invalid)
		}
	}
}

func TestDirectAndHTTPAlarmAdaptersHandleSuppressionUnassignAndEvaluations(t *testing.T) {
	now := time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC)
	tenantID := "01910000-0000-7000-8000-000000000001"
	siteID := gatewayAlarmSiteID
	store, err := alarmservice.NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	alarm, err := store.Publish(context.Background(), tenantID, siteID, alarmservice.Publication{
		AlarmType: "HIGH_TEMP", SourceType: alarmmodel.SourceSiteRule, SourceReference: "rule-temp",
		RuleRevision: "r1", Title: "High Temp", Summary: "Temp exceeded",
		Severity: alarmmodel.SeverityMinor, OccurredAt: now.Format(time.RFC3339Nano),
		ActorType: "WORKLOAD", ActorID: "alarm-evaluator", CorrelationID: "gw-pub-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordEvaluation(tenantID, siteID, alarm.Fingerprint, alarm.AlarmID, 1, alarmservice.AlarmEvaluationRecord{
		EvaluatedAt: now.Format(time.RFC3339Nano), PolicyRevisionID: "01910000-2000-7000-8000-000000000001",
		AssignmentID: "01910000-2000-7000-8000-000000000002", InputRevision: "rev-1",
		Status: alarmservice.EvaluationMatched, Effect: alarmservice.EvaluationEffectPublish,
		Snapshot: json.RawMessage(`{"supplyTemp":31.5}`),
	}); err != nil {
		t.Fatal(err)
	}

	adapter := &directAlarmAdapter{store: store, now: func() time.Time { return now }}
	ctx := context.WithValue(context.Background(), routeSessionContextKey, bffSession{Session: sessionstore.Session{ID: "sess-1", TenantID: tenantID}})

	// Suppress rejects past timestamp before modifying store.
	pastReq := httptest.NewRequest(http.MethodPost, "/api/v1/alarms/"+alarm.AlarmID+"/suppress", strings.NewReader(`{"expectedVersion":1,"reason":"Maintenance","suppressedUntil":"2026-08-24T01:00:00Z"}`)).WithContext(ctx)
	pastReq.Header.Set("Idempotency-Key", "idem-past-1")
	if _, _, fail := adapter.Execute(ctx, pastReq, publicAlarmRoute{siteID: siteID, alarmID: alarm.AlarmID, action: alarmauth.ActionSuppress, now: now}, ""); fail == nil || fail.status != http.StatusBadRequest {
		t.Fatalf("past suppressedUntil failure = %#v, want 400", fail)
	}

	// Valid suppress -> assign -> unassign -> unsuppress -> evaluations.
	suppressReq := httptest.NewRequest(http.MethodPost, "/api/v1/alarms/"+alarm.AlarmID+"/suppress", strings.NewReader(`{"expectedVersion":1,"reason":"Maintenance","suppressedUntil":"2026-08-24T05:00:00Z"}`)).WithContext(ctx)
	suppressReq.Header.Set("Idempotency-Key", "idem-supp-1")
	suppBytes, status, fail := adapter.Execute(ctx, suppressReq, publicAlarmRoute{siteID: siteID, alarmID: alarm.AlarmID, action: alarmauth.ActionSuppress, now: now}, "")
	if fail != nil || status != http.StatusOK {
		t.Fatalf("direct suppress failed: status=%d fail=%#v", status, fail)
	}
	var suppressed alarmmodel.Alarm
	if json.Unmarshal(suppBytes, &suppressed) != nil || suppressed.Suppression == nil || suppressed.Version != 2 {
		t.Fatalf("unexpected suppressed alarm: %s", string(suppBytes))
	}

	evalReq := httptest.NewRequest(http.MethodGet, "/api/v1/alarms/"+alarm.AlarmID+"/evaluations?limit=5", nil).WithContext(ctx)
	evalBytes, status, fail := adapter.Execute(ctx, evalReq, publicAlarmRoute{siteID: siteID, alarmID: alarm.AlarmID, action: alarmauth.ActionRead, evaluations: true}, "")
	if fail != nil || status != http.StatusOK {
		t.Fatalf("direct evaluations failed: status=%d fail=%#v", status, fail)
	}
	var evalList alarmservice.AlarmEvaluationList
	if json.Unmarshal(evalBytes, &evalList) != nil || len(evalList.Items) != 1 || evalList.Items[0].InputRevision != "rev-1" {
		t.Fatalf("unexpected evaluations: %s", string(evalBytes))
	}

	// HTTP adapter forwards :suppress with X-Alarm-Write-Context and /evaluations?limit=5 with X-Alarm-Read-Context.
	var seenMethod, seenPath, seenQuery, seenWriteCtx, seenReadCtx string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		seenQuery = r.URL.RawQuery
		seenWriteCtx = r.Header.Get(alarmWriteContextHeader)
		seenReadCtx = r.Header.Get(alarmReadContextHeader)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	httpAdapter := newHTTPAlarmAdapter(AlarmConfig{
		BackendBaseURL: upstream.URL, BackendHTTPClient: upstream.Client(),
		Timeout: time.Second, MaxResponseBytes: 1 << 20, Now: func() time.Time { return now },
	})
	httpSuppReq := httptest.NewRequest(http.MethodPost, "/api/v1/alarms/"+alarm.AlarmID+"/suppress", strings.NewReader(`{"expectedVersion":2,"reason":"Upstream forward","suppressedUntil":"2026-08-24T06:00:00Z"}`))
	httpSuppReq.Header.Set("Content-Type", "application/json")
	httpSuppReq.Header.Set("Idempotency-Key", "idem-http-1")
	if _, status, fail := httpAdapter.Execute(ctx, httpSuppReq, publicAlarmRoute{siteID: siteID, alarmID: alarm.AlarmID, action: alarmauth.ActionSuppress, now: now}, "signed-write-ctx"); fail != nil || status != http.StatusOK {
		t.Fatalf("http suppress failed: status=%d fail=%#v", status, fail)
	}
	if seenMethod != http.MethodPost || seenPath != "/internal/v1/sites/"+siteID+"/alarms/"+alarm.AlarmID+":suppress" || seenWriteCtx != "signed-write-ctx" || seenReadCtx != "" {
		t.Fatalf("http suppress upstream mismatch: method=%s path=%s writeCtx=%q readCtx=%q", seenMethod, seenPath, seenWriteCtx, seenReadCtx)
	}

	httpEvalReq := httptest.NewRequest(http.MethodGet, "/api/v1/alarms/"+alarm.AlarmID+"/evaluations?limit=5", nil)
	if _, status, fail := httpAdapter.Execute(ctx, httpEvalReq, publicAlarmRoute{siteID: siteID, alarmID: alarm.AlarmID, action: alarmauth.ActionRead, evaluations: true}, "signed-read-ctx"); fail != nil || status != http.StatusOK {
		t.Fatalf("http evaluations failed: status=%d fail=%#v", status, fail)
	}
	if seenMethod != http.MethodGet || seenPath != "/internal/v1/sites/"+siteID+"/alarms/"+alarm.AlarmID+"/evaluations" || seenQuery != "limit=5" || seenReadCtx != "signed-read-ctx" || seenWriteCtx != "" {
		t.Fatalf("http evaluations upstream mismatch: method=%s path=%s query=%s readCtx=%q writeCtx=%q", seenMethod, seenPath, seenQuery, seenReadCtx, seenWriteCtx)
	}
}
