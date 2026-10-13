package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
	"github.com/quanlaihe/hvac-web/libs/tariffmodel"
)

// PostgresStore keeps report snapshots in core_registry as report_runtime, scoped by Tenant and
// Site row security, and reads what a report draws from the Registry: the Site's clock and import
// Tariff, its verified savings and its FDD findings.
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

func (store *PostgresStore) GenerationQueue() (*schedulerjob.Queue, error) {
	return schedulerjob.NewQueue(store.pool, GenerationJobType)
}

// scoped runs work in a transaction bound to one Tenant and Site.
func (store *PostgresStore) scoped(ctx context.Context, tenantID, siteID string, readOnly bool, work func(pgx.Tx) error) error {
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

func (store *PostgresStore) SiteLocation(ctx context.Context, tenantID, siteID string) (*time.Location, error) {
	var timezone string
	err := store.scoped(ctx, tenantID, siteID, true, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT timezone FROM core_registry.sites WHERE tenant_id = $1::uuid AND id = $2::uuid`, tenantID, siteID).Scan(&timezone)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSiteNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read Site timezone: %w", err)
	}
	return time.LoadLocation(timezone)
}

// Tariffs is each import Tariff Version priced on the Site's grid connection during the window,
// over the part of it where both the version and its assignment are in effect; a Site with several
// import connections is priced on the first by boundary code, as M&V and the Metric engine price it.
func (store *PostgresStore) Tariffs(ctx context.Context, tenantID, siteID string, from, to time.Time) ([]Tariff, error) {
	var tariffs []Tariff
	err := store.scoped(ctx, tenantID, siteID, true, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT boundary.boundary_code,
       version.id::text,
       GREATEST(version.effective_from, assignment.effective_from),
       LEAST(version.effective_to, assignment.effective_to),
       version.timezone
FROM core_registry.tariff_assignments AS assignment
JOIN core_registry.settlement_boundaries AS boundary
  ON boundary.tenant_id = assignment.tenant_id AND boundary.site_id = assignment.site_id AND boundary.id = assignment.boundary_id
JOIN core_registry.tariff_versions AS version
  ON version.tenant_id = assignment.tenant_id AND version.site_id = assignment.site_id AND version.tariff_id = assignment.tariff_id
WHERE assignment.tenant_id = $1::uuid AND assignment.site_id = $2::uuid
  AND assignment.status = 'RELEASED' AND assignment.effective_from < $4 AND (assignment.effective_to IS NULL OR assignment.effective_to > $3)
  AND boundary.status = 'ACTIVE' AND boundary.boundary_type = 'GRID_CONNECTION' AND boundary.direction = 'IMPORT'
  AND version.status = 'RELEASED' AND version.effective_from < $4 AND (version.effective_to IS NULL OR version.effective_to > $3)
ORDER BY 1, 3`, tenantID, siteID, from.UTC(), to.UTC())
		if err != nil {
			return err
		}
		firstBoundary := ""
		for rows.Next() {
			var tariff Tariff
			var boundaryCode, timezone string
			if err := rows.Scan(&boundaryCode, &tariff.VersionID, &tariff.EffectiveFrom, &tariff.EffectiveTo, &timezone); err != nil {
				rows.Close()
				return err
			}
			if firstBoundary == "" {
				firstBoundary = boundaryCode
			}
			if boundaryCode != firstBoundary {
				continue
			}
			if tariff.Location, err = time.LoadLocation(timezone); err != nil {
				rows.Close()
				return err
			}
			tariffs = append(tariffs, tariff)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for index := range tariffs {
			periods, err := tx.Query(ctx, `SELECT period_code, day_type, local_start_minute, local_end_minute, (pricing_rule->>'energyRatePerKWh')::float8
FROM core_registry.tariff_periods WHERE tariff_version_id = $1::uuid ORDER BY day_type, local_start_minute`, tariffs[index].VersionID)
			if err != nil {
				return err
			}
			for periods.Next() {
				var period tariffmodel.Period
				if err := periods.Scan(&period.Code, &period.DayType, &period.StartMinute, &period.EndMinute, &period.EnergyRatePerKWh); err != nil {
					periods.Close()
					return err
				}
				tariffs[index].Periods = append(tariffs[index].Periods, period)
			}
			periods.Close()
			if err := periods.Err(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read import Tariffs: %w", err)
	}
	return tariffs, nil
}

// SavingsDays is every verified day of the month, from Site-local date from to date to (exclusive).
func (store *PostgresStore) SavingsDays(ctx context.Context, tenantID, siteID string, from, to string) ([]SavingsDay, error) {
	var days []SavingsDay
	err := store.scoped(ctx, tenantID, siteID, true, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT adjusted_baseline_kwh, avoided_kwh, avoided_cost, unpriced_hours
FROM core_registry.savings_daily_results
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND day >= $3::date AND day < $4::date
ORDER BY day`, tenantID, siteID, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var day SavingsDay
			if err := rows.Scan(&day.AdjustedBaselineKWh, &day.AvoidedKWh, &day.AvoidedCost, &day.UnpricedHours); err != nil {
				return err
			}
			days = append(days, day)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("read verified savings: %w", err)
	}
	return days, nil
}

// FindingCounts counts the FDD findings whose evaluation window ends in the month, by type.
func (store *PostgresStore) FindingCounts(ctx context.Context, tenantID, siteID string, from, to time.Time) ([]FindingCount, error) {
	var counts []FindingCount
	err := store.scoped(ctx, tenantID, siteID, true, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT finding_type, count(*)
FROM core_registry.fdd_findings
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND evaluation_to >= $3 AND evaluation_to < $4
GROUP BY finding_type ORDER BY count(*) DESC, finding_type`, tenantID, siteID, from.UTC(), to.UTC())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var count FindingCount
			if err := rows.Scan(&count.FindingType, &count.Count); err != nil {
				return err
			}
			counts = append(counts, count)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("read FDD findings: %w", err)
	}
	return counts, nil
}

// InsertSnapshot stores a month's snapshot as the next version of that month.
func (store *PostgresStore) InsertSnapshot(ctx context.Context, tenantID, siteID string, stored StoredSnapshot) (StoredSnapshot, error) {
	payload, err := json.Marshal(stored.Report)
	if err != nil {
		return StoredSnapshot{}, err
	}
	err = store.scoped(ctx, tenantID, siteID, false, func(tx pgx.Tx) error {
		// One generation per Site and month at a time; the next version follows the last.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, siteID+"/"+stored.Month); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(version), 0) + 1 FROM core_registry.report_snapshots
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND month = $3`, tenantID, siteID, stored.Month).Scan(&stored.Version); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO core_registry.report_snapshots (id, tenant_id, site_id, month, version, payload, scheduler_job_id, generated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::jsonb, $7::uuid, $8)`,
			stored.ID, tenantID, siteID, stored.Month, stored.Version, payload, stored.SchedulerJobID, stored.GeneratedAt.UTC())
		return err
	})
	if err != nil {
		return StoredSnapshot{}, fmt.Errorf("store report snapshot: %w", err)
	}
	return stored, nil
}

const snapshotColumns = `id::text, month, version, payload, scheduler_job_id::text, generated_at`

// LatestSnapshot is the newest version of a month's snapshot.
func (store *PostgresStore) LatestSnapshot(ctx context.Context, tenantID, siteID, month string) (StoredSnapshot, error) {
	var stored StoredSnapshot
	err := store.scoped(ctx, tenantID, siteID, true, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+snapshotColumns+` FROM core_registry.report_snapshots
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND month = $3 ORDER BY version DESC LIMIT 1`, tenantID, siteID, month)
		var scanErr error
		stored, scanErr = scanSnapshot(row)
		return scanErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredSnapshot{}, ErrSnapshotNotFound
	}
	if err != nil {
		return StoredSnapshot{}, fmt.Errorf("read report snapshot: %w", err)
	}
	return stored, nil
}

// YearSnapshots is the newest version of each snapshotted month of a year, in month order.
func (store *PostgresStore) YearSnapshots(ctx context.Context, tenantID, siteID string, year int) ([]StoredSnapshot, error) {
	snapshots := []StoredSnapshot{}
	err := store.scoped(ctx, tenantID, siteID, true, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT ON (month) `+snapshotColumns+` FROM core_registry.report_snapshots
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND month LIKE $3
ORDER BY month, version DESC`, tenantID, siteID, fmt.Sprintf("%04d-%%", year))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			stored, err := scanSnapshot(rows)
			if err != nil {
				return err
			}
			snapshots = append(snapshots, stored)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("read report snapshots: %w", err)
	}
	return snapshots, nil
}

func scanSnapshot(row pgx.Row) (StoredSnapshot, error) {
	var stored StoredSnapshot
	var payload []byte
	if err := row.Scan(&stored.ID, &stored.Month, &stored.Version, &payload, &stored.SchedulerJobID, &stored.GeneratedAt); err != nil {
		return StoredSnapshot{}, err
	}
	if err := json.Unmarshal(payload, &stored.Report); err != nil {
		return StoredSnapshot{}, fmt.Errorf("decode report snapshot %s: %w", stored.ID, err)
	}
	stored.GeneratedAt = stored.GeneratedAt.UTC()
	return stored, nil
}
