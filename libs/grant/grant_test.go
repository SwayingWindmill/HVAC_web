package grant_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/grant"
)

type claims struct {
	Version int    `json:"version"`
	Subject string `json:"subject"`
}

var format = grant.Format{Name: "fixture grant", MaxEncodedSize: 1 << 10}

func TestSignedClaimsVerifyWithRSAAndECDSAKeys(t *testing.T) {
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		token  func() (string, error)
		public any
	}{
		{"ecdsa", func() (string, error) { return grant.Sign(format, ecKey, claims{Version: 1, Subject: "user-1"}) }, &ecKey.PublicKey},
		{"rsa", func() (string, error) { return grant.Sign(format, rsaKey, claims{Version: 1, Subject: "user-1"}) }, &rsaKey.PublicKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token, err := tc.token()
			if err != nil {
				t.Fatal(err)
			}
			got, err := grant.Verify[claims](format, tc.public, token)
			if err != nil {
				t.Fatal(err)
			}
			if got != (claims{Version: 1, Subject: "user-1"}) {
				t.Fatalf("claims = %+v", got)
			}
		})
	}
}

func TestVerifyFailsClosed(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token, err := grant.Sign(format, key, claims{Version: 1, Subject: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	payload, signature, _ := strings.Cut(token, ".")
	forged, err := grant.Sign(format, other, claims{Version: 1, Subject: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	forgedPayload, _, _ := strings.Cut(forged, ".")
	_, edPublic, _ := ed25519.GenerateKey(rand.Reader)

	cases := []struct {
		name   string
		token  string
		public any
		want   string
	}{
		{"empty", "", &key.PublicKey, "fixture grant size is invalid"},
		{"oversized", strings.Repeat("a", format.MaxEncodedSize+1), &key.PublicKey, "fixture grant size is invalid"},
		{"three parts", token + ".x", &key.PublicKey, "fixture grant format is invalid"},
		{"payload encoding", "!!." + signature, &key.PublicKey, "fixture grant payload encoding is invalid"},
		{"signature encoding", payload + ".!!", &key.PublicKey, "fixture grant signature encoding is invalid"},
		{"swapped payload", forgedPayload + "." + signature, &key.PublicKey, "fixture grant signature is invalid"},
		{"wrong key", token, &other.PublicKey, "fixture grant signature is invalid"},
		{"unsupported key", token, edPublic, "fixture grant signature is invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := grant.Verify[claims](format, tc.public, tc.token)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if got != (claims{}) {
				t.Fatalf("rejected grant returned claims %+v", got)
			}
		})
	}
}

func TestSignRequiresASigner(t *testing.T) {
	if _, err := grant.Sign(format, nil, claims{}); err == nil || err.Error() != "fixture grant signer is required" {
		t.Fatalf("err = %v", err)
	}
}
