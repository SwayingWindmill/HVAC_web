package alarmservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	InternalControlEventsPath = "/internal/v1/alarms/control-events"

	ControlEventOutcomeUnknown            = "OUTCOME_UNKNOWN"
	ControlEventPulsePowerUnconfirmed     = "PULSE_DELIVERED_POWER_UNCONFIRMED"
	ControlEventEmergencyDORelease        = "EMERGENCY_DO_RELEASE"
	ControlEventSetpointTrackingDeviation = "SETPOINT_TRACKING_DEVIATION"
	ControlEventAutoRollbackFailed        = "AUTO_CONTROL_ROLLBACK_FAILED"
	controlFailureRuleRevision            = "control-safety-v1"
	maximumControlEventRequestBodySize    = int64(256 << 10)
)

var ErrControlEventInvalid = errors.New("control failure event payload is invalid")

type ControlEventRequest struct {
	SchemaVersion int                 `json:"schemaVersion"`
	TenantID      string              `json:"tenantId"`
	SiteID        string              `json:"siteId"`
	DeviceID      *string             `json:"deviceId,omitempty"`
	PointID       *string             `json:"pointId,omitempty"`
	CommandID     string              `json:"commandId"`
	EventKind     string              `json:"eventKind"`
	Capability    string              `json:"capability,omitempty"`
	Title         string              `json:"title,omitempty"`
	Summary       string              `json:"summary,omitempty"`
	Severity      alarmmodel.Severity `json:"severity,omitempty"`
	ReasonCode    string              `json:"reasonCode,omitempty"`
	Detail        string              `json:"detail,omitempty"`
	OccurredAt    string              `json:"occurredAt,omitempty"`
	ActorType     string              `json:"actorType,omitempty"`
	ActorID       string              `json:"actorId,omitempty"`
	CorrelationID string              `json:"correlationId,omitempty"`
}

// PublishControlEvent records an operational control-safety alarm incident in alarm_runtime
// using a deterministic sourceReference so repeated reports update the active incident idempotently.
func PublishControlEvent(ctx context.Context, store Store, input ControlEventRequest, now time.Time) (alarmmodel.Alarm, error) {
	if store == nil {
		return alarmmodel.Alarm{}, ErrUnavailable
	}
	tenantID := strings.TrimSpace(input.TenantID)
	siteID := strings.TrimSpace(input.SiteID)
	commandID := strings.TrimSpace(input.CommandID)
	eventKind := strings.ToUpper(strings.TrimSpace(input.EventKind))
	if !alarmmodel.IsUUIDv7(tenantID) || !alarmmodel.IsUUIDv7(siteID) || commandID == "" || len(commandID) > 128 || eventKind == "" || len(eventKind) > 64 {
		return alarmmodel.Alarm{}, ErrControlEventInvalid
	}
	var deviceID *string
	if input.DeviceID != nil && strings.TrimSpace(*input.DeviceID) != "" {
		trimmed := strings.TrimSpace(*input.DeviceID)
		if !alarmmodel.IsUUIDv7(trimmed) {
			return alarmmodel.Alarm{}, ErrControlEventInvalid
		}
		deviceID = &trimmed
	}
	var pointID *string
	if input.PointID != nil && strings.TrimSpace(*input.PointID) != "" {
		trimmed := strings.TrimSpace(*input.PointID)
		if !alarmmodel.IsUUIDv7(trimmed) {
			return alarmmodel.Alarm{}, ErrControlEventInvalid
		}
		pointID = &trimmed
	}

	alarmType, defaultSeverity, defaultTitle, defaultSummary := describeControlEvent(eventKind, commandID, strings.TrimSpace(input.Capability), strings.TrimSpace(input.ReasonCode), strings.TrimSpace(input.Detail), strings.TrimSpace(input.ActorID))
	severity, ok := normalizeControlEventSeverity(input.Severity, defaultSeverity)
	if !ok {
		return alarmmodel.Alarm{}, ErrControlEventInvalid
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = defaultTitle
	}
	summary := strings.TrimSpace(input.Summary)
	if summary == "" {
		summary = defaultSummary
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	occurredAt := strings.TrimSpace(input.OccurredAt)
	if occurredAt == "" {
		occurredAt = now.UTC().Format(time.RFC3339Nano)
	}
	if _, err := time.Parse(time.RFC3339Nano, occurredAt); err != nil {
		return alarmmodel.Alarm{}, ErrControlEventInvalid
	}
	actorType := strings.TrimSpace(input.ActorType)
	if actorType == "" {
		actorType = "SYSTEM"
	}
	actorID := strings.TrimSpace(input.ActorID)
	if actorID == "" {
		actorID = "command-safety"
	}
	correlationID := strings.TrimSpace(input.CorrelationID)
	if correlationID == "" {
		correlationID = commandID
	}

	publication := Publication{
		DeviceID:        deviceID,
		PointID:         pointID,
		AlarmType:       alarmType,
		SourceType:      alarmmodel.SourceExternal,
		SourceReference: fmt.Sprintf("control-failure:%s:%s", eventKind, commandID),
		RuleRevision:    controlFailureRuleRevision,
		Title:           truncateBounded(title, 256),
		Summary:         truncateBounded(summary, 2048),
		Severity:        severity,
		OccurredAt:      occurredAt,
		Evidence: []alarmmodel.EvidenceReference{
			{Kind: "command", Reference: commandID, CapturedAt: occurredAt},
		},
		ActorType:     truncateBounded(actorType, 64),
		ActorID:       truncateBounded(actorID, 256),
		CorrelationID: truncateBounded(correlationID, 256),
	}
	alarm, err := store.Publish(identitycontext.WithTenantID(ctx, tenantID), tenantID, siteID, publication)
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	return alarm, nil
}

// NewControlFailureHandler accepts authenticated internal control-failure events and publishes
// them through Store.Publish so alarm_runtime and notification_outbox deliver alerts promptly.
func NewControlFailureHandler(store Store) (http.Handler, error) {
	if store == nil {
		return nil, errors.New("control failure handler requires an alarm store")
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != InternalControlEventsPath {
			writeControlEventProblem(writer, http.StatusNotFound, "ALARM_CONTROL_ROUTE_NOT_FOUND", false)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, maximumControlEventRequestBodySize)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var input ControlEventRequest
		if err := decoder.Decode(&input); err != nil {
			writeControlEventProblem(writer, http.StatusBadRequest, "ALARM_CONTROL_EVENT_INVALID", false)
			return
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			writeControlEventProblem(writer, http.StatusBadRequest, "ALARM_CONTROL_EVENT_INVALID", false)
			return
		}
		if input.SchemaVersion != 1 {
			writeControlEventProblem(writer, http.StatusBadRequest, "ALARM_CONTROL_SCHEMA_UNSUPPORTED", false)
			return
		}
		alarm, err := PublishControlEvent(request.Context(), store, input, time.Now().UTC())
		if err != nil {
			if errors.Is(err, ErrControlEventInvalid) {
				writeControlEventProblem(writer, http.StatusBadRequest, "ALARM_CONTROL_EVENT_INVALID", false)
				return
			}
			writeControlEventProblem(writer, http.StatusServiceUnavailable, "ALARM_CONTROL_PUBLISH_UNAVAILABLE", true)
			return
		}
		status := http.StatusCreated
		if alarm.OccurrenceCount > 1 {
			status = http.StatusOK
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_ = json.NewEncoder(writer).Encode(alarm)
	}), nil
}

func describeControlEvent(kind, commandID, capability, reasonCode, detail, actorID string) (string, alarmmodel.Severity, string, string) {
	contextParts := make([]string, 0, 4)
	contextParts = append(contextParts, "指令 "+commandID)
	if capability != "" {
		contextParts = append(contextParts, "动作 "+capability)
	}
	if reasonCode != "" {
		contextParts = append(contextParts, "原因 "+reasonCode)
	}
	if detail != "" {
		contextParts = append(contextParts, detail)
	}
	diagnostic := strings.Join(contextParts, "；")

	switch kind {
	case ControlEventOutcomeUnknown:
		return "COMMAND_OUTCOME_UNKNOWN", alarmmodel.SeverityCritical, "控制指令执行结果未知", fmt.Sprintf("控制指令进入 OUTCOME_UNKNOWN 状态，需人工核查现场设备实际状态并核销（%s）。", diagnostic)
	case ControlEventPulsePowerUnconfirmed:
		return "COMMAND_POWER_UNCONFIRMED", alarmmodel.SeverityMajor, "脉冲控制后功率变化未确认", fmt.Sprintf("脉冲指令已发出但电表功率变化未获确认，请核查接触器动作及负载电流（%s）。", diagnostic)
	case ControlEventEmergencyDORelease:
		actorNote := ""
		if actorID != "" {
			actorNote = "，操作人 " + actorID
		}
		return "EMERGENCY_DO_RELEASE", alarmmodel.SeverityCritical, "已触发全站紧急断电释放", fmt.Sprintf("已下发 EMERGENCY_DO_RELEASE 全站输出清零指令%s，请立即确认现场冷站与配电安全状态（%s）。", actorNote, diagnostic)
	case ControlEventSetpointTrackingDeviation:
		return "SETPOINT_TRACKING_DEVIATION", alarmmodel.SeverityMajor, "设定值下发后未收敛", fmt.Sprintf("自动控制指令下发后在收敛窗口内未达到目标设定值，可能受本地限幅、只读锁或执行机构响应异常影响，系统已触发回退检查（%s）。", diagnostic)
	case ControlEventAutoRollbackFailed:
		return "AUTO_CONTROL_ROLLBACK_FAILED", alarmmodel.SeverityCritical, "自动控制异常回退失败", fmt.Sprintf("自动控制进入异常触发条件但回退恢复指令未能确认成功，请值班人员立即核查冷站设定值与现场控制状态（%s）。", diagnostic)
	default:
		return "CONTROL_FAILURE_" + kind, alarmmodel.SeverityMajor, "控制链路异常告警", fmt.Sprintf("控制安全模块报告异常事件 %s（%s）。", kind, diagnostic)
	}
}

func normalizeControlEventSeverity(requested alarmmodel.Severity, fallback alarmmodel.Severity) (alarmmodel.Severity, bool) {
	trimmed := alarmmodel.Severity(strings.ToUpper(strings.TrimSpace(string(requested))))
	if trimmed == "" {
		return fallback, true
	}
	if trimmed == "HIGH" {
		return alarmmodel.SeverityMajor, true
	}
	switch trimmed {
	case alarmmodel.SeverityInfo, alarmmodel.SeverityWarning, alarmmodel.SeverityMinor, alarmmodel.SeverityMajor, alarmmodel.SeverityCritical:
		return trimmed, true
	default:
		return "", false
	}
}

func truncateBounded(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	if len([]rune(trimmed)) <= limit {
		return trimmed
	}
	runes := []rune(trimmed)
	return string(runes[:limit])
}

func writeControlEventProblem(writer http.ResponseWriter, status int, code string, retryable bool) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"code": code, "retryable": retryable})
}
