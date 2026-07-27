package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	maximumBodyBytes       = int64(256 << 10)
	reportedStatePath      = "/internal/v1/commands/reported-state"
	setpointMethod         = "setTemperatureSetpoint"
	reportedStateKey       = "temperatureSetpointC"
	devicePollTimeout      = 20 * time.Second
	providerRequestTimeout = 35 * time.Second
)

type bridgeConfig struct {
	SchemaVersion       int            `json:"schemaVersion"`
	ThingsBoardBaseURL string         `json:"thingsBoardBaseUrl"`
	OrganizationID     string         `json:"organizationId"`
	SiteID             string         `json:"siteId"`
	Devices            []virtualDevice `json:"devices"`
}

type virtualDevice struct {
	Name                 string  `json:"name"`
	Type                 string  `json:"type"`
	PlatformDeviceID     string  `json:"platformDeviceId"`
	ThingsBoardDeviceID  string  `json:"thingsBoardDeviceId"`
	AccessToken          string  `json:"accessToken"`
	VerifierSPIFFE       string  `json:"verifierSpiffe"`
	InitialSetpointC     float64 `json:"initialSetpointC"`
	InitialRevision      uint64  `json:"initialRevision"`
}

type deviceState struct {
	device    virtualDevice
	mu        sync.RWMutex
	setpointC float64
	revision  uint64
	updatedAt time.Time
}

type bridge struct {
	config       bridgeConfig
	client       *http.Client
	byTBID       map[string]*deviceState
	bySPIFFE     map[string]*deviceState
	pollersReady atomic.Int32
}

type rpcRequest struct {
	Method  string `json:"method"`
	Params  struct {
		SetpointC float64 `json:"setpointC"`
	} `json:"params"`
	Timeout int64 `json:"timeout"`
}

type deviceRPC struct {
	ID     *int64 `json:"id"`
	Method string `json:"method"`
	Params struct {
		SetpointC float64 `json:"setpointC"`
	} `json:"params"`
}

type reportedStateResponse struct {
	SchemaVersion          int       `json:"schemaVersion"`
	EvidenceID             string    `json:"evidenceId"`
	OrganizationID         string    `json:"organizationId"`
	SiteID                 string    `json:"siteId"`
	DeviceID               string    `json:"deviceId"`
	EvaluationAvailability string    `json:"evaluationAvailability"`
	Presence               string    `json:"presence"`
	Readiness              string    `json:"readiness"`
	Freshness              string    `json:"freshness"`
	Quality                string    `json:"quality"`
	BusinessRevision       uint64    `json:"businessRevision"`
	ReportedSetpointC      float64   `json:"reportedSetpointC"`
	ObservedAt             time.Time `json:"observedAt"`
	ReportedStateKey       string    `json:"reportedStateKey"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	config, err := loadConfig(requiredEnv("S3_LOCAL_THINGSBOARD_CONFIG_FILE"))
	if err != nil {
		logger.Error("s3_local_thingsboard_config_invalid", "error", err.Error())
		os.Exit(1)
	}
	certificate, err := tls.LoadX509KeyPair(requiredEnv("S3_LOCAL_THINGSBOARD_TLS_CERT"), requiredEnv("S3_LOCAL_THINGSBOARD_TLS_KEY"))
	if err != nil {
		logger.Error("s3_local_thingsboard_tls_invalid")
		os.Exit(1)
	}
	caBody, err := os.ReadFile(requiredEnv("S3_LOCAL_THINGSBOARD_VERIFIER_CA"))
	if err != nil {
		logger.Error("s3_local_thingsboard_verifier_ca_unreadable")
		os.Exit(1)
	}
	verifierCAs := x509.NewCertPool()
	if !verifierCAs.AppendCertsFromPEM(caBody) {
		logger.Error("s3_local_thingsboard_verifier_ca_invalid")
		os.Exit(1)
	}

	bridge := newBridge(config)
	providerServer := &http.Server{
		Addr:              envOr("S3_LOCAL_THINGSBOARD_PROVIDER_ADDR", ":8448"),
		Handler:           http.HandlerFunc(bridge.proxyRPC),
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}},
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      40 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	reportedServer := &http.Server{
		Addr:    envOr("S3_LOCAL_THINGSBOARD_REPORTED_ADDR", ":8449"),
		Handler: http.HandlerFunc(bridge.reportedState),
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{certificate},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    verifierCAs,
		},
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	diagnosticsServer := &http.Server{
		Addr: envOr("S3_LOCAL_THINGSBOARD_DIAGNOSTICS_ADDR", ":19091"),
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/health/live" && request.URL.Path != "/health/ready" {
				http.NotFound(writer, request)
				return
			}
			if request.URL.Path == "/health/ready" && bridge.pollersReady.Load() != int32(len(config.Devices)) {
				writer.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("ok\n"))
		}),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	for _, state := range bridge.byTBID {
		go bridge.runVirtualDevice(ctx, logger, state)
	}
	errCh := make(chan error, 3)
	go func() { errCh <- providerServer.ListenAndServeTLS("", "") }()
	go func() { errCh <- reportedServer.ListenAndServeTLS("", "") }()
	go func() { errCh <- diagnosticsServer.ListenAndServe() }()
	logger.Info("s3_local_thingsboard_bridge_started", "device_count", len(config.Devices), "thingsboard", config.ThingsBoardBaseURL)

	select {
	case <-ctx.Done():
	case serveErr := <-errCh:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("s3_local_thingsboard_bridge_failed", "error", serveErr.Error())
		}
	}
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = providerServer.Shutdown(shutdownContext)
	_ = reportedServer.Shutdown(shutdownContext)
	_ = diagnosticsServer.Shutdown(shutdownContext)
}

func newBridge(config bridgeConfig) *bridge {
	result := &bridge{
		config:   config,
		client:   &http.Client{Timeout: providerRequestTimeout},
		byTBID:   make(map[string]*deviceState, len(config.Devices)),
		bySPIFFE: make(map[string]*deviceState, len(config.Devices)),
	}
	for _, device := range config.Devices {
		state := &deviceState{device: device, setpointC: device.InitialSetpointC, revision: device.InitialRevision, updatedAt: time.Now().UTC()}
		result.byTBID[device.ThingsBoardDeviceID] = state
		result.bySPIFFE[device.VerifierSPIFFE] = state
	}
	return result
}

func (bridge *bridge) proxyRPC(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || !strings.HasPrefix(request.URL.Path, "/api/rpc/twoway/") {
		http.NotFound(writer, request)
		return
	}
	thingsBoardDeviceID, err := url.PathUnescape(strings.TrimPrefix(request.URL.Path, "/api/rpc/twoway/"))
	if err != nil || thingsBoardDeviceID == "" || strings.Contains(thingsBoardDeviceID, "/") || bridge.byTBID[thingsBoardDeviceID] == nil {
		http.NotFound(writer, request)
		return
	}
	if !strings.HasPrefix(request.Header.Get("X-Authorization"), "Bearer ") {
		writeJSON(writer, http.StatusUnauthorized, map[string]any{"success": false})
		return
	}
	body, err := readBounded(request.Body, maximumBodyBytes)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"success": false})
		return
	}
	var input rpcRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureEOF(decoder) != nil || input.Method != setpointMethod || input.Params.SetpointC < 16 || input.Params.SetpointC > 30 || input.Timeout < 1000 || input.Timeout > 30000 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"success": false})
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), providerRequestTimeout)
	defer cancel()
	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, bridge.config.ThingsBoardBaseURL+request.URL.Path, bytes.NewReader(body))
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]any{"success": false})
		return
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Accept", "application/json")
	upstream.Header.Set("X-Authorization", request.Header.Get("X-Authorization"))
	response, err := bridge.client.Do(upstream)
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]any{"success": false})
		return
	}
	defer response.Body.Close()
	responseBody, readErr := readBounded(response.Body, maximumBodyBytes)
	if readErr != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]any{"success": false})
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(response.StatusCode)
	_, _ = writer.Write(responseBody)
}

func (bridge *bridge) reportedState(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || request.URL.Path != reportedStatePath {
		http.NotFound(writer, request)
		return
	}
	state := bridge.bySPIFFE[peerSPIFFE(request)]
	if state == nil {
		writeJSON(writer, http.StatusForbidden, map[string]string{"code": "VERIFIER_WORKLOAD_FORBIDDEN"})
		return
	}
	state.mu.RLock()
	setpointC := state.setpointC
	revision := state.revision
	device := state.device
	state.mu.RUnlock()
	observedAt := time.Now().UTC()
	evidenceInput := fmt.Sprintf("%s|%s|%s|%d|%.3f|%s", bridge.config.OrganizationID, bridge.config.SiteID, device.PlatformDeviceID, revision, setpointC, observedAt.Format(time.RFC3339Nano))
	digest := sha256.Sum256([]byte(evidenceInput))
	writeJSON(writer, http.StatusOK, reportedStateResponse{
		SchemaVersion:          1,
		EvidenceID:             "s2:sha256:" + hex.EncodeToString(digest[:]),
		OrganizationID:         bridge.config.OrganizationID,
		SiteID:                 bridge.config.SiteID,
		DeviceID:               device.PlatformDeviceID,
		EvaluationAvailability: "AVAILABLE",
		Presence:               "ONLINE",
		Readiness:              "CURRENT",
		Freshness:              "FRESH",
		Quality:                "GOOD",
		BusinessRevision:       revision,
		ReportedSetpointC:      setpointC,
		ObservedAt:             observedAt,
		ReportedStateKey:       reportedStateKey,
	})
}

func (bridge *bridge) runVirtualDevice(ctx context.Context, logger *slog.Logger, state *deviceState) {
	bridge.pollersReady.Add(1)
	for ctx.Err() == nil {
		requestContext, cancel := context.WithTimeout(ctx, devicePollTimeout+5*time.Second)
		request, err := http.NewRequestWithContext(requestContext, http.MethodGet, bridge.config.ThingsBoardBaseURL+"/api/v1/"+url.PathEscape(state.device.AccessToken)+"/rpc?timeout=20000", nil)
		if err != nil {
			cancel()
			return
		}
		response, err := bridge.client.Do(request)
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return
			}
			logger.Warn("virtual_device_poll_failed", "device", state.device.Name)
			time.Sleep(time.Second)
			continue
		}
		body, readErr := readBounded(response.Body, 64<<10)
		response.Body.Close()
		cancel()
		if readErr != nil || response.StatusCode != http.StatusOK || len(bytes.TrimSpace(body)) == 0 {
			continue
		}
		var command deviceRPC
		if json.Unmarshal(body, &command) != nil || command.ID == nil || *command.ID < 0 || command.Method == "" {
			logger.Warn("virtual_device_rpc_invalid", "device", state.device.Name)
			continue
		}
		success := command.Method == setpointMethod && command.Params.SetpointC >= 16 && command.Params.SetpointC <= 30
		if success {
			state.mu.Lock()
			state.setpointC = command.Params.SetpointC
			state.revision++
			state.updatedAt = time.Now().UTC()
			state.mu.Unlock()
			logger.Info("virtual_device_setpoint_applied", "device", state.device.Name, "setpoint_c", command.Params.SetpointC)
		}
		reply, _ := json.Marshal(map[string]any{"success": success, "appliedSetpointC": command.Params.SetpointC})
		replyContext, replyCancel := context.WithTimeout(ctx, 10*time.Second)
		replyRequest, err := http.NewRequestWithContext(replyContext, http.MethodPost, bridge.config.ThingsBoardBaseURL+"/api/v1/"+url.PathEscape(state.device.AccessToken)+"/rpc/"+fmt.Sprint(*command.ID), bytes.NewReader(reply))
		if err == nil {
			replyRequest.Header.Set("Content-Type", "application/json")
			replyResponse, replyErr := bridge.client.Do(replyRequest)
			if replyErr == nil {
				_, _ = io.Copy(io.Discard, replyResponse.Body)
				replyResponse.Body.Close()
			}
		}
		replyCancel()
	}
}

func loadConfig(path string) (bridgeConfig, error) {
	if !filepath.IsAbs(path) {
		return bridgeConfig{}, errors.New("bridge config path must be absolute")
	}
	body, err := os.ReadFile(path)
	if err != nil || len(body) == 0 || len(body) > 256<<10 {
		return bridgeConfig{}, errors.New("bridge config is unreadable")
	}
	var config bridgeConfig
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || ensureEOF(decoder) != nil || config.SchemaVersion != 1 || strings.TrimSpace(config.OrganizationID) == "" || strings.TrimSpace(config.SiteID) == "" || len(config.Devices) == 0 || len(config.Devices) > 16 {
		return bridgeConfig{}, errors.New("bridge config document is invalid")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(config.ThingsBoardBaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || !allowedLocalHost(parsed.Hostname()) {
		return bridgeConfig{}, errors.New("ThingsBoard base URL must be a local HTTP origin")
	}
	config.ThingsBoardBaseURL = baseURL
	seenPlatform := map[string]struct{}{}
	seenTB := map[string]struct{}{}
	seenSPIFFE := map[string]struct{}{}
	for _, device := range config.Devices {
		if strings.TrimSpace(device.Name) == "" || strings.TrimSpace(device.Type) == "" || strings.TrimSpace(device.PlatformDeviceID) == "" || strings.TrimSpace(device.ThingsBoardDeviceID) == "" || strings.TrimSpace(device.AccessToken) == "" || !strings.HasPrefix(device.VerifierSPIFFE, "spiffe://hvac.local/command-verifier/") || device.InitialSetpointC < 16 || device.InitialSetpointC > 30 || device.InitialRevision == 0 {
			return bridgeConfig{}, errors.New("virtual Device config is invalid")
		}
		if _, exists := seenPlatform[device.PlatformDeviceID]; exists {
			return bridgeConfig{}, errors.New("duplicate platform Device ID")
		}
		if _, exists := seenTB[device.ThingsBoardDeviceID]; exists {
			return bridgeConfig{}, errors.New("duplicate ThingsBoard Device ID")
		}
		if _, exists := seenSPIFFE[device.VerifierSPIFFE]; exists {
			return bridgeConfig{}, errors.New("duplicate Verifier SPIFFE ID")
		}
		seenPlatform[device.PlatformDeviceID] = struct{}{}
		seenTB[device.ThingsBoardDeviceID] = struct{}{}
		seenSPIFFE[device.VerifierSPIFFE] = struct{}{}
	}
	return config, nil
}

func allowedLocalHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "host.docker.internal", "127.0.0.1", "localhost":
		return true
	default:
		return false
	}
}

func peerSPIFFE(request *http.Request) string {
	if request == nil || request.TLS == nil || len(request.TLS.PeerCertificates) != 1 || request.TLS.PeerCertificates[0] == nil {
		return ""
	}
	leaf := request.TLS.PeerCertificates[0]
	if len(leaf.URIs) != 1 || leaf.URIs[0] == nil {
		return ""
	}
	return leaf.URIs[0].String()
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: maximum + 1}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maximum {
		return nil, errors.New("body exceeds size limit")
	}
	return body, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		_, _ = fmt.Fprintln(os.Stderr, name+" is required")
		os.Exit(1)
	}
	return value
}
