package adapter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type tokenProviderFunc func(context.Context) (string, error)

func (function tokenProviderFunc) Token(ctx context.Context) (string, error) {
	return function(ctx)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestThingsBoardFetchTimeseriesUsesRangeAndJWT(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/plugins/telemetry/DEVICE/tb-device-1/values/timeseries" {
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("X-Authorization") != "Bearer test-jwt-value-123456" {
			t.Errorf("unexpected authorization header")
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		query := request.URL.Query()
		if query.Get("keys") != "powerKw,runState" || query.Get("agg") != "NONE" || query.Get("orderBy") != "ASC" || query.Get("limit") != "100" {
			t.Errorf("unexpected query: %s", request.URL.RawQuery)
			http.Error(writer, "bad query", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"powerKw":[{"ts":2000,"value":"12.5"},{"ts":1000,"value":"10.5"}],"runState":[{"ts":2000,"value":"RUNNING"}]}`)
	}))
	defer server.Close()

	client, err := NewThingsBoardClient(server.URL, tokenProviderFunc(func(context.Context) (string, error) {
		return "test-jwt-value-123456", nil
	}), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.FetchTimeseries(context.Background(), "tb-device-1", []string{"powerKw", "runState"}, time.UnixMilli(500), time.UnixMilli(2500), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(result["powerKw"]) != 2 || result["powerKw"][0].Timestamp != 1000 || string(result["powerKw"][1].Value) != `"12.5"` {
		t.Fatalf("unexpected sorted timeseries: %#v", result)
	}
}

func TestThingsBoardTransportFailureDoesNotExposeJWT(t *testing.T) {
	const secretMarker = "[REDACTED_SECRET]"
	client, err := NewThingsBoardClient("https://thingsboard.invalid", tokenProviderFunc(func(context.Context) (string, error) {
		return secretMarker, nil
	}), &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed for " + request.URL.String())
	})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.FetchTimeseries(context.Background(), "tb-device-1", []string{"powerKw"}, time.Now().Add(-time.Minute), time.Now(), 10)
	if err == nil || strings.Contains(err.Error(), secretMarker) {
		t.Fatalf("expected redacted provider error, got %v", err)
	}
}
