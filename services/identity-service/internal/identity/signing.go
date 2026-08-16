package identity

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
)

type SigningKey struct {
	Kid       string
	key       *rsa.PrivateKey
	PublicJWK map[string]any
}

func NewSigningKey() (SigningKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return SigningKey{}, fmt.Errorf("generate identity signing key: %w", err)
	}
	return signingKeyFromPrivateKey(key)
}

func GenerateSigningKeyPEM() ([]byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate identity signing key: %w", err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("encode identity signing key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), nil
}

func LoadSigningKeyFile(path string) (SigningKey, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return SigningKey{}, errors.New("identity signing key file is required")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return SigningKey{}, fmt.Errorf("read identity signing key: %w", err)
	}
	block, rest := pem.Decode(encoded)
	if block == nil || block.Type != "PRIVATE KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return SigningKey{}, errors.New("identity signing key must contain one PKCS#8 PRIVATE KEY PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return SigningKey{}, fmt.Errorf("parse identity signing key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok || key.N.BitLen() < 2048 {
		return SigningKey{}, errors.New("identity signing key must be an RSA key of at least 2048 bits")
	}
	if err := key.Validate(); err != nil {
		return SigningKey{}, fmt.Errorf("validate identity signing key: %w", err)
	}
	return signingKeyFromPrivateKey(key)
}

func signingKeyFromPrivateKey(key *rsa.PrivateKey) (SigningKey, error) {
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return SigningKey{}, fmt.Errorf("encode identity public key: %w", err)
	}
	digest := sha256.Sum256(publicDER)
	kid := base64.RawURLEncoding.EncodeToString(digest[:12])
	public := key.PublicKey
	return SigningKey{
		Kid: kid,
		key: key,
		PublicJWK: map[string]any{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": kid,
			"n":   base64.RawURLEncoding.EncodeToString(public.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(public.E)).Bytes()),
		},
	}, nil
}

type idTokenClaims struct {
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	Subject   string `json:"sub"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
	NotBefore int64  `json:"nbf"`
	Nonce     string `json:"nonce"`
	Name      string `json:"name"`
	Email     string `json:"email"`
}

func signIDToken(key SigningKey, claims idTokenClaims) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": key.Kid, "typ": "JWT"})
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
