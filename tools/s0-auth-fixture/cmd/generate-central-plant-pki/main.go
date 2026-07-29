package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type identity struct {
	name       string
	commonName string
	spiffeID   string
	dnsNames   []string
	server     bool
	client     bool
}

func main() {
	if len(os.Args) != 2 {
		_, _ = os.Stderr.WriteString("usage: generate-central-plant-pki <output-directory>\n")
		os.Exit(2)
	}
	if err := generate(os.Args[1], time.Now().UTC()); err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	fmt.Println(os.Args[1])
}

func generate(directory string, now time.Time) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "HVAC Central Plant Local CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(7 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		return err
	}
	if err := writePEM(filepath.Join(directory, "ca.pem"), "CERTIFICATE", caDER, 0o644); err != nil {
		return err
	}

	identities := []identity{
		{name: "oidc", commonName: "localhost", spiffeID: "spiffe://hvac.local/oidc-test-provider", dnsNames: []string{"localhost", "oidc"}, server: true},
		{name: "iam", commonName: "localhost", spiffeID: "spiffe://hvac.local/iam-service", dnsNames: []string{"localhost", "iam"}, server: true},
		{name: "core", commonName: "localhost", spiffeID: "spiffe://hvac.local/platform-core-service", dnsNames: []string{"localhost", "platform-core"}, server: true, client: true},
		{name: "telemetry", commonName: "localhost", spiffeID: "spiffe://hvac.local/telemetry-runtime-service", dnsNames: []string{"localhost", "telemetry-runtime"}, server: true, client: true},
		{name: "gateway", commonName: "platform-gateway", spiffeID: "spiffe://hvac.local/platform-gateway", client: true},
		{name: "adapter", commonName: "thingsboard-telemetry-adapter", spiffeID: "spiffe://hvac.local/thingsboard-telemetry-adapter", client: true},
		{name: "centrifugo", commonName: "centrifugo", spiffeID: "spiffe://hvac.local/centrifugo", client: true},
		{name: "web", commonName: "localhost", spiffeID: "spiffe://hvac.local/hvac-web", dnsNames: []string{"localhost"}, server: true},
	}
	for index, definition := range identities {
		certificate, key, err := issue(caCertificate, caKey, int64(index+2), definition, now)
		if err != nil {
			return err
		}
		if err := writePEM(filepath.Join(directory, definition.name+"-cert.pem"), "CERTIFICATE", certificate, 0o644); err != nil {
			return err
		}
		if err := writePEM(filepath.Join(directory, definition.name+"-key.pem"), "PRIVATE KEY", key, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func issue(ca *x509.Certificate, caKey *ecdsa.PrivateKey, serial int64, definition identity, now time.Time) ([]byte, []byte, error) {
	if !definition.server && !definition.client {
		return nil, nil, errors.New("certificate must have at least one extended key usage")
	}
	spiffeURI, err := url.Parse(definition.spiffeID)
	if err != nil || spiffeURI.Scheme != "spiffe" {
		return nil, nil, errors.New("invalid SPIFFE URI")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	usages := make([]x509.ExtKeyUsage, 0, 2)
	if definition.server {
		usages = append(usages, x509.ExtKeyUsageServerAuth)
	}
	if definition.client {
		usages = append(usages, x509.ExtKeyUsageClientAuth)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: definition.commonName},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(7 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  usages,
		URIs:         []*url.URL{spiffeURI},
	}
	if definition.server {
		template.DNSNames = append([]string(nil), definition.dnsNames...)
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return certificateDER, keyDER, nil
}

func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	content := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if len(content) == 0 {
		return errors.New("encode PEM")
	}
	return os.WriteFile(path, content, mode)
}
