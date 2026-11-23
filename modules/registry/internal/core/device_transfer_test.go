package core

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

type memoryDeviceTransferStore struct {
	*memoryScheduleBackend
	calls      int
	lastSource registryauth.GrantClaims
	lastTarget *registryauth.GrantClaims
	lastInput  TransferDeviceRequest
	res        TransferDeviceResult
	err        error
}

func (m *memoryDeviceTransferStore) TransferDevice(
	_ context.Context,
	sourceClaims registryauth.GrantClaims,
	targetClaims *registryauth.GrantClaims,
	input TransferDeviceRequest,
) (TransferDeviceResult, error) {
	if err := input.Validate(); err != nil {
		return TransferDeviceResult{}, err
	}
	m.calls++
	m.lastSource = sourceClaims
	if targetClaims != nil {
		copied := *targetClaims
		m.lastTarget = &copied
	} else {
		m.lastTarget = nil
	}
	m.lastInput = input
	if m.err != nil {
		return TransferDeviceResult{}, m.err
	}
	return m.res, nil
}

func TestTransferDeviceRequestValidation(t *testing.T) {
	targetTenant := "018f1d00-0000-7000-8000-000000000002"
	newCode := "eg8200-moved-01"
	newName := "二号站主网关"
	wipeFalse := false
	valid := TransferDeviceRequest{
		SourceSiteID:   testSiteA1,
		DeviceID:       testDeviceA1,
		TargetTenantID: &targetTenant,
		TargetSiteID:   testSiteA2,
		NewCode:        &newCode,
		NewDisplayName: &newName,
		WipeEdgeCache:  &wipeFalse,
		Meta: MutationMeta{
			ExpectedRevision: 1,
			IdempotencyKey:   "dev-transfer-valid-01",
			Reason:           "设备搬迁至新站点并触发出厂清理",
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid TransferDeviceRequest, got %v", err)
	}
	if valid.EffectiveWipeEdgeCache() {
		t.Fatal("expected EffectiveWipeEdgeCache=false when explicitly disabled")
	}
	defaultWipe := valid
	defaultWipe.WipeEdgeCache = nil
	if !defaultWipe.EffectiveWipeEdgeCache() {
		t.Fatal("expected EffectiveWipeEdgeCache=true by default")
	}

	cases := []struct {
		name   string
		mutate func(*TransferDeviceRequest)
	}{
		{"invalid targetSiteId", func(r *TransferDeviceRequest) { r.TargetSiteID = "bad-site" }},
		{"invalid targetTenantId", func(r *TransferDeviceRequest) { bad := "bad-tenant"; r.TargetTenantID = &bad }},
		{"invalid newCode", func(r *TransferDeviceRequest) { bad := "BAD CODE!"; r.NewCode = &bad }},
		{"empty newDisplayName", func(r *TransferDeviceRequest) { bad := "   "; r.NewDisplayName = &bad }},
		{"overlong newDisplayName", func(r *TransferDeviceRequest) { bad := strings.Repeat("网", 129); r.NewDisplayName = &bad }},
		{"zero expectedRevision", func(r *TransferDeviceRequest) { r.Meta.ExpectedRevision = 0 }},
		{"empty reason", func(r *TransferDeviceRequest) { r.Meta.Reason = "   " }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := valid
			tc.mutate(&req)
			if err := req.Validate(); !errors.Is(err, ErrInvalidMutation) {
				t.Fatalf("expected ErrInvalidMutation, got %v", err)
			}
		})
	}
}

func TestServerTransferDeviceHTTPDualGrantAndErrorMapping(t *testing.T) {
	targetTenant := "018f1d00-0000-7000-8000-000000000002"
	targetSite := "018f1e00-1000-7000-8000-000000000003"
	targetDevice := "019a2222-0000-7000-8000-000000000099"
	backend := &memoryDeviceTransferStore{
		memoryScheduleBackend: newMemoryScheduleBackend(),
		res: TransferDeviceResult{
			TransferID:           "019a2222-0000-7000-8000-000000000001",
			TransferScope:        TransferScopeCrossTenant,
			SourceTenantID:       testTenantA,
			SourceSiteID:         testSiteA1,
			SourceDeviceID:       testDeviceA1,
			SourceDeviceRevision: 2,
			TargetTenantID:       targetTenant,
			TargetSiteID:         targetSite,
			TargetDeviceID:       targetDevice,
			WipeRequired:         true,
			Reason:               "跨租户设备交接",
			TransferredBy:        "admin-01",
			TransferredAt:        "2026-10-12T08:00:00.000Z",
			SourceDevice:         Device{ID: testDeviceA1, TenantID: testTenantA, SiteID: testSiteA1, Status: "RETIRED", Revision: 2},
			TargetDevice:         Device{ID: targetDevice, TenantID: targetTenant, SiteID: targetSite, Status: "ACTIVE", Revision: 1},
		},
	}
	client := newScheduleHTTPClientWithInterfaces(t, backend, backend)
	sourceClaims := testGrantClaims(registryauth.ActionDeviceWrite)
	sourceClaims.TenantID = testTenantA
	sourceClaims.AllowedSiteIDs = []string{testSiteA1}

	targetClaims := testGrantClaims(registryauth.ActionDeviceWrite)
	targetClaims.TenantID = targetTenant
	targetClaims.AllowedSiteIDs = []string{targetSite}
	targetClaims.IssuedAt = client.now.Unix()
	targetClaims.ExpiresAt = client.now.Add(20 * time.Second).Unix()
	targetClaims.Presenter = testPresenter
	signedTargetGrant, err := registryauth.SignGrant(client.signer, targetClaims)
	if err != nil {
		t.Fatal(err)
	}

	sourceClaims.IssuedAt = client.now.Unix()
	sourceClaims.ExpiresAt = client.now.Add(20 * time.Second).Unix()
	sourceClaims.Presenter = testPresenter
	signedSourceGrant, err := registryauth.SignGrant(client.signer, sourceClaims)
	if err != nil {
		t.Fatal(err)
	}

	route := RegistryPathPrefix + "sites/" + testSiteA1 + "/devices/" + testDeviceA1 + "/transfer"
	bodyBytes, _ := json.Marshal(map[string]any{
		"targetTenantId": targetTenant,
		"targetSiteId":   targetSite,
		"wipeEdgeCache":  true,
		"meta": map[string]any{
			"expectedRevision": 1,
			"idempotencyKey":   "http-transfer-01",
			"reason":           "跨租户设备交接",
		},
	})
	sendWithTargetGrant := func(rawTargetGrant string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, route, bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Delegation-Grant", signedSourceGrant)
		if rawTargetGrant != "" {
			req.Header.Set("X-Target-Delegation-Grant", rawTargetGrant)
		}
		peerURI, _ := url.Parse(testPresenter)
		peer := &x509.Certificate{URIs: []*url.URL{peerURI}}
		req.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{peer},
			VerifiedChains:   [][]*x509.Certificate{{peer}},
		}
		rec := httptest.NewRecorder()
		client.handler.ServeHTTP(rec, req)
		return rec
	}

	// 1. Dual valid grants -> 201 Created, target claims forwarded to Writer.
	rec := sendWithTargetGrant(signedTargetGrant)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d want=201 body=%s", rec.Code, rec.Body.String())
	}
	if backend.lastTarget == nil || backend.lastTarget.TenantID != targetTenant {
		t.Fatalf("expected target claims TenantID=%s forwarded to Writer, got %+v", targetTenant, backend.lastTarget)
	}

	// 2. Invalid target grant token -> 401 before Writer.
	badTargetRec := sendWithTargetGrant("tampered.token")
	if badTargetRec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered target grant status=%d want=401 body=%s", badTargetRec.Code, badTargetRec.Body.String())
	}

	// 3. Target grant lacking ActionDeviceWrite -> 403 CORE_REGISTRY_GRANT_REJECTED.
	readOnlyTarget := targetClaims
	readOnlyTarget.Actions = []registryauth.Action{registryauth.ActionDeviceRead}
	signedReadOnlyTarget, _ := registryauth.SignGrant(client.signer, readOnlyTarget)
	forbiddenTargetRec := sendWithTargetGrant(signedReadOnlyTarget)
	if forbiddenTargetRec.Code != http.StatusForbidden {
		t.Fatalf("read-only target grant status=%d want=403 body=%s", forbiddenTargetRec.Code, forbiddenTargetRec.Body.String())
	}

	// 4. ErrTransferBlocked -> 409 REGISTRY_TRANSFER_BLOCKED.
	backend.err = ErrTransferBlocked
	blockedRec := sendWithTargetGrant(signedTargetGrant)
	if blockedRec.Code != http.StatusConflict || !strings.Contains(blockedRec.Body.String(), "REGISTRY_TRANSFER_BLOCKED") {
		t.Fatalf("blocked status=%d body=%s", blockedRec.Code, blockedRec.Body.String())
	}

	// 5. ErrTransferForbidden -> 403 REGISTRY_TRANSFER_FORBIDDEN.
	backend.err = ErrTransferForbidden
	forbiddenRec := sendWithTargetGrant(signedTargetGrant)
	if forbiddenRec.Code != http.StatusForbidden || !strings.Contains(forbiddenRec.Body.String(), "REGISTRY_TRANSFER_FORBIDDEN") {
		t.Fatalf("forbidden status=%d body=%s", forbiddenRec.Code, forbiddenRec.Body.String())
	}
}
