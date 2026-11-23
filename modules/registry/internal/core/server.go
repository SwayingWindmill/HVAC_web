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
	AllowedCommandServiceSPIFFE       string
	Audience                          string
	GrantStatus                       GrantStatusProvider
	Logger                            *slog.Logger
	Observability                     *observability.Runtime
	Now                               func() time.Time
}

type server struct {
	store                       RegistryStore
	writer                      RegistryWriter
	cursorCodec                 *CursorCodec
	grantPublicKey              crypto.PublicKey
	grantIssuer                 string
	allowedPresenterSPIFFEs     map[string]struct{}
	allowedCommandServiceSPIFFE string
	audience                    string
	grantStatus                 GrantStatusProvider
	logger                      *slog.Logger
	observability               *observability.Runtime
	now                         func() time.Time
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
	allowedCommandServiceSPIFFE := strings.TrimSpace(config.AllowedCommandServiceSPIFFE)
	if allowedCommandServiceSPIFFE == "" {
		allowedCommandServiceSPIFFE = "spiffe://hvac.local/command-service"
	} else if !strings.HasPrefix(allowedCommandServiceSPIFFE, "spiffe://") {
		panic("Core Registry allowed command service SPIFFE is invalid")
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
		store:                       config.Store,
		writer:                      config.Writer,
		cursorCodec:                 config.CursorCodec,
		grantPublicKey:              config.GrantPublicKey,
		grantIssuer:                 config.GrantIssuer,
		allowedPresenterSPIFFEs:     allowedPresenters,
		allowedCommandServiceSPIFFE: allowedCommandServiceSPIFFE,
		audience:                    config.Audience,
		grantStatus:                 config.GrantStatus,
		logger:                      logger,
		observability:               telemetry,
		now:                         now,
	}
}

func (server *server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	started := server.now()
	route, routeOK := parseRegistryRoute(request.Method, request.URL.Path)
	routeName := "unmatched"
	if request.URL.Path == CommandDispatchContextPath {
		routeName = CommandDispatchContextPath
	} else if routeOK {
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

	if request.URL.Path == CommandDispatchContextPath {
		if request.Method != http.MethodGet {
			statusCode = http.StatusMethodNotAllowed
			writer.Header().Set("Allow", http.MethodGet)
			writeProblem(writer, request, statusCode, "CORE_METHOD_NOT_ALLOWED", "The command dispatch context route only supports GET.", false)
			return
		}
		if hasForgedIdentityHeader(request.Header) {
			statusCode = http.StatusBadRequest
			writeProblem(writer, request, statusCode, "CORE_FORGED_IDENTITY_HEADER", "Caller-supplied identity or business-scope headers are not accepted.", false)
			return
		}
		peerSPIFFE, ok := verifiedPeerSPIFFE(request)
		if !ok || peerSPIFFE != server.allowedCommandServiceSPIFFE {
			statusCode = http.StatusUnauthorized
			writeProblem(writer, request, statusCode, "CORE_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.", false)
			return
		}
		statusCode = server.handleCommandDispatchContext(writer, request)
		return
	}

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
	claims, errStatus, errCode, errDetail, retryable := server.authorizeDelegationGrant(request, peerSPIFFE, request.Header.Get("X-Delegation-Grant"), func(c registryauth.GrantClaims) registryauth.Action {
		return requiredCoreActionForRoute(route, c)
	})
	if errStatus != 0 {
		statusCode = errStatus
		writeProblem(writer, request, statusCode, errCode, errDetail, retryable)
		return
	}
	statusCode = server.handleAuthorized(writer, request, route, claims)
}

func (server *server) authorizeDelegationGrant(request *http.Request, peerSPIFFE, rawGrant string, resolveAction func(registryauth.GrantClaims) registryauth.Action) (registryauth.GrantClaims, int, string, string, bool) {
	claims, err := registryauth.VerifyGrant(server.grantPublicKey, rawGrant)
	if err != nil {
		return registryauth.GrantClaims{}, http.StatusUnauthorized, "CORE_REGISTRY_GRANT_INVALID", "The Registry delegation grant is invalid.", false
	}
	if err := validateGrantScopeIDs(claims); err != nil {
		return registryauth.GrantClaims{}, http.StatusForbidden, "CORE_REGISTRY_GRANT_REJECTED", "The Registry delegation grant is not authorized for this query.", false
	}
	requiredAction := resolveAction(claims)
	if err := registryauth.ValidateGrant(claims, registryauth.GrantValidation{
		Now:                   server.now(),
		Issuer:                server.grantIssuer,
		Presenter:             peerSPIFFE,
		Audience:              server.audience,
		Action:                requiredAction,
		CurrentPolicyRevision: claims.PolicyRevision,
		IsRevoked:             func(string) (bool, error) { return false, nil },
	}); err != nil {
		return registryauth.GrantClaims{}, http.StatusForbidden, "CORE_REGISTRY_GRANT_REJECTED", "The Registry delegation grant is not authorized for this query.", false
	}
	grantStatus, err := server.grantStatus.Lookup(request.Context(), claims)
	if err != nil {
		return registryauth.GrantClaims{}, http.StatusServiceUnavailable, "CORE_GRANT_STATUS_UNAVAILABLE", "IAM grant status is temporarily unavailable.", true
	}
	if err := registryauth.ValidateGrant(claims, registryauth.GrantValidation{
		Now:                   server.now(),
		Issuer:                server.grantIssuer,
		Presenter:             peerSPIFFE,
		Audience:              server.audience,
		Action:                requiredAction,
		CurrentPolicyRevision: grantStatus.CurrentPolicyRevision,
		IsRevoked: func(string) (bool, error) {
			return grantStatus.Revoked, nil
		},
	}); err != nil {
		return registryauth.GrantClaims{}, http.StatusForbidden, "CORE_REGISTRY_GRANT_REJECTED", "The Registry delegation grant is not authorized for this query.", false
	}
	return claims, 0, "", "", false
}

func (server *server) handleAuthorized(writer http.ResponseWriter, request *http.Request, route registryRoute, claims registryauth.GrantClaims) int {
	if route.resource == "site-meter-bindings" {
		if len(request.URL.Query()) != 0 {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The Site meter bindings query does not accept parameters.", false)
			return http.StatusBadRequest
		}
		result, err := server.store.ListSiteMeterBindings(request.Context(), claims, route.parentID)
		if errors.Is(err, ErrInvalidBindingResolution) {
			writeProblem(writer, request, http.StatusBadRequest, "METER_BINDING_RESOLUTION_INVALID", "The meter binding site scope is invalid.", false)
			return http.StatusBadRequest
		}
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, result)
		return http.StatusOK
	}
	if route.resource == "total-electricity-sources" {
		if len(request.URL.Query()) != 0 {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The Site total electricity sources query does not accept parameters.", false)
			return http.StatusBadRequest
		}
		sources, err := server.store.GetSiteTotalElectricitySources(request.Context(), claims, route.parentID)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, sources)
		return http.StatusOK
	}
	if route.resource == "site-control-state" {
		if len(request.URL.Query()) != 0 {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The Site control state query does not accept parameters.", false)
			return http.StatusBadRequest
		}
		state, err := server.store.GetSiteControlState(request.Context(), claims, route.parentID)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, state)
		return http.StatusOK
	}
	if route.resource == "auto-control-lease" {
		if len(request.URL.Query()) != 0 {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The Site auto control lease query does not accept parameters.", false)
			return http.StatusBadRequest
		}
		state, err := server.store.GetSiteAutoControlLease(request.Context(), claims, route.parentID)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, state)
		return http.StatusOK
	}
	if route.resource == "optimization-execution-monitors" {
		if len(request.URL.Query()) != 0 {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The optimization execution monitors query does not accept parameters.", false)
			return http.StatusBadRequest
		}
		list, err := server.store.ListOptimizationExecutionMonitors(request.Context(), claims, route.parentID)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, list)
		return http.StatusOK
	}
	if route.resource == "import-tariffs" {
		query := request.URL.Query()
		before, err := time.Parse(time.RFC3339Nano, query.Get("before"))
		if len(query) != 1 || len(query["before"]) != 1 || err != nil {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The import Tariff query is invalid.", false)
			return http.StatusBadRequest
		}
		timeline, err := server.store.ListImportTariffs(request.Context(), claims, route.parentID, before)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, timeline)
		return http.StatusOK
	}
	if route.resource == "operating-exceptions" {
		query := request.URL.Query()
		for name, values := range query {
			if (name != "from" && name != "to" && name != "status") || len(values) != 1 {
				writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The Operating Exceptions query is invalid.", false)
				return http.StatusBadRequest
			}
		}
		filter := OperatingExceptionFilter{
			From:   query.Get("from"),
			To:     query.Get("to"),
			Status: query.Get("status"),
		}
		if err := filter.Validate(); err != nil {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The Operating Exceptions query is invalid.", false)
			return http.StatusBadRequest
		}
		list, err := server.store.ListOperatingExceptions(request.Context(), claims, route.parentID, filter)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, list)
		return http.StatusOK
	}
	if route.resource == "electricity-budgets" {
		query := request.URL.Query()
		for name, values := range query {
			if (name != "billingMonth" && name != "fromMonth" && name != "toMonth") || len(values) != 1 {
				writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The monthly electricity budgets query is invalid.", false)
				return http.StatusBadRequest
			}
		}
		filter := MonthlyElectricityBudgetFilter{
			BillingMonth: query.Get("billingMonth"),
			FromMonth:    query.Get("fromMonth"),
			ToMonth:      query.Get("toMonth"),
		}
		if err := filter.Validate(); err != nil {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The monthly electricity budgets query is invalid.", false)
			return http.StatusBadRequest
		}
		list, err := server.store.ListMonthlyElectricityBudgets(request.Context(), claims, route.parentID, filter)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, list)
		return http.StatusOK
	}
	if route.resource == "utility-bills" {
		query := request.URL.Query()
		for name, values := range query {
			if (name != "billingMonth" && name != "meterScope" && name != "status") || len(values) != 1 {
				writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The utility bills query is invalid.", false)
				return http.StatusBadRequest
			}
		}
		filter := UtilityBillFilter{
			BillingMonth: query.Get("billingMonth"),
			MeterScope:   query.Get("meterScope"),
			Status:       query.Get("status"),
		}
		if err := filter.Validate(); err != nil {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The utility bills query is invalid.", false)
			return http.StatusBadRequest
		}
		list, err := server.store.ListUtilityBills(request.Context(), claims, route.parentID, filter)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, list)
		return http.StatusOK
	}
	if route.resource == "accounting-recalculate-jobs" {
		query := request.URL.Query()
		for name, values := range query {
			if name != "limit" || len(values) != 1 {
				writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The accounting recalculation jobs query is invalid.", false)
				return http.StatusBadRequest
			}
		}
		limit := 0
		if rawLimit := query.Get("limit"); rawLimit != "" {
			parsedLimit, err := strconv.Atoi(rawLimit)
			if err != nil || parsedLimit < 1 || parsedLimit > maxAccountingRecalcJobsLimit {
				writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_QUERY_INVALID", "The accounting recalculation jobs limit is invalid.", false)
				return http.StatusBadRequest
			}
			limit = parsedLimit
		}
		reader, ok := server.store.(AccountingRecalcStore)
		if !ok {
			writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "The Core Registry accounting recalculation store is not configured.", true)
			return http.StatusServiceUnavailable
		}
		list, err := reader.ListAccountingRecalcJobs(request.Context(), claims, route.parentID, limit)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, list)
		return http.StatusOK
	}
	if route.resource == "accounting-recalculate" {
		if request.URL.RawQuery != "" {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Registry mutation routes do not accept query parameters.", false)
			return http.StatusBadRequest
		}
		if server.writer == nil {
			writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_WRITER_UNAVAILABLE", "The Registry writer is not configured.", true)
			return http.StatusServiceUnavailable
		}
		recalcWriter, ok := server.writer.(AccountingRecalcWriter)
		if !ok {
			writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_WRITER_UNAVAILABLE", "The Registry accounting recalculation writer is not configured.", true)
			return http.StatusServiceUnavailable
		}
		var input AccountingRecalculateRequest
		if !decodeRegistryMutation(writer, request, &input) {
			return http.StatusBadRequest
		}
		input.SiteID = route.parentID
		input.ActorDisplayName = request.Header.Get("X-Actor-Display-Name")
		job, err := recalcWriter.RequestAccountingRecalculation(request.Context(), claims, input)
		if err != nil {
			if errors.Is(err, ErrMutationForbidden) {
				writeProblem(writer, request, http.StatusForbidden, "ACCOUNTING_RECALC_FORBIDDEN", "The authenticated Principal is not allowed to trigger accounting recalculation on this Site.", false)
				return http.StatusForbidden
			}
			return server.writeMutationError(writer, request, err)
		}
		writeJSON(writer, http.StatusAccepted, job)
		return http.StatusAccepted
	}
	if route.resource == "accounting-dirty-window" {
		if request.URL.RawQuery != "" {
			writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Registry mutation routes do not accept query parameters.", false)
			return http.StatusBadRequest
		}
		if server.writer == nil {
			writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_WRITER_UNAVAILABLE", "The Registry writer is not configured.", true)
			return http.StatusServiceUnavailable
		}
		dirtyWriter, ok := server.writer.(InternalDirtyWindowWriter)
		if !ok {
			writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_WRITER_UNAVAILABLE", "The Registry internal dirty-window writer is not configured.", true)
			return http.StatusServiceUnavailable
		}
		var input InternalDirtyWindowRequest
		if !decodeRegistryMutation(writer, request, &input) {
			return http.StatusBadRequest
		}
		if input.TenantID == "" {
			input.TenantID = claims.TenantID
		} else if input.TenantID != claims.TenantID {
			return server.writeMutationError(writer, request, ErrInvalidMutation)
		}
		if input.SiteID == "" {
			input.SiteID = route.parentID
		} else if input.SiteID != route.parentID {
			return server.writeMutationError(writer, request, ErrInvalidMutation)
		}
		if input.RequestedByDisplay == "" {
			input.RequestedByDisplay = request.Header.Get("X-Actor-Display-Name")
		}
		job, err := dirtyWriter.EnqueueInternalDirtyWindow(request.Context(), claims, input)
		if err != nil {
			if errors.Is(err, ErrMutationForbidden) {
				writeProblem(writer, request, http.StatusForbidden, "ACCOUNTING_RECALC_FORBIDDEN", "The authenticated Principal is not allowed to enqueue accounting dirty windows on this Site.", false)
				return http.StatusForbidden
			}
			return server.writeMutationError(writer, request, err)
		}
		writeJSON(writer, http.StatusAccepted, job)
		return http.StatusAccepted
	}
	if status, handled := server.tryHandleAccountingConfigRoute(writer, request, route, claims); handled {
		return status
	}
	if route.write {
		return server.handleAuthorizedWrite(writer, request, route, claims)
	}
	queryParams, err := server.parseRegistryListQuery(request, route, claims)
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "CURSOR_INVALID", "The Registry page cursor or limit is invalid.", false)
		return http.StatusBadRequest
	}
	page := queryParams.page

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
			result, err := server.store.ListAssetsFiltered(request.Context(), claims, queryParams.siteID, queryParams.assetFilter, page)
			if err != nil {
				return server.writeStoreError(writer, request, err)
			}
			collection, err := collectionWithCursor(server.cursorCodec, route.resource, queryParams.cursorParent, route.action, claims, result, func(item Asset) (string, string) {
				return item.DisplayName, item.ID
			})
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
			result, err := server.store.ListDevicesFiltered(request.Context(), claims, queryParams.siteID, queryParams.deviceFilter, page)
			if err != nil {
				return server.writeStoreError(writer, request, err)
			}
			collection, err := collectionWithCursor(server.cursorCodec, route.resource, queryParams.cursorParent, route.action, claims, result, func(item Device) (string, string) {
				return item.DisplayName, item.ID
			})
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
	case "templates":
		result, err := server.store.ListTemplates(request.Context(), claims, queryParams.templateKind, page)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		collection, err := collectionWithCursor(server.cursorCodec, route.resource, queryParams.cursorParent, route.action, claims, result, func(item Template) (string, string) {
			return item.TemplateKey, item.ID
		})
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, collection)
		return http.StatusOK
	case "template-revision":
		item, err := server.store.GetTemplateRevision(request.Context(), claims, route.id)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, item)
		return http.StatusOK
	case "site-template-assignments":
		result, err := server.store.ListSiteTemplateAssignments(request.Context(), claims, route.parentID, queryParams.targetType, queryParams.targetID, page)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		collection, err := collectionWithCursor(server.cursorCodec, route.resource, queryParams.cursorParent, route.action, claims, result, func(item TemplateAssignment) (string, string) {
			return string(item.TargetType), item.ID
		})
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, collection)
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
	case "tariff":
		tariff, err := server.store.GetSiteTariff(request.Context(), claims, route.parentID)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, tariff)
		return http.StatusOK
	case "operating-schedule":
		schedule, err := server.store.GetOperatingSchedule(request.Context(), claims, route.parentID)
		if err != nil {
			return server.writeStoreError(writer, request, err)
		}
		writeJSON(writer, http.StatusOK, schedule)
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

type registryListQuery struct {
	page         PageRequest
	cursorParent string
	siteID       string
	assetFilter  AssetFilter
	deviceFilter DeviceFilter
	templateKind TemplateKind
	targetType   TemplateKind
	targetID     string
}

func requiredCoreActionForRoute(route registryRoute, claims registryauth.GrantClaims) registryauth.Action {
	if route.resource == "total-electricity-sources" && len(claims.Actions) == 1 && claims.Actions[0] == registryauth.ActionAssetModelRead {
		return registryauth.ActionAssetModelRead
	}
	switch route.resource {
	case "accounting-recalculate":
		for _, candidate := range []registryauth.Action{
			registryauth.ActionBillingManage,
			registryauth.ActionSavingsManage,
		} {
			for _, granted := range claims.Actions {
				if registryauth.ActionAllows(granted, candidate) {
					return candidate
				}
			}
		}
	case "templates", "template-revision", "site-template-assignments":
		for _, candidate := range []registryauth.Action{
			registryauth.ActionTemplateManage,
			registryauth.ActionAssetModelRead,
			registryauth.ActionDeviceList,
		} {
			for _, granted := range claims.Actions {
				if registryauth.ActionAllows(granted, candidate) {
					return candidate
				}
			}
		}
	}
	return route.action
}

func (server *server) parseRegistryListQuery(request *http.Request, route registryRoute, claims registryauth.GrantClaims) (registryListQuery, error) {
	if !route.list {
		if request.URL.RawQuery != "" {
			return registryListQuery{}, ErrInvalidPage
		}
		return registryListQuery{}, nil
	}
	query := request.URL.Query()
	for name, values := range query {
		if !isAllowedListQueryParam(route, name) {
			return registryListQuery{}, ErrInvalidPage
		}
		if name != "tag" && len(values) != 1 {
			return registryListQuery{}, ErrInvalidPage
		}
	}

	parsed := registryListQuery{siteID: route.parentID}
	cursorParent := route.parentID

	switch route.resource {
	case "space-children":
		parentSpaceID := query.Get("parentSpaceId")
		if parentSpaceID != "" && !validUUIDv7(parentSpaceID) {
			return registryListQuery{}, ErrInvalidPage
		}
		cursorParent += "|" + parentSpaceID
	case "assets", "devices":
		if route.parentID == "" {
			siteID := query.Get("siteId")
			if siteID != "" && !validUUIDv7(siteID) {
				return registryListQuery{}, ErrInvalidPage
			}
			parsed.siteID = siteID
		}
		common, tags, tagKeys, err := parseCommonResourceFilterParams(query)
		if err != nil {
			return registryListQuery{}, err
		}
		if route.resource == "assets" {
			filter := AssetFilter{
				Status:             common.status,
				AssetType:          query.Get("assetType"),
				SpaceID:            common.spaceID,
				TemplateKey:        common.templateKey,
				TemplateRevisionID: common.templateRevisionID,
				Tags:               tags,
				TagKeys:            tagKeys,
			}
			if err := filter.Validate(); err != nil {
				return registryListQuery{}, ErrInvalidPage
			}
			parsed.assetFilter = filter
			cursorParent = cursorParentWithFilter(parsed.siteID, filter.CanonicalKey())
		} else {
			filter := DeviceFilter{
				Status:             common.status,
				DeviceType:         query.Get("deviceType"),
				SpaceID:            common.spaceID,
				TemplateKey:        common.templateKey,
				TemplateRevisionID: common.templateRevisionID,
				Tags:               tags,
				TagKeys:            tagKeys,
			}
			if err := filter.Validate(); err != nil {
				return registryListQuery{}, ErrInvalidPage
			}
			parsed.deviceFilter = filter
			cursorParent = cursorParentWithFilter(parsed.siteID, filter.CanonicalKey())
		}
	case "templates":
		kind := TemplateKind(query.Get("templateKind"))
		switch kind {
		case "", TemplateAsset, TemplateDevice, TemplatePoint:
		default:
			return registryListQuery{}, ErrInvalidPage
		}
		parsed.templateKind = kind
		cursorParent = cursorParentWithFilter("", string(kind))
	case "site-template-assignments":
		targetType := TemplateKind(query.Get("targetType"))
		switch targetType {
		case "", TemplateAsset, TemplateDevice, TemplatePoint:
		default:
			return registryListQuery{}, ErrInvalidPage
		}
		targetID := query.Get("targetId")
		if targetID != "" && !validUUIDv7(targetID) {
			return registryListQuery{}, ErrInvalidPage
		}
		parsed.targetType = targetType
		parsed.targetID = targetID
		filterSuffix := ""
		if targetType != "" || targetID != "" {
			filterSuffix = string(targetType) + ":" + targetID
		}
		cursorParent = cursorParentWithFilter(route.parentID, filterSuffix)
	}

	limit := 0
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return registryListQuery{}, ErrInvalidPage
		}
		limit = value
	}
	limit, err := normalizedLimit(limit)
	if err != nil {
		return registryListQuery{}, err
	}
	page, err := server.cursorCodec.Decode(query.Get("cursor"), route.resource, cursorParent, route.action, claims)
	if err != nil {
		return registryListQuery{}, err
	}
	page.Limit = limit
	parsed.page = page
	parsed.cursorParent = cursorParent
	return parsed, nil
}

func isAllowedListQueryParam(route registryRoute, name string) bool {
	if name == "limit" || name == "cursor" {
		return true
	}
	switch route.resource {
	case "space-children":
		return name == "parentSpaceId"
	case "assets":
		switch name {
		case "status", "assetType", "spaceId", "templateKey", "templateRevisionId", "tag":
			return true
		case "siteId":
			return route.parentID == ""
		}
	case "devices":
		switch name {
		case "status", "deviceType", "spaceId", "templateKey", "templateRevisionId", "tag":
			return true
		case "siteId":
			return route.parentID == ""
		}
	case "templates":
		return name == "templateKind"
	case "site-template-assignments":
		return name == "targetType" || name == "targetId"
	}
	return false
}

type commonFilterValues struct {
	status             string
	spaceID            string
	templateKey        string
	templateRevisionID string
}

func parseCommonResourceFilterParams(query map[string][]string) (commonFilterValues, map[string]string, []string, error) {
	first := func(key string) string {
		if values := query[key]; len(values) > 0 {
			return values[0]
		}
		return ""
	}
	common := commonFilterValues{
		status:             first("status"),
		spaceID:            first("spaceId"),
		templateKey:        first("templateKey"),
		templateRevisionID: first("templateRevisionId"),
	}
	rawTags := query["tag"]
	if len(rawTags) == 0 {
		return common, nil, nil, nil
	}
	tags := map[string]string{}
	var tagKeys []string
	for _, raw := range rawTags {
		if key, val, hasValue := strings.Cut(raw, ":"); hasValue {
			if prev, exists := tags[key]; exists && prev != val {
				return commonFilterValues{}, nil, nil, ErrInvalidPage
			}
			tags[key] = val
		} else {
			tagKeys = append(tagKeys, raw)
		}
	}
	return common, tags, tagKeys, nil
}

func collectionWithCursor[T any](
	codec *CursorCodec,
	resource string,
	parentID string,
	action registryauth.Action,
	claims registryauth.GrantClaims,
	result PageResult[T],
	keyFn func(T) (string, string),
) (Collection[T], error) {
	collection := Collection[T]{Items: result.Items, HasMore: result.HasMore}
	if result.HasMore && len(result.Items) > 0 {
		last := result.Items[len(result.Items)-1]
		cursorName, cursorID := keyFn(last)
		cursor, err := codec.Encode(resource, parentID, action, claims, cursorName, cursorID)
		if err != nil {
			return Collection[T]{}, err
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
	case method == http.MethodGet && len(segments) == 1 && segments[0] == "assets":
		return registryRoute{template: RegistryPathPrefix + "assets", resource: "assets", action: registryauth.ActionAssetList, list: true}, true
	case method == http.MethodGet && len(segments) == 2 && segments[0] == "assets":
		return registryRoute{template: RegistryPathPrefix + "assets/{assetId}", resource: "assets", id: segments[1], action: registryauth.ActionAssetRead}, true
	case method == http.MethodGet && len(segments) == 1 && segments[0] == "devices":
		return registryRoute{template: RegistryPathPrefix + "devices", resource: "devices", action: registryauth.ActionDeviceList, list: true}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "devices":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/devices", resource: "devices", parentID: segments[1], action: registryauth.ActionDeviceList, list: true}, true
	case method == http.MethodPost && len(segments) == 3 && segments[0] == "sites" && segments[2] == "devices":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/devices", resource: "device-write", parentID: segments[1], action: registryauth.ActionDeviceWrite, write: true}, true
	case method == http.MethodPatch && len(segments) == 4 && segments[0] == "sites" && segments[2] == "devices":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/devices/{deviceId}", resource: "device-write", parentID: segments[1], id: segments[3], action: registryauth.ActionDeviceWrite, write: true}, true
	case method == http.MethodPost && len(segments) == 5 && segments[0] == "sites" && segments[2] == "devices" && segments[4] == "replace-hardware":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/devices/{deviceId}/replace-hardware", resource: "device-replace-hardware", parentID: segments[1], id: segments[3], action: registryauth.ActionDeviceWrite, write: true}, true
	case method == http.MethodPost && len(segments) == 5 && segments[0] == "sites" && segments[2] == "devices" && segments[4] == "transfer":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/devices/{deviceId}/transfer", resource: "device-transfer", parentID: segments[1], id: segments[3], action: registryauth.ActionDeviceWrite, write: true}, true
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
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "meter-bindings":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/meter-bindings", resource: "site-meter-bindings", parentID: segments[1], action: registryauth.ActionMeterBindingResolve}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "total-electricity-sources":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/total-electricity-sources", resource: "total-electricity-sources", parentID: segments[1], action: registryauth.ActionMeterBindingResolve}, true
	case method == http.MethodPost && len(segments) == 4 && segments[0] == "sites" && segments[2] == "bindings" && segments[3] == "rebind":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/bindings/rebind", resource: "binding-write", parentID: segments[1], action: registryauth.ActionBindingWrite, write: true}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "asset-model":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/asset-model", resource: "asset-model", parentID: segments[1], action: registryauth.ActionAssetModelRead}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "tariff":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/tariff", resource: "tariff", parentID: segments[1], action: registryauth.ActionTariffRead}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "import-tariffs":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/import-tariffs", resource: "import-tariffs", parentID: segments[1], action: registryauth.ActionTariffRead}, true
	case method == http.MethodGet && len(segments) == 2 && segments[0] == "devices":
		return registryRoute{template: RegistryPathPrefix + "devices/{deviceId}", resource: "devices", id: segments[1], action: registryauth.ActionDeviceRead}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "devices" && segments[2] == "points":
		return registryRoute{template: RegistryPathPrefix + "devices/{deviceId}/points", resource: "device-points", parentID: segments[1], action: registryauth.ActionDeviceRead, list: true}, true
	case method == http.MethodGet && len(segments) == 1 && segments[0] == "templates":
		return registryRoute{template: RegistryPathPrefix + "templates", resource: "templates", action: registryauth.ActionDeviceList, list: true}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "templates" && segments[1] == "revisions":
		return registryRoute{template: RegistryPathPrefix + "templates/revisions/{revisionId}", resource: "template-revision", id: segments[2], action: registryauth.ActionDeviceList}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "template-assignments":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/template-assignments", resource: "site-template-assignments", parentID: segments[1], action: registryauth.ActionAssetModelRead, list: true}, true
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
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "operating-schedule":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/operating-schedule", resource: "operating-schedule", parentID: segments[1], action: registryauth.ActionSiteRead}, true
	case method == http.MethodPut && len(segments) == 3 && segments[0] == "sites" && segments[2] == "operating-schedule":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/operating-schedule", resource: "operating-schedule-write", parentID: segments[1], action: registryauth.ActionScheduleManage, write: true}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "operating-exceptions":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/operating-exceptions", resource: "operating-exceptions", parentID: segments[1], action: registryauth.ActionSiteRead}, true
	case method == http.MethodPost && len(segments) == 3 && segments[0] == "sites" && segments[2] == "operating-exceptions":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/operating-exceptions", resource: "operating-exception-create", parentID: segments[1], action: registryauth.ActionScheduleRequest, write: true}, true
	case method == http.MethodPost && len(segments) == 5 && segments[0] == "sites" && segments[2] == "operating-exceptions" && segments[4] == "decide":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/operating-exceptions/{exceptionId}/decide", resource: "operating-exception-decide", parentID: segments[1], id: segments[3], action: registryauth.ActionScheduleRequest, write: true}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "electricity-budgets":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/electricity-budgets", resource: "electricity-budgets", parentID: segments[1], action: registryauth.ActionSiteRead}, true
	case method == http.MethodPut && len(segments) == 3 && segments[0] == "sites" && segments[2] == "electricity-budgets":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/electricity-budgets", resource: "electricity-budget-write", parentID: segments[1], action: registryauth.ActionBillingManage, write: true}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "utility-bills":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/utility-bills", resource: "utility-bills", parentID: segments[1], action: registryauth.ActionSiteRead}, true
	case method == http.MethodPost && len(segments) == 3 && segments[0] == "sites" && segments[2] == "utility-bills":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/utility-bills", resource: "utility-bill-write", parentID: segments[1], action: registryauth.ActionBillingManage, write: true}, true
	case method == http.MethodPut && len(segments) == 4 && segments[0] == "sites" && segments[2] == "utility-bills":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/utility-bills/{billId}", resource: "utility-bill-write", parentID: segments[1], id: segments[3], action: registryauth.ActionBillingManage, write: true}, true
	case method == http.MethodPost && len(segments) == 5 && segments[0] == "sites" && segments[2] == "utility-bills" && segments[4] == "confirm":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/utility-bills/{billId}/confirm", resource: "utility-bill-confirm", parentID: segments[1], id: segments[3], action: registryauth.ActionBillingManage, write: true}, true
	case method == http.MethodPost && len(segments) == 4 && segments[0] == "sites" && segments[2] == "accounting" && segments[3] == "recalculate":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/accounting/recalculate", resource: "accounting-recalculate", parentID: segments[1], action: registryauth.ActionBillingManage, write: true}, true
	case method == http.MethodPost && len(segments) == 4 && segments[0] == "sites" && segments[2] == "accounting" && segments[3] == "dirty-windows":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/accounting/dirty-windows", resource: "accounting-dirty-window", parentID: segments[1], action: registryauth.ActionMeterBindingResolve, write: true}, true
	case method == http.MethodGet && len(segments) == 4 && segments[0] == "sites" && segments[2] == "accounting" && segments[3] == "recalculate-jobs":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/accounting/recalculate-jobs", resource: "accounting-recalculate-jobs", parentID: segments[1], action: registryauth.ActionSiteRead}, true
	case method == http.MethodGet && len(segments) == 4 && segments[0] == "sites" && segments[2] == "accounting" && segments[3] == "holidays":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/accounting/holidays", resource: "accounting-holidays", parentID: segments[1], action: registryauth.ActionSiteRead}, true
	case method == http.MethodPut && len(segments) == 5 && segments[0] == "sites" && segments[2] == "accounting" && segments[3] == "holidays":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/accounting/holidays/{date}", resource: "accounting-holiday-upsert", parentID: segments[1], id: segments[4], action: registryauth.ActionBillingManage, write: true}, true
	case method == http.MethodDelete && len(segments) == 5 && segments[0] == "sites" && segments[2] == "accounting" && segments[3] == "holidays":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/accounting/holidays/{date}", resource: "accounting-holiday-delete", parentID: segments[1], id: segments[4], action: registryauth.ActionBillingManage, write: true}, true
	case method == http.MethodGet && len(segments) == 4 && segments[0] == "sites" && segments[2] == "accounting" && segments[3] == "meter-ratios":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/accounting/meter-ratios", resource: "accounting-meter-ratios", parentID: segments[1], action: registryauth.ActionSiteRead}, true
	case method == http.MethodPost && len(segments) == 4 && segments[0] == "sites" && segments[2] == "accounting" && segments[3] == "meter-ratios":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/accounting/meter-ratios", resource: "accounting-meter-ratio-create", parentID: segments[1], action: registryauth.ActionBillingManage, write: true}, true
	case method == http.MethodPost && len(segments) == 6 && segments[0] == "sites" && segments[2] == "accounting" && segments[3] == "meter-ratios" && segments[5] == "retire":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/accounting/meter-ratios/{versionId}/retire", resource: "accounting-meter-ratio-retire", parentID: segments[1], id: segments[4], action: registryauth.ActionBillingManage, write: true}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "control-state":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/control-state", resource: "site-control-state", parentID: segments[1], action: registryauth.ActionSiteRead}, true
	case method == http.MethodPut && len(segments) == 3 && segments[0] == "sites" && segments[2] == "control-mode":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/control-mode", resource: "site-control-mode-write", parentID: segments[1], action: registryauth.ActionScheduleManage, write: true}, true
	case method == http.MethodPut && len(segments) == 3 && segments[0] == "assets" && segments[2] == "control-lock":
		return registryRoute{template: RegistryPathPrefix + "assets/{assetId}/control-lock", resource: "asset-control-lock-write", id: segments[1], action: registryauth.ActionScheduleManage, write: true}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "auto-control-lease":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/auto-control-lease", resource: "auto-control-lease", parentID: segments[1], action: registryauth.ActionSiteRead}, true
	case method == http.MethodPut && len(segments) == 3 && segments[0] == "sites" && segments[2] == "auto-control-lease":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/auto-control-lease", resource: "auto-control-lease-write", parentID: segments[1], action: registryauth.ActionScheduleManage, write: true}, true
	case method == http.MethodPost && len(segments) == 4 && segments[0] == "sites" && segments[2] == "auto-control-lease" && segments[3] == "revoke":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/auto-control-lease/revoke", resource: "auto-control-lease-revoke", parentID: segments[1], action: registryauth.ActionScheduleManage, write: true}, true
	case method == http.MethodGet && len(segments) == 3 && segments[0] == "sites" && segments[2] == "optimization-execution-monitors":
		return registryRoute{template: RegistryPathPrefix + "sites/{siteId}/optimization-execution-monitors", resource: "optimization-execution-monitors", parentID: segments[1], action: registryauth.ActionSiteRead}, true
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
