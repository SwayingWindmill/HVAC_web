package fdd

import (
	"context"
	"crypto"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

// FDD serves only contexts the Gateway signs (#595). Before, any caller on the network was trusted
// with whatever X-Tenant-ID it sent.
const (
	ContextHeader          = "X-FDD-Context"
	ReadAction             = "fdd:read"
	WriteAction            = "fdd:write"
	DefaultAudience        = "fdd-service"
	DefaultGatewaySPIFFEID = "spiffe://hvac.local/platform-gateway"
)

type AuthConfig struct {
	GatewayPublicKey crypto.PublicKey
	GatewaySPIFFE    string
	// ReaderSPIFFE may only list a Site's findings, with a context the Gateway signed in its name
	// (the Operations Agent). Empty when no reader is deployed.
	ReaderSPIFFE string
	Audience     string
	Now          func() time.Time
}

type authorizer struct {
	publicKey crypto.PublicKey
	gateway   string
	reader    string
	audience  string
	now       func() time.Time
}

func newAuthorizer(config AuthConfig) (*authorizer, error) {
	if config.GatewayPublicKey == nil {
		return nil, errors.New("FDD requires the Gateway's delegation key")
	}
	gateway := strings.TrimSpace(config.GatewaySPIFFE)
	if gateway == "" {
		gateway = DefaultGatewaySPIFFEID
	}
	reader := strings.TrimSpace(config.ReaderSPIFFE)
	if reader != "" && (!strings.HasPrefix(reader, "spiffe://") || reader == gateway) {
		return nil, errors.New("FDD reader identity is invalid")
	}
	audience := strings.TrimSpace(config.Audience)
	if audience == "" {
		audience = DefaultAudience
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &authorizer{publicKey: config.GatewayPublicKey, gateway: gateway, reader: reader, audience: audience, now: now}, nil
}

type tenantKey struct{}

// tenantFrom is the Tenant the Gateway's context named; handlers never read it from a header.
func tenantFrom(ctx context.Context) string {
	tenant, _ := ctx.Value(tenantKey{}).(string)
	return tenant
}

// guard admits a request whose context the Gateway signed for this action, Site and the workload
// presenting it. readerAllowed marks the one route the reader workload may call.
func (auth *authorizer) guard(action string, readerAllowed bool, next func(http.ResponseWriter, *http.Request, identitycontext.DelegationClaims)) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		peer := workloadSPIFFE(request)
		if peer == "" || (peer != auth.gateway && (peer != auth.reader || !readerAllowed)) {
			writeFDDError(writer, http.StatusForbidden, "fdd_workload_forbidden")
			return
		}
		claims, err := identitycontext.VerifyDelegation(auth.publicKey, strings.TrimSpace(request.Header.Get(ContextHeader)))
		// A context covers one Tenant and one Site; which Site is checked against the request below.
		if err != nil || len(claims.Scopes) != 2 || claims.Scopes[0] != "tenant:"+claims.TenantID || !strings.HasPrefix(claims.Scopes[1], "site:") ||
			identitycontext.ValidateDelegationFromIssuerAnyScope(claims, auth.now().UTC(), auth.gateway, peer, auth.audience, action, claims.Scopes) != nil {
			writeFDDError(writer, http.StatusForbidden, "fdd_context_invalid")
			return
		}
		if siteID := request.PathValue("siteId"); siteID != "" && !coversSite(claims, siteID) {
			writeFDDError(writer, http.StatusForbidden, "fdd_context_invalid")
			return
		}
		next(writer, request.WithContext(context.WithValue(request.Context(), tenantKey{}, claims.TenantID)), claims)
	}
}

// coversSite is whether the context names exactly this Tenant and Site.
func coversSite(claims identitycontext.DelegationClaims, siteID string) bool {
	if len(claims.Scopes) != 2 {
		return false
	}
	return claims.Scopes[0] == "tenant:"+claims.TenantID && claims.Scopes[1] == "site:"+siteID
}

func workloadSPIFFE(request *http.Request) string {
	if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 {
		return ""
	}
	leaf := request.TLS.PeerCertificates[0]
	if leaf == nil || len(leaf.URIs) != 1 || leaf.URIs[0] == nil || !strings.HasPrefix(leaf.URIs[0].String(), "spiffe://") {
		return ""
	}
	return leaf.URIs[0].String()
}
