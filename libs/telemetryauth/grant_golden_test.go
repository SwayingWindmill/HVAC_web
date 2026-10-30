package telemetryauth_test

import (
	"crypto"
	"crypto/sha256"
	"io"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

// The wire format of a telemetry grant is shared with every running owner, so a refactor must not change a byte of it.
func TestTelemetryGrantWireFormatIsStable(t *testing.T) {
	token, err := telemetryauth.SignGrant(goldenSigner{}, telemetryauth.GrantClaims{
		Issuer: "iam", Presenter: "energy-api", Audience: "telemetry", TenantID: "018f1d00-0000-7000-8000-000000000001",
		PrincipalID: "principal-1", SubjectIssuer: "https://idp.example", Subject: "user-1",
		ActorChain: []telemetryauth.Actor{{Service: "energy-api", SPIFFEID: "spiffe://hvac/energy-api"}},
		Action:     "telemetry.snapshot.read", ScopeDigest: "sha256:abc", TargetCount: 1, KeyCount: 2,
		PolicyRevision: "rev-7", SessionID: "session-1", ParentTokenID: "parent-1", RequestID: "request-1",
		TraceID: "trace-1", Route: "/api/v1/telemetry", IssuedAt: 1784635200, ExpiresAt: 1784635230, TokenID: "token-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if token != goldenTelemetryGrant {
		t.Fatalf("telemetry grant wire format changed:\n got %s\nwant %s", token, goldenTelemetryGrant)
	}
}

const goldenTelemetryGrant = "eyJ2ZXJzaW9uIjoxLCJpc3N1ZXIiOiJpYW0iLCJwcmVzZW50ZXIiOiJlbmVyZ3ktYXBpIiwiYXVkaWVuY2UiOiJ0ZWxlbWV0cnkiLCJ0ZW5hbnRJZCI6IjAxOGYxZDAwLTAwMDAtNzAwMC04MDAwLTAwMDAwMDAwMDAwMSIsInByaW5jaXBhbElkIjoicHJpbmNpcGFsLTEiLCJzdWJqZWN0SXNzdWVyIjoiaHR0cHM6Ly9pZHAuZXhhbXBsZSIsInN1YmplY3QiOiJ1c2VyLTEiLCJhY3RvckNoYWluIjpbeyJzZXJ2aWNlIjoiZW5lcmd5LWFwaSIsInNwaWZmZUlkIjoic3BpZmZlOi8vaHZhYy9lbmVyZ3ktYXBpIn1dLCJhY3Rpb24iOiJ0ZWxlbWV0cnkuc25hcHNob3QucmVhZCIsInNjb3BlRGlnZXN0Ijoic2hhMjU2OmFiYyIsInRhcmdldENvdW50IjoxLCJrZXlDb3VudCI6MiwicG9saWN5UmV2aXNpb24iOiJyZXYtNyIsInNlc3Npb25JZCI6InNlc3Npb24tMSIsInBhcmVudFRva2VuSWQiOiJwYXJlbnQtMSIsInJlcXVlc3RJZCI6InJlcXVlc3QtMSIsInRyYWNlSWQiOiJ0cmFjZS0xIiwicm91dGUiOiIvYXBpL3YxL3RlbGVtZXRyeSIsImlzc3VlZEF0IjoxNzg0NjM1MjAwLCJleHBpcmVzQXQiOjE3ODQ2MzUyMzAsInRva2VuSWQiOiJ0b2tlbi0xIiwidHJhbnNpdGl2ZSI6ZmFsc2V9.c2lnOnyeEqEGeopILZRprPyWebhBdZ-AYsK8Ge2XG_qhE6fu"

// goldenSigner is deterministic so the whole token, signature included, can be compared byte for byte.
type goldenSigner struct{}

func (goldenSigner) Public() crypto.PublicKey { return nil }

func (goldenSigner) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if opts.HashFunc() != crypto.SHA256 || len(digest) != sha256.Size {
		panic("grant must be signed over a SHA-256 digest")
	}
	return append([]byte("sig:"), digest...), nil
}
