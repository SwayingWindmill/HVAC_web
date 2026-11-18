package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

type memoryHardwareReplacementStore struct {
	*memoryScheduleBackend
	calls int
	last  ReplaceDeviceHardwareRequest
	res   ReplaceDeviceHardwareResult
	err   error
}

func (m *memoryHardwareReplacementStore) ReplaceDeviceHardware(
	_ context.Context,
	_ registryauth.GrantClaims,
	input ReplaceDeviceHardwareRequest,
) (ReplaceDeviceHardwareResult, error) {
	if err := input.Validate(); err != nil {
		return ReplaceDeviceHardwareResult{}, err
	}
	m.calls++
	m.last = input
	if m.err != nil {
		return ReplaceDeviceHardwareResult{}, m.err
	}
	return m.res, nil
}

func TestReplaceDeviceHardwareRequestValidation(t *testing.T) {
	validGateway := "018f1e00-4000-7000-8000-000000000100"
	validSourceKey := "meter_total_01"
	valid := ReplaceDeviceHardwareRequest{
		SiteID:             testSiteA1,
		DeviceID:           testDeviceA1,
		NewExternalID:      "SN-2026-NEW-001",
		NewGatewayDeviceID: &validGateway,
		NewSourceKey:       &validSourceKey,
		NewProtocolAddress: map[string]any{"modbusUnit": 7},
		Meta: MutationMeta{
			ExpectedRevision: 2,
			IdempotencyKey:   "hw-replace-valid-01",
			Reason:           "现场更换损坏电表通信模块",
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid hardware replacement request, got err=%v", err)
	}

	selfGateway := testDeviceA1
	badSourceKey := "99-invalid-key"
	oversized := map[string]any{"blob": strings.Repeat("x", maxProtocolAddressBytes+16)}
	cases := []struct {
		name  string
		build func() ReplaceDeviceHardwareRequest
	}{
		{
			name: "missing external id",
			build: func() ReplaceDeviceHardwareRequest {
				c := valid
				c.NewExternalID = "   "
				return c
			},
		},
		{
			name: "invalid device uuid",
			build: func() ReplaceDeviceHardwareRequest {
				c := valid
				c.DeviceID = "not-a-uuid"
				return c
			},
		},
		{
			name: "self gateway",
			build: func() ReplaceDeviceHardwareRequest {
				c := valid
				c.NewGatewayDeviceID = &selfGateway
				return c
			},
		},
		{
			name: "invalid source key",
			build: func() ReplaceDeviceHardwareRequest {
				c := valid
				c.NewSourceKey = &badSourceKey
				return c
			},
		},
		{
			name: "oversized protocol address",
			build: func() ReplaceDeviceHardwareRequest {
				c := valid
				c.NewProtocolAddress = oversized
				return c
			},
		},
		{
			name: "zero expected revision",
			build: func() ReplaceDeviceHardwareRequest {
				c := valid
				c.Meta.ExpectedRevision = 0
				return c
			},
		},
		{
			name: "missing reason",
			build: func() ReplaceDeviceHardwareRequest {
				c := valid
				c.Meta.Reason = "   "
				return c
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.build().Validate(); !errors.Is(err, ErrInvalidMutation) {
				t.Fatalf("expected ErrInvalidMutation, got %v", err)
			}
		})
	}
}

func TestServerReplaceDeviceHardwareHTTP(t *testing.T) {
	newExternalID := "SN-NEW-9001"
	prevExternalID := "SN-OLD-1001"
	gwID := "018f1e00-4000-7000-8000-000000000100"
	backend := &memoryHardwareReplacementStore{
		memoryScheduleBackend: newMemoryScheduleBackend(),
		res: ReplaceDeviceHardwareResult{
			ReplacementID:           "019a0000-0000-7000-8000-000000000001",
			TenantID:                testTenantA,
			SiteID:                  testSiteA1,
			DeviceID:                testDeviceA1,
			Revision:                2,
			PreviousExternalID:      &prevExternalID,
			NewExternalID:           newExternalID,
			PreviousGatewayDeviceID: &gwID,
			NewGatewayDeviceID:      &gwID,
			Reason:                  "损坏更换新表",
			ReplacedBy:              "tech-01",
			ReplacedAt:              "2026-10-12T08:00:00.000Z",
			Device: Device{
				ID:          testDeviceA1,
				TenantID:    testTenantA,
				SiteID:      testSiteA1,
				Code:        "hvac-meter",
				DisplayName: "空调总电表",
				DeviceType:  "POWER_METER",
				Status:      "ACTIVE",
				Revision:    2,
				CreatedAt:   "2026-07-22T00:00:00.000Z",
				UpdatedAt:   "2026-10-12T08:00:00.000Z",
				ExternalID:  &newExternalID,
			},
		},
	}
	client := newScheduleHTTPClientWithInterfaces(t, backend, backend)
	writeClaims := testGrantClaims(registryauth.ActionDeviceWrite)
	writeClaims.AllowedSiteIDs = []string{testSiteA1}
	writeClaims.Subject = "tech-01"

	route := "/internal/v1/registry/sites/" + testSiteA1 + "/devices/" + testDeviceA1 + "/replace-hardware"
	body := map[string]any{
		"newExternalId": newExternalID,
		"meta": map[string]any{
			"expectedRevision": 1,
			"idempotencyKey":   "hw-replace-http-01",
			"reason":           "损坏更换新表",
		},
	}

	// 1. Success -> 201 Created, preserves logical device ID.
	rec := client.do(t, http.MethodPost, route, writeClaims, "张工", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d want=201 body=%s", rec.Code, rec.Body.String())
	}
	var got ReplaceDeviceHardwareResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.DeviceID != testDeviceA1 || got.Device.ID != testDeviceA1 || got.Revision != 2 || got.NewExternalID != newExternalID {
		t.Fatalf("unexpected response: %#v", got)
	}

	// 2. Idempotent replay -> 200 OK with X-Idempotent-Replay header.
	backend.res.Replayed = true
	replayRec := client.do(t, http.MethodPost, route, writeClaims, "张工", body)
	if replayRec.Code != http.StatusOK || replayRec.Header().Get("X-Idempotent-Replay") != "true" {
		t.Fatalf("replay status=%d header=%q body=%s", replayRec.Code, replayRec.Header().Get("X-Idempotent-Replay"), replayRec.Body.String())
	}

	// 3. Revision conflict -> 409 REGISTRY_REVISION_CONFLICT.
	backend.res.Replayed = false
	backend.err = ErrRevisionConflict
	conflictRec := client.do(t, http.MethodPost, route, writeClaims, "张工", body)
	if conflictRec.Code != http.StatusConflict || !strings.Contains(conflictRec.Body.String(), "REGISTRY_REVISION_CONFLICT") {
		t.Fatalf("conflict status=%d body=%s", conflictRec.Code, conflictRec.Body.String())
	}

	// 4. Not found / cross-site -> 404.
	backend.err = ErrNotFound
	notFoundRec := client.do(t, http.MethodPost, route, writeClaims, "张工", body)
	if notFoundRec.Code != http.StatusNotFound {
		t.Fatalf("not found status=%d body=%s", notFoundRec.Code, notFoundRec.Body.String())
	}

	// 5. Missing DeviceWrite capability -> 403.
	readOnlyClaims := writeClaims
	readOnlyClaims.Actions = []registryauth.Action{registryauth.ActionDeviceRead}
	deniedRec := client.do(t, http.MethodPost, route, readOnlyClaims, "只读成员", body)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("read-only status=%d want=403 body=%s", deniedRec.Code, deniedRec.Body.String())
	}
}
