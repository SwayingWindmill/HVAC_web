// Package grant signs and verifies the compact grants that the Gateway, IAM and the owners pass
// to each other: base64url(JSON claims) "." base64url(signature over SHA-256 of the JSON).
//
// The claims, their version and what makes them valid belong to each domain's *auth package;
// this package only owns the encoding, the size limit and the signature check, so a fix to any of
// them lands once for every grant.
package grant

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Format names one kind of grant. Name prefixes every error ("registry grant size is invalid"),
// MaxEncodedSize bounds the encoded token before any decoding or signature work.
type Format struct {
	Name           string
	MaxEncodedSize int
}

// Sign encodes claims as JSON and signs the SHA-256 digest of that JSON with signer.
func Sign[C any](format Format, signer crypto.Signer, claims C) (string, error) {
	if signer == nil {
		return "", fmt.Errorf("%s signer is required", format.Name)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal %s claims: %w", format.Name, err)
	}
	digest := sha256.Sum256(payload)
	signature, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		return "", fmt.Errorf("sign %s claims: %w", format.Name, err)
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// Verify checks the size, the encoding and the RSA PKCS#1 v1.5 or ECDSA signature of token,
// then decodes its claims. It does not judge the claims: version, expiry, audience and scope are
// the caller's to validate.
func Verify[C any](format Format, publicKey crypto.PublicKey, token string) (C, error) {
	var claims C
	if len(token) == 0 || len(token) > format.MaxEncodedSize {
		return claims, fmt.Errorf("%s size is invalid", format.Name)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return claims, fmt.Errorf("%s format is invalid", format.Name)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return claims, fmt.Errorf("%s payload encoding is invalid", format.Name)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims, fmt.Errorf("%s signature encoding is invalid", format.Name)
	}
	digest := sha256.Sum256(payload)
	switch key := publicKey.(type) {
	case *rsa.PublicKey:
		err = rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature)
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(key, digest[:], signature) {
			err = errors.New("ecdsa signature is invalid")
		}
	default:
		err = fmt.Errorf("unsupported public key %T", publicKey)
	}
	if err != nil {
		return claims, fmt.Errorf("%s signature is invalid", format.Name)
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		var zero C
		return zero, fmt.Errorf("%s payload is invalid", format.Name)
	}
	return claims, nil
}
