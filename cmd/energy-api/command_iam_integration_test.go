package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/testpki"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
	"github.com/quanlaihe/hvac-web/modules/iam/pkg/iamserver"
)

type staticCommandIAMTestStore struct {
	facts  iamserver.AuthorizationFacts
	siteID string
}

func (s staticCommandIAMTestStore) LookupRegistryAuthorization(_ context.Context, lookup iamserver.AuthorizationLookup) (iamserver.AuthorizationFacts, error) {
	if lookup.SubjectIssuer != s.facts.Principal.SubjectIssuer || lookup.Subject != s.facts.Principal.Subject {
		return iamserver.AuthorizationFacts{PolicyRevision: s.facts.PolicyRevision}, nil
	}
	return s.facts, nil
}

func (s staticCommandIAMTestStore) LookupCommandAuthorization(_ context.Context, lookup iamserver.CommandAuthorizationLookup) (iamserver.CommandAuthorizationFacts, error) {
	if lookup.SubjectIssuer != s.facts.Principal.SubjectIssuer || lookup.Subject != s.facts.Principal.Subject {
		return iamserver.CommandAuthorizationFacts{
			AuthorizationFacts:          iamserver.AuthorizationFacts{PolicyRevision: s.facts.PolicyRevision},
			EmergencyRevocationRevision: 1,
		}, nil
	}
	return iamserver.CommandAuthorizationFacts{
		AuthorizationFacts:          s.facts,
		EmergencyRevocationRevision: 1,
		DeviceSiteID:                s.siteID,
	}, nil
}

func TestCommandHTTPAdaptersAuthorizeRegistryAndCommandAgainstRealIAM(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	const (
		iamSPIFFEID          = "spiffe://hvac.local/iam-service"
		gatewaySPIFFEID      = "spiffe://hvac.local/platform-gateway"
		commandServiceSPIFFE = "spiffe://hvac.local/command-service"
		tenantID             = "018f1d00-0000-7000-8000-000000000001"
		siteID               = "018f1d00-0000-7000-8000-000000000002"
		deviceID             = "018f1d00-0000-7000-8000-000000000003"
		principalID          = "018f1d00-0000-7000-8000-000000000004"
		sessionID            = "018f1d00-0000-7000-8000-000000000005"
		tokenID              = "018f1d00-0000-7000-8000-000000000006"
		subjectIssuer        = "https://oidc.example.test"
		subject              = "operator-sub-1"
	)

	bundle, err := testpki.Generate(iamSPIFFEID, commandServiceSPIFFE, now)
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, err := bundle.ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	clientTLS, err := bundle.ClientTLSConfig("localhost")
	if err != nil {
		t.Fatal(err)
	}
	clientTransport := &http.Transport{TLSClientConfig: clientTLS}
	t.Cleanup(clientTransport.CloseIdleConnections)
	clientHTTP := &http.Client{Timeout: 5 * time.Second, Transport: clientTransport}
	clientPair, err := tls.X509KeyPair(bundle.ClientCertPEM, bundle.ClientKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	delegationSigner, ok := clientPair.PrivateKey.(crypto.Signer)
	if !ok {
		t.Fatal("command service client key is not a signer")
	}

	iamGrantKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	store := staticCommandIAMTestStore{
		siteID: siteID,
		facts: iamserver.AuthorizationFacts{
			Found:          true,
			PolicyRevision: "policy-v1",
			Principal: iamserver.PrincipalRecord{
				ID:            principalID,
				SubjectIssuer: subjectIssuer,
				Subject:       subject,
				DisplayName:   "Operator One",
				Status:        iamserver.FactStatusActive,
			},
			TenantSiteIDs: []string{siteID},
			Memberships: []iamserver.TenantMembership{{
				TenantID:     tenantID,
				TenantStatus: iamserver.FactStatusActive,
				Status:       iamserver.FactStatusActive,
				ValidFrom:    now.Add(-time.Hour),
			}},
			Assignments: []iamserver.RoleAssignment{{
				TenantID:     tenantID,
				SiteID:       siteID,
				RoleKey:      "operator",
				Capabilities: []string{commandservice.ActionSiteRead, "command.submit.low"},
				Status:       iamserver.FactStatusActive,
				ValidFrom:    now.Add(-time.Hour),
			}},
		},
	}

	iamHandler := iamserver.NewHandler(iamserver.Config{
		AllowedWorkloadSPIFFE:     gatewaySPIFFEID,
		CommandAuthoritySPIFFE:    commandServiceSPIFFE,
		Audience:                  "iam-service",
		Now:                       func() time.Time { return now },
		AuthorizationStore:        store,
		RegistryGrantSigner:       iamGrantKey,
		RegistryGrantIssuer:       iamSPIFFEID,
		RegistryGrantAudience:     "platform-core-service",
		CommandAuthorizationStore: store,
		CommandGrantSigner:        iamGrantKey,
		CommandGrantIssuer:        iamSPIFFEID,
		CommandGrantAudience:      "command-service",
	})
	iamServer := httptest.NewUnstartedServer(iamHandler)
	iamServer.TLS = serverTLS
	iamServer.StartTLS()
	t.Cleanup(iamServer.Close)

	adapters, err := commandservice.NewHTTPAdapters(commandservice.HTTPClientsConfig{
		Now:                   func() time.Time { return now },
		CommandServiceSPIFFE:  commandServiceSPIFFE,
		DelegationSigner:      delegationSigner,
		PolicyRevision:        "policy-v1",
		IAMBaseURL:            iamServer.URL,
		IAMAudience:           "iam-service",
		IAMHTTPClient:         clientHTTP,
		CommandGrantPublicKey: &iamGrantKey.PublicKey,
		Timeout:               5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	operator := commandservice.OperatorIdentity{
		TenantID:       tenantID,
		Subject:        subject,
		SubjectIssuer:  subjectIssuer,
		DisplayName:    "Operator One",
		Email:          "operator@example.test",
		Roles:          []string{"operator"},
		SessionID:      sessionID,
		TokenID:        tokenID,
		PolicyRevision: "policy-v1",
		IssuedAt:       now,
		ExpiresAt:      now.Add(5 * time.Minute),
	}

	regDecision, err := adapters.AuthorizeRegistry(t.Context(), operator, commandservice.ActionSiteRead)
	if err != nil {
		t.Fatalf("AuthorizeRegistry() error = %v", err)
	}
	if !regDecision.Allowed || regDecision.Grant == "" {
		t.Fatalf("AuthorizeRegistry() denied or missing grant: %+v", regDecision)
	}
	regGrant, err := registryauth.VerifyGrant(&iamGrantKey.PublicKey, regDecision.Grant)
	if err != nil {
		t.Fatalf("registryauth.VerifyGrant() error = %v", err)
	}
	if regGrant.Presenter != commandServiceSPIFFE {
		t.Fatalf("registry grant presenter=%q, want %q", regGrant.Presenter, commandServiceSPIFFE)
	}

	profile, ok := commandmodel.CapabilityProfileFor(commandmodel.CapabilitySetTemperatureSetpoint)
	if !ok {
		t.Fatal("missing setpoint capability profile")
	}
	var cmdClaims commandauth.GrantClaims
	var cmdGrantToken string
	cmdGrantToken, cmdClaims, err = adapters.AuthorizeCommand(
		t.Context(),
		operator,
		commandmodel.AuthorizationCommandSubmit,
		siteID,
		deviceID,
		commandmodel.CapabilitySetTemperatureSetpoint,
		profile.Revision,
	)
	if err != nil {
		t.Fatalf("AuthorizeCommand() error = %v", err)
	}
	if cmdGrantToken == "" || cmdClaims.Presenter != commandServiceSPIFFE || cmdClaims.SiteID != siteID || cmdClaims.DeviceID != deviceID {
		t.Fatalf("unexpected command grant claims: %+v tokenPresent=%v", cmdClaims, cmdGrantToken != "")
	}
}
