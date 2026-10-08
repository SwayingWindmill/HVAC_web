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

const matchPayload = `{"status":"MATCH","tenantId":"tenant-1","siteId":"site-1","meterId":"meter-1","meterBindingId":"binding-1","topologyVersionId":"topology-1","bindingVersion":4,"revision":9,"energyTypeId":"energy-1","energyType":"electricity","meterRole":"PRIMARY","direction":"IMPORT","deviceId":"device-1","pointId":"point-1","pointType":"COUNTER","effectiveFrom":"2026-01-01T00:00:00Z"}`

func TestResolverCallsPrivateCoreRouteAndMapsBindingSnapshot(t *testing.T) {
	clock := newTestClock()
	iam := newFakeIAM(t, clock, true)
	core := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/internal/v1/registry/sites/site-1/meter-bindings/resolve" {
			t.Fatalf("request=%s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("X-Delegation-Grant") != "grant-tenant-1-1" {
			t.Fatalf("grant=%q", request.Header.Get("X-Delegation-Grant"))
		}
		if request.URL.Query().Get("deviceId") != "device-1" || request.URL.Query().Get("pointId") != "point-1" || request.URL.Query().Get("sampledAt") == "" {
			t.Fatalf("query=%s", request.URL.RawQuery)
		}
		_, _ = io.WriteString(writer, matchPayload)
	}))
	defer core.Close()

	resolution, err := newTestResolver(t, core, iam, clock).Resolve(context.Background(), resolveInput("tenant-1"))
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != energy.BindingMatch || resolution.MeterBindingID != "binding-1" || resolution.BindingVersion != 4 || resolution.BindingRevision != 9 || resolution.EnergyType != energy.EnergyTypeElectricity {
		t.Fatalf("resolution=%#v", resolution)
	}
}

func TestResolverReusesWorkloadGrantUntilItNearlyExpires(t *testing.T) {
	clock := newTestClock()
	iam := newFakeIAM(t, clock, true)
	presented := []string{}
	core := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		presented = append(presented, request.Header.Get("X-Delegation-Grant"))
		_, _ = io.WriteString(writer, matchPayload)
	}))
	defer core.Close()
	resolver := newTestResolver(t, core, iam, clock)

	for _, elapsed := range []time.Duration{0, 19 * time.Second, 21 * time.Second} {
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
		_, _ = io.WriteString(writer, matchPayload)
	}))
	defer core.Close()
	resolver := newTestResolver(t, core, iam, clock)

	for _, tenant := range []string{"tenant-1", "tenant-2", "tenant-1"} {
		if _, err := resolver.Resolve(context.Background(), resolveInput(tenant)); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(presented, ","); got != "grant-tenant-1-1,grant-tenant-2-1,grant-tenant-1-1" {
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
		_ = json.NewEncoder(writer).Encode(map[string]any{"status": "MATCH", "bindingVersion": -1})
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
