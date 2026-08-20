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
id,tenant_id,site_id,asset_id,finding_type,evaluation_from,evaluation_to,evidence_ids,model_deployment_revision_id,rule_revision_id,confidence,quality_blocker,alarm_id,work_order_id,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9::uuid,$10,$11,NULLIF($12,''),NULL,NULL,$13)
ON CONFLICT (tenant_id,site_id,id) DO NOTHING`, finding.ID, finding.TenantID, finding.SiteID, finding.AssetID, finding.FindingType,
		finding.EvaluationFrom.UTC(), finding.EvaluationTo.UTC(), finding.EvidenceIDs, modelDeployment, ruleRevision, finding.Confidence, finding.QualityBlocker, finding.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("insert FDD finding: %w", err)
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) ListFindings(ctx context.Context, tenantID, siteID string, limit int) ([]intelligencemodel.FDDFinding, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = fddScope(ctx, tx, tenantID, siteID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id::text,tenant_id::text,site_id::text,asset_id::text,finding_type,evaluation_from,evaluation_to,evidence_ids,
COALESCE(model_deployment_revision_id::text,''),COALESCE(rule_revision_id,''),confidence,COALESCE(quality_blocker,''),COALESCE(alarm_id::text,''),COALESCE(work_order_id::text,''),created_at
FROM core_registry.fdd_findings
WHERE tenant_id=$1::uuid AND site_id=$2::uuid
ORDER BY evaluation_to DESC,created_at DESC
LIMIT $3`, tenantID, siteID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	findings := make([]intelligencemodel.FDDFinding, 0, limit)
	for rows.Next() {
		var finding intelligencemodel.FDDFinding
		if err = rows.Scan(&finding.ID, &finding.TenantID, &finding.SiteID, &finding.AssetID, &finding.FindingType,
			&finding.EvaluationFrom, &finding.EvaluationTo, &finding.EvidenceIDs, &finding.ModelDeploymentID, &finding.RuleRevisionID,
			&finding.Confidence, &finding.QualityBlocker, &finding.AlarmID, &finding.WorkOrderID, &finding.CreatedAt); err != nil {
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
	var finding intelligencemodel.FDDFinding
	err = tx.QueryRow(ctx, `SELECT id::text,tenant_id::text,site_id::text,asset_id::text,finding_type,evaluation_from,evaluation_to,evidence_ids,
COALESCE(model_deployment_revision_id::text,''),COALESCE(rule_revision_id,''),confidence,COALESCE(quality_blocker,''),COALESCE(alarm_id::text,''),COALESCE(work_order_id::text,''),created_at
FROM core_registry.fdd_findings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid`, tenantID, siteID, findingID).Scan(
		&finding.ID, &finding.TenantID, &finding.SiteID, &finding.AssetID, &finding.FindingType, &finding.EvaluationFrom, &finding.EvaluationTo,
		&finding.EvidenceIDs, &finding.ModelDeploymentID, &finding.RuleRevisionID, &finding.Confidence, &finding.QualityBlocker, &finding.AlarmID, &finding.WorkOrderID, &finding.CreatedAt)
	if err != nil {
		return intelligencemodel.FDDFinding{}, err
	}
	_ = at // Link target mutation is captured by linked IDs; source finding evidence remains immutable.
	if err = tx.Commit(ctx); err != nil {
		return intelligencemodel.FDDFinding{}, err
	}
	return finding, nil
}
