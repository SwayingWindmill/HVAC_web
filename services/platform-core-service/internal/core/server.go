package core

import (
	"context"
	"crypto"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const RegistryPathPrefix = "/internal/v1/registry/"

type ServerConfig struct {
	Store                  RegistryStore
	CursorCodec            *CursorCodec
	GrantPublicKey         crypto.PublicKey
	GrantIssuer            string
	AllowedPresenterSPIFFE string
	Audience               string
	GrantStatus            GrantStatusProvider
	Logger                 *slog.Logger
	Observability          *observability.Runtime
	Now                    func() time.Time
}

type server struct {
	store                  RegistryStore
	cursorCodec            *CursorCodec
	grantPublicKey         crypto.PublicKey
	grantIssuer            string
	allowedPresenterSPIFFE string
	audience               string
	grantStatus            GrantStatusProvider
	logger                 *slog.Logger
	observability          *observability.Runtime
	now                    func() time.Time
}

type registryRoute struct {
	template string
	resource string
	parentID string
	id       string
	action   registryauth.Action
	list     bool
}

func NewHandler(config ServerConfig) http.Handler {
	if config.Store == nil || config.CursorCodec == nil || config.GrantPublicKey == nil || strings.TrimSpace(config.GrantIssuer) == "" || strings.TrimSpace(config.AllowedPresenterSPIFFE) == "" || strings.TrimSpace(config.Audience) == "" || config.GrantStatus == nil {
		panic("Core Registry server configuration is incomplete")
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	telemetry := config.Observability
	if telemetry == nil {
		telemetry = observability.NewRuntime(observability.RuntimeConfig{Service: "platform-core-service"})
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &server{
		store:                  config.Store,
		cursorCodec:            config.CursorCodec,
		grantPublicKey:         config.GrantPublicKey,
		grantIssuer:            config.GrantIssuer,
		allowedPresenterSPIFFE: config.AllowedPresenterSPIFFE,
		audience:               config.Audience,
		grantStatus:            config.GrantStatus,
		logger:                 logger,
		observability:          telemetry,
		now:                    now,
	}
}

func (server *server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	started := server.now()
	route, routeOK := parseRegistryRoute(request.URL.Path)
	routeName := "unmatched"
	if routeOK {
		routeName = route.template
	}
	ctx := server.observability.Tracer.ExtractHTTP(request.Context(), request.Header)
	ctx, span := server.observability.Tracer.Start(ctx, "http.core.registry", observability.SpanKindServer, map[string]any{
		"http.request.method": request.Method,
		"http.route":          routeName,
	})
	request = request.WithContext(ctx)
	writer.Header().Set("traceparent", observability.Traceparent(ctx))
	statusCode := http.StatusOK
	defer func() {
		result := "ok"
		if statusCode >= http.StatusBadRequest {
			result = "error"
			span.SetStatus("error", http.StatusText(statusCode))
		} else {
			span.SetStatus("ok", "")
		}
		span.SetAttributes(map[string]any{"http.response.status_code": statusCode})
		span.End()
		_ = server.observability.Metrics.AddCounter("s1_core_registry_requests_total", "Core Registry read requests.", map[string]string{"route": routeName, "method": request.Method, "result": result}, 1)
		_ = server.observability.Metrics.ObserveHistogram("s1_core_registry_request_duration_seconds", "Core Registry read latency.", map[string]string{"route": routeName, "method": request.Method}, server.now().Sub(started).Seconds(), nil)
		server.logger.InfoContext(request.Context(), "core_registry_request",
			"method", request.Method,
			"path", routeName,
			"status", statusCode,
			"duration_ms", server.now().Sub(started).Milliseconds(),
			"trace_id", observability.TraceID(request.Context()),
		)
	}()

	if !routeOK {
		statusCode = http.StatusNotFound
		writeProblem(writer, request, statusCode, "CORE_ROUTE_NOT_FOUND", "The requested Core Registry route does not exist.", false)
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		statusCode = http.StatusMethodNotAllowed
		writeProblem(writer, request, statusCode, "CORE_METHOD_NOT_ALLOWED", "Core Registry routes only support GET.", false)
		return
	}
	if hasForgedIdentityHeader(request.Header) {
		statusCode = http.StatusBadRequest
		writeProblem(writer, request, statusCode, "CORE_FORGED_IDENTITY_HEADER", "Caller-supplied identity or business-scope headers are not accepted.", false)
		return
	}
	peerSPIFFE, ok := verifiedPeerSPIFFE(request)
	if !ok || peerSPIFFE != server.allowedPresenterSPIFFE {
		statusCode = http.StatusUnauthorized
		writeProblem(writer, request, statusCode, "CORE_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.", false)
		return
	}
	claims, err := registryauth.VerifyGrant(server.grantPublicKey, request.Header.Get("X-Delegation-Grant"))
	if err != nil {
		statusCode = http.StatusUnauthorized
		writeProblem(writer, request, statusCode, "CORE_REGISTRY_GRANT_INVALID", "The Registry delegation grant is invalid.", false)
		return
	}
	if err := validateGrantScopeIDs(claims); err != nil {
		statusCode = http.StatusForbidden
		writeProblem(writer, request, statusCode, "CORE_REGISTRY_GRANT_REJECTED", "The Registry delegation grant is not authorized for this query.", false)
		return
	}
	if err := registryauth.ValidateGrant(claims, registryauth.GrantValidation{
		Now:                   server.now(),
		Issuer:                server.grantIssuer,
		Presenter:             peerSPIFFE,
		Audience:              server.audience,
		Action:                route.action,
		CurrentPolicyRevision: claims.PolicyRevision,
		IsRevoked:             func(string) (bool, error) { return false, nil },
	}); err != nil {
		statusCode = http.StatusForbidden
		writeProblem(writer, request, statusCode, "CORE_REGISTRY_GRANT_REJECTED", "The Registry delegation grant is not authorized for this query.", false)
		return
	}
	grantStatus, err := server.grantStatus.Lookup(request.Context(), claims)
	if err != nil {
		statusCode = http.StatusServiceUnavailable
		writeProblem(writer, request, statusCode, "CORE_GRANT_STATUS_UNAVAILABLE", "IAM grant status is temporarily unavailable.", true)
		return
	}
	if err := registryauth.ValidateGrant(claims, registryauth.GrantValidation{
		Now:                   server.now(),
		Issuer:                server.grantIssuer,
		Presenter:             peerSPIFFE,
		Audience:              server.audience,
		Action:                route.action,
		CurrentPolicyRevision: grantStatus.CurrentPolicyRevision,
		IsRevoked: func(string) (bool, error) {
			return grantStatus.Revoked, nil
		},
	}); err != nil {
		statusCode = http.StatusForbidden
		writeProblem(writer, request, statusCode, "CORE_REGISTRY_GRANT_REJECTED", "The Registry delegation grant is not authorized for this query.", false)
		return
	}
	statusCode = server.handleAuthorized(writer, request, route, claims)
}

func (server *server) handleAuthorized(writer http.ResponseWriter, request *http.Request, route registryRoute, claims registryauth.GrantClaims) int {
	page, err := server.pageRequest(request, route, claims)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "CURSOR_INVALID", "The Registry page cursor or limit is invalid.", false)
		return http.StatusBadRequest
	}

	switch route.resource {
	case "organizations":
		if route.list {
			result, err := server.store.ListOrganizations(request.Context(), claims, page)
			if err != nil {
				return server.writeStoreError(writer, request, err)
			}
			collection, err := server.organizationCollection(route, claims, result)
			if err != nil {
				return server.writeStoreError(writer, request, err)
			}
			writeJSON(writer, http.StatusOK, collection)
			return http.StatusOK
		}
		item, err := server.store.GetOrganization(request.Context(), claims, route.id)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, item)
		return http.StatusOK
	case "sites":
		if route.list {
			result, err := server.store.ListSites(request.Context(), claims, route.parentID, page)
			if err != nil {
				return server.writeStoreError(writer, request, err)
			}
			collection, err := server.siteCollection(route, claims, result)
			if err != nil {
				return server.writeStoreError(writer, request, err)
			}
			writeJSON(writer, http.StatusOK, collection)
			return http.StatusOK
		}
		item, err := server.store.GetSite(request.Context(), claims, route.id)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, item)
		return http.StatusOK
	case "equipment":
		if route.list {
			result, err := server.store.ListEquipment(request.Context(), claims, route.parentID, page)
			if err != nil {
				return server.writeStoreError(writer, request, err)
			}
			collection, err := server.equipmentCollection(route, claims, result)
			if err != nil {
				return server.writeStoreError(writer, request, err)
			}
			writeJSON(writer, http.StatusOK, collection)
			return http.StatusOK
		}
		item, err := server.store.GetEquipment(request.Context(), claims, route.id)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, item)
		return http.StatusOK
	case "devices":
		if route.list {
			result, err := server.store.ListDevices(request.Context(), claims, route.parentID, page)
			if err != nil {
				return server.writeStoreError(writer, request, err)
			}
			collection, err := server.deviceCollection(route, claims, result)
			if err != nil {
				return server.writeStoreError(writer, request, err)
			}
			writeJSON(writer, http.StatusOK, collection)
			return http.StatusOK
		}
		item, err := server.store.GetDevice(request.Context(), claims, route.id)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, item)
		return http.StatusOK
	case "device-bindings":
		result, err := server.store.ListDeviceBindings(request.Context(), claims, route.parentID, page)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		collection, err := server.deviceBindingCollection(route, claims, result)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, collection)
		return http.StatusOK
	default:
		writeProblem(writer, request, http.StatusNotFound, "CORE_ROUTE_NOT_FOUND", "The requested Core Registry route does not exist.", false)
		return http.StatusNotFound
	}
}

func (server *server) pageRequest(request *http.Request, route registryRoute, claims registryauth.GrantClaims) (PageRequest, error) {
	if !route.list {
		if request.URL.RawQuery != "" {
			return PageRequest{}, ErrInvalidPage
		}
		return PageRequest{}, nil
	}
	query := request.URL.Query()
	for name, values := range query {
		if (name != "limit" && name != "cursor") || len(values) != 1 {
			return PageRequest{}, ErrInvalidPage
		}
	}
	limit := 0
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return PageRequest{}, ErrInvalidPage
		}
		limit = parsed
	}
	limit, err := normalizedLimit(limit)
	if err != nil {
		return PageRequest{}, err
	}
	page, err := server.cursorCodec.Decode(query.Get("cursor"), route.resource, route.parentID, route.action, claims)
	if err != nil {
		return PageRequest{}, err
	}
	page.Limit = limit
	return page, nil
}

func (server *server) organizationCollection(route registryRoute, claims registryauth.GrantClaims, result PageResult[Organization]) (Collection[Organization], error) {
	collection := Collection[Organization]{Items: result.Items, HasMore: result.HasMore}
	if result.HasMore && len(result.Items) > 0 {
		last := result.Items[len(result.Items)-1]
		cursor, err := server.cursorCodec.Encode(route.resource, route.parentID, route.action, claims, last.DisplayName, last.ID)
		if err != nil {
			return Collection[Organization]{}, err
		}
		collection.NextCursor = &cursor
	}
	return collection, nil
}

func (server *server) siteCollection(route registryRoute, claims registryauth.GrantClaims, result PageResult[Site]) (Collection[Site], error) {
	collection := Collection[Site]{Items: result.Items, HasMore: result.HasMore}
	if result.HasMore && len(result.Items) > 0 {
		last := result.Items[len(result.Items)-1]
		cursor, err := server.cursorCodec.Encode(route.resource, route.parentID, route.action, claims, last.DisplayName, last.ID)
		if err != nil {
			return Collection[Site]{}, err
		}
		collection.NextCursor = &cursor
	}
	return collection, nil
}

func (server *server) equipmentCollection(route registryRoute, claims registryauth.GrantClaims, result PageResult[Equipment]) (Collection[Equipment], error) {
	collection := Collection[Equipment]{Items: result.Items, HasMore: result.HasMore}
	if result.HasMore && len(result.Items) > 0 {
		last := result.Items[len(result.Items)-1]
		cursor, err := server.cursorCodec.Encode(route.resource, route.parentID, route.action, claims, last.DisplayName, last.ID)
		if err != nil {
			return Collection[Equipment]{}, err
		}
		collection.NextCursor = &cursor
	}
	return collection, nil
}

func (server *server) deviceCollection(route registryRoute, claims registryauth.GrantClaims, result PageResult[Device]) (Collection[Device], error) {
	collection := Collection[Device]{Items: result.Items, HasMore: result.HasMore}
	if result.HasMore && len(result.Items) > 0 {
		last := result.Items[len(result.Items)-1]
		cursor, err := server.cursorCodec.Encode(route.resource, route.parentID, route.action, claims, last.DisplayName, last.ID)
		if err != nil {
			return Collection[Device]{}, err
		}
		collection.NextCursor = &cursor
	}
	return collection, nil
}

func (server *server) deviceBindingCollection(route registryRoute, claims registryauth.GrantClaims, result PageResult[DeviceBinding]) (Collection[DeviceBinding], error) {
	collection := Collection[DeviceBinding]{Items: result.Items, HasMore: result.HasMore}
	if result.HasMore && len(result.Items) > 0 {
		last := result.Items[len(result.Items)-1]
		cursor, err := server.cursorCodec.Encode(route.resource, route.parentID, route.action, claims, last.BindingRole, last.ID)
		if err != nil {
			return Collection[DeviceBinding]{}, err
		}
		collection.NextCursor = &cursor
	}
	return collection, nil
}

func (server *server) writeStoreError(writer http.ResponseWriter, request *http.Request, err error) int {
	if errors.Is(err, ErrNotFound) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested Registry resource was not found.", false)
		return http.StatusNotFound
	}
	if errors.Is(err, ErrInvalidPage) {
		writeProblem(writer, request, http.StatusBadRequest, "CURSOR_INVALID", "The Registry page cursor or limit is invalid.", false)
		return http.StatusBadRequest
	}
	var databaseError *pgconn.PgError
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &databaseError) && databaseError.Code == "57014") {
		writeProblem(writer, request, http.StatusGatewayTimeout, "REGISTRY_TIMEOUT", "The Registry query timed out.", true)
		return http.StatusGatewayTimeout
	}
	writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "The Core Registry is temporarily unavailable.", true)
	return http.StatusServiceUnavailable
}

func parseRegistryRoute(path string) (registryRoute, bool) {
	if !strings.HasPrefix(path, RegistryPathPrefix) {
		return registryRoute{}, false
	}
	segments := strings.Split(strings.TrimPrefix(path, RegistryPathPrefix), "/")
	for _, segment := range segments {
		if segment == "" {
			return registryRoute{}, false
		}
	}
	switch {
	case len(segments) == 1 && segments[0] == "organizations":
		return registryRoute{template: RegistryPathPrefix + "organizations", resource: "organizations", action: registryauth.ActionOrganizationList, list: true}, true
	case len(segments) == 2 && segments[0] == "organizations":
		return registryRoute{template: RegistryPathPrefix + "organizations/{organizationId}", resource: "organizations", id: segments[1], action: registryauth.ActionOrganizationRead}, true
	case len(segments) == 3 && segments[0] == "organizations" && segments[2] == "sites":
		return registryRoute{template: RegistryPathPrefix + "organizations/{organizationId}/sites", resource: "sites", parentID: segments[1], action: registryauth.ActionSiteList, list: true}, true
	case len(segments) == 2 && segments[0] == "sites":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}", resource: "sites", id: segments[1], action: registryauth.ActionSiteRead}, true
	case len(segments) == 3 && segments[0] == "sites" && segments[2] == "equipment":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/equipment", resource: "equipment", parentID: segments[1], action: registryauth.ActionEquipmentList, list: true}, true
	case len(segments) == 2 && segments[0] == "equipment":
		return registryRoute{template: RegistryPathPrefix + "equipment/{equipmentId}", resource: "equipment", id: segments[1], action: registryauth.ActionEquipmentRead}, true
	case len(segments) == 3 && segments[0] == "sites" && segments[2] == "devices":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/devices", resource: "devices", parentID: segments[1], action: registryauth.ActionDeviceList, list: true}, true
	case len(segments) == 3 && segments[0] == "sites" && segments[2] == "device-bindings":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/device-bindings", resource: "device-bindings", parentID: segments[1], action: registryauth.ActionDeviceBindingList, list: true}, true
	case len(segments) == 2 && segments[0] == "devices":
		return registryRoute{template: RegistryPathPrefix + "devices/{deviceId}", resource: "devices", id: segments[1], action: registryauth.ActionDeviceRead}, true
	default:
		return registryRoute{}, false
	}
}

func verifiedPeerSPIFFE(request *http.Request) (string, bool) {
	if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 || len(request.TLS.VerifiedChains) == 0 {
		return "", false
	}
	certificate := request.TLS.PeerCertificates[0]
	if len(certificate.URIs) != 1 || certificate.URIs[0] == nil || certificate.URIs[0].Scheme != "spiffe" {
		return "", false
	}
	return certificate.URIs[0].String(), true
}

func hasForgedIdentityHeader(header http.Header) bool {
	for name, values := range header {
		nonEmpty := false
		for _, value := range values {
			if value != "" {
				nonEmpty = true
				break
			}
		}
		if !nonEmpty {
			continue
		}
		lowerName := strings.ToLower(name)
		switch lowerName {
		case "x-principal", "x-roles", "x-role", "x-admin", "x-scope", "x-organization-id", "x-site-id":
			return true
		}
		if strings.HasPrefix(lowerName, "x-principal-") || strings.HasPrefix(lowerName, "x-organization-") || strings.HasPrefix(lowerName, "x-site-") {
			return true
		}
	}
	return false
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeProblem(writer http.ResponseWriter, request *http.Request, status int, code, detail string, retryable bool) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"type":      "https://api.quanlaihe.com/problems/" + strings.ToLower(strings.ReplaceAll(code, "_", "-")),
		"title":     http.StatusText(status),
		"status":    status,
		"detail":    detail,
		"instance":  request.URL.Path,
		"code":      code,
		"traceId":   observability.TraceID(request.Context()),
		"retryable": retryable,
	})
}
