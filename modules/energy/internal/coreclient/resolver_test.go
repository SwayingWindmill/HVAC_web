package coreclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

const siteBindingsPayload = `{"siteId":"site-1","items":[{"tenantId":"tenant-1","siteId":"site-1","meterId":"meter-1","meterBindingId":"binding-1","topologyVersionId":"topology-1","bindingVersion":4,"revision":9,"energyTypeId":"energy-1","energyType":"electricity","meterRole":"PRIMARY","direction":"IMPORT","accountingScope":"BRANCH","loadDeviceId":"chiller-1","deviceId":"device-1","pointId":"point-1","pointType":"COUNTER","effectiveFrom":"2026-01-01T00:00:00Z"},{"tenantId":"tenant-1","siteId":"site-1","meterId":"meter-2","meterBindingId":"binding-2","topologyVersionId":"topology-1","bindingVersion":2,"revision":5,"energyTypeId":"energy-1","energyType":"electricity","meterRole":"PRIMARY","direction":"IMPORT","accountingScope":"SITE","loadDeviceId":"","deviceId":"device-2","pointId":"point-2","pointType":"COUNTER","effectiveFrom":"2026-01-01T00:00:00Z","effectiveTo":"2026-07-30T00:00:00Z"}]}`

func TestResolverFetchesSiteMeterBindingsOncePerBatchAndResolvesLocally(t *testing.T) {
	clock := newTestClock()
	iam := newFakeIAM(t, clock, true)
	coreCalls := 0
	core := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		coreCalls++
		if request.Method != http.MethodGet || request.URL.Path != "/internal/v1/registry/sites/site-1/meter-bindings" {
			t.Fatalf("request=%s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("X-Delegation-Grant") != "grant-tenant-1-1" {
			t.Fatalf("grant=%q", request.Header.Get("X-Delegation-Grant"))
		}
		_, _ = io.WriteString(writer, siteBindingsPayload)
	}))
	defer core.Close()

	resolver := newTestResolver(t, core, iam, clock)
	for i := 0; i < 256; i++ {
		input := resolveInput("tenant-1")
		switch i % 3 {
		case 1:
			input.DeviceID = "device-2"
			input.PointID = "point-2"
		case 2:
			// Simulated unmapped BTU counter point
			input.DeviceID = "btu-1"
			input.PointID = "cooling-point"
		}
		res, err := resolver.Resolve(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		switch i % 3 {
		case 0:
			if res.Status != energy.BindingMatch || res.MeterBindingID != "binding-1" || res.BindingVersion != 4 || res.BindingRevision != 9 || res.EnergyType != energy.EnergyTypeElectricity || res.AccountingScope != energy.AccountingScopeBranch || res.LoadDeviceID != "chiller-1" {
				t.Fatalf("branch resolution=%#v", res)
			}
		case 1:
			if res.Status != energy.BindingMatch || res.MeterBindingID != "binding-2" || res.AccountingScope != energy.AccountingScopeSite || res.LoadDeviceID != "" {
				t.Fatalf("site resolution=%#v", res)
			}
		case 2:
			if res.Status != energy.BindingNoMatch {
				t.Fatalf("unmapped counter resolution=%#v", res)
			}
		}
	}
	if coreCalls != 1 {
		t.Fatalf("256 resolutions made %d Core HTTP calls, want 1", coreCalls)
	}
}

func TestResolverRefreshesOncePerMissKeyAfterAdvanceAndOnCacheTTLExpiry(t *testing.T) {
	clock := newTestClock()
	iam := newFakeIAM(t, clock, true)
	coreCalls := 0
	serveNewPoint := false
	core := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		coreCalls++
		if serveNewPoint {
			_, _ = io.WriteString(writer, `{"siteId":"site-1","items":[{"tenantId":"tenant-1","siteId":"site-1","meterId":"meter-1","meterBindingId":"binding-new","topologyVersionId":"topology-1","bindingVersion":1,"revision":1,"energyTypeId":"energy-1","energyType":"electricity","meterRole":"PRIMARY","direction":"IMPORT","accountingScope":"SITE","loadDeviceId":"","deviceId":"device-new","pointId":"point-new","pointType":"COUNTER","effectiveFrom":"2026-01-01T00:00:00Z"}]}`)
			return
		}
		_, _ = io.WriteString(writer, siteBindingsPayload)
	}))
	defer core.Close()

	resolver := newTestResolver(t, core, iam, clock)
	if _, err := resolver.Resolve(context.Background(), resolveInput("tenant-1")); err != nil {
		t.Fatal(err)
	}

	// Advancing slightly picks up a newly onboarded point via single miss-triggered refresh.
	clock.now = clock.now.Add(2 * time.Second)
	serveNewPoint = true
	newInput := resolveInput("tenant-1")
	newInput.DeviceID = "device-new"
	newInput.PointID = "point-new"
	res, err := resolver.Resolve(context.Background(), newInput)
	if err != nil || res.Status != energy.BindingMatch || res.MeterBindingID != "binding-new" {
		t.Fatalf("miss refresh resolution=%#v err=%v", res, err)
	}
	if coreCalls != 2 {
		t.Fatalf("coreCalls=%d, want 2 after new-point miss refresh", coreCalls)
	}

	// Distinct unmapped BTU counter points each refresh at most once within CacheTTL without ping-ponging.
	unmappedA := resolveInput("tenant-1")
	unmappedA.DeviceID = "btu-1"
	unmappedA.PointID = "cooling-point"
	unmappedB := resolveInput("tenant-1")
	unmappedB.DeviceID = "btu-2"
	unmappedB.PointID = "heating-point"

	for _, unmapped := range []energy.BindingResolveInput{unmappedA, unmappedB} {
		clock.now = clock.now.Add(time.Second)
		if miss, err := resolver.Resolve(context.Background(), unmapped); err != nil || miss.Status != energy.BindingNoMatch {
			t.Fatalf("first unmapped=%#v err=%v", miss, err)
		}
	}
	if coreCalls != 4 {
		t.Fatalf("first miss for two distinct unmapped points triggered %d Core calls, want 4", coreCalls)
	}
	for i := 0; i < 6; i++ {
		clock.now = clock.now.Add(time.Second)
		target := unmappedA
		if i%2 == 1 {
			target = unmappedB
		}
		if miss, err := resolver.Resolve(context.Background(), target); err != nil || miss.Status != energy.BindingNoMatch {
			t.Fatalf("repeat unmapped=%#v err=%v", miss, err)
		}
	}
	if coreCalls != 4 {
		t.Fatalf("alternating unmapped queries triggered %d Core calls, want 4 total", coreCalls)
	}

	// Once CacheTTL (30s) elapses, cache refreshes cleanly.
	clock.now = clock.now.Add(35 * time.Second)
	if _, err := resolver.Resolve(context.Background(), newInput); err != nil {
		t.Fatal(err)
	}
	if coreCalls != 5 {
		t.Fatalf("coreCalls=%d, want 5 after TTL expiry", coreCalls)
	}
}

func TestResolverReusesWorkloadGrantUntilItNearlyExpires(t *testing.T) {
	clock := newTestClock()
	iam := newFakeIAM(t, clock, true)
	presented := []string{}
	core := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		presented = append(presented, request.Header.Get("X-Delegation-Grant"))
		_, _ = io.WriteString(writer, siteBindingsPayload)
	}))
	defer core.Close()
	resolver, err := NewResolver(Config{
		BaseURL: core.URL, IAMURL: iam.URL, HTTPClient: core.Client(),
		Now: func() time.Time { return clock.now }, CacheTTL: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, elapsed := range []time.Duration{0, 19 * time.Second, 31 * time.Second} {
		clock.now = clock.start.Add(elapsed)
		if _, err := resolver.Resolve(context.Background(), resolveInput("tenant-1")); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(presented, ","); got != "grant-tenant-1-1,grant-tenant-1-1,grant-tenant-1-2" {
		t.Fatalf("presented grants = %s", got)
	}
}

func TestResolverHoldsOneGrantPerTenant(t *testing.T) {
	clock := newTestClock()
	iam := newFakeIAM(t, clock, true)
	presented := []string{}
	core := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		presented = append(presented, request.Header.Get("X-Delegation-Grant"))
		_, _ = io.WriteString(writer, siteBindingsPayload)
	}))
	defer core.Close()
	resolver := newTestResolver(t, core, iam, clock)

	for _, tenant := range []string{"tenant-1", "tenant-2", "tenant-1"} {
		if _, err := resolver.Resolve(context.Background(), resolveInput(tenant)); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(presented, ","); got != "grant-tenant-1-1,grant-tenant-2-1" {
		t.Fatalf("presented grants = %s", got)
	}
}

func TestResolverFailsWhenIAMDeniesTheWorkload(t *testing.T) {
	clock := newTestClock()
	iam := newFakeIAM(t, clock, false)
	core := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("Core was called without a grant")
	}))
	defer core.Close()

	_, err := newTestResolver(t, core, iam, clock).Resolve(context.Background(), resolveInput("tenant-1"))
	if err == nil || !strings.Contains(err.Error(), "DENY_TENANT_MEMBERSHIP_REQUIRED") {
		t.Fatalf("Resolve() error = %v", err)
	}
}

func TestResolverRejectsMalformedSuccessPayload(t *testing.T) {
	clock := newTestClock()
	iam := newFakeIAM(t, clock, true)
	core := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"siteId": "site-1", "items": []map[string]any{{"bindingVersion": -1}}})
	}))
	defer core.Close()
	if _, err := newTestResolver(t, core, iam, clock).Resolve(context.Background(), resolveInput("tenant-1")); err == nil {
		t.Fatal("Resolve() error = nil")
	}
}

type testClock struct {
	start time.Time
	now   time.Time
}

func newTestClock() *testClock {
	start := time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC)
	return &testClock{start: start, now: start}
}

// newFakeIAM issues numbered 30-second grants per tenant, like IAM's
// Registry workload decision route.
func newFakeIAM(t *testing.T, clock *testClock, allow bool) *httptest.Server {
	t.Helper()
	issued := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/internal/v1/registry/workload-decision" || request.Header.Get("X-Delegation-Grant") != "" {
			t.Fatalf("IAM request=%s %s", request.Method, request.URL.Path)
		}
		var body struct {
			TenantID string `json:"tenantId"`
			Action   string `json:"action"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Action != "meter-binding.resolve" {
			t.Fatalf("IAM body=%#v err=%v", body, err)
		}
		if !allow {
			_, _ = fmt.Fprintf(writer, `{"decision":{"allowed":false,"tenantId":%q,"reasonCode":"DENY_TENANT_MEMBERSHIP_REQUIRED"}}`, body.TenantID)
			return
		}
		issued[body.TenantID]++
		_, _ = fmt.Fprintf(writer, `{"decision":{"allowed":true,"tenantId":%q,"reasonCode":"ALLOW_TENANT_ROLE"},"delegationGrant":"grant-%s-%d","delegationGrantExpiresAt":%q}`,
			body.TenantID, body.TenantID, issued[body.TenantID], clock.now.Add(30*time.Second).Format(time.RFC3339))
	}))
	t.Cleanup(server.Close)
	return server
}

func newTestResolver(t *testing.T, core, iam *httptest.Server, clock *testClock) *Resolver {
	t.Helper()
	resolver, err := NewResolver(Config{BaseURL: core.URL, IAMURL: iam.URL, HTTPClient: core.Client(), Now: func() time.Time { return clock.now }})
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

func resolveInput(tenantID string) energy.BindingResolveInput {
	return energy.BindingResolveInput{
		TenantID: tenantID, SiteID: "site-1", DeviceID: "device-1", PointID: "point-1",
		SampledAt: time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
	}
}

func TestResolverDecodesReadingUnitAndRatioVersions(t *testing.T) {
	clock := newTestClock()
	iam := newFakeIAM(t, clock, true)
	core := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{
  "siteId": "site-1",
  "items": [{
    "tenantId": "tenant-1",
    "siteId": "site-1",
    "meterId": "meter-1",
    "meterBindingId": "binding-1",
    "topologyVersionId": "topo-1",
    "bindingVersion": 2,
    "revision": 3,
    "energyTypeId": "et-1",
    "energyType": "electricity",
    "meterRole": "PRIMARY",
    "direction": "IMPORT",
    "accountingScope": "SITE",
    "deviceId": "device-1",
    "pointId": "point-1",
    "pointType": "COUNTER",
    "readingUnit": "MWh",
    "ratioVersions": [{
      "id": "rv-1",
      "ctRatio": 20,
      "ptRatio": 1,
      "meterMultiplier": 1,
      "ratioApplicationMode": "PLATFORM_APPLIED",
      "readingUnit": "kWh",
      "effectiveFrom": "2026-07-01T00:00:00Z",
      "status": "RELEASED"
    }],
    "effectiveFrom": "2026-01-01T00:00:00Z"
  }]
}`))
	}))
	defer core.Close()

	res, err := newTestResolver(t, core, iam, clock).Resolve(context.Background(), resolveInput("tenant-1"))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if res.ReadingUnit != "MWh" || len(res.RatioVersions) != 1 || res.RatioVersions[0].CTRatio != 20 {
		t.Fatalf("unexpected decoded readingUnit/ratioVersions: %+v", res)
	}
}
