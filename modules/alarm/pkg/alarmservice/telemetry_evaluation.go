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
	records, assignmentIDs, err := store.loadSiteTelemetryEvaluationState(ctx, string(snapshot.TenantId), string(snapshot.SiteId))
	if err != nil {
		return nil, err
	}
	if len(assignmentIDs) == 0 {
		return []EvaluationDecision{}, nil
	}
	evaluationSnapshot, err := buildSiteEvaluationSnapshot(eventID, string(snapshot.TenantId), string(snapshot.SiteId), records)
	if err != nil {
		return nil, err
	}
	decisions := make([]EvaluationDecision, 0, len(assignmentIDs))
	for _, assignmentID := range assignmentIDs {
		decision, err := store.EvaluateAssignedSnapshot(ctx, assignmentID, evaluationSnapshot, now.UTC())
		if err != nil {
			return decisions, err
		}
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
	err = tx.QueryRow(ctx, `
SELECT business_revision, event_id::text
FROM alarm_runtime.telemetry_evaluation_input
WHERE tenant_id=$1 AND site_id=$2 AND device_id=$3
FOR UPDATE`, tenantID, siteID, deviceID).Scan(&currentRevision, &currentEventID)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `
INSERT INTO alarm_runtime.telemetry_evaluation_input (
  tenant_id, site_id, device_id, business_revision, event_id, evaluated_at, snapshot, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8)`, tenantID, siteID, deviceID, int64(snapshot.BusinessRevision), eventID, evaluatedAt.UTC(), encoded, now.UTC())
		if err != nil {
			return fmt.Errorf("insert Alarm telemetry evaluation input: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("read current Alarm telemetry evaluation input: %w", err)
	} else if int64(snapshot.BusinessRevision) > currentRevision {
		_, err = tx.Exec(ctx, `
UPDATE alarm_runtime.telemetry_evaluation_input
SET business_revision=$4, event_id=$5, evaluated_at=$6, snapshot=$7::jsonb, updated_at=$8
WHERE tenant_id=$1 AND site_id=$2 AND device_id=$3`, tenantID, siteID, deviceID, int64(snapshot.BusinessRevision), eventID, evaluatedAt.UTC(), encoded, now.UTC())
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

func (store *PostgresStore) loadSiteTelemetryEvaluationState(ctx context.Context, tenantID, siteID string) ([]telemetryEvaluationInputRecord, []string, error) {
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
SELECT device_id::text, business_revision, event_id::text, evaluated_at, snapshot
FROM alarm_runtime.telemetry_evaluation_input
WHERE tenant_id=$1 AND site_id=$2
ORDER BY device_id`, tenantID, siteID)
	if err != nil {
		return nil, nil, fmt.Errorf("read Site telemetry evaluation inputs: %w", err)
	}
	records := make([]telemetryEvaluationInputRecord, 0)
	for rows.Next() {
		var record telemetryEvaluationInputRecord
		var encoded []byte
		if err := rows.Scan(&record.DeviceID, &record.BusinessRevision, &record.EventID, &record.EvaluatedAt, &encoded); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("scan Site telemetry evaluation input: %w", err)
		}
		if err := json.Unmarshal(encoded, &record.Snapshot); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("decode Site telemetry evaluation input: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, fmt.Errorf("iterate Site telemetry evaluation inputs: %w", err)
	}
	rows.Close()

	assignmentRows, err := tx.Query(ctx, `
SELECT DISTINCT ON (assignment_id) assignment_id::text
FROM alarm_runtime.alarm_policy_assignment
WHERE tenant_id=$1 AND site_id=$2 AND subject_type='SITE' AND subject_id=$2
ORDER BY assignment_id, assignment_revision DESC`, tenantID, siteID)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve Site Alarm policy assignments: %w", err)
	}
	assignmentIDs := make([]string, 0)
	for assignmentRows.Next() {
		var assignmentID string
		if err := assignmentRows.Scan(&assignmentID); err != nil {
			assignmentRows.Close()
			return nil, nil, fmt.Errorf("scan Site Alarm policy assignment: %w", err)
		}
		assignmentIDs = append(assignmentIDs, assignmentID)
	}
	if err := assignmentRows.Err(); err != nil {
		assignmentRows.Close()
		return nil, nil, fmt.Errorf("iterate Site Alarm policy assignments: %w", err)
	}
	assignmentRows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit Site Alarm evaluation input read: %w", err)
	}
	return records, assignmentIDs, nil
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
			if _, duplicate := inputs[key]; duplicate {
				return EvaluationSnapshot{}, fmt.Errorf("%w: duplicate canonical telemetry key %q across Site devices", ErrTelemetryEvaluationInvalid, key)
			}
			inputs[key] = fact
		}
	}
	if len(inputs) > 128 || asOf.IsZero() {
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
		return key, InputFact{
			Key: key, Present: true, Revision: revision, Value: value, Quality: quality, ObservedAt: observedAt,
			Evidence: []alarmmodel.EvidenceReference{{Kind: "telemetry-device-snapshot", Reference: record.EventID + "#" + key, CapturedAt: observedAt}},
		}, nil
	}
	if state.Missing != nil && state.Present == nil {
		key := string(state.Missing.Key)
		capturedAt := record.EvaluatedAt.UTC().Format(time.RFC3339Nano)
		return key, InputFact{
			Key: key, Present: false, Revision: revision,
			Evidence: []alarmmodel.EvidenceReference{{Kind: "telemetry-device-snapshot", Reference: record.EventID + "#" + key, CapturedAt: capturedAt}},
		}, nil
	}
	return "", InputFact{}, fmt.Errorf("%w: telemetry key state is ambiguous", ErrTelemetryEvaluationInvalid)
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
