package mv

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
	"github.com/quanlaihe/hvac-web/libs/tariffmodel"
)

// PostgresStore keeps measures and verified days in core_registry as mv_runtime, scoped by
// Tenant and Site row security; it also reads the Site's clock and import Tariff.
type PostgresStore struct{ pool *pgxpool.Pool }

func OpenPostgres(ctx context.Context, dsn string) (*PostgresStore, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &PostgresStore{pool: pool}, nil
}

func (store *PostgresStore) Close() { store.pool.Close() }

func (store *PostgresStore) EvaluationQueue() (*schedulerjob.Queue, error) {
	return schedulerjob.NewQueue(store.pool, EvaluationJobType)
}

// scoped runs work in a transaction bound to one Tenant and Site.
func (store *PostgresStore) scoped(ctx context.Context, tenantID, siteID string, readOnly bool, work func(pgx.Tx) error) error {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) {
		return errors.New("savings scope must contain valid tenant and site UUIDs")
	}
	options := pgx.TxOptions{}
	if readOnly {
		options.AccessMode = pgx.ReadOnly
	}
	tx, err := store.pool.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('app.tenant_id',$1,true), set_config('app.authorized_site_ids',$2,true)`, tenantID, "{"+siteID+"}"); err != nil {
		return err
	}
	if err = work(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const measureColumns = `id::text, tenant_id::text, site_id::text, name, kind, description, evidence, evidence_command_id::text, planned_at, implemented_at, ended_at,
investment_cny, expected_annual_kwh, status, baseline_from, baseline_to, baseline_intercept_kwh, baseline_slope, baseline_samples,
baseline_expected_hours, baseline_cv_rmse, baseline_nmbe, baseline_trusted, baseline_reason, created_by::text, updated_by::text, created_at, updated_at`

func scanMeasure(row pgx.Row) (Measure, error) {
	var measure Measure
	var kind, status string
	var from, to *time.Time
	var intercept, slope, cvrmse, nmbe *float64
	var samples, expected *int
	var trusted *bool
	var reason string
	err := row.Scan(&measure.ID, &measure.TenantID, &measure.SiteID, &measure.Name, &kind, &measure.Description, &measure.Evidence, &measure.EvidenceCommandID,
		&measure.PlannedAt, &measure.ImplementedAt, &measure.EndedAt, &measure.InvestmentCNY, &measure.ExpectedAnnualKWh, &status,
		&from, &to, &intercept, &slope, &samples, &expected, &cvrmse, &nmbe, &trusted, &reason,
		&measure.CreatedBy, &measure.UpdatedBy, &measure.CreatedAt, &measure.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Measure{}, ErrMeasureNotFound
	}
	if err != nil {
		return Measure{}, err
	}
	measure.Kind, measure.Status = Kind(kind), Status(status)
	if trusted != nil {
		measure.Baseline = &Baseline{
			From: *from, To: *to, Intercept: *intercept, Slope: *slope, Samples: *samples, ExpectedHours: *expected,
			CVRMSE: *cvrmse, NMBE: *nmbe, Trusted: *trusted, Reason: reason,
		}
	}
	return measure, nil
}

// baselineArgs flattens a measure's baseline into its nullable columns.
func baselineArgs(baseline *Baseline) []any {
	if baseline == nil {
		return []any{nil, nil, nil, nil, nil, nil, nil, nil, nil, ""}
	}
	return []any{baseline.From, baseline.To, baseline.Intercept, baseline.Slope, baseline.Samples, baseline.ExpectedHours,
		baseline.CVRMSE, baseline.NMBE, baseline.Trusted, baseline.Reason}
}

func overlapError(err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23P01" {
		return ErrReportingOverlap
	}
	return err
}

func (store *PostgresStore) InsertMeasure(ctx context.Context, measure Measure) error {
	return store.scoped(ctx, measure.TenantID, measure.SiteID, false, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO core_registry.savings_measures(id, tenant_id, site_id, name, kind, description, evidence, evidence_command_id,
planned_at, investment_cny, expected_annual_kwh, status, created_by, updated_by, created_at, updated_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8::uuid,$9,$10,$11,$12,$13::uuid,$14::uuid,$15,$16)`,
			measure.ID, measure.TenantID, measure.SiteID, measure.Name, string(measure.Kind), measure.Description, measure.Evidence, measure.EvidenceCommandID,
			measure.PlannedAt, measure.InvestmentCNY, measure.ExpectedAnnualKWh, string(measure.Status), measure.CreatedBy, measure.UpdatedBy, measure.CreatedAt, measure.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert savings measure: %w", err)
		}
		return nil
	})
}

func (store *PostgresStore) UpdateMeasure(ctx context.Context, measure Measure) error {
	return store.scoped(ctx, measure.TenantID, measure.SiteID, false, func(tx pgx.Tx) error {
		args := append([]any{measure.ID, measure.TenantID, measure.SiteID, measure.ImplementedAt, measure.EndedAt, string(measure.Status),
			measure.UpdatedBy, measure.UpdatedAt}, baselineArgs(measure.Baseline)...)
		tag, err := tx.Exec(ctx, `UPDATE core_registry.savings_measures SET
implemented_at=$4, ended_at=$5, status=$6, updated_by=$7::uuid, updated_at=$8,
baseline_from=$9, baseline_to=$10, baseline_intercept_kwh=$11, baseline_slope=$12, baseline_samples=$13, baseline_expected_hours=$14,
baseline_cv_rmse=$15, baseline_nmbe=$16, baseline_trusted=$17, baseline_reason=$18
WHERE tenant_id=$2::uuid AND site_id=$3::uuid AND id=$1::uuid`, args...)
		if err != nil {
			return overlapError(fmt.Errorf("update savings measure: %w", err))
		}
		if tag.RowsAffected() != 1 {
			return ErrMeasureNotFound
		}
		return nil
	})
}

func (store *PostgresStore) Measure(ctx context.Context, tenantID, siteID, id string) (Measure, error) {
	if !uuidPattern.MatchString(id) {
		return Measure{}, ErrMeasureNotFound
	}
	var measure Measure
	err := store.scoped(ctx, tenantID, siteID, true, func(tx pgx.Tx) error {
		var err error
		measure, err = scanMeasure(tx.QueryRow(ctx, `SELECT `+measureColumns+` FROM core_registry.savings_measures
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid`, tenantID, siteID, id))
		return err
	})
	return measure, err
}

func (store *PostgresStore) Measures(ctx context.Context, tenantID, siteID string) ([]Measure, error) {
	measures := []Measure{}
	err := store.scoped(ctx, tenantID, siteID, true, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+measureColumns+` FROM core_registry.savings_measures
WHERE tenant_id=$1::uuid AND site_id=$2::uuid ORDER BY planned_at DESC, id DESC LIMIT 200`, tenantID, siteID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			measure, err := scanMeasure(rows)
			if err != nil {
				return err
			}
			measures = append(measures, measure)
		}
		return rows.Err()
	})
	return measures, err
}

func (store *PostgresStore) InsertDailyResult(ctx context.Context, result DailyResult) error {
	return store.scoped(ctx, result.TenantID, result.SiteID, false, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO core_registry.savings_daily_results(tenant_id, site_id, measure_id, day, expected_hours, hours, unpriced_hours,
adjusted_baseline_kwh, actual_kwh, avoided_kwh, avoided_cost, verified_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::date,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (tenant_id, site_id, measure_id, day) DO NOTHING`,
			result.TenantID, result.SiteID, result.MeasureID, result.Day, result.ExpectedHours, result.Hours, result.UnpricedHours,
			result.AdjustedBaselineKWh, result.ActualKWh, result.AvoidedKWh, result.AvoidedCost, result.VerifiedAt)
		if err != nil {
			return fmt.Errorf("insert savings day: %w", err)
		}
		return nil
	})
}

func (store *PostgresStore) DailyResults(ctx context.Context, tenantID, siteID, measureID string) ([]DailyResult, error) {
	results := []DailyResult{}
	err := store.scoped(ctx, tenantID, siteID, true, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT measure_id::text, to_char(day, 'YYYY-MM-DD'), expected_hours, hours, unpriced_hours,
adjusted_baseline_kwh, actual_kwh, avoided_kwh, avoided_cost, verified_at
FROM core_registry.savings_daily_results WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND measure_id=$3::uuid ORDER BY day`, tenantID, siteID, measureID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			result := DailyResult{TenantID: tenantID, SiteID: siteID}
			if err := rows.Scan(&result.MeasureID, &result.Day, &result.ExpectedHours, &result.Hours, &result.UnpricedHours,
				&result.AdjustedBaselineKWh, &result.ActualKWh, &result.AvoidedKWh, &result.AvoidedCost, &result.VerifiedAt); err != nil {
				return err
			}
			results = append(results, result)
		}
		return rows.Err()
	})
	return results, err
}

// SiteClock reads the Site's timezone and the released Tariff Version assigned to the Site's grid
// import now, with the version's own timezone, choosing it the way Settlement does.
func (store *PostgresStore) SiteClock(ctx context.Context, tenantID, siteID string) (SiteClock, error) {
	var clock SiteClock
	err := store.scoped(ctx, tenantID, siteID, true, func(tx pgx.Tx) error {
		var timezone string
		if err := tx.QueryRow(ctx, `SELECT timezone FROM core_registry.sites WHERE tenant_id = $1::uuid AND id = $2::uuid`, tenantID, siteID).Scan(&timezone); err != nil {
			return fmt.Errorf("read Site timezone: %w", err)
		}
		location, err := time.LoadLocation(timezone)
		if err != nil {
			return err
		}
		clock.Location = location
		var versionID, versionTimezone string
		err = tx.QueryRow(ctx, `
SELECT version.id::text, version.timezone
FROM core_registry.tariff_assignments AS assignment
JOIN core_registry.settlement_boundaries AS boundary
  ON boundary.tenant_id = assignment.tenant_id AND boundary.site_id = assignment.site_id AND boundary.id = assignment.boundary_id
JOIN core_registry.tariff_versions AS version
  ON version.tenant_id = assignment.tenant_id AND version.site_id = assignment.site_id AND version.tariff_id = assignment.tariff_id
WHERE assignment.tenant_id = $1::uuid AND assignment.site_id = $2::uuid
  AND assignment.status = 'RELEASED' AND assignment.effective_from <= now() AND (assignment.effective_to IS NULL OR assignment.effective_to > now())
  AND boundary.status = 'ACTIVE' AND boundary.boundary_type = 'GRID_CONNECTION' AND boundary.direction = 'IMPORT'
  AND version.status = 'RELEASED' AND version.effective_from <= now() AND (version.effective_to IS NULL OR version.effective_to > now())
ORDER BY boundary.boundary_code
LIMIT 1`, tenantID, siteID).Scan(&versionID, &versionTimezone)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		tariffLocation, err := time.LoadLocation(versionTimezone)
		if err != nil {
			return err
		}
		clock.Tariff = &ImportTariff{Location: tariffLocation}
		rows, err := tx.Query(ctx, `SELECT day_type, local_start_minute, local_end_minute, (pricing_rule->>'energyRatePerKWh')::float8
FROM core_registry.tariff_periods WHERE tariff_version_id = $1::uuid ORDER BY day_type, local_start_minute`, versionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var period tariffmodel.Period
			if err := rows.Scan(&period.DayType, &period.StartMinute, &period.EndMinute, &period.EnergyRatePerKWh); err != nil {
				return err
			}
			clock.Tariff.Periods = append(clock.Tariff.Periods, period)
		}
		return rows.Err()
	})
	return clock, err
}
