package cube

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/modules/telemetry/internal/analytics"
)

type HMACTokenFactory struct {
	secret []byte
	now    func() time.Time
}

func NewHMACTokenFactory(secret []byte, now func() time.Time) (*HMACTokenFactory, error) {
	if len(secret) < 32 {
		return nil, errors.New("Cube API secret must contain at least 32 bytes")
	}
	if now == nil {
		now = time.Now
	}
	return &HMACTokenFactory{secret: append([]byte(nil), secret...), now: now}, nil
}

func (factory *HMACTokenFactory) Token(_ context.Context, caller analytics.CallerContext, productQuery analyticsmodel.EnergySeriesQuery) (string, error) {
	if factory == nil || len(factory.secret) < 32 || factory.now == nil {
		return "", errors.New("Cube token factory is closed")
	}
	if err := productQuery.Validate(); err != nil {
		return "", err
	}
	if strings.TrimSpace(caller.PrincipalID) == "" || strings.TrimSpace(caller.PolicyRevision) == "" {
		return "", errors.New("Cube caller context is incomplete")
	}
	now := factory.now().UTC()
	header, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(struct {
		Subject        string   `json:"sub"`
		IssuedAt       int64    `json:"iat"`
		ExpiresAt      int64    `json:"exp"`
		TenantID       string   `json:"tenantId"`
		SiteID         string   `json:"siteId"`
		SiteIDs        []string `json:"siteIds"`
		Groups         []string `json:"groups"`
		PrincipalID    string   `json:"principalId"`
		PolicyRevision string   `json:"policyRevision"`
	}{
		Subject:        caller.PrincipalID,
		IssuedAt:       now.Unix(),
		ExpiresAt:      now.Add(30 * time.Second).Unix(),
		TenantID:       productQuery.TenantID,
		SiteID:         productQuery.SiteID,
		SiteIDs:        []string{productQuery.SiteID},
		Groups:         []string{"analytics_reader"},
		PrincipalID:    caller.PrincipalID,
		PolicyRevision: caller.PolicyRevision,
	})
	if err != nil {
		return "", err
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedClaims := base64.RawURLEncoding.EncodeToString(claims)
	unsigned := encodedHeader + "." + encodedClaims
	mac := hmac.New(sha256.New, factory.secret)
	_, _ = mac.Write([]byte(unsigned))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return unsigned + "." + signature, nil
}

var _ TokenFactory = (*HMACTokenFactory)(nil)
