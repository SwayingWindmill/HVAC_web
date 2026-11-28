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
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

var ErrCRLSignUnsupported = errors.New("gateway CA lacks CRLSign key usage")

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

func parseAndValidateCSR(csrPEM string) (*x509.CertificateRequest, error) {
	block, rest := pem.Decode([]byte(csrPEM))
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(rest) != 0 {
		return nil, ErrEnrollmentRejected
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
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

func parseGatewayCSR(csrPEM string) (*x509.CertificateRequest, error) {
	csr, err := parseAndValidateCSR(csrPEM)
	if err != nil || !uuidv7.IsValid(csr.Subject.CommonName) {
		return nil, ErrEnrollmentRejected
	}
	return csr, nil
}

func parseJITPCSR(csrPEM, hardwareSerial, deviceID string) (*x509.CertificateRequest, error) {
	csr, err := parseAndValidateCSR(csrPEM)
	if err != nil {
		return nil, ErrEnrollmentRejected
	}
	cn := strings.TrimSpace(csr.Subject.CommonName)
	if cn == "" || (cn != hardwareSerial && !strings.EqualFold(cn, deviceID)) {
		return nil, ErrEnrollmentRejected
	}
	return csr, nil
}

func (authority *CertificateAuthority) issue(gatewayID string, csr *x509.CertificateRequest, now time.Time) (IssuedCredential, *x509.Certificate, error) {
	expires := now.Add(90 * 24 * time.Hour)
	if now.Before(authority.certificate.NotBefore) || expires.After(authority.certificate.NotAfter) {
		return IssuedCredential{}, nil, errors.New("gateway CA cannot cover the certificate lifetime")
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
	return IssuedCredential{
		SerialNumber:   strings.ToLower(cert.SerialNumber.Text(16)),
		CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		CAPEM:          authority.certificatePEM,
		BrokerURL:      authority.brokerURL,
		ExpiresAt:      cert.NotAfter,
	}, cert, nil
}

// IssueEphemeralGatewayTLS generates an ephemeral ECDSA P-256 client certificate signed
// by the Gateway CA with CommonName set to gatewayID for short-lived upstream mTLS
// forwarding to the MQTT broker.
func (authority *CertificateAuthority) IssueEphemeralGatewayTLS(gatewayID string, ttl time.Duration, now time.Time) (tls.Certificate, error) {
	if authority == nil || authority.certificate == nil || authority.signer == nil {
		return tls.Certificate{}, errors.New("gateway CA is not initialized")
	}
	if !authority.certificate.IsCA || authority.certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
		return tls.Certificate{}, errors.New("gateway CA lacks certificate signing capability")
	}
	normalizedID := strings.ToLower(strings.TrimSpace(gatewayID))
	if !uuidv7.IsValid(normalizedID) {
		return tls.Certificate{}, ErrEnrollmentRejected
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	issuedAt := now.UTC()
	if issuedAt.IsZero() {
		issuedAt = time.Now().UTC()
	}
	if issuedAt.Before(authority.certificate.NotBefore) || !issuedAt.Before(authority.certificate.NotAfter) {
		return tls.Certificate{}, errors.New("gateway CA is outside its validity window")
	}
	notBefore := issuedAt.Add(-time.Minute)
	if notBefore.Before(authority.certificate.NotBefore) {
		notBefore = authority.certificate.NotBefore
	}
	notAfter := issuedAt.Add(ttl)
	if notAfter.After(authority.certificate.NotAfter) {
		notAfter = authority.certificate.NotAfter
	}
	if !notAfter.After(notBefore) {
		return tls.Certificate{}, errors.New("gateway CA validity window exhausted")
	}
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: normalizedID},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, authority.certificate, &privKey.PublicKey, authority.signer)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  privKey,
		Leaf:        leaf,
	}, nil
}

// CreateCRL signs an RFC 5280 CRL (PEM-encoded) containing the supplied revoked serials.
func (authority *CertificateAuthority) CreateCRL(entries []pkix.RevokedCertificate, now time.Time) ([]byte, error) {
	if authority == nil || authority.certificate == nil || authority.signer == nil {
		return nil, errors.New("gateway CA is not initialized")
	}
	if authority.certificate.KeyUsage&x509.KeyUsageCRLSign == 0 {
		return nil, ErrCRLSignUnsupported
	}
	issuedAt := now.UTC()
	revoked := make([]x509.RevocationListEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.SerialNumber == nil || entry.SerialNumber.Sign() <= 0 {
			continue
		}
		revokedAt := entry.RevocationTime.UTC()
		if revokedAt.IsZero() {
			revokedAt = issuedAt
		}
		revoked = append(revoked, x509.RevocationListEntry{
			SerialNumber:   entry.SerialNumber,
			RevocationTime: revokedAt,
		})
	}
	der, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number:                    big.NewInt(max(1, issuedAt.UnixMilli())),
		ThisUpdate:                issuedAt,
		NextUpdate:                issuedAt.Add(24 * time.Hour),
		RevokedCertificateEntries: revoked,
	}, authority.certificate, authority.signer)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: der}), nil
}
