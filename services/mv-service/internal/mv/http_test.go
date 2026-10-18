package mv

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const testGatewaySPIFFE = "spiffe://hvac.local/platform-gateway"

// testGatewaySigner signs contexts the way the Gateway does.
var testGatewaySigner = func() *ecdsa.PrivateKey {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return key
}()

func newTestHandler(t *testing.T, service *Service) http.Handler {
	t.Helper()
	handler, err := NewHTTPHandler(service, AuthConfig{GatewayPublicKey: &testGatewaySigner.PublicKey, GatewaySPIFFE: testGatewaySPIFFE})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

// presented is a request from the peer workload carrying a context signed for presenter, action,
// Tenant, Site and person.
func presented(t *testing.T, method, path, body, peer, presenter, action, tenantID, siteID, subject string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	uri, _ := url.Parse(peer)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{uri}}}}
	request.Header.Set(ContextHeader, signContext(t, presenter, Audience, action, tenantID, siteID, subject))
	return request
}

func signContext(t *testing.T, presenter, audience, action, tenantID, siteID, subject string) string {
	t.Helper()
	now := time.Now()
	token, err := identitycontext.SignDelegation(testGatewaySigner, identitycontext.DelegationClaims{
		Issuer: testGatewaySPIFFE, Subject: subject, SubjectIssuer: "https://identity.example.test",
		ExecutingService: presenter, Audience: audience, TenantID: tenantID,
		Actions: []string{action}, Scopes: []string{"tenant:" + tenantID, "site:" + siteID},
		PolicyRevision: "policy-1", SessionID: "session-1", IssuedAt: now.Add(-time.Second).Unix(),
		ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "token-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// call is the Gateway asking for testActor on testTenant's testSite: a read for GET, a write otherwise.
func call(t *testing.T, handler http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	action := WriteAction
	if method == http.MethodGet {
		action = ReadAction
	}
	return serve(handler, presented(t, method, path, body, testGatewaySPIFFE, testGatewaySPIFFE, action, testTenant, testSite, testActor))
}

func serve(handler http.Handler, request *http.Request) (int, map[string]any) {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var decoded map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder.Code, decoded
}

// #612: M&V trusted any caller's X-Tenant-ID and X-Actor-ID. It now serves only the Gateway, with a
// context the Gateway signed for the Site, and takes the Tenant and the person from that context.
func TestSavingsServesOnlyTheGatewayWithAContextForTheSite(t *testing.T) {
	store := newMemoryStore()
	handler := newTestHandler(t, newTestService(t, store, plantHistory{}))
	base := "/v1/sites/" + testSite + "/savings/measures"
	register := `{"name":"提高冷冻水供水温度","kind":"SETPOINT_RESET","plannedAt":"2026-10-15T09:00:00+08:00"}`
	other := "spiffe://hvac.local/telemetry-worker"
	otherSite := "01990000-5000-7000-8000-0000000000ff"
	elsewhere := presented(t, http.MethodPost, base, register, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite, testActor)
	elsewhere.Header.Set(ContextHeader, signContext(t, testGatewaySPIFFE, "fdd-service", WriteAction, testTenant, testSite, testActor))
	uncontexted := presented(t, http.MethodPost, base, register, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite, testActor)
	uncontexted.Header.Del(ContextHeader)
	for name, request := range map[string]*http.Request{
		"a context for another owner":   elsewhere,
		"the Gateway without a context": uncontexted,
		"another workload":              presented(t, http.MethodPost, base, register, other, other, WriteAction, testTenant, testSite, testActor),
		"a read context":                presented(t, http.MethodPost, base, register, testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testTenant, testSite, testActor),
		"a context for another Site":    presented(t, http.MethodPost, base, register, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, otherSite, testActor),
	} {
		if status, _ := serve(handler, request); status != http.StatusForbidden {
			t.Fatalf("%s registered a measure: %d", name, status)
		}
	}
	bare := httptest.NewRequest(http.MethodPost, base, strings.NewReader(register))
	bare.Header.Set("X-Tenant-ID", testTenant)
	bare.Header.Set("X-Actor-ID", testActor)
	if status, _ := serve(handler, bare); status != http.StatusForbidden {
		t.Fatalf("a request with only X-Tenant-ID and X-Actor-ID registered a measure: %d", status)
	}
	if measures, _ := store.Measures(t.Context(), testTenant, testSite); len(measures) != 0 {
		t.Fatalf("a refused request registered: %v", measures)
	}

	// The person and Tenant come from the context; the headers a caller writes decide nothing.
	someone := "01990000-2000-7000-8000-0000000000aa"
	request := presented(t, http.MethodPost, base, register, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite, someone)
	request.Header.Set("X-Actor-ID", testActor)
	status, created := serve(handler, request)
	if status != http.StatusCreated {
		t.Fatalf("the Gateway's context could not register: %d %v", status, created)
	}
	stored, err := store.Measure(t.Context(), testTenant, testSite, created["id"].(string))
	if err != nil || stored.CreatedBy != someone {
		t.Fatalf("the measure was not registered by the context's person: %+v %v", stored, err)
	}
	otherTenant := "01990000-3000-7000-8000-000000000002"
	if status, _ := serve(handler, presented(t, http.MethodGet, base+"/"+stored.ID, "", testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, otherTenant, testSite, testActor)); status != http.StatusNotFound {
		t.Fatalf("another Tenant's context read this Tenant's measure: %d", status)
	}
}

func TestTheMeasureLifecycleOverHTTP(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	store := newMemoryStore()
	service := newTestService(t, store, plantHistory{changedAt: implemented, factor: 0.9})
	handler := newTestHandler(t, service)
	base := "/v1/sites/" + testSite + "/savings/measures"

	status, created := call(t, handler, http.MethodPost, base, `{"name":"提高冷冻水供水温度","kind":"SETPOINT_RESET","plannedAt":"2026-10-15T09:00:00+08:00","investmentCny":0}`)
	if status != http.StatusCreated || created["status"] != "PLANNED" {
		t.Fatalf("register = %d %v", status, created)
	}
	id := created["id"].(string)

	status, measure := call(t, handler, http.MethodPost, base+"/"+id+"/implement", `{"implementedAt":"2026-10-15T09:00:00+08:00"}`)
	baseline, _ := measure["baseline"].(map[string]any)
	model, _ := baseline["model"].(map[string]any)
	if status != http.StatusOK || measure["status"] != "IMPLEMENTED" || baseline["trusted"] != true || baseline["samples"] != float64(336) || model["slope"].(float64) <= 0 {
		t.Fatalf("implement = %d %v", status, measure)
	}

	stored, _ := store.Measure(t.Context(), testTenant, testSite, id)
	if err := service.VerifyDay(t.Context(), stored, time.Date(2026, 10, 17, 0, 0, 0, 0, shanghai)); err != nil {
		t.Fatal(err)
	}
	status, detail := call(t, handler, http.MethodGet, base+"/"+id, "")
	days, _ := detail["days"].([]any)
	totals, _ := detail["totals"].(map[string]any)
	if status != http.StatusOK || len(days) != 1 || totals["avoidedKWh"].(float64) <= 0 {
		t.Fatalf("detail = %d %v", status, detail)
	}

	status, summary := call(t, handler, http.MethodGet, "/v1/sites/"+testSite+"/savings/summary?from=2026-10-01&to=2026-10-31", "")
	if status != http.StatusOK || summary["totals"].(map[string]any)["days"] != float64(1) {
		t.Fatalf("summary = %d %v", status, summary)
	}

	if status, body := call(t, handler, http.MethodPost, base+"/"+id+"/refit", `{"from":"2026-10-01T00:00:00+08:00","to":"2026-10-08T00:00:00+08:00"}`); status != http.StatusConflict || body["code"] != "savings_baseline_locked" {
		t.Fatalf("refit after results = %d %v", status, body)
	}
}

func TestBadRequestsAreRefusedWithAReason(t *testing.T) {
	handler := newTestHandler(t, newTestService(t, newMemoryStore(), plantHistory{}))
	base := "/v1/sites/" + testSite + "/savings/measures"
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPost, base, `{"name":"","kind":"SETPOINT_RESET","plannedAt":"2026-10-15T09:00:00+08:00"}`, http.StatusBadRequest},
		{http.MethodPost, base, `{"name":"x","kind":"MAGIC","plannedAt":"2026-10-15T09:00:00+08:00"}`, http.StatusBadRequest},
		{http.MethodPost, base, `{"name":"x","kind":"OTHER","plannedAt":"2026-10-15T09:00:00+08:00","owner":"me"}`, http.StatusBadRequest},
		{http.MethodGet, base + "/01990000-7000-7000-8000-00000000000f", "", http.StatusNotFound},
		{http.MethodGet, "/v1/sites/" + testSite + "/savings/summary?from=2026-10-31&to=2026-10-01", "", http.StatusBadRequest},
	} {
		if status, body := call(t, handler, test.method, test.path, test.body); status != test.status {
			t.Fatalf("%s %s = %d %v, want %d", test.method, test.path, status, body, test.status)
		}
	}
}

func TestABaselineWithoutEnoughHoursCarriesNoModel(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	// Only the last two days before implementation have data.
	history := plantHistory{changedAt: implemented, factor: 0.9, gapFrom: implemented.Add(-14 * 24 * time.Hour), gapTo: implemented.Add(-48 * time.Hour)}
	handler := newTestHandler(t, newTestService(t, newMemoryStore(), history))
	base := "/v1/sites/" + testSite + "/savings/measures"
	_, created := call(t, handler, http.MethodPost, base, `{"name":"提高冷冻水供水温度","kind":"SETPOINT_RESET","plannedAt":"2026-10-15T09:00:00+08:00"}`)
	_, measure := call(t, handler, http.MethodPost, base+"/"+created["id"].(string)+"/implement", `{"implementedAt":"2026-10-15T09:00:00+08:00"}`)
	baseline, _ := measure["baseline"].(map[string]any)
	if baseline["trusted"] != false || baseline["samples"] != float64(48) || baseline["model"] != nil || baseline["reason"] != "TOO_FEW_HOURS" {
		t.Fatalf("baseline = %v", baseline)
	}
}

func TestAMeasureCitesTheExecutedCommandItCameFrom(t *testing.T) {
	handler := newTestHandler(t, newTestService(t, newMemoryStore(), plantHistory{}))
	base := "/v1/sites/" + testSite + "/savings/measures"
	command := "ec61c2dc-2d2c-4933-8fb7-bdc284a76a28"
	status, created := call(t, handler, http.MethodPost, base, `{"name":"提高冷冻水供水温度","kind":"SETPOINT_RESET","plannedAt":"2026-10-15T09:00:00+08:00","evidenceCommandId":"`+command+`"}`)
	if status != http.StatusCreated || created["evidenceCommandId"] != command {
		t.Fatalf("register = %d %v", status, created)
	}
	if status, _ := call(t, handler, http.MethodPost, base, `{"name":"x","kind":"OTHER","plannedAt":"2026-10-15T09:00:00+08:00","evidenceCommandId":"command-1"}`); status != http.StatusBadRequest {
		t.Fatalf("a command reference that is not a UUID = %d", status)
	}
	_, plain := call(t, handler, http.MethodPost, base, `{"name":"y","kind":"OTHER","plannedAt":"2026-10-15T09:00:00+08:00"}`)
	if plain["evidenceCommandId"] != nil {
		t.Fatalf("a measure without a command = %v", plain["evidenceCommandId"])
	}
}
