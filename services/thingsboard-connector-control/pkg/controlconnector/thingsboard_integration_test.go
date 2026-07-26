package controlconnector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestLocalThingsBoardTwoWaySetpointContract(t *testing.T) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("S3_THINGSBOARD_URL")), "/")
	username := strings.TrimSpace(os.Getenv("S3_THINGSBOARD_USERNAME"))
	password := os.Getenv("S3_THINGSBOARD_PASSWORD")
	if baseURL == "" || username == "" || password == "" {
		t.Skip("local ThingsBoard integration environment is not configured")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	credential := thingsBoardLogin(t, client, baseURL, username, password)
	deviceID, deviceAccess := createThingsBoardTestDevice(t, client, baseURL, credential)
	defer deleteThingsBoardTestDevice(t, client, baseURL, credential, deviceID)

	deviceContext, cancelDevice := context.WithCancel(t.Context())
	defer cancelDevice()
	ready := make(chan struct{})
	received := make(chan localRPCCommand, 1)
	deviceErr := make(chan error, 1)
	go func() {
		deviceErr <- runLocalHTTPDevice(deviceContext, client, baseURL, deviceAccess, ready, received)
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("local ThingsBoard device did not begin RPC polling")
	}
	time.Sleep(500 * time.Millisecond)

	evidence := &memoryEvidenceStore{}
	connector, err := NewThingsBoard(ThingsBoardConfig{
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		TargetResolver: staticTargetResolver{target: Target{
			IntegrationID:    "thingsboard-ce-local-4.3.1.3",
			ExternalDeviceID: deviceID,
			BindingRevision:  "local-device-binding:v1",
		}},
		CredentialProvider: staticCredentialProvider{value: credential},
		EvidenceStore:      evidence,
		Mappings: []Mapping{{
			Capability:         commandmodel.CapabilitySetTemperatureSetpoint,
			CapabilityRevision: "capability:set-temperature-setpoint:v1",
			MappingRevision:    "thingsboard-ce-4.3.1.3:setTemperatureSetpoint:v1",
			Status:             MappingLocalVerified,
			Method:             "setTemperatureSetpoint",
			Timeout:            5 * time.Second,
		}},
		AllowLocalVerified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope := testEnvelope(100, 24.5)
	result, err := connector.Execute(t.Context(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if result.Phase != commandmodel.ConnectorAcknowledged || !result.Acknowledged || result.Verified {
		var observed any = "no command observed"
		select {
		case command := <-received:
			observed = command
		case <-time.After(time.Second):
		}
		var emulatorResult any = "device emulator still polling"
		select {
		case err := <-deviceErr:
			emulatorResult = err
		default:
		}
		t.Fatalf("unexpected local ThingsBoard result %#v observed=%#v emulator=%#v", result, observed, emulatorResult)
	}
	select {
	case command := <-received:
		if command.Method != "setTemperatureSetpoint" || command.SetpointC != 24.5 {
			t.Fatalf("unexpected local RPC command %#v", command)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("local device did not receive the setpoint RPC")
	}
	cancelDevice()
	select {
	case err := <-deviceErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("local device emulator did not stop")
	}
	if len(evidence.completed) != 1 || evidence.completed[0].ProviderStatusCode != http.StatusOK || !evidence.completed[0].RequestWritten {
		t.Fatalf("unexpected local provider evidence %#v", evidence.completed)
	}
}

type localRPCCommand struct {
	Method    string
	SetpointC float64
}

type loginResponse struct {
	Credential string `json:"token"`
}

type thingsBoardDevice struct {
	ID struct {
		ID string `json:"id"`
	} `json:"id"`
}

type thingsBoardDeviceCredentials struct {
	CredentialsType string `json:"credentialsType"`
	CredentialsID   string `json:"credentialsId"`
}

func thingsBoardLogin(t *testing.T, client *http.Client, baseURL, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	response := doThingsBoardRequest(t, client, http.MethodPost, baseURL+"/api/auth/login", "", body)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		value, _ := io.ReadAll(response.Body)
		t.Fatalf("ThingsBoard login status=%d body=%s", response.StatusCode, value)
	}
	var output loginResponse
	if json.NewDecoder(response.Body).Decode(&output) != nil || output.Credential == "" {
		t.Fatal("ThingsBoard login response is invalid")
	}
	return output.Credential
}

func createThingsBoardTestDevice(t *testing.T, client *http.Client, baseURL, credential string) (string, string) {
	t.Helper()
	name := fmt.Sprintf("hvac-s3-rpc-%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]any{"name": name, "type": "default", "label": "S3 local RPC contract fixture"})
	response := doThingsBoardRequest(t, client, http.MethodPost, baseURL+"/api/device", credential, body)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		value, _ := io.ReadAll(response.Body)
		t.Fatalf("create ThingsBoard device status=%d body=%s", response.StatusCode, value)
	}
	var device thingsBoardDevice
	if json.NewDecoder(response.Body).Decode(&device) != nil || device.ID.ID == "" {
		t.Fatal("created ThingsBoard device response is invalid")
	}
	credentialsResponse := doThingsBoardRequest(t, client, http.MethodGet, baseURL+"/api/device/"+url.PathEscape(device.ID.ID)+"/credentials", credential, nil)
	defer credentialsResponse.Body.Close()
	if credentialsResponse.StatusCode != http.StatusOK {
		value, _ := io.ReadAll(credentialsResponse.Body)
		t.Fatalf("read ThingsBoard device credentials status=%d body=%s", credentialsResponse.StatusCode, value)
	}
	var credentials thingsBoardDeviceCredentials
	if json.NewDecoder(credentialsResponse.Body).Decode(&credentials) != nil || credentials.CredentialsType != "ACCESS_TOKEN" || credentials.CredentialsID == "" {
		t.Fatal("ThingsBoard device access credential response is invalid")
	}
	return device.ID.ID, credentials.CredentialsID
}

func deleteThingsBoardTestDevice(t *testing.T, client *http.Client, baseURL, credential, deviceID string) {
	t.Helper()
	response := doThingsBoardRequest(t, client, http.MethodDelete, baseURL+"/api/device/"+url.PathEscape(deviceID), credential, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		value, _ := io.ReadAll(response.Body)
		t.Errorf("delete ThingsBoard device status=%d body=%s", response.StatusCode, value)
	}
}

func doThingsBoardRequest(t *testing.T, client *http.Client, method, endpoint, credential string, body []byte) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, endpoint, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	if credential != "" {
		request.Header.Set("X-Authorization", providerAuthorization(credential))
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func runLocalHTTPDevice(ctx context.Context, client *http.Client, baseURL, accessCredential string, ready chan<- struct{}, received chan<- localRPCCommand) error {
	readyOnce := sync.Once{}
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/"+url.PathEscape(accessCredential)+"/rpc?timeout=20000", nil)
		if err != nil {
			return err
		}
		trace := &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				readyOnce.Do(func() { close(ready) })
			}
		}}
		request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
		response, err := client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		response.Body.Close()
		if readErr != nil {
			return readErr
		}
		if response.StatusCode != http.StatusOK || len(bytes.TrimSpace(body)) == 0 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		var command struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
			Params struct {
				SetpointC float64 `json:"setpointC"`
			} `json:"params"`
		}
		if json.Unmarshal(body, &command) != nil || command.ID == nil || *command.ID < 0 || command.Method == "" {
			return fmt.Errorf("invalid ThingsBoard RPC command %s", body)
		}
		select {
		case received <- localRPCCommand{Method: command.Method, SetpointC: command.Params.SetpointC}:
		case <-ctx.Done():
			return ctx.Err()
		}
		reply, _ := json.Marshal(map[string]any{"success": true, "appliedSetpointC": command.Params.SetpointC})
		replyRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/"+url.PathEscape(accessCredential)+"/rpc/"+fmt.Sprint(*command.ID), bytes.NewReader(reply))
		if err != nil {
			return err
		}
		replyRequest.Header.Set("Content-Type", "application/json")
		replyResponse, err := client.Do(replyRequest)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, replyResponse.Body)
		replyResponse.Body.Close()
		if replyResponse.StatusCode < 200 || replyResponse.StatusCode >= 300 {
			return fmt.Errorf("ThingsBoard RPC reply status %d", replyResponse.StatusCode)
		}
		return nil
	}
}
