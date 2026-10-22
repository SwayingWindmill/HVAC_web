package notificationservice

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const notificationTenantID = "0190f000-0010-7000-8000-000000000001"

// countingReminders counts the calls that reach the WeChat reminder store.
type countingReminders struct {
	noReminders
	calls int
}

func (store *countingReminders) WeChatReminders(context.Context, WeChatPerson) (WeChatReminders, error) {
	store.calls++
	return WeChatReminders{}, nil
}

func TestNotificationHTTPUsesExactAuthenticatedPrincipalScope(t *testing.T) {
	now := time.Date(2026, 8, 19, 20, 0, 0, 0, time.UTC)
	signer := notificationSigner(t)
	store := &countingReminders{}
	handler, err := NewHTTPHandler(HTTPConfig{Reminders: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	read := httptest.NewRequest(http.MethodGet, InternalWeChatRemindersPath, nil)
	read.Header.Set(NotificationContextHeader, signedNotificationContext(t, signer, now, WeChatRemindersAction, notificationTenantID, "principal:operator"))
	readRecorder := httptest.NewRecorder()
	handler.ServeHTTP(readRecorder, read)
	if readRecorder.Code != http.StatusOK || store.calls != 1 {
		t.Fatalf("valid Notification principal scope was rejected: status=%d calls=%d body=%s", readRecorder.Code, store.calls, readRecorder.Body.String())
	}

	wrongPrincipal := httptest.NewRequest(http.MethodGet, InternalWeChatRemindersPath, nil)
	wrongPrincipal.Header.Set(NotificationContextHeader, signedNotificationContextWithScope(t, signer, now, WeChatRemindersAction, notificationTenantID, "principal:operator", []string{"tenant:" + notificationTenantID, "principal:someone-else"}))
	wrongRecorder := httptest.NewRecorder()
	handler.ServeHTTP(wrongRecorder, wrongPrincipal)
	if wrongRecorder.Code != http.StatusForbidden || store.calls != 1 {
		t.Fatalf("mismatched Notification principal scope reached Store: status=%d calls=%d", wrongRecorder.Code, store.calls)
	}
}

func TestNotificationHTTPRejectsForgedIdentityBeforeStore(t *testing.T) {
	now := time.Date(2026, 8, 19, 20, 0, 0, 0, time.UTC)
	signer := notificationSigner(t)
	store := &countingReminders{}
	handler, err := NewHTTPHandler(HTTPConfig{Reminders: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, InternalWeChatRemindersPath, nil)
	request.Header.Set("X-Tenant-ID", notificationTenantID)
	request.Header.Set(NotificationContextHeader, signedNotificationContext(t, signer, now, WeChatRemindersAction, notificationTenantID, "principal:operator"))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || store.calls != 0 {
		t.Fatalf("forged Notification identity header reached Store: status=%d calls=%d", recorder.Code, store.calls)
	}
}

func notificationSigner(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func signedNotificationContext(t *testing.T, signer *ecdsa.PrivateKey, now time.Time, action, tenantID, principalID string) string {
	t.Helper()
	return signedNotificationContextWithScope(t, signer, now, action, tenantID, principalID, []string{"tenant:" + tenantID, "principal:" + principalID})
}

func signedNotificationContextWithScope(t *testing.T, signer *ecdsa.PrivateKey, now time.Time, action, tenantID, principalID string, scopes []string) string {
	t.Helper()
	value, err := identitycontext.SignDelegation(signer, identitycontext.DelegationClaims{
		Issuer: DefaultGatewaySPIFFEID, Subject: principalID, SubjectIssuer: "https://identity.example.test", ExecutingService: DefaultGatewaySPIFFEID,
		Audience: DefaultAudience, TenantID: tenantID, Actions: []string{action}, Scopes: scopes, PolicyRevision: "policy-1",
		SessionID: "session-1", IssuedAt: now.Add(-time.Second).Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "id-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// noReminders is a WeChat reminder store for tests that do not touch reminders.
type noReminders struct{}

func (noReminders) WeChatReminders(context.Context, WeChatPerson) (WeChatReminders, error) {
	return WeChatReminders{}, nil
}

func (noReminders) AddWeChatReminder(context.Context, WeChatPerson, ReminderRequest, time.Time) (WeChatReminders, error) {
	return WeChatReminders{}, nil
}

func (noReminders) ClearWeChatReminders(context.Context, WeChatPerson, time.Time) error { return nil }
