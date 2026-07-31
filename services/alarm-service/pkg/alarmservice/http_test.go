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
	testOrganizationID = "018f3e00-1000-7000-8000-000000000001"
	testSiteID         = "018f3e00-2000-7000-8000-000000000001"
	testOtherSiteID    = "018f3e00-2000-7000-8000-000000000002"
	testAlarmID        = "018f3e00-4000-7000-8000-000000000001"
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

type invalidProjectionStore struct{ alarm alarmmodel.Alarm }

func (store invalidProjectionStore) List(context.Context, string, string, Filter) (alarmmodel.ListResponse, error) {
	return alarmmodel.ListResponse{SchemaVersion: alarmmodel.SchemaVersion, Items: []alarmmodel.Alarm{store.alarm}}, nil
}

func (store invalidProjectionStore) Get(context.Context, string, string, string) (alarmmodel.Alarm, error) {
	return store.alarm, nil
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

	list := httptest.NewRequest(http.MethodGet, InternalSiteAlarmsPrefix+testSiteID+"/alarms?status=OPEN&severity=MAJOR&limit=25", nil)
	list.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmListAction, testOrganizationID, testSiteID, ""))
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
	detail.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmReadAction, testOrganizationID, testSiteID, testAlarmID))
	detailRecorder := httptest.NewRecorder()
	handler.ServeHTTP(detailRecorder, detail)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", detailRecorder.Code, detailRecorder.Body.String())
	}
	var alarm alarmmodel.Alarm
	if json.NewDecoder(detailRecorder.Body).Decode(&alarm) != nil || alarm.AlarmID != testAlarmID || alarm.SourceReference == "" || len(alarm.Transitions) != 1 {
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
	forged.Header.Set("X-Organization-ID", testOrganizationID)
	forged.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmListAction, testOrganizationID, testSiteID, ""))
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
	request.Header.Set(AlarmReadContextHeader, signedReadContextWithActions(t, signer, now, []string{AlarmListAction, AlarmReadAction}, testOrganizationID, testSiteID, ""))
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
	request.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmListAction, testOrganizationID, testOtherSiteID, ""))
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
	request.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmListAction, testOrganizationID, testSiteID, ""))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway || strings.Contains(recorder.Body.String(), testOtherSiteID) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func signedReadContext(t *testing.T, signer *ecdsa.PrivateKey, now time.Time, action, organizationID, siteID, alarmID string) string {
	t.Helper()
	return signedReadContextWithActions(t, signer, now, []string{action}, organizationID, siteID, alarmID)
}

func signedReadContextWithActions(t *testing.T, signer *ecdsa.PrivateKey, now time.Time, actions []string, organizationID, siteID, alarmID string) string {
	t.Helper()
	scopes := []string{"organization:" + organizationID, "site:" + siteID}
	if alarmID != "" {
		scopes = append(scopes, "alarm:"+alarmID)
	}
	value, err := identitycontext.SignDelegation(signer, identitycontext.DelegationClaims{
		Issuer: DefaultGatewaySPIFFEID, Subject: "operator", SubjectIssuer: "https://identity.example.test",
		DisplayName: "Operator", ExecutingService: DefaultGatewaySPIFFEID, Audience: DefaultAudience,
		ActingOrganizationID: organizationID, Actions: actions, Scopes: scopes,
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

func validHTTPAlarm() alarmmodel.Alarm {
	status := alarmmodel.StatusOpen
	return alarmmodel.Alarm{
		SchemaVersion: alarmmodel.SchemaVersion, AlarmID: testAlarmID, OrganizationID: testOrganizationID, SiteID: testSiteID,
		SourceType: alarmmodel.SourceSiteRule, SourceReference: "rule:central-plant-temperature-drift:v3",
		Title: "Supply temperature drift", Summary: "Alarm Service published a durable operational exception.",
		Severity: alarmmodel.SeverityMajor, Status: status, OccurrenceCount: 2,
		FirstOccurredAt: "2026-07-31T09:00:00Z", LastOccurredAt: "2026-07-31T09:05:00Z",
		Evidence:    []alarmmodel.EvidenceReference{{Kind: "telemetry-snapshot", Reference: "snapshot:41", CapturedAt: "2026-07-31T09:05:00Z"}},
		Transitions: []alarmmodel.Transition{{ToStatus: status, Reason: "ALARM_PUBLISHED", ActorType: "WORKLOAD", OccurredAt: "2026-07-31T09:00:00Z", Version: 1}},
		Version:     1, CreatedAt: "2026-07-31T09:00:00Z", UpdatedAt: "2026-07-31T09:05:00Z",
	}
}
