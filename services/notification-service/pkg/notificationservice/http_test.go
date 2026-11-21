package notificationservice

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestNotificationHTTPSiteAlarmRoutingReadAndReloadDelegation(t *testing.T) {
	now := time.Date(2026, 8, 19, 20, 0, 0, 0, time.UTC)
	signer := notificationSigner(t)
	dir := t.TempDir()
	recipientsPath, routingDir := writeReloaderFixtureFiles(
		t,
		dir,
		validReloaderRecipientsJSON("duty-secret@example.test", "duty-wecom-secret"),
		validReloaderRoutingJSON(3, 60),
	)
	reloader, err := NewRoutingReloader(recipientsPath, routingDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	reloader.logger.Store(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))

	handler, err := NewHTTPHandler(HTTPConfig{
		Reminders:        &countingReminders{},
		Routing:          reloader,
		GatewayPublicKey: &signer.PublicKey,
		Now:              func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	readURL := InternalSiteAlarmRoutingPrefix + deliveryTestSiteID + "/alarm-routing"
	reloadURL := InternalSiteAlarmRoutingPrefix + deliveryTestSiteID + "/alarm-routing:reload"
	siteScope := []string{"tenant:" + notificationTenantID, "site:" + deliveryTestSiteID}

	// Missing token -> 403.
	unauth := httptest.NewRequest(http.MethodGet, readURL, nil)
	unauthRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthRec, unauth)
	if unauthRec.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated GET status = %d, want 403", unauthRec.Code)
	}

	// Principal-scoped WeChat token or wrong site scope -> 403.
	wrongScope := httptest.NewRequest(http.MethodGet, readURL, nil)
	wrongScope.Header.Set(NotificationContextHeader, signedNotificationContext(t, signer, now, AlarmRoutingReadAction, notificationTenantID, "principal:operator"))
	wrongScopeRec := httptest.NewRecorder()
	handler.ServeHTTP(wrongScopeRec, wrongScope)
	if wrongScopeRec.Code != http.StatusForbidden {
		t.Fatalf("principal-scoped token on site route status = %d, want 403", wrongScopeRec.Code)
	}

	otherSite := httptest.NewRequest(http.MethodGet, readURL, nil)
	otherSite.Header.Set(NotificationContextHeader, signedNotificationContextWithScope(t, signer, now, AlarmRoutingReadAction, notificationTenantID, "principal:operator", []string{"tenant:" + notificationTenantID, "site:01a14000-1000-7000-8000-0000000000ff"}))
	otherSiteRec := httptest.NewRecorder()
	handler.ServeHTTP(otherSiteRec, otherSite)
	if otherSiteRec.Code != http.StatusForbidden {
		t.Fatalf("other-site token status = %d, want 403", otherSiteRec.Code)
	}

	// Read action cannot call reload endpoint -> 403.
	readOnReload := httptest.NewRequest(http.MethodPost, reloadURL, nil)
	readOnReload.Header.Set(NotificationContextHeader, signedNotificationContextWithScope(t, signer, now, AlarmRoutingReadAction, notificationTenantID, "principal:operator", siteScope))
	readOnReloadRec := httptest.NewRecorder()
	handler.ServeHTTP(readOnReloadRec, readOnReload)
	if readOnReloadRec.Code != http.StatusForbidden {
		t.Fatalf("read action on reload endpoint status = %d, want 403", readOnReloadRec.Code)
	}

	// Method checks -> 405.
	postRead := httptest.NewRequest(http.MethodPost, readURL, nil)
	postReadRec := httptest.NewRecorder()
	handler.ServeHTTP(postReadRec, postRead)
	if postReadRec.Code != http.StatusMethodNotAllowed || postReadRec.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST on read endpoint = status %d allow %q", postReadRec.Code, postReadRec.Header().Get("Allow"))
	}
	getReload := httptest.NewRequest(http.MethodGet, reloadURL, nil)
	getReloadRec := httptest.NewRecorder()
	handler.ServeHTTP(getReloadRec, getReload)
	if getReloadRec.Code != http.StatusMethodNotAllowed || getReloadRec.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("GET on reload endpoint = status %d allow %q", getReloadRec.Code, getReloadRec.Header().Get("Allow"))
	}

	// Forged identity header -> 400.
	forged := httptest.NewRequest(http.MethodGet, readURL, nil)
	forged.Header.Set("X-Site-ID", deliveryTestSiteID)
	forged.Header.Set(NotificationContextHeader, signedNotificationContextWithScope(t, signer, now, AlarmRoutingReadAction, notificationTenantID, "principal:operator", siteScope))
	forgedRec := httptest.NewRecorder()
	handler.ServeHTTP(forgedRec, forged)
	if forgedRec.Code != http.StatusBadRequest {
		t.Fatalf("forged X-Site-ID status = %d, want 400", forgedRec.Code)
	}

	// Valid GET returns secret-free summary.
	getReq := httptest.NewRequest(http.MethodGet, readURL, nil)
	getReq.Header.Set(NotificationContextHeader, signedNotificationContextWithScope(t, signer, now, AlarmRoutingReadAction, notificationTenantID, "principal:operator", siteScope))
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("valid GET alarm-routing status = %d body=%s", getRec.Code, getRec.Body.String())
	}
	var summary SiteRoutingSummaryView
	if err := json.Unmarshal(getRec.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.SiteID != deliveryTestSiteID || summary.HoldMinutes != 3 || summary.ReloadCount != 0 {
		t.Fatalf("unexpected GET summary: %+v", summary)
	}
	for _, secret := range []string{"duty-secret@example.test", "duty-wecom-secret", "lead@example.test", "lead-secret-id", dir} {
		if strings.Contains(getRec.Body.String(), secret) {
			t.Fatalf("GET summary leaked secret/path %q: %s", secret, getRec.Body.String())
		}
	}

	// Valid POST reload when unchanged -> 200 changed=false.
	unchangedReq := httptest.NewRequest(http.MethodPost, reloadURL, nil)
	unchangedReq.Header.Set(NotificationContextHeader, signedNotificationContextWithScope(t, signer, now, AlarmRoutingManageAction, notificationTenantID, "principal:admin", siteScope))
	unchangedRec := httptest.NewRecorder()
	handler.ServeHTTP(unchangedRec, unchangedReq)
	if unchangedRec.Code != http.StatusOK {
		t.Fatalf("unchanged POST reload status = %d body=%s", unchangedRec.Code, unchangedRec.Body.String())
	}
	var reloadResp AlarmRoutingReloadResponse
	if err := json.Unmarshal(unchangedRec.Body.Bytes(), &reloadResp); err != nil {
		t.Fatal(err)
	}
	if reloadResp.Changed || reloadResp.ReloadCount != 0 {
		t.Fatalf("expected unchanged reload response, got %+v", reloadResp)
	}

	// Update config and POST reload -> 200 changed=true, reloadCount=1.
	if err := os.WriteFile(filepath.Join(routingDir, "songyaxuan.alarm-routing.json"), []byte(validReloaderRoutingJSON(8, 120)), 0o600); err != nil {
		t.Fatal(err)
	}
	changedReq := httptest.NewRequest(http.MethodPost, reloadURL, nil)
	changedReq.Header.Set(NotificationContextHeader, signedNotificationContextWithScope(t, signer, now, AlarmRoutingManageAction, notificationTenantID, "principal:admin", siteScope))
	changedRec := httptest.NewRecorder()
	handler.ServeHTTP(changedRec, changedReq)
	if changedRec.Code != http.StatusOK {
		t.Fatalf("valid POST reload status = %d body=%s", changedRec.Code, changedRec.Body.String())
	}
	if err := json.Unmarshal(changedRec.Body.Bytes(), &reloadResp); err != nil {
		t.Fatal(err)
	}
	if !reloadResp.Changed || reloadResp.ReloadCount != 1 || reloadResp.HoldMinutes != 8 || reloadResp.Routes[0].GroupWindowSeconds != 120 {
		t.Fatalf("unexpected changed reload response: %+v", reloadResp)
	}

	// Corrupt config and POST reload -> 422 with bounded error code; GET still returns known-good holdMinutes=8 + lastReloadErrorCode.
	if err := os.WriteFile(filepath.Join(routingDir, "songyaxuan.alarm-routing.json"), []byte(`{corrupt`), 0o600); err != nil {
		t.Fatal(err)
	}
	badReloadReq := httptest.NewRequest(http.MethodPost, reloadURL, nil)
	badReloadReq.Header.Set(NotificationContextHeader, signedNotificationContextWithScope(t, signer, now, AlarmRoutingManageAction, notificationTenantID, "principal:admin", siteScope))
	badReloadRec := httptest.NewRecorder()
	handler.ServeHTTP(badReloadRec, badReloadReq)
	if badReloadRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("corrupt POST reload status = %d body=%s", badReloadRec.Code, badReloadRec.Body.String())
	}
	if strings.Contains(badReloadRec.Body.String(), dir) {
		t.Fatalf("reload error response leaked OS path: %s", badReloadRec.Body.String())
	}

	afterBadGet := httptest.NewRequest(http.MethodGet, readURL, nil)
	afterBadGet.Header.Set(NotificationContextHeader, signedNotificationContextWithScope(t, signer, now, AlarmRoutingReadAction, notificationTenantID, "principal:operator", siteScope))
	afterBadGetRec := httptest.NewRecorder()
	handler.ServeHTTP(afterBadGetRec, afterBadGet)
	if afterBadGetRec.Code != http.StatusOK {
		t.Fatalf("GET after failed reload status = %d", afterBadGetRec.Code)
	}
	if err := json.Unmarshal(afterBadGetRec.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.HoldMinutes != 8 || summary.LastReloadErrorCode != ReloadErrorRoutingInvalid {
		t.Fatalf("expected known-good config + error code after failed reload, got %+v", summary)
	}

	// Unconfigured Routing reloader returns 503 after auth.
	noReloaderHandler, err := NewHTTPHandler(HTTPConfig{
		Reminders:        &countingReminders{},
		GatewayPublicKey: &signer.PublicKey,
		Now:              func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	unconfiguredReq := httptest.NewRequest(http.MethodGet, readURL, nil)
	unconfiguredReq.Header.Set(NotificationContextHeader, signedNotificationContextWithScope(t, signer, now, AlarmRoutingReadAction, notificationTenantID, "principal:operator", siteScope))
	unconfiguredRec := httptest.NewRecorder()
	noReloaderHandler.ServeHTTP(unconfiguredRec, unconfiguredReq)
	if unconfiguredRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured reloader GET status = %d, want 503", unconfiguredRec.Code)
	}
}
