package alarmservice

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	testTenantID    = "018f3d00-1000-7000-8000-000000000001"
	testSiteID      = "018f3e00-2000-7000-8000-000000000001"
	testOtherSiteID = "018f3e00-2000-7000-8000-000000000002"
	testAlarmID     = "018f3e00-4000-7000-8000-000000000001"
)

type countingStore struct {
	delegate Store
	calls    atomic.Int32
}

func (store *countingStore) List(ctx context.Context, organizationID, siteID string, filter Filter) (alarmmodel.ListResponse, error) {
	store.calls.Add(1)
	return store.delegate.List(ctx, organizationID, siteID, filter)
}

func (store *countingStore) Get(ctx context.Context, organizationID, siteID, alarmID string) (alarmmodel.Alarm, error) {
	store.calls.Add(1)
	return store.delegate.Get(ctx, organizationID, siteID, alarmID)
}

func (store *countingStore) ResolveScope(ctx context.Context, tenantID, alarmID string) (AlarmScope, error) {
	store.calls.Add(1)
	return store.delegate.ResolveScope(ctx, tenantID, alarmID)
}

func (store *countingStore) Apply(ctx context.Context, tenantID, siteID, alarmID string, mutation Mutation) (MutationResult, error) {
	store.calls.Add(1)
	return store.delegate.Apply(ctx, tenantID, siteID, alarmID, mutation)
}

func (store *countingStore) Publish(ctx context.Context, tenantID, siteID string, publication Publication) (alarmmodel.Alarm, error) {
	store.calls.Add(1)
	return store.delegate.Publish(ctx, tenantID, siteID, publication)
}

func (store *countingStore) ClearActive(ctx context.Context, tenantID, siteID string, recovery Recovery) (alarmmodel.Alarm, error) {
	store.calls.Add(1)
	return store.delegate.ClearActive(ctx, tenantID, siteID, recovery)
}

type invalidProjectionStore struct{ alarm alarmmodel.Alarm }

func (store invalidProjectionStore) List(context.Context, string, string, Filter) (alarmmodel.ListResponse, error) {
	return alarmmodel.ListResponse{SchemaVersion: alarmmodel.SchemaVersion, Items: []alarmmodel.Alarm{store.alarm}}, nil
}

func (store invalidProjectionStore) Get(context.Context, string, string, string) (alarmmodel.Alarm, error) {
	return store.alarm, nil
}

func (store invalidProjectionStore) ResolveScope(context.Context, string, string) (AlarmScope, error) {
	return AlarmScope{TenantID: store.alarm.TenantID, SiteID: store.alarm.SiteID}, nil
}

func (store invalidProjectionStore) Apply(context.Context, string, string, string, Mutation) (MutationResult, error) {
	return MutationResult{}, ErrUnavailable
}

func (store invalidProjectionStore) Publish(context.Context, string, string, Publication) (alarmmodel.Alarm, error) {
	return alarmmodel.Alarm{}, ErrUnavailable
}

func (store invalidProjectionStore) ClearActive(context.Context, string, string, Recovery) (alarmmodel.Alarm, error) {
	return alarmmodel.Alarm{}, ErrUnavailable
}

func TestAlarmHTTPListsAndReadsExactScopedProjection(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore([]alarmmodel.Alarm{validHTTPAlarm()})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(HTTPConfig{Store: memory, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	list := httptest.NewRequest(http.MethodGet, InternalSiteAlarmsPrefix+testSiteID+"/alarms?condition=ACTIVE&severity=MAJOR&acknowledged=false&suppressed=false&limit=25", nil)
	list.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmListAction, testTenantID, testSiteID, ""))
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var response alarmmodel.ListResponse
	if json.NewDecoder(listRecorder.Body).Decode(&response) != nil || len(response.Items) != 1 || response.Items[0].AlarmID != testAlarmID {
		t.Fatalf("unexpected list %#v", response)
	}

	detail := httptest.NewRequest(http.MethodGet, InternalSiteAlarmsPrefix+testSiteID+"/alarms/"+testAlarmID, nil)
	detail.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmReadAction, testTenantID, testSiteID, testAlarmID))
	detailRecorder := httptest.NewRecorder()
	handler.ServeHTTP(detailRecorder, detail)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", detailRecorder.Code, detailRecorder.Body.String())
	}
	var alarm alarmmodel.Alarm
	if json.NewDecoder(detailRecorder.Body).Decode(&alarm) != nil || alarm.AlarmID != testAlarmID || alarm.SourceReference == "" || len(alarm.Timeline) != 1 {
		t.Fatalf("unexpected Alarm %#v", alarm)
	}
}

func TestAlarmHTTPRejectsMissingAndForgedIdentityBeforeStore(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore([]alarmmodel.Alarm{validHTTPAlarm()})
	if err != nil {
		t.Fatal(err)
	}
	store := &countingStore{delegate: memory}
	handler, err := NewHTTPHandler(HTTPConfig{Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	missing := httptest.NewRequest(http.MethodGet, InternalSiteAlarmsPrefix+testSiteID+"/alarms", nil)
	missingRecorder := httptest.NewRecorder()
	handler.ServeHTTP(missingRecorder, missing)
	if missingRecorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", missingRecorder.Code, missingRecorder.Body.String())
	}

	forged := httptest.NewRequest(http.MethodGet, InternalSiteAlarmsPrefix+testSiteID+"/alarms", nil)
	forged.Header.Set("X-Organization-ID", testTenantID)
	forged.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmListAction, testTenantID, testSiteID, ""))
	forgedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(forgedRecorder, forged)
	if forgedRecorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", forgedRecorder.Code, forgedRecorder.Body.String())
	}
	if store.calls.Load() != 0 {
		t.Fatal("unauthorized Alarm request reached Store")
	}
}

func TestAlarmHTTPRejectsReadContextWithAdditionalAction(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore([]alarmmodel.Alarm{validHTTPAlarm()})
	if err != nil {
		t.Fatal(err)
	}
	store := &countingStore{delegate: memory}
	handler, err := NewHTTPHandler(HTTPConfig{Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, InternalSiteAlarmsPrefix+testSiteID+"/alarms", nil)
	request.Header.Set(AlarmReadContextHeader, signedReadContextWithActions(t, signer, now, []string{AlarmListAction, AlarmReadAction}, testTenantID, testSiteID, ""))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || store.calls.Load() != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, store.calls.Load(), recorder.Body.String())
	}
}

func TestAlarmHTTPRejectsCrossSiteReadContextWithoutLeakage(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore([]alarmmodel.Alarm{validHTTPAlarm()})
	if err != nil {
		t.Fatal(err)
	}
	store := &countingStore{delegate: memory}
	handler, err := NewHTTPHandler(HTTPConfig{Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, InternalSiteAlarmsPrefix+testSiteID+"/alarms", nil)
	request.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmListAction, testTenantID, testOtherSiteID, ""))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || strings.Contains(recorder.Body.String(), testOtherSiteID) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.calls.Load() != 0 {
		t.Fatal("cross-Site Alarm request reached Store")
	}
}

func TestAlarmHTTPRejectsCrossScopeStoreProjection(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	alarm := validHTTPAlarm()
	alarm.SiteID = testOtherSiteID
	handler, err := NewHTTPHandler(HTTPConfig{Store: invalidProjectionStore{alarm: alarm}, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, InternalSiteAlarmsPrefix+testSiteID+"/alarms", nil)
	request.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmListAction, testTenantID, testSiteID, ""))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway || strings.Contains(recorder.Body.String(), testOtherSiteID) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAlarmHTTPAppliesAndReplaysAcknowledgement(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore([]alarmmodel.Alarm{validHTTPAlarm()})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(HTTPConfig{Store: memory, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	path := InternalSiteAlarmsPrefix + testSiteID + "/alarms/" + testAlarmID + ":acknowledge"
	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"comment":"operator acknowledged"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "alarm-http-ack-1")
		request.Header.Set(AlarmWriteContextHeader, signedReadContext(t, signer, now, AlarmAcknowledgeAction, testTenantID, testSiteID, testAlarmID))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("attempt=%d status=%d body=%s", attempt, recorder.Code, recorder.Body.String())
		}
		if attempt == 1 && recorder.Header().Get("Idempotent-Replay") != "true" {
			t.Fatal("idempotent replay header is missing")
		}
		var alarm alarmmodel.Alarm
		if json.NewDecoder(recorder.Body).Decode(&alarm) != nil || alarm.Condition != alarmmodel.ConditionActive || alarm.Acknowledgement == nil || alarm.Version != 2 || len(alarm.Timeline) != 2 {
			t.Fatalf("unexpected acknowledged Alarm: %#v", alarm)
		}
	}
	current, err := memory.Get(context.Background(), testTenantID, testSiteID, testAlarmID)
	if err != nil || current.Version != 2 || len(current.Timeline) != 2 {
		t.Fatalf("idempotent replay duplicated transition: %#v err=%v", current, err)
	}
}

func TestAlarmHTTPAcknowledgementIsNaturallyIdempotentWithoutKey(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore([]alarmmodel.Alarm{validHTTPAlarm()})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(HTTPConfig{Store: memory, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	acknowledgePath := InternalSiteAlarmsPrefix + testSiteID + "/alarms/" + testAlarmID + ":acknowledge"
	for _, comment := range []string{"first acknowledgement", "duplicate acknowledgement"} {
		request := httptest.NewRequest(http.MethodPost, acknowledgePath, strings.NewReader(`{"comment":"`+comment+`"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(AlarmWriteContextHeader, signedReadContext(t, signer, now, AlarmAcknowledgeAction, testTenantID, testSiteID, testAlarmID))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("comment=%q status=%d body=%s", comment, recorder.Code, recorder.Body.String())
		}
	}
	current, err := memory.Get(context.Background(), testTenantID, testSiteID, testAlarmID)
	if err != nil || current.Condition != alarmmodel.ConditionActive || current.Acknowledgement == nil || current.Version != 2 || len(current.Timeline) != 2 {
		t.Fatalf("natural acknowledgement idempotency failed: %#v err=%v", current, err)
	}
	if current.Acknowledgement.Comment != "first acknowledgement" {
		t.Fatalf("first acknowledgement fact was replaced: %#v", current.Acknowledgement)
	}
}

func TestAlarmHTTPRejectsWrongWriteActionBeforeStore(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore([]alarmmodel.Alarm{validHTTPAlarm()})
	if err != nil {
		t.Fatal(err)
	}
	store := &countingStore{delegate: memory}
	handler, err := NewHTTPHandler(HTTPConfig{Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, InternalSiteAlarmsPrefix+testSiteID+"/alarms/"+testAlarmID+":assign", strings.NewReader(`{"expectedVersion":1,"reason":"assign","assigneeId":"principal:operator-2"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "alarm-http-assign-1")
	request.Header.Set(AlarmWriteContextHeader, signedReadContextWithActions(t, signer, now, []string{AlarmSuppressAction, AlarmReadAction}, testTenantID, testSiteID, testAlarmID))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || store.calls.Load() != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, store.calls.Load(), recorder.Body.String())
	}
}

func signedReadContext(t *testing.T, signer *ecdsa.PrivateKey, now time.Time, action, tenantID, siteID, alarmID string) string {
	t.Helper()
	return signedReadContextWithActions(t, signer, now, []string{action}, tenantID, siteID, alarmID)
}

func signedReadContextWithActions(t *testing.T, signer *ecdsa.PrivateKey, now time.Time, actions []string, tenantID, siteID, alarmID string) string {
	t.Helper()
	scopes := []string{"tenant:" + tenantID, "site:" + siteID}
	if alarmID != "" {
		scopes = append(scopes, "alarm:"+alarmID)
	}
	value, err := identitycontext.SignDelegation(signer, identitycontext.DelegationClaims{
		Issuer: DefaultGatewaySPIFFEID, Subject: "operator", SubjectIssuer: "https://identity.example.test",
		DisplayName: "Operator", ExecutingService: DefaultGatewaySPIFFEID, Audience: DefaultAudience,
		TenantID: tenantID, Actions: actions, Scopes: scopes,
		PolicyRevision: "policy-1", SessionID: "session-1", IssuedAt: now.Add(-time.Second).Unix(),
		ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "id-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func testSigner(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func validHTTPAlarm(test ...*testing.T) alarmmodel.Alarm {
	alarm, err := alarmmodel.NewIncident(alarmmodel.IncidentInput{
		AlarmID: testAlarmID, TenantID: testTenantID, SiteID: testSiteID,
		AlarmType: "SUPPLY_TEMPERATURE_DRIFT", IncidentCorrelationID: "01910000-1000-7000-8000-000000000002",
		SourceType: alarmmodel.SourceSiteRule, SourceReference: "rule:central-plant-temperature-drift:v3", RuleRevision: "alarm-policy-9",
		Title: "Supply temperature drift", Summary: "Alarm Service published a durable operational exception.",
		Severity: alarmmodel.SeverityMajor, OccurredAt: "2026-07-31T09:00:00Z",
		Evidence:  []alarmmodel.EvidenceReference{{Kind: "telemetry-snapshot", Reference: "snapshot:41", CapturedAt: "2026-07-31T09:00:00Z"}},
		ActorType: "WORKLOAD", ActorID: "alarm-evaluator",
	})
	if err != nil {
		if len(test) != 0 {
			test[0].Fatal(err)
		}
		panic(err)
	}
	return alarm
}
