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
	Audience               = "fdd-service"
	DefaultGatewaySPIFFEID = "spiffe://hvac.local/platform-gateway"
)

type AuthConfig struct {
	GatewayPublicKey crypto.PublicKey
	GatewaySPIFFE    string
	// ReaderSPIFFE may only list a Site's findings, with a context the Gateway signed in its name
	// (the Operations Agent). Empty when no reader is deployed.
	ReaderSPIFFE string
	Now          func() time.Time
}

type authorizer struct {
	verifier identitycontext.SiteContextVerifier
	gateway  string
	reader   string
}

func newAuthorizer(config AuthConfig) (*authorizer, error) {
	gateway := strings.TrimSpace(config.GatewaySPIFFE)
	verifier := identitycontext.SiteContextVerifier{PublicKey: config.GatewayPublicKey, Issuer: gateway, Audience: Audience, Header: ContextHeader, Now: config.Now}
	if err := verifier.Validate(); err != nil {
		return nil, err
	}
	reader := strings.TrimSpace(config.ReaderSPIFFE)
	if reader != "" && (!strings.HasPrefix(reader, "spiffe://") || reader == gateway) {
		return nil, errors.New("FDD reader identity is invalid")
	}
	return &authorizer{verifier: verifier, gateway: gateway, reader: reader}, nil
}

type tenantKey struct{}

// tenantFrom is the Tenant the Gateway's context named; handlers never read it from a header.
func tenantFrom(ctx context.Context) string {
	tenant, _ := ctx.Value(tenantKey{}).(string)
	return tenant
}

// guard admits a request whose context the Gateway signed for this action, Site and the workload
// presenting it. readerAllowed marks the one route the reader workload may call.
func (auth *authorizer) guard(action string, readerAllowed bool, next func(http.ResponseWriter, *http.Request, identitycontext.SiteContext)) http.HandlerFunc {
	presenters := []string{auth.gateway}
	if readerAllowed && auth.reader != "" {
		presenters = append(presenters, auth.reader)
	}
	return func(writer http.ResponseWriter, request *http.Request) {
		site, err := auth.verifier.Verify(request, action, presenters...)
		if errors.Is(err, identitycontext.ErrSiteContextWorkload) {
			writeFDDError(writer, http.StatusForbidden, "fdd_workload_forbidden")
			return
		}
		if err != nil {
			writeFDDError(writer, http.StatusForbidden, "fdd_context_invalid")
			return
		}
		next(writer, request.WithContext(context.WithValue(request.Context(), tenantKey{}, site.TenantID)), site)
	}
}
