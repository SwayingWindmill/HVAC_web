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
		IntegrationInstanceID: candidate.IntegrationInstanceID,
		SourcePath:            string(candidate.SourcePath),
		ExternalEntityType:    candidate.ExternalEntityType,
		ExternalID:            candidate.ExternalID,
		TelemetryKey:          candidate.TelemetryKey,
		Value:                 candidate.Value,
		ValueType:             candidate.ValueType,
		Unit:                  candidate.Unit,
		SampledAt:             candidate.SampledAt.UTC().Format(time.RFC3339Nano),
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
		SourceAuthenticator: NewStaticSourceAuthenticator(map[string][]string{
			thingsBoardSPIFFE: {candidate.IntegrationInstanceID},
		}),
		Now: func() time.Time { return candidate.ReceivedAt },
	})
	request := httptest.NewRequest(http.MethodPost, InternalSourceObservationPath, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.TLS = verifiedTLSState(thingsBoardSPIFFE)
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
