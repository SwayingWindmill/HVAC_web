package iam_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/modules/iam/internal/iam"
)

// Who may get a WeChat reminder (#664, docs/specs/miniprogram-wechat.md): Notification Service,
// with nobody signed in, asks IAM which of the people holding reminders may get one now. An
// Alarm reminder needs the rule of a signed-in Alarm read on the Site, a daily summary the rule of
// a signed-in energy read.

const notificationWorkload = "spiffe://hvac.local/platform-gateway"

// factsBySubject answers each person's authorization facts, for Alarms and for energy.
type factsBySubject map[string]iam.AuthorizationFacts

func (store factsBySubject) LookupAlarmAuthorization(_ context.Context, lookup iam.AuthorizationLookup) (iam.AuthorizationFacts, error) {
	return store[lookup.Subject], nil
}

func (store factsBySubject) LookupRegistryAuthorization(_ context.Context, lookup iam.AuthorizationLookup) (iam.AuthorizationFacts, error) {
	return store[lookup.Subject], nil
}

func reminderRecipientsHarness(t *testing.T, sink *capturingAlarmAuditSink) iamHarness {
	moved := principalFacts([]iam.RoleAssignment{{
		TenantID: iam.S1FixtureTenantAID, SiteID: iam.S1FixtureOwnerASite2ID, RoleKey: "operator", Status: iam.FactStatusActive,
		Capabilities: []string{string(alarmauth.ActionRead), analyticsmodel.EnergySeriesAction},
	}}, nil)
	moved.PolicyRevision = principalCapabilityAlarmPolicy
	energyOnly := principalFacts([]iam.RoleAssignment{{
		TenantID: iam.S1FixtureTenantAID, SiteID: iam.S1FixtureOwnerASite1ID, RoleKey: "energy-viewer", Status: iam.FactStatusActive,
		Capabilities: []string{analyticsmodel.EnergySeriesAction},
	}}, nil)
	disabled := principalAlarmFacts()
	disabled.Principal.Status = "DISABLED"
	facts := factsBySubject{"duty": principalAlarmFacts(), "moved": moved, "energy": energyOnly, "disabled": disabled}
	return newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.AlarmAuthorizationStore = facts
		config.AuthorizationStore = facts
		config.AlarmAuditSink = sink
		config.AllowedReminderRecipientWorkloads = []string{notificationWorkload}
	})
}

func reminderRecipientsBody(t *testing.T, kind string) string {
	t.Helper()
	request := map[string]any{
		"tenantId": iam.S1FixtureTenantAID, "siteId": iam.S1FixtureOwnerASite1ID, "kind": kind,
		"people": []map[string]string{
			{"subjectIssuer": "https://issuer.example.test", "subject": "duty"},
			{"subjectIssuer": "https://issuer.example.test", "subject": "moved"},
			{"subjectIssuer": "https://issuer.example.test", "subject": "energy"},
			{"subjectIssuer": "https://issuer.example.test", "subject": "disabled"},
			{"subjectIssuer": "https://issuer.example.test", "subject": "stranger"},
		},
	}
	if kind == "ALARM" {
		request["alarmId"] = "01910000-0000-7000-8000-0000000000a1"
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func allowedSubjects(t *testing.T, harness iamHarness, kind string) []string {
	t.Helper()
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, harness.workloadRequestTo(t, iam.ReminderRecipientDecisionPath, notificationWorkload, reminderRecipientsBody(t, kind)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response iam.ReminderRecipientDecision
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	subjects := []string{}
	for _, person := range response.Allowed {
		subjects = append(subjects, person.Subject)
	}
	return subjects
}

func TestIAMNamesOnlyThePeopleWhoCanReadTheSitesAlarmsNow(t *testing.T) {
	sink := &capturingAlarmAuditSink{}
	harness := reminderRecipientsHarness(t, sink)
	// Only the person with Alarm read on this Site: not one moved to another Site, one who reads
	// only energy, disabled, or unknown.
	if allowed := allowedSubjects(t, harness, "ALARM"); len(allowed) != 1 || allowed[0] != "duty" {
		t.Fatalf("allowed = %v", allowed)
	}
	// Each person's decision is recorded like a signed-in Alarm read.
	if len(sink.events) != 5 {
		t.Fatalf("audit evidence = %#v", sink.events)
	}
}

func TestIAMNamesOnlyThePeopleWhoCanReadTheSitesEnergyNow(t *testing.T) {
	harness := reminderRecipientsHarness(t, &capturingAlarmAuditSink{})
	if allowed := allowedSubjects(t, harness, "DAILY"); len(allowed) != 1 || allowed[0] != "energy" {
		t.Fatalf("allowed = %v", allowed)
	}
}

func TestIAMReminderRecipientDecisionRefusesSessionsAndOtherWorkloads(t *testing.T) {
	harness := reminderRecipientsHarness(t, &capturingAlarmAuditSink{})
	other := httptest.NewRecorder()
	harness.handler.ServeHTTP(other, harness.workloadRequestTo(t, iam.ReminderRecipientDecisionPath, "spiffe://hvac.local/report-service", reminderRecipientsBody(t, "ALARM")))
	assertIAMProblem(t, other, http.StatusUnauthorized, "IAM_WORKLOAD_IDENTITY_INVALID")
	withSession := harness.workloadRequestTo(t, iam.ReminderRecipientDecisionPath, notificationWorkload, reminderRecipientsBody(t, "ALARM"))
	withSession.Header.Set("X-Delegation-Grant", "session-delegation")
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, withSession)
	assertIAMProblem(t, recorder, http.StatusBadRequest, "IAM_FORGED_IDENTITY_HEADER")
}
