package connectivity

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// HeaderGatewayCertPopTimestamp holds Unix epoch seconds of a gateway PoP signature.
	HeaderGatewayCertPopTimestamp = "X-Gateway-Cert-Pop-Timestamp"
	// HeaderGatewayCertPopNonce holds a per-request unique nonce (>= 16 printable ASCII bytes).
	HeaderGatewayCertPopNonce = "X-Gateway-Cert-Pop-Nonce"
	// HeaderGatewayCertPopSignature holds the base64 PoP signature over the canonical string.
	HeaderGatewayCertPopSignature = "X-Gateway-Cert-Pop-Signature"
	// HeaderGatewayCertForwardedBy is set by a trusted mTLS front proxy when forwarding client certs.
	HeaderGatewayCertForwardedBy = "X-Gateway-Cert-Forwarded-By"

	popCanonicalPrefix   = "HVAC-GW-POP-V1"
	popMaxClockSkew      = 300 * time.Second
	popReplayWindow      = 10 * time.Minute
	popMaxReplayEntries  = 65536
	popMinNonceLength    = 16
	popMaxNonceLength    = 128
	popMaxSignatureBytes = 4096
	popRequireEnvVarName = "CONNECTIVITY_REQUIRE_FORWARDED_CERT_POP"
)

// PoPReplayGuard tracks seen (certFingerprintSHA256, nonce) pairs within popReplayWindow
// using FIFO + TTL bounding to prevent replay attacks without unbounded memory growth.
type PoPReplayGuard struct {
	mu      sync.Mutex
	ttl     time.Duration
	maxSize int
	seen    map[string]time.Time
	order   []popReplayEntry
}

type popReplayEntry struct {
	key       string
	expiresAt time.Time
}

// NewPoPReplayGuard constructs a bounded nonce replay guard with the supplied TTL and capacity.
func NewPoPReplayGuard(ttl time.Duration, maxSize int) *PoPReplayGuard {
	if ttl <= 0 {
		ttl = popReplayWindow
	}
	if maxSize <= 0 {
		maxSize = popMaxReplayEntries
	}
	return &PoPReplayGuard{
		ttl:     ttl,
		maxSize: maxSize,
		seen:    make(map[string]time.Time),
	}
}

// Consume checks whether (fingerprint, nonce) has already been used and records it when fresh.
func (g *PoPReplayGuard) Consume(fingerprint, nonce string, now time.Time) bool {
	if g == nil {
		return true
	}
	key := fingerprint + "\x00" + nonce
	g.mu.Lock()
	defer g.mu.Unlock()

	for len(g.order) > 0 && !g.order[0].expiresAt.After(now) {
		head := g.order[0]
		g.order[0] = popReplayEntry{}
		g.order = g.order[1:]
		if exp, ok := g.seen[head.key]; ok && !exp.After(now) {
			delete(g.seen, head.key)
		}
	}
	if len(g.order) == 0 {
		g.order = nil
	} else if cap(g.order) > 1024 && len(g.order)*2 <= cap(g.order) {
		compacted := make([]popReplayEntry, len(g.order))
		copy(compacted, g.order)
		g.order = compacted
	}

	if exp, exists := g.seen[key]; exists && exp.After(now) {
		return false
	}

	for len(g.seen) >= g.maxSize && len(g.order) > 0 {
		evict := g.order[0]
		g.order[0] = popReplayEntry{}
		g.order = g.order[1:]
		if exp, ok := g.seen[evict.key]; ok && !exp.After(evict.expiresAt) {
			delete(g.seen, evict.key)
		}
	}

	expiresAt := now.Add(g.ttl)
	g.seen[key] = expiresAt
	g.order = append(g.order, popReplayEntry{key: key, expiresAt: expiresAt})
	return true
}

var defaultPoPReplayGuard = NewPoPReplayGuard(popReplayWindow, popMaxReplayEntries)

// CanonicalGatewayPoPPayload builds the deterministic ASCII signing payload shared by
// Go gateways, edge Node-RED scripts, and connectivity HTTP ingress verification.
func CanonicalGatewayPoPPayload(method, requestURI, timestamp, nonce, certFingerprintSHA256 string) string {
	pathAndQuery := strings.TrimSpace(requestURI)
	if pathAndQuery == "" {
		pathAndQuery = "/"
	}
	return popCanonicalPrefix + "\n" +
		strings.ToUpper(strings.TrimSpace(method)) + "\n" +
		pathAndQuery + "\n" +
		strings.TrimSpace(timestamp) + "\n" +
		strings.TrimSpace(nonce) + "\n" +
		strings.ToLower(strings.TrimSpace(certFingerprintSHA256))
}

func certFingerprintSHA256Hex(certDER []byte) string {
	sum := sha256.Sum256(certDER)
	return hex.EncodeToString(sum[:])
}

func requestPathAndQuery(r *http.Request) string {
	if r == nil || r.URL == nil {
		return "/"
	}
	if uri := r.URL.RequestURI(); uri != "" {
		return uri
	}
	path := r.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	if r.URL.RawQuery != "" {
		return path + "?" + r.URL.RawQuery
	}
	return path
}

func hasAnyGatewayPoPHeader(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.Header.Get(HeaderGatewayCertPopTimestamp)) != "" ||
		strings.TrimSpace(r.Header.Get(HeaderGatewayCertPopNonce)) != "" ||
		strings.TrimSpace(r.Header.Get(HeaderGatewayCertPopSignature)) != ""
}

func shouldVerifyGatewayRequestPoP(r *http.Request) bool {
	if hasAnyGatewayPoPHeader(r) {
		return true
	}
	// Direct gateway mTLS connections already prove private-key possession in TLS 1.3;
	// only forwarded client certificate requests require PoP when configured globally.
	if r != nil && r.Header.Get("X-Gateway-Client-Cert") != "" {
		return strings.EqualFold(strings.TrimSpace(os.Getenv(popRequireEnvVarName)), "true")
	}
	return false
}

// VerifyGatewayRequestPoP validates X-Gateway-Cert-Pop-{Timestamp,Nonce,Signature} against
// peer's public key, checking timestamp skew and nonce uniqueness in guard.
func VerifyGatewayRequestPoP(r *http.Request, peer *x509.Certificate, guard *PoPReplayGuard, now time.Time) error {
	if r == nil || peer == nil || len(peer.Raw) == 0 || peer.PublicKey == nil {
		return ErrEnrollmentRejected
	}
	tsRaw := strings.TrimSpace(r.Header.Get(HeaderGatewayCertPopTimestamp))
	nonce := strings.TrimSpace(r.Header.Get(HeaderGatewayCertPopNonce))
	sigRaw := strings.TrimSpace(r.Header.Get(HeaderGatewayCertPopSignature))
	if tsRaw == "" || nonce == "" || sigRaw == "" {
		return ErrEnrollmentRejected
	}
	tsSec, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil {
		return ErrEnrollmentRejected
	}
	nowUTC := now.UTC()
	signedAt := time.Unix(tsSec, 0).UTC()
	skew := nowUTC.Sub(signedAt)
	if math.Abs(skew.Seconds()) > popMaxClockSkew.Seconds() {
		return ErrEnrollmentRejected
	}
	if !isValidPoPNonce(nonce) {
		return ErrEnrollmentRejected
	}
	if len(sigRaw) > popMaxSignatureBytes {
		return ErrEnrollmentRejected
	}
	sigBytes, err := base64.StdEncoding.DecodeString(sigRaw)
	if err != nil || len(sigBytes) == 0 {
		return ErrEnrollmentRejected
	}

	fingerprint := certFingerprintSHA256Hex(peer.Raw)
	canonical := CanonicalGatewayPoPPayload(r.Method, requestPathAndQuery(r), tsRaw, nonce, fingerprint)
	if !verifyGatewayPoPSignature(peer.PublicKey, []byte(canonical), sigBytes) {
		return ErrEnrollmentRejected
	}
	if guard != nil && !guard.Consume(fingerprint, nonce, nowUTC) {
		return ErrEnrollmentRejected
	}
	return nil
}

// SignGatewayRequestPoP attaches X-Gateway-Cert-Pop-{Timestamp,Nonce,Signature} headers
// to req signed by the gateway leaf certificate's private key.
func SignGatewayRequestPoP(req *http.Request, certDER []byte, signer crypto.Signer, now time.Time) error {
	if req == nil || len(certDER) == 0 || signer == nil {
		return errors.New("gateway PoP signing requires request, certificate, and signer")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var nonceBytes [16]byte
	if _, err := rand.Read(nonceBytes[:]); err != nil {
		return err
	}
	nonce := hex.EncodeToString(nonceBytes[:])
	ts := strconv.FormatInt(now.UTC().Unix(), 10)
	fingerprint := certFingerprintSHA256Hex(certDER)
	canonical := CanonicalGatewayPoPPayload(req.Method, requestPathAndQuery(req), ts, nonce, fingerprint)
	sig, err := signGatewayPoPPayload(signer, []byte(canonical))
	if err != nil {
		return err
	}
	req.Header.Set(HeaderGatewayCertPopTimestamp, ts)
	req.Header.Set(HeaderGatewayCertPopNonce, nonce)
	req.Header.Set(HeaderGatewayCertPopSignature, base64.StdEncoding.EncodeToString(sig))
	return nil
}

func isValidPoPNonce(nonce string) bool {
	if len(nonce) < popMinNonceLength || len(nonce) > popMaxNonceLength {
		return false
	}
	for i := 0; i < len(nonce); i++ {
		b := nonce[i]
		if b <= 0x20 || b >= 0x7f {
			return false
		}
	}
	return true
}

func signGatewayPoPPayload(signer crypto.Signer, payload []byte) ([]byte, error) {
	switch signer.Public().(type) {
	case ed25519.PublicKey:
		return signer.Sign(rand.Reader, payload, crypto.Hash(0))
	case *ecdsa.PublicKey, *rsa.PublicKey:
		digest := sha256.Sum256(payload)
		return signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	default:
		return nil, errors.New("unsupported gateway PoP signer key type")
	}
}

func verifyGatewayPoPSignature(pub crypto.PublicKey, payload, sig []byte) bool {
	digest := sha256.Sum256(payload)
	switch key := pub.(type) {
	case *ecdsa.PublicKey:
		return ecdsa.VerifyASN1(key, digest[:], sig)
	case *rsa.PublicKey:
		if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) == nil {
			return true
		}
		return rsa.VerifyPSS(key, crypto.SHA256, digest[:], sig, nil) == nil
	case ed25519.PublicKey:
		return ed25519.Verify(key, payload, sig)
	default:
		return false
	}
}
