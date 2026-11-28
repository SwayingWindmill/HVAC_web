package connectivity

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

const (
	DefaultMaxOTAPackageBytes int64 = 64 << 20
	DefaultOTASigningKeyID          = "platform-ota-v1"
)

var ErrOTAPackageTooLarge = errors.New("OTA package file exceeds maximum allowed size")

// OTAArtifactStorage persists immutable OTA binaries on local disk under a tenant-partitioned root directory.
type OTAArtifactStorage struct {
	rootDir string
}

// NewOTAArtifactStorage initializes and verifies the local OTA package storage root directory.
func NewOTAArtifactStorage(dir string) (*OTAArtifactStorage, error) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return nil, errors.New("OTA storage directory is required")
	}
	absDir, err := filepath.Abs(filepath.Clean(trimmed))
	if err != nil {
		return nil, fmt.Errorf("resolve OTA storage directory: %w", err)
	}
	if err := os.MkdirAll(absDir, 0o750); err != nil {
		return nil, fmt.Errorf("create OTA storage directory: %w", err)
	}
	return &OTAArtifactStorage{rootDir: absDir}, nil
}

// SaveStream atomically writes reader to disk while computing its SHA-256 digest and enforcing maxBytes.
func (s *OTAArtifactStorage) SaveStream(tenantID, packageID string, reader io.Reader, maxBytes int64) (string, int64, string, error) {
	if s == nil {
		return "", 0, "", errors.New("OTA storage is not configured")
	}
	tID := strings.ToLower(strings.TrimSpace(tenantID))
	pID := strings.ToLower(strings.TrimSpace(packageID))
	if !uuidv7.IsValid(tID) || !uuidv7.IsValid(pID) {
		return "", 0, "", fmt.Errorf("%w: tenantId and packageId must be UUIDv7", ErrOTAInvalid)
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxOTAPackageBytes
	}

	tenantDir := filepath.Join(s.rootDir, tID)
	if err := os.MkdirAll(tenantDir, 0o750); err != nil {
		return "", 0, "", fmt.Errorf("create tenant OTA directory: %w", err)
	}

	tmp, err := os.CreateTemp(tenantDir, ".ota-pkg-*.tmp")
	if err != nil {
		return "", 0, "", fmt.Errorf("create temporary OTA file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	limited := io.LimitReader(reader, maxBytes+1)
	written, copyErr := io.Copy(io.MultiWriter(tmp, hasher), limited)
	if copyErr != nil {
		_ = tmp.Close()
		return "", 0, "", fmt.Errorf("stream OTA package: %w", copyErr)
	}
	if written == 0 {
		_ = tmp.Close()
		return "", 0, "", fmt.Errorf("%w: package file must not be empty", ErrOTAInvalid)
	}
	if written > maxBytes {
		_ = tmp.Close()
		return "", 0, "", fmt.Errorf("%w: exceeds %d bytes", ErrOTAPackageTooLarge, maxBytes)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", 0, "", fmt.Errorf("sync OTA package file: %w", err)
	}
	if err := tmp.Chmod(0o640); err != nil {
		_ = tmp.Close()
		return "", 0, "", fmt.Errorf("chmod OTA package file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", 0, "", fmt.Errorf("close OTA package file: %w", err)
	}

	relNative := filepath.Join(tID, pID+".bin")
	finalPath, err := s.resolveSafePath(relNative)
	if err != nil {
		return "", 0, "", err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", 0, "", fmt.Errorf("commit OTA package file: %w", err)
	}
	cleanup = false

	shaHex := hex.EncodeToString(hasher.Sum(nil))
	return filepath.ToSlash(relNative), written, shaHex, nil
}

// Open opens a stored OTA package binary using traversal-safe path resolution.
func (s *OTAArtifactStorage) Open(relPath string) (*os.File, os.FileInfo, error) {
	if s == nil {
		return nil, nil, errors.New("OTA storage is not configured")
	}
	target, err := s.resolveSafePath(relPath)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Open(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, fmt.Errorf("open OTA package file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("stat OTA package file: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, ErrNotFound
	}
	return file, info, nil
}

// Delete removes a previously stored OTA binary if present.
func (s *OTAArtifactStorage) Delete(relPath string) error {
	if s == nil || strings.TrimSpace(relPath) == "" {
		return nil
	}
	target, err := s.resolveSafePath(relPath)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *OTAArtifactStorage) resolveSafePath(relPath string) (string, error) {
	cleanedRel := filepath.Clean(filepath.FromSlash(strings.TrimSpace(relPath)))
	if cleanedRel == "" || cleanedRel == "." || !filepath.IsLocal(cleanedRel) {
		return "", fmt.Errorf("%w: invalid OTA storage relative path", ErrOTAInvalid)
	}
	target := filepath.Join(s.rootDir, cleanedRel)
	relCheck, err := filepath.Rel(s.rootDir, target)
	if err != nil || !filepath.IsLocal(relCheck) {
		return "", fmt.Errorf("%w: OTA storage path escapes root", ErrOTAInvalid)
	}
	return target, nil
}

// OTASignerVerifier signs newly uploaded OTA artifacts or validates caller-supplied Ed25519 signatures.
type OTASignerVerifier struct {
	signingKeyID string
	signingKey   ed25519.PrivateKey
	trustedKeys  map[string]ed25519.PublicKey
}

// NewOTASignerVerifier constructs a signer/verifier from optional Ed25519 private key and trusted public keys (keyId -> base64).
func NewOTASignerVerifier(signingKeyID string, priv ed25519.PrivateKey, trustedB64 map[string]string) (*OTASignerVerifier, error) {
	trusted := make(map[string]ed25519.PublicKey, len(trustedB64)+1)
	for id, rawB64 := range trustedB64 {
		keyID := strings.TrimSpace(id)
		if keyID == "" {
			return nil, fmt.Errorf("%w: trusted OTA key ID must not be empty", ErrOTAInvalid)
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rawB64))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: invalid Ed25519 public key for %q", ErrOTAInvalid, keyID)
		}
		trusted[keyID] = ed25519.PublicKey(raw)
	}

	resolvedKeyID := strings.TrimSpace(signingKeyID)
	if len(priv) != 0 {
		if len(priv) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("%w: invalid Ed25519 private key size", ErrOTAInvalid)
		}
		if resolvedKeyID == "" {
			resolvedKeyID = DefaultOTASigningKeyID
		}
		pub := priv.Public().(ed25519.PublicKey)
		trusted[resolvedKeyID] = pub
	}

	return &OTASignerVerifier{
		signingKeyID: resolvedKeyID,
		signingKey:   ed25519.PrivateKey(append([]byte(nil), priv...)),
		trustedKeys:  trusted,
	}, nil
}

// LoadOTASignerVerifierFromEnv loads Ed25519 signing/trusted keys from CONNECTIVITY_OTA_* env vars if set.
func LoadOTASignerVerifierFromEnv() (*OTASignerVerifier, error) {
	keyID := strings.TrimSpace(os.Getenv("CONNECTIVITY_OTA_SIGNING_KEY_ID"))
	if keyID == "" {
		keyID = DefaultOTASigningKeyID
	}
	var priv ed25519.PrivateKey
	if keyFile := strings.TrimSpace(os.Getenv("CONNECTIVITY_OTA_SIGNING_KEY_FILE")); keyFile != "" {
		raw, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, fmt.Errorf("read CONNECTIVITY_OTA_SIGNING_KEY_FILE: %w", err)
		}
		priv, err = parseEd25519PrivateKeyBytes(raw)
		if err != nil {
			return nil, fmt.Errorf("parse CONNECTIVITY_OTA_SIGNING_KEY_FILE: %w", err)
		}
	}
	var trusted map[string]string
	if rawJSON := strings.TrimSpace(os.Getenv("CONNECTIVITY_OTA_TRUSTED_KEYS_JSON")); rawJSON != "" {
		if err := json.Unmarshal([]byte(rawJSON), &trusted); err != nil {
			return nil, fmt.Errorf("parse CONNECTIVITY_OTA_TRUSTED_KEYS_JSON: %w", err)
		}
	}
	return NewOTASignerVerifier(keyID, priv, trusted)
}

// CanServerSign reports whether a server-side Ed25519 signing key is configured.
func (v *OTASignerVerifier) CanServerSign() bool {
	return v != nil && len(v.signingKey) == ed25519.PrivateKeySize && v.signingKeyID != ""
}

// ResolveSignature verifies an operator-supplied signature (or signs server-side when omitted).
func (v *OTASignerVerifier) ResolveSignature(
	packageID, version, component, targetOS, targetArch, sha256Hex string,
	fileSizeBytes int64,
	suppliedKeyID, suppliedSigB64 string,
) (string, string, error) {
	keyID := strings.TrimSpace(suppliedKeyID)
	sigB64 := strings.TrimSpace(suppliedSigB64)
	if sigB64 != "" {
		if keyID == "" || len(keyID) > 128 {
			return "", "", fmt.Errorf("%w: signingKeyId is required when signatureB64 is provided", ErrOTAInvalid)
		}
		sigBytes, err := base64.StdEncoding.DecodeString(sigB64)
		if err != nil || len(sigBytes) != ed25519.SignatureSize {
			return "", "", fmt.Errorf("%w: signatureB64 must be a valid Base64-encoded 64-byte Ed25519 signature", ErrOTAInvalid)
		}
		if v != nil && len(v.trustedKeys) > 0 {
			pub, ok := v.trustedKeys[keyID]
			if !ok {
				return "", "", fmt.Errorf("%w: unknown OTA signingKeyId %q", ErrOTAInvalid, keyID)
			}
			if err := edgecontrol.VerifyOTASignatureEd25519(pub, packageID, version, component, targetOS, targetArch, sha256Hex, fileSizeBytes, sigB64); err != nil {
				return "", "", fmt.Errorf("%w: %v", ErrOTAInvalid, err)
			}
		}
		return keyID, sigB64, nil
	}

	if v == nil || len(v.signingKey) != ed25519.PrivateKeySize || v.signingKeyID == "" {
		return "", "", fmt.Errorf("%w: signatureB64 and signingKeyId are required when server signing key is not configured", ErrOTAInvalid)
	}
	if keyID != "" && keyID != v.signingKeyID {
		return "", "", fmt.Errorf("%w: signingKeyId %q does not match configured server signing key %q", ErrOTAInvalid, keyID, v.signingKeyID)
	}
	signed, err := edgecontrol.SignOTAPackageEd25519(v.signingKey, packageID, version, component, targetOS, targetArch, sha256Hex, fileSizeBytes)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrOTAInvalid, err)
	}
	return v.signingKeyID, signed, nil
}

func parseEd25519PrivateKeyBytes(raw []byte) (ed25519.PrivateKey, error) {
	if block, _ := pem.Decode(raw); block != nil {
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		priv, ok := parsed.(ed25519.PrivateKey)
		if !ok {
			return nil, errors.New("PEM key is not an Ed25519 private key")
		}
		return priv, nil
	}
	trimmed := strings.TrimSpace(string(raw))
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, err
	}
	switch len(decoded) {
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(decoded), nil
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(decoded), nil
	default:
		return nil, fmt.Errorf("unexpected decoded Ed25519 key length %d", len(decoded))
	}
}
