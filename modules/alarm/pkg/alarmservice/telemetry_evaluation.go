package alarmservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

const InternalTelemetryEvaluationPath = "/internal/v1/alarm/evaluations/telemetry"

var ErrTelemetryEvaluationInvalid = errors.New("telemetry evaluation input is invalid")

type TelemetryEvaluationRequest struct {
	SchemaVersion int                                    `json:"schemaVersion"`
	EventID       string                                 `json:"eventId"`
	Snapshot      telemetryapi.DeviceObservationSnapshot `json:"snapshot"`
}

type TelemetrySnapshotEvaluator interface {
	EvaluateTelemetrySnapshot(context.Context, string, telemetryapi.DeviceObservationSnapshot, time.Time) ([]EvaluationDecision, error)
}

type telemetryEvaluationInputRecord struct {
	DeviceID         string
	BusinessRevision int64
	EventID          string
	EvaluatedAt      time.Time
	Snapshot         telemetryapi.DeviceObservationSnapshot
	// UnchangedSince is, per reading key, when the reading took the value it still has (#616).
	UnchangedSince map[string]string
}

func NewTelemetryEvaluationHandler(evaluator TelemetrySnapshotEvaluator) (http.Handler, error) {
	if evaluator == nil {
		return nil, errors.New("telemetry evaluation handler requires an evaluator")
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != InternalTelemetryEvaluationPath {
			writeTelemetryEvaluationProblem(writer, http.StatusNotFound, "ALARM_TELEMETRY_ROUTE_NOT_FOUND", false)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 2<<20)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var input TelemetryEvaluationRequest
		if err := decoder.Decode(&input); err != nil {
			writeTelemetryEvaluationProblem(writer, http.StatusBadRequest, "ALARM_TELEMETRY_PAYLOAD_INVALID", false)
			return
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			writeTelemetryEvaluationProblem(writer, http.StatusBadRequest, "ALARM_TELEMETRY_PAYLOAD_INVALID", false)
			return
		}
		if input.SchemaVersion != 1 {
			writeTelemetryEvaluationProblem(writer, http.StatusBadRequest, "ALARM_TELEMETRY_SCHEMA_UNSUPPORTED", false)
			return
		}
		tenantID := string(input.Snapshot.TenantId)
		ctx := identitycontext.WithTenantID(request.Context(), tenantID)
		if _, err := evaluator.EvaluateTelemetrySnapshot(ctx, input.EventID, input.Snapshot, time.Now().UTC()); err != nil {
			if errors.Is(err, ErrTelemetryEvaluationInvalid) {
				writeTelemetryEvaluationProblem(writer, http.StatusBadRequest, "ALARM_TELEMETRY_PAYLOAD_INVALID", false)
				return
			}
			writeTelemetryEvaluationProblem(writer, http.StatusServiceUnavailable, "ALARM_TELEMETRY_EVALUATION_UNAVAILABLE", true)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}), nil
}

func (store *PostgresStore) EvaluateTelemetrySnapshot(ctx context.Context, eventID string, snapshot telemetryapi.DeviceObservationSnapshot, now time.Time) ([]EvaluationDecision, error) {
	if store == nil || store.pool == nil {
		return nil, ErrUnavailable
	}
	if err := validateTelemetryEvaluationInput(eventID, snapshot); err != nil {
		return nil, err
	}
	if err := store.recordTelemetryEvaluationInput(ctx, eventID, snapshot, now); err != nil {
		return nil, err
	}
	records, rules, upstreamActive, err := store.loadSiteTelemetryEvaluationState(ctx, string(snapshot.TenantId), string(snapshot.SiteId))
	if err != nil {
		return nil, err
	}
	ordered := orderSiteRulesForEvaluation(rules)
	// Evaluate rules that read this device plus transitive downstream dependents in
	// topological order so gated rules react immediately within the same telemetry pass.
	relevant := selectRelevantSiteRules(ordered, string(snapshot.DeviceId))
	if len(relevant) == 0 {
		return []EvaluationDecision{}, nil
	}
	evaluationSnapshot, err := buildSiteEvaluationSnapshot(eventID, string(snapshot.TenantId), string(snapshot.SiteId), records)
	if err != nil {
		return nil, err
	}
	evaluationSnapshot.UpstreamRuleActive = upstreamActive
	decisions := make([]EvaluationDecision, 0, len(relevant))
	for _, rule := range relevant {
		decision, err := store.EvaluateAssignedSnapshot(ctx, rule.AssignmentID, evaluationSnapshot, now.UTC())
		if errors.Is(err, ErrRuleRetired) {
			delete(upstreamActive, rule.AssignmentID)
			continue
		}
		if err != nil {
			return decisions, err
		}
		upstreamActive[rule.AssignmentID] = decision.State.ActiveAlarmID != ""
		decisions = append(decisions, decision)
	}
	return decisions, nil
}

func validateTelemetryEvaluationInput(eventID string, snapshot telemetryapi.DeviceObservationSnapshot) error {
	if !alarmmodel.IsUUIDv7(eventID) || snapshot.SchemaVersion != 1 || !alarmmodel.IsUUIDv7(string(snapshot.DeviceId)) || !alarmmodel.IsUUIDv7(string(snapshot.TenantId)) || !alarmmodel.IsUUIDv7(string(snapshot.SiteId)) || snapshot.BusinessRevision < 1 {
		return ErrTelemetryEvaluationInvalid
	}
	if _, err := time.Parse(time.RFC3339Nano, string(snapshot.EvaluatedAt)); err != nil {
		return ErrTelemetryEvaluationInvalid
	}
	return nil
}

func (store *PostgresStore) recordTelemetryEvaluationInput(ctx context.Context, eventID string, snapshot telemetryapi.DeviceObservationSnapshot, now time.Time) error {
	tenantID := string(snapshot.TenantId)
	siteID := string(snapshot.SiteId)
	deviceID := string(snapshot.DeviceId)
	evaluatedAt, _ := time.Parse(time.RFC3339Nano, string(snapshot.EvaluatedAt))
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode Alarm telemetry evaluation input: %w", err)
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentRevision int64
	var currentEventID string
	var currentSnapshot, currentSince []byte
	err = tx.QueryRow(ctx, `
SELECT business_revision, event_id::text, snapshot, unchanged_since
FROM alarm_runtime.telemetry_evaluation_input
WHERE tenant_id=$1 AND site_id=$2 AND device_id=$3
FOR UPDATE`, tenantID, siteID, deviceID).Scan(&currentRevision, &currentEventID, &currentSnapshot, &currentSince)
	if errors.Is(err, pgx.ErrNoRows) {
		held, heldErr := unchangedSince(nil, nil, snapshot)
		if heldErr != nil {
			return heldErr
		}
		since, encodeErr := json.Marshal(held)
		if encodeErr != nil {
			return fmt.Errorf("encode Alarm telemetry unchanged-since: %w", encodeErr)
		}
		_, err = tx.Exec(ctx, `
INSERT INTO alarm_runtime.telemetry_evaluation_input (
  tenant_id, site_id, device_id, business_revision, event_id, evaluated_at, snapshot, unchanged_since, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9)`, tenantID, siteID, deviceID, int64(snapshot.BusinessRevision), eventID, evaluatedAt.UTC(), encoded, since, now.UTC())
		if err != nil {
			return fmt.Errorf("insert Alarm telemetry evaluation input: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("read current Alarm telemetry evaluation input: %w", err)
	} else if int64(snapshot.BusinessRevision) > currentRevision {
		var previous telemetryapi.DeviceObservationSnapshot
		var previousSince map[string]string
		if err := json.Unmarshal(currentSnapshot, &previous); err != nil {
			return fmt.Errorf("decode current Alarm telemetry evaluation input: %w", err)
		}
		if err := json.Unmarshal(currentSince, &previousSince); err != nil {
			return fmt.Errorf("decode current Alarm telemetry unchanged-since: %w", err)
		}
		held, err := unchangedSince(&previous, previousSince, snapshot)
		if err != nil {
			return err
		}
		since, err := json.Marshal(held)
		if err != nil {
			return fmt.Errorf("encode Alarm telemetry unchanged-since: %w", err)
		}
		_, err = tx.Exec(ctx, `
UPDATE alarm_runtime.telemetry_evaluation_input
SET business_revision=$4, event_id=$5, evaluated_at=$6, snapshot=$7::jsonb, unchanged_since=$8::jsonb, updated_at=$9
WHERE tenant_id=$1 AND site_id=$2 AND device_id=$3`, tenantID, siteID, deviceID, int64(snapshot.BusinessRevision), eventID, evaluatedAt.UTC(), encoded, since, now.UTC())
		if err != nil {
			return fmt.Errorf("advance Alarm telemetry evaluation input: %w", err)
		}
	} else if int64(snapshot.BusinessRevision) == currentRevision && currentEventID != eventID {
		return fmt.Errorf("%w: telemetry business revision has conflicting event identity", ErrTelemetryEvaluationInvalid)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Alarm telemetry evaluation input: %w", err)
	}
	return nil
}

// orderSiteRulesForEvaluation sorts rules in DAG topological order when the site rule graph is
// valid, falling back deterministically by AssignmentID if legacy DB rules contain an unresolved
// dependency so runtime evaluation never fails silent.
func orderSiteRulesForEvaluation(rules []SiteRule) []SiteRule {
	if len(rules) <= 1 {
		ordered := make([]SiteRule, len(rules))
		copy(ordered, rules)
		return ordered
	}
	report, err := ValidateSiteRuleGraph(rules)
	if err == nil && len(report.Order) == len(rules) {
		byID := make(map[string]SiteRule, len(rules))
		for _, rule := range rules {
			byID[rule.AssignmentID] = rule
		}
		ordered := make([]SiteRule, 0, len(report.Order))
		for _, assignmentID := range report.Order {
			if rule, ok := byID[assignmentID]; ok {
				ordered = append(ordered, rule)
			}
		}
		if len(ordered) == len(rules) {
			return ordered
		}
	}
	ordered := make([]SiteRule, len(rules))
	copy(ordered, rules)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].AssignmentID < ordered[right].AssignmentID
	})
	return ordered
}

// selectRelevantSiteRules collects rules reading deviceID plus transitive downstream rules
// declaring a dependency on any relevant rule, preserving the caller's ordering.
func selectRelevantSiteRules(ordered []SiteRule, deviceID string) []SiteRule {
	relevantIDs := make(map[string]bool, len(ordered))
	for _, rule := range ordered {
		if readsDevice(rule.Policy, deviceID) {
			relevantIDs[rule.AssignmentID] = true
		}
	}
	if len(relevantIDs) == 0 {
		return nil
	}
	for {
		added := false
		for _, rule := range ordered {
			if relevantIDs[rule.AssignmentID] {
				continue
			}
			for _, depID := range rule.Policy.DependsOnRuleIDs {
				if relevantIDs[depID] {
					relevantIDs[rule.AssignmentID] = true
					added = true
					break
				}
			}
		}
		if !added {
			break
		}
	}
	relevant := make([]SiteRule, 0, len(relevantIDs))
	for _, rule := range ordered {
		if relevantIDs[rule.AssignmentID] {
			relevant = append(relevant, rule)
		}
	}
	return relevant
}

// siteInputKey is how the Site snapshot names a device's reading.
func siteInputKey(deviceID, key string) string {
	return deviceID + "/" + key
}

// readsDevice is whether the policy reads one of the device's readings.
func readsDevice(policy AlarmPolicyRevision, deviceID string) bool {
	prefix := siteInputKey(deviceID, "")
	return conditionReadsDevice(policy.Raise, prefix) || conditionReadsDevice(policy.Clear, prefix)
}

func conditionReadsDevice(condition Condition, prefix string) bool {
	if strings.HasPrefix(condition.Input, prefix) {
		return true
	}
	for _, inputKey := range condition.ExprInputs {
		if strings.HasPrefix(inputKey, prefix) {
			return true
		}
	}
	for _, child := range condition.Children {
		if conditionReadsDevice(child, prefix) {
			return true
		}
	}
	return false
}

func (store *PostgresStore) loadSiteTelemetryEvaluationState(ctx context.Context, tenantID, siteID string) ([]telemetryEvaluationInputRecord, []SiteRule, map[string]bool, error) {
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
SELECT device_id::text, business_revision, event_id::text, evaluated_at, snapshot, unchanged_since
FROM alarm_runtime.telemetry_evaluation_input
WHERE tenant_id=$1 AND site_id=$2
ORDER BY device_id`, tenantID, siteID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read Site telemetry evaluation inputs: %w", err)
	}
	records := make([]telemetryEvaluationInputRecord, 0)
	for rows.Next() {
		var record telemetryEvaluationInputRecord
		var encoded, since []byte
		if err := rows.Scan(&record.DeviceID, &record.BusinessRevision, &record.EventID, &record.EvaluatedAt, &encoded, &since); err != nil {
			rows.Close()
			return nil, nil, nil, fmt.Errorf("scan Site telemetry evaluation input: %w", err)
		}
		if err := json.Unmarshal(encoded, &record.Snapshot); err != nil {
			rows.Close()
			return nil, nil, nil, fmt.Errorf("decode Site telemetry evaluation input: %w", err)
		}
		if err := json.Unmarshal(since, &record.UnchangedSince); err != nil {
			rows.Close()
			return nil, nil, nil, fmt.Errorf("decode Site telemetry unchanged-since: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, nil, fmt.Errorf("iterate Site telemetry evaluation inputs: %w", err)
	}
	rows.Close()

	assignmentRows, err := tx.Query(ctx, `
SELECT DISTINCT ON (assignment.assignment_id) assignment.assignment_id::text, assignment.retired, policy.policy
FROM alarm_runtime.alarm_policy_assignment assignment
JOIN alarm_runtime.alarm_policy_revision policy
  ON policy.tenant_id = assignment.tenant_id AND policy.site_id = assignment.site_id AND policy.policy_revision_id = assignment.policy_revision_id
WHERE assignment.tenant_id=$1 AND assignment.site_id=$2 AND assignment.subject_type='SITE' AND assignment.subject_id=$2
ORDER BY assignment.assignment_id, assignment.assignment_revision DESC`, tenantID, siteID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve Site Alarm policy assignments: %w", err)
	}
	rules := make([]SiteRule, 0)
	activeRuleSet := make(map[string]struct{})
	for assignmentRows.Next() {
		var rule SiteRule
		var retired bool
		var policyJSON []byte
		if err := assignmentRows.Scan(&rule.AssignmentID, &retired, &policyJSON); err != nil {
			assignmentRows.Close()
			return nil, nil, nil, fmt.Errorf("scan Site Alarm policy assignment: %w", err)
		}
		if retired {
			continue
		}
		if rule.Policy, err = decodeAlarmPolicy(policyJSON); err != nil {
			assignmentRows.Close()
			return nil, nil, nil, err
		}
		activeRuleSet[rule.AssignmentID] = struct{}{}
		rules = append(rules, rule)
	}
	if err := assignmentRows.Err(); err != nil {
		assignmentRows.Close()
		return nil, nil, nil, fmt.Errorf("iterate Site Alarm policy assignments: %w", err)
	}
	assignmentRows.Close()

	activeRows, err := tx.Query(ctx, `
SELECT assignment_id::text
FROM alarm_runtime.alarm_evaluation_state
WHERE tenant_id=$1 AND site_id=$2 AND active_alarm_id IS NOT NULL`, tenantID, siteID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read active Site Alarm states: %w", err)
	}
	upstreamActive := make(map[string]bool)
	for activeRows.Next() {
		var assignmentID string
		if err := activeRows.Scan(&assignmentID); err != nil {
			activeRows.Close()
			return nil, nil, nil, fmt.Errorf("scan active Site Alarm state: %w", err)
		}
		if _, nonRetired := activeRuleSet[assignmentID]; nonRetired {
			upstreamActive[assignmentID] = true
		}
	}
	if err := activeRows.Err(); err != nil {
		activeRows.Close()
		return nil, nil, nil, fmt.Errorf("iterate active Site Alarm states: %w", err)
	}
	activeRows.Close()

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, nil, fmt.Errorf("commit Site Alarm evaluation input read: %w", err)
	}
	return records, rules, upstreamActive, nil
}

func buildSiteEvaluationSnapshot(triggerEventID, tenantID, siteID string, records []telemetryEvaluationInputRecord) (EvaluationSnapshot, error) {
	if len(records) == 0 {
		return EvaluationSnapshot{}, ErrTelemetryEvaluationInvalid
	}
	sort.Slice(records, func(left, right int) bool { return records[left].DeviceID < records[right].DeviceID })
	digest := sha256.New()
	inputs := make(map[string]InputFact)
	asOf := time.Time{}
	for _, record := range records {
		_, _ = fmt.Fprintf(digest, "%s:%d:%s\n", record.DeviceID, record.BusinessRevision, record.EventID)
		if record.EvaluatedAt.After(asOf) {
			asOf = record.EvaluatedAt.UTC()
		}
		for _, state := range record.Snapshot.Values {
			key, fact, err := inputFactFromTelemetry(record, state)
			if err != nil {
				return EvaluationSnapshot{}, err
			}
			key = siteInputKey(record.DeviceID, key)
			fact.Key = key
			if _, duplicate := inputs[key]; duplicate {
				return EvaluationSnapshot{}, fmt.Errorf("%w: duplicate telemetry input %q", ErrTelemetryEvaluationInvalid, key)
			}
			inputs[key] = fact
		}
	}
	if len(inputs) > MaxEvaluationSnapshotInputs || asOf.IsZero() {
		return EvaluationSnapshot{}, ErrTelemetryEvaluationInvalid
	}
	return EvaluationSnapshot{
		TenantID:      tenantID,
		SiteID:        siteID,
		SubjectType:   "SITE",
		SubjectID:     siteID,
		InputRevision: "telemetry-site:" + hex.EncodeToString(digest.Sum(nil)),
		AsOf:          asOf.Format(time.RFC3339Nano),
		Inputs:        inputs,
		CorrelationID: triggerEventID,
	}, nil
}

func inputFactFromTelemetry(record telemetryEvaluationInputRecord, state telemetryapi.TelemetryKeyState) (string, InputFact, error) {
	revision := fmt.Sprintf("%s:%d", record.DeviceID, record.BusinessRevision)
	if state.Present != nil && state.Missing == nil {
		present := state.Present
		key := string(present.Key)
		observedAt := string(present.SampledAt)
		quality := string(present.Quality)
		if record.Snapshot.EvaluationAvailability != telemetryapi.EvaluationAvailabilityAvailable {
			quality = "UNAVAILABLE"
		} else if !strings.EqualFold(strings.TrimSpace(present.Freshness), string(telemetryapi.TelemetryFreshnessFresh)) {
			quality = "STALE"
		}
		value, err := evaluationValueFromTelemetry(present.ValueType, present.Value)
		if err != nil {
			return "", InputFact{}, fmt.Errorf("%w: telemetry key %q value cannot be evaluated", ErrTelemetryEvaluationInvalid, key)
		}
		unchanged := record.UnchangedSince[key]
		if unchanged == "" && present.UnchangedSince != nil {
			unchanged = strings.TrimSpace(string(*present.UnchangedSince))
		}
		fact := InputFact{
			Key: key, Present: true, Revision: revision, Value: value, Quality: quality, ObservedAt: observedAt, UnchangedSince: unchanged,
			QualityReasons: reasonStrings(present.QualityReasons),
			Evidence:       []alarmmodel.EvidenceReference{{Kind: "telemetry-device-snapshot", Reference: record.EventID + "#" + key, CapturedAt: observedAt}},
		}
		if present.Health != nil {
			fact.HealthReasons = reasonStrings(present.Health.Reasons)
		}
		return key, fact, nil
	}
	if state.Missing != nil && state.Present == nil {
		key := string(state.Missing.Key)
		capturedAt := record.EvaluatedAt.UTC().Format(time.RFC3339Nano)
		fact := InputFact{
			Key: key, Present: false, Revision: revision,
			Evidence: []alarmmodel.EvidenceReference{{Kind: "telemetry-device-snapshot", Reference: record.EventID + "#" + key, CapturedAt: capturedAt}},
		}
		if state.Missing.MissingReason == "ONLY_REJECTED_CANDIDATES" {
			fact.HealthReasons = []string{"OUT_OF_RANGE"}
		}
		return key, fact, nil
	}
	return "", InputFact{}, fmt.Errorf("%w: telemetry key state is ambiguous", ErrTelemetryEvaluationInvalid)
}

// unchangedSince prefers the authoritative unchangedSince timestamp on each snapshot reading,
// falling back to value diffing against the prior snapshot only when the field is absent (#616).
func unchangedSince(previous *telemetryapi.DeviceObservationSnapshot, previousSince map[string]string, next telemetryapi.DeviceObservationSnapshot) (map[string]string, error) {
	var prior map[string]any
	ensurePrior := func() (map[string]any, error) {
		if prior != nil {
			return prior, nil
		}
		prior = map[string]any{}
		if previous != nil {
			for _, state := range previous.Values {
				if state.Present != nil {
					var value any
					if err := json.Unmarshal(state.Present.Value, &value); err != nil {
						return nil, fmt.Errorf("%w: stored telemetry key %q value cannot be read", ErrTelemetryEvaluationInvalid, state.Present.Key)
					}
					prior[string(state.Present.Key)] = value
				}
			}
		}
		return prior, nil
	}
	held := map[string]string{}
	for _, state := range next.Values {
		if state.Present == nil {
			continue
		}
		key := string(state.Present.Key)
		if state.Present.UnchangedSince != nil {
			since := strings.TrimSpace(string(*state.Present.UnchangedSince))
			if since != "" {
				if _, err := time.Parse(time.RFC3339Nano, since); err != nil {
					return nil, fmt.Errorf("%w: telemetry key %q unchangedSince is invalid", ErrTelemetryEvaluationInvalid, key)
				}
				held[key] = since
				continue
			}
		}
		priorValues, err := ensurePrior()
		if err != nil {
			return nil, err
		}
		var value any
		if err := json.Unmarshal(state.Present.Value, &value); err != nil {
			return nil, fmt.Errorf("%w: telemetry key %q value cannot be read", ErrTelemetryEvaluationInvalid, key)
		}
		if before, seen := priorValues[key]; seen && previousSince[key] != "" && reflect.DeepEqual(before, value) {
			held[key] = previousSince[key]
			continue
		}
		held[key] = string(state.Present.SampledAt)
	}
	return held, nil
}

func reasonStrings(values []telemetryapi.QualityReasonCode) []string {
	if len(values) == 0 {
		return nil
	}
	reasons := make([]string, len(values))
	for index, value := range values {
		reasons[index] = string(value)
	}
	return reasons
}

func evaluationValueFromTelemetry(valueType string, raw json.RawMessage) (TypedValue, error) {
	switch strings.ToUpper(strings.TrimSpace(valueType)) {
	case "NUMBER":
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			return TypedValue{}, err
		}
		return NumberValue(value), nil
	case "STRING":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return TypedValue{}, err
		}
		return StringValue(value), nil
	case "BOOLEAN":
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return TypedValue{}, err
		}
		return BooleanValue(value), nil
	default:
		return TypedValue{}, errors.New("unsupported telemetry value type")
	}
}

func writeTelemetryEvaluationProblem(writer http.ResponseWriter, status int, code string, retryable bool) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"code": code, "retryable": retryable})
}
