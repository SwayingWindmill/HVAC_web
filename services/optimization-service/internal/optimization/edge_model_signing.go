package optimization

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

// DefaultEdgeModelSigningKeyID matches connectivity's default platform OTA Ed25519 key identifier.
const DefaultEdgeModelSigningKeyID = "platform-ota-v1"

// LoadEdgeModelSigningKeyFromFile loads a PKCS#8 PEM-encoded Ed25519 private key from disk
// for signing desired edge model bundles. Blank keyID defaults to DefaultEdgeModelSigningKeyID.
func LoadEdgeModelSigningKeyFromFile(keyID, path string) (string, ed25519.PrivateKey, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return "", nil, errors.New("edge model signing key path is required")
	}

	rawPEM, err := os.ReadFile(trimmedPath)
	if err != nil {
		return "", nil, fmt.Errorf("read edge model signing key %q: %w", trimmedPath, err)
	}

	block, rest := pem.Decode(rawPEM)
	if block == nil {
		return "", nil, fmt.Errorf("decode edge model signing key %q: invalid PEM block", trimmedPath)
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return "", nil, fmt.Errorf("decode edge model signing key %q: unexpected trailing data after PEM block", trimmedPath)
	}
	if block.Type != "PRIVATE KEY" {
		return "", nil, fmt.Errorf("decode edge model signing key %q: unsupported PEM type %q (expected PKCS#8 PRIVATE KEY)", trimmedPath, block.Type)
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", nil, fmt.Errorf("parse PKCS#8 edge model signing key %q: %w", trimmedPath, err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(priv) != ed25519.PrivateKeySize {
		return "", nil, fmt.Errorf("parse edge model signing key %q: expected ed25519.PrivateKey, got %T", trimmedPath, parsed)
	}

	resolvedKeyID := strings.TrimSpace(keyID)
	if resolvedKeyID == "" {
		resolvedKeyID = DefaultEdgeModelSigningKeyID
	}

	out := make(ed25519.PrivateKey, ed25519.PrivateKeySize)
	copy(out, priv)
	return resolvedKeyID, out, nil
}
