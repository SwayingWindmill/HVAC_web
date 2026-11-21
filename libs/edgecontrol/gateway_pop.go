package edgecontrol

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderGatewayCertPopTimestamp = "X-Gateway-Cert-Pop-Timestamp"
	HeaderGatewayCertPopNonce     = "X-Gateway-Cert-Pop-Nonce"
	HeaderGatewayCertPopSignature = "X-Gateway-Cert-Pop-Signature"
	HeaderGatewayCertForwardedBy  = "X-Gateway-Cert-Forwarded-By"

	gatewayPoPDomainPrefix = "HVAC-GW-POP-V1"
)

// CanonicalGatewayPoPPayload builds the canonical string signed for gateway PoP headers.
func CanonicalGatewayPoPPayload(method, pathAndQuery, timestamp, nonce, certFingerprintSHA256 string) string {
	normMethod := strings.ToUpper(strings.TrimSpace(method))
	normPath := strings.TrimSpace(pathAndQuery)
	if normPath == "" {
		normPath = "/"
	}
	return strings.Join([]string{
		gatewayPoPDomainPrefix,
		normMethod,
		normPath,
		strings.TrimSpace(timestamp),
		strings.TrimSpace(nonce),
		strings.ToLower(strings.TrimSpace(certFingerprintSHA256)),
	}, "\n")
}

// SignGatewayRequestPoP attaches X-Gateway-Cert-Pop-* headers proving possession of
// leafDER's corresponding private key.
func SignGatewayRequestPoP(req *http.Request, leafDER []byte, signer crypto.Signer, now time.Time) error {
	if req == nil {
		return errors.New("gateway PoP signing requires HTTP request")
	}
	if len(leafDER) == 0 {
		return errors.New("gateway PoP signing requires leaf certificate DER")
	}
	if signer == nil {
		return errors.New("gateway PoP signing requires private key signer")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	var nonceBuf [16]byte
	if _, err := rand.Read(nonceBuf[:]); err != nil {
		return fmt.Errorf("generate gateway PoP nonce: %w", err)
	}
	nonce := hex.EncodeToString(nonceBuf[:])
	timestamp := strconv.FormatInt(now.UTC().Unix(), 10)
	fpBytes := sha256.Sum256(leafDER)
	fingerprint := hex.EncodeToString(fpBytes[:])

	pathAndQuery := "/"
	if req.URL != nil {
		pathAndQuery = req.URL.RequestURI()
	}
	canonical := []byte(CanonicalGatewayPoPPayload(req.Method, pathAndQuery, timestamp, nonce, fingerprint))

	var (
		sig []byte
		err error
	)
	switch k := signer.(type) {
	case *ecdsa.PrivateKey:
		digest := sha256.Sum256(canonical)
		sig, err = k.Sign(rand.Reader, digest[:], crypto.SHA256)
	case *rsa.PrivateKey:
		digest := sha256.Sum256(canonical)
		sig, err = rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, digest[:])
	case ed25519.PrivateKey:
		sig = ed25519.Sign(k, canonical)
	default:
		if _, isEd := signer.Public().(ed25519.PublicKey); isEd {
			sig, err = signer.Sign(rand.Reader, canonical, crypto.Hash(0))
		} else {
			digest := sha256.Sum256(canonical)
			sig, err = signer.Sign(rand.Reader, digest[:], crypto.SHA256)
		}
	}
	if err != nil {
		return fmt.Errorf("sign gateway PoP payload: %w", err)
	}

	req.Header.Set(HeaderGatewayCertPopTimestamp, timestamp)
	req.Header.Set(HeaderGatewayCertPopNonce, nonce)
	req.Header.Set(HeaderGatewayCertPopSignature, base64.StdEncoding.EncodeToString(sig))
	return nil
}
