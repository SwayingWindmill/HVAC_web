package coreclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

func TestResolverCallsPrivateCoreRouteAndMapsBindingSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/internal/v1/registry/sites/site-1/meter-bindings/resolve" {
			t.Fatalf("request=%s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("X-Delegation-Grant") != "grant" {
			t.Fatalf("grant=%q", request.Header.Get("X-Delegation-Grant"))
		}
		if request.URL.Query().Get("deviceId") != "device-1" || request.URL.Query().Get("pointId") != "point-1" || request.URL.Query().Get("sampledAt") == "" {
			t.Fatalf("query=%s", request.URL.RawQuery)
		}
		_, _ = io.WriteString(writer, `{"status":"MATCH","tenantId":"tenant-1","siteId":"site-1","meterId":"meter-1","meterBindingId":"binding-1","topologyVersionId":"topology-1","bindingVersion":4,"revision":9,"energyTypeId":"energy-1","energyType":"electricity","meterRole":"PRIMARY","direction":"IMPORT","deviceId":"device-1","pointId":"point-1","pointType":"COUNTER","effectiveFrom":"2026-01-01T00:00:00Z"}`)
	}))
	defer server.Close()

	resolver, err := NewResolver(Config{BaseURL: server.URL, Grant: "grant", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := resolver.Resolve(context.Background(), energy.BindingResolveInput{
		TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1", PointID: "point-1",
		SampledAt: time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != energy.BindingMatch || resolution.MeterBindingID != "binding-1" || resolution.BindingVersion != 4 || resolution.BindingRevision != 9 || resolution.EnergyType != energy.EnergyTypeElectricity {
		t.Fatalf("resolution=%#v", resolution)
	}
}

func TestResolverRejectsMalformedSuccessPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"status": "MATCH", "bindingVersion": -1})
	}))
	defer server.Close()
	resolver, err := NewResolver(Config{BaseURL: server.URL, Grant: "grant", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.Resolve(context.Background(), energy.BindingResolveInput{SiteID: "site-1", DeviceID: "device-1", PointID: "point-1", SampledAt: time.Now()})
	if err == nil {
		t.Fatal("Resolve() error = nil")
	}
}
