package edgedaemon

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const maxRenewResponseBytes = 512 << 10

// CertRenewOptions configures 14-day automatic mTLS gateway certificate renewal.
type CertRenewOptions struct {
	PlatformBaseURL string
	GatewayID       string
	ServerName      string
	CertFile        string
	KeyFile         string
	CAFile          string
	ThresholdDays   int
	Now             func() time.Time
	OnRenewed       func(CertRenewResult)
}

// CertRenewResult describes the outcome of a certificate expiration check/renewal pass.
type CertRenewResult struct {
	Renewed   bool
	Reason    string
	GatewayID string
	ExpiresAt time.Time
}

// CheckAndRenewCertificate inspects CertFile and renews it against /gateway/v1/renew if within ThresholdDays.
func CheckAndRenewCertificate(ctx context.Context, opts CertRenewOptions) (CertRenewResult, error) {
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().UTC()
	thresholdDays := opts.ThresholdDays
	if thresholdDays <= 0 {
		thresholdDays = defaultCertRenewThresholdDays
	}

	certPEM, err := os.ReadFile(strings.TrimSpace(opts.CertFile))
	if err != nil {
		return CertRenewResult{}, fmt.Errorf("read gateway cert %s: %w", opts.CertFile, err)
	}
	leaf, err := parseSinglePEMCert(certPEM)
	if err != nil {
		return CertRenewResult{}, fmt.Errorf("parse gateway cert: %w", err)
	}
	gatewayID := strings.TrimSpace(leaf.Subject.CommonName)
	if want := strings.TrimSpace(opts.GatewayID); want != "" && gatewayID != want {
		return CertRenewResult{}, fmt.Errorf("gateway cert CN %q does not match configured gatewayId %q", gatewayID, want)
	}

	remaining := leaf.NotAfter.Sub(now)
	if remaining > time.Duration(thresholdDays)*24*time.Hour {
		return CertRenewResult{
			Renewed:   false,
			Reason:    "valid_beyond_threshold",
			GatewayID: gatewayID,
			ExpiresAt: leaf.NotAfter,
		}, nil
	}

	client, id, err := newMTLSHTTPClient(opts.CAFile, opts.CertFile, opts.KeyFile, opts.ServerName, 20*time.Second)
	if err != nil {
		return CertRenewResult{}, err
	}
	csrPEM, expectedPubDER, err := createPKCS10CSRPEM(gatewayID, id.Signer)
	if err != nil {
		return CertRenewResult{}, err
	}

	body, err := json.Marshal(map[string]any{"csrPem": string(csrPEM)})
	if err != nil {
		return CertRenewResult{}, err
	}
	renewURL := strings.TrimRight(strings.TrimSpace(opts.PlatformBaseURL), "/") + "/gateway/v1/renew"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, renewURL, bytes.NewReader(body))
	if err != nil {
		return CertRenewResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := signHTTPPoPIfPresent(req, id, now); err != nil {
		return CertRenewResult{}, fmt.Errorf("sign renew PoP: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return CertRenewResult{}, fmt.Errorf("request gateway certificate renew: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxRenewResponseBytes))
	if err != nil {
		return CertRenewResult{}, fmt.Errorf("read renew response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CertRenewResult{}, fmt.Errorf("gateway certificate renew failed with HTTP %d", resp.StatusCode)
	}

	var parsed struct {
		CertificatePEM string `json:"certificatePem"`
		CAPEM          string `json:"caPem"`
		ExpiresAt      string `json:"expiresAt"`
	}
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return CertRenewResult{}, fmt.Errorf("decode renew response: %w", err)
	}
	if strings.TrimSpace(parsed.CertificatePEM) == "" || strings.TrimSpace(parsed.CAPEM) == "" {
		return CertRenewResult{}, errors.New("renew response missing certificatePem or caPem")
	}

	currentCA, err := parseSinglePEMCert(id.CAPEM)
	if err != nil {
		return CertRenewResult{}, fmt.Errorf("parse pinned CA cert: %w", err)
	}
	responseCA, err := parseSinglePEMCert([]byte(parsed.CAPEM))
	if err != nil {
		return CertRenewResult{}, fmt.Errorf("parse response CA cert: %w", err)
	}
	if !bytes.Equal(currentCA.Raw, responseCA.Raw) {
		return CertRenewResult{}, errors.New("renew response CA certificate does not match pinned CA")
	}

	nextCert, err := parseSinglePEMCert([]byte(parsed.CertificatePEM))
	if err != nil {
		return CertRenewResult{}, fmt.Errorf("parse renewed leaf certificate: %w", err)
	}
	if _, err := nextCert.Verify(x509.VerifyOptions{
		Roots:       id.RootCAs,
		CurrentTime: now,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return CertRenewResult{}, fmt.Errorf("renewed certificate failed pinned CA verification: %w", err)
	}
	gotPubDER, err := x509.MarshalPKIXPublicKey(nextCert.PublicKey)
	if err != nil || !bytes.Equal(expectedPubDER, gotPubDER) {
		return CertRenewResult{}, errors.New("renewed certificate public key does not match gateway private key")
	}
	if strings.TrimSpace(nextCert.Subject.CommonName) != gatewayID {
		return CertRenewResult{}, errors.New("renewed certificate CN does not match gateway identity")
	}

	if err := writeFileAtomic(opts.CertFile, []byte(parsed.CertificatePEM), 0o644); err != nil {
		return CertRenewResult{}, fmt.Errorf("write renewed certificate atomically: %w", err)
	}
	result := CertRenewResult{
		Renewed:   true,
		Reason:    "renewed",
		GatewayID: gatewayID,
		ExpiresAt: nextCert.NotAfter,
	}
	if opts.OnRenewed != nil {
		opts.OnRenewed(result)
	}
	return result, nil
}

func parseSinglePEMCert(rawPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(rawPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("expected CERTIFICATE PEM block")
	}
	return x509.ParseCertificate(block.Bytes)
}

func createPKCS10CSRPEM(commonName string, signer crypto.Signer) ([]byte, []byte, error) {
	pubDER, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return nil, nil, err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: commonName},
	}, signer)
	if err != nil {
		return nil, nil, fmt.Errorf("create PKCS#10 CSR: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	return csrPEM, pubDER, nil
}
