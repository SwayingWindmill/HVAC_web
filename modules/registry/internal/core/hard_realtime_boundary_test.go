package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func TestPointMutationAndTemplateEnforceHardRealtimeSafetyBoundary(t *testing.T) {
	t.Parallel()
	stopProfile, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilityStop)

	// 1. Read-only hardware safety observation point is permitted.
	obs := PointMutation{
		SiteID:            testSiteA1,
		ReportingDeviceID: testDeviceA1,
		PointCode:         "plc_estop_trip",
		SourceKey:         "estop_coil_in",
		DisplayName:       "PLC急停状态",
		PointType:         "STATE",
		ValueType:         "BOOLEAN",
		Unit:              "bool",
		Writable:          false,
		SampleIntervalMS:  1000,
		PublishIntervalMS: 5000,
		StaleAfterMS:      15000,
		SourceMetadata:    map[string]any{"safetyBoundary": "HARD_REALTIME_SAFETY"},
		Status:            "ACTIVE",
		Meta:              MutationMeta{IdempotencyKey: "k-obs-0001", Reason: "observe estop"},
	}
	if err := obs.Validate(); err != nil {
		t.Fatalf("expected read-only STATE safety observation point to validate, got %v", err)
	}

	// 2. emergency_stop_all remains allowed via explicit exclusion.
	estopAll := PointMutation{
		SiteID:            testSiteA1,
		ReportingDeviceID: testDeviceA1,
		PointCode:         "emergency_stop_all",
		SourceKey:         "all_do_release",
		DisplayName:       "全站一键紧急停机",
		PointType:         "COMMAND",
		ValueType:         "BOOLEAN",
		Unit:              "bool",
		Writable:          true,
		SampleIntervalMS:  1000,
		PublishIntervalMS: 5000,
		StaleAfterMS:      15000,
		SourceMetadata:    map[string]any{"capability": "STOP", "capabilityRevision": stopProfile.Revision, "controlKind": "ACTION", "actuationMode": "EMERGENCY_DO_RELEASE"},
		Status:            "ACTIVE",
		Meta:              MutationMeta{IdempotencyKey: "k-estop-0001", Reason: "emergency release"},
	}
	if err := estopAll.Validate(); err != nil {
		t.Fatalf("expected emergency_stop_all point to remain valid, got %v", err)
	}

	// 3. Writable / COMMAND / SETTING hard-realtime points fail with ErrHardRealtimeSafetyBoundary.
	blockedCmd := estopAll
	blockedCmd.PointCode = "fire_interlock_reset"
	blockedCmd.SourceKey = "fire_coil_out"
	blockedCmd.SourceMetadata = map[string]any{"capability": "RESET_FAULT", "capabilityRevision": stopProfile.Revision, "controlKind": "ACTION"}
	err := blockedCmd.Validate()
	if !errors.Is(err, edgecontrol.ErrHardRealtimeSafetyBoundary) {
		t.Fatalf("PointMutation.Validate() error = %v, want ErrHardRealtimeSafetyBoundary", err)
	}

	// 4. Server maps ErrHardRealtimeSafetyBoundary to HTTP 400 HARD_REALTIME_SAFETY_BOUNDARY.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/internal/registry/sites/"+testSiteA1+"/points", nil)
	(&server{}).writeMutationError(rec, req, fmt.Errorf("%w: %w", ErrInvalidMutation, edgecontrol.ErrHardRealtimeSafetyBoundary))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), edgecontrol.ReasonHardRealtimeSafetyBoundary) {
		t.Fatalf("expected HTTP 400 HARD_REALTIME_SAFETY_BOUNDARY, got status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 5. Template activation validation rejects writable safety standardPoints and Modbus writeFunctionCode.
	badStdTemplate := map[string]any{
		"deviceType": "CHILLER",
		"standardPoints": []any{
			map[string]any{
				"pointCode":         "high_pressure_trip",
				"displayName":       "高压保护开关",
				"pointType":         "SETTING",
				"valueType":         "NUMBER",
				"unit":              "kPa",
				"writable":          true,
				"sampleIntervalMs":  1000,
				"publishIntervalMs": 5000,
				"staleAfterMs":      15000,
			},
		},
	}
	if _, err := parseAndValidateTemplatePayload(TemplateDevice, badStdTemplate); !errors.Is(err, edgecontrol.ErrHardRealtimeSafetyBoundary) {
		t.Fatalf("template standardPoints hard-realtime error = %v, want ErrHardRealtimeSafetyBoundary", err)
	}

	badModbusTemplate := map[string]any{
		"deviceType": "PLC",
		"modbusDriver": map[string]any{
			"driverType": "MODBUS_TCP",
			"registers": []any{
				map[string]any{
					"pointCode":         "plc_estop_trip",
					"functionCode":      3,
					"writeFunctionCode": 6,
					"address":           10,
					"registerCount":     1,
					"rawType":           "UINT16",
					"byteOrder":         "BIG_ENDIAN",
					"wordOrder":         "BIG_ENDIAN",
					"scale":             1.0,
					"offset":            0.0,
					"pollPriority":      "VERY_HIGH",
				},
			},
		},
	}
	if _, err := parseAndValidateTemplatePayload(TemplateDevice, badModbusTemplate); !errors.Is(err, edgecontrol.ErrHardRealtimeSafetyBoundary) {
		t.Fatalf("template modbusDriver hard-realtime error = %v, want ErrHardRealtimeSafetyBoundary", err)
	}

	// 6. CommandDispatchContext JSON preserves SafetyBoundary field.
	ctxView := CommandDispatchContext{
		TenantID:       testTenantA,
		SiteID:         testSiteA1,
		DeviceID:       testDeviceA1,
		PointID:        "pt-1",
		ControlMode:    SiteControlModeRemoteAuto,
		SafetyBoundary: string(edgecontrol.SafetyBoundaryHardRealtime),
	}
	raw, err := json.Marshal(ctxView)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"safetyBoundary":"HARD_REALTIME_SAFETY"`) {
		t.Fatalf("expected safetyBoundary JSON field, got %s", string(raw))
	}
}
