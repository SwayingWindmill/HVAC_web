package connectivity

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"regexp"
	"time"
)

var gatewayIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type CertificateAuthority struct {
	certificate    *x509.Certificate
	signer         crypto.Signer
	certificatePEM string
	brokerURL      string
}

func NewCertificateAuthority(certificatePEM, keyPEM []byte, brokerURL string) (*CertificateAuthority, error) {
	pair, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	signer, ok := pair.PrivateKey.(crypto.Signer)
	broker, parseErr := url.Parse(brokerURL)
	if !ok || !cert.IsCA || cert.KeyUsage&x509.KeyUsageCertSign == 0 || parseErr != nil || broker.Scheme != "tls" || broker.Host == "" || broker.User != nil || broker.Path != "" || broker.RawQuery != "" || broker.Fragment != "" {
		return nil, errors.New("invalid Gateway CA or broker URL")
	}
	return &CertificateAuthority{cert, signer, string(certificatePEM), brokerURL}, nil
}

func parseGatewayCSR(csrPEM string) (*x509.CertificateRequest, error) {
	block, rest := pem.Decode([]byte(csrPEM))
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(rest) != 0 {
		return nil, ErrEnrollmentRejected
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil || !gatewayIDPattern.MatchString(csr.Subject.CommonName) {
		return nil, ErrEnrollmentRejected
	}
	switch key := csr.PublicKey.(type) {
	case *rsa.PublicKey:
		if key.N.BitLen() < 2048 {
			return nil, ErrEnrollmentRejected
		}
	case *ecdsa.PublicKey:
		if key.Curve != elliptic.P256() && key.Curve != elliptic.P384() {
			return nil, ErrEnrollmentRejected
		}
	default:
		return nil, ErrEnrollmentRejected
	}
	return csr, nil
}

func (authority *CertificateAuthority) issue(gatewayID string, csr *x509.CertificateRequest, now time.Time) (IssuedCredential, *x509.Certificate, error) {
	expires := now.Add(90 * 24 * time.Hour)
	if now.Before(authority.certificate.NotBefore) || expires.After(authority.certificate.NotAfter) {
		return IssuedCredential{}, nil, errors.New("Gateway CA cannot cover the certificate lifetime")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return IssuedCredential{}, nil, err
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: gatewayID}, NotBefore: now, NotAfter: expires, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, authority.certificate, csr.PublicKey, authority.signer)
	if err != nil {
		return IssuedCredential{}, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return IssuedCredential{}, nil, err
	}
	return IssuedCredential{CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), CAPEM: authority.certificatePEM, BrokerURL: authority.brokerURL, ExpiresAt: cert.NotAfter}, cert, nil
}
