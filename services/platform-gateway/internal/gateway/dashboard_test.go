package gateway

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

func TestSameDashboardSessionRejectsScopeDrift(t *testing.T) {
	base := bffSession{Session: sessionstore.Session{
		ID:        "session-1",
		TenantID:  "tenant-1",
		Principal: identitycontext.UserPrincipal{Subject: "user-1", Issuer: "issuer-1"},
	}}
	if !sameDashboardSession(base, base) {
		t.Fatal("identical dashboard session was rejected")
	}
	for name, mutate := range map[string]func(*bffSession){
		"session": func(value *bffSession) { value.ID = "session-2" },
		"tenant":  func(value *bffSession) { value.TenantID = "tenant-2" },
		"subject": func(value *bffSession) { value.Principal.Subject = "user-2" },
		"issuer":  func(value *bffSession) { value.Principal.Issuer = "issuer-2" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if sameDashboardSession(base, candidate) {
				t.Fatalf("accepted dashboard session %s drift", name)
			}
		})
	}
}

func TestDashboardStreamBaseGeneratedAt(t *testing.T) {
	valid := "2026-08-19T12:34:56.789Z"
	value, ok := dashboardStreamBaseGeneratedAt(url.Values{"baseGeneratedAt": {valid}})
	if !ok || value != valid {
		t.Fatalf("valid baseGeneratedAt = %q, %t", value, ok)
	}

	for name, query := range map[string]url.Values{
		"missing":       {},
		"extra query":   {"baseGeneratedAt": {valid}, "other": {"x"}},
		"duplicate":     {"baseGeneratedAt": {valid, valid}},
		"offset":        {"baseGeneratedAt": {"2026-08-19T20:34:56.789+08:00"}},
		"no millis":     {"baseGeneratedAt": {"2026-08-19T12:34:56Z"}},
		"invalid value": {"baseGeneratedAt": {"not-a-time"}},
	} {
		t.Run(name, func(t *testing.T) {
			if value, ok := dashboardStreamBaseGeneratedAt(query); ok {
				t.Fatalf("accepted invalid baseGeneratedAt %q", value)
			}
		})
	}
}

func TestWriteDashboardSummaryEvent(t *testing.T) {
	recorder := httptest.NewRecorder()
	delta := platformapi.SiteDashboardSummaryDelta{
		SchemaVersion:   1,
		BaseGeneratedAt: "2026-08-19T12:34:56.789Z",
		Summary: platformapi.SiteDashboardSummary{
			SchemaVersion: 1,
			TenantID:      "01900000-0001-7000-8000-000000000001",
			SiteID:        "01900000-0002-7000-8000-000000000002",
			GeneratedAt:   "2026-08-19T12:35:01.789Z",
		},
	}
	if err := writeDashboardSummaryEvent(recorder, recorder, delta); err != nil {
		t.Fatal(err)
	}
	if !recorder.Flushed {
		t.Fatal("dashboard SSE event was not flushed")
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"event: dashboard-summary\n",
		`"baseGeneratedAt":"2026-08-19T12:34:56.789Z"`,
		`"generatedAt":"2026-08-19T12:35:01.789Z"`,
		"\n\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard SSE body missing %q: %s", want, body)
		}
	}
}

func TestDashboardPopulationInputsExcludeInactiveDevicesFromPresence(t *testing.T) {
	devices := []platformapi.Device{
		{ID: "active", Status: "ACTIVE"},
		{ID: "retired", Status: "RETIRED"},
		{ID: "inactive", Status: "INACTIVE"},
	}
	active, observations := dashboardPopulationInputs(devices)
	if len(active) != 1 || active[0].ID != "active" {
		t.Fatalf("active devices = %#v", active)
	}
	if len(observations) != 2 {
		t.Fatalf("non-applicable observations = %d, want 2", len(observations))
	}
	for _, observation := range observations {
		if observation.Applicability != "NOT_APPLICABLE" {
			t.Fatalf("inactive observation applicability = %q", observation.Applicability)
		}
	}
}
