package simulator

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// EnsureGatewayCredential enrolls once, or renews a current identity within its
// final fourteen days. The private key is generated and retained on the Gateway.
func EnsureGatewayCredential(ctx context.Context, config *MQTTGatewayConfig, code string) error {
	origin, err := url.Parse(config.EnrollmentURL)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" {
		return errors.New("Gateway enrollment URL must be an https:// origin")
	}
	identity, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
	renew := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read Gateway identity: %w", err)
	}
	if renew {
		leaf, err := x509.ParseCertificate(identity.Certificate[0])
		if err != nil {
			return err
		}
		if leaf.Subject.CommonName != config.GatewayID {
			return errors.New("stored Gateway identity does not match the configured Gateway")
		}
		if time.Until(leaf.NotAfter) > 14*24*time.Hour {
			return nil
		}
		if !time.Now().Before(leaf.NotAfter) {
			return errors.New("Gateway identity expired; register a replacement Gateway")
		}
	} else if code == "" {
		return errors.New("Gateway enrollment code is required for initial enrollment")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: config.GatewayID}}, key)
	if err != nil {
		return err
	}
	input := map[string]string{"csrPem": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}))}
	endpoint := config.EnrollmentURL + "/gateway/v1/enroll"
	if renew {
		endpoint = config.EnrollmentURL + "/gateway/v1/renew"
	} else {
		input["enrollmentCode"] = code
	}
	ca, err := os.ReadFile(config.EnrollmentCAFile)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return errors.New("Gateway enrollment server CA is invalid")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: config.EnrollmentServerName}
	if renew {
		tlsConfig.Certificates = []tls.Certificate{identity}
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Gateway credential request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Gateway credential request rejected (HTTP %d)", response.StatusCode)
	}
	var issued struct {
		CertificatePEM string    `json:"certificatePem"`
		CAPEM          string    `json:"caPem"`
		BrokerURL      string    `json:"brokerUrl"`
		ExpiresAt      time.Time `json:"expiresAt"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 32<<10)).Decode(&issued); err != nil {
		return err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	pair, err := tls.X509KeyPair([]byte(issued.CertificatePEM), privatePEM)
	if err != nil {
		return fmt.Errorf("invalid issued Gateway identity: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	issuer := x509.NewCertPool()
	if !issuer.AppendCertsFromPEM([]byte(issued.CAPEM)) {
		return errors.New("issued Gateway CA is invalid")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: issuer, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return err
	}
	if leaf.Subject.CommonName != config.GatewayID {
		return errors.New("issued Gateway identity does not match the registered Gateway")
	}
	updated := *config
	updated.BrokerURL = issued.BrokerURL
	if err := updated.Validate(); err != nil {
		return err
	}
	if config.CertFile != config.KeyFile {
		return errors.New("enrolled Gateway identity requires one atomic PEM bundle")
	}
	if err := os.MkdirAll(filepath.Dir(config.CertFile), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(config.CertFile), ".gateway-identity-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(privatePEM, []byte(issued.CertificatePEM)...)); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), config.CertFile); err != nil {
		return err
	}
	config.BrokerURL = issued.BrokerURL
	return nil
}
