package fdd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

var ErrFindingNotFound = errors.New("FDD finding not found")

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

func (store *PostgresStore) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func fddScope(ctx context.Context, tx pgx.Tx, tenantID, siteID string) error {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) {
		return errors.New("FDD scope must contain valid tenant and site UUIDs")
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "SELECT set_config('app.authorized_site_ids',$1,true)", "{"+siteID+"}")
	return err
}

func (store *PostgresStore) CurrentRule(ctx context.Context, tenantID, siteID string, at time.Time) (RuleRevision, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return RuleRevision{}, err
	}
	defer tx.Rollback(ctx)
	if err = fddScope(ctx, tx, tenantID, siteID); err != nil {
		return RuleRevision{}, err
	}
	var rule RuleRevision
	err = tx.QueryRow(ctx, `SELECT id::text, revision, minimum_delta_tc, window_minutes, effective_from
FROM core_registry.fdd_rule_revisions
WHERE tenant_id = $1::uuid AND rule_code = $2 AND effective_from <= $3
ORDER BY revision DESC LIMIT 1`, tenantID, LowDeltaTRuleCode, at.UTC()).Scan(&rule.ID, &rule.Revision, &rule.MinimumDeltaTC, &rule.WindowMinutes, &rule.EffectiveFrom)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuleRevision{}, ErrRuleNotFound
	}
	if err != nil {
		return RuleRevision{}, fmt.Errorf("read FDD rule revision: %w", err)
	}
	return rule, tx.Commit(ctx)
}

func (store *PostgresStore) InsertFinding(ctx context.Context, finding intelligencemodel.FDDFinding) error {
	if store == nil || store.pool == nil {
		return errors.New("FDD postgres store is unavailable")
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = fddScope(ctx, tx, finding.TenantID, finding.SiteID); err != nil {
		return err
	}
	var modelDeployment any
	if finding.ModelDeploymentID != "" {
		modelDeployment = finding.ModelDeploymentID
	}
	var ruleRevision any
	if finding.RuleRevisionID != "" {
		ruleRevision = finding.RuleRevisionID
	}
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.fdd_findings(
id,tenant_id,site_id,asset_id,finding_type,evaluation_from,evaluation_to,evidence_ids,observed_value,threshold_value,value_unit,model_deployment_revision_id,rule_revision_id,confidence,quality_blocker,alarm_id,work_order_id,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12::uuid,$13,$14,NULLIF($15,''),NULL,NULL,$16)
ON CONFLICT (tenant_id,site_id,id) DO NOTHING`, finding.ID, finding.TenantID, finding.SiteID, finding.AssetID, finding.FindingType,
		finding.EvaluationFrom.UTC(), finding.EvaluationTo.UTC(), finding.EvidenceIDs, finding.ObservedValue, finding.ThresholdValue, finding.ValueUnit, modelDeployment, ruleRevision, finding.Confidence, finding.QualityBlocker, finding.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("insert FDD finding: %w", err)
	}
	return tx.Commit(ctx)
}

const findingColumns = `id::text,tenant_id::text,site_id::text,asset_id::text,finding_type,evaluation_from,evaluation_to,evidence_ids,
observed_value,threshold_value,value_unit,
COALESCE(model_deployment_revision_id::text,''),COALESCE(rule_revision_id,''),confidence,COALESCE(quality_blocker,''),COALESCE(alarm_id::text,''),COALESCE(work_order_id::text,''),created_at`

func scanFinding(row pgx.Row) (intelligencemodel.FDDFinding, error) {
	var finding intelligencemodel.FDDFinding
	err := row.Scan(&finding.ID, &finding.TenantID, &finding.SiteID, &finding.AssetID, &finding.FindingType,
		&finding.EvaluationFrom, &finding.EvaluationTo, &finding.EvidenceIDs, &finding.ObservedValue, &finding.ThresholdValue, &finding.ValueUnit,
		&finding.ModelDeploymentID, &finding.RuleRevisionID, &finding.Confidence, &finding.QualityBlocker, &finding.AlarmID, &finding.WorkOrderID, &finding.CreatedAt)
	return finding, err
}

func (store *PostgresStore) RecentFinding(ctx context.Context, tenantID, siteID, assetID, findingType, ruleRevisionID string, since time.Time) (intelligencemodel.FDDFinding, bool, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return intelligencemodel.FDDFinding{}, false, err
	}
	defer tx.Rollback(ctx)
	if err = fddScope(ctx, tx, tenantID, siteID); err != nil {
		return intelligencemodel.FDDFinding{}, false, err
	}
	finding, err := scanFinding(tx.QueryRow(ctx, `SELECT `+findingColumns+`
FROM core_registry.fdd_findings
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND asset_id=$3::uuid AND finding_type=$4 AND rule_revision_id=$5 AND evaluation_to >= $6
ORDER BY evaluation_to DESC,created_at DESC
LIMIT 1`, tenantID, siteID, assetID, findingType, ruleRevisionID, since.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return intelligencemodel.FDDFinding{}, false, tx.Commit(ctx)
	}
	if err != nil {
		return intelligencemodel.FDDFinding{}, false, err
	}
	return finding, true, tx.Commit(ctx)
}

func (store *PostgresStore) ListFindings(ctx context.Context, tenantID, siteID string, filter FindingFilter) ([]intelligencemodel.FDDFinding, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = fddScope(ctx, tx, tenantID, siteID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+findingColumns+`
FROM core_registry.fdd_findings
WHERE tenant_id=$1::uuid AND site_id=$2::uuid
  AND ($3='' OR alarm_id=$3::uuid)
  AND ($4='' OR work_order_id=$4::uuid)
ORDER BY evaluation_to DESC,created_at DESC
LIMIT $5`, tenantID, siteID, filter.AlarmID, filter.WorkOrderID, filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	findings := make([]intelligencemodel.FDDFinding, 0, filter.Limit)
	for rows.Next() {
		finding, err := scanFinding(rows)
		if err != nil {
			return nil, err
		}
		findings = append(findings, finding)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return findings, nil
}

func (store *PostgresStore) LinkFinding(ctx context.Context, tenantID, siteID, findingID, alarmID, workOrderID string, at time.Time) (intelligencemodel.FDDFinding, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return intelligencemodel.FDDFinding{}, err
	}
	defer tx.Rollback(ctx)
	if err = fddScope(ctx, tx, tenantID, siteID); err != nil {
		return intelligencemodel.FDDFinding{}, err
	}
	var currentAlarmID, currentWorkOrderID string
	err = tx.QueryRow(ctx, `SELECT COALESCE(alarm_id::text,''),COALESCE(work_order_id::text,'')
FROM core_registry.fdd_findings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid FOR UPDATE`, tenantID, siteID, findingID).Scan(&currentAlarmID, &currentWorkOrderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return intelligencemodel.FDDFinding{}, ErrFindingNotFound
	}
	if err != nil {
		return intelligencemodel.FDDFinding{}, err
	}
	if alarmID != "" && currentAlarmID != "" && currentAlarmID != alarmID {
		return intelligencemodel.FDDFinding{}, errors.New("FDD finding is already linked to another Alarm")
	}
	if workOrderID != "" && currentWorkOrderID != "" && currentWorkOrderID != workOrderID {
		return intelligencemodel.FDDFinding{}, errors.New("FDD finding is already linked to another Work Order")
	}
	_, err = tx.Exec(ctx, `UPDATE core_registry.fdd_findings SET
alarm_id=CASE WHEN $4='' THEN alarm_id ELSE $4::uuid END,
work_order_id=CASE WHEN $5='' THEN work_order_id ELSE $5::uuid END
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid`, tenantID, siteID, findingID, alarmID, workOrderID)
	if err != nil {
		return intelligencemodel.FDDFinding{}, err
	}
	finding, err := scanFinding(tx.QueryRow(ctx, `SELECT `+findingColumns+`
FROM core_registry.fdd_findings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid`, tenantID, siteID, findingID))
	if err != nil {
		return intelligencemodel.FDDFinding{}, err
	}
	_ = at // Link target mutation is captured by linked IDs; source finding evidence remains immutable.
	if err = tx.Commit(ctx); err != nil {
		return intelligencemodel.FDDFinding{}, err
	}
	return finding, nil
}
