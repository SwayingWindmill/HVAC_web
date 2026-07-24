package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

type centrifugoRoundTripFunc func(*http.Request) (*http.Response, error)

func (function centrifugoRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestCentrifugoTransportBoundsPublicationAndUsesServerAPI(t *testing.T) {
	var calls atomic.Int32
	client := NewBoundedCentrifugoHTTPClient(centrifugoRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.URL.Path != "/api/publish" || request.Header.Get("X-API-Key") != "fixture-api" {
			t.Fatalf("unexpected Centrifugo request path=%s headers=%v", request.URL.Path, request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || !json.Valid(body) || len(body) > maximumCentrifugoPublicationSize+4096 {
			t.Fatalf("invalid bounded publish body len=%d err=%v", len(body), err)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{\"result\":{}}")), Header: make(http.Header)}, nil
	}))
	transport, err := NewCentrifugoTransport(CentrifugoTransportConfig{BaseURL: "http://127.0.0.1:18000", APIKey: "fixture-api", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	publication := DeviceObservationPublication{SchemaVersion: 1, Kind: "DEVICE_OBSERVATION_DELTA", EventId: "event-1", SubscriptionId: telemetryapi.OpaqueSubscriptionId(strings.Repeat("s", 32)), DeviceId: realtimeTestDevice1, PreviousRevision: 1, Revision: 2, TelemetryChanges: []telemetryapi.TelemetryKeyState{}}
	if err := transport.Publish(context.Background(), "s2:fixture", publication); err != nil || calls.Load() != 1 {
		t.Fatalf("publish err=%v calls=%d", err, calls.Load())
	}

	publication.TelemetryChanges = []telemetryapi.TelemetryKeyState{{Present: &telemetryapi.TelemetryPresentState{Key: "temperature", State: "PRESENT", Value: json.RawMessage(`"` + strings.Repeat("x", maximumCentrifugoPublicationSize) + `"`), ValueType: "STRING", SampledAt: "2026-07-24T15:00:00.000Z", ReceivedAt: "2026-07-24T15:00:00.000Z", Freshness: "FRESH", Quality: telemetryapi.TelemetryQualityGood, QualityReasons: []telemetryapi.QualityReasonCode{}, PolicyRevision: 1}}}
	if err := transport.Publish(context.Background(), "s2:fixture", publication); !errors.Is(err, ErrRealtimeUnavailable) {
		t.Fatalf("oversized publication was not rejected: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("oversized publication reached Centrifugo: calls=%d", calls.Load())
	}
}
