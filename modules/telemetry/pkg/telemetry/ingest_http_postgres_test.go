package telemetry

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func acceptObservationViaHTTP(t *testing.T, store ObservationAcceptor, candidate ObservationCandidate) ObservationReceipt {
	t.Helper()
	body, err := json.Marshal(sourceObservationRequest{
		SourceID:           candidate.SourceID,
		SourcePath:         string(candidate.SourcePath),
		ExternalEntityType: candidate.ExternalEntityType,
		ExternalID:         candidate.ExternalID,
		Device:             resolvedDeviceJSON(candidate.Device),
		Point:              resolvedPointJSON(candidate.Point),
		TelemetryKey:       candidate.TelemetryKey,
		Value:              candidate.Value,
		ValueType:          candidate.ValueType,
		Unit:               candidate.Unit,
		SampledAt:          candidate.SampledAt.UTC().Format(time.RFC3339Nano),
		SourcePosition: sourcePositionRequest{
			Partition: candidate.Position.Partition,
			Offset:    candidate.Position.Offset,
			EventID:   candidate.Position.EventID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(ServerConfig{
		ObservationAcceptor: store,
		AllowedSourceSPIFFE: mqttSourceSPIFFE,
		Now:                 func() time.Time { return candidate.ReceivedAt },
	})
	request := httptest.NewRequest(http.MethodPost, InternalSourceObservationPath, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.TLS = verifiedTLSState(mqttSourceSPIFFE)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("source HTTP status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var receipt ObservationReceipt
	if err := json.Unmarshal(recorder.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func resolvedDeviceJSON(device *ResolvedDevice) *resolvedDeviceRequest {
	if device == nil {
		return nil
	}
	return &resolvedDeviceRequest{TenantID: device.TenantID, SiteID: device.SiteID, DeviceID: device.DeviceID}
}

func resolvedPointJSON(point *ResolvedPoint) *resolvedPointRequest {
	if point == nil {
		return nil
	}
	return &resolvedPointRequest{
		PointID: point.PointID, SensorID: point.SensorID, PointType: point.PointType, ValueType: point.ValueType, Unit: point.Unit,
		CounterDecreaseMode: point.CounterDecreaseMode, CounterRolloverModulus: point.CounterRolloverModulus, PointRevision: point.PointRevision,
	}
}
