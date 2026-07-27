package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	maximumRequestBytes  = int64(16 << 10)
	maximumResponseBytes = int64(256 << 10)
	capabilityRevision   = "capability:set-temperature-setpoint:v1"
)

var (
	idempotencyPattern = regexp.MustCompile("^[A-Za-z0-9._:-]{8,256}$")
	commandIDPattern   = regexp.MustCompile("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")
)

type config struct {
	addr                     string
	publicOrigin             string
	commandServiceURL        string
	commandServiceServerName string
	organizationID           string
	siteID                   string
	deviceCatalog            []localDevice
	principalID              string
	approverID               string
	csrfToken                string
	policyRevision           string
	revocationRevision       uint64
	gatewaySPIFFE            string
	iamIssuer                string
	commandAudience          string
	client                   *http.Client
	grantSigner              crypto.Signer
	delegationSigner         crypto.Signer
	now                      func() time.Time
}

type localDevice struct {
	DeviceID string `json:"deviceId"`
	Name     string `json:"name"`
	Type     string `json:"type"`
}

type localDeviceCatalog struct {
	SchemaVersion int           `json:"schemaVersion"`
	Devices       []localDevice `json:"devices"`
}

type gateway struct {
	config config
}

type createCommandRequest struct {
	DeviceID   string                  `json:"deviceId"`
	Capability commandmodel.Capability `json:"capability"`
	Parameters struct {
		SetpointC float64 `json:"setpointC"`
	} `json:"parameters"`
}

type internalCreateCommandRequest struct {
	OrganizationID string                  `json:"organizationId"`
	SiteID         string                  `json:"siteId"`
	DeviceID       string                  `json:"deviceId"`
	PrincipalID    string                  `json:"principalId"`
	IdempotencyKey string                  `json:"idempotencyKey"`
	Capability     commandmodel.Capability `json:"capability"`
	SetpointC      float64                 `json:"setpointC"`
	CurrentState   struct {
		EvaluationAvailability string    `json:"evaluationAvailability"`
		Presence               string    `json:"presence"`
		Readiness              string    `json:"readiness"`
		Quality                string    `json:"quality"`
		BusinessRevision       uint64    `json:"businessRevision"`
		CurrentTemperatureC    float64   `json:"currentTemperatureC"`
		ObservedAt             time.Time `json:"observedAt"`
	} `json:"currentState"`
}

type internalApproveCommandRequest struct {
	OrganizationID string `json:"organizationId"`
	SiteID         string `json:"siteId"`
	DeviceID       string `json:"deviceId"`
	PrincipalID    string `json:"principalId"`
	ApproverRole   string `json:"approverRole"`
}

type commandProjection struct {
	DeviceID string `json:"deviceId"`
}

type problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Instance  string `json:"instance"`
	Code      string `json:"code"`
	TraceID   string `json:"traceId"`
	Retryable bool   `json:"retryable"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	loaded, err := loadConfig()
	if err != nil {
		logger.Error("s3_local_web_gateway_configuration_invalid", "error", err.Error())
		os.Exit(1)
	}
	handler := (&gateway{config: loaded}).handler()
	server := &http.Server{
		Addr:              loaded.addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	logger.Info("s3_local_web_gateway_started", "addr", loaded.addr, "formal_certification_claim", false)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case serveErr := <-errCh:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("s3_local_web_gateway_failed", "error", serveErr.Error())
		}
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownContext)
}

func loadConfig() (config, error) {
	certificate, err := tls.LoadX509KeyPair(requiredEnv("S3_LOCAL_WEB_GATEWAY_TLS_CERT"), requiredEnv("S3_LOCAL_WEB_GATEWAY_TLS_KEY"))
	if err != nil {
		return config{}, fmt.Errorf("load Gateway workload certificate: %w", err)
	}
	caBody, err := os.ReadFile(requiredEnv("S3_LOCAL_WEB_GATEWAY_SERVER_CA"))
	if err != nil {
		return config{}, fmt.Errorf("read Command Service CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caBody) {
		return config{}, errors.New("Command Service CA is invalid")
	}
	grantSigner, err := loadSigner(requiredEnv("S3_LOCAL_WEB_GATEWAY_GRANT_KEY"))
	if err != nil {
		return config{}, fmt.Errorf("load Command Grant signer: %w", err)
	}
	delegationSigner, err := loadSigner(requiredEnv("S3_LOCAL_WEB_GATEWAY_DELEGATION_KEY"))
	if err != nil {
		return config{}, fmt.Errorf("load Command delegation signer: %w", err)
	}
	csrfToken, err := loadSingleLineFile(requiredEnv("S3_LOCAL_WEB_GATEWAY_CSRF_FILE"), 4096)
	if err != nil {
		return config{}, fmt.Errorf("load CSRF token: %w", err)
	}
	serverName := envOr("S3_LOCAL_COMMAND_SERVICE_SERVER_NAME", "command-service.s3-local.svc.cluster.local")
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		ServerName:   serverName,
		Certificates: []tls.Certificate{certificate},
	}}
	deviceCatalog, err := loadDeviceCatalog(
		strings.TrimSpace(os.Getenv("S3_LOCAL_DEVICE_CATALOG_FILE")),
		requiredEnv("S3_LOCAL_DEVICE_ID"),
	)
	if err != nil {
		return config{}, fmt.Errorf("load local Device catalog: %w", err)
	}
	return config{
		addr:                     envOr("S3_LOCAL_WEB_GATEWAY_ADDR", ":8080"),
		publicOrigin:             requiredEnv("S3_LOCAL_WEB_PUBLIC_ORIGIN"),
		commandServiceURL:        strings.TrimRight(requiredEnv("S3_LOCAL_COMMAND_SERVICE_URL"), "/"),
		commandServiceServerName: serverName,
		organizationID:           requiredEnv("S3_LOCAL_ORGANIZATION_ID"),
		siteID:                   requiredEnv("S3_LOCAL_SITE_ID"),
		deviceCatalog:            deviceCatalog,
		principalID:              envOr("S3_LOCAL_PRINCIPAL_ID", "018f3e00-5000-7000-8000-000000000001"),
		approverID:               envOr("S3_LOCAL_APPROVER_ID", "018f3e00-5000-7000-8000-000000000002"),
		csrfToken:                csrfToken,
		policyRevision:           envOr("S3_LOCAL_POLICY_REVISION", "s3-local-policy-v1"),
		revocationRevision:       1,
		gatewaySPIFFE:            envOr("S3_LOCAL_GATEWAY_SPIFFE", "spiffe://hvac.local/platform-gateway"),
		iamIssuer:                envOr("S3_LOCAL_IAM_ISSUER", "spiffe://hvac.local/iam-service"),
		commandAudience:          envOr("S3_LOCAL_COMMAND_AUDIENCE", "command-service"),
		client:                   &http.Client{Transport: transport, Timeout: 10 * time.Second},
		grantSigner:              grantSigner,
		delegationSigner:         delegationSigner,
		now:                      time.Now,
	}, nil
}

func (g *gateway) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", g.health)
	mux.HandleFunc("GET /api/v1/version", g.version)
	mux.HandleFunc("GET /api/v1/platform/status", g.platformStatus)
	mux.HandleFunc("GET /api/v1/principal", g.principal)
	mux.HandleFunc("GET /api/v1/local/devices", g.localDevices)
	mux.HandleFunc("POST /api/v1/commands", g.createCommand)
	mux.HandleFunc("/api/v1/commands/", g.commandItem)
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		writeProblem(writer, request, http.StatusNotFound, "ROUTE_NOT_FOUND", "The requested local Gateway route does not exist.", false)
	})
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "private, no-store")
		writer.Header().Set("X-S3-Local-Only", "true")
		mux.ServeHTTP(writer, request)
	})
}

func (g *gateway) commandItem(writer http.ResponseWriter, request *http.Request) {
	raw := strings.TrimPrefix(request.URL.Path, "/api/v1/commands/")
	if raw == "" || strings.Contains(raw, "/") {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested Command was not found.", false)
		return
	}
	if strings.HasSuffix(raw, ":approve") {
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			writeProblem(writer, request, http.StatusMethodNotAllowed, "COMMAND_METHOD_NOT_ALLOWED", "The local approval route only accepts POST.", false)
			return
		}
		g.approveCommand(writer, request, strings.TrimSuffix(raw, ":approve"))
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeProblem(writer, request, http.StatusMethodNotAllowed, "COMMAND_METHOD_NOT_ALLOWED", "The local Command item route only accepts GET.", false)
		return
	}
	g.getCommand(writer, request, raw)
}

func (g *gateway) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"status": "ok", "service": "platform-gateway", "checkedAt": g.config.now().UTC().Format(time.RFC3339Nano),
		"build": map[string]string{"service": "platform-gateway", "version": "s3-local", "commit": "local", "builtAt": "local"},
	})
}

func (g *gateway) version(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"service": "platform-gateway", "version": "s3-local", "commit": "local", "builtAt": "local"})
}

func (g *gateway) platformStatus(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"status": "ok", "service": "platform-status", "implementation": "go", "version": "s3-local",
		"checkedAt": g.config.now().UTC().Format(time.RFC3339Nano), "routePolicyRevision": 1, "routeRevision": 1, "compatibilityMode": "native",
	})
}

func (g *gateway) principal(writer http.ResponseWriter, _ *http.Request) {
	now := g.config.now().UTC()
	expires := now.Add(8 * time.Hour)
	principal := map[string]any{
		"subject": g.config.principalID, "issuer": "https://s3-local.invalid", "displayName": "S3 Local Operator",
		"email": "s3-local@example.invalid", "roles": []string{"ops", "command-approver"},
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"principal": principal,
		"context": map[string]any{
			"initiatingPrincipal":       principal,
			"executingServicePrincipal": map[string]string{"service": "platform-gateway", "spiffeId": g.config.gatewaySPIFFE},
			"actingOrganizationId":      g.config.organizationID, "audience": "iam-service", "policyRevision": g.config.policyRevision,
			"delegationExpiresAt": expires.Format(time.RFC3339Nano),
		},
		"session": map[string]any{
			"id": "s3-local-session", "expiresAt": expires.Format(time.RFC3339Nano), "csrfToken": g.config.csrfToken,
			"revocationObjectiveMs": 0, "lastAuditMessageId": "s3-local-no-formal-audit",
		},
	})
}

func (g *gateway) localDevices(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, localDeviceCatalog{SchemaVersion: 1, Devices: g.config.deviceCatalog})
}

func (g *gateway) createCommand(writer http.ResponseWriter, request *http.Request) {
	if !g.validMutation(request) {
		writeProblem(writer, request, http.StatusForbidden, "CSRF_INVALID", "The local Session Origin or CSRF token is invalid.", false)
		return
	}
	if mediaType(request.Header.Get("Content-Type")) != "application/json" {
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "COMMAND_REQUEST_INVALID", "The Command request must use application/json.", false)
		return
	}
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if !idempotencyPattern.MatchString(idempotencyKey) {
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", "A valid Idempotency-Key is required.", false)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumRequestBytes)
	var input createCommandRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureEOF(decoder) != nil || !g.deviceAllowed(input.DeviceID) ||
		input.Capability != commandmodel.CapabilitySetTemperatureSetpoint || input.Parameters.SetpointC < 16 || input.Parameters.SetpointC > 30 {
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", "The local Command request is invalid.", false)
		return
	}

	grant, err := g.signGrant(commandmodel.AuthorizationCommandSubmit, g.config.principalID, input.DeviceID, commandmodel.RiskHigh)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", "The local Command Grant could not be issued.", true)
		return
	}
	now := g.config.now().UTC()
	upstream := internalCreateCommandRequest{
		OrganizationID: g.config.organizationID,
		SiteID:         g.config.siteID,
		DeviceID:       input.DeviceID,
		PrincipalID:    g.config.principalID,
		IdempotencyKey: idempotencyKey,
		Capability:     input.Capability,
		SetpointC:      input.Parameters.SetpointC,
	}
	upstream.CurrentState.EvaluationAvailability = "AVAILABLE"
	upstream.CurrentState.Presence = "ONLINE"
	upstream.CurrentState.Readiness = "CURRENT"
	upstream.CurrentState.Quality = "GOOD"
	upstream.CurrentState.BusinessRevision = 21
	upstream.CurrentState.CurrentTemperatureC = 23
	upstream.CurrentState.ObservedAt = now.Add(-time.Second)
	g.proxyJSON(writer, request, http.MethodPost, "/internal/v1/commands", upstream, map[string]string{"X-Command-Grant": grant})
}

func (g *gateway) getCommand(writer http.ResponseWriter, request *http.Request, commandID string) {
	if !commandIDPattern.MatchString(commandID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested Command was not found.", false)
		return
	}
	delegation, err := g.signReadDelegation(commandID)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", "The local read delegation could not be issued.", true)
		return
	}
	g.proxyJSON(writer, request, http.MethodGet, "/internal/v1/commands/"+url.PathEscape(commandID), nil, map[string]string{
		"X-Command-Read-Context":   delegation,
		"X-Acting-Organization-ID": g.config.organizationID,
	})
}

func (g *gateway) approveCommand(writer http.ResponseWriter, request *http.Request, commandID string) {
	if !g.validMutation(request) {
		writeProblem(writer, request, http.StatusForbidden, "CSRF_INVALID", "The local Session Origin or CSRF token is invalid.", false)
		return
	}
	if !commandIDPattern.MatchString(commandID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested Command was not found.", false)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 1024)
	var empty struct{}
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&empty) != nil || ensureEOF(decoder) != nil {
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_APPROVAL_REQUEST_INVALID", "The approval request must be an empty JSON object.", false)
		return
	}
	projection, err := g.readCommandProjection(request.Context(), commandID)
	if err != nil || !g.deviceAllowed(projection.DeviceID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested Command was not found.", false)
		return
	}
	grant, err := g.signGrant(commandmodel.AuthorizationCommandApprove, g.config.approverID, projection.DeviceID, commandmodel.RiskHigh)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", "The local approval Grant could not be issued.", true)
		return
	}
	upstream := internalApproveCommandRequest{
		OrganizationID: g.config.organizationID,
		SiteID:         g.config.siteID,
		DeviceID:       projection.DeviceID,
		PrincipalID:    g.config.approverID,
		ApproverRole:   "s3-local-independent-approver",
	}
	g.proxyJSON(writer, request, http.MethodPost, "/internal/v1/commands/"+url.PathEscape(commandID)+":approve", upstream, map[string]string{"X-Command-Grant": grant})
}

func (g *gateway) deviceAllowed(deviceID string) bool {
	for _, device := range g.config.deviceCatalog {
		if device.DeviceID == deviceID {
			return true
		}
	}
	return false
}

func (g *gateway) readCommandProjection(ctx context.Context, commandID string) (commandProjection, error) {
	delegation, err := g.signReadDelegation(commandID)
	if err != nil {
		return commandProjection{}, err
	}
	requestContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, g.config.commandServiceURL+"/internal/v1/commands/"+url.PathEscape(commandID), nil)
	if err != nil {
		return commandProjection{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Command-Read-Context", delegation)
	request.Header.Set("X-Acting-Organization-ID", g.config.organizationID)
	response, err := g.config.client.Do(request)
	if err != nil {
		return commandProjection{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return commandProjection{}, errors.New("Command projection is unavailable")
	}
	limited := &io.LimitedReader{R: response.Body, N: maximumResponseBytes + 1}
	body, err := io.ReadAll(limited)
	if err != nil || int64(len(body)) > maximumResponseBytes {
		return commandProjection{}, errors.New("Command projection is invalid")
	}
	var projection commandProjection
	decoder := json.NewDecoder(bytes.NewReader(body))
	if decoder.Decode(&projection) != nil || projection.DeviceID == "" {
		return commandProjection{}, errors.New("Command projection is invalid")
	}
	return projection, nil
}

func (g *gateway) validMutation(request *http.Request) bool {
	return request.Header.Get("Origin") == g.config.publicOrigin && request.Header.Get("X-CSRF-Token") == g.config.csrfToken
}

func (g *gateway) signGrant(purpose commandmodel.AuthorizationPurpose, principalID, deviceID string, maximumRisk commandmodel.RiskLevel) (string, error) {
	now := g.config.now().UTC()
	grantID, err := randomIdentifier("grant")
	if err != nil {
		return "", err
	}
	tokenID, err := randomIdentifier("token")
	if err != nil {
		return "", err
	}
	return commandauth.SignGrant(g.config.grantSigner, commandauth.GrantClaims{
		Issuer:                      g.config.iamIssuer,
		Presenter:                   g.config.gatewaySPIFFE,
		Audience:                    g.config.commandAudience,
		GrantID:                     grantID,
		Purpose:                     purpose,
		PrincipalID:                 principalID,
		OrganizationID:              g.config.organizationID,
		SiteID:                      g.config.siteID,
		DeviceID:                    deviceID,
		Capability:                  commandmodel.CapabilitySetTemperatureSetpoint,
		MaximumRisk:                 maximumRisk,
		CapabilityRevision:          capabilityRevision,
		PolicyRevision:              g.config.policyRevision,
		EmergencyRevocationRevision: g.config.revocationRevision,
		IssuedAt:                    now.Unix(),
		ExpiresAt:                   now.Add(25 * time.Second).Unix(),
		TokenID:                     tokenID,
		Transitive:                  false,
	})
}

func (g *gateway) signReadDelegation(commandID string) (string, error) {
	now := g.config.now().UTC()
	tokenID, err := randomIdentifier("read")
	if err != nil {
		return "", err
	}
	return identitycontext.SignDelegation(g.config.delegationSigner, identitycontext.DelegationClaims{
		Issuer:               g.config.gatewaySPIFFE,
		Subject:              g.config.principalID,
		SubjectIssuer:        "https://s3-local.invalid",
		DisplayName:          "S3 Local Operator",
		Email:                "s3-local@example.invalid",
		Roles:                []string{"ops"},
		ExecutingService:     g.config.gatewaySPIFFE,
		Audience:             g.config.commandAudience,
		ActingOrganizationID: g.config.organizationID,
		Actions:              []string{"command:read"},
		Scopes:               []string{"organization:" + g.config.organizationID, "command:" + commandID},
		PolicyRevision:       g.config.policyRevision,
		SessionID:            "s3-local-session",
		IssuedAt:             now.Unix(),
		ExpiresAt:            now.Add(30 * time.Second).Unix(),
		TokenID:              tokenID,
	})
}

func (g *gateway) proxyJSON(writer http.ResponseWriter, incoming *http.Request, method, path string, body any, headers map[string]string) {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			writeProblem(writer, incoming, http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", "The local Gateway could not encode the Command request.", true)
			return
		}
	}
	ctx, cancel := context.WithTimeout(incoming.Context(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, g.config.commandServiceURL+path, bytes.NewReader(payload))
	if err != nil {
		writeProblem(writer, incoming, http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", "The local Gateway could not create the upstream request.", true)
		return
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := g.config.client.Do(request)
	if err != nil {
		writeProblem(writer, incoming, http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", "The local Command Service is unavailable.", true)
		return
	}
	defer response.Body.Close()
	limited := &io.LimitedReader{R: response.Body, N: maximumResponseBytes + 1}
	responseBody, err := io.ReadAll(limited)
	if err != nil || int64(len(responseBody)) > maximumResponseBytes {
		writeProblem(writer, incoming, http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", "The local Command Service response is invalid.", true)
		return
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "" {
		writer.Header().Set("Content-Type", contentType)
	}
	if location := response.Header.Get("Location"); location != "" {
		writer.Header().Set("Location", location)
	}
	writer.WriteHeader(response.StatusCode)
	_, _ = writer.Write(responseBody)
}

func loadSigner(path string) (crypto.Signer, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(body)
	if block == nil {
		return nil, errors.New("private key PEM is invalid")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("private key %T is not a signer", key)
	}
	return signer, nil
}

func loadSingleLineFile(path string, maximum int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	limited := &io.LimitedReader{R: file, N: maximum + 1}
	body, err := io.ReadAll(limited)
	if err != nil || int64(len(body)) > maximum {
		return "", errors.New("value file is too large")
	}
	value := strings.TrimSpace(string(body))
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("value file content is invalid")
	}
	return value, nil
}

func loadDeviceCatalog(path, fallbackDeviceID string) ([]localDevice, error) {
	if path == "" {
		if !commandmodel.IsUUIDv7(fallbackDeviceID) {
			return nil, errors.New("fallback Device ID is invalid")
		}
		return []localDevice{{DeviceID: fallbackDeviceID, Name: "Local HVAC Device", Type: "HVAC"}}, nil
	}
	body, err := os.ReadFile(path)
	if err != nil || len(body) == 0 || len(body) > 64<<10 {
		return nil, errors.New("Device catalog file is unreadable")
	}
	var catalog localDeviceCatalog
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&catalog) != nil || ensureEOF(decoder) != nil || catalog.SchemaVersion != 1 || len(catalog.Devices) == 0 || len(catalog.Devices) > 16 {
		return nil, errors.New("Device catalog document is invalid")
	}
	seen := make(map[string]struct{}, len(catalog.Devices))
	for _, device := range catalog.Devices {
		if !commandmodel.IsUUIDv7(device.DeviceID) || strings.TrimSpace(device.Name) == "" || strings.TrimSpace(device.Type) == "" {
			return nil, errors.New("Device catalog entry is invalid")
		}
		if _, duplicate := seen[device.DeviceID]; duplicate {
			return nil, errors.New("Device catalog contains duplicate Device IDs")
		}
		seen[device.DeviceID] = struct{}{}
	}
	return catalog.Devices, nil
}

func randomIdentifier(prefix string) (string, error) {
	body := make([]byte, 16)
	if _, err := rand.Read(body); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(body), nil
}

func mediaType(value string) string {
	return strings.TrimSpace(strings.Split(value, ";")[0])
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

func writeProblem(writer http.ResponseWriter, request *http.Request, status int, code, detail string, retryable bool) {
	traceID, _ := randomIdentifier("")
	traceID = strings.TrimPrefix(traceID, "-")
	if len(traceID) > 32 {
		traceID = traceID[:32]
	}
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(problem{
		Type:  "urn:hvac:problem:" + strings.ToLower(strings.ReplaceAll(code, "_", "-")),
		Title: http.StatusText(status), Status: status, Detail: detail, Instance: request.URL.Path,
		Code: code, TraceID: traceID, Retryable: retryable,
	})
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
