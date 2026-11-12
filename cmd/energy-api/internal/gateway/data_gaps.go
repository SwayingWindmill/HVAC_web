package gateway

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/s2telemetryapi"
	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

const (
	readingGapAlarmPrefix    = "reading-gap/"
	dataGapsMaxAlarmPages    = 10
	dataGapsAlarmPageLimit   = 200
	dataGapsMaxTelemetryKeys = 128
)

type siteTotalSubMeterSource struct {
	PointID   string `json:"pointId"`
	DeviceID  string `json:"deviceId"`
	MeterName string `json:"meterName"`
	Ordinal   int    `json:"ordinal"`
}

type siteTotalElectricitySources struct {
	SiteID      string                    `json:"siteId"`
	Synthesized bool                      `json:"synthesized"`
	SubMeters   []siteTotalSubMeterSource `json:"subMeters"`
}

type resolvedGapCandidate struct {
	alarm  alarmmodel.Alarm
	device platformapi.Device
	point  platformapi.TelemetryPoint
}

func matchPublicDataGapsRoute(path string) (string, bool) {
	remainder, ok := strings.CutPrefix(path, "/api/v1/sites/")
	if !ok || strings.HasSuffix(remainder, "/") {
		return "", false
	}
	segments := strings.Split(remainder, "/")
	if len(segments) != 2 || segments[0] == "" || segments[1] != "data-gaps" {
		return "", false
	}
	siteID, err := url.PathUnescape(segments[0])
	if err != nil {
		return "", false
	}
	return siteID, true
}

func dispatchDataGapsRoute(h *handler, writer http.ResponseWriter, request *http.Request, siteID string) {
	if request.Method != http.MethodGet {
		writeMethodNotAllowedFor(writer, request, http.MethodGet)
		return
	}
	decision := routeDecisionFromContext(request.Context())
	if decision.SelectedOwner != ownershipregistry.OwnerGateway {
		writeProblem(writer, request, http.StatusServiceUnavailable, "DATA_GAPS_UNAVAILABLE", "Data gaps unavailable", "The Site data-gaps route is not active for this Session.", true, nil)
		return
	}
	if len(request.URL.RawQuery) != 0 {
		writeProblem(writer, request, http.StatusBadRequest, "REQUEST_INVALID", "Request invalid", "The Site data-gaps query does not accept query parameters.", false, nil)
		return
	}
	if !isLowerUUIDv7(siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}

	session, ok := h.dataGapsSession(writer, request)
	if !ok {
		return
	}
	ctx := context.WithValue(request.Context(), routeSessionContextKey, session)
	request = request.WithContext(ctx)

	if h.registry == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "The Registry is not configured.", true, nil)
		return
	}
	regAuth, regFailure := h.authorizeRegistry(ctx, session, registryauth.ActionAssetModelRead)
	if regFailure != nil {
		if regFailure.code == "RESOURCE_NOT_FOUND" || regFailure.status == http.StatusForbidden {
			writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
			return
		}
		writeProblem(writer, request, regFailure.status, regFailure.code, regFailure.title, regFailure.detail, regFailure.retryable, nil)
		return
	}
	if _, allowed := regAuth.allowedSiteIDs[siteID]; !allowed {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}

	gapAlarms, ok := h.listActiveReadingGapAlarms(ctx, writer, request, session, siteID)
	if !ok {
		return
	}
	if len(gapAlarms) == 0 {
		writer.Header().Set("Cache-Control", "private, no-store")
		writeJSON(writer, http.StatusOK, platformapi.SiteDataGapList{Items: []platformapi.SiteDataGap{}})
		return
	}

	assetModel, ok := h.fetchSiteAssetModelForDataGaps(ctx, writer, request, siteID, regAuth.coreGrant, decision)
	if !ok {
		return
	}
	totalSources, ok := h.fetchSiteTotalElectricitySourcesForDataGaps(ctx, writer, request, siteID, regAuth.coreGrant, decision)
	if !ok {
		return
	}

	candidates := resolveGapCandidates(gapAlarms, assetModel)
	if len(candidates) == 0 {
		writer.Header().Set("Cache-Control", "private, no-store")
		writeJSON(writer, http.StatusOK, platformapi.SiteDataGapList{Items: []platformapi.SiteDataGap{}})
		return
	}

	sampledAtByPoint := h.lookupDataGapSnapshotTimes(ctx, request, session, candidates)
	boundAssetDevices := activeAssetBoundDevices(assetModel)
	subMeterPoints := make(map[string]struct{}, len(totalSources.SubMeters))
	if totalSources.Synthesized {
		for _, sub := range totalSources.SubMeters {
			subMeterPoints[sub.DeviceID+"/"+sub.PointID] = struct{}{}
		}
	}

	items := make([]platformapi.SiteDataGap, 0, len(candidates))
	for _, candidate := range candidates {
		since := candidate.alarm.FirstOccurredAt
		if sampledAt, exists := sampledAtByPoint[candidate.device.ID+"/"+candidate.point.PointCode]; exists {
			if shouldUseSnapshotSampledAt(sampledAt, since) {
				since = sampledAt
			}
		}
		_, isSynthesizedSubMeter := subMeterPoints[candidate.device.ID+"/"+candidate.point.ID]
		_, isAssetBound := boundAssetDevices[candidate.device.ID]
		affected, excludesFromTotal := classifyDataGapImpact(candidate.device, candidate.point, totalSources.Synthesized, isSynthesizedSubMeter, isAssetBound)
		items = append(items, platformapi.SiteDataGap{
			AlarmID:                 candidate.alarm.AlarmID,
			DeviceID:                candidate.device.ID,
			DeviceName:              cleanDataGapDisplayName(candidate.device.DisplayName),
			PointID:                 candidate.point.ID,
			PointCode:               candidate.point.PointCode,
			PointName:               cleanDataGapDisplayName(candidate.point.DisplayName),
			Since:                   since,
			AffectedFigures:         affected,
			ExcludesDeviceFromTotal: excludesFromTotal,
		})
	}

	slices.SortFunc(items, func(left, right platformapi.SiteDataGap) int {
		if left.ExcludesDeviceFromTotal != right.ExcludesDeviceFromTotal {
			if left.ExcludesDeviceFromTotal {
				return -1
			}
			return 1
		}
		if c := cmp.Compare(left.Since, right.Since); c != 0 {
			return c
		}
		if c := cmp.Compare(left.DeviceName, right.DeviceName); c != 0 {
			return c
		}
		if c := cmp.Compare(left.PointCode, right.PointCode); c != 0 {
			return c
		}
		return cmp.Compare(left.AlarmID, right.AlarmID)
	})

	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, platformapi.SiteDataGapList{Items: items})
}

func (h *handler) dataGapsSession(writer http.ResponseWriter, request *http.Request) (bffSession, bool) {
	if session, ok := routeSessionFromContext(request.Context()); ok {
		return session, true
	}
	if h.identity == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "SESSION_UNAVAILABLE", "Session unavailable", "Session validation is unavailable.", true, nil)
		return bffSession{}, false
	}
	session, failure := h.identitySession(request)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return bffSession{}, false
	}
	return session, true
}

func (h *handler) listActiveReadingGapAlarms(
	ctx context.Context,
	writer http.ResponseWriter,
	request *http.Request,
	session bffSession,
	siteID string,
) ([]alarmmodel.Alarm, bool) {
	if h.alarm == nil || h.alarm.operations == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "ALARM_UNAVAILABLE", "Alarm unavailable", "The Alarm service is not configured.", true, nil)
		return nil, false
	}
	route := publicAlarmRoute{
		template: "/api/v1/alarms",
		siteID:   siteID,
		action:   alarmauth.ActionRead,
	}
	var gaps []alarmmodel.Alarm
	cursor := ""
	for page := 0; page < dataGapsMaxAlarmPages; page++ {
		query := url.Values{
			"siteId":    {siteID},
			"condition": {string(alarmmodel.ConditionActive)},
			"limit":     {strconv.Itoa(dataGapsAlarmPageLimit)},
		}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		syntheticURL := &url.URL{Path: "/api/v1/alarms", RawQuery: query.Encode()}
		syntheticReq := (&http.Request{
			Method: http.MethodGet,
			URL:    syntheticURL,
			Header: make(http.Header),
		}).WithContext(ctx)

		response, failure := CallOwner(
			ctx,
			h,
			h.alarmCaller(session),
			h.alarmOwner(),
			string(alarmauth.ActionRead),
			route.scope(session.TenantID),
			alarmCall{public: syntheticReq, route: route, limit: dataGapsAlarmPageLimit},
		)
		if failure != nil {
			if failure.status == http.StatusForbidden {
				writeProblem(writer, request, http.StatusForbidden, "FORBIDDEN", "Forbidden", "The authenticated Principal is not authorized to read Alarms for this Site.", false, nil)
				return nil, false
			}
			if failure.status == http.StatusNotFound {
				writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
				return nil, false
			}
			writeProblem(writer, request, http.StatusServiceUnavailable, "ALARM_UNAVAILABLE", "Alarm unavailable", "Alarm Service is temporarily unavailable.", true, nil)
			return nil, false
		}
		if response.status == http.StatusForbidden {
			writeProblem(writer, request, http.StatusForbidden, "FORBIDDEN", "Forbidden", "The authenticated Principal is not authorized to read Alarms for this Site.", false, nil)
			return nil, false
		}
		if response.status != http.StatusOK {
			writeProblem(writer, request, http.StatusServiceUnavailable, "ALARM_UNAVAILABLE", "Alarm unavailable", "Alarm Service could not list active Alarms.", true, nil)
			return nil, false
		}
		list, ok := response.value.(alarmmodel.ListResponse)
		if !ok {
			writeProblem(writer, request, http.StatusServiceUnavailable, "ALARM_UNAVAILABLE", "Alarm unavailable", "Alarm Service returned an invalid Alarm list.", true, nil)
			return nil, false
		}
		for _, item := range list.Items {
			if item.Condition == alarmmodel.ConditionActive && strings.HasPrefix(item.AlarmType, readingGapAlarmPrefix) {
				gaps = append(gaps, item)
			}
		}
		if !list.HasMore || list.NextCursor == nil || strings.TrimSpace(*list.NextCursor) == "" {
			break
		}
		cursor = *list.NextCursor
	}
	return gaps, true
}

func (h *handler) fetchSiteAssetModelForDataGaps(
	ctx context.Context,
	writer http.ResponseWriter,
	request *http.Request,
	siteID string,
	grant string,
	outer ownershipregistry.Decision,
) (platformapi.SiteAssetModel, bool) {
	route := publicRegistryRoute{
		template:     platformapi.GetSiteAssetModelPathTemplate,
		internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/asset-model",
		resource:     "asset-model",
		action:       registryauth.ActionAssetModelRead,
		scopeID:      siteID,
	}
	coreDecision := ownershipregistry.Decision{
		RouteKey:          http.MethodGet + " " + route.template,
		PathTemplate:      route.template,
		SelectedOwner:     ownershipregistry.OwnerCore,
		RegistryRevision:  outer.RegistryRevision,
		RouteRevision:     1,
		CompatibilityMode: "native",
	}
	result := h.executeCoreRegistry(ctx, route, "", grant, coreDecision)
	if result.status != http.StatusOK {
		h.writeRegistryBackendResult(writer, request, result)
		return platformapi.SiteAssetModel{}, false
	}
	var model platformapi.SiteAssetModel
	if err := json.Unmarshal(result.body, &model); err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "Core Registry returned an invalid Site asset model.", true, nil)
		return platformapi.SiteAssetModel{}, false
	}
	return model, true
}

func (h *handler) fetchSiteTotalElectricitySourcesForDataGaps(
	ctx context.Context,
	writer http.ResponseWriter,
	request *http.Request,
	siteID string,
	grant string,
	outer ownershipregistry.Decision,
) (siteTotalElectricitySources, bool) {
	route := publicRegistryRoute{
		template:     "/internal/v1/registry/sites/{siteId}/total-electricity-sources",
		internalPath: "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/total-electricity-sources",
		resource:     "total-electricity-sources",
		action:       registryauth.ActionAssetModelRead,
		scopeID:      siteID,
	}
	coreDecision := ownershipregistry.Decision{
		RouteKey:          http.MethodGet + " " + route.template,
		PathTemplate:      route.template,
		SelectedOwner:     ownershipregistry.OwnerCore,
		RegistryRevision:  outer.RegistryRevision,
		RouteRevision:     1,
		CompatibilityMode: "native",
	}
	result := h.executeCoreRegistry(ctx, route, "", grant, coreDecision)
	if result.status != http.StatusOK {
		h.writeRegistryBackendResult(writer, request, result)
		return siteTotalElectricitySources{}, false
	}
	var sources siteTotalElectricitySources
	if err := json.Unmarshal(result.body, &sources); err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "Registry unavailable", "Core Registry returned invalid total electricity sources.", true, nil)
		return siteTotalElectricitySources{}, false
	}
	return sources, true
}

func resolveGapCandidates(alarms []alarmmodel.Alarm, model platformapi.SiteAssetModel) []resolvedGapCandidate {
	devicesByID := make(map[string]platformapi.Device, len(model.Devices))
	devicesByCode := make(map[string]platformapi.Device, len(model.Devices))
	for _, device := range model.Devices {
		if device.Status != "ACTIVE" {
			continue
		}
		devicesByID[device.ID] = device
		devicesByCode[device.Code] = device
	}
	pointsByID := make(map[string]platformapi.TelemetryPoint, len(model.TelemetryPoints))
	pointsByDeviceAndCode := make(map[string]platformapi.TelemetryPoint, len(model.TelemetryPoints))
	for _, point := range model.TelemetryPoints {
		if point.Status != "ACTIVE" {
			continue
		}
		pointsByID[point.ID] = point
		pointsByDeviceAndCode[point.ReportingDeviceID+"/"+point.PointCode] = point
	}

	seen := make(map[string]struct{}, len(alarms))
	candidates := make([]resolvedGapCandidate, 0, len(alarms))
	for _, alarm := range alarms {
		suffix := strings.TrimPrefix(alarm.AlarmType, readingGapAlarmPrefix)
		deviceCode, pointCode, hasSlash := strings.Cut(suffix, "/")
		if !hasSlash || deviceCode == "" || pointCode == "" || strings.Contains(pointCode, "/") {
			continue
		}
		var (
			device   platformapi.Device
			deviceOK bool
		)
		if alarm.DeviceID != nil {
			device, deviceOK = devicesByID[*alarm.DeviceID]
		}
		if !deviceOK {
			device, deviceOK = devicesByCode[deviceCode]
		}
		if !deviceOK {
			continue
		}
		var (
			point   platformapi.TelemetryPoint
			pointOK bool
		)
		if alarm.PointID != nil {
			point, pointOK = pointsByID[*alarm.PointID]
			if pointOK && point.ReportingDeviceID != device.ID {
				pointOK = false
			}
		}
		if !pointOK {
			point, pointOK = pointsByDeviceAndCode[device.ID+"/"+pointCode]
		}
		if !pointOK {
			continue
		}
		dedupeKey := device.ID + "/" + point.ID
		if _, duplicate := seen[dedupeKey]; duplicate {
			continue
		}
		seen[dedupeKey] = struct{}{}
		candidates = append(candidates, resolvedGapCandidate{
			alarm:  alarm,
			device: device,
			point:  point,
		})
	}
	return candidates
}

func (h *handler) lookupDataGapSnapshotTimes(
	ctx context.Context,
	request *http.Request,
	session bffSession,
	candidates []resolvedGapCandidate,
) map[string]string {
	result := make(map[string]string)
	if h.telemetry == nil || len(candidates) == 0 {
		return result
	}

	keysByDevice := make(map[string][]string)
	seenByDevice := make(map[string]map[string]struct{})
	totalKeys := 0
	for _, candidate := range candidates {
		if totalKeys >= dataGapsMaxTelemetryKeys {
			break
		}
		devID := candidate.device.ID
		key := candidate.point.PointCode
		if seenByDevice[devID] == nil {
			seenByDevice[devID] = make(map[string]struct{})
		}
		if _, exists := seenByDevice[devID][key]; exists {
			continue
		}
		seenByDevice[devID][key] = struct{}{}
		keysByDevice[devID] = append(keysByDevice[devID], key)
		totalKeys++
	}
	if len(keysByDevice) == 0 {
		return result
	}

	deviceIDs := make([]string, 0, len(keysByDevice))
	for devID := range keysByDevice {
		deviceIDs = append(deviceIDs, devID)
	}
	slices.Sort(deviceIDs)

	targets := make([]telemetryauth.Target, 0, len(deviceIDs))
	batchReq := s2telemetryapi.BatchGetObservationSnapshotsRequest{
		Requests: make([]s2telemetryapi.ObservationSnapshotTarget, 0, len(deviceIDs)),
	}
	for index, devID := range deviceIDs {
		keys := keysByDevice[devID]
		slices.Sort(keys)
		targets = append(targets, telemetryauth.Target{DeviceID: devID, Keys: keys})
		apiKeys := make([]s2telemetryapi.TelemetryKey, len(keys))
		for keyIndex, key := range keys {
			apiKeys[keyIndex] = s2telemetryapi.TelemetryKey(key)
		}
		batchReq.Requests = append(batchReq.Requests, s2telemetryapi.ObservationSnapshotTarget{
			RequestId: "gap-" + strconv.Itoa(index+1),
			DeviceId:  s2telemetryapi.UUIDv7(devID),
			Keys:      apiKeys,
		})
	}

	caller := telemetryCaller{
		principal: session.Principal,
		tenantID:  session.TenantID,
		contextID: "session:" + session.ID,
		expiresAt: session.ExpiresAt,
	}
	authorization, failure := h.authorizeTelemetry(ctx, request, caller, telemetryauth.ActionBatchRead, targets)
	if failure != nil {
		return result
	}
	body, err := json.Marshal(batchReq)
	if err != nil {
		return result
	}
	raw, failure := h.executeTelemetryRuntime(ctx, request, http.MethodPost, internalTelemetryBatchPath, nil, body, authorization.grant)
	if failure != nil {
		return result
	}
	var batchResp s2telemetryapi.BatchGetObservationSnapshotsResponse
	if decodeStrictTelemetryJSON(raw, &batchResp) != nil {
		return result
	}
	for _, item := range batchResp.Items {
		if item.Success == nil {
			continue
		}
		devID := string(item.Success.DeviceId)
		for _, value := range item.Success.Snapshot.Values {
			if value.Present == nil {
				continue
			}
			sampledAt := strings.TrimSpace(string(value.Present.SampledAt))
			if sampledAt != "" {
				result[devID+"/"+string(value.Present.Key)] = sampledAt
			}
		}
	}
	return result
}

func shouldUseSnapshotSampledAt(sampledAt, firstOccurredAt string) bool {
	sampledTime, err := time.Parse(time.RFC3339Nano, sampledAt)
	if err != nil {
		return false
	}
	firstTime, err := time.Parse(time.RFC3339Nano, firstOccurredAt)
	if err != nil {
		return true
	}
	return !sampledTime.After(firstTime)
}

func activeAssetBoundDevices(model platformapi.SiteAssetModel) map[string]struct{} {
	bound := make(map[string]struct{})
	for _, rel := range model.Relationships {
		if rel.Status == "ACTIVE" && rel.ValidTo == nil && rel.FromType == "DEVICE" && rel.ToType == "ASSET" {
			bound[rel.FromID] = struct{}{}
		}
	}
	return bound
}

func cleanDataGapDisplayName(name string) string {
	cleaned := strings.TrimSpace(name)
	if before, _, ok := strings.Cut(cleaned, "（"); ok && strings.TrimSpace(before) != "" {
		cleaned = strings.TrimSpace(before)
	}
	if before, _, ok := strings.Cut(cleaned, " ("); ok && strings.TrimSpace(before) != "" {
		cleaned = strings.TrimSpace(before)
	}
	if trimmed := strings.TrimSpace(strings.TrimSuffix(cleaned, "通信端点")); trimmed != "" {
		cleaned = trimmed
	}
	return cleaned
}

func classifyDataGapImpact(
	device platformapi.Device,
	point platformapi.TelemetryPoint,
	synthesizedTotal bool,
	isSynthesizedSubMeter bool,
	isAssetBound bool,
) ([]string, bool) {
	if isSynthesizedSubMeter {
		return []string{"空调用电", "综合能效", "估算电费"}, true
	}

	deviceType := strings.ToUpper(device.DeviceType)
	pointCode := strings.ToLower(point.PointCode)
	unit := ""
	if point.Unit != nil {
		unit = strings.TrimSpace(*point.Unit)
	}
	isPowerMeter := deviceType == "POWER_METER" || deviceType == "HVAC_POWER_METER"

	if point.PointType == "COUNTER" && isPowerMeter {
		if synthesizedTotal && !isAssetBound {
			return []string{"总电表与设备电表核对"}, false
		}
		if !isAssetBound {
			return []string{"空调用电", "综合能效", "估算电费"}, false
		}
		return []string{"设备分项用电"}, false
	}

	if point.PointType == "COUNTER" && (deviceType == "BTU_METER" || strings.Contains(pointCode, "cooling")) {
		return []string{"供冷量", "综合能效"}, false
	}

	if strings.Contains(pointCode, "cooling_capacity") || (deviceType == "BTU_METER" && unit == "kW") {
		return []string{"实时冷量", "综合能效"}, false
	}

	if unit == "kW" || pointCode == "power_total" || pointCode == "active_power" {
		if !isAssetBound {
			return []string{"实时功率", "用电负荷"}, false
		}
		return []string{"设备实时功率", "用电负荷"}, false
	}

	if deviceType == "FLOW_METER" || strings.Contains(pointCode, "flow") {
		return []string{"供冷量", "综合能效", "实时冷量"}, false
	}

	if unit == "Cel" && !isAssetBound {
		return []string{"供冷量", "综合能效", "实时冷量"}, false
	}

	return []string{"实时运行数据"}, false
}
