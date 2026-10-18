package identitycontext

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	// ErrSiteContextWorkload: the request came from no workload, or one this route does not serve.
	ErrSiteContextWorkload = errors.New("the presenting workload is not allowed")
	// ErrSiteContextInvalid: the request carries no context the Gateway signed for this route.
	ErrSiteContextInvalid = errors.New("the site context is invalid")
)

// SiteContextVerifier admits the requests an owner that serves only Gateway-signed contexts
// receives (#595, #612): over mutual TLS from an allowed workload, each carrying a context the
// Gateway signed for one action on one Tenant and one Site, naming that workload as its presenter.
type SiteContextVerifier struct {
	PublicKey crypto.PublicKey
	// Issuer is the Gateway's workload identity, which signs every context.
	Issuer   string
	Audience string
	Header   string
	Now      func() time.Time
}

// SiteContext is what a verified request may act on. The Tenant comes only from here, never from
// a header the caller writes.
type SiteContext struct {
	TenantID string
	SiteID   string
	// Subject is the person the Gateway signed the context for.
	Subject string
}

// Verify admits a request whose context covers action and names the peer, which must be one of
// presenters. A route with a {siteId} must be that Site; a Site named in the body is the
// caller's to compare with SiteID.
func (verifier SiteContextVerifier) Verify(request *http.Request, action string, presenters ...string) (SiteContext, error) {
	peer := PeerSPIFFE(request)
	if peer == "" || !containsExact(presenters, peer) {
		return SiteContext{}, ErrSiteContextWorkload
	}
	claims, err := VerifyDelegation(verifier.PublicKey, strings.TrimSpace(request.Header.Get(verifier.Header)))
	// A context names one Tenant and one Site, in that order.
	if err != nil || len(claims.Scopes) != 2 || !strings.HasPrefix(claims.Scopes[1], "site:") ||
		ValidateDelegationFromIssuerAnyScope(claims, verifier.now(), verifier.Issuer, peer, verifier.Audience, action, []string{"tenant:" + claims.TenantID, claims.Scopes[1]}) != nil {
		return SiteContext{}, ErrSiteContextInvalid
	}
	site := SiteContext{TenantID: claims.TenantID, SiteID: strings.TrimPrefix(claims.Scopes[1], "site:"), Subject: claims.Subject}
	if siteID := request.PathValue("siteId"); siteID != "" && siteID != site.SiteID {
		return SiteContext{}, ErrSiteContextInvalid
	}
	return site, nil
}

func (verifier SiteContextVerifier) now() time.Time {
	if verifier.Now == nil {
		return time.Now().UTC()
	}
	return verifier.Now().UTC()
}

// Validate reports a verifier that could admit nothing, so an owner fails at start.
func (verifier SiteContextVerifier) Validate() error {
	if verifier.PublicKey == nil {
		return errors.New("the Gateway's delegation key is required")
	}
	if !strings.HasPrefix(verifier.Issuer, "spiffe://") {
		return errors.New("the Gateway's workload identity is required")
	}
	if verifier.Audience == "" || verifier.Header == "" {
		return errors.New("the context audience and header are required")
	}
	return nil
}

// PeerSPIFFE is the SPIFFE ID of the workload certificate the client presented, or empty.
func PeerSPIFFE(request *http.Request) string {
	if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 {
		return ""
	}
	leaf := request.TLS.PeerCertificates[0]
	if leaf == nil || len(leaf.URIs) != 1 || leaf.URIs[0] == nil || !strings.HasPrefix(leaf.URIs[0].String(), "spiffe://") {
		return ""
	}
	return leaf.URIs[0].String()
}

// LoadCertificatePublicKey reads the Gateway's delegation key from its certificate.
func LoadCertificatePublicKey(path string) (crypto.PublicKey, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("public key certificate is invalid")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	return certificate.PublicKey, nil
}
