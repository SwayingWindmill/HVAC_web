package gateway

import (
	"context"
	"net/http"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

// One call from the Gateway to a domain owner on a signed-in caller's behalf has one shape,
// whatever the domain: the policy decision point decides the action on the scope, the Gateway
// signs a context naming exactly that action and scope for the owner's audience, the owner serves
// the request, and the Gateway checks a successful answer against the public contract before it
// leaves. CallOwner is that shape; a domain supplies only what differs (domainOwner).

// ownerCaller is the signed-in Principal a call serves and the workload that presents the signed
// context to the owner: the Gateway for the pages, the Operations Agent for its tools.
type ownerCaller struct {
	session   bffSession
	presenter string
}

// ownerScope is what an action is bound to: a Tenant and a Site, and for one resource its kind and
// id. The signed context names each, so the owner serves nothing wider than was decided.
type ownerScope struct {
	tenantID     string
	siteID       string
	resourceKind string
	resourceID   string
}

func (scope ownerScope) grants() []string {
	grants := []string{"tenant:" + scope.tenantID, "site:" + scope.siteID}
	if scope.resourceID != "" {
		grants = append(grants, scope.resourceKind+":"+scope.resourceID)
	}
	return grants
}

// ownerDecision is what the policy decision point allowed: the Principal it resolved and the
// policy revision it decided under, both carried into the signed context.
type ownerDecision struct {
	principalID    string
	policyRevision string
}

// ownerResponse is the owner's answer. value is a 2xx body as the domain's contract decodes it.
type ownerResponse struct {
	status int
	body   []byte
	value  any
}

type ownerFailure struct {
	status    int
	code      string
	title     string
	detail    string
	retryable bool
}

// domainOwner is what a domain contributes to CallOwner. Req is the domain's own request.
type domainOwner[Req any] struct {
	// name is how the owner appears in a public failure detail.
	name string
	// audience is the owner's audience in the signed context.
	audience string
	// decide asks the policy decision point; a denial is a failure.
	decide func(ctx context.Context, caller ownerCaller, action string, scope ownerScope) (ownerDecision, *ownerFailure)
	// serve sends the request with the signed context and returns the owner's answer unread.
	serve func(ctx context.Context, request Req, serviceContext string) (ownerResponse, *ownerFailure)
	// validate checks a 2xx answer against the public contract and decodes it. The owner is
	// trusted; this is defense in depth against a drifted or compromised owner, so a failure here
	// never reaches the caller as the owner's body.
	validate func(scope ownerScope, request Req, response ownerResponse) (any, error)
}

// CallOwner decides, signs, serves and validates one call to a domain owner.
func CallOwner[Req any](ctx context.Context, h *handler, caller ownerCaller, owner domainOwner[Req], action string, scope ownerScope, request Req) (ownerResponse, *ownerFailure) {
	serviceContext, _, failure := grantOwner(ctx, h, caller, owner, action, scope)
	if failure != nil {
		return ownerResponse{}, failure
	}
	response, failure := owner.serve(ctx, request, serviceContext)
	if failure != nil {
		return ownerResponse{}, failure
	}
	if response.status >= 200 && response.status < 300 && owner.validate != nil {
		value, err := owner.validate(scope, request, response)
		if err != nil {
			failure := ownerUnavailable(owner.name + " returned a response outside its public contract.")
			return ownerResponse{}, &failure
		}
		response.value = value
	}
	return response, nil
}

// grantOwner decides the action and signs the owner's context without calling the owner, for a
// caller that presents the context itself (the Operations Agent).
func grantOwner[Req any](ctx context.Context, h *handler, caller ownerCaller, owner domainOwner[Req], action string, scope ownerScope) (string, ownerDecision, *ownerFailure) {
	if caller.session.TenantID != scope.tenantID || !isLowerUUIDv7(scope.tenantID) {
		failure := ownerUnavailable(owner.name + " was asked for a scope outside the caller's Tenant.")
		return "", ownerDecision{}, &failure
	}
	decision, failure := owner.decide(ctx, caller, action, scope)
	if failure != nil {
		return "", ownerDecision{}, failure
	}
	serviceContext, failure := h.signOwnerContext(caller, owner.name, owner.audience, action, scope, decision)
	if failure != nil {
		return "", ownerDecision{}, failure
	}
	return serviceContext, decision, nil
}

// signOwnerContext signs the context the owner reads: the caller, the presenting workload, the one
// action and the scope it was decided for, under the decision's policy revision.
func (h *handler) signOwnerContext(caller ownerCaller, name, audience, action string, scope ownerScope, decision ownerDecision) (string, *ownerFailure) {
	if h.identity == nil || h.identity.config.DelegationSigner == nil || audience == "" || caller.presenter == "" {
		failure := ownerUnavailable(name + " context signing is unavailable.")
		return "", &failure
	}
	session := caller.session
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer,
		PrincipalID: decision.principalID, DisplayName: session.Principal.DisplayName, Email: session.Principal.Email,
		Roles: append([]string(nil), session.Principal.Roles...), ExecutingService: caller.presenter,
		Audience: audience, TenantID: session.TenantID,
		Actions: []string{action}, Scopes: scope.grants(), PolicyRevision: decision.policyRevision, SessionID: session.ID,
		IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	token, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := ownerUnavailable("The " + name + " context could not be signed.")
		return "", &failure
	}
	return token, nil
}

func ownerUnavailable(detail string) ownerFailure {
	return ownerFailure{status: http.StatusServiceUnavailable, code: "INTERNAL_ERROR", title: "Internal error", detail: detail, retryable: true}
}
