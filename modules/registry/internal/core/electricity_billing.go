package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	UtilityBillScopeChillerPlant  = "CHILLER_PLANT"
	UtilityBillScopeWholeBuilding = "WHOLE_BUILDING"

	BasicChargeModeNone                   = "NONE"
	BasicChargeModeTransformerCapacityKVA = "TRANSFORMER_CAPACITY_KVA"
	BasicChargeModeMaxDemandKW            = "MAX_DEMAND_KW"

	UtilityBillStatusDraft     = "DRAFT"
	UtilityBillStatusConfirmed = "CONFIRMED"

	maxMoneyAmountCny = 999_999_999_999.99
	maxEnergyKwh      = 1_000_000_000.0
)

var (
	ErrUtilityBillStateConflict = errors.New("utility bill is already confirmed and locked")
	billingMonthPattern         = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])\z`)
)

type MonthlyElectricityBudget struct {
	ID               string  `json:"id"`
	TenantID         string  `json:"tenantId"`
	SiteID           string  `json:"siteId"`
	BillingMonth     string  `json:"billingMonth"`
	BudgetAmountCny  float64 `json:"budgetAmountCny"`
	Note             string  `json:"note"`
	Revision         int64   `json:"revision"`
	UpdatedBy        string  `json:"updatedBy"`
	UpdatedByDisplay string  `json:"updatedByDisplay"`
	CreatedAt        string  `json:"createdAt"`
	UpdatedAt        string  `json:"updatedAt"`
}

type MonthlyElectricityBudgetList struct {
	Items []MonthlyElectricityBudget `json:"items"`
}

type MonthlyElectricityBudgetFilter struct {
	BillingMonth string
	FromMonth    string
	ToMonth      string
}

func (filter MonthlyElectricityBudgetFilter) Validate() error {
	if filter.BillingMonth != "" && !billingMonthPattern.MatchString(filter.BillingMonth) {
		return ErrInvalidMutation
	}
	if filter.FromMonth != "" && !billingMonthPattern.MatchString(filter.FromMonth) {
		return ErrInvalidMutation
	}
	if filter.ToMonth != "" && !billingMonthPattern.MatchString(filter.ToMonth) {
		return ErrInvalidMutation
	}
	if filter.FromMonth != "" && filter.ToMonth != "" && filter.FromMonth > filter.ToMonth {
		return ErrInvalidMutation
	}
	return nil
}

type MonthlyElectricityBudgetMutation struct {
	SiteID           string       `json:"siteId,omitempty"`
	ActorDisplayName string       `json:"-"`
	BillingMonth     string       `json:"billingMonth"`
	BudgetAmountCny  float64      `json:"budgetAmountCny"`
	Note             string       `json:"note"`
	Meta             MutationMeta `json:"meta"`
}

func (input MonthlyElectricityBudgetMutation) Validate() error {
	if !validUUIDv7(input.SiteID) || !billingMonthPattern.MatchString(input.BillingMonth) {
		return ErrInvalidMutation
	}
	if _, ok := validMoneyCents(input.BudgetAmountCny, false); !ok {
		return ErrInvalidMutation
	}
	if utf8.RuneCountInString(input.Note) > 1000 || utf8.RuneCountInString(input.Meta.Reason) > 1000 {
		return ErrInvalidMutation
	}
	if !writeKeyPattern.MatchString(input.Meta.IdempotencyKey) || strings.TrimSpace(input.Meta.Reason) == "" || len(input.Meta.Reason) > 1000 || input.Meta.ExpectedRevision < 0 {
		return ErrInvalidMutation
	}
	return nil
}

type UtilityBillTouBreakdown struct {
	SharpKwh  *float64 `json:"sharpKwh,omitempty"`
	PeakKwh   *float64 `json:"peakKwh,omitempty"`
	FlatKwh   *float64 `json:"flatKwh,omitempty"`
	ValleyKwh *float64 `json:"valleyKwh,omitempty"`
}

type UtilityBill struct {
	ID                        string                   `json:"id"`
	TenantID                  string                   `json:"tenantId"`
	SiteID                    string                   `json:"siteId"`
	BillingMonth              string                   `json:"billingMonth"`
	ReadingStartDate          string                   `json:"readingStartDate"`
	ReadingEndDate            string                   `json:"readingEndDate"`
	MeterScope                string                   `json:"meterScope"`
	BilledEnergyKwh           float64                  `json:"billedEnergyKwh"`
	TouBreakdown              *UtilityBillTouBreakdown `json:"touBreakdown,omitempty"`
	EnergyChargeCny           float64                  `json:"energyChargeCny"`
	BasicChargeMode           string                   `json:"basicChargeMode"`
	BasicChargeQuantity       *float64                 `json:"basicChargeQuantity"`
	BasicChargeCny            float64                  `json:"basicChargeCny"`
	StandardPowerFactor       *float64                 `json:"standardPowerFactor"`
	ActualPowerFactor         *float64                 `json:"actualPowerFactor"`
	PowerFactorAdjustmentCny  float64                  `json:"powerFactorAdjustmentCny"`
	OtherChargesCny           float64                  `json:"otherChargesCny"`
	TotalPayableCny           float64                  `json:"totalPayableCny"`
	ChillerShareOverrideRatio *float64                 `json:"chillerShareOverrideRatio"`
	UtilityAccountNo          string                   `json:"utilityAccountNo"`
	InvoiceNo                 string                   `json:"invoiceNo"`
	Note                      string                   `json:"note"`
	Status                    string                   `json:"status"`
	ConfirmedBy               *string                  `json:"confirmedBy"`
	ConfirmedByDisplay        *string                  `json:"confirmedByDisplay"`
	ConfirmedAt               *string                  `json:"confirmedAt"`
	UpdatedBy                 string                   `json:"updatedBy"`
	UpdatedByDisplay          string                   `json:"updatedByDisplay"`
	Revision                  int64                    `json:"revision"`
	CreatedAt                 string                   `json:"createdAt"`
	UpdatedAt                 string                   `json:"updatedAt"`
}

type UtilityBillList struct {
	Items []UtilityBill `json:"items"`
}

type UtilityBillFilter struct {
	BillingMonth string
	MeterScope   string
	Status       string
}

func (filter UtilityBillFilter) Validate() error {
	if filter.BillingMonth != "" && !billingMonthPattern.MatchString(filter.BillingMonth) {
		return ErrInvalidMutation
	}
	if filter.MeterScope != "" && filter.MeterScope != UtilityBillScopeChillerPlant && filter.MeterScope != UtilityBillScopeWholeBuilding {
		return ErrInvalidMutation
	}
	if filter.Status != "" && filter.Status != UtilityBillStatusDraft && filter.Status != UtilityBillStatusConfirmed {
		return ErrInvalidMutation
	}
	return nil
}

type UtilityBillMutation struct {
	ID                        string                   `json:"id,omitempty"`
	SiteID                    string                   `json:"siteId,omitempty"`
	ActorDisplayName          string                   `json:"-"`
	BillingMonth              string                   `json:"billingMonth"`
	ReadingStartDate          string                   `json:"readingStartDate"`
	ReadingEndDate            string                   `json:"readingEndDate"`
	MeterScope                string                   `json:"meterScope"`
	BilledEnergyKwh           float64                  `json:"billedEnergyKwh"`
	TouBreakdown              *UtilityBillTouBreakdown `json:"touBreakdown,omitempty"`
	EnergyChargeCny           float64                  `json:"energyChargeCny"`
	BasicChargeMode           string                   `json:"basicChargeMode"`
	BasicChargeQuantity       *float64                 `json:"basicChargeQuantity,omitempty"`
	BasicChargeCny            float64                  `json:"basicChargeCny"`
	StandardPowerFactor       *float64                 `json:"standardPowerFactor,omitempty"`
	ActualPowerFactor         *float64                 `json:"actualPowerFactor,omitempty"`
	PowerFactorAdjustmentCny  float64                  `json:"powerFactorAdjustmentCny"`
	OtherChargesCny           float64                  `json:"otherChargesCny"`
	TotalPayableCny           float64                  `json:"totalPayableCny"`
	ChillerShareOverrideRatio *float64                 `json:"chillerShareOverrideRatio,omitempty"`
	UtilityAccountNo          string                   `json:"utilityAccountNo,omitempty"`
	InvoiceNo                 string                   `json:"invoiceNo,omitempty"`
	Note                      string                   `json:"note,omitempty"`
	Meta                      MutationMeta             `json:"meta"`
}

func (input UtilityBillMutation) Validate() error {
	isCreate := input.ID == ""
	if !validUUIDv7(input.SiteID) || (!isCreate && !validUUIDv7(input.ID)) {
		return ErrInvalidMutation
	}
	if err := input.Meta.validate(isCreate); err != nil || utf8.RuneCountInString(input.Meta.Reason) > 1000 {
		return ErrInvalidMutation
	}
	if !billingMonthPattern.MatchString(input.BillingMonth) {
		return ErrInvalidMutation
	}
	startDate, err := time.Parse("2006-01-02", input.ReadingStartDate)
	if err != nil {
		return ErrInvalidMutation
	}
	endDate, err := time.Parse("2006-01-02", input.ReadingEndDate)
	if err != nil || endDate.Before(startDate) {
		return ErrInvalidMutation
	}
	if input.MeterScope != UtilityBillScopeChillerPlant && input.MeterScope != UtilityBillScopeWholeBuilding {
		return ErrInvalidMutation
	}
	if !validFiniteRange(input.BilledEnergyKwh, 0, maxEnergyKwh) {
		return ErrInvalidMutation
	}
	if input.TouBreakdown != nil {
		for _, val := range []*float64{input.TouBreakdown.SharpKwh, input.TouBreakdown.PeakKwh, input.TouBreakdown.FlatKwh, input.TouBreakdown.ValleyKwh} {
			if val != nil && !validFiniteRange(*val, 0, maxEnergyKwh) {
				return ErrInvalidMutation
			}
		}
	}
	energyCents, ok := validMoneyCents(input.EnergyChargeCny, false)
	if !ok {
		return ErrInvalidMutation
	}
	basicCents, ok := validMoneyCents(input.BasicChargeCny, false)
	if !ok {
		return ErrInvalidMutation
	}
	switch input.BasicChargeMode {
	case BasicChargeModeNone:
		if input.BasicChargeQuantity != nil || basicCents != 0 {
			return ErrInvalidMutation
		}
	case BasicChargeModeTransformerCapacityKVA, BasicChargeModeMaxDemandKW:
		if input.BasicChargeQuantity == nil || !validFiniteRange(*input.BasicChargeQuantity, 0.01, 1_000_000) {
			return ErrInvalidMutation
		}
	default:
		return ErrInvalidMutation
	}
	if input.StandardPowerFactor != nil {
		if !validFiniteRange(*input.StandardPowerFactor, 0.70, 0.99) || math.Abs(*input.StandardPowerFactor*100-math.Round(*input.StandardPowerFactor*100)) > 1e-4 {
			return ErrInvalidMutation
		}
	}
	if input.ActualPowerFactor != nil {
		if !validFiniteRange(*input.ActualPowerFactor, 0, 1) || math.Abs(*input.ActualPowerFactor*1000-math.Round(*input.ActualPowerFactor*1000)) > 1e-4 {
			return ErrInvalidMutation
		}
	}
	pfCents, ok := validMoneyCents(input.PowerFactorAdjustmentCny, true)
	if !ok {
		return ErrInvalidMutation
	}
	otherCents, ok := validMoneyCents(input.OtherChargesCny, true)
	if !ok {
		return ErrInvalidMutation
	}
	totalCents, ok := validMoneyCents(input.TotalPayableCny, false)
	if !ok {
		return ErrInvalidMutation
	}
	if energyCents+basicCents+pfCents+otherCents != totalCents {
		return ErrInvalidMutation
	}
	if input.ChillerShareOverrideRatio != nil {
		if input.MeterScope != UtilityBillScopeWholeBuilding || !validFiniteRange(*input.ChillerShareOverrideRatio, 0.0001, 1.0) {
			return ErrInvalidMutation
		}
	}
	if utf8.RuneCountInString(input.UtilityAccountNo) > 100 || utf8.RuneCountInString(input.InvoiceNo) > 100 || utf8.RuneCountInString(input.Note) > 1000 {
		return ErrInvalidMutation
	}
	return nil
}

type ConfirmUtilityBillRequest struct {
	SiteID           string       `json:"siteId,omitempty"`
	BillID           string       `json:"billId,omitempty"`
	ActorDisplayName string       `json:"-"`
	Note             *string      `json:"note,omitempty"`
	Meta             MutationMeta `json:"meta"`
}

func (input ConfirmUtilityBillRequest) Validate() error {
	if !validUUIDv7(input.SiteID) || !validUUIDv7(input.BillID) {
		return ErrInvalidMutation
	}
	if err := input.Meta.validate(false); err != nil || utf8.RuneCountInString(input.Meta.Reason) > 1000 {
		return ErrInvalidMutation
	}
	if input.Note != nil && utf8.RuneCountInString(*input.Note) > 1000 {
		return ErrInvalidMutation
	}
	return nil
}

func validFiniteRange(value, minValue, maxValue float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= minValue && value <= maxValue
}

func validMoneyCents(value float64, allowNegative bool) (int64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > maxMoneyAmountCny {
		return 0, false
	}
	if !allowNegative && value < 0 {
		return 0, false
	}
	scaled := value * 100
	rounded := math.Round(scaled)
	if math.Abs(scaled-rounded) > 1e-4 {
		return 0, false
	}
	return int64(rounded), true
}

func roundCents(value float64) float64 {
	return math.Round(value*100) / 100
}

func formatNumericCents(value float64) string {
	return fmt.Sprintf("%.2f", roundCents(value))
}

func normalizeTouBreakdown(input *UtilityBillTouBreakdown) *UtilityBillTouBreakdown {
	if input == nil {
		return nil
	}
	if input.SharpKwh == nil && input.PeakKwh == nil && input.FlatKwh == nil && input.ValleyKwh == nil {
		return nil
	}
	copyBreakdown := *input
	return &copyBreakdown
}

func (store *PostgresStore) ListMonthlyElectricityBudgets(ctx context.Context, claims registryauth.GrantClaims, siteID string, filter MonthlyElectricityBudgetFilter) (MonthlyElectricityBudgetList, error) {
	if err := filter.Validate(); err != nil {
		return MonthlyElectricityBudgetList{}, err
	}
	if !validUUIDv7(siteID) {
		return MonthlyElectricityBudgetList{}, ErrNotFound
	}
	list := MonthlyElectricityBudgetList{Items: []MonthlyElectricityBudget{}}
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		var exists bool
		if err := transaction.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM core_registry.sites
  WHERE id = $1::uuid AND NOT (id = ANY($2::uuid[]))
)
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&exists); err != nil {
			return fmt.Errorf("verify Site for monthly electricity budgets list: %w", err)
		}
		if !exists {
			return ErrNotFound
		}

		var monthParam, fromParam, toParam any
		if filter.BillingMonth != "" {
			monthParam = filter.BillingMonth
		}
		if filter.FromMonth != "" {
			fromParam = filter.FromMonth
		}
		if filter.ToMonth != "" {
			toParam = filter.ToMonth
		}

		rows, err := transaction.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, billing_month,
       budget_amount_cny::text, note, revision,
       updated_by::text, updated_by_display, created_at, updated_at
FROM core_registry.monthly_electricity_budgets
WHERE site_id = $1::uuid
  AND NOT (site_id = ANY($2::uuid[]))
  AND ($3::text IS NULL OR billing_month = $3::text)
  AND ($4::text IS NULL OR billing_month >= $4::text)
  AND ($5::text IS NULL OR billing_month <= $5::text)
ORDER BY billing_month DESC
LIMIT 120
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs), monthParam, fromParam, toParam)
		if err != nil {
			return fmt.Errorf("query Site monthly electricity budgets: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var item MonthlyElectricityBudget
			var amountText string
			var createdAt, updatedAt time.Time
			if err := rows.Scan(
				&item.ID, &item.TenantID, &item.SiteID, &item.BillingMonth,
				&amountText, &item.Note, &item.Revision,
				&item.UpdatedBy, &item.UpdatedByDisplay, &createdAt, &updatedAt,
			); err != nil {
				return fmt.Errorf("scan monthly electricity budget: %w", err)
			}
			parsedAmount, err := strconv.ParseFloat(amountText, 64)
			if err != nil {
				return fmt.Errorf("parse monthly electricity budget amount: %w", err)
			}
			item.BudgetAmountCny = roundCents(parsedAmount)
			item.CreatedAt = formatInstant(createdAt)
			item.UpdatedAt = formatInstant(updatedAt)
			list.Items = append(list.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return MonthlyElectricityBudgetList{}, err
	}
	return list, nil
}

func (store *PostgresStore) SaveMonthlyElectricityBudget(ctx context.Context, claims registryauth.GrantClaims, input MonthlyElectricityBudgetMutation) (MonthlyElectricityBudget, bool, error) {
	if err := input.Validate(); err != nil {
		return MonthlyElectricityBudget{}, false, err
	}
	trimmedNote := strings.TrimSpace(input.Note)
	trimmedReason := strings.TrimSpace(input.Meta.Reason)
	actorDisplay := normalizeActorDisplay(input.ActorDisplayName, claims.PrincipalID)
	amountRounded := roundCents(input.BudgetAmountCny)
	amountNumeric := formatNumericCents(amountRounded)

	return runRegistryMutation(ctx, store, claims, registryauth.ActionBillingManage, input.Meta.IdempotencyKey, trimmedReason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[MonthlyElectricityBudget], error) {
		var siteExists bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM core_registry.sites
  WHERE tenant_id = $1::uuid AND id = $2::uuid AND NOT (id = ANY($3::uuid[]))
)
`, claims.TenantID, input.SiteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&siteExists); err != nil {
			return mutationRecord[MonthlyElectricityBudget]{}, fmt.Errorf("verify Site for monthly electricity budget save: %w", err)
		}
		if !siteExists {
			return mutationRecord[MonthlyElectricityBudget]{}, ErrNotFound
		}

		var budgetID string
		var currentRevision int64
		var createdAt time.Time
		err := tx.QueryRow(ctx, `
SELECT id::text, revision, created_at
FROM core_registry.monthly_electricity_budgets
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND billing_month = $3
FOR UPDATE
`, claims.TenantID, input.SiteID, input.BillingMonth).Scan(&budgetID, &currentRevision, &createdAt)

		var nextRevision int64
		var beforeRevision *int64
		var eventType string
		if errors.Is(err, pgx.ErrNoRows) {
			if input.Meta.ExpectedRevision != 0 {
				return mutationRecord[MonthlyElectricityBudget]{}, ErrRevisionConflict
			}
			budgetID, err = newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[MonthlyElectricityBudget]{}, err
			}
			createdAt = now
			nextRevision = 1
			eventType = "registry.monthly-electricity-budget.created"
			if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.monthly_electricity_budgets (
  id, tenant_id, site_id, billing_month, budget_amount_cny, note,
  revision, updated_by, updated_by_display, created_at, updated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::numeric(14,2), $6, 1, $7::uuid, $8, $9, $9)
`, budgetID, claims.TenantID, input.SiteID, input.BillingMonth, amountNumeric, trimmedNote, claims.PrincipalID, actorDisplay, now); err != nil {
				return mutationRecord[MonthlyElectricityBudget]{}, err
			}
		} else if err != nil {
			return mutationRecord[MonthlyElectricityBudget]{}, fmt.Errorf("lock Site monthly electricity budget: %w", err)
		} else {
			if currentRevision != input.Meta.ExpectedRevision {
				return mutationRecord[MonthlyElectricityBudget]{}, ErrRevisionConflict
			}
			beforeRevision = revisionPointer(currentRevision)
			nextRevision = currentRevision + 1
			eventType = "registry.monthly-electricity-budget.updated"
			tag, err := tx.Exec(ctx, `
UPDATE core_registry.monthly_electricity_budgets
SET budget_amount_cny = $4::numeric(14,2),
    note = $5,
    revision = $6,
    updated_by = $7::uuid,
    updated_by_display = $8,
    updated_at = $9
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND revision = $10
`, claims.TenantID, input.SiteID, budgetID, amountNumeric, trimmedNote, nextRevision, claims.PrincipalID, actorDisplay, now, currentRevision)
			if err != nil {
				return mutationRecord[MonthlyElectricityBudget]{}, err
			}
			if tag.RowsAffected() != 1 {
				return mutationRecord[MonthlyElectricityBudget]{}, ErrRevisionConflict
			}
		}

		siteCopy := input.SiteID
		saved := MonthlyElectricityBudget{
			ID:               budgetID,
			TenantID:         claims.TenantID,
			SiteID:           input.SiteID,
			BillingMonth:     input.BillingMonth,
			BudgetAmountCny:  amountRounded,
			Note:             trimmedNote,
			Revision:         nextRevision,
			UpdatedBy:        claims.PrincipalID,
			UpdatedByDisplay: actorDisplay,
			CreatedAt:        formatInstant(createdAt),
			UpdatedAt:        formatInstant(now),
		}
		return mutationRecord[MonthlyElectricityBudget]{
			Result:           saved,
			SiteID:           &siteCopy,
			ResourceType:     "MONTHLY_ELECTRICITY_BUDGET",
			ResourceID:       budgetID,
			BeforeRevision:   beforeRevision,
			AfterRevision:    revisionPointer(nextRevision),
			EventType:        eventType,
			AggregateVersion: nextRevision,
			Payload: map[string]any{
				"siteId":          input.SiteID,
				"billingMonth":    input.BillingMonth,
				"budgetAmountCny": amountRounded,
				"revision":        nextRevision,
			},
		}, nil
	})
}

func (store *PostgresStore) ListUtilityBills(ctx context.Context, claims registryauth.GrantClaims, siteID string, filter UtilityBillFilter) (UtilityBillList, error) {
	if err := filter.Validate(); err != nil {
		return UtilityBillList{}, err
	}
	if !validUUIDv7(siteID) {
		return UtilityBillList{}, ErrNotFound
	}
	list := UtilityBillList{Items: []UtilityBill{}}
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		var exists bool
		if err := transaction.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM core_registry.sites
  WHERE id = $1::uuid AND NOT (id = ANY($2::uuid[]))
)
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&exists); err != nil {
			return fmt.Errorf("verify Site for utility bills list: %w", err)
		}
		if !exists {
			return ErrNotFound
		}

		var monthParam, scopeParam, statusParam any
		if filter.BillingMonth != "" {
			monthParam = filter.BillingMonth
		}
		if filter.MeterScope != "" {
			scopeParam = filter.MeterScope
		}
		if filter.Status != "" {
			statusParam = filter.Status
		}

		rows, err := transaction.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, billing_month,
       to_char(reading_start_date, 'YYYY-MM-DD'), to_char(reading_end_date, 'YYYY-MM-DD'),
       meter_scope, billed_energy_kwh, tou_breakdown,
       energy_charge_cny::text, basic_charge_mode, basic_charge_quantity, basic_charge_cny::text,
       standard_power_factor::text, actual_power_factor::text,
       power_factor_adjustment_cny::text, other_charges_cny::text, total_payable_cny::text,
       chiller_share_override_ratio, utility_account_no, invoice_no, note, status,
       confirmed_by::text, confirmed_by_display, confirmed_at,
       updated_by::text, updated_by_display, revision, created_at, updated_at
FROM core_registry.utility_bills
WHERE site_id = $1::uuid
  AND NOT (site_id = ANY($2::uuid[]))
  AND ($3::text IS NULL OR billing_month = $3::text)
  AND ($4::text IS NULL OR meter_scope = $4::text)
  AND ($5::text IS NULL OR status = $5::text)
ORDER BY billing_month DESC, created_at DESC, id DESC
LIMIT 200
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs), monthParam, scopeParam, statusParam)
		if err != nil {
			return fmt.Errorf("query Site utility bills: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanUtilityBill(rows)
			if err != nil {
				return err
			}
			list.Items = append(list.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return UtilityBillList{}, err
	}
	return list, nil
}

func scanUtilityBill(row rowScanner) (UtilityBill, error) {
	var item UtilityBill
	var touJSON []byte
	var energyText, basicText, pfText, otherText, totalText string
	var stdPfText, actPfText *string
	var confirmedAt *time.Time
	var createdAt, updatedAt time.Time
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.SiteID, &item.BillingMonth,
		&item.ReadingStartDate, &item.ReadingEndDate,
		&item.MeterScope, &item.BilledEnergyKwh, &touJSON,
		&energyText, &item.BasicChargeMode, &item.BasicChargeQuantity, &basicText,
		&stdPfText, &actPfText,
		&pfText, &otherText, &totalText,
		&item.ChillerShareOverrideRatio, &item.UtilityAccountNo, &item.InvoiceNo, &item.Note, &item.Status,
		&item.ConfirmedBy, &item.ConfirmedByDisplay, &confirmedAt,
		&item.UpdatedBy, &item.UpdatedByDisplay, &item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return UtilityBill{}, fmt.Errorf("scan utility bill: %w", err)
	}
	var err error
	if item.EnergyChargeCny, err = parseMoneyField(energyText); err != nil {
		return UtilityBill{}, err
	}
	if item.BasicChargeCny, err = parseMoneyField(basicText); err != nil {
		return UtilityBill{}, err
	}
	if item.PowerFactorAdjustmentCny, err = parseMoneyField(pfText); err != nil {
		return UtilityBill{}, err
	}
	if item.OtherChargesCny, err = parseMoneyField(otherText); err != nil {
		return UtilityBill{}, err
	}
	if item.TotalPayableCny, err = parseMoneyField(totalText); err != nil {
		return UtilityBill{}, err
	}
	if stdPfText != nil {
		val, err := strconv.ParseFloat(*stdPfText, 64)
		if err != nil {
			return UtilityBill{}, fmt.Errorf("parse standard power factor: %w", err)
		}
		item.StandardPowerFactor = &val
	}
	if actPfText != nil {
		val, err := strconv.ParseFloat(*actPfText, 64)
		if err != nil {
			return UtilityBill{}, fmt.Errorf("parse actual power factor: %w", err)
		}
		item.ActualPowerFactor = &val
	}
	if len(touJSON) > 0 && string(touJSON) != "{}" && string(touJSON) != "null" {
		var breakdown UtilityBillTouBreakdown
		if err := json.Unmarshal(touJSON, &breakdown); err != nil {
			return UtilityBill{}, fmt.Errorf("decode utility bill TOU breakdown: %w", err)
		}
		item.TouBreakdown = normalizeTouBreakdown(&breakdown)
	}
	if confirmedAt != nil {
		formatted := formatInstant(*confirmedAt)
		item.ConfirmedAt = &formatted
	}
	item.CreatedAt = formatInstant(createdAt)
	item.UpdatedAt = formatInstant(updatedAt)
	return item, nil
}

func parseMoneyField(raw string) (float64, error) {
	val, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse numeric money field %q: %w", raw, err)
	}
	return roundCents(val), nil
}

func (store *PostgresStore) SaveUtilityBill(ctx context.Context, claims registryauth.GrantClaims, input UtilityBillMutation) (UtilityBill, bool, error) {
	if err := input.Validate(); err != nil {
		return UtilityBill{}, false, err
	}
	normalizedTou := normalizeTouBreakdown(input.TouBreakdown)
	touBytes := []byte("{}")
	if normalizedTou != nil {
		encoded, err := json.Marshal(normalizedTou)
		if err != nil {
			return UtilityBill{}, false, ErrInvalidMutation
		}
		touBytes = encoded
	}
	trimmedAccount := strings.TrimSpace(input.UtilityAccountNo)
	trimmedInvoice := strings.TrimSpace(input.InvoiceNo)
	trimmedNote := strings.TrimSpace(input.Note)
	trimmedReason := strings.TrimSpace(input.Meta.Reason)
	actorDisplay := normalizeActorDisplay(input.ActorDisplayName, claims.PrincipalID)

	energyNumeric := formatNumericCents(input.EnergyChargeCny)
	basicNumeric := formatNumericCents(input.BasicChargeCny)
	pfNumeric := formatNumericCents(input.PowerFactorAdjustmentCny)
	otherNumeric := formatNumericCents(input.OtherChargesCny)
	totalNumeric := formatNumericCents(input.TotalPayableCny)

	var stdPfNumeric, actPfNumeric any
	if input.StandardPowerFactor != nil {
		stdPfNumeric = fmt.Sprintf("%.2f", *input.StandardPowerFactor)
	}
	if input.ActualPowerFactor != nil {
		actPfNumeric = fmt.Sprintf("%.3f", *input.ActualPowerFactor)
	}

	return runRegistryMutation(ctx, store, claims, registryauth.ActionBillingManage, input.Meta.IdempotencyKey, trimmedReason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[UtilityBill], error) {
		var siteExists bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM core_registry.sites
  WHERE tenant_id = $1::uuid AND id = $2::uuid AND NOT (id = ANY($3::uuid[]))
)
`, claims.TenantID, input.SiteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&siteExists); err != nil {
			return mutationRecord[UtilityBill]{}, fmt.Errorf("verify Site for utility bill save: %w", err)
		}
		if !siteExists {
			return mutationRecord[UtilityBill]{}, ErrNotFound
		}

		var billID string
		var nextRevision int64
		var beforeRevision *int64
		var createdAt time.Time
		var eventType string

		if input.ID == "" {
			var err error
			billID, err = newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[UtilityBill]{}, err
			}
			nextRevision = 1
			createdAt = now
			eventType = "registry.utility-bill.created"
			if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.utility_bills (
  id, tenant_id, site_id, billing_month, reading_start_date, reading_end_date,
  meter_scope, billed_energy_kwh, tou_breakdown,
  energy_charge_cny, basic_charge_mode, basic_charge_quantity, basic_charge_cny,
  standard_power_factor, actual_power_factor,
  power_factor_adjustment_cny, other_charges_cny, total_payable_cny,
  chiller_share_override_ratio, utility_account_no, invoice_no, note,
  status, updated_by, updated_by_display, revision, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4, $5::date, $6::date,
  $7, $8, $9::jsonb,
  $10::numeric(14,2), $11, $12, $13::numeric(14,2),
  $14::numeric(4,2), $15::numeric(4,3),
  $16::numeric(14,2), $17::numeric(14,2), $18::numeric(14,2),
  $19, $20, $21, $22,
  'DRAFT', $23::uuid, $24, 1, $25, $25
)
`, billID, claims.TenantID, input.SiteID, input.BillingMonth, input.ReadingStartDate, input.ReadingEndDate,
				input.MeterScope, input.BilledEnergyKwh, touBytes,
				energyNumeric, input.BasicChargeMode, input.BasicChargeQuantity, basicNumeric,
				stdPfNumeric, actPfNumeric,
				pfNumeric, otherNumeric, totalNumeric,
				input.ChillerShareOverrideRatio, trimmedAccount, trimmedInvoice, trimmedNote,
				claims.PrincipalID, actorDisplay, now); err != nil {
				return mutationRecord[UtilityBill]{}, err
			}
		} else {
			billID = input.ID
			var currentStatus string
			var currentRevision int64
			err := tx.QueryRow(ctx, `
SELECT status, revision, created_at
FROM core_registry.utility_bills
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid
FOR UPDATE
`, claims.TenantID, input.SiteID, billID).Scan(&currentStatus, &currentRevision, &createdAt)
			if errors.Is(err, pgx.ErrNoRows) {
				return mutationRecord[UtilityBill]{}, ErrNotFound
			}
			if err != nil {
				return mutationRecord[UtilityBill]{}, fmt.Errorf("lock utility bill for update: %w", err)
			}
			if currentStatus != UtilityBillStatusDraft {
				return mutationRecord[UtilityBill]{}, ErrUtilityBillStateConflict
			}
			if currentRevision != input.Meta.ExpectedRevision {
				return mutationRecord[UtilityBill]{}, ErrRevisionConflict
			}
			beforeRevision = revisionPointer(currentRevision)
			nextRevision = currentRevision + 1
			eventType = "registry.utility-bill.updated"
			tag, err := tx.Exec(ctx, `
UPDATE core_registry.utility_bills
SET billing_month = $4,
    reading_start_date = $5::date,
    reading_end_date = $6::date,
    meter_scope = $7,
    billed_energy_kwh = $8,
    tou_breakdown = $9::jsonb,
    energy_charge_cny = $10::numeric(14,2),
    basic_charge_mode = $11,
    basic_charge_quantity = $12,
    basic_charge_cny = $13::numeric(14,2),
    standard_power_factor = $14::numeric(4,2),
    actual_power_factor = $15::numeric(4,3),
    power_factor_adjustment_cny = $16::numeric(14,2),
    other_charges_cny = $17::numeric(14,2),
    total_payable_cny = $18::numeric(14,2),
    chiller_share_override_ratio = $19,
    utility_account_no = $20,
    invoice_no = $21,
    note = $22,
    updated_by = $23::uuid,
    updated_by_display = $24,
    revision = $25,
    updated_at = $26
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND revision = $27
`, claims.TenantID, input.SiteID, billID, input.BillingMonth, input.ReadingStartDate, input.ReadingEndDate,
				input.MeterScope, input.BilledEnergyKwh, touBytes,
				energyNumeric, input.BasicChargeMode, input.BasicChargeQuantity, basicNumeric,
				stdPfNumeric, actPfNumeric,
				pfNumeric, otherNumeric, totalNumeric,
				input.ChillerShareOverrideRatio, trimmedAccount, trimmedInvoice, trimmedNote,
				claims.PrincipalID, actorDisplay, nextRevision, now, currentRevision)
			if err != nil {
				return mutationRecord[UtilityBill]{}, err
			}
			if tag.RowsAffected() != 1 {
				return mutationRecord[UtilityBill]{}, ErrRevisionConflict
			}
		}

		siteCopy := input.SiteID
		saved := UtilityBill{
			ID:                        billID,
			TenantID:                  claims.TenantID,
			SiteID:                    input.SiteID,
			BillingMonth:              input.BillingMonth,
			ReadingStartDate:          input.ReadingStartDate,
			ReadingEndDate:            input.ReadingEndDate,
			MeterScope:                input.MeterScope,
			BilledEnergyKwh:           input.BilledEnergyKwh,
			TouBreakdown:              normalizedTou,
			EnergyChargeCny:           roundCents(input.EnergyChargeCny),
			BasicChargeMode:           input.BasicChargeMode,
			BasicChargeQuantity:       input.BasicChargeQuantity,
			BasicChargeCny:            roundCents(input.BasicChargeCny),
			StandardPowerFactor:       input.StandardPowerFactor,
			ActualPowerFactor:         input.ActualPowerFactor,
			PowerFactorAdjustmentCny:  roundCents(input.PowerFactorAdjustmentCny),
			OtherChargesCny:           roundCents(input.OtherChargesCny),
			TotalPayableCny:           roundCents(input.TotalPayableCny),
			ChillerShareOverrideRatio: input.ChillerShareOverrideRatio,
			UtilityAccountNo:          trimmedAccount,
			InvoiceNo:                 trimmedInvoice,
			Note:                      trimmedNote,
			Status:                    UtilityBillStatusDraft,
			UpdatedBy:                 claims.PrincipalID,
			UpdatedByDisplay:          actorDisplay,
			Revision:                  nextRevision,
			CreatedAt:                 formatInstant(createdAt),
			UpdatedAt:                 formatInstant(now),
		}
		return mutationRecord[UtilityBill]{
			Result:           saved,
			SiteID:           &siteCopy,
			ResourceType:     "UTILITY_BILL",
			ResourceID:       billID,
			BeforeRevision:   beforeRevision,
			AfterRevision:    revisionPointer(nextRevision),
			EventType:        eventType,
			AggregateVersion: nextRevision,
			Payload: map[string]any{
				"siteId":          input.SiteID,
				"billingMonth":    input.BillingMonth,
				"meterScope":      input.MeterScope,
				"totalPayableCny": saved.TotalPayableCny,
				"status":          saved.Status,
				"revision":        nextRevision,
			},
		}, nil
	})
}

func (store *PostgresStore) ConfirmUtilityBill(ctx context.Context, claims registryauth.GrantClaims, input ConfirmUtilityBillRequest) (UtilityBill, bool, error) {
	if err := input.Validate(); err != nil {
		return UtilityBill{}, false, err
	}
	trimmedReason := strings.TrimSpace(input.Meta.Reason)
	actorDisplay := normalizeActorDisplay(input.ActorDisplayName, claims.PrincipalID)

	return runRegistryMutation(ctx, store, claims, registryauth.ActionBillingManage, input.Meta.IdempotencyKey, trimmedReason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[UtilityBill], error) {
		var siteExists bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM core_registry.sites
  WHERE tenant_id = $1::uuid AND id = $2::uuid AND NOT (id = ANY($3::uuid[]))
)
`, claims.TenantID, input.SiteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&siteExists); err != nil {
			return mutationRecord[UtilityBill]{}, fmt.Errorf("verify Site for utility bill confirm: %w", err)
		}
		if !siteExists {
			return mutationRecord[UtilityBill]{}, ErrNotFound
		}

		row := tx.QueryRow(ctx, `
SELECT id::text, tenant_id::text, site_id::text, billing_month,
       to_char(reading_start_date, 'YYYY-MM-DD'), to_char(reading_end_date, 'YYYY-MM-DD'),
       meter_scope, billed_energy_kwh, tou_breakdown,
       energy_charge_cny::text, basic_charge_mode, basic_charge_quantity, basic_charge_cny::text,
       standard_power_factor::text, actual_power_factor::text,
       power_factor_adjustment_cny::text, other_charges_cny::text, total_payable_cny::text,
       chiller_share_override_ratio, utility_account_no, invoice_no, note, status,
       confirmed_by::text, confirmed_by_display, confirmed_at,
       updated_by::text, updated_by_display, revision, created_at, updated_at
FROM core_registry.utility_bills
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid
FOR UPDATE
`, claims.TenantID, input.SiteID, input.BillID)
		current, err := scanUtilityBill(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return mutationRecord[UtilityBill]{}, ErrNotFound
		}
		if err != nil {
			return mutationRecord[UtilityBill]{}, err
		}
		if current.Status != UtilityBillStatusDraft {
			return mutationRecord[UtilityBill]{}, ErrUtilityBillStateConflict
		}
		if current.Revision != input.Meta.ExpectedRevision {
			return mutationRecord[UtilityBill]{}, ErrRevisionConflict
		}

		nextNote := current.Note
		if input.Note != nil {
			nextNote = strings.TrimSpace(*input.Note)
		}
		nextRevision := current.Revision + 1
		tag, err := tx.Exec(ctx, `
UPDATE core_registry.utility_bills
SET status = 'CONFIRMED',
    note = $4,
    confirmed_by = $5::uuid,
    confirmed_by_display = $6,
    confirmed_at = $7,
    updated_by = $5::uuid,
    updated_by_display = $6,
    revision = $8,
    updated_at = $7
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND revision = $9
`, claims.TenantID, input.SiteID, input.BillID, nextNote, claims.PrincipalID, actorDisplay, now, nextRevision, current.Revision)
		if err != nil {
			return mutationRecord[UtilityBill]{}, err
		}
		if tag.RowsAffected() != 1 {
			return mutationRecord[UtilityBill]{}, ErrRevisionConflict
		}

		confirmedBy := claims.PrincipalID
		confirmedAtText := formatInstant(now)
		current.Status = UtilityBillStatusConfirmed
		current.Note = nextNote
		current.ConfirmedBy = &confirmedBy
		current.ConfirmedByDisplay = &actorDisplay
		current.ConfirmedAt = &confirmedAtText
		current.UpdatedBy = claims.PrincipalID
		current.UpdatedByDisplay = actorDisplay
		current.Revision = nextRevision
		current.UpdatedAt = confirmedAtText
		siteCopy := input.SiteID

		return mutationRecord[UtilityBill]{
			Result:           current,
			SiteID:           &siteCopy,
			ResourceType:     "UTILITY_BILL",
			ResourceID:       current.ID,
			BeforeRevision:   revisionPointer(nextRevision - 1),
			AfterRevision:    revisionPointer(nextRevision),
			EventType:        "registry.utility-bill.confirmed",
			AggregateVersion: nextRevision,
			Payload: map[string]any{
				"siteId":          input.SiteID,
				"billingMonth":    current.BillingMonth,
				"meterScope":      current.MeterScope,
				"totalPayableCny": current.TotalPayableCny,
				"revision":        nextRevision,
			},
		}, nil
	})
}
