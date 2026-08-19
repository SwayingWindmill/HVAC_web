package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/presentationmodel"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/s2telemetryapi"
)

const (
	maximumDashboardDevicePages  = 10
	maximumDashboardAlarmPages   = 10
	dashboardPageLimit           = 200
	dashboardTelemetryBatchLimit = 100
	dashboardStreamInterval      = 5 * time.Second
	dashboardStreamLifetime      = time.Minute
)

func matchPublicDashboardRoute(path string) (string, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) != 5 || segments[0] != "api" || segments[1] != "v1" || segments[2] != "sites" || segments[4] != "dashboard-summary" {
		return "", false
	}
	if !isLowerUUIDv7(segments[3]) {
		return "", false
	}
	return segments[3], true
}

func matchPublicDashboardStreamRoute(path string) (string, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) != 6 || segments[0] != "api" || segments[1] != "v1" || segments[2] != "sites" || segments[4] != "dashboard-summary" || segments[5] != "events" {
		return "", false
	}
	if !isLowerUUIDv7(segments[3]) {
		return "", false
	}
	return segments[3], true
}

func dispatchDashboardRoute(h *handler, writer http.ResponseWriter, request *http.Request, siteID string) {
	if request.Method != http.MethodGet {
		writeMethodNotAllowedFor(writer, request, http.MethodGet)
		return
	}
	if len(request.URL.Query()) != 0 {
		writeProblem(writer, request, http.StatusBadRequest, "DASHBOARD_QUERY_INVALID", "Dashboard query invalid", "The Site dashboard summary does not accept query parameters.", false, nil)
		return
	}
	decision := routeDecisionFromContext(request.Context())
	if decision.SelectedOwner != ownershipregistry.OwnerPresentation {
		writeProblem(writer, request, http.StatusServiceUnavailable, "DASHBOARD_UNAVAILABLE", "Dashboard unavailable", "The Site dashboard projection is not the selected owner for this route.", true, nil)
		return
	}
	h.GetSiteDashboardSummary(writer, request, siteID)
}

func dispatchDashboardStreamRoute(h *handler, writer http.ResponseWriter, request *http.Request, siteID string) {
	if request.Method != http.MethodGet {
		writeMethodNotAllowedFor(writer, request, http.MethodGet)
		return
	}
	decision := routeDecisionFromContext(request.Context())
	if decision.SelectedOwner != ownershipregistry.OwnerPresentation {
		writeProblem(writer, request, http.StatusServiceUnavailable, "DASHBOARD_UNAVAILABLE", "Dashboard unavailable", "The Site dashboard projection is not the selected owner for this route.", true, nil)
		return
	}
	h.StreamSiteDashboardSummary(writer, request, siteID)
}

func (h *handler) GetSiteDashboardSummary(writer http.ResponseWriter, request *http.Request, siteID string) {
	session, ok := h.dashboardSession(writer, request)
	if !ok {
		return
	}
	summary, failure := h.buildSiteDashboardSummary(request, session, siteID)
	if failure != nil {
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, summary)
}

func (h *handler) buildSiteDashboardSummary(request *http.Request, session bffSession, siteID string) (platformapi.SiteDashboardSummary, *dashboardFailure) {
	site, failure := h.dashboardSite(request, session, siteID)
	if failure != nil {
		return platformapi.SiteDashboardSummary{}, failure
	}

	asOf := h.now().UTC()
	population := h.dashboardDevicePopulation(request, session, siteID)
	energy := h.dashboardLocalDayEnergy(request, session, site, asOf)
	alarms := h.dashboardOpenAlarms(request, session, siteID)

	currentPower := unavailableMetric(presentationmodel.StateNotIntegrated, "METRIC_QUERY_PORT", "Current power does not yet have a released Site metric query port.")
	cost := unavailableMetric(presentationmodel.StateNotIntegrated, "SETTLEMENT_QUERY_PORT", "Authoritative Site cost is not integrated yet.")
	baselineSavings := unavailableMetric(presentationmodel.StateNotIntegrated, "METRIC_QUERY_PORT", "Baseline savings is not integrated yet.")
	cop := unavailableMetric(presentationmodel.StateNotIntegrated, "METRIC_QUERY_PORT", "COP is not integrated yet.")

	completeness := presentationmodel.WorstState(
		stateFromPlatform(population.State),
		stateFromPlatform(energy.State),
		stateFromPlatform(alarms.State),
	)
	quality := completeness
	if quality == presentationmodel.StateReady && (population.Offline > 0 || population.Stale > 0 || derefInt(alarms.ActiveCount) > 0) {
		quality = presentationmodel.StateAttention
	}

	dataWatermark := oldestInstant(population.EvaluatedAt, energy.DataWatermark, alarms.Watermark)
	aggregateWatermark := energy.AggregateWatermark
	reasons := dashboardReasons(population, energy, alarms)

	return platformapi.SiteDashboardSummary{
		SchemaVersion:      1,
		TenantID:           session.TenantID,
		SiteID:             siteID,
		SiteTimezone:       site.Timezone,
		AsOf:               dashboardInstant(asOf),
		GeneratedAt:        dashboardInstant(h.now().UTC()),
		DataWatermark:      dataWatermark,
		AggregateWatermark: aggregateWatermark,
		Completeness:       platformState(completeness),
		Quality:            platformState(quality),
		Reasons:            reasons,
		DevicePopulation:   population,
		SlowMetrics: platformapi.SiteDashboardSlowMetrics{
			SiteLocalDayEnergy: energy,
			Cost:               cost,
			BaselineSavings:    baselineSavings,
			COP:                cop,
		},
		FastMetrics: platformapi.SiteDashboardFastMetrics{
			CurrentPower: currentPower,
			OpenAlarms:   alarms,
		},
	}, nil
}

func (h *handler) StreamSiteDashboardSummary(writer http.ResponseWriter, request *http.Request, siteID string) {
	baseGeneratedAt, ok := dashboardStreamBaseGeneratedAt(request.URL.Query())
	if !ok {
		writeProblem(writer, request, http.StatusBadRequest, "DASHBOARD_STREAM_INVALID", "Dashboard stream invalid", "The dashboard stream requires exactly one valid baseGeneratedAt REST Snapshot timestamp.", false, nil)
		return
	}
	session, ok := h.dashboardSession(writer, request)
	if !ok {
		return
	}
	if h.identity == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "DASHBOARD_STREAM_UNAVAILABLE", "Dashboard stream unavailable", "Session revocation validation is unavailable.", true, nil)
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeProblem(writer, request, http.StatusServiceUnavailable, "DASHBOARD_STREAM_UNAVAILABLE", "Dashboard stream unavailable", "Streaming is not supported by the active HTTP boundary.", true, nil)
		return
	}

	summary, failure := h.buildSiteDashboardSummary(request, session, siteID)
	if failure != nil {
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}

	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("Cache-Control", "private, no-store, no-transform")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)

	if err := writeDashboardSummaryEvent(writer, flusher, platformapi.SiteDashboardSummaryDelta{
		SchemaVersion: 1, BaseGeneratedAt: baseGeneratedAt, Summary: summary,
	}); err != nil {
		return
	}
	baseGeneratedAt = summary.GeneratedAt

	summaryTicker := time.NewTicker(dashboardStreamInterval)
	defer summaryTicker.Stop()
	sessionTicker := time.NewTicker(h.identity.config.RevocationObjective)
	defer sessionTicker.Stop()
	lifetime := time.NewTimer(dashboardStreamLifetime)
	defer lifetime.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-lifetime.C:
			return
		case <-sessionTicker.C:
			refreshed, failure := h.identitySession(request)
			if failure != nil || !sameDashboardSession(session, refreshed) {
				return
			}
			session = refreshed
		case <-summaryTicker.C:
			next, failure := h.buildSiteDashboardSummary(request, session, siteID)
			if failure != nil {
				return
			}
			if err := writeDashboardSummaryEvent(writer, flusher, platformapi.SiteDashboardSummaryDelta{
				SchemaVersion: 1, BaseGeneratedAt: baseGeneratedAt, Summary: next,
			}); err != nil {
				return
			}
			baseGeneratedAt = next.GeneratedAt
		}
	}
}

func sameDashboardSession(expected, actual bffSession) bool {
	return expected.ID == actual.ID &&
		expected.TenantID == actual.TenantID &&
		expected.Principal.Subject == actual.Principal.Subject &&
		expected.Principal.Issuer == actual.Principal.Issuer
}

func dashboardStreamBaseGeneratedAt(query url.Values) (string, bool) {
	if len(query) != 1 || len(query["baseGeneratedAt"]) != 1 {
		return "", false
	}
	value := strings.TrimSpace(query.Get("baseGeneratedAt"))
	parsed, err := time.Parse("2006-01-02T15:04:05.000Z", value)
	if err != nil || dashboardInstant(parsed) != value {
		return "", false
	}
	return value, true
}

func writeDashboardSummaryEvent(writer io.Writer, flusher http.Flusher, delta platformapi.SiteDashboardSummaryDelta) error {
	payload, err := json.Marshal(delta)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "event: dashboard-summary\ndata: %s\n\n", payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

type dashboardFailure struct {
	status    int
	code      string
	title     string
	detail    string
	retryable bool
}

func (h *handler) dashboardSession(writer http.ResponseWriter, request *http.Request) (bffSession, bool) {
	if session, ok := routeSessionFromContext(request.Context()); ok {
		return session, true
	}
	if h.identity == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "DASHBOARD_UNAVAILABLE", "Dashboard unavailable", "Session validation is unavailable.", true, nil)
		return bffSession{}, false
	}
	session, failure := h.identitySession(request)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return bffSession{}, false
	}
	return session, true
}

func (h *handler) dashboardSite(request *http.Request, session bffSession, siteID string) (platformapi.Site, *dashboardFailure) {
	if h.registry == nil {
		return platformapi.Site{}, &dashboardFailure{http.StatusServiceUnavailable, "DASHBOARD_UNAVAILABLE", "Dashboard unavailable", "Registry is not configured.", true}
	}
	authorization, authFailure := h.authorizeRegistry(request.Context(), session, registryauth.ActionSiteRead)
	if authFailure != nil {
		status := authFailure.status
		code := "DASHBOARD_UNAVAILABLE"
		title := "Dashboard unavailable"
		if status == http.StatusForbidden || status == http.StatusNotFound {
			status, code, title = http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found"
		}
		return platformapi.Site{}, &dashboardFailure{status, code, title, authFailure.detail, authFailure.retryable}
	}
	if _, allowed := authorization.allowedSiteIDs[siteID]; !allowed {
		return platformapi.Site{}, &dashboardFailure{http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false}
	}
	route := publicRegistryRoute{template: "/api/v1/sites/{siteId}", internalPath: "/internal/v1/registry/sites/" + siteID, resource: "sites", action: registryauth.ActionSiteRead, scopeID: siteID}
	result := h.executeCoreRegistry(request.Context(), route, "", authorization.coreGrant, routeDecisionFromContext(request.Context()))
	if result.status != http.StatusOK {
		return platformapi.Site{}, &dashboardFailure{http.StatusServiceUnavailable, "DASHBOARD_UNAVAILABLE", "Dashboard unavailable", "Authoritative Site lookup is unavailable.", true}
	}
	var site platformapi.Site
	if decodeDashboardJSON(result.body, &site) != nil || validateSite(site) != nil || site.ID != siteID || site.TenantID != session.TenantID {
		return platformapi.Site{}, &dashboardFailure{http.StatusServiceUnavailable, "DASHBOARD_UNAVAILABLE", "Dashboard unavailable", "Authoritative Site lookup returned an invalid projection.", true}
	}
	return site, nil
}

func (h *handler) dashboardDevicePopulation(request *http.Request, session bffSession, siteID string) platformapi.DevicePopulationSummary {
	base := platformapi.DevicePopulationSummary{State: platformapi.PresentationStateUnavailable, DenominatorPolicy: presentationmodel.DenominatorApplicableKnownPresence}
	if h.registry == nil || h.telemetry == nil {
		base.State = platformapi.PresentationStateNotIntegrated
		return base
	}
	authorization, authFailure := h.authorizeRegistry(request.Context(), session, registryauth.ActionDeviceList)
	if authFailure != nil {
		if authFailure.status == http.StatusForbidden {
			base.State = platformapi.PresentationStateNotAuthorized
		}
		return base
	}
	if _, allowed := authorization.allowedSiteIDs[siteID]; !allowed {
		base.State = platformapi.PresentationStateNotAuthorized
		return base
	}

	devices := make([]platformapi.Device, 0, dashboardPageLimit)
	cursor := ""
	complete := true
	for page := 0; page < maximumDashboardDevicePages; page++ {
		query := url.Values{"limit": {strconv.Itoa(dashboardPageLimit)}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		route := publicRegistryRoute{template: "/api/v1/sites/{siteId}/devices", internalPath: "/internal/v1/registry/sites/" + siteID + "/devices", resource: "devices", action: registryauth.ActionDeviceList, scopeID: siteID, list: true}
		result := h.executeCoreRegistry(request.Context(), route, query.Encode(), authorization.coreGrant, routeDecisionFromContext(request.Context()))
		if result.status != http.StatusOK {
			base.Registered = len(devices)
			return base
		}
		var collection platformapi.DeviceCollection
		if decodeDashboardJSON(result.body, &collection) != nil {
			base.Registered = len(devices)
			return base
		}
		for _, device := range collection.Items {
			if device.TenantID != session.TenantID || device.SiteID != siteID || !isLowerUUIDv7(device.ID) {
				base.Registered = len(devices)
				return base
			}
		}
		devices = append(devices, collection.Items...)
		if !collection.HasMore {
			cursor = ""
			break
		}
		if collection.NextCursor == nil || strings.TrimSpace(*collection.NextCursor) == "" {
			base.Registered = len(devices)
			return base
		}
		cursor = *collection.NextCursor
		if page == maximumDashboardDevicePages-1 {
			complete = false
		}
	}
	if cursor != "" {
		complete = false
	}
	if len(devices) == 0 {
		projected := presentationmodel.ProjectDevicePopulation(0, complete, nil)
		return platformPopulation(projected)
	}

	caller := telemetryCaller{principal: session.Principal, tenantID: session.TenantID, contextID: session.ID, expiresAt: session.ExpiresAt}
	activeDevices, observations := dashboardPopulationInputs(devices)
	for start := 0; start < len(activeDevices); start += dashboardTelemetryBatchLimit {
		end := min(start+dashboardTelemetryBatchLimit, len(activeDevices))
		chunk := activeDevices[start:end]
		targets := make([]telemetryauth.Target, len(chunk))
		batch := s2telemetryapi.BatchGetObservationSnapshotsRequest{Requests: make([]s2telemetryapi.ObservationSnapshotTarget, len(chunk))}
		for index, device := range chunk {
			targets[index] = telemetryauth.Target{DeviceID: device.ID, Keys: []string{}}
			batch.Requests[index] = s2telemetryapi.ObservationSnapshotTarget{RequestId: "dashboard-" + strconv.Itoa(start+index), DeviceId: s2telemetryapi.UUIDv7(device.ID), Keys: []s2telemetryapi.TelemetryKey{}}
		}
		authorized, failure := h.authorizeTelemetry(request.Context(), request, caller, telemetryauth.ActionBatchRead, targets)
		if failure != nil {
			if failure.status == http.StatusForbidden {
				base.State = platformapi.PresentationStateNotAuthorized
				base.Registered = len(devices)
				return base
			}
			complete = false
			for range chunk {
				observations = append(observations, presentationmodel.DevicePopulationObservation{Applicability: "APPLICABLE", Availability: "UNAVAILABLE", Presence: "UNKNOWN", DisplayState: "UNAVAILABLE"})
			}
			continue
		}
		body, err := json.Marshal(batch)
		if err != nil {
			complete = false
			continue
		}
		raw, runtimeFailure := h.executeTelemetryRuntime(request.Context(), request, http.MethodPost, internalTelemetryBatchPath, nil, body, authorized.grant)
		if runtimeFailure != nil {
			complete = false
			for range chunk {
				observations = append(observations, presentationmodel.DevicePopulationObservation{Applicability: "APPLICABLE", Availability: "UNAVAILABLE", Presence: "UNKNOWN", DisplayState: "UNAVAILABLE"})
			}
			continue
		}
		var response s2telemetryapi.BatchGetObservationSnapshotsResponse
		if decodeDashboardJSON(raw, &response) != nil || len(response.Items) != len(chunk) {
			complete = false
			for range chunk {
				observations = append(observations, presentationmodel.DevicePopulationObservation{Applicability: "APPLICABLE", Availability: "UNAVAILABLE", Presence: "UNKNOWN", DisplayState: "UNAVAILABLE"})
			}
			continue
		}
		for index, item := range response.Items {
			if item.Success == nil || item.Success.DeviceId != s2telemetryapi.UUIDv7(chunk[index].ID) {
				complete = false
				observations = append(observations, presentationmodel.DevicePopulationObservation{Applicability: "APPLICABLE", Availability: "UNAVAILABLE", Presence: "UNKNOWN", DisplayState: "UNAVAILABLE"})
				continue
			}
			snapshot := item.Success.Snapshot
			presence := "UNKNOWN"
			if snapshot.Presence.CurrentState != nil {
				presence = string(*snapshot.Presence.CurrentState)
			}
			display := "UNKNOWN"
			if snapshot.DisplayState != nil {
				display = string(*snapshot.DisplayState)
			}
			evaluatedAt, _ := time.Parse("2006-01-02T15:04:05.000Z", string(snapshot.EvaluatedAt))
			observations = append(observations, presentationmodel.DevicePopulationObservation{
				Applicability: string(snapshot.Presence.Applicability), Availability: string(snapshot.EvaluationAvailability),
				Presence: presence, DisplayState: display, EvaluatedAt: evaluatedAt,
			})
		}
	}
	projected := presentationmodel.ProjectDevicePopulation(len(devices), complete, observations)
	return platformPopulation(projected)
}

func (h *handler) dashboardLocalDayEnergy(request *http.Request, session bffSession, site platformapi.Site, asOf time.Time) platformapi.DashboardMetric {
	if h.analytics == nil {
		return unavailableMetric(presentationmodel.StateNotIntegrated, "ANALYTICS_ENERGY", "Energy analytics is not integrated.")
	}
	from, _, err := presentationmodel.SiteLocalDayBounds(asOf, site.Timezone)
	if err != nil {
		return unavailableMetric(presentationmodel.StateUnavailable, "ANALYTICS_ENERGY", "The Site timezone is not valid for local-day energy.")
	}
	query := analyticsmodel.EnergySeriesQuery{
		TenantID: session.TenantID, SiteID: site.ID, EnergyType: analyticsmodel.EnergyTypeElectricity,
		Granularity: analyticsmodel.GranularityDay, Timezone: site.Timezone, From: from, To: asOf,
		QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
	}
	grant, failure := h.authorizeAnalytics(request.Context(), request, session, query)
	if failure != nil {
		state := presentationmodel.StateUnavailable
		if failure.status == http.StatusForbidden {
			state = presentationmodel.StateNotAuthorized
		}
		return unavailableMetric(state, "ANALYTICS_ENERGY", failure.detail)
	}
	body, err := json.Marshal(query)
	if err != nil {
		return unavailableMetric(presentationmodel.StateUnavailable, "ANALYTICS_ENERGY", "The local-day energy request could not be encoded.")
	}
	raw, queryFailure := h.executeAnalyticsQuery(request.Context(), request, body, grant)
	if queryFailure != nil {
		return unavailableMetric(presentationmodel.StateUnavailable, "ANALYTICS_ENERGY", queryFailure.detail)
	}
	var response analyticsmodel.EnergySeriesResponse
	if decodeDashboardJSON(raw, &response) != nil {
		return unavailableMetric(presentationmodel.StateUnavailable, "ANALYTICS_ENERGY", "Energy analytics returned an invalid projection.")
	}
	metric := platformapi.DashboardMetric{State: platformapi.PresentationStateReady, Source: "ANALYTICS_ENERGY"}
	unit := "kWh"
	metric.Unit = &unit
	metric.DataWatermark = dashboardTimePointer(response.Metadata.DataWatermark)
	metric.AggregateWatermark = dashboardTimePointer(response.Metadata.AggregateWatermark)
	if len(response.Points) == 0 {
		metric.State = platformapi.PresentationStateNoData
		reason := "No valid Site-local-day energy facts are available."
		metric.Reason = &reason
		return metric
	}
	var total float64
	for _, point := range response.Points {
		total += point.EnergyKWh
	}
	metric.Value = &total
	if response.Metadata.Partial {
		metric.State = platformapi.PresentationStatePartial
		reason := "Energy analytics reports a partial result."
		metric.Reason = &reason
	} else if response.Metadata.QualitySummary.Invalid > 0 || response.Metadata.QualitySummary.Suspect > 0 {
		metric.State = platformapi.PresentationStateSuspect
		reason := "Energy analytics contains suspect or invalid source quality."
		metric.Reason = &reason
	}
	return metric
}

func (h *handler) dashboardOpenAlarms(request *http.Request, session bffSession, siteID string) platformapi.OpenAlarmSummary {
	result := platformapi.OpenAlarmSummary{State: platformapi.PresentationStateUnavailable}
	if h.alarm == nil {
		result.State = platformapi.PresentationStateNotIntegrated
		reason := "Alarm query is not integrated."
		result.Reason = &reason
		return result
	}
	route := publicAlarmRoute{template: "/api/v1/alarms", siteID: siteID, action: alarmauth.ActionRead}
	decision, failure := h.authorizeAlarm(request, session, route)
	if failure != nil {
		if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
			result.State = platformapi.PresentationStateNotAuthorized
		}
		reason := failure.detail
		result.Reason = &reason
		return result
	}
	contextToken, failure := h.signAlarmServiceContext(session, route, decision)
	if failure != nil {
		reason := failure.detail
		result.Reason = &reason
		return result
	}

	count := 0
	cursor := ""
	var highest alarmmodel.Severity
	var watermark time.Time
	for page := 0; page < maximumDashboardAlarmPages; page++ {
		pageRequest := request.Clone(request.Context())
		query := url.Values{"siteId": {siteID}, "condition": {string(alarmmodel.ConditionActive)}, "limit": {strconv.Itoa(dashboardPageLimit)}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		pageRequest.URL = cloneURL(request.URL)
		pageRequest.URL.RawQuery = query.Encode()
		raw, status, executionFailure := h.executeAlarmOperation(pageRequest, route, contextToken)
		if executionFailure != nil || status != http.StatusOK {
			result.State = platformapi.PresentationStateUnavailable
			reason := "Authoritative Alarm query is unavailable."
			result.Reason = &reason
			return result
		}
		var pageResult alarmmodel.ListResponse
		if decodeDashboardJSON(raw, &pageResult) != nil || pageResult.Validate(session.TenantID, siteID, dashboardPageLimit) != nil {
			result.State = platformapi.PresentationStateUnavailable
			reason := "Authoritative Alarm query returned an invalid projection."
			result.Reason = &reason
			return result
		}
		for _, alarm := range pageResult.Items {
			count++
			if severityRank(alarm.CurrentSeverity) > severityRank(highest) {
				highest = alarm.CurrentSeverity
			}
			if updated, err := time.Parse("2006-01-02T15:04:05.000Z", alarm.UpdatedAt); err == nil && updated.After(watermark) {
				watermark = updated
			}
		}
		if !pageResult.HasMore {
			result.State = platformapi.PresentationStateReady
			result.ActiveCount = &count
			if highest != "" {
				severity := string(highest)
				result.HighestSeverity = &severity
			}
			if !watermark.IsZero() {
				value := dashboardInstant(watermark)
				result.Watermark = &value
			}
			return result
		}
		if pageResult.NextCursor == nil || strings.TrimSpace(*pageResult.NextCursor) == "" {
			break
		}
		cursor = *pageResult.NextCursor
	}
	result.State = platformapi.PresentationStatePartial
	reason := "Active Alarm population exceeds the bounded dashboard query budget."
	result.Reason = &reason
	return result
}

func dashboardPopulationInputs(devices []platformapi.Device) ([]platformapi.Device, []presentationmodel.DevicePopulationObservation) {
	active := make([]platformapi.Device, 0, len(devices))
	observations := make([]presentationmodel.DevicePopulationObservation, 0, len(devices))
	for _, device := range devices {
		if device.Status != "ACTIVE" {
			observations = append(observations, presentationmodel.DevicePopulationObservation{Applicability: "NOT_APPLICABLE"})
			continue
		}
		active = append(active, device)
	}
	return active, observations
}

func platformPopulation(input presentationmodel.DevicePopulation) platformapi.DevicePopulationSummary {
	var evaluatedAt *string
	if input.EvaluatedAt != nil {
		value := dashboardInstant(*input.EvaluatedAt)
		evaluatedAt = &value
	}
	return platformapi.DevicePopulationSummary{
		State: platformState(input.State), Registered: input.Registered, Applicable: input.Applicable,
		Observable: input.Observable, Online: input.Online, Offline: input.Offline, Stale: input.Stale,
		Unknown: input.Unknown, Unavailable: input.Unavailable, DenominatorPolicy: input.DenominatorPolicy,
		Denominator: input.Denominator, AvailabilityPercent: input.AvailabilityPercent, EvaluatedAt: evaluatedAt,
	}
}

func unavailableMetric(state presentationmodel.State, source, reasonText string) platformapi.DashboardMetric {
	reason := reasonText
	return platformapi.DashboardMetric{State: platformState(state), Source: source, Reason: &reason}
}

func platformState(state presentationmodel.State) platformapi.PresentationState {
	return platformapi.PresentationState(state)
}

func stateFromPlatform(state platformapi.PresentationState) presentationmodel.State {
	return presentationmodel.State(state)
}

func dashboardInstant(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}

func dashboardTimePointer(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := dashboardInstant(*value)
	return &formatted
}

func oldestInstant(values ...*string) *string {
	var oldest time.Time
	for _, value := range values {
		if value == nil {
			continue
		}
		parsed, err := time.Parse("2006-01-02T15:04:05.000Z", *value)
		if err != nil {
			continue
		}
		if oldest.IsZero() || parsed.Before(oldest) {
			oldest = parsed
		}
	}
	if oldest.IsZero() {
		return nil
	}
	formatted := dashboardInstant(oldest)
	return &formatted
}

func dashboardReasons(population platformapi.DevicePopulationSummary, energy platformapi.DashboardMetric, alarms platformapi.OpenAlarmSummary) []string {
	reasons := make([]string, 0, 8)
	if population.State != platformapi.PresentationStateReady {
		reasons = append(reasons, "DEVICE_POPULATION_"+string(population.State))
	}
	if population.Offline > 0 {
		reasons = append(reasons, "DEVICE_OFFLINE")
	}
	if population.Stale > 0 {
		reasons = append(reasons, "DEVICE_STALE")
	}
	if energy.State != platformapi.PresentationStateReady {
		reasons = append(reasons, "ENERGY_"+string(energy.State))
	}
	if alarms.State != platformapi.PresentationStateReady {
		reasons = append(reasons, "ALARM_"+string(alarms.State))
	} else if derefInt(alarms.ActiveCount) > 0 {
		reasons = append(reasons, "ACTIVE_ALARMS")
	}
	return reasons
}

func derefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func severityRank(value alarmmodel.Severity) int {
	switch value {
	case alarmmodel.SeverityCritical:
		return 5
	case alarmmodel.SeverityMajor:
		return 4
	case alarmmodel.SeverityMinor:
		return 3
	case alarmmodel.SeverityWarning:
		return 2
	case alarmmodel.SeverityInfo:
		return 1
	default:
		return 0
	}
}

func cloneURL(source *url.URL) *url.URL {
	if source == nil {
		return &url.URL{}
	}
	copy := *source
	return &copy
}

func decodeDashboardJSON(raw []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return err
	}
	return nil
}
