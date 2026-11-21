package connectivity

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func issuePoPTestCert(t *testing.T, key any) (*x509.Certificate, []byte) {
	t.Helper()
	var pub any
	switch k := key.(type) {
	case *ecdsa.PrivateKey:
		pub = &k.PublicKey
	case *rsa.PrivateKey:
		pub = &k.PublicKey
	case ed25519.PrivateKey:
		pub = k.Public()
	default:
		t.Fatalf("unsupported key type %T", key)
	}
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "0193f000-2000-7000-8000-000000000001"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	var signer any = key
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, pub, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestVerifyGatewayRequestPoPSupportsKeyTypesAndRejectsReplaySkewPathTamper(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		signer interface {
			Public() any
		}
		key any
	}{
		{name: "ecdsa_p256", key: ecKey},
		{name: "rsa_2048", key: rsaKey},
		{name: "ed25519", key: edKey},
	} {
		_ = tc.signer
		t.Run(tc.name, func(t *testing.T) {
			cert, _ := issuePoPTestCert(t, tc.key)
			req := httptest.NewRequest(http.MethodGet, "/gateway/v1/config-shadow?sinceRevision=12", nil)
			guard := NewPoPReplayGuard(5*time.Minute, 16)

			switch k := tc.key.(type) {
			case *ecdsa.PrivateKey:
				if err := SignGatewayRequestPoP(req, cert.Raw, k, now); err != nil {
					t.Fatal(err)
				}
			case *rsa.PrivateKey:
				if err := SignGatewayRequestPoP(req, cert.Raw, k, now); err != nil {
					t.Fatal(err)
				}
			case ed25519.PrivateKey:
				if err := SignGatewayRequestPoP(req, cert.Raw, k, now); err != nil {
					t.Fatal(err)
				}
			}

			if err := VerifyGatewayRequestPoP(req, cert, guard, now); err != nil {
				t.Fatalf("VerifyGatewayRequestPoP failed: %v", err)
			}

			// Replaying identical nonce fails.
			if err := VerifyGatewayRequestPoP(req, cert, guard, now); !errors.Is(err, ErrEnrollmentRejected) {
				t.Fatalf("expected ErrEnrollmentRejected on replay, got %v", err)
			}

			// Tampering request path or method fails.
			tampered := req.Clone(t.Context())
			tampered.Method = http.MethodPost
			if err := VerifyGatewayRequestPoP(tampered, cert, NewPoPReplayGuard(5*time.Minute, 16), now); !errors.Is(err, ErrEnrollmentRejected) {
				t.Fatalf("expected ErrEnrollmentRejected on method tamper, got %v", err)
			}

			// Expired timestamp (> 300s skew) fails.
			if err := VerifyGatewayRequestPoP(req, cert, NewPoPReplayGuard(5*time.Minute, 16), now.Add(301*time.Second)); !errors.Is(err, ErrEnrollmentRejected) {
				t.Fatalf("expected ErrEnrollmentRejected on expired PoP, got %v", err)
			}
		})
	}
}

func TestParseGatewayPeerCertEnforcesPoPWhenHeadersPresentOrEnvRequired(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert, certPEM := issuePoPTestCert(t, key)

	// Without PoP headers and without CONNECTIVITY_REQUIRE_FORWARDED_CERT_POP, backward compatible.
	reqPlain := httptest.NewRequest(http.MethodPost, "/gateway/v1/renew", nil)
	reqPlain.Header.Set("X-Gateway-Client-Cert", url.QueryEscape(string(certPEM)))
	if _, err := ParseGatewayPeerCert(reqPlain); err != nil {
		t.Fatalf("expected compatibility without PoP headers, got %v", err)
	}

	// Partial or invalid PoP header triggers immediate rejection.
	reqPartial := reqPlain.Clone(t.Context())
	reqPartial.Header.Set(HeaderGatewayCertPopTimestamp, "1700000000")
	if _, err := ParseGatewayPeerCert(reqPartial); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("expected ErrEnrollmentRejected on partial PoP header, got %v", err)
	}

	// Valid PoP passes and replay is blocked.
	reqSigned := reqPlain.Clone(t.Context())
	if err := SignGatewayRequestPoP(reqSigned, cert.Raw, key, now); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseGatewayPeerCert(reqSigned); err != nil {
		t.Fatalf("expected signed PoP to pass, got %v", err)
	}
	if _, err := ParseGatewayPeerCert(reqSigned); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("expected replayed signed PoP to fail, got %v", err)
	}

	// Global requirement rejects unsigned requests.
	t.Setenv(popRequireEnvVarName, "true")
	if _, err := ParseGatewayPeerCert(reqPlain); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("expected ErrEnrollmentRejected when PoP required globally, got %v", err)
	}
}
