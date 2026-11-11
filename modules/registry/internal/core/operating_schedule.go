package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	defaultOperatingScheduleName = "默认周运行计划"
	defaultPreCoolLeadMinutes    = 45
	defaultCoastDownLagMinutes   = 30
	maxOperatingScheduleSlots    = 168
	maxOperatingExceptionSlots   = 24
)

var (
	ErrMutationForbidden      = errors.New("registry mutation is forbidden for this Principal")
	ErrExceptionStateConflict = errors.New("operating exception is not in a state that allows this decision")
)

type OperatingScheduleSlot struct {
	ID                string   `json:"id,omitempty"`
	DayOfWeek         int      `json:"dayOfWeek"`
	StartMinute       int      `json:"startMinute"`
	EndMinute         int      `json:"endMinute"`
	TargetSupplyTempC *float64 `json:"targetSupplyTempC,omitempty"`
	SpanGroupID       *string  `json:"spanGroupId,omitempty"`
}

type OperatingSchedule struct {
	ID                  *string                 `json:"id"`
	TenantID            string                  `json:"tenantId"`
	SiteID              string                  `json:"siteId"`
	Timezone            string                  `json:"timezone"`
	Name                string                  `json:"name"`
	PreCoolLeadMinutes  int                     `json:"preCoolLeadMinutes"`
	CoastDownLagMinutes int                     `json:"coastDownLagMinutes"`
	Revision            int64                   `json:"revision"`
	UpdatedBy           *string                 `json:"updatedBy"`
	UpdatedAt           *string                 `json:"updatedAt"`
	Slots               []OperatingScheduleSlot `json:"slots"`
}

type OperatingScheduleSlotInput struct {
	DayOfWeek         int      `json:"dayOfWeek"`
	StartMinute       int      `json:"startMinute"`
	EndMinute         int      `json:"endMinute"`
	TargetSupplyTempC *float64 `json:"targetSupplyTempC,omitempty"`
	SpanGroupID       *string  `json:"spanGroupId,omitempty"`
}

type OperatingScheduleMutation struct {
	SiteID              string                       `json:"siteId,omitempty"`
	Name                string                       `json:"name"`
	PreCoolLeadMinutes  int                          `json:"preCoolLeadMinutes"`
	CoastDownLagMinutes int                          `json:"coastDownLagMinutes"`
	Slots               []OperatingScheduleSlotInput `json:"slots"`
	Meta                MutationMeta                 `json:"meta"`
}

type OperatingExceptionSlot struct {
	StartMinute       int      `json:"startMinute"`
	EndMinute         int      `json:"endMinute"`
	TargetSupplyTempC *float64 `json:"targetSupplyTempC,omitempty"`
}

type OperatingException struct {
	ID                 string                   `json:"id"`
	TenantID           string                   `json:"tenantId"`
	SiteID             string                   `json:"siteId"`
	Title              string                   `json:"title"`
	Kind               string                   `json:"kind"`
	StartDate          string                   `json:"startDate"`
	EndDate            string                   `json:"endDate"`
	ReplacementSlots   []OperatingExceptionSlot `json:"replacementSlots"`
	Reason             string                   `json:"reason"`
	Status             string                   `json:"status"`
	RequestedBy        string                   `json:"requestedBy"`
	RequestedByDisplay string                   `json:"requestedByDisplay"`
	DecidedBy          *string                  `json:"decidedBy"`
	DecidedByDisplay   *string                  `json:"decidedByDisplay"`
	DecidedAt          *string                  `json:"decidedAt"`
	DecisionNote       string                   `json:"decisionNote"`
	Revision           int64                    `json:"revision"`
	CreatedAt          string                   `json:"createdAt"`
	UpdatedAt          string                   `json:"updatedAt"`
}

type OperatingExceptionList struct {
	Items []OperatingException `json:"items"`
}

type OperatingExceptionFilter struct {
	From   string
	To     string
	Status string
}

type CreateOperatingExceptionRequest struct {
	SiteID           string                   `json:"siteId,omitempty"`
	ActorDisplayName string                   `json:"-"`
	Title            string                   `json:"title"`
	Kind             string                   `json:"kind"`
	StartDate        string                   `json:"startDate"`
	EndDate          string                   `json:"endDate"`
	ReplacementSlots []OperatingExceptionSlot `json:"replacementSlots"`
	AutoApprove      bool                     `json:"autoApprove,omitempty"`
	Meta             MutationMeta             `json:"meta"`
}

type DecideOperatingExceptionRequest struct {
	SiteID           string       `json:"siteId,omitempty"`
	ExceptionID      string       `json:"exceptionId,omitempty"`
	ActorDisplayName string       `json:"-"`
	Decision         string       `json:"decision"`
	DecisionNote     string       `json:"decisionNote,omitempty"`
	Meta             MutationMeta `json:"meta"`
}

func defaultOperatingSchedule(tenantID, siteID, timezone string) OperatingSchedule {
	return OperatingSchedule{
		ID:                  nil,
		TenantID:            tenantID,
		SiteID:              siteID,
		Timezone:            timezone,
		Name:                defaultOperatingScheduleName,
		PreCoolLeadMinutes:  defaultPreCoolLeadMinutes,
		CoastDownLagMinutes: defaultCoastDownLagMinutes,
		Revision:            0,
		UpdatedBy:           nil,
		UpdatedAt:           nil,
		Slots:               []OperatingScheduleSlot{},
	}
}

func grantAllowsAction(claims registryauth.GrantClaims, requested registryauth.Action) bool {
	for _, granted := range claims.Actions {
		if registryauth.ActionAllows(granted, requested) {
			return true
		}
	}
	return false
}

func normalizeActorDisplay(raw, fallback string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = strings.TrimSpace(fallback)
	}
	if utf8.RuneCountInString(trimmed) > 256 {
		runes := []rune(trimmed)
		trimmed = string(runes[:256])
	}
	if trimmed == "" {
		return "值班人员"
	}
	return trimmed
}

func validSupplyTempPointer(value *float64) bool {
	if value == nil {
		return true
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) {
		return false
	}
	return *value >= 4.0 && *value <= 20.0
}

func parseISODate(raw string) (time.Time, bool) {
	if len(raw) != 10 {
		return time.Time{}, false
	}
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil || parsed.Format("2006-01-02") != raw {
		return time.Time{}, false
	}
	return parsed, true
}

func (filter OperatingExceptionFilter) Validate() error {
	var fromDate, toDate time.Time
	var hasFrom, hasTo bool
	if filter.From != "" {
		var ok bool
		fromDate, ok = parseISODate(filter.From)
		if !ok {
			return ErrInvalidPage
		}
		hasFrom = true
	}
	if filter.To != "" {
		var ok bool
		toDate, ok = parseISODate(filter.To)
		if !ok {
			return ErrInvalidPage
		}
		hasTo = true
	}
	if hasFrom && hasTo && toDate.Before(fromDate) {
		return ErrInvalidPage
	}
	if filter.Status != "" {
		switch filter.Status {
		case "PENDING_APPROVAL", "APPROVED", "REJECTED", "CANCELLED":
		default:
			return ErrInvalidPage
		}
	}
	return nil
}

func (input OperatingScheduleMutation) Validate() error {
	if !validUUIDv7(input.SiteID) {
		return ErrInvalidMutation
	}
	if !writeKeyPattern.MatchString(input.Meta.IdempotencyKey) || strings.TrimSpace(input.Meta.Reason) == "" || utf8.RuneCountInString(input.Meta.Reason) > 1000 || input.Meta.ExpectedRevision < 0 {
		return ErrInvalidMutation
	}
	trimmedName := strings.TrimSpace(input.Name)
	nameLen := utf8.RuneCountInString(trimmedName)
	if nameLen < 1 || nameLen > 100 {
		return ErrInvalidMutation
	}
	if input.PreCoolLeadMinutes < 0 || input.PreCoolLeadMinutes > 120 {
		return ErrInvalidMutation
	}
	if input.CoastDownLagMinutes < 0 || input.CoastDownLagMinutes > 60 {
		return ErrInvalidMutation
	}
	if input.Slots == nil || len(input.Slots) > maxOperatingScheduleSlots {
		return ErrInvalidMutation
	}
	sorted := append([]OperatingScheduleSlotInput(nil), input.Slots...)
	for _, slot := range sorted {
		if slot.DayOfWeek < 1 || slot.DayOfWeek > 7 || slot.StartMinute < 0 || slot.StartMinute > 1439 || slot.EndMinute < 1 || slot.EndMinute > 1440 || slot.EndMinute <= slot.StartMinute {
			return ErrInvalidMutation
		}
		if !validSupplyTempPointer(slot.TargetSupplyTempC) {
			return ErrInvalidMutation
		}
		if slot.SpanGroupID != nil && !validUUIDv7(*slot.SpanGroupID) {
			return ErrInvalidMutation
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].DayOfWeek != sorted[j].DayOfWeek {
			return sorted[i].DayOfWeek < sorted[j].DayOfWeek
		}
		return sorted[i].StartMinute < sorted[j].StartMinute
	})
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1].DayOfWeek == sorted[i].DayOfWeek && sorted[i-1].EndMinute > sorted[i].StartMinute {
			return ErrInvalidMutation
		}
	}
	return nil
}

func (input CreateOperatingExceptionRequest) Validate() error {
	if !validUUIDv7(input.SiteID) {
		return ErrInvalidMutation
	}
	if err := input.Meta.validate(true); err != nil || utf8.RuneCountInString(strings.TrimSpace(input.Meta.Reason)) < 1 || utf8.RuneCountInString(input.Meta.Reason) > 1000 {
		return ErrInvalidMutation
	}
	titleLen := utf8.RuneCountInString(strings.TrimSpace(input.Title))
	if titleLen < 1 || titleLen > 100 {
		return ErrInvalidMutation
	}
	startDate, startOK := parseISODate(input.StartDate)
	endDate, endOK := parseISODate(input.EndDate)
	if !startOK || !endOK || endDate.Before(startDate) {
		return ErrInvalidMutation
	}
	if input.ReplacementSlots == nil || len(input.ReplacementSlots) > maxOperatingExceptionSlots {
		return ErrInvalidMutation
	}
	switch input.Kind {
	case "HOLIDAY_SHUTDOWN", "MAINTENANCE_WINDOW":
		if len(input.ReplacementSlots) != 0 {
			return ErrInvalidMutation
		}
	case "OVERTIME_COOLING":
		if len(input.ReplacementSlots) < 1 {
			return ErrInvalidMutation
		}
		sorted := append([]OperatingExceptionSlot(nil), input.ReplacementSlots...)
		for _, slot := range sorted {
			if slot.StartMinute < 0 || slot.StartMinute > 1439 || slot.EndMinute < 1 || slot.EndMinute > 1440 || slot.EndMinute <= slot.StartMinute || !validSupplyTempPointer(slot.TargetSupplyTempC) {
				return ErrInvalidMutation
			}
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].StartMinute < sorted[j].StartMinute
		})
		for i := 1; i < len(sorted); i++ {
			if sorted[i-1].EndMinute > sorted[i].StartMinute {
				return ErrInvalidMutation
			}
		}
	default:
		return ErrInvalidMutation
	}
	return nil
}

func (input DecideOperatingExceptionRequest) Validate() error {
	if !validUUIDv7(input.SiteID) || !validUUIDv7(input.ExceptionID) {
		return ErrInvalidMutation
	}
	if err := input.Meta.validate(false); err != nil || utf8.RuneCountInString(input.Meta.Reason) > 1000 {
		return ErrInvalidMutation
	}
	if utf8.RuneCountInString(input.DecisionNote) > 1000 {
		return ErrInvalidMutation
	}
	switch input.Decision {
	case "APPROVE", "REJECT", "CANCEL":
		return nil
	default:
		return ErrInvalidMutation
	}
}

func authorizeExceptionDecision(principalID string, hasScheduleManage bool, current OperatingException, decision string) error {
	switch decision {
	case "APPROVE", "REJECT":
		if !hasScheduleManage {
			return ErrMutationForbidden
		}
		if current.Status != "PENDING_APPROVAL" {
			return ErrExceptionStateConflict
		}
		return nil
	case "CANCEL":
		if hasScheduleManage {
			if current.Status != "PENDING_APPROVAL" && current.Status != "APPROVED" {
				return ErrExceptionStateConflict
			}
			return nil
		}
		if current.RequestedBy != principalID {
			return ErrMutationForbidden
		}
		if current.Status != "PENDING_APPROVAL" {
			return ErrMutationForbidden
		}
		return nil
	default:
		return ErrInvalidMutation
	}
}

func (store *PostgresStore) GetOperatingSchedule(ctx context.Context, claims registryauth.GrantClaims, siteID string) (OperatingSchedule, error) {
	if !validUUIDv7(siteID) {
		return OperatingSchedule{}, ErrNotFound
	}
	var schedule OperatingSchedule
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		var tenantID, timezone string
		err := transaction.QueryRow(ctx, `
SELECT tenant_id::text, timezone
FROM core_registry.sites
WHERE id = $1::uuid AND NOT (id = ANY($2::uuid[]))
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&tenantID, &timezone)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("query Site for Operating Schedule: %w", err)
		}

		var id, updatedBy string
		var updatedAt time.Time
		err = transaction.QueryRow(ctx, `
SELECT id::text, name, pre_cool_lead_minutes, coast_down_lag_minutes, revision, updated_by::text, updated_at
FROM core_registry.operating_schedules
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
`, tenantID, siteID).Scan(&id, &schedule.Name, &schedule.PreCoolLeadMinutes, &schedule.CoastDownLagMinutes, &schedule.Revision, &updatedBy, &updatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			schedule = defaultOperatingSchedule(tenantID, siteID, timezone)
			return nil
		}
		if err != nil {
			return fmt.Errorf("query Site Operating Schedule: %w", err)
		}
		updatedAtText := formatInstant(updatedAt)
		schedule.ID = &id
		schedule.TenantID = tenantID
		schedule.SiteID = siteID
		schedule.Timezone = timezone
		schedule.UpdatedBy = &updatedBy
		schedule.UpdatedAt = &updatedAtText
		slots, err := queryOperatingScheduleSlots(ctx, transaction, tenantID, siteID, id, schedule.Revision)
		if err != nil {
			return err
		}
		schedule.Slots = slots
		return nil
	})
	if err != nil {
		return OperatingSchedule{}, err
	}
	return schedule, nil
}

func queryOperatingScheduleSlots(ctx context.Context, transaction pgx.Tx, tenantID, siteID, scheduleID string, revision int64) ([]OperatingScheduleSlot, error) {
	slots := []OperatingScheduleSlot{}
	rows, err := transaction.Query(ctx, `
SELECT id::text, day_of_week, start_minute, end_minute, target_supply_temp_c, span_group_id::text
FROM core_registry.operating_schedule_slots
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND schedule_id = $3::uuid AND schedule_revision = $4
ORDER BY day_of_week ASC, start_minute ASC
`, tenantID, siteID, scheduleID, revision)
	if err != nil {
		return nil, fmt.Errorf("query Site Operating Schedule slots: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var slot OperatingScheduleSlot
		if err := rows.Scan(&slot.ID, &slot.DayOfWeek, &slot.StartMinute, &slot.EndMinute, &slot.TargetSupplyTempC, &slot.SpanGroupID); err != nil {
			return nil, fmt.Errorf("scan Site Operating Schedule slot: %w", err)
		}
		slots = append(slots, slot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Site Operating Schedule slots: %w", err)
	}
	return slots, nil
}

func (store *PostgresStore) SaveOperatingSchedule(ctx context.Context, claims registryauth.GrantClaims, input OperatingScheduleMutation) (OperatingSchedule, bool, error) {
	if err := input.Validate(); err != nil {
		return OperatingSchedule{}, false, err
	}
	trimmedName := strings.TrimSpace(input.Name)
	sortedInputs := append([]OperatingScheduleSlotInput(nil), input.Slots...)
	sort.Slice(sortedInputs, func(i, j int) bool {
		if sortedInputs[i].DayOfWeek != sortedInputs[j].DayOfWeek {
			return sortedInputs[i].DayOfWeek < sortedInputs[j].DayOfWeek
		}
		return sortedInputs[i].StartMinute < sortedInputs[j].StartMinute
	})

	return runRegistryMutation(ctx, store, claims, registryauth.ActionScheduleManage, input.Meta.IdempotencyKey, input.Meta.Reason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[OperatingSchedule], error) {
		var timezone string
		err := tx.QueryRow(ctx, `
SELECT timezone
FROM core_registry.sites
WHERE tenant_id = $1::uuid AND id = $2::uuid AND NOT (id = ANY($3::uuid[]))
`, claims.TenantID, input.SiteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&timezone)
		if errors.Is(err, pgx.ErrNoRows) {
			return mutationRecord[OperatingSchedule]{}, ErrNotFound
		}
		if err != nil {
			return mutationRecord[OperatingSchedule]{}, fmt.Errorf("verify Site for Operating Schedule save: %w", err)
		}

		var scheduleID string
		var currentRevision int64
		err = tx.QueryRow(ctx, `
SELECT id::text, revision
FROM core_registry.operating_schedules
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
FOR UPDATE
`, claims.TenantID, input.SiteID).Scan(&scheduleID, &currentRevision)

		var nextRevision int64
		var beforeRevision *int64
		var eventType string
		if errors.Is(err, pgx.ErrNoRows) {
			if input.Meta.ExpectedRevision != 0 {
				return mutationRecord[OperatingSchedule]{}, ErrRevisionConflict
			}
			scheduleID, err = newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[OperatingSchedule]{}, err
			}
			nextRevision = 1
			eventType = "registry.operating-schedule.created"
			if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.operating_schedules (
  id, tenant_id, site_id, name, pre_cool_lead_minutes, coast_down_lag_minutes,
  revision, updated_by, created_at, updated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, 1, $7::uuid, $8, $8)
`, scheduleID, claims.TenantID, input.SiteID, trimmedName, input.PreCoolLeadMinutes, input.CoastDownLagMinutes, claims.PrincipalID, now); err != nil {
				return mutationRecord[OperatingSchedule]{}, err
			}
		} else if err != nil {
			return mutationRecord[OperatingSchedule]{}, fmt.Errorf("lock Site Operating Schedule: %w", err)
		} else {
			if currentRevision != input.Meta.ExpectedRevision {
				return mutationRecord[OperatingSchedule]{}, ErrRevisionConflict
			}
			beforeRevision = revisionPointer(currentRevision)
			nextRevision = currentRevision + 1
			eventType = "registry.operating-schedule.updated"
			tag, err := tx.Exec(ctx, `
UPDATE core_registry.operating_schedules
SET name = $4,
    pre_cool_lead_minutes = $5,
    coast_down_lag_minutes = $6,
    revision = $7,
    updated_by = $8::uuid,
    updated_at = $9
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND revision = $10
`, claims.TenantID, input.SiteID, scheduleID, trimmedName, input.PreCoolLeadMinutes, input.CoastDownLagMinutes, nextRevision, claims.PrincipalID, now, currentRevision)
			if err != nil {
				return mutationRecord[OperatingSchedule]{}, err
			}
			if tag.RowsAffected() != 1 {
				return mutationRecord[OperatingSchedule]{}, ErrRevisionConflict
			}
		}

		slots := make([]OperatingScheduleSlot, 0, len(sortedInputs))
		for _, slotInput := range sortedInputs {
			slotID, err := newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[OperatingSchedule]{}, err
			}
			var spanGroup any
			if slotInput.SpanGroupID != nil {
				spanGroup = *slotInput.SpanGroupID
			}
			if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.operating_schedule_slots (
  id, tenant_id, site_id, schedule_id, schedule_revision,
  day_of_week, start_minute, end_minute, target_supply_temp_c, span_group_id, created_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10::uuid, $11)
`, slotID, claims.TenantID, input.SiteID, scheduleID, nextRevision, slotInput.DayOfWeek, slotInput.StartMinute, slotInput.EndMinute, slotInput.TargetSupplyTempC, spanGroup, now); err != nil {
				return mutationRecord[OperatingSchedule]{}, err
			}
			slots = append(slots, OperatingScheduleSlot{
				ID:                slotID,
				DayOfWeek:         slotInput.DayOfWeek,
				StartMinute:       slotInput.StartMinute,
				EndMinute:         slotInput.EndMinute,
				TargetSupplyTempC: slotInput.TargetSupplyTempC,
				SpanGroupID:       slotInput.SpanGroupID,
			})
		}

		updatedBy := claims.PrincipalID
		updatedAt := formatInstant(now)
		siteCopy := input.SiteID
		saved := OperatingSchedule{
			ID:                  &scheduleID,
			TenantID:            claims.TenantID,
			SiteID:              input.SiteID,
			Timezone:            timezone,
			Name:                trimmedName,
			PreCoolLeadMinutes:  input.PreCoolLeadMinutes,
			CoastDownLagMinutes: input.CoastDownLagMinutes,
			Revision:            nextRevision,
			UpdatedBy:           &updatedBy,
			UpdatedAt:           &updatedAt,
			Slots:               slots,
		}
		return mutationRecord[OperatingSchedule]{
			Result:           saved,
			SiteID:           &siteCopy,
			ResourceType:     "OPERATING_SCHEDULE",
			ResourceID:       scheduleID,
			BeforeRevision:   beforeRevision,
			AfterRevision:    revisionPointer(nextRevision),
			EventType:        eventType,
			AggregateVersion: nextRevision,
			Payload: map[string]any{
				"siteId":              input.SiteID,
				"revision":            nextRevision,
				"slotCount":           len(slots),
				"preCoolLeadMinutes":  input.PreCoolLeadMinutes,
				"coastDownLagMinutes": input.CoastDownLagMinutes,
			},
		}, nil
	})
}

func (store *PostgresStore) ListOperatingExceptions(ctx context.Context, claims registryauth.GrantClaims, siteID string, filter OperatingExceptionFilter) (OperatingExceptionList, error) {
	if err := filter.Validate(); err != nil {
		return OperatingExceptionList{}, err
	}
	if !validUUIDv7(siteID) {
		return OperatingExceptionList{}, ErrNotFound
	}
	list := OperatingExceptionList{Items: []OperatingException{}}
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		var exists bool
		if err := transaction.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM core_registry.sites
  WHERE id = $1::uuid AND NOT (id = ANY($2::uuid[]))
)
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&exists); err != nil {
			return fmt.Errorf("verify Site for Operating Exceptions list: %w", err)
		}
		if !exists {
			return ErrNotFound
		}

		var fromParam, toParam, statusParam any
		if filter.From != "" {
			fromParam = filter.From
		}
		if filter.To != "" {
			toParam = filter.To
		}
		if filter.Status != "" {
			statusParam = filter.Status
		}

		rows, err := transaction.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, title, kind,
       to_char(start_date, 'YYYY-MM-DD'), to_char(end_date, 'YYYY-MM-DD'),
       replacement_slots, reason, status,
       requested_by::text, requested_by_display,
       decided_by::text, decided_by_display, decided_at, decision_note,
       revision, created_at, updated_at
FROM core_registry.operating_exceptions
WHERE site_id = $1::uuid
  AND NOT (site_id = ANY($2::uuid[]))
  AND ($3::date IS NULL OR end_date >= $3::date)
  AND ($4::date IS NULL OR start_date <= $4::date)
  AND ($5::text IS NULL OR status = $5::text)
ORDER BY start_date DESC, created_at DESC, id DESC
LIMIT 200
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs), fromParam, toParam, statusParam)
		if err != nil {
			return fmt.Errorf("query Site Operating Exceptions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanOperatingException(rows)
			if err != nil {
				return err
			}
			list.Items = append(list.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return OperatingExceptionList{}, err
	}
	return list, nil
}

func scanOperatingException(row rowScanner) (OperatingException, error) {
	var item OperatingException
	var slotsJSON []byte
	var decidedAt *time.Time
	var createdAt, updatedAt time.Time
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.SiteID, &item.Title, &item.Kind,
		&item.StartDate, &item.EndDate,
		&slotsJSON, &item.Reason, &item.Status,
		&item.RequestedBy, &item.RequestedByDisplay,
		&item.DecidedBy, &item.DecidedByDisplay, &decidedAt, &item.DecisionNote,
		&item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return OperatingException{}, fmt.Errorf("scan Operating Exception: %w", err)
	}
	item.ReplacementSlots = []OperatingExceptionSlot{}
	if len(slotsJSON) > 0 {
		if err := json.Unmarshal(slotsJSON, &item.ReplacementSlots); err != nil {
			return OperatingException{}, fmt.Errorf("decode Operating Exception replacement slots: %w", err)
		}
	}
	if decidedAt != nil {
		formatted := formatInstant(*decidedAt)
		item.DecidedAt = &formatted
	}
	item.CreatedAt = formatInstant(createdAt)
	item.UpdatedAt = formatInstant(updatedAt)
	return item, nil
}

func (store *PostgresStore) CreateOperatingException(ctx context.Context, claims registryauth.GrantClaims, input CreateOperatingExceptionRequest) (OperatingException, bool, error) {
	if err := input.Validate(); err != nil {
		return OperatingException{}, false, err
	}
	if input.AutoApprove && !grantAllowsAction(claims, registryauth.ActionScheduleManage) {
		return OperatingException{}, false, ErrMutationForbidden
	}
	requiredAction := registryauth.ActionScheduleRequest
	if input.AutoApprove {
		requiredAction = registryauth.ActionScheduleManage
	}

	sortedSlots := append([]OperatingExceptionSlot(nil), input.ReplacementSlots...)
	sort.Slice(sortedSlots, func(i, j int) bool {
		return sortedSlots[i].StartMinute < sortedSlots[j].StartMinute
	})
	if sortedSlots == nil {
		sortedSlots = []OperatingExceptionSlot{}
	}
	slotsBytes, err := json.Marshal(sortedSlots)
	if err != nil {
		return OperatingException{}, false, ErrInvalidMutation
	}
	trimmedTitle := strings.TrimSpace(input.Title)
	trimmedReason := strings.TrimSpace(input.Meta.Reason)
	actorDisplay := normalizeActorDisplay(input.ActorDisplayName, claims.PrincipalID)

	return runRegistryMutation(ctx, store, claims, requiredAction, input.Meta.IdempotencyKey, trimmedReason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[OperatingException], error) {
		var siteExists bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM core_registry.sites
  WHERE tenant_id = $1::uuid AND id = $2::uuid AND NOT (id = ANY($3::uuid[]))
)
`, claims.TenantID, input.SiteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&siteExists); err != nil {
			return mutationRecord[OperatingException]{}, fmt.Errorf("verify Site for Operating Exception create: %w", err)
		}
		if !siteExists {
			return mutationRecord[OperatingException]{}, ErrNotFound
		}

		exceptionID, err := newCoreUUIDv7(now)
		if err != nil {
			return mutationRecord[OperatingException]{}, err
		}
		status := "PENDING_APPROVAL"
		var decidedByParam, decidedByDisplayParam, decidedAtParam any
		var decidedByPtr, decidedByDisplayPtr, decidedAtPtr *string
		if input.AutoApprove {
			status = "APPROVED"
			principalCopy := claims.PrincipalID
			displayCopy := actorDisplay
			decidedText := formatInstant(now)
			decidedByParam = principalCopy
			decidedByDisplayParam = displayCopy
			decidedAtParam = now
			decidedByPtr = &principalCopy
			decidedByDisplayPtr = &displayCopy
			decidedAtPtr = &decidedText
		}

		if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.operating_exceptions (
  id, tenant_id, site_id, title, kind, start_date, end_date,
  replacement_slots, reason, status,
  requested_by, requested_by_display,
  decided_by, decided_by_display, decided_at, decision_note,
  revision, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4, $5, $6::date, $7::date,
  $8::jsonb, $9, $10,
  $11::uuid, $12,
  $13::uuid, $14, $15, '',
  1, $16, $16
)
`, exceptionID, claims.TenantID, input.SiteID, trimmedTitle, input.Kind, input.StartDate, input.EndDate,
			slotsBytes, trimmedReason, status,
			claims.PrincipalID, actorDisplay,
			decidedByParam, decidedByDisplayParam, decidedAtParam,
			now); err != nil {
			return mutationRecord[OperatingException]{}, err
		}

		createdText := formatInstant(now)
		siteCopy := input.SiteID
		created := OperatingException{
			ID:                 exceptionID,
			TenantID:           claims.TenantID,
			SiteID:             input.SiteID,
			Title:              trimmedTitle,
			Kind:               input.Kind,
			StartDate:          input.StartDate,
			EndDate:            input.EndDate,
			ReplacementSlots:   sortedSlots,
			Reason:             trimmedReason,
			Status:             status,
			RequestedBy:        claims.PrincipalID,
			RequestedByDisplay: actorDisplay,
			DecidedBy:          decidedByPtr,
			DecidedByDisplay:   decidedByDisplayPtr,
			DecidedAt:          decidedAtPtr,
			DecisionNote:       "",
			Revision:           1,
			CreatedAt:          createdText,
			UpdatedAt:          createdText,
		}
		return mutationRecord[OperatingException]{
			Result:           created,
			SiteID:           &siteCopy,
			ResourceType:     "OPERATING_EXCEPTION",
			ResourceID:       exceptionID,
			AfterRevision:    revisionPointer(1),
			EventType:        "registry.operating-exception.created",
			AggregateVersion: 1,
			Payload: map[string]any{
				"siteId":    input.SiteID,
				"kind":      input.Kind,
				"startDate": input.StartDate,
				"endDate":   input.EndDate,
				"status":    status,
			},
		}, nil
	})
}

func (store *PostgresStore) DecideOperatingException(ctx context.Context, claims registryauth.GrantClaims, input DecideOperatingExceptionRequest) (OperatingException, bool, error) {
	if err := input.Validate(); err != nil {
		return OperatingException{}, false, err
	}
	hasManage := grantAllowsAction(claims, registryauth.ActionScheduleManage)
	if (input.Decision == "APPROVE" || input.Decision == "REJECT") && !hasManage {
		return OperatingException{}, false, ErrMutationForbidden
	}
	requiredAction := registryauth.ActionScheduleRequest
	if hasManage {
		requiredAction = registryauth.ActionScheduleManage
	}
	trimmedReason := strings.TrimSpace(input.Meta.Reason)
	trimmedNote := strings.TrimSpace(input.DecisionNote)
	actorDisplay := normalizeActorDisplay(input.ActorDisplayName, claims.PrincipalID)

	return runRegistryMutation(ctx, store, claims, requiredAction, input.Meta.IdempotencyKey, trimmedReason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[OperatingException], error) {
		row := tx.QueryRow(ctx, `
SELECT id::text, tenant_id::text, site_id::text, title, kind,
       to_char(start_date, 'YYYY-MM-DD'), to_char(end_date, 'YYYY-MM-DD'),
       replacement_slots, reason, status,
       requested_by::text, requested_by_display,
       decided_by::text, decided_by_display, decided_at, decision_note,
       revision, created_at, updated_at
FROM core_registry.operating_exceptions
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid
  AND NOT (site_id = ANY($4::uuid[]))
FOR UPDATE
`, claims.TenantID, input.SiteID, input.ExceptionID, postgresUUIDArray(claims.DeniedSiteIDs))
		current, err := scanOperatingException(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return mutationRecord[OperatingException]{}, ErrNotFound
		}
		if err != nil {
			return mutationRecord[OperatingException]{}, err
		}

		if err := authorizeExceptionDecision(claims.PrincipalID, hasManage, current, input.Decision); err != nil {
			return mutationRecord[OperatingException]{}, err
		}
		if current.Revision != input.Meta.ExpectedRevision {
			return mutationRecord[OperatingException]{}, ErrRevisionConflict
		}

		nextStatus := "CANCELLED"
		switch input.Decision {
		case "APPROVE":
			nextStatus = "APPROVED"
		case "REJECT":
			nextStatus = "REJECTED"
		}
		nextRevision := current.Revision + 1

		tag, err := tx.Exec(ctx, `
UPDATE core_registry.operating_exceptions
SET status = $4,
    decided_by = $5::uuid,
    decided_by_display = $6,
    decided_at = $7,
    decision_note = $8,
    revision = $9,
    updated_at = $7
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND revision = $10
`, claims.TenantID, input.SiteID, input.ExceptionID, nextStatus, claims.PrincipalID, actorDisplay, now, trimmedNote, nextRevision, current.Revision)
		if err != nil {
			return mutationRecord[OperatingException]{}, err
		}
		if tag.RowsAffected() != 1 {
			return mutationRecord[OperatingException]{}, ErrRevisionConflict
		}

		principalCopy := claims.PrincipalID
		decidedAtText := formatInstant(now)
		current.Status = nextStatus
		current.DecidedBy = &principalCopy
		current.DecidedByDisplay = &actorDisplay
		current.DecidedAt = &decidedAtText
		current.DecisionNote = trimmedNote
		current.Revision = nextRevision
		current.UpdatedAt = decidedAtText
		siteCopy := input.SiteID

		return mutationRecord[OperatingException]{
			Result:           current,
			SiteID:           &siteCopy,
			ResourceType:     "OPERATING_EXCEPTION",
			ResourceID:       current.ID,
			BeforeRevision:   revisionPointer(nextRevision - 1),
			AfterRevision:    revisionPointer(nextRevision),
			EventType:        "registry.operating-exception." + strings.ToLower(nextStatus),
			AggregateVersion: nextRevision,
			Payload: map[string]any{
				"siteId":   input.SiteID,
				"decision": input.Decision,
				"status":   nextStatus,
			},
		}, nil
	})
}
