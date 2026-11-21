package core

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

const maxProtocolAddressBytes = 8192

// ReplaceDeviceHardwareRequest swaps physical hardware metadata or gateway routing for an existing logical Device.
type ReplaceDeviceHardwareRequest struct {
	SiteID             string         `json:"-"`
	DeviceID           string         `json:"-"`
	NewExternalID      string         `json:"newExternalId"`
	NewGatewayDeviceID *string        `json:"newGatewayDeviceId,omitempty"`
	NewSourceKey       *string        `json:"newSourceKey,omitempty"`
	NewProtocolAddress map[string]any `json:"newProtocolAddress,omitempty"`
	Meta               MutationMeta   `json:"meta"`
}

// ReplaceDeviceHardwareResult returns the updated logical Device and immutable replacement history entry.
type ReplaceDeviceHardwareResult struct {
	ReplacementID           string         `json:"replacementId"`
	TenantID                string         `json:"tenantId"`
	SiteID                  string         `json:"siteId"`
	DeviceID                string         `json:"deviceId"`
	Revision                int64          `json:"revision"`
	PreviousExternalID      *string        `json:"previousExternalId"`
	NewExternalID           string         `json:"newExternalId"`
	PreviousGatewayDeviceID *string        `json:"previousGatewayDeviceId"`
	NewGatewayDeviceID      *string        `json:"newGatewayDeviceId"`
	PreviousProtocolAddress map[string]any `json:"previousProtocolAddress"`
	NewProtocolAddress      map[string]any `json:"newProtocolAddress"`
	Reason                  string         `json:"reason"`
	ReplacedBy              string         `json:"replacedBy"`
	ReplacedAt              string         `json:"replacedAt"`
	Device                  Device         `json:"device"`
	Replayed                bool           `json:"replayed"`
}

func (request ReplaceDeviceHardwareRequest) Validate() error {
	if !validUUIDv7(request.SiteID) || !validUUIDv7(request.DeviceID) {
		return ErrInvalidMutation
	}
	trimmedExternalID := strings.TrimSpace(request.NewExternalID)
	if trimmedExternalID == "" || utf8.RuneCountInString(trimmedExternalID) > 256 {
		return ErrInvalidMutation
	}
	if request.NewGatewayDeviceID != nil {
		gwID := strings.TrimSpace(*request.NewGatewayDeviceID)
		if !validUUIDv7(gwID) || gwID == request.DeviceID {
			return ErrInvalidMutation
		}
	}
	if request.NewSourceKey != nil {
		if !sourceKeyPattern.MatchString(*request.NewSourceKey) {
			return ErrInvalidMutation
		}
	}
	if request.NewProtocolAddress != nil {
		raw, err := json.Marshal(request.NewProtocolAddress)
		if err != nil || len(raw) > maxProtocolAddressBytes {
			return ErrInvalidMutation
		}
	}
	return request.Meta.validate(false)
}

func (request ReplaceDeviceHardwareRequest) canonicalDigestPayload() map[string]any {
	payload := map[string]any{
		"siteId":        request.SiteID,
		"deviceId":      request.DeviceID,
		"newExternalId": strings.TrimSpace(request.NewExternalID),
		"meta": map[string]any{
			"expectedRevision": request.Meta.ExpectedRevision,
			"reason":           strings.TrimSpace(request.Meta.Reason),
		},
	}
	if request.NewGatewayDeviceID != nil {
		payload["newGatewayDeviceId"] = strings.TrimSpace(*request.NewGatewayDeviceID)
	}
	if request.NewSourceKey != nil {
		payload["newSourceKey"] = *request.NewSourceKey
	}
	if request.NewProtocolAddress != nil {
		raw, _ := json.Marshal(request.NewProtocolAddress)
		sum := sha256.Sum256(raw)
		payload["newProtocolAddressDigest"] = sum[:]
	}
	return payload
}

var errReplaceDeviceInactive = errors.New("cannot replace hardware on a non-active device")
