package gateway_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/gateway"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
	"github.com/quanlaihe/hvac-web/modules/iam/pkg/iamserver"
)

type capturedUserAdminAudit struct {
	mu       sync.Mutex
	events   []auditserver.DomainEvent
	payloads [][]byte
}

func (c *capturedUserAdminAudit) GetRecord(_ context.Context, _, _ string) (auditserver.Record, error) {
	return auditserver.Record{}, auditserver.ErrRecordNotFound
}

func (c *capturedUserAdminAudit) ConsumeDomain(_ context.Context, payload []byte, _ auditserver.MessageMetadata) (bool, error) {
	var event auditserver.DomainEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
	c.payloads = append(c.payloads, append([]byte(nil), payload...))
	return true, nil
}

func (c *capturedUserAdminAudit) list() ([]auditserver.DomainEvent, [][]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	events := append([]auditserver.DomainEvent(nil), c.events...)
	payloads := make([][]byte, len(c.payloads))
	for i, raw := range c.payloads {
		payloads[i] = append([]byte(nil), raw...)
	}
	return events, payloads
}

type roundTripHandlerFunc func(*http.Request) (*http.Response, error)

func (fn roundTripHandlerFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestUserAdminEmitsDomainAuditAcrossMemberRoleAndDeniedRequests(t *testing.T) {
	iam := &fakeIAMAdmin{}
	directory := &fakeUserDirectory{
		enabled:     map[string]bool{},
		mfaEnabled:  map[string]bool{},
		lockedUntil: map[string]*time.Time{},
		omitUsers:   map[string]bool{},
	}
	captured := &capturedUserAdminAudit{}

	harness := buildAuthHarnessWithGatewayConfig(
		t,
		func(clientSPIFFEID string) http.Handler {
			return iam.wrap(iamserver.NewHandler(iamserver.Config{
				AllowedWorkloadSPIFFE:       clientSPIFFEID,
				Audience:                    "iam-service",
				PrincipalCapabilityResolver: readerCapabilityResolver{},
			}))
		},
		nil,
		nil,
		func(config *gateway.IdentityConfig) {
			config.UserDirectory = directory
			now := time.Now().UTC()
			uri, err := url.Parse(config.ExecutingWorkloadSPIFFE)
			if err != nil {
				t.Fatal(err)
			}
			template := &x509.Certificate{
				SerialNumber: big.NewInt(101),
				Subject:      pkix.Name{CommonName: "platform-gateway"},
				NotBefore:    now.Add(-time.Hour),
				NotAfter:     now.Add(24 * time.Hour),
				KeyUsage:     x509.KeyUsageDigitalSignature,
				URIs:         []*url.URL{uri},
			}
			der, err := x509.CreateCertificate(rand.Reader, template, template, config.DelegationSigner.Public(), config.DelegationSigner)
			if err != nil {
				t.Fatal(err)
			}
			cert, err := x509.ParseCertificate(der)
			if err != nil {
				t.Fatal(err)
			}
			auditHandler := auditserver.NewHandler(auditserver.Config{
				Store:                 captured,
				DomainWriter:          captured,
				AllowedWorkloadSPIFFE: config.ExecutingWorkloadSPIFFE,
				Now:                   func() time.Time { return time.Now().UTC() },
			})
			config.AuditURL = "https://audit.example.test"
			config.AuditAudience = "audit-ledger-service"
			config.AuditHTTPClient = &http.Client{
				Transport: roundTripHandlerFunc(func(req *http.Request) (*http.Response, error) {
					clone := req.Clone(req.Context())
					clone.TLS = &tls.ConnectionState{
						PeerCertificates: []*x509.Certificate{cert},
						VerifiedChains:   [][]*x509.Certificate{{cert}},
					}
					rr := httptest.NewRecorder()
					auditHandler.ServeHTTP(rr, clone)
					return rr.Result(), nil
				}),
			}
		},
	)
	iam.issuer = harness.oidcServer.URL

	admin := harness.browserClient(t)
	adminPrincipal, _ := loginAndReadPrincipal(t, admin, harness.gatewayURL, "admin")

	operator := harness.browserClient(t)
	opPrincipal, _ := loginAndReadPrincipal(t, operator, harness.gatewayURL, "")

	deniedResp := postAsUser(t, operator, harness, opPrincipal.Session.CSRFToken, "/api/v1/iam/members",
		`{"username":"forged.user","displayName":"越权","email":"x@example.test","password":"chiller-night-shift","assignments":[{"roleKey":"operator"}]}`)
	if deniedResp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin create status=%d", deniedResp.StatusCode)
	}

	createResp := postAsUser(t, admin, harness, adminPrincipal.Session.CSRFToken, "/api/v1/iam/members",
		`{"username":"zhang.wei","displayName":"张伟","email":"zhang.wei@example.test","password":"chiller-night-shift","assignments":[{"roleKey":"operator","siteId":"`+userAdminSiteID+`"}]}`)
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create member status=%d", createResp.StatusCode)
	}

	statusResp := postAsUser(t, admin, harness, adminPrincipal.Session.CSRFToken, "/api/v1/iam/members/"+userAdminMemberID+"/status", `{"enabled":false}`)
	if statusResp.StatusCode != http.StatusNoContent {
		t.Fatalf("disable member status=%d", statusResp.StatusCode)
	}

	profileResp := postAsUser(t, admin, harness, adminPrincipal.Session.CSRFToken, "/api/v1/iam/members/"+userAdminMemberID+"/profile", `{"displayName":"张伟（值班）","email":"zw@example.test"}`)
	if profileResp.StatusCode != http.StatusNoContent {
		t.Fatalf("update profile status=%d", profileResp.StatusCode)
	}

	assignResp := postAsUser(t, admin, harness, adminPrincipal.Session.CSRFToken, "/api/v1/iam/members/"+userAdminMemberID+"/assignments", `{"roleKey":"operator","siteId":"`+userAdminSiteID+`"}`)
	if assignResp.StatusCode != http.StatusNoContent {
		t.Fatalf("assign role status=%d", assignResp.StatusCode)
	}

	resetResp := postAsUser(t, admin, harness, adminPrincipal.Session.CSRFToken, "/api/v1/iam/members/"+userAdminMemberID+"/password", `{"password":"new-reset-password-1"}`)
	if resetResp.StatusCode != http.StatusNoContent {
		t.Fatalf("reset member password status=%d", resetResp.StatusCode)
	}

	missingResp := postAsUser(t, admin, harness, adminPrincipal.Session.CSRFToken, "/api/v1/iam/members/018f3d00-0000-7000-8000-000000009999/status", `{"enabled":false}`)
	if missingResp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing member status=%d", missingResp.StatusCode)
	}

	events, payloads := captured.list()
	if len(events) != 7 {
		t.Fatalf("expected 7 IAM domain audit events, got %d: %#v", len(events), events)
	}
	if events[0].Action != "IAM_ADMIN_ACCESS" || events[0].Outcome != "DENIED" {
		t.Fatalf("unexpected non-admin denial event: %#v", events[0])
	}
	if events[1].Action != "IAM_MEMBER_CREATED" || events[1].Outcome != "SUCCEEDED" || events[1].TargetLabel != "张伟" {
		t.Fatalf("unexpected member create event: %#v", events[1])
	}
	if events[2].Action != "IAM_MEMBER_STATUS_UPDATED" || !bytes.Contains(events[2].ChangeSummary, []byte(`"afterEnabled":false`)) {
		t.Fatalf("unexpected member status event: %#v", events[2])
	}
	if events[3].Action != "IAM_MEMBER_PROFILE_UPDATED" || !bytes.Contains(events[3].ChangeSummary, []byte(`"beforeDisplayName":"Fixture User"`)) {
		t.Fatalf("unexpected member profile event: %#v", events[3])
	}
	if events[4].Action != "IAM_ROLE_ASSIGNED" || !bytes.Contains(events[4].ChangeSummary, []byte(`"roleKey":"operator"`)) {
		t.Fatalf("unexpected role assign event: %#v", events[4])
	}
	if events[5].Action != "IAM_MEMBER_PASSWORD_RESET" || !bytes.Contains(events[5].ChangeSummary, []byte(`"passwordChanged":true`)) {
		t.Fatalf("unexpected member password reset event: %#v (summary=%s)", events[5], events[5].ChangeSummary)
	}
	if events[6].Action != "IAM_MEMBER_STATUS_UPDATED" || events[6].Outcome != "REJECTED" || !bytes.Contains(events[6].ChangeSummary, []byte(`"reasonCode":"RESOURCE_NOT_FOUND"`)) {
		t.Fatalf("unexpected missing member rejected event: %#v", events[6])
	}
	for _, raw := range payloads {
		if bytes.Contains(raw, []byte("chiller-night-shift")) || bytes.Contains(raw, []byte("new-reset-password-1")) {
			t.Fatalf("password leaked into IAM audit payload: %s", raw)
		}
	}
}
