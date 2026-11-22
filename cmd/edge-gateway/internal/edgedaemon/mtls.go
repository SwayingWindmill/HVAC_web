package edgedaemon

import (
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

type tlsIdentity struct {
	Certificate tls.Certificate
	LeafDER     []byte
	Signer      crypto.Signer
	RootCAs     *x509.CertPool
	CAPEM       []byte
}

func loadTLSIdentity(caFile, certFile, keyFile string) (tlsIdentity, error) {
	caPath := strings.TrimSpace(caFile)
	if caPath == "" {
		return tlsIdentity{}, errors.New("CA certificate path is required")
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return tlsIdentity{}, fmt.Errorf("read CA certificate %s: %w", caPath, err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return tlsIdentity{}, fmt.Errorf("invalid CA PEM in %s", caPath)
	}

	certPath := strings.TrimSpace(certFile)
	keyPath := strings.TrimSpace(keyFile)
	if certPath == "" || keyPath == "" {
		return tlsIdentity{RootCAs: roots, CAPEM: caPEM}, nil
	}

	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tlsIdentity{}, fmt.Errorf("load gateway key pair: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return tlsIdentity{}, errors.New("gateway certificate contains no X.509 certificates")
	}
	signer, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		return tlsIdentity{}, errors.New("gateway private key does not implement crypto.Signer")
	}
	return tlsIdentity{
		Certificate: pair,
		LeafDER:     pair.Certificate[0],
		Signer:      signer,
		RootCAs:     roots,
		CAPEM:       caPEM,
	}, nil
}

func newMTLSClientConfig(caFile, certFile, keyFile, serverName string) (*tls.Config, tlsIdentity, error) {
	id, err := loadTLSIdentity(caFile, certFile, keyFile)
	if err != nil {
		return nil, tlsIdentity{}, err
	}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    id.RootCAs,
		ServerName: strings.TrimSpace(serverName),
	}
	if len(id.Certificate.Certificate) > 0 {
		tlsCfg.Certificates = []tls.Certificate{id.Certificate}
	}
	return tlsCfg, id, nil
}

func newMTLSHTTPClient(caFile, certFile, keyFile, serverName string, timeout time.Duration) (*http.Client, tlsIdentity, error) {
	tlsCfg, id, err := newMTLSClientConfig(caFile, certFile, keyFile, serverName)
	if err != nil {
		return nil, tlsIdentity{}, err
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}, id, nil
}

func signHTTPPoPIfPresent(req *http.Request, id tlsIdentity, now time.Time) error {
	if len(id.LeafDER) == 0 || id.Signer == nil {
		return nil
	}
	return edgecontrol.SignGatewayRequestPoP(req, id.LeafDER, id.Signer, now)
}

func writeFileAtomic(targetPath string, content []byte, mode os.FileMode) error {
	tmpPath := targetPath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(content)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return writeErr
	}
	if syncErr != nil {
		_ = os.Remove(tmpPath)
		return syncErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return closeErr
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
