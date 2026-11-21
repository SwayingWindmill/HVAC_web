package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
	"github.com/quanlaihe/hvac-web/services/notification-service/pkg/notificationservice"
)

type stubWeChatReminderStore struct{}

func (stubWeChatReminderStore) WeChatReminders(context.Context, notificationservice.WeChatPerson) (notificationservice.WeChatReminders, error) {
	return notificationservice.WeChatReminders{}, nil
}

func (stubWeChatReminderStore) AddWeChatReminder(context.Context, notificationservice.WeChatPerson, notificationservice.ReminderRequest, time.Time) (notificationservice.WeChatReminders, error) {
	return notificationservice.WeChatReminders{}, nil
}

func (stubWeChatReminderStore) ClearWeChatReminders(context.Context, notificationservice.WeChatPerson, time.Time) error {
	return nil
}

func TestGatewayAlarmRoutingSummaryAndHotReload(t *testing.T) {
	now := time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC)
	signer := commandTestSigner(t)
	fixture := &alarmContractFixture{
		tenantID: "01910000-0000-7000-8000-000000000001",
		siteID:   gatewayAlarmSiteID,
	}

	tempDir := t.TempDir()
	recipientsPath := filepath.Join(tempDir, "recipients.json")
	routingDir := filepath.Join(tempDir, "routing")
	if err := os.MkdirAll(routingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	recipientsJSON := `{
  "recipients": {
    "duty-a": {
      "name": "值班工程师A",
      "email": "duty-a@example.com",
      "wecomUserId": "duty-a-wecom"
    }
  }
}`
	writeRoutingFile := func(holdMinutes, groupWindow int) {
		t.Helper()
		payload := fmt.Sprintf(`{
  "schemaVersion": 1,
  "siteId": %q,
  "siteName": "中央冷站",
  "timezone": "Asia/Shanghai",
  "holdMinutes": %d,
  "maxAgeHours": 24,
  "rateLimitPerHour": 20,
  "routes": [
    {
      "name": "default-major",
      "minSeverity": "MAJOR",
      "cooldownMinutes": 10,
      "groupWindowSeconds": %d,
      "groupMaxItems": 10,
      "groupBy": "severity",
      "notifyAck": true,
      "notifyRecovery": true,
      "steps": [
        {
          "afterMinutes": 0,
          "channels": ["smtp"],
          "recipients": ["duty-a"]
        }
      ]
    }
  ]
}`, fixture.siteID, holdMinutes, groupWindow)
		if err := os.WriteFile(filepath.Join(routingDir, "site.alarm-routing.json"), []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(recipientsPath, []byte(recipientsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRoutingFile(5, 60)

	reloader, err := notificationservice.NewRoutingReloader(recipientsPath, routingDir, nil)
	if err != nil {
		t.Fatalf("NewRoutingReloader: %v", err)
	}
	notificationHandler, err := notificationservice.NewHTTPHandler(notificationservice.HTTPConfig{
		Reminders:        stubWeChatReminderStore{},
		Routing:          reloader,
		GatewayPublicKey: &signer.PublicKey,
		GatewaySPIFFEID:  "spiffe://hvac.local/platform-gateway",
		Audience:         notificationservice.DefaultAudience,
		Now:              func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("notification NewHTTPHandler: %v", err)
	}
	notifServer := httptest.NewServer(notificationHandler)
	defer notifServer.Close()

	store, err := alarmservice.NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := ownershipregistry.Parse(readRepositoryFile(t, "contracts", "ownership", "route-ownership.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := sessionstore.NewMemoryStore()
	configured := NewHandler(Config{
		Now:          func() time.Time { return now },
		RouteManager: ownershipregistry.NewManager(ownership, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now }),
		Identity: &IdentityConfig{
			OIDCIssuer: "https://issuer.example.test", OIDCClientID: "client", OIDCRedirectURI: "https://web.example.test/api/v1/auth/callback",
			PublicOrigin: "https://web.example.test", IAMURL: "https://iam.example.test", IAMAudience: "iam-service",
			ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway", PolicyRevision: "identity-policy-1", DelegationSigner: signer,
			TokenEncryptionKey: make([]byte, 32), SessionStore: sessions, SessionTTL: time.Hour, DelegationTTL: 30 * time.Second,
			IAMHTTPClient: fixture.iamClient(t, signer, now),
		},
		Registry:     &RegistryConfig{CoreBaseURL: "https://registry.example.test", CoreHTTPClient: fixture.registryClient(now), CoreTimeout: time.Second},
		Alarm:        &AlarmConfig{Store: store, BackendAudience: "alarm-service", Timeout: time.Second, Now: func() time.Time { return now }},
		Notification: &NotificationConfig{BackendBaseURL: notifServer.URL, BackendHTTPClient: notifServer.Client(), BackendAudience: notificationservice.DefaultAudience, Timeout: time.Second},
	}).(*handler)
	fixture.handler = configured

	ciphertext, err := configured.identity.encryptBytes([]byte("alarm-contract-csrf"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.CreateSession(context.Background(), sessionstore.Session{
		Client: sessionstore.ClientBrowser, ID: "alarm-routing-session",
		Principal: identitycontext.UserPrincipal{Subject: "alarm-operator", Issuer: "https://issuer.example.test", Roles: []string{"operator"}},
		TenantID:  fixture.tenantID, CSRFTokenCiphertext: ciphertext, ExpiresAt: now.Add(time.Hour),
	}, sessionstore.MutationContext{
		Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "identity-policy-1", CorrelationID: "alarm-routing",
		TraceID: strings.Repeat("a", 32), Traceparent: "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
		ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.sessionID = created.ID

	basePath := "/api/v1/sites/" + fixture.siteID + "/alarm-routing"

	// GET returns non-secret summary without leaking contact email or recipient key.
	getRec := fixture.serve(http.MethodGet, basePath, "", "")
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET alarm-routing status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	if strings.Contains(getRec.Body.String(), "duty-a@example.com") || strings.Contains(getRec.Body.String(), "duty-a-wecom") {
		t.Fatalf("secret contact leaked in alarm-routing summary: %s", getRec.Body.String())
	}
	var summaryEnv struct {
		Data notificationservice.SiteRoutingSummaryView `json:"data"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &summaryEnv); err != nil ||
		summaryEnv.Data.HoldMinutes != 5 || len(summaryEnv.Data.Routes) != 1 ||
		summaryEnv.Data.Routes[0].Steps[0].RecipientCount != 1 || summaryEnv.Data.Routes[0].GroupWindowSeconds != 60 {
		t.Fatalf("unexpected alarm-routing summary: err=%v body=%s", err, getRec.Body.String())
	}
	initialDigest := summaryEnv.Data.ConfigDigest

	// Hidden site fails closed as 404.
	hiddenRec := fixture.serve(http.MethodGet, "/api/v1/sites/01910000-0001-7000-8000-0000000000ff/alarm-routing", "", "")
	if hiddenRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on hidden site, got %d body=%s", hiddenRec.Code, hiddenRec.Body.String())
	}

	// Missing CSRF on POST reload returns 403.
	noCSRF := httptest.NewRequest(http.MethodPost, "https://web.example.test"+basePath+"/reload", nil)
	noCSRF.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fixture.sessionID})
	noCSRF.Header.Set("Origin", "https://web.example.test")
	noCSRFRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(noCSRFRec, noCSRF)
	if noCSRFRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when reload omits CSRF, got %d", noCSRFRec.Code)
	}

	// IAM deny on schedule.manage returns 403.
	fixture.deny.Store(true)
	deniedReload := fixture.serve(http.MethodPost, basePath+"/reload", `{}`, "")
	if deniedReload.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on denied reload, got %d body=%s", deniedReload.Code, deniedReload.Body.String())
	}
	fixture.deny.Store(false)

	// Valid change updates routing and increments reload counter.
	writeRoutingFile(12, 120)
	reloadRec := fixture.serve(http.MethodPost, basePath+"/reload", `{}`, "")
	if reloadRec.Code != http.StatusOK {
		t.Fatalf("POST reload status=%d body=%s", reloadRec.Code, reloadRec.Body.String())
	}
	var reloadEnv struct {
		Data notificationservice.AlarmRoutingReloadResponse `json:"data"`
	}
	if err := json.Unmarshal(reloadRec.Body.Bytes(), &reloadEnv); err != nil ||
		!reloadEnv.Data.Changed || reloadEnv.Data.HoldMinutes != 12 ||
		reloadEnv.Data.Routes[0].GroupWindowSeconds != 120 || reloadEnv.Data.ConfigDigest == initialDigest {
		t.Fatalf("unexpected reload response: err=%v body=%s", err, reloadRec.Body.String())
	}
	validDigest := reloadEnv.Data.ConfigDigest

	// Corrupt routing file returns 422 while preserving last known-good snapshot on subsequent GET.
	if err := os.WriteFile(filepath.Join(routingDir, "site.alarm-routing.json"), []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	badReload := fixture.serve(http.MethodPost, basePath+"/reload", `{}`, "")
	if badReload.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on invalid reload file, got %d body=%s", badReload.Code, badReload.Body.String())
	}
	afterBadGet := fixture.serve(http.MethodGet, basePath, "", "")
	if afterBadGet.Code != http.StatusOK {
		t.Fatalf("expected 200 after bad reload, got %d body=%s", afterBadGet.Code, afterBadGet.Body.String())
	}
	var afterEnv struct {
		Data notificationservice.SiteRoutingSummaryView `json:"data"`
	}
	if err := json.Unmarshal(afterBadGet.Body.Bytes(), &afterEnv); err != nil ||
		afterEnv.Data.ConfigDigest != validDigest || afterEnv.Data.HoldMinutes != 12 || afterEnv.Data.LastReloadErrorCode == "" {
		t.Fatalf("expected last valid snapshot + error code after bad reload: err=%v body=%s", err, afterBadGet.Body.String())
	}
}
