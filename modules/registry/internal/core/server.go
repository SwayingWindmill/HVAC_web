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
	Store                             RegistryStore
	Writer                            RegistryWriter
	CursorCodec                       *CursorCodec
	GrantPublicKey                    crypto.PublicKey
	GrantIssuer                       string
	AllowedPresenterSPIFFE            string
	AdditionalAllowedPresenterSPIFFEs []string
	Audience                          string
	GrantStatus                       GrantStatusProvider
	Logger                            *slog.Logger
	Observability                     *observability.Runtime
	Now                               func() time.Time
}

type server struct {
	store                   RegistryStore
	writer                  RegistryWriter
	cursorCodec             *CursorCodec
	grantPublicKey          crypto.PublicKey
	grantIssuer             string
	allowedPresenterSPIFFEs map[string]struct{}
	audience                string
	grantStatus             GrantStatusProvider
	logger                  *slog.Logger
	observability           *observability.Runtime
	now                     func() time.Time
}

type registryRoute struct {
	template string
	resource string
	parentID string
	id       string
	action   registryauth.Action
	list     bool
	write    bool
}

func NewHandler(config ServerConfig) http.Handler {
	primaryPresenter := strings.TrimSpace(config.AllowedPresenterSPIFFE)
	if config.Store == nil || config.CursorCodec == nil || config.GrantPublicKey == nil || strings.TrimSpace(config.GrantIssuer) == "" || primaryPresenter == "" || strings.TrimSpace(config.Audience) == "" || config.GrantStatus == nil {
		panic("Core Registry server configuration is incomplete")
	}
	allowedPresenters := map[string]struct{}{primaryPresenter: {}}
	for _, candidate := range config.AdditionalAllowedPresenterSPIFFEs {
		presenter := strings.TrimSpace(candidate)
		if presenter == "" || !strings.HasPrefix(presenter, "spiffe://") {
			panic("Core Registry allowed presenter is invalid")
		}
		allowedPresenters[presenter] = struct{}{}
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
		store:                   config.Store,
		writer:                  config.Writer,
		cursorCodec:             config.CursorCodec,
		grantPublicKey:          config.GrantPublicKey,
		grantIssuer:             config.GrantIssuer,
		allowedPresenterSPIFFEs: allowedPresenters,
		audience:                config.Audience,
		grantStatus:             config.GrantStatus,
		logger:                  logger,
		observability:           telemetry,
		now:                     now,
	}
}

func (server *server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	started := server.now()
	route, routeOK := parseRegistryRoute(request.Method, request.URL.Path)
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
	if hasForgedIdentityHeader(request.Header) {
		statusCode = http.StatusBadRequest
		writeProblem(writer, request, statusCode, "CORE_FORGED_IDENTITY_HEADER", "Caller-supplied identity or business-scope headers are not accepted.", false)
		return
	}
	peerSPIFFE, ok := verifiedPeerSPIFFE(request)
	_, presenterAllowed := server.allowedPresenterSPIFFEs[peerSPIFFE]
	if !ok || !presenterAllowed {
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
	if route.resource == "meter-binding-resolve" {
		query := request.URL.Query()
		for name, values := range query {
			if (name != "deviceId" && name != "pointId" && name != "sampledAt") || len(values) != 1 {
				writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The meter binding resolver query is invalid.", false)
				return http.StatusBadRequest
			}
		}
		sampledAt, err := time.Parse(time.RFC3339Nano, query.Get("sampledAt"))
		if err != nil {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The meter binding resolver query is invalid.", false)
			return http.StatusBadRequest
		}
		result, err := server.store.ResolveMeterBinding(request.Context(), claims, route.parentID, MeterBindingResolveRequest{
			DeviceID: query.Get("deviceId"), PointID: query.Get("pointId"), SampledAt: sampledAt,
		})
		if errors.Is(err, ErrInvalidBindingResolution) {
			writeProblem(writer, request, http.StatusBadRequest, "METER_BINDING_RESOLUTION_INVALID", "The meter binding resolver input is invalid.", false)
			return http.StatusBadRequest
		}
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, result)
		return http.StatusOK
	}
	if route.write {
		return server.handleAuthorizedWrite(writer, request, route, claims)
	}
	page, err := server.pageRequest(request, route, claims)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "CURSOR_INVALID", "The Registry page cursor or limit is invalid.", false)
		return http.StatusBadRequest
	}

	switch route.resource {
	case "sites":
		if route.list {
			result, err := server.store.ListSites(request.Context(), claims, page)
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
	case "assets":
		if route.list {
			result, err := server.store.ListAssets(request.Context(), claims, route.parentID, page)
			if err != nil {
				return server.writeStoreError(writer, request, err)
			}
			collection, err := server.assetCollection(route, claims, result)
			if err != nil {
				return server.writeStoreError(writer, request, err)
			}
			writeJSON(writer, http.StatusOK, collection)
			return http.StatusOK
		}
		item, err := server.store.GetAsset(request.Context(), claims, route.id)
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
	case "asset-model":
		model, err := server.store.GetSiteAssetModel(request.Context(), claims, route.parentID)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, model)
		return http.StatusOK
	case "space-children":
		parentSpaceID := request.URL.Query().Get("parentSpaceId")
		result, err := server.store.ListSpaceChildren(request.Context(), claims, route.parentID, parentSpaceID, page)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		collection, err := server.spaceCollection(route, claims, parentSpaceID, result)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, collection)
		return http.StatusOK
	case "device-points":
		result, err := server.store.ListDevicePoints(request.Context(), claims, route.parentID, page)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		collection, err := server.pointCollection(route, claims, result)
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
		allowed := name == "limit" || name == "cursor" || (route.resource == "space-children" && name == "parentSpaceId")
		if !allowed || len(values) != 1 {
			return PageRequest{}, ErrInvalidPage
		}
	}
	if route.resource == "space-children" {
		parentSpaceID := query.Get("parentSpaceId")
		if parentSpaceID != "" && !validUUIDv7(parentSpaceID) {
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
	cursorParentID := route.parentID
	if route.resource == "space-children" {
		cursorParentID += "|" + query.Get("parentSpaceId")
	}
	page, err := server.cursorCodec.Decode(query.Get("cursor"), route.resource, cursorParentID, route.action, claims)
	if err != nil {
		return PageRequest{}, err
	}
	page.Limit = limit
	return page, nil
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

func (server *server) assetCollection(route registryRoute, claims registryauth.GrantClaims, result PageResult[Asset]) (Collection[Asset], error) {
	collection := Collection[Asset]{Items: result.Items, HasMore: result.HasMore}
	if result.HasMore && len(result.Items) > 0 {
		last := result.Items[len(result.Items)-1]
		cursor, err := server.cursorCodec.Encode(route.resource, route.parentID, route.action, claims, last.DisplayName, last.ID)
		if err != nil {
			return Collection[Asset]{}, err
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

func (server *server) spaceCollection(route registryRoute, claims registryauth.GrantClaims, parentSpaceID string, result PageResult[Space]) (Collection[Space], error) {
	collection := Collection[Space]{Items: result.Items, HasMore: result.HasMore}
	if result.HasMore && len(result.Items) > 0 {
		last := result.Items[len(result.Items)-1]
		cursor, err := server.cursorCodec.Encode(route.resource, route.parentID+"|"+parentSpaceID, route.action, claims, last.DisplayName, last.ID)
		if err != nil {
			return Collection[Space]{}, err
		}
		collection.NextCursor = &cursor
	}
	return collection, nil
}

func (server *server) pointCollection(route registryRoute, claims registryauth.GrantClaims, result PageResult[TelemetryPoint]) (Collection[TelemetryPoint], error) {
	collection := Collection[TelemetryPoint]{Items: result.Items, HasMore: result.HasMore}
	if result.HasMore && len(result.Items) > 0 {
		last := result.Items[len(result.Items)-1]
		cursor, err := server.cursorCodec.Encode(route.resource, route.parentID, route.action, claims, last.DisplayName, last.ID)
		if err != nil {
			return Collection[TelemetryPoint]{}, err
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

func parseRegistryRoute(method, path string) (registryRoute, bool) {
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
	case method == http.MethodGet && len(segments) == 1 && segments[0] == "sites":
		return registryRoute{template: RegistryPathPrefix + "sites", resource: "sites", action: registryauth.ActionSiteList, list: true}, true
	case method == http.MethodPost && len(segments) == 1 && segments[0] == "sites":
		return registryRoute{template: RegistryPathPrefix + "sites", resource: "site-write", action: registryauth.ActionSiteWrite, write: true}, true
	case method == http.MethodGet && len(segments) == 2 && segments[0] == "sites":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}", resource: "sites", id: segments[1], action: registryauth.ActionSiteRead}, true
	case method == http.MethodPatch && len(segments) == 2 && segments[0] == "sites":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}", resource: "site-write", id: segments[1], action: registryauth.ActionSiteWrite, write: true}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "assets":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/assets", resource: "assets", parentID: segments[1], action: registryauth.ActionAssetList, list: true}, true
	case method == http.MethodPost && len(segments) == 3 && segments[0] == "sites" && segments[2] == "assets":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/assets", resource: "asset-write", parentID: segments[1], action: registryauth.ActionAssetWrite, write: true}, true
	case method == http.MethodPatch && len(segments) == 4 && segments[0] == "sites" && segments[2] == "assets":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/assets/{assetId}", resource: "asset-write", parentID: segments[1], id: segments[3], action: registryauth.ActionAssetWrite, write: true}, true
	case method == http.MethodGet && len(segments) == 2 && segments[0] == "assets":
		return registryRoute{template: RegistryPathPrefix + "assets/{assetId}", resource: "assets", id: segments[1], action: registryauth.ActionAssetRead}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "devices":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/devices", resource: "devices", parentID: segments[1], action: registryauth.ActionDeviceList, list: true}, true
	case method == http.MethodPost && len(segments) == 3 && segments[0] == "sites" && segments[2] == "devices":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/devices", resource: "device-write", parentID: segments[1], action: registryauth.ActionDeviceWrite, write: true}, true
	case method == http.MethodPatch && len(segments) == 4 && segments[0] == "sites" && segments[2] == "devices":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/devices/{deviceId}", resource: "device-write", parentID: segments[1], id: segments[3], action: registryauth.ActionDeviceWrite, write: true}, true
	case method == http.MethodPost && len(segments) == 3 && segments[0] == "sites" && segments[2] == "spaces":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/spaces", resource: "space-write", parentID: segments[1], action: registryauth.ActionSpaceWrite, write: true}, true
	case method == http.MethodPatch && len(segments) == 4 && segments[0] == "sites" && segments[2] == "spaces":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/spaces/{spaceId}", resource: "space-write", parentID: segments[1], id: segments[3], action: registryauth.ActionSpaceWrite, write: true}, true
	case method == http.MethodGet && len(segments) == 4 && segments[0] == "sites" && segments[2] == "spaces" && segments[3] == "tree":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/spaces/tree", resource: "space-children", parentID: segments[1], action: registryauth.ActionAssetModelRead, list: true}, true
	case method == http.MethodPost && len(segments) == 3 && segments[0] == "sites" && segments[2] == "sensors":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/sensors", resource: "sensor-write", parentID: segments[1], action: registryauth.ActionSensorWrite, write: true}, true
	case method == http.MethodPatch && len(segments) == 4 && segments[0] == "sites" && segments[2] == "sensors":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/sensors/{sensorId}", resource: "sensor-write", parentID: segments[1], id: segments[3], action: registryauth.ActionSensorWrite, write: true}, true
	case method == http.MethodPost && len(segments) == 3 && segments[0] == "sites" && segments[2] == "points":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/points", resource: "point-write", parentID: segments[1], action: registryauth.ActionPointWrite, write: true}, true
	case method == http.MethodPatch && len(segments) == 4 && segments[0] == "sites" && segments[2] == "points":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/points/{pointId}", resource: "point-write", parentID: segments[1], id: segments[3], action: registryauth.ActionPointWrite, write: true}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "device-bindings":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/device-bindings", resource: "device-bindings", parentID: segments[1], action: registryauth.ActionDeviceBindingList, list: true}, true
	case method == http.MethodGet && len(segments) == 4 && segments[0] == "sites" && segments[2] == "meter-bindings" && segments[3] == "resolve":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/meter-bindings/resolve", resource: "meter-binding-resolve", parentID: segments[1], action: registryauth.ActionMeterBindingResolve}, true
	case method == http.MethodPost && len(segments) == 4 && segments[0] == "sites" && segments[2] == "bindings" && segments[3] == "rebind":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/bindings/rebind", resource: "binding-write", parentID: segments[1], action: registryauth.ActionBindingWrite, write: true}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "asset-model":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/asset-model", resource: "asset-model", parentID: segments[1], action: registryauth.ActionAssetModelRead}, true
	case method == http.MethodGet && len(segments) == 2 && segments[0] == "devices":
		return registryRoute{template: RegistryPathPrefix + "devices/{deviceId}", resource: "devices", id: segments[1], action: registryauth.ActionDeviceRead}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "devices" && segments[2] == "points":
		return registryRoute{template: RegistryPathPrefix + "devices/{deviceId}/points", resource: "device-points", parentID: segments[1], action: registryauth.ActionDeviceRead, list: true}, true
	case method == http.MethodPost && len(segments) == 2 && segments[0] == "templates" && segments[1] == "revisions":
		return registryRoute{template: RegistryPathPrefix + "templates/revisions", resource: "template-release", action: registryauth.ActionTemplateManage, write: true}, true
	case method == http.MethodPost && len(segments) == 2 && segments[0] == "templates" && segments[1] == "assignments":
		return registryRoute{template: RegistryPathPrefix + "templates/assignments", resource: "template-assign", action: registryauth.ActionTemplateManage, write: true}, true
	case method == http.MethodPost && len(segments) == 4 && segments[0] == "sites" && segments[2] == "imports" && segments[3] == "dry-run":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/imports/dry-run", resource: "import-plan", parentID: segments[1], action: registryauth.ActionRegistryImport, write: true}, true
	case method == http.MethodPost && len(segments) == 4 && segments[0] == "sites" && segments[2] == "imports" && segments[3] == "commit":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/imports/commit", resource: "import-commit", parentID: segments[1], action: registryauth.ActionRegistryImport, write: true}, true
	case method == http.MethodPost && len(segments) == 3 && segments[0] == "sites" && segments[2] == "retire":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/retire", resource: "retire", parentID: segments[1], action: registryauth.ActionRegistryRetire, write: true}, true
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
