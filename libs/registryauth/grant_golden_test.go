package registryauth_test

import (
	"crypto"
	"crypto/sha256"
	"io"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

// The wire format of a registry grant is shared with every running owner, so a refactor must not change a byte of it.
func TestRegistryGrantWireFormatIsStable(t *testing.T) {
	token, err := registryauth.SignGrant(goldenSigner{}, registryauth.GrantClaims{
		Issuer: "iam", Presenter: "energy-api", Audience: "registry-core",
		PrincipalID: ownerA, SubjectIssuer: "https://idp.example", Subject: "user-1", TenantID: tenantA,
		AllowedSiteIDs: []string{ownerASite1}, DeniedSiteIDs: []string{ownerASite2},
		Actions: []registryauth.Action{registryauth.ActionSiteRead}, PolicyRevision: "rev-7",
		DecisionReason: "ALLOW", SessionID: fixtureSession, ParentTokenID: fixtureParent,
		IssuedAt: 1784635200, ExpiresAt: 1784635230, TokenID: fixtureIdentifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	if token != goldenRegistryGrant {
		t.Fatalf("registry grant wire format changed:\n got %s\nwant %s", token, goldenRegistryGrant)
	}
}

const goldenRegistryGrant = "eyJ2ZXJzaW9uIjoyLCJpc3N1ZXIiOiJpYW0iLCJwcmVzZW50ZXIiOiJlbmVyZ3ktYXBpIiwiYXVkaWVuY2UiOiJyZWdpc3RyeS1jb3JlIiwicHJpbmNpcGFsSWQiOiIwMThmMWUwMC0wMDAwLTcwMDAtODAwMC0wMDAwMDAwMDAwMDEiLCJzdWJqZWN0SXNzdWVyIjoiaHR0cHM6Ly9pZHAuZXhhbXBsZSIsInN1YmplY3QiOiJ1c2VyLTEiLCJ0ZW5hbnRJZCI6IjAxOGYxZDAwLTAwMDAtNzAwMC04MDAwLTAwMDAwMDAwMDAwMSIsImFsbG93ZWRTaXRlSWRzIjpbIjAxOGYxZTAwLTEwMDAtNzAwMC04MDAwLTAwMDAwMDAwMDAwMSJdLCJkZW5pZWRTaXRlSWRzIjpbIjAxOGYxZTAwLTEwMDAtNzAwMC04MDAwLTAwMDAwMDAwMDAwMiJdLCJhY3Rpb25zIjpbInNpdGUucmVhZCJdLCJwb2xpY3lSZXZpc2lvbiI6InJldi03IiwiZGVjaXNpb25SZWFzb24iOiJBTExPVyIsInNlc3Npb25JZCI6ImZpeHR1cmUtc2Vzc2lvbiIsInBhcmVudFRva2VuSWQiOiJmaXh0dXJlLXBhcmVudCIsImlzc3VlZEF0IjoxNzg0NjM1MjAwLCJleHBpcmVzQXQiOjE3ODQ2MzUyMzAsInRva2VuSWQiOiJmaXh0dXJlLWlkZW50aWZpZXIiLCJ0cmFuc2l0aXZlIjpmYWxzZX0.c2lnOoQk91Gp99N3cQXvbedFIMq8sopGQHXP1oKaFoe_-v4g"

// goldenSigner is deterministic so the whole token, signature included, can be compared byte for byte.
type goldenSigner struct{}

func (goldenSigner) Public() crypto.PublicKey { return nil }

func (goldenSigner) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if opts.HashFunc() != crypto.SHA256 || len(digest) != sha256.Size {
		panic("grant must be signed over a SHA-256 digest")
	}
	return append([]byte("sig:"), digest...), nil
}
