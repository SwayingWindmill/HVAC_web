package core

import (
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("registry resource not found")
	ErrInvalidPage  = errors.New("registry page request is invalid")
	ErrStoreClosed  = errors.New("registry store is closed")
	ErrStatusFailed = errors.New("registry grant status unavailable")
)

const (
	DefaultPageLimit = 50
	MaximumPageLimit = 200
)

type Organization struct {
	ID          string `json:"id"`
	Code        string `json:"code"`
	DisplayName string `json:"displayName"`
	Status      string `json:"status"`
	Revision    int64  `json:"revision"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type Site struct {
	ID                   string `json:"id"`
	OwningOrganizationID string `json:"owningOrganizationId"`
	Code                 string `json:"code"`
	DisplayName          string `json:"displayName"`
	Timezone             string `json:"timezone"`
	Status               string `json:"status"`
	Revision             int64  `json:"revision"`
	CreatedAt            string `json:"createdAt"`
	UpdatedAt            string `json:"updatedAt"`
}

type Equipment struct {
	ID                   string `json:"id"`
	OwningOrganizationID string `json:"owningOrganizationId"`
	SiteID               string `json:"siteId"`
	Code                 string `json:"code"`
	DisplayName          string `json:"displayName"`
	EquipmentType        string `json:"equipmentType"`
	Status               string `json:"status"`
	Revision             int64  `json:"revision"`
	CreatedAt            string `json:"createdAt"`
	UpdatedAt            string `json:"updatedAt"`
}

type Device struct {
	ID                   string `json:"id"`
	OwningOrganizationID string `json:"owningOrganizationId"`
	SiteID               string `json:"siteId"`
	Code                 string `json:"code"`
	DisplayName          string `json:"displayName"`
	DeviceType           string `json:"deviceType"`
	Status               string `json:"status"`
	Revision             int64  `json:"revision"`
	CreatedAt            string `json:"createdAt"`
	UpdatedAt            string `json:"updatedAt"`
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
