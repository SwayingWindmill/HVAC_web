package identitycontext_test

import (
	"crypto"
	"crypto/sha256"
	"io"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

// The wire format of a delegation grant is shared with every running owner, so a refactor must not change a byte of it.
func TestDelegationGrantWireFormatIsStable(t *testing.T) {
	token, err := identitycontext.SignDelegation(goldenSigner{}, identitycontext.DelegationClaims{
		Issuer: "energy-api", Subject: "user-1", SubjectIssuer: "https://idp.example", PrincipalID: "principal-1",
		DisplayName: "值班员", Email: "duty@example.com", Roles: []string{"operator"}, ExecutingService: "energy-api",
		Audience: "alarm", TenantID: "tenant-1", Actions: []string{"alarm.read"}, Scopes: []string{"site:site-1"},
		PolicyRevision: "rev-7", SessionID: "session-1", IssuedAt: 1784635200, ExpiresAt: 1784635230, TokenID: "token-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if token != goldenDelegationGrant {
		t.Fatalf("delegation grant wire format changed:\n got %s\nwant %s", token, goldenDelegationGrant)
	}
}

const goldenDelegationGrant = "eyJ2ZXJzaW9uIjoxLCJpc3N1ZXIiOiJlbmVyZ3ktYXBpIiwic3ViamVjdCI6InVzZXItMSIsInN1YmplY3RJc3N1ZXIiOiJodHRwczovL2lkcC5leGFtcGxlIiwicHJpbmNpcGFsSWQiOiJwcmluY2lwYWwtMSIsImRpc3BsYXlOYW1lIjoi5YC854-t5ZGYIiwiZW1haWwiOiJkdXR5QGV4YW1wbGUuY29tIiwicm9sZXMiOlsib3BlcmF0b3IiXSwiZXhlY3V0aW5nU2VydmljZSI6ImVuZXJneS1hcGkiLCJhdWRpZW5jZSI6ImFsYXJtIiwidGVuYW50SWQiOiJ0ZW5hbnQtMSIsImFjdGlvbnMiOlsiYWxhcm0ucmVhZCJdLCJzY29wZXMiOlsic2l0ZTpzaXRlLTEiXSwicG9saWN5UmV2aXNpb24iOiJyZXYtNyIsInNlc3Npb25JZCI6InNlc3Npb24tMSIsImlzc3VlZEF0IjoxNzg0NjM1MjAwLCJleHBpcmVzQXQiOjE3ODQ2MzUyMzAsInRva2VuSWQiOiJ0b2tlbi0xIn0.c2lnOmbJUFZjPBsE1ZAQx0myAlP8ftFj1GkyFuTdbwjTNAV_"

// goldenSigner is deterministic so the whole token, signature included, can be compared byte for byte.
type goldenSigner struct{}

func (goldenSigner) Public() crypto.PublicKey { return nil }

func (goldenSigner) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if opts.HashFunc() != crypto.SHA256 || len(digest) != sha256.Size {
		panic("grant must be signed over a SHA-256 digest")
	}
	return append([]byte("sig:"), digest...), nil
}
