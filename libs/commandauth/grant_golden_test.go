package commandauth_test

import (
	"crypto"
	"crypto/sha256"
	"io"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

// The wire format of a command grant is shared with every running owner, so a refactor must not change a byte of it.
func TestCommandGrantWireFormatIsStable(t *testing.T) {
	token, err := commandauth.SignGrant(goldenSigner{}, commandauth.GrantClaims{
		Issuer: "iam", Presenter: "energy-api", Audience: "command", GrantID: "grant-1", Purpose: "SUBMIT",
		PrincipalID: "principal-1", TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1",
		Capability: commandmodel.CapabilityStart, MaximumRisk: commandmodel.RiskLow, CapabilityRevision: "cap-3",
		PolicyRevision: "rev-7", EmergencyRevocationRevision: 4, IssuedAt: 1784635200, ExpiresAt: 1784635230, TokenID: "token-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if token != goldenCommandGrant {
		t.Fatalf("command grant wire format changed:\n got %s\nwant %s", token, goldenCommandGrant)
	}
}

const goldenCommandGrant = "eyJ2ZXJzaW9uIjoxLCJpc3N1ZXIiOiJpYW0iLCJwcmVzZW50ZXIiOiJlbmVyZ3ktYXBpIiwiYXVkaWVuY2UiOiJjb21tYW5kIiwiZ3JhbnRJZCI6ImdyYW50LTEiLCJwdXJwb3NlIjoiU1VCTUlUIiwicHJpbmNpcGFsSWQiOiJwcmluY2lwYWwtMSIsInRlbmFudElkIjoidGVuYW50LTEiLCJzaXRlSWQiOiJzaXRlLTEiLCJkZXZpY2VJZCI6ImRldmljZS0xIiwiY2FwYWJpbGl0eSI6IlNUQVJUIiwibWF4aW11bVJpc2siOiJMT1ciLCJjYXBhYmlsaXR5UmV2aXNpb24iOiJjYXAtMyIsInBvbGljeVJldmlzaW9uIjoicmV2LTciLCJlbWVyZ2VuY3lSZXZvY2F0aW9uUmV2aXNpb24iOjQsImlzc3VlZEF0IjoxNzg0NjM1MjAwLCJleHBpcmVzQXQiOjE3ODQ2MzUyMzAsInRva2VuSWQiOiJ0b2tlbi0xIiwidHJhbnNpdGl2ZSI6ZmFsc2V9.c2lnOgQIRXEUQVhtbVNWt8FK5gJEJ4fbZ45LzyAN9ihvTwE5"

// goldenSigner is deterministic so the whole token, signature included, can be compared byte for byte.
type goldenSigner struct{}

func (goldenSigner) Public() crypto.PublicKey { return nil }

func (goldenSigner) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if opts.HashFunc() != crypto.SHA256 || len(digest) != sha256.Size {
		panic("grant must be signed over a SHA-256 digest")
	}
	return append([]byte("sig:"), digest...), nil
}
