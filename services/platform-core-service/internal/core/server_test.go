package core

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	testGrantIssuer         = "spiffe://hvac.local/iam-service"
	testPresenter           = "spiffe://hvac.local/platform-gateway"
	testOperationsPresenter = "spiffe://hvac.local/operations-agent-service"
	testAudience            = "platform-core-service"
	testPolicy              = "registry-read:1"
	testPrincipal           = "018f1e00-2000-7000-8000-000000000002"
	testTenantA             = "018f1d00-0000-7000-8000-000000000001"
	testOrganizationA       = "018f1e00-0000-7000-8000-000000000001"
	testOrganizationB       = "018f1e00-0000-7000-8000-000000000002"
	testActingOrg           = "018f1e00-0000-7000-8000-000000000003"
	testSiteA1              = "018f1e00-1000-7000-8000-000000000001"
	testSiteA2              = "018f1e00-1000-7000-8000-000000000002"
	testEquipmentA1         = "018f1e00-3000-7000-8000-000000000001"
	testDeviceA1            = "018f1e00-4000-7000-8000-000000000001"
	testBindingA1           = "018f1e00-5000-7000-8000-000000000001"
)

type countingGrantStatusProvider struct {
	calls  int
	status GrantStatus
}

func (provider *countingGrantStatusProvider) Lookup(_ context.Context, _ registryauth.GrantClaims) (GrantStatus, error) {
	provider.calls++
	return provider.status, nil
}

type fakeRegistryStore struct {
	sites         PageResult[Site]
	site          Site
	equipment     PageResult[Equipment]
	equipmentItem Equipment
	devices       PageResult[Device]
	device        Device
	bindings      PageResult[DeviceBinding]
	assetModel    SiteAssetModel
	err           error
	lastClaims    registryauth.GrantClaims
	lastPage      PageRequest
	lastID        string
}

func (store *fakeRegistryStore) ListSites(_ context.Context, claims registryauth.GrantClaims, page PageRequest) (PageResult[Site], error) {
	store.lastClaims, store.lastPage = claims, page
	return store.sites, store.err
}
func (store *fakeRegistryStore) GetSite(_ context.Context, claims registryauth.GrantClaims, id string) (Site, error) {
	store.lastClaims, store.lastID = claims, id
	return store.site, store.err
}
func (store *fakeRegistryStore) ListEquipment(_ context.Context, claims registryauth.GrantClaims, id string, page PageRequest) (PageResult[Equipment], error) {
	store.lastClaims, store.lastID, store.lastPage = claims, id, page
	return store.equipment, store.err
}
func (store *fakeRegistryStore) GetEquipment(_ context.Context, claims registryauth.GrantClaims, id string) (Equipment, error) {
	store.lastClaims, store.lastID = claims, id
	return store.equipmentItem, store.err
}
func (store *fakeRegistryStore) ListDevices(_ context.Context, claims registryauth.GrantClaims, id string, page PageRequest) (PageResult[Device], error) {
	store.lastClaims, store.lastID, store.lastPage = claims, id, page
	return store.devices, store.err
}
func (store *fakeRegistryStore) GetDevice(_ context.Context, claims registryauth.GrantClaims, id string) (Device, error) {
	store.lastClaims, store.lastID = claims, id
	return store.device, store.err
}
func (store *fakeRegistryStore) ListDeviceBindings(_ context.Context, claims registryauth.GrantClaims, id string, page PageRequest) (PageResult[DeviceBinding], error) {
	store.lastClaims, store.lastID, store.lastPage = claims, id, page
	return store.bindings, store.err
}
func (store *fakeRegistryStore) GetSiteAssetModel(_ context.Context, claims registryauth.GrantClaims, id string) (SiteAssetModel, error) {
	store.lastClaims, store.lastID = claims, id
	return store.assetModel, store.err
}

func TestServerListsSitesAndReturnsBoundCursor(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	store := &fakeRegistryStore{sites: PageResult[Site]{
		Items:   []Site{{ID: testSiteA1, TenantID: testTenantA, DisplayName: "Site A1"}},
		HasMore: true,
	}}
	harness := newCoreHarness(t, now, store, StaticGrantStatusProvider{PolicyRevision: testPolicy})
	claims := testGrantClaims(registryauth.ActionSiteList)
	response := harness.serve(t, http.MethodGet, RegistryPathPrefix+"sites?limit=1", claims, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	var collection Collection[Site]
	if err := json.NewDecoder(response.Body).Decode(&collection); err != nil {
		t.Fatal(err)
	}
	if len(collection.Items) != 1 || collection.NextCursor == nil || !collection.HasMore {
		t.Fatalf("collection = %#v", collection)
	}
	if store.lastPage.Limit != 1 || store.lastClaims.TokenID != claims.TokenID {
		t.Fatalf("store call page=%#v claims=%#v", store.lastPage, store.lastClaims)
	}
	page, err := harness.codec.Decode(*collection.NextCursor, "sites", "", registryauth.ActionSiteList, claims)
	if err != nil || page.ID != testSiteA1 {
		t.Fatalf("decode cursor: page=%#v err=%v", page, err)
	}
}

func TestServerAcceptsIAMGrantBoundToOperationsAgentPresenter(t *testing.T) {
	now := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	store := &fakeRegistryStore{site: Site{ID: testSiteA1, TenantID: testTenantA}}
	harness := newCoreHarness(t, now, store, StaticGrantStatusProvider{PolicyRevision: testPolicy})
	claims := testGrantClaims(registryauth.ActionSiteRead)
	response, _ := harness.serveAndGrantAsPresenter(
		t,
		http.MethodGet,
		RegistryPathPrefix+"sites/"+testSiteA1,
		claims,
		nil,
		testOperationsPresenter,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	if store.lastClaims.Presenter != testOperationsPresenter {
		t.Fatalf("presenter = %q", store.lastClaims.Presenter)
	}
}

func TestServerRoutesAllRegistryReadsThroughConcreteGrantActions(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	store := &fakeRegistryStore{
		sites:         PageResult[Site]{Items: []Site{}},
		site:          Site{ID: testSiteA1},
		equipment:     PageResult[Equipment]{Items: []Equipment{}},
		equipmentItem: Equipment{ID: testEquipmentA1},
		devices:       PageResult[Device]{Items: []Device{}},
		device:        Device{ID: testDeviceA1},
		bindings:      PageResult[DeviceBinding]{Items: []DeviceBinding{{ID: testBindingA1}}},
	}
	harness := newCoreHarness(t, now, store, StaticGrantStatusProvider{PolicyRevision: testPolicy})
	tests := []struct {
		path   string
		action registryauth.Action
	}{
		{RegistryPathPrefix + "sites", registryauth.ActionSiteList},
		{RegistryPathPrefix + "sites/" + testSiteA1, registryauth.ActionSiteRead},
		{RegistryPathPrefix + "sites/" + testSiteA1 + "/equipment", registryauth.ActionEquipmentList},
		{RegistryPathPrefix + "equipment/" + testEquipmentA1, registryauth.ActionEquipmentRead},
		{RegistryPathPrefix + "sites/" + testSiteA1 + "/devices", registryauth.ActionDeviceList},
		{RegistryPathPrefix + "sites/" + testSiteA1 + "/device-bindings", registryauth.ActionDeviceBindingList},
		{RegistryPathPrefix + "devices/" + testDeviceA1, registryauth.ActionDeviceRead},
	}
	for _, test := range tests {
		t.Run(string(test.action), func(t *testing.T) {
			response := harness.serve(t, http.MethodGet, test.path, testGrantClaims(test.action), nil)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestServerFailsClosedForWrongActionRevocationStalePolicyAndForgedHeaders(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	store := &fakeRegistryStore{sites: PageResult[Site]{Items: []Site{}}}
	tests := []struct {
		name       string
		claims     registryauth.GrantClaims
		status     GrantStatusProvider
		headers    http.Header
		wantStatus int
	}{
		{"wrong action", testGrantClaims(registryauth.ActionDeviceRead), StaticGrantStatusProvider{PolicyRevision: testPolicy}, nil, http.StatusForbidden},
		{"revoked", testGrantClaims(registryauth.ActionSiteList), StaticGrantStatusProvider{PolicyRevision: testPolicy, RevokedTokenIDs: map[string]struct{}{"grant-1": {}}}, nil, http.StatusForbidden},
		{"stale policy", testGrantClaims(registryauth.ActionSiteList), StaticGrantStatusProvider{PolicyRevision: "registry-read:2"}, nil, http.StatusForbidden},
		{"status unavailable", testGrantClaims(registryauth.ActionSiteList), StaticGrantStatusProvider{Err: errors.New("offline")}, nil, http.StatusServiceUnavailable},
		{"forged scope", testGrantClaims(registryauth.ActionSiteList), StaticGrantStatusProvider{PolicyRevision: testPolicy}, http.Header{"X-Site-ID": []string{testSiteA1}}, http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newCoreHarness(t, now, store, test.status)
			response := harness.serve(t, http.MethodGet, RegistryPathPrefix+"sites", test.claims, test.headers)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestServerRejectsInvalidGrantBeforeOnlineStatusLookup(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	status := &countingGrantStatusProvider{status: GrantStatus{CurrentPolicyRevision: testPolicy}}
	harness := newCoreHarness(t, now, &fakeRegistryStore{}, status)
	response := harness.serve(t, http.MethodGet, RegistryPathPrefix+"sites", testGrantClaims(registryauth.Action("organization.read")), nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	if status.calls != 0 {
		t.Fatalf("online status calls = %d", status.calls)
	}
}

func TestServerLogsExcludeGrantAndRequestCredentialMaterial(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	store := &fakeRegistryStore{sites: PageResult[Site]{Items: []Site{}}}
	harness := newCoreHarnessWithLogger(t, now, store, StaticGrantStatusProvider{PolicyRevision: testPolicy}, logger)
	claims := testGrantClaims(registryauth.ActionSiteList)
	response, grant := harness.serveAndGrant(t, http.MethodGet, RegistryPathPrefix+"sites", claims, http.Header{
		"Cookie":        []string{"session=opaque-cookie-value"},
		"Authorization": []string{"Bearer opaque-auth-value"},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	output := logs.String()
	for _, forbidden := range []string{grant, "opaque-cookie-value", "opaque-auth-value", claims.Subject, claims.SessionID, claims.TokenID} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("Core log contains request credential material %q: %s", forbidden, output)
		}
	}
}

func TestServerReturnsSameNotFoundForUnauthorizedAndMissingDetail(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	store := &fakeRegistryStore{err: ErrNotFound}
	harness := newCoreHarness(t, now, store, StaticGrantStatusProvider{PolicyRevision: testPolicy})
	response := harness.serve(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA2, testGrantClaims(registryauth.ActionSiteRead), nil)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"RESOURCE_NOT_FOUND"`) {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
}

type coreHarness struct {
	handler http.Handler
	signer  crypto.Signer
	peer    *x509.Certificate
	codec   *CursorCodec
	now     time.Time
}

func newCoreHarness(t *testing.T, now time.Time, store RegistryStore, status GrantStatusProvider) coreHarness {
	return newCoreHarnessWithLogger(t, now, store, status, nil)
}

func newCoreHarnessWithLogger(t *testing.T, now time.Time, store RegistryStore, status GrantStatusProvider, logger *slog.Logger) coreHarness {
	t.Helper()
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse(testPresenter)
	if err != nil {
		t.Fatal(err)
	}
	peer := &x509.Certificate{URIs: []*url.URL{uri}, PublicKey: &signer.PublicKey}
	codec, err := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return coreHarness{
		handler: NewHandler(ServerConfig{
			Store:                             store,
			CursorCodec:                       codec,
			GrantPublicKey:                    &signer.PublicKey,
			GrantIssuer:                       testGrantIssuer,
			AllowedPresenterSPIFFE:            testPresenter,
			AdditionalAllowedPresenterSPIFFEs: []string{testOperationsPresenter},
			Audience:                          testAudience,
			GrantStatus:                       status,
			Logger:                            logger,
			Now:                               func() time.Time { return now },
		}),
		signer: signer,
		peer:   peer,
		codec:  codec,
		now:    now,
	}
}

func (harness coreHarness) serve(t *testing.T, method, path string, claims registryauth.GrantClaims, headers http.Header) *httptest.ResponseRecorder {
	recorder, _ := harness.serveAndGrant(t, method, path, claims, headers)
	return recorder
}

func (harness coreHarness) serveAndGrant(t *testing.T, method, path string, claims registryauth.GrantClaims, headers http.Header) (*httptest.ResponseRecorder, string) {
	t.Helper()
	return harness.serveAndGrantAsPresenter(t, method, path, claims, headers, testPresenter)
}

func (harness coreHarness) serveAndGrantAsPresenter(
	t *testing.T,
	method,
	path string,
	claims registryauth.GrantClaims,
	headers http.Header,
	presenterSPIFFE string,
) (*httptest.ResponseRecorder, string) {
	t.Helper()
	claims.IssuedAt = harness.now.Unix()
	claims.ExpiresAt = harness.now.Add(20 * time.Second).Unix()
	claims.Presenter = presenterSPIFFE
	grant, err := registryauth.SignGrant(harness.signer, claims)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("X-Delegation-Grant", grant)
	for name, values := range headers {
		request.Header[name] = append([]string(nil), values...)
	}
	peerURI, err := url.Parse(presenterSPIFFE)
	if err != nil {
		t.Fatal(err)
	}
	peer := &x509.Certificate{URIs: []*url.URL{peerURI}}
	request.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{peer},
		VerifiedChains:   [][]*x509.Certificate{{peer}},
	}
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	return recorder, grant
}

func testGrantClaims(action registryauth.Action) registryauth.GrantClaims {
	return registryauth.GrantClaims{
		Issuer:                 testGrantIssuer,
		Presenter:              testPresenter,
		Audience:               testAudience,
		PrincipalID:            testPrincipal,
		SubjectIssuer:          "https://identity.example.test/oidc",
		Subject:                "delegated",
		TenantID:             testTenantA,
		ActingOrganizationID: testActingOrg,
		AllowedSiteIDs:       []string{testSiteA1},
		DeniedSiteIDs:        []string{},
		Actions:                []registryauth.Action{action},
		PolicyRevision:         testPolicy,
		DecisionReason:         registryauth.ReasonAllowSiteBinding,
		SessionID:              "session-1",
		ParentTokenID:          "parent-1",
		TokenID:                "grant-1",
		Transitive:             false,
	}
}
