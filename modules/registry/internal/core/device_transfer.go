package core

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	TransferScopeCrossSite   = "CROSS_SITE"
	TransferScopeCrossTenant = "CROSS_TENANT"
)

var (
	ErrTransferBlocked   = errors.New("device transfer blocked by active bindings or points")
	ErrTransferForbidden = errors.New("device transfer across tenants is forbidden")
)

var (
	errTransferInactiveDevice = errors.New("cannot transfer a non-active device")
	errTransferInactiveTarget = errors.New("cannot transfer device to a non-active site")
	errTransferSameSite       = errors.New("cross-site transfer requires a distinct target site")
)

// TransferDeviceRequest requests transferring physical device ownership across sites or tenants.
// The source logical Device is retired and a brand-new logical Device UUID is provisioned in the
// destination scope to preserve historical data boundaries, while optionally requesting edge wipe.
type TransferDeviceRequest struct {
	SourceSiteID   string       `json:"-"`
	DeviceID       string       `json:"-"`
	TargetTenantID *string      `json:"targetTenantId,omitempty"`
	TargetSiteID   string       `json:"targetSiteId"`
	NewCode        *string      `json:"newCode,omitempty"`
	NewDisplayName *string      `json:"newDisplayName,omitempty"`
	WipeEdgeCache  *bool        `json:"wipeEdgeCache,omitempty"`
	Meta           MutationMeta `json:"meta"`
}

// TransferDeviceResult returns the immutable transfer audit entry along with both retired source
// and newly minted target Device records.
type TransferDeviceResult struct {
	TransferID           string  `json:"transferId"`
	TransferScope        string  `json:"transferScope"`
	SourceTenantID       string  `json:"sourceTenantId"`
	SourceSiteID         string  `json:"sourceSiteId"`
	SourceDeviceID       string  `json:"sourceDeviceId"`
	SourceDeviceRevision int64   `json:"sourceDeviceRevision"`
	TargetTenantID       string  `json:"targetTenantId"`
	TargetSiteID         string  `json:"targetSiteId"`
	TargetDeviceID       string  `json:"targetDeviceId"`
	HardwareExternalID   *string `json:"hardwareExternalId,omitempty"`
	WipeRequired         bool    `json:"wipeRequired"`
	Reason               string  `json:"reason"`
	TransferredBy        string  `json:"transferredBy"`
	TransferredAt        string  `json:"transferredAt"`
	SourceDevice         Device  `json:"sourceDevice"`
	TargetDevice         Device  `json:"targetDevice"`
	Replayed             bool    `json:"replayed"`
}

// EffectiveWipeEdgeCache returns whether edge cache/credential factory wipe is required (defaults true).
func (request TransferDeviceRequest) EffectiveWipeEdgeCache() bool {
	if request.WipeEdgeCache == nil {
		return true
	}
	return *request.WipeEdgeCache
}

// Validate validates the structural fields of TransferDeviceRequest.
func (request TransferDeviceRequest) Validate() error {
	if !validUUIDv7(request.SourceSiteID) || !validUUIDv7(request.DeviceID) || !validUUIDv7(request.TargetSiteID) {
		return ErrInvalidMutation
	}
	if request.TargetTenantID != nil {
		trimmedTenant := strings.TrimSpace(*request.TargetTenantID)
		if trimmedTenant != "" && !validUUIDv7(trimmedTenant) {
			return ErrInvalidMutation
		}
	}
	if request.NewCode != nil {
		if !codePattern.MatchString(*request.NewCode) {
			return ErrInvalidMutation
		}
	}
	if request.NewDisplayName != nil {
		trimmedName := strings.TrimSpace(*request.NewDisplayName)
		if trimmedName == "" || utf8.RuneCountInString(trimmedName) > 128 {
			return ErrInvalidMutation
		}
	}
	return request.Meta.validate(false)
}

func (request TransferDeviceRequest) canonicalDigestPayload(resolvedTargetTenantID string) map[string]any {
	payload := map[string]any{
		"sourceSiteId":   request.SourceSiteID,
		"deviceId":       request.DeviceID,
		"targetTenantId": resolvedTargetTenantID,
		"targetSiteId":   request.TargetSiteID,
		"wipeEdgeCache":  request.EffectiveWipeEdgeCache(),
		"meta": map[string]any{
			"expectedRevision": request.Meta.ExpectedRevision,
			"reason":           strings.TrimSpace(request.Meta.Reason),
		},
	}
	if request.NewCode != nil {
		payload["newCode"] = *request.NewCode
	}
	if request.NewDisplayName != nil {
		payload["newDisplayName"] = strings.TrimSpace(*request.NewDisplayName)
	}
	return payload
}
