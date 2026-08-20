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

type notificationHTTPStore struct {
	items     []InboxItem
	listCalls int
	readCalls int
}

func (store *notificationHTTPStore) ListInbox(_ context.Context, tenantID, principalID string, _ int) ([]InboxItem, error) {
	store.listCalls++
	var result []InboxItem
	for _, item := range store.items {
		if item.TenantID == tenantID && item.PrincipalID == principalID {
			result = append(result, item)
		}
	}
	return result, nil
}

func (store *notificationHTTPStore) MarkRead(_ context.Context, tenantID, principalID, inboxItemID string, now time.Time) (InboxItem, error) {
	store.readCalls++
	for index := range store.items {
		item := &store.items[index]
		if item.TenantID == tenantID && item.PrincipalID == principalID && item.InboxItemID == inboxItemID {
			item.Status = InboxRead
			readAt := now.UTC()
			item.ReadAt = &readAt
			return *item, nil
		}
	}
	return InboxItem{}, ErrNotFound
}

func TestNotificationHTTPUsesExactAuthenticatedPrincipalScope(t *testing.T) {
	now := time.Date(2026, 8, 19, 20, 0, 0, 0, time.UTC)
	signer := notificationSigner(t)
	itemID := "01910000-9100-7000-8000-000000000001"
	store := &notificationHTTPStore{items: []InboxItem{{
		InboxItemID: itemID, IntentID: "01910000-9200-7000-8000-000000000001", TenantID: replayTenantID, SiteID: replaySiteID,
		PrincipalID: "principal:operator", AlarmID: "01910000-9300-7000-8000-000000000001", IncidentCorrelationID: "01910000-9400-7000-8000-000000000001",
		SourceAction: AlarmCreated, Status: InboxUnread, CreatedAt: now,
	}}}
	handler, err := NewHTTPHandler(HTTPConfig{Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	list := httptest.NewRequest(http.MethodGet, InternalInboxPath+"?limit=25", nil)
	list.Header.Set(NotificationContextHeader, signedNotificationContext(t, signer, now, NotificationReadAction, replayTenantID, "principal:operator", ""))
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK || store.listCalls != 1 {
		t.Fatalf("valid Notification principal scope was rejected: status=%d calls=%d body=%s", listRecorder.Code, store.listCalls, listRecorder.Body.String())
	}

	wrongPrincipal := httptest.NewRequest(http.MethodGet, InternalInboxPath, nil)
	wrongPrincipal.Header.Set(NotificationContextHeader, signedNotificationContextWithScope(t, signer, now, NotificationReadAction, replayTenantID, "principal:operator", []string{"tenant:" + replayTenantID, "principal:someone-else"}))
	wrongRecorder := httptest.NewRecorder()
	handler.ServeHTTP(wrongRecorder, wrongPrincipal)
	if wrongRecorder.Code != http.StatusForbidden || store.listCalls != 1 {
		t.Fatalf("mismatched Notification principal scope reached Store: status=%d calls=%d", wrongRecorder.Code, store.listCalls)
	}

	read := httptest.NewRequest(http.MethodPost, InternalInboxPath+"/"+itemID+"/read", nil)
	read.Header.Set(NotificationContextHeader, signedNotificationContext(t, signer, now, NotificationMarkReadAction, replayTenantID, "principal:operator", itemID))
	readRecorder := httptest.NewRecorder()
	handler.ServeHTTP(readRecorder, read)
	if readRecorder.Code != http.StatusOK || store.readCalls != 1 || store.items[0].Status != InboxRead {
		t.Fatalf("exact Notification read scope failed: status=%d calls=%d body=%s", readRecorder.Code, store.readCalls, readRecorder.Body.String())
	}
}

func TestNotificationHTTPRejectsForgedIdentityBeforeStore(t *testing.T) {
	now := time.Date(2026, 8, 19, 20, 0, 0, 0, time.UTC)
	signer := notificationSigner(t)
	store := &notificationHTTPStore{}
	handler, err := NewHTTPHandler(HTTPConfig{Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, InternalInboxPath, nil)
	request.Header.Set("X-Tenant-ID", replayTenantID)
	request.Header.Set(NotificationContextHeader, signedNotificationContext(t, signer, now, NotificationReadAction, replayTenantID, "principal:operator", ""))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || store.listCalls != 0 {
		t.Fatalf("forged Notification identity header reached Store: status=%d calls=%d", recorder.Code, store.listCalls)
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

func signedNotificationContext(t *testing.T, signer *ecdsa.PrivateKey, now time.Time, action, tenantID, principalID, itemID string) string {
	t.Helper()
	scopes := []string{"tenant:" + tenantID, "principal:" + principalID}
	if itemID != "" {
		scopes = append(scopes, "notification:"+itemID)
	}
	return signedNotificationContextWithScope(t, signer, now, action, tenantID, principalID, scopes)
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
