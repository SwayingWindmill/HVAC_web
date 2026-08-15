package analyticsmodel

import (
	"errors"
	"strings"
)

type AuthorizationReason string

const (
	AuthorizationReasonAllowSiteBinding AuthorizationReason = "ALLOW_SITE_BINDING"
	AuthorizationReasonDenyExplicit     AuthorizationReason = "DENY_EXPLICIT"
	AuthorizationReasonDenyPrincipal    AuthorizationReason = "DENY_PRINCIPAL"
	AuthorizationReasonDenyMembership   AuthorizationReason = "DENY_MEMBERSHIP"
	AuthorizationReasonDenyAction       AuthorizationReason = "DENY_ACTION_NOT_GRANTED"
)

type AuthorizationDecisionRequest struct {
	TenantID string `json:"tenantId"`
	SiteID   string `json:"siteId"`
	Action   string `json:"action"`
}

func (request AuthorizationDecisionRequest) Validate() error {
	if !validUUIDv7(request.TenantID) || !validUUIDv7(request.SiteID) {
		return errors.New("analytics authorization tenant and site must be UUIDv7")
	}
	if strings.TrimSpace(request.Action) != EnergySeriesAction {
		return errors.New("analytics authorization action is invalid")
	}
	return nil
}

type AuthorizationDecision struct {
	Allowed        bool                `json:"allowed"`
	PrincipalID    string              `json:"principalId"`
	SubjectIssuer  string              `json:"subjectIssuer"`
	Subject        string              `json:"subject"`
	TenantID       string              `json:"tenantId"`
	SiteID         string              `json:"siteId"`
	Action         string              `json:"action"`
	PolicyRevision string              `json:"policyRevision"`
	ReasonCode     AuthorizationReason `json:"reasonCode"`
	DecidedAt      string              `json:"decidedAt"`
}

type AuthorizationDecisionResponse struct {
	Decision AuthorizationDecision `json:"decision"`
}
