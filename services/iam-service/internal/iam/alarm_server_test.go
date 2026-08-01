package iam_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/services/iam-service/internal/iam"
)

func TestIAMAlarmDecisionPublishesExactAllowAndAuditEvidence(t *testing.T) {
	sink := &capturingAlarmAuditSink{}
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.AlarmAuthorizationStore = fixedAlarmStore{facts: principalAlarmFacts()}
		config.AlarmAuditSink = sink
	})
	claims := validIAMClaims(harness.now, "fixture-user", "alarm:authorize")
	claims.ActingOrganizationID = iam.S1FixtureOwnerAOrganizationID
	input := alarmauth.DecisionRequest{
		ActingOrganizationID: iam.S1FixtureOwnerAOrganizationID,
		SiteID:               iam.S1FixtureOwnerASite1ID,
		Action:               alarmauth.ActionList,
	}
	body, _ := json.Marshal(input)
	request := harness.request(t, iam.AlarmDecisionPath, strings.NewReader(string(body)), claims, harness.gatewaySigner)
	request.Header.Set("X-Request-ID", "alarm-decision-request-1")
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response alarmauth.DecisionResponse
	if json.NewDecoder(recorder.Body).Decode(&response) != nil || !response.Decision.Allowed || response.Decision.Action != alarmauth.ActionList || response.Decision.SiteID != iam.S1FixtureOwnerASite1ID {
		t.Fatalf("unexpected Alarm decision: %#v", response)
	}
	if len(sink.events) != 1 || !sink.events[0].Allowed || sink.events[0].RequestID != "alarm-decision-request-1" || sink.events[0].PolicyRevision != principalCapabilityAlarmPolicy {
		t.Fatalf("unexpected Alarm audit evidence: %#v", sink.events)
	}
}

func TestIAMAlarmDecisionDeniesCrossSiteWithoutGrantMaterial(t *testing.T) {
	sink := &capturingAlarmAuditSink{}
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.AlarmAuthorizationStore = fixedAlarmStore{facts: principalAlarmFacts()}
		config.AlarmAuditSink = sink
	})
	claims := validIAMClaims(harness.now, "fixture-user", "alarm:authorize")
	claims.ActingOrganizationID = iam.S1FixtureOwnerAOrganizationID
	input := alarmauth.DecisionRequest{
		ActingOrganizationID: iam.S1FixtureOwnerAOrganizationID,
		SiteID:               "01910000-0002-7000-8000-000000000001",
		Action:               alarmauth.ActionList,
	}
	body, _ := json.Marshal(input)
	request := harness.request(t, iam.AlarmDecisionPath, strings.NewReader(string(body)), claims, harness.gatewaySigner)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response alarmauth.DecisionResponse
	if json.NewDecoder(recorder.Body).Decode(&response) != nil || response.Decision.Allowed || response.Decision.ReasonCode != alarmauth.ReasonDenyScope {
		t.Fatalf("unexpected cross-Site decision: %#v", response)
	}
	if strings.Contains(recorder.Body.String(), "delegation") || len(sink.events) != 1 || sink.events[0].Allowed {
		t.Fatalf("denied Alarm decision leaked grant material or omitted audit: body=%s audit=%#v", recorder.Body.String(), sink.events)
	}
}

func TestIAMAlarmDecisionRejectsExpandedBodyWrongActionAndAuditFailure(t *testing.T) {
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.AlarmAuthorizationStore = fixedAlarmStore{facts: principalAlarmFacts()}
	})
	claims := validIAMClaims(harness.now, "fixture-user", "alarm:authorize")
	claims.ActingOrganizationID = iam.S1FixtureOwnerAOrganizationID
	request := harness.request(t, iam.AlarmDecisionPath, strings.NewReader(`{"actingOrganizationId":"`+iam.S1FixtureOwnerAOrganizationID+`","siteId":"`+iam.S1FixtureOwnerASite1ID+`","action":"alarm:list","roles":["admin"]}`), claims, harness.gatewaySigner)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	assertIAMProblem(t, recorder, http.StatusBadRequest, "IAM_ALARM_DECISION_REQUEST_INVALID")

	wrongAction := validIAMClaims(harness.now, "fixture-user", "principal:read")
	body, _ := json.Marshal(alarmauth.DecisionRequest{ActingOrganizationID: iam.S1FixtureOwnerAOrganizationID, SiteID: iam.S1FixtureOwnerASite1ID, Action: alarmauth.ActionList})
	request = harness.request(t, iam.AlarmDecisionPath, strings.NewReader(string(body)), wrongAction, harness.gatewaySigner)
	recorder = httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	assertIAMProblem(t, recorder, http.StatusForbidden, "IAM_DELEGATION_REJECTED")

	harness = newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.AlarmAuthorizationStore = fixedAlarmStore{facts: principalAlarmFacts()}
		config.AlarmAuditSink = &capturingAlarmAuditSink{err: errors.New("audit unavailable")}
	})
	request = harness.request(t, iam.AlarmDecisionPath, strings.NewReader(string(body)), claims, harness.gatewaySigner)
	recorder = httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	assertIAMProblem(t, recorder, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_AUDIT_UNAVAILABLE")
}

type capturingAlarmAuditSink struct {
	events []iam.AlarmDecisionAudit
	err    error
}

func (sink *capturingAlarmAuditSink) RecordAlarmDecision(_ context.Context, event iam.AlarmDecisionAudit) error {
	sink.events = append(sink.events, event)
	return sink.err
}
