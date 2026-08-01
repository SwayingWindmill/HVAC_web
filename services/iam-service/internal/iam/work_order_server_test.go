package iam_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/workorderauth"
	"github.com/quanlaihe/hvac-web/services/iam-service/internal/iam"
)

func TestIAMWorkOrderDecisionPublishesExactAllowAndAuditEvidence(t *testing.T) {
	sink := &capturingWorkOrderAuditSink{}
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.WorkOrderAuthorizationStore = fixedWorkOrderStore{facts: principalWorkOrderFacts()}
		config.WorkOrderAuditSink = sink
	})
	claims := validIAMClaims(harness.now, "fixture-user", "work-order:authorize")
	claims.ActingOrganizationID = iam.S1FixtureOwnerAOrganizationID
	input := workorderauth.DecisionRequest{
		ActingOrganizationID: iam.S1FixtureOwnerAOrganizationID,
		SiteID:               iam.S1FixtureOwnerASite1ID,
		Action:               workorderauth.ActionList,
	}
	body, _ := json.Marshal(input)
	request := harness.request(t, iam.WorkOrderDecisionPath, strings.NewReader(string(body)), claims, harness.gatewaySigner)
	request.Header.Set("X-Request-ID", "work-order-decision-request-1")
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response workorderauth.DecisionResponse
	if json.NewDecoder(recorder.Body).Decode(&response) != nil || !response.Decision.Allowed || response.Decision.Action != workorderauth.ActionList || response.Decision.SiteID != iam.S1FixtureOwnerASite1ID {
		t.Fatalf("unexpected Work Order decision: %#v", response)
	}
	if len(sink.events) != 1 || !sink.events[0].Allowed || sink.events[0].RequestID != "work-order-decision-request-1" || sink.events[0].PolicyRevision != principalCapabilityWorkOrderPolicy {
		t.Fatalf("unexpected Work Order audit evidence: %#v", sink.events)
	}
}

func TestIAMWorkOrderDecisionDeniesCrossSiteWithoutGrantMaterial(t *testing.T) {
	sink := &capturingWorkOrderAuditSink{}
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.WorkOrderAuthorizationStore = fixedWorkOrderStore{facts: principalWorkOrderFacts()}
		config.WorkOrderAuditSink = sink
	})
	claims := validIAMClaims(harness.now, "fixture-user", "work-order:authorize")
	claims.ActingOrganizationID = iam.S1FixtureOwnerAOrganizationID
	input := workorderauth.DecisionRequest{
		ActingOrganizationID: iam.S1FixtureOwnerAOrganizationID,
		SiteID:               "01910000-0002-7000-8000-000000000001",
		Action:               workorderauth.ActionList,
	}
	body, _ := json.Marshal(input)
	request := harness.request(t, iam.WorkOrderDecisionPath, strings.NewReader(string(body)), claims, harness.gatewaySigner)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response workorderauth.DecisionResponse
	if json.NewDecoder(recorder.Body).Decode(&response) != nil || response.Decision.Allowed || response.Decision.ReasonCode != workorderauth.ReasonDenyScope {
		t.Fatalf("unexpected cross-Site decision: %#v", response)
	}
	if strings.Contains(recorder.Body.String(), "delegation") || len(sink.events) != 1 || sink.events[0].Allowed {
		t.Fatalf("denied Work Order decision leaked grant material or omitted audit: body=%s audit=%#v", recorder.Body.String(), sink.events)
	}
}

func TestIAMWorkOrderDecisionRejectsExpandedBodyWrongActionAndAuditFailure(t *testing.T) {
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.WorkOrderAuthorizationStore = fixedWorkOrderStore{facts: principalWorkOrderFacts()}
	})
	claims := validIAMClaims(harness.now, "fixture-user", "work-order:authorize")
	claims.ActingOrganizationID = iam.S1FixtureOwnerAOrganizationID
	expandedBody := map[string]any{
		"actingOrganizationId": iam.S1FixtureOwnerAOrganizationID,
		"siteId":               iam.S1FixtureOwnerASite1ID,
		"action":               "work-order:list",
		"roles":                []string{"admin"},
	}
	encodedExpandedBody, _ := json.Marshal(expandedBody)
	request := harness.request(t, iam.WorkOrderDecisionPath, strings.NewReader(string(encodedExpandedBody)), claims, harness.gatewaySigner)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	assertIAMProblem(t, recorder, http.StatusBadRequest, "IAM_WORK_ORDER_DECISION_REQUEST_INVALID")

	wrongAction := validIAMClaims(harness.now, "fixture-user", "principal:read")
	body, _ := json.Marshal(workorderauth.DecisionRequest{ActingOrganizationID: iam.S1FixtureOwnerAOrganizationID, SiteID: iam.S1FixtureOwnerASite1ID, Action: workorderauth.ActionList})
	request = harness.request(t, iam.WorkOrderDecisionPath, strings.NewReader(string(body)), wrongAction, harness.gatewaySigner)
	recorder = httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	assertIAMProblem(t, recorder, http.StatusForbidden, "IAM_DELEGATION_REJECTED")

	harness = newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.WorkOrderAuthorizationStore = fixedWorkOrderStore{facts: principalWorkOrderFacts()}
		config.WorkOrderAuditSink = &capturingWorkOrderAuditSink{err: errors.New("audit unavailable")}
	})
	request = harness.request(t, iam.WorkOrderDecisionPath, strings.NewReader(string(body)), claims, harness.gatewaySigner)
	recorder = httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	assertIAMProblem(t, recorder, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_AUDIT_UNAVAILABLE")
}

type capturingWorkOrderAuditSink struct {
	events []iam.WorkOrderDecisionAudit
	err    error
}

func (sink *capturingWorkOrderAuditSink) RecordWorkOrderDecision(_ context.Context, event iam.WorkOrderDecisionAudit) error {
	sink.events = append(sink.events, event)
	return sink.err
}
