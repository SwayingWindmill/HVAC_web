package core

import (
	"errors"
	"time"
)

var (
	ErrNotFound                 = errors.New("registry resource not found")
	ErrInvalidPage              = errors.New("registry page request is invalid")
	ErrStoreClosed              = errors.New("registry store is closed")
	ErrStatusFailed             = errors.New("registry grant status unavailable")
	ErrInvalidBindingResolution = errors.New("meter binding resolution request is invalid")
)

const (
	DefaultPageLimit = 50
	MaximumPageLimit = 200
)

// V2 public Registry hierarchy starts at Tenant -> Site.
// Organization is intentionally absent from the canonical API model.
type Site struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenantId"`
	Code        string `json:"code"`
	DisplayName string `json:"displayName"`
	Timezone    string `json:"timezone"`
	Status      string `json:"status"`
	Revision    int64  `json:"revision"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type Asset struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenantId"`
	SiteID      string `json:"siteId"`
	Code        string `json:"code"`
	DisplayName string `json:"displayName"`
	AssetType   string `json:"assetType"`
	Status      string `json:"status"`
	Revision    int64  `json:"revision"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type Device struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenantId"`
	SiteID      string `json:"siteId"`
	Code        string `json:"code"`
	DisplayName string `json:"displayName"`
	DeviceType  string `json:"deviceType"`
	Status      string `json:"status"`
	Revision    int64  `json:"revision"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type DeviceBinding struct {
	ID          string  `json:"id"`
	TenantID    string  `json:"tenantId"`
	SiteID      string  `json:"siteId"`
	DeviceID    string  `json:"deviceId"`
	AssetID     string  `json:"assetId"`
	BindingRole string  `json:"bindingRole"`
	Status      string  `json:"status"`
	ValidFrom   string  `json:"validFrom"`
	ValidTo     *string `json:"validTo,omitempty"`
	Revision    int64   `json:"revision"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
}

type MeterBindingResolveRequest struct {
	DeviceID  string    `json:"deviceId"`
	PointID   string    `json:"pointId"`
	SampledAt time.Time `json:"sampledAt"`
}

type MeterBindingResolution struct {
	Status            string     `json:"status"`
	TenantID          string     `json:"tenantId,omitempty"`
	SiteID            string     `json:"siteId,omitempty"`
	MeterID           string     `json:"meterId,omitempty"`
	MeterBindingID    string     `json:"meterBindingId,omitempty"`
	TopologyVersionID string     `json:"topologyVersionId,omitempty"`
	BindingVersion    int64      `json:"bindingVersion,omitempty"`
	BindingRevision   int64      `json:"revision,omitempty"`
	EnergyTypeID      string     `json:"energyTypeId,omitempty"`
	EnergyType        string     `json:"energyType,omitempty"`
	MeterRole         string     `json:"meterRole,omitempty"`
	Direction         string     `json:"direction,omitempty"`
	DeviceID          string     `json:"deviceId,omitempty"`
	PointID           string     `json:"pointId,omitempty"`
	PointType         string     `json:"pointType,omitempty"`
	EffectiveFrom     time.Time  `json:"effectiveFrom,omitempty"`
	EffectiveTo       *time.Time `json:"effectiveTo,omitempty"`
}

type Collection[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
	HasMore    bool    `json:"hasMore"`
}

type PageRequest struct {
	Limit       int
	DisplayName string
	ID          string
}

type PageResult[T any] struct {
	Items   []T
	HasMore bool
}

func formatInstant(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}
