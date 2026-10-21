package iam_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/modules/iam/internal/iam"
)

// Who may get a WeChat reminder of an Alarm (#664, docs/specs/miniprogram-wechat.md): Notification
// Service, with nobody signed in, asks IAM which of the people holding reminders can read Alarms of
// the Site now. IAM answers with the same rule as a signed-in person's Alarm read.

const notificationWorkload = "spiffe://hvac.local/platform-gateway"

// alarmFactsBySubject answers each person's Alarm authorization facts.
type alarmFactsBySubject map[string]iam.AuthorizationFacts

func (store alarmFactsBySubject) LookupAlarmAuthorization(_ context.Context, lookup iam.AuthorizationLookup) (iam.AuthorizationFacts, error) {
	return store[lookup.Subject], nil
}

func alarmRecipientsHarness(t *testing.T, sink *capturingAlarmAuditSink) iamHarness {
	moved := principalFacts([]iam.RoleAssignment{{
		TenantID: iam.S1FixtureTenantAID, SiteID: iam.S1FixtureOwnerASite2ID, RoleKey: "operator", Status: iam.FactStatusActive,
		Capabilities: []string{string(alarmauth.ActionRead)},
	}}, nil)
	moved.PolicyRevision = principalCapabilityAlarmPolicy
	disabled := principalAlarmFacts()
	disabled.Principal.Status = "DISABLED"
	return newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.AlarmAuthorizationStore = alarmFactsBySubject{"duty": principalAlarmFacts(), "moved": moved, "disabled": disabled}
		config.AlarmAuditSink = sink
		config.AllowedAlarmRecipientWorkloads = []string{notificationWorkload}
	})
}

func alarmRecipientsBody(t *testing.T) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"tenantId": iam.S1FixtureTenantAID, "siteId": iam.S1FixtureOwnerASite1ID, "alarmId": "01910000-0000-7000-8000-0000000000a1",
		"people": []map[string]string{
			{"subjectIssuer": "https://issuer.example.test", "subject": "duty"},
			{"subjectIssuer": "https://issuer.example.test", "subject": "moved"},
			{"subjectIssuer": "https://issuer.example.test", "subject": "disabled"},
			{"subjectIssuer": "https://issuer.example.test", "subject": "stranger"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestIAMNamesOnlyThePeopleWhoCanReadTheSitesAlarmsNow(t *testing.T) {
	sink := &capturingAlarmAuditSink{}
	harness := alarmRecipientsHarness(t, sink)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, harness.workloadRequestTo(t, iam.AlarmRecipientDecisionPath, notificationWorkload, alarmRecipientsBody(t)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response iam.AlarmRecipientDecision
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	// Only the person with Alarm read on this Site: not one moved to another Site, disabled, or unknown.
	if len(response.Allowed) != 1 || response.Allowed[0].Subject != "duty" || response.Allowed[0].SubjectIssuer != "https://issuer.example.test" {
		t.Fatalf("allowed = %#v", response.Allowed)
	}
	// Each person's decision is recorded like a signed-in Alarm read.
	if len(sink.events) != 4 {
		t.Fatalf("audit evidence = %#v", sink.events)
	}
}

func TestIAMAlarmRecipientDecisionRefusesSessionsAndOtherWorkloads(t *testing.T) {
	harness := alarmRecipientsHarness(t, &capturingAlarmAuditSink{})
	other := httptest.NewRecorder()
	harness.handler.ServeHTTP(other, harness.workloadRequestTo(t, iam.AlarmRecipientDecisionPath, "spiffe://hvac.local/report-service", alarmRecipientsBody(t)))
	assertIAMProblem(t, other, http.StatusUnauthorized, "IAM_WORKLOAD_IDENTITY_INVALID")
	withSession := harness.workloadRequestTo(t, iam.AlarmRecipientDecisionPath, notificationWorkload, alarmRecipientsBody(t))
	withSession.Header.Set("X-Delegation-Grant", "session-delegation")
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, withSession)
	assertIAMProblem(t, recorder, http.StatusBadRequest, "IAM_FORGED_IDENTITY_HEADER")
}
