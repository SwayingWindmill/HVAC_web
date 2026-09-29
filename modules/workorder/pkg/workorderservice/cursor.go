package workorderservice

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const cursorVersion = 1

type cursorCodec struct{ secret []byte }

type cursorPayload struct {
	Version        int                     `json:"version"`
	TenantID string                  `json:"tenantId"`
	SiteID         string                  `json:"siteId"`
	Status         workordermodel.Status   `json:"status,omitempty"`
	Priority       workordermodel.Priority `json:"priority,omitempty"`
	AssigneeID     string                  `json:"assigneeId,omitempty"`
	SourceDomain   workordermodel.SourceDomain `json:"sourceDomain,omitempty"`
	SourceRef      string                  `json:"sourceRef,omitempty"`
	UpdatedAt      string                  `json:"updatedAt"`
	WorkOrderID    string                  `json:"workOrderId"`
}

type cursorPosition struct {
	UpdatedAt   time.Time
	WorkOrderID string
}

func newCursorCodec(secret []byte) (*cursorCodec, error) {
	if len(secret) < 32 {
		return nil, errors.New("Work Order cursor secret must contain at least 32 bytes")
	}
	copySecret := append([]byte(nil), secret...)
	return &cursorCodec{secret: copySecret}, nil
}

func (codec *cursorCodec) Encode(tenantID, siteID string, filter Filter, updatedAt time.Time, workOrderID string) (string, error) {
	filter = normalizeFilter(filter)
	payload := cursorPayload{
		Version: cursorVersion, TenantID: tenantID, SiteID: siteID,
		Status: filter.Status, Priority: filter.Priority, AssigneeID: filter.AssigneeID,
		SourceDomain: filter.SourceDomain, SourceRef: filter.SourceRef,
		UpdatedAt: updatedAt.UTC().Format(time.RFC3339Nano), WorkOrderID: workOrderID,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	signature := codec.sign(encoded)
	return base64.RawURLEncoding.EncodeToString(encoded) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (codec *cursorCodec) Decode(token, tenantID, siteID string, filter Filter) (cursorPosition, error) {
	filter = normalizeFilter(filter)
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 512 {
		return cursorPosition{}, ErrInvalidCursor
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return cursorPosition{}, ErrInvalidCursor
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return cursorPosition{}, ErrInvalidCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, codec.sign(payloadBytes)) {
		return cursorPosition{}, ErrInvalidCursor
	}
	var payload cursorPayload
	decoder := json.NewDecoder(strings.NewReader(string(payloadBytes)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(&struct{}{}) != io.EOF || payload.Version != cursorVersion ||
		payload.TenantID != tenantID || payload.SiteID != siteID ||
		payload.Status != filter.Status || payload.Priority != filter.Priority || payload.AssigneeID != filter.AssigneeID ||
		payload.SourceDomain != filter.SourceDomain || payload.SourceRef != filter.SourceRef ||
		!workordermodel.IsUUIDv7(payload.WorkOrderID) {
		return cursorPosition{}, ErrInvalidCursor
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, payload.UpdatedAt)
	if err != nil {
		return cursorPosition{}, ErrInvalidCursor
	}
	return cursorPosition{UpdatedAt: updatedAt.UTC(), WorkOrderID: payload.WorkOrderID}, nil
}

func (codec *cursorCodec) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, codec.secret)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}
