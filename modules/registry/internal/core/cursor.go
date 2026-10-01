package core

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	cursorVersion       = 1
	cursorQueryRevision = 1
	cursorFilterHash    = "none"
)

var cursorOrder = []string{"displayName", "id"}

type cursorPayload struct {
	Version       int                 `json:"v"`
	Route         string              `json:"route"`
	Action        registryauth.Action `json:"action"`
	ScopeHash     string              `json:"scopeHash"`
	FilterHash    string              `json:"filterHash"`
	Order         []string            `json:"order"`
	Last          []string            `json:"last"`
	QueryRevision int                 `json:"queryRevision"`
}

type CursorCodec struct {
	key []byte
}

func NewCursorCodec(key []byte) (*CursorCodec, error) {
	if len(key) < 32 {
		return nil, errors.New("cursor signing key must contain at least 32 bytes")
	}
	return &CursorCodec{key: append([]byte(nil), key...)}, nil
}

func (codec *CursorCodec) Encode(resource, parentID string, action registryauth.Action, claims registryauth.GrantClaims, displayName, id string) (string, error) {
	if codec == nil || len(codec.key) == 0 || resource == "" || displayName == "" || !validUUIDv7(id) {
		return "", ErrInvalidPage
	}
	payload := cursorPayload{
		Version:       cursorVersion,
		Route:         cursorRoute(resource, parentID),
		Action:        action,
		ScopeHash:     scopeHash(claims),
		FilterHash:    cursorFilterHash,
		Order:         append([]string(nil), cursorOrder...),
		Last:          []string{displayName, id},
		QueryRevision: cursorQueryRevision,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal Registry cursor: %w", err)
	}
	mac := hmac.New(sha256.New, codec.key)
	_, _ = mac.Write(encoded)
	return base64.RawURLEncoding.EncodeToString(encoded) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (codec *CursorCodec) Decode(token, resource, parentID string, action registryauth.Action, claims registryauth.GrantClaims) (PageRequest, error) {
	if token == "" {
		return PageRequest{}, nil
	}
	if codec == nil || len(codec.key) == 0 || len(token) > 4096 {
		return PageRequest{}, ErrInvalidPage
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return PageRequest{}, ErrInvalidPage
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return PageRequest{}, ErrInvalidPage
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return PageRequest{}, ErrInvalidPage
	}
	mac := hmac.New(sha256.New, codec.key)
	_, _ = mac.Write(payloadBytes)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return PageRequest{}, ErrInvalidPage
	}
	var payload cursorPayload
	decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return PageRequest{}, ErrInvalidPage
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return PageRequest{}, ErrInvalidPage
	}
	if payload.Version != cursorVersion || payload.Route != cursorRoute(resource, parentID) || payload.Action != action || payload.ScopeHash != scopeHash(claims) || payload.FilterHash != cursorFilterHash || payload.QueryRevision != cursorQueryRevision || len(payload.Order) != 2 || payload.Order[0] != cursorOrder[0] || payload.Order[1] != cursorOrder[1] || len(payload.Last) != 2 || payload.Last[0] == "" || !validUUIDv7(payload.Last[1]) {
		return PageRequest{}, ErrInvalidPage
	}
	return PageRequest{DisplayName: payload.Last[0], ID: payload.Last[1]}, nil
}

func cursorRoute(resource, parentID string) string {
	if parentID == "" {
		return resource
	}
	return resource + "/" + parentID
}

func scopeHash(claims registryauth.GrantClaims) string {
	parts := []string{
		"principal=" + claims.PrincipalID,
		"tenant=" + claims.TenantID,
		"policy=" + claims.PolicyRevision,
	}
	for label, values := range map[string][]string{
		"allow-site": claims.AllowedSiteIDs,
		"deny-site":  claims.DeniedSiteIDs,
	} {
		copyValues := append([]string(nil), values...)
		sort.Strings(copyValues)
		parts = append(parts, label+"="+strings.Join(copyValues, ","))
	}
	sort.Strings(parts)
	digest := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(digest[:])
}
