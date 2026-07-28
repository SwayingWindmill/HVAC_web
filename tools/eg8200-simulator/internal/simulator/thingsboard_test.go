package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestThingsBoardPublishTelemetryUsesDeviceTokenAndTimestampedPayload(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/token-1/telemetry" {
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
			http.Error(writer, "unexpected request", http.StatusBadRequest)
			return
		}
		if request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content type %q", request.Header.Get("Content-Type"))
			http.Error(writer, "unexpected content type", http.StatusBadRequest)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Errorf("decode telemetry: %v", err)
			http.Error(writer, "invalid telemetry", http.StatusBadRequest)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewThingsBoardClient(server.URL, map[string]string{"CHILLER-01": "token-1"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	observedAt := time.Date(2026, 7, 28, 8, 0, 1, 123000000, time.UTC)
	if err := client.PublishTelemetry(context.Background(), "CHILLER-01", observedAt, DeviceTelemetry{"powerKw": 123.4}); err != nil {
		t.Fatal(err)
	}
	if received["ts"].(float64) != float64(observedAt.UnixMilli()) {
		t.Fatalf("unexpected timestamp %#v", received["ts"])
	}
	values := received["values"].(map[string]any)
	if values["powerKw"].(float64) != 123.4 {
		t.Fatalf("unexpected telemetry %#v", values)
	}
}

func TestThingsBoardPollAndReplyRPC(t *testing.T) {
	var mu sync.Mutex
	var reply CommandResult
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/token-1/rpc":
			if request.URL.Query().Get("timeout") != "20000" {
				t.Errorf("unexpected timeout %q", request.URL.Query().Get("timeout"))
				http.Error(writer, "unexpected timeout", http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(writer, `{"id":17,"method":"setChilledWaterTemperatureSetpoint","params":{"setpointC":8.5}}`)
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/token-1/rpc/17":
			mu.Lock()
			defer mu.Unlock()
			if err := json.NewDecoder(request.Body).Decode(&reply); err != nil {
				t.Errorf("decode reply: %v", err)
				http.Error(writer, "invalid reply", http.StatusBadRequest)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := NewThingsBoardClient(server.URL, map[string]string{"CHILLER-01": "token-1"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	command, err := client.PollRPC(context.Background(), "CHILLER-01", 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if command == nil || command.ID != 17 || command.Method != "setChilledWaterTemperatureSetpoint" || command.Params["setpointC"] != 8.5 {
		t.Fatalf("unexpected command %#v", command)
	}
	result := CommandResult{Success: true, Code: "APPLIED", AppliedValue: 8.5, BusinessRevision: 2}
	if err := client.ReplyRPC(context.Background(), command.DeviceID, command.ID, result); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if reply != result {
		t.Fatalf("unexpected reply %#v", reply)
	}
}

func TestThingsBoardPollRejectsNonNumericParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, `{"id":1,"method":"setFrequency","params":{"frequencyHz":"fast"}}`)
	}))
	defer server.Close()
	client, err := NewThingsBoardClient(server.URL, map[string]string{"CHWP-01": "token"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.PollRPC(context.Background(), "CHWP-01", time.Second)
	if err == nil || !strings.Contains(err.Error(), "decode ThingsBoard RPC params") {
		t.Fatalf("expected strict params error, got %v", err)
	}
}

func TestThingsBoardErrorsDoNotExposeUnboundedResponseBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(writer, strings.Repeat("x", 1024))
	}))
	defer server.Close()
	client, err := NewThingsBoardClient(server.URL, map[string]string{"METER-01": "token"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = client.PublishTelemetry(context.Background(), "METER-01", time.Now(), DeviceTelemetry{"powerKw": 1})
	if err == nil || len(err.Error()) > 700 {
		t.Fatalf("expected bounded provider error, got %v", err)
	}
}

func TestThingsBoardTransportErrorsDoNotExposeAccessTokens(t *testing.T) {
	const tokenMarker = "[REDACTED_SECRET]"
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed")
	})}
	client, err := NewThingsBoardClient("https://thingsboard.invalid", map[string]string{"CHILLER-01": tokenMarker}, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	err = client.PublishTelemetry(context.Background(), "CHILLER-01", time.Now(), DeviceTelemetry{"powerKw": 1})
	if err == nil {
		t.Fatal("expected provider request failure")
	}
	if strings.Contains(err.Error(), tokenMarker) {
		t.Fatalf("provider error exposed access token: %v", err)
	}
}
