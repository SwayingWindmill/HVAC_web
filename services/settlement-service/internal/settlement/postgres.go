package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, errors.New("settlement postgres pool is required")
	}
	return &PostgresStore{pool: pool}, nil
}

func setScope(ctx context.Context, tx pgx.Tx, tenantID, siteID string) error {
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "SELECT set_config('app.authorized_site_ids',$1,true)", "{"+siteID+"}")
	return err
}

type pricingRule struct {
	EnergyRate float64 `json:"energyRatePerKWh"`
	DemandRate float64 `json:"demandRatePerKW"`
}

func (s *PostgresStore) LoadPeriod(ctx context.Context, tenantID, siteID, periodID string) (Period, []MetricBinding, Tariff, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Period{}, nil, Tariff{}, err
	}
	defer tx.Rollback(ctx)
	if err = setScope(ctx, tx, tenantID, siteID); err != nil {
		return Period{}, nil, Tariff{}, err
	}
	var period Period
	var tariff Tariff
	err = tx.QueryRow(ctx, `SELECT p.tenant_id::text,p.site_id::text,p.id::text,p.boundary_id::text,p.period_start,p.period_end,p.timezone,p.status,
 tv.tariff_id::text,tv.id::text,tv.version,tv.currency,tv.timezone
 FROM core_registry.settlement_periods p
 JOIN core_registry.tariff_assignments a ON a.tenant_id=p.tenant_id AND a.site_id=p.site_id AND a.boundary_id=p.boundary_id AND a.status='RELEASED' AND a.effective_from<=p.period_start AND (a.effective_to IS NULL OR a.effective_to>p.period_start)
 JOIN core_registry.tariff_versions tv ON tv.tenant_id=a.tenant_id AND tv.site_id=a.site_id AND tv.tariff_id=a.tariff_id AND tv.status='RELEASED' AND tv.effective_from<=p.period_start AND (tv.effective_to IS NULL OR tv.effective_to>p.period_start)
 WHERE p.tenant_id=$1::uuid AND p.site_id=$2::uuid AND p.id=$3::uuid`, tenantID, siteID, periodID).Scan(
		&period.TenantID, &period.SiteID, &period.ID, &period.BoundaryID, &period.Start, &period.End, &period.Timezone, &period.Status,
		&tariff.ID, &tariff.VersionID, &tariff.Version, &tariff.Currency, &tariff.Timezone,
	)
	if err != nil {
		return Period{}, nil, Tariff{}, fmt.Errorf("load settlement period: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT tp.id::text,tp.period_code,tp.day_type,tp.local_start_minute,tp.local_end_minute,tp.pricing_rule
FROM core_registry.tariff_periods tp
WHERE tp.tenant_id=$1::uuid AND tp.site_id=$2::uuid AND tp.tariff_version_id=$3::uuid
ORDER BY tp.day_type,tp.ordinal`, tenantID, siteID, tariff.VersionID)
	if err != nil {
		return Period{}, nil, Tariff{}, err
	}
	for rows.Next() {
		var slice TariffPeriod
		var raw []byte
		if err = rows.Scan(&slice.ID, &slice.Code, &slice.DayType, &slice.StartMinute, &slice.EndMinute, &raw); err != nil {
			rows.Close()
			return Period{}, nil, Tariff{}, err
		}
		var rule pricingRule
		if err = json.Unmarshal(raw, &rule); err != nil {
			rows.Close()
			return Period{}, nil, Tariff{}, fmt.Errorf("decode tariff pricing rule: %w", err)
		}
		slice.EnergyRate, slice.DemandRate = rule.EnergyRate, rule.DemandRate
		tariff.Periods = append(tariff.Periods, slice)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Period{}, nil, Tariff{}, err
	}
	if len(tariff.Periods) == 0 {
		return Period{}, nil, Tariff{}, errors.New("released tariff version has no tariff periods")
	}

	metricRows, err := tx.Query(ctx, `SELECT smb.id::text,smb.metric_binding_id::text,mb.metric_version_id::text,m.metric_code,smb.metric_role,coalesce(smb.tariff_period_code,'')
FROM core_registry.settlement_metric_bindings smb
JOIN core_registry.metric_bindings mb ON mb.tenant_id=smb.tenant_id AND mb.site_id=smb.site_id AND mb.id=smb.metric_binding_id AND mb.status='RELEASED'
JOIN core_registry.metric_versions mv ON mv.tenant_id=mb.tenant_id AND mv.id=mb.metric_version_id AND mv.status='RELEASED'
JOIN core_registry.metrics m ON m.tenant_id=mb.tenant_id AND m.id=mb.metric_id
WHERE smb.tenant_id=$1::uuid AND smb.site_id=$2::uuid AND smb.boundary_id=$3::uuid
  AND smb.status='RELEASED' AND smb.effective_from <= $4 AND (smb.effective_to IS NULL OR smb.effective_to > $4)
ORDER BY smb.metric_role,smb.tariff_period_code,smb.id`, tenantID, siteID, period.BoundaryID, period.Start)
	if err != nil {
		return Period{}, nil, Tariff{}, err
	}
	bindings := make([]MetricBinding, 0)
	for metricRows.Next() {
		var binding MetricBinding
		if err = metricRows.Scan(&binding.ID, &binding.MetricBindingID, &binding.MetricVersionID, &binding.MetricCode, &binding.Role, &binding.TariffPeriodCode); err != nil {
			metricRows.Close()
			return Period{}, nil, Tariff{}, err
		}
		bindings = append(bindings, binding)
	}
	metricRows.Close()
	if err = metricRows.Err(); err != nil {
		return Period{}, nil, Tariff{}, err
	}
	if len(bindings) == 0 {
		return Period{}, nil, Tariff{}, errors.New("settlement boundary has no released Metric bindings")
	}

	// Meter bindings remain lineage/evidence for the accounting boundary. The
	// Settlement calculation itself reads Metric results, not raw meter facts.
	meterRows, err := tx.Query(ctx, `SELECT mb.id::text
FROM core_registry.meter_bindings mb
JOIN core_registry.settlement_boundaries b ON b.tenant_id=mb.tenant_id AND b.site_id=mb.site_id AND b.topology_version_id=mb.topology_version_id
WHERE b.tenant_id=$1::uuid AND b.site_id=$2::uuid AND b.id=$3::uuid
  AND mb.meter_role='PRIMARY' AND mb.status IN ('RELEASED','ACTIVE')
  AND mb.effective_from<=$4 AND (mb.effective_to IS NULL OR mb.effective_to>$4)
  AND ((b.definition_mode='EDGE_SET' AND EXISTS (
      SELECT 1 FROM core_registry.settlement_boundary_edges be
      WHERE be.tenant_id=b.tenant_id AND be.site_id=b.site_id AND be.boundary_id=b.id AND be.energy_edge_id=mb.energy_edge_id
    )) OR (b.definition_mode='NODE' AND EXISTS (
      SELECT 1 FROM core_registry.energy_edges ee
      WHERE ee.tenant_id=b.tenant_id AND ee.site_id=b.site_id AND ee.topology_version_id=b.topology_version_id
        AND ee.id=mb.energy_edge_id AND (ee.from_node_id=b.node_id OR ee.to_node_id=b.node_id)
    )))
ORDER BY mb.id`, tenantID, siteID, period.BoundaryID, period.Start)
	if err != nil {
		return Period{}, nil, Tariff{}, err
	}
	for meterRows.Next() {
		var id string
		if err = meterRows.Scan(&id); err != nil {
			meterRows.Close()
			return Period{}, nil, Tariff{}, err
		}
		period.MeterBindingRefs = append(period.MeterBindingRefs, id)
	}
	meterRows.Close()
	if err = meterRows.Err(); err != nil {
		return Period{}, nil, Tariff{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Period{}, nil, Tariff{}, err
	}
	return period, bindings, tariff, nil
}

func (s *PostgresStore) TransitionPeriod(ctx context.Context, period Period, status string, at time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = setScope(ctx, tx, period.TenantID, period.SiteID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE core_registry.settlement_periods SET status=$4,updated_at=$5,revision=revision+1 WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid`, period.TenantID, period.SiteID, period.ID, status, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("settlement period transition rejected")
	}
	return tx.Commit(ctx)
}

func encode(value any) []byte { raw, _ := json.Marshal(value); return raw }

func (s *PostgresStore) InsertSnapshot(ctx context.Context, snapshot Snapshot) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	period := snapshot.Period
	if err = setScope(ctx, tx, period.TenantID, period.SiteID); err != nil {
		return err
	}
	var previous, revision any
	if snapshot.PreviousSnapshotID != "" {
		previous = snapshot.PreviousSnapshotID
	}
	if snapshot.SettlementRevisionID != "" {
		revision = snapshot.SettlementRevisionID
	}
	calc := snapshot.Calculation
	tariffs := []map[string]any{{"tariffVersionId": calc.TariffVersionID}}
	demand := map[string]any{"kwByPeriod": calc.DemandKW}
	cost := map[string]any{"currency": calc.Currency, "energyByPeriod": calc.EnergyCost, "demandByPeriod": calc.DemandCost, "total": calc.TotalCost}
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.settlement_snapshots(
id,tenant_id,site_id,settlement_period_id,boundary_id,revision_no,previous_snapshot_id,settlement_revision_id,
meter_binding_refs,metric_version_refs,tariff_version_refs,source_reading_refs,energy_breakdown,demand,cost,quality,completeness,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7::uuid,$8::uuid,$9::jsonb,$10::jsonb,$11::jsonb,$12::jsonb,$13::jsonb,$14::jsonb,$15::jsonb,$16,$17,$18)`,
		snapshot.ID, period.TenantID, period.SiteID, period.ID, period.BoundaryID, snapshot.RevisionNo, previous, revision,
		encode(calc.MeterRefs), encode(calc.MetricVersionRefs), encode(tariffs), encode(calc.SourceRefs), encode(calc.EnergyBreakdown), encode(demand), encode(cost), calc.Quality, calc.Completeness, snapshot.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert settlement snapshot: %w", err)
	}
	return tx.Commit(ctx)
}

const snapshotSelect = `SELECT s.id::text,s.revision_no,coalesce(s.previous_snapshot_id::text,''),coalesce(s.settlement_revision_id::text,''),s.created_at,
s.meter_binding_refs,s.metric_version_refs,s.tariff_version_refs,s.source_reading_refs,s.energy_breakdown,s.demand,s.cost,s.quality,s.completeness,
p.tenant_id::text,p.site_id::text,p.id::text,p.boundary_id::text,p.period_start,p.period_end,p.timezone,p.status`

type scanner interface{ Scan(...any) error }

func scanSnapshot(row scanner) (Snapshot, error) {
	var snapshot Snapshot
	var meters, metricVersions, tariffs, sources, energy, demand, cost []byte
	var completeness float64
	err := row.Scan(&snapshot.ID, &snapshot.RevisionNo, &snapshot.PreviousSnapshotID, &snapshot.SettlementRevisionID, &snapshot.CreatedAt,
		&meters, &metricVersions, &tariffs, &sources, &energy, &demand, &cost, &snapshot.Calculation.Quality, &completeness,
		&snapshot.Period.TenantID, &snapshot.Period.SiteID, &snapshot.Period.ID, &snapshot.Period.BoundaryID,
		&snapshot.Period.Start, &snapshot.Period.End, &snapshot.Period.Timezone, &snapshot.Period.Status)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.Calculation.Completeness = completeness
	_ = json.Unmarshal(meters, &snapshot.Calculation.MeterRefs)
	_ = json.Unmarshal(metricVersions, &snapshot.Calculation.MetricVersionRefs)
	_ = json.Unmarshal(sources, &snapshot.Calculation.SourceRefs)
	_ = json.Unmarshal(energy, &snapshot.Calculation.EnergyBreakdown)
	var tariffRefs []map[string]any
	_ = json.Unmarshal(tariffs, &tariffRefs)
	if len(tariffRefs) > 0 {
		snapshot.Calculation.TariffVersionID, _ = tariffRefs[0]["tariffVersionId"].(string)
	}
	var demandValue struct{ KW map[string]float64 `json:"kwByPeriod"` }
	_ = json.Unmarshal(demand, &demandValue)
	snapshot.Calculation.DemandKW = demandValue.KW
	var costValue struct {
		Currency string             `json:"currency"`
		Energy   map[string]float64 `json:"energyByPeriod"`
		Demand   map[string]float64 `json:"demandByPeriod"`
		Total    float64            `json:"total"`
	}
	_ = json.Unmarshal(cost, &costValue)
	snapshot.Calculation.Currency = costValue.Currency
	snapshot.Calculation.EnergyCost = costValue.Energy
	snapshot.Calculation.DemandCost = costValue.Demand
	snapshot.Calculation.TotalCost = costValue.Total
	return snapshot, nil
}

func (s *PostgresStore) LatestSnapshot(ctx context.Context, tenantID, siteID, periodID string) (Snapshot, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Snapshot{}, err
	}
	defer tx.Rollback(ctx)
	if err = setScope(ctx, tx, tenantID, siteID); err != nil {
		return Snapshot{}, err
	}
	query := snapshotSelect + ` FROM core_registry.settlement_snapshots s JOIN core_registry.settlement_periods p ON p.tenant_id=s.tenant_id AND p.site_id=s.site_id AND p.id=s.settlement_period_id WHERE s.tenant_id=$1::uuid AND s.site_id=$2::uuid AND s.settlement_period_id=$3::uuid ORDER BY s.revision_no DESC LIMIT 1`
	snapshot, err := scanSnapshot(tx.QueryRow(ctx, query, tenantID, siteID, periodID))
	if err != nil {
		return Snapshot{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (s *PostgresStore) CreateCandidate(ctx context.Context, candidate Candidate, at time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = setScope(ctx, tx, candidate.TenantID, candidate.SiteID); err != nil {
		return err
	}
	impact := map[string]any{"recalculatedTotalCost": candidate.Calculation.TotalCost, "currency": candidate.Calculation.Currency}
	evidence := map[string]any{"baseSnapshotId": candidate.BaseSnapshotID, "recalculated": candidate.Calculation}
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.settlement_change_candidates(id,tenant_id,site_id,settlement_period_id,reason_code,impact_summary,evidence,status,detected_at,revision,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::jsonb,$7::jsonb,'OPEN',$8,1,$8,$8)`, candidate.ID, candidate.TenantID, candidate.SiteID, candidate.PeriodID, candidate.Reason, encode(impact), encode(evidence), at)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) ApproveCandidate(ctx context.Context, tenantID, siteID, candidateID string, at time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = setScope(ctx, tx, tenantID, siteID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE core_registry.settlement_change_candidates SET status='APPROVED',resolved_at=$4,updated_at=$4,revision=revision+1 WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status='OPEN'`, tenantID, siteID, candidateID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("settlement candidate approval rejected")
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) LoadApprovedCandidate(ctx context.Context, tenantID, siteID, candidateID string) (Candidate, Snapshot, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Candidate{}, Snapshot{}, err
	}
	defer tx.Rollback(ctx)
	if err = setScope(ctx, tx, tenantID, siteID); err != nil {
		return Candidate{}, Snapshot{}, err
	}
	var candidate Candidate
	var evidence []byte
	err = tx.QueryRow(ctx, `SELECT id::text,tenant_id::text,site_id::text,settlement_period_id::text,reason_code,evidence FROM core_registry.settlement_change_candidates WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status='APPROVED'`, tenantID, siteID, candidateID).Scan(&candidate.ID, &candidate.TenantID, &candidate.SiteID, &candidate.PeriodID, &candidate.Reason, &evidence)
	if err != nil {
		return Candidate{}, Snapshot{}, err
	}
	var envelope struct {
		BaseSnapshotID string      `json:"baseSnapshotId"`
		Recalculated   Calculation `json:"recalculated"`
	}
	if err = json.Unmarshal(evidence, &envelope); err != nil {
		return Candidate{}, Snapshot{}, err
	}
	candidate.BaseSnapshotID, candidate.Calculation = envelope.BaseSnapshotID, envelope.Recalculated
	query := snapshotSelect + ` FROM core_registry.settlement_snapshots s JOIN core_registry.settlement_periods p ON p.tenant_id=s.tenant_id AND p.site_id=s.site_id AND p.id=s.settlement_period_id WHERE s.tenant_id=$1::uuid AND s.site_id=$2::uuid AND s.id=$3::uuid`
	base, err := scanSnapshot(tx.QueryRow(ctx, query, tenantID, siteID, candidate.BaseSnapshotID))
	if err != nil {
		return Candidate{}, Snapshot{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Candidate{}, Snapshot{}, err
	}
	return candidate, base, nil
}

func (s *PostgresStore) ApplyRevision(ctx context.Context, candidate Candidate, base, next Snapshot, at time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = setScope(ctx, tx, candidate.TenantID, candidate.SiteID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.settlement_revisions(id,tenant_id,site_id,settlement_period_id,revision_no,change_candidate_id,base_snapshot_id,reason,status,revision,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7::uuid,$8,'DRAFT',1,$9,$9)`, next.SettlementRevisionID, candidate.TenantID, candidate.SiteID, candidate.PeriodID, next.RevisionNo, candidate.ID, base.ID, candidate.Reason, at)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE core_registry.settlement_revisions SET status='APPROVED',approved_at=$2,updated_at=$2,revision=revision+1 WHERE id=$1::uuid AND status='DRAFT'`, next.SettlementRevisionID, at)
	if err != nil {
		return err
	}
	calc := next.Calculation
	tariffs := []map[string]any{{"tariffVersionId": calc.TariffVersionID}}
	demand := map[string]any{"kwByPeriod": calc.DemandKW}
	cost := map[string]any{"currency": calc.Currency, "energyByPeriod": calc.EnergyCost, "demandByPeriod": calc.DemandCost, "total": calc.TotalCost}
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.settlement_snapshots(
id,tenant_id,site_id,settlement_period_id,boundary_id,revision_no,previous_snapshot_id,settlement_revision_id,meter_binding_refs,metric_version_refs,tariff_version_refs,source_reading_refs,energy_breakdown,demand,cost,quality,completeness,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7::uuid,$8::uuid,$9::jsonb,$10::jsonb,$11::jsonb,$12::jsonb,$13::jsonb,$14::jsonb,$15::jsonb,$16,$17,$18)`,
		next.ID, candidate.TenantID, candidate.SiteID, candidate.PeriodID, base.Period.BoundaryID, next.RevisionNo, next.PreviousSnapshotID, next.SettlementRevisionID,
		encode(calc.MeterRefs), encode(calc.MetricVersionRefs), encode(tariffs), encode(calc.SourceRefs), encode(calc.EnergyBreakdown), encode(demand), encode(cost), calc.Quality, calc.Completeness, at)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE core_registry.settlement_change_candidates SET status='APPLIED',resolved_at=$4,updated_at=$4,revision=revision+1 WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status='APPROVED'`, candidate.TenantID, candidate.SiteID, candidate.ID, at)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE core_registry.settlement_periods SET status='REVISED',updated_at=$4,revision=revision+1 WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status IN ('LOCKED','REVISED')`, candidate.TenantID, candidate.SiteID, candidate.PeriodID, at)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
