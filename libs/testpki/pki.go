package testpki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type Bundle struct {
	CAPEM          []byte
	ServerCertPEM  []byte
	ServerKeyPEM   []byte
	ClientCertPEM  []byte
	ClientKeyPEM   []byte
	ServerSPIFFEID string
	ClientSPIFFEID string
}

func Generate(serverSPIFFEID, clientSPIFFEID string, now time.Time) (Bundle, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Bundle{}, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "HVAC S0 Test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return Bundle{}, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return Bundle{}, err
	}
	serverCert, serverKey, err := issue(caCert, caKey, 2, "iam-service", serverSPIFFEID, true, now)
	if err != nil {
		return Bundle{}, err
	}
	clientCert, clientKey, err := issue(caCert, caKey, 3, "platform-gateway", clientSPIFFEID, false, now)
	if err != nil {
		return Bundle{}, err
	}
	return Bundle{
		CAPEM:          pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		ServerCertPEM:  serverCert,
		ServerKeyPEM:   serverKey,
		ClientCertPEM:  clientCert,
		ClientKeyPEM:   clientKey,
		ServerSPIFFEID: serverSPIFFEID,
		ClientSPIFFEID: clientSPIFFEID,
	}, nil
}

func issue(ca *x509.Certificate, caKey *ecdsa.PrivateKey, serial int64, commonName, spiffeID string, server bool, now time.Time) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	uri, err := url.Parse(spiffeID)
	if err != nil {
		return nil, nil, err
	}
	usage := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	if server {
		usage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  usage,
		URIs:         []*url.URL{uri},
	}
	if server {
		template.DNSNames = []string{"localhost"}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

func (bundle Bundle) ServerTLSConfig() (*tls.Config, error) {
	certificate, err := tls.X509KeyPair(bundle.ServerCertPEM, bundle.ServerKeyPEM)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(bundle.CAPEM) {
		return nil, errors.New("append test CA")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}, nil
}

func (bundle Bundle) ClientTLSConfig(serverName string) (*tls.Config, error) {
	certificate, err := tls.X509KeyPair(bundle.ClientCertPEM, bundle.ClientKeyPEM)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(bundle.CAPEM) {
		return nil, errors.New("append test CA")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      pool,
		Certificates: []tls.Certificate{certificate},
		ServerName:   serverName,
	}, nil
}

func (bundle Bundle) WriteFiles(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	files := map[string][]byte{
		"ca.pem":           bundle.CAPEM,
		"iam-cert.pem":     bundle.ServerCertPEM,
		"iam-key.pem":      bundle.ServerKeyPEM,
		"gateway-cert.pem": bundle.ClientCertPEM,
		"gateway-key.pem":  bundle.ClientKeyPEM,
	}
	for name, content := range files {
		mode := os.FileMode(0o600)
		if name == "ca.pem" || name == "iam-cert.pem" || name == "gateway-cert.pem" {
			mode = 0o644
		}
		if err := os.WriteFile(filepath.Join(directory, name), content, mode); err != nil {
			return err
		}
	}
	return nil
}
