package migration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ResolutionApply  = "apply"
	ResolutionRetire = "retire"
)

type PostgresMigrator struct {
	pool  *pgxpool.Pool
	now   func() time.Time
	newID func(time.Time) (string, error)
}

type mapRecord struct {
	ID                 string
	SiteID             string
	TargetResourceType string
	TargetResourceID   string
	MappingState       string
	SourceRowHash      string
}

type quarantineRecord struct {
	ID               string
	SourceSystem     string
	SourceTable      string
	SourceKey        string
	ReasonCode       string
	PreviousTargetID string
	Resolved         bool
}

func OpenPostgres(ctx context.Context, databaseURL string) (*PostgresMigrator, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("legacy migration database URL is required")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse legacy migration database URL: %w", err)
	}
	config.MaxConns = 4
	config.MinConns = 0
	config.MaxConnLifetime = 10 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open legacy migration database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping legacy migration database: %w", err)
	}
	return &PostgresMigrator{pool: pool, now: time.Now, newID: newUUIDv7}, nil
}

func (migrator *PostgresMigrator) Close() {
	if migrator != nil && migrator.pool != nil {
		migrator.pool.Close()
	}
}

func (migrator *PostgresMigrator) Apply(ctx context.Context, records []Record) (Summary, error) {
	ordered := append([]Record(nil), records...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := kindRank(ordered[i].Kind), kindRank(ordered[j].Kind)
		if left != right {
			return left < right
		}
		return ordered[i].SourceIdentity() < ordered[j].SourceIdentity()
	})
	var summary Summary
	for _, record := range ordered {
		result, err := migrator.ApplyRecord(ctx, record)
		if err != nil {
			return summary, err
		}
		summary.Add(result)
	}
	return summary, nil
}

func (migrator *PostgresMigrator) ApplyRecord(ctx context.Context, record Record) (RecordResult, error) {
	if migrator == nil || migrator.pool == nil {
		return RecordResult{}, errors.New("legacy migration store is closed")
	}
	record = normalizeRecord(record)
	if err := record.ValidateEnvelope(); err != nil {
		return RecordResult{}, err
	}
	result := RecordResult{SourceSystem: record.SourceSystem, SourceTable: record.SourceTable, SourceKey: record.SourceKey}
	err := migrator.withOperatorTx(ctx, func(tx pgx.Tx, now time.Time) error {
		if err := lockSource(ctx, tx, record.SourceIdentity()); err != nil {
			return err
		}
		existing, err := loadMap(ctx, tx, record)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if existing != nil {
			switch existing.MappingState {
			case "VERIFIED":
				if existing.SourceRowHash == record.SourceRowHash {
					if err := migrator.insertProvenance(ctx, tx, now, record, existing.ID, existing.TargetResourceID, "SKIPPED"); err != nil {
						return err
					}
					result.Outcome = OutcomeSkipped
					result.TargetID = existing.TargetResourceID
					return nil
				}
				quarantined, err := migrator.quarantine(ctx, tx, now, record, existing, "SOURCE_HASH_CONFLICT", existing.SiteID)
				if err != nil {
					return err
				}
				result = quarantined
				return nil
			case "QUARANTINED":
				openQuarantine, err := loadOpenQuarantine(ctx, tx, record)
				if err == nil {
					if err := migrator.insertProvenance(ctx, tx, now, record, existing.ID, "", "QUARANTINED"); err != nil {
						return err
					}
					result.Outcome = OutcomeQuarantined
					result.ReasonCode = openQuarantine.ReasonCode
					return nil
				}
				if !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
				quarantined, err := migrator.quarantine(ctx, tx, now, record, existing, "INCOMPLETE_QUARANTINE", existing.SiteID)
				if err != nil {
					return err
				}
				result = quarantined
				return nil
			case "DISCOVERED", "MAPPED":
				quarantined, err := migrator.quarantine(ctx, tx, now, record, existing, "INCOMPLETE_MAPPING_STATE", existing.SiteID)
				if err != nil {
					return err
				}
				result = quarantined
				return nil
			case "RETIRED":
				if err := migrator.insertProvenance(ctx, tx, now, record, existing.ID, existing.TargetResourceID, "SKIPPED"); err != nil {
					return err
				}
				result.Outcome = OutcomeSkipped
				result.TargetID = existing.TargetResourceID
				return nil
			default:
				return fmt.Errorf("unsupported stored mapping state %q", existing.MappingState)
			}
		}

		siteID, parentReason, err := resolveParents(ctx, tx, record)
		if err != nil {
			return err
		}
		reason := record.BusinessReason()
		if reason == "" {
			reason = parentReason
		}
		if reason != "" {
			quarantined, err := migrator.quarantine(ctx, tx, now, record, nil, reason, siteID)
			if err != nil {
				return err
			}
			result = quarantined
			return nil
		}

		targetID, err := migrator.newID(now)
		if err != nil {
			return err
		}
		if record.Kind == KindSite {
			siteID = targetID
		}
		mappingID, err := migrator.newID(now)
		if err != nil {
			return err
		}
		metadata, err := json.Marshal(record.RelationEvidence)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO core_registry.legacy_resource_maps
			  (id, tenant_id, site_id, source_system, source_table, source_key, target_resource_type,
			   target_resource_id, mapping_state, transformation_version, batch_id, source_watermark,
			   source_row_hash, relation_evidence, created_at, updated_at)
			VALUES ($1,$2::uuid,NULLIF($3,'')::uuid,$4,$5,$6,$7,NULL,'DISCOVERED',$8,$9,$10,$11,$12::jsonb,$13,$13)`,
			mappingID, record.TenantID, siteID, record.SourceSystem, record.SourceTable, record.SourceKey,
			record.TargetResourceType(), record.TransformationVersion, record.BatchID, record.SourceWatermark,
			record.SourceRowHash, string(metadata), now); err != nil {
			return fmt.Errorf("insert discovered legacy mapping: %w", err)
		}

		reason, err = insertBusinessWithSavepoint(ctx, tx, now, targetID, siteID, record)
		if err != nil {
			return err
		}
		if reason != "" {
			existing = &mapRecord{ID: mappingID, SiteID: siteID, TargetResourceType: record.Kind, MappingState: "DISCOVERED", SourceRowHash: record.SourceRowHash}
			quarantined, err := migrator.quarantine(ctx, tx, now, record, existing, reason, siteID)
			if err != nil {
				return err
			}
			result = quarantined
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE core_registry.legacy_resource_maps SET target_resource_id=$2, mapping_state='MAPPED', updated_at=$3 WHERE id=$1`, mappingID, targetID, now); err != nil {
			return fmt.Errorf("mark legacy mapping mapped: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE core_registry.legacy_resource_maps SET mapping_state='VERIFIED', updated_at=$2 WHERE id=$1`, mappingID, now); err != nil {
			return fmt.Errorf("verify legacy mapping: %w", err)
		}
		if err := migrator.insertProvenance(ctx, tx, now, record, mappingID, targetID, "IMPORTED"); err != nil {
			return err
		}
		result.Outcome = OutcomeImported
		result.TargetID = targetID
		return nil
	})
	return result, err
}

func (migrator *PostgresMigrator) Resolve(ctx context.Context, quarantineID, action string, record Record) (RecordResult, error) {
	if migrator == nil || migrator.pool == nil {
		return RecordResult{}, errors.New("legacy migration store is closed")
	}
	if !isUUIDv7(quarantineID) {
		return RecordResult{}, errors.New("quarantine id must be UUIDv7")
	}
	if action != ResolutionApply && action != ResolutionRetire {
		return RecordResult{}, fmt.Errorf("unsupported resolution action %q", action)
	}
	record = normalizeRecord(record)
	if err := record.ValidateEnvelope(); err != nil {
		return RecordResult{}, err
	}
	result := RecordResult{SourceSystem: record.SourceSystem, SourceTable: record.SourceTable, SourceKey: record.SourceKey}
	err := migrator.withOperatorTx(ctx, func(tx pgx.Tx, now time.Time) error {
		if err := lockSource(ctx, tx, record.SourceIdentity()); err != nil {
			return err
		}
		quarantine, err := loadQuarantine(ctx, tx, quarantineID)
		if err != nil {
			return fmt.Errorf("load migration quarantine: %w", err)
		}
		if quarantine.SourceSystem != record.SourceSystem || quarantine.SourceTable != record.SourceTable || quarantine.SourceKey != record.SourceKey {
			return errors.New("corrected record does not match quarantine source identity")
		}
		existing, err := loadMap(ctx, tx, record)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errors.New("migration quarantine mapping is missing")
			}
			return err
		}
		if quarantine.Resolved {
			if err := migrator.insertProvenance(ctx, tx, now, record, existing.ID, existing.TargetResourceID, "SKIPPED"); err != nil {
				return err
			}
			result.Outcome = OutcomeSkipped
			result.TargetID = existing.TargetResourceID
			return nil
		}
		siteID, parentReason, err := resolveParents(ctx, tx, record)
		if err != nil {
			return err
		}
		reason := record.BusinessReason()
		if reason == "" {
			reason = parentReason
		}
		if reason != "" {
			return fmt.Errorf("corrected record remains invalid: %s", reason)
		}
		resolvedRecord := record
		if action == ResolutionRetire {
			resolvedRecord.Status = "RETIRED"
		}
		targetID := quarantine.PreviousTargetID
		if targetID != "" {
			if record.Kind == KindSite {
				siteID = targetID
			}
			if action == ResolutionApply {
				matches, err := businessTargetMatches(ctx, tx, targetID, siteID, resolvedRecord)
				if err != nil {
					return err
				}
				if !matches {
					return errors.New("corrected record does not match the quarantined target")
				}
			} else if err := retireBusinessTarget(ctx, tx, now, targetID, siteID, record.Kind); err != nil {
				return err
			}
		} else {
			targetID, err = migrator.newID(now)
			if err != nil {
				return err
			}
			if record.Kind == KindSite {
				siteID = targetID
			}
			if reason, err := insertBusinessWithSavepoint(ctx, tx, now, targetID, siteID, resolvedRecord); err != nil {
				return err
			} else if reason != "" {
				return fmt.Errorf("corrected record could not be imported: %s", reason)
			}
		}
		metadata, err := json.Marshal(record.RelationEvidence)
		if err != nil {
			return err
		}
		mappingID := existing.ID
		finalState := "VERIFIED"
		resolution := "CORRECTED_RECORD_APPLIED"
		result.Outcome = OutcomeResolved
		if action == ResolutionRetire {
			finalState = "RETIRED"
			resolution = "SOURCE_RETIRED"
			result.Outcome = OutcomeRetired
		}
		if _, err := tx.Exec(ctx, `
			UPDATE core_registry.legacy_resource_maps
			SET site_id=NULLIF($2,'')::uuid, target_resource_type=$3,
			    target_resource_id=$4, mapping_state=$5, transformation_version=$6, batch_id=$7,
			    source_watermark=$8, source_row_hash=$9, relation_evidence=$10::jsonb, updated_at=$11
			WHERE id=$1`, mappingID, siteID, record.Kind, targetID, finalState,
			record.TransformationVersion, record.BatchID, record.SourceWatermark, record.SourceRowHash, string(metadata), now); err != nil {
			return fmt.Errorf("finalize resolved legacy mapping: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE core_registry.migration_quarantine SET resolved_at=$2, resolution=$3 WHERE id=$1 AND resolved_at IS NULL`, quarantineID, now, resolution); err != nil {
			return fmt.Errorf("resolve migration quarantine: %w", err)
		}
		if err := migrator.insertProvenance(ctx, tx, now, record, mappingID, targetID, "VERIFIED"); err != nil {
			return err
		}
		result.TargetID = targetID
		return nil
	})
	return result, err
}

func (migrator *PostgresMigrator) withOperatorTx(ctx context.Context, body func(pgx.Tx, time.Time) error) error {
	tx, err := migrator.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin legacy migration transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s1_migration_operator`); err != nil {
		return fmt.Errorf("activate legacy migration role: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('statement_timeout','5000',true)`); err != nil {
		return fmt.Errorf("set legacy migration statement timeout: %w", err)
	}
	var role string
	if err := tx.QueryRow(ctx, `SELECT current_role`).Scan(&role); err != nil {
		return fmt.Errorf("read active legacy migration role: %w", err)
	}
	if role != "s1_migration_operator" {
		return fmt.Errorf("unexpected legacy migration role %q", role)
	}
	now := migrator.now().UTC()
	if err := body(tx, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit legacy migration transaction: %w", err)
	}
	return nil
}

func loadMap(ctx context.Context, tx pgx.Tx, record Record) (*mapRecord, error) {
	var stored mapRecord
	err := tx.QueryRow(ctx, `
		SELECT id::text, COALESCE(site_id::text,''), target_resource_type,
		       COALESCE(target_resource_id::text,''), mapping_state, source_row_hash
		FROM core_registry.legacy_resource_maps
		WHERE tenant_id=$1::uuid AND source_system=$2 AND source_table=$3 AND source_key=$4
		FOR UPDATE`, record.TenantID, record.SourceSystem, record.SourceTable, record.SourceKey).Scan(
		&stored.ID, &stored.SiteID, &stored.TargetResourceType,
		&stored.TargetResourceID, &stored.MappingState, &stored.SourceRowHash,
	)
	if err != nil {
		return nil, err
	}
	return &stored, nil
}

func loadQuarantine(ctx context.Context, tx pgx.Tx, id string) (quarantineRecord, error) {
	var quarantine quarantineRecord
	var resolvedAt *time.Time
	err := tx.QueryRow(ctx, `
		SELECT id::text, source_system, source_table, source_key,
		       reason_code, COALESCE(payload_metadata->>'previousTargetResourceId',''), resolved_at
		FROM core_registry.migration_quarantine WHERE id=$1 FOR UPDATE`, id).Scan(
		&quarantine.ID, &quarantine.SourceSystem, &quarantine.SourceTable, &quarantine.SourceKey,
		&quarantine.ReasonCode, &quarantine.PreviousTargetID, &resolvedAt,
	)
	quarantine.Resolved = resolvedAt != nil
	return quarantine, err
}

func loadOpenQuarantine(ctx context.Context, tx pgx.Tx, record Record) (quarantineRecord, error) {
	var quarantine quarantineRecord
	err := tx.QueryRow(ctx, `
		SELECT id::text, source_system, source_table, source_key,
		       reason_code, COALESCE(payload_metadata->>'previousTargetResourceId','')
		FROM core_registry.migration_quarantine
		WHERE tenant_id=$1::uuid AND source_system=$2 AND source_table=$3 AND source_key=$4 AND resolved_at IS NULL
		FOR UPDATE`, record.TenantID, record.SourceSystem, record.SourceTable, record.SourceKey).Scan(
		&quarantine.ID, &quarantine.SourceSystem, &quarantine.SourceTable, &quarantine.SourceKey,
		&quarantine.ReasonCode, &quarantine.PreviousTargetID,
	)
	return quarantine, err
}

func resolveParents(ctx context.Context, tx pgx.Tx, record Record) (string, string, error) {
	if record.Kind == KindSite {
		return "", "", nil
	}
	siteID, err := resolveReference(ctx, tx, record.TenantID, record.SiteRef, KindSite)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "MISSING_SITE_PARENT", nil
	}
	if err != nil {
		return "", "", err
	}
	return siteID, "", nil
}

func resolveReference(ctx context.Context, tx pgx.Tx, tenantID string, ref *SourceRef, expectedType string) (string, error) {
	if ref == nil {
		return "", pgx.ErrNoRows
	}
	var targetID string
	err := tx.QueryRow(ctx, `
		SELECT target_resource_id::text
		FROM core_registry.legacy_resource_maps
		WHERE tenant_id=$1::uuid AND source_system=$2 AND source_table=$3 AND source_key=$4
		  AND target_resource_type=$5 AND mapping_state='VERIFIED'`,
		tenantID, ref.SourceSystem, ref.SourceTable, ref.SourceKey, expectedType).Scan(&targetID)
	return targetID, err
}

func businessTargetMatches(ctx context.Context, tx pgx.Tx, targetID, siteID string, record Record) (bool, error) {
	var matches bool
	var err error
	switch record.Kind {
	case KindSite:
		err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core_registry.sites WHERE id=$1 AND tenant_id=$2::uuid AND code=$3 AND display_name=$4 AND timezone=$5 AND status=$6)`, targetID, record.TenantID, record.Code, record.DisplayName, record.Timezone, record.Status).Scan(&matches)
	case KindAsset:
		err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core_registry.assets WHERE id=$1 AND tenant_id=$2::uuid AND site_id=$3 AND code=$4 AND display_name=$5 AND asset_type=$6 AND status=$7)`, targetID, record.TenantID, siteID, record.Code, record.DisplayName, record.ResourceType, record.Status).Scan(&matches)
	case KindDevice:
		err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core_registry.devices WHERE id=$1 AND tenant_id=$2::uuid AND site_id=$3 AND code=$4 AND display_name=$5 AND device_type=$6 AND status=$7)`, targetID, record.TenantID, siteID, record.Code, record.DisplayName, record.ResourceType, record.Status).Scan(&matches)
	default:
		return false, fmt.Errorf("unsupported record kind %q", record.Kind)
	}
	if err != nil {
		return false, fmt.Errorf("verify quarantined business target: %w", err)
	}
	return matches, nil
}

func retireBusinessTarget(ctx context.Context, tx pgx.Tx, now time.Time, targetID, siteID, kind string) error {
	var command string
	var arguments []any
	switch kind {
	case KindSite:
		command = `UPDATE core_registry.sites SET status='RETIRED', revision=revision+1, updated_at=$2 WHERE id=$1`
		arguments = []any{targetID, now}
	case KindAsset:
		command = `UPDATE core_registry.assets SET status='RETIRED', revision=revision+1, updated_at=$3 WHERE id=$1 AND site_id=$2`
		arguments = []any{targetID, siteID, now}
	case KindDevice:
		command = `UPDATE core_registry.devices SET status='RETIRED', revision=revision+1, updated_at=$3 WHERE id=$1 AND site_id=$2`
		arguments = []any{targetID, siteID, now}
	default:
		return fmt.Errorf("unsupported record kind %q", kind)
	}
	result, err := tx.Exec(ctx, command, arguments...)
	if err != nil {
		return fmt.Errorf("retire quarantined business target: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("quarantined business target is missing")
	}
	return nil
}

func insertBusinessWithSavepoint(ctx context.Context, tx pgx.Tx, now time.Time, targetID, siteID string, record Record) (string, error) {
	if _, err := tx.Exec(ctx, `SAVEPOINT legacy_business_insert`); err != nil {
		return "", err
	}
	var err error
	switch record.Kind {
	case KindSite:
		_, err = tx.Exec(ctx, `INSERT INTO core_registry.sites (id,tenant_id,code,display_name,timezone,status,revision,created_at,updated_at) VALUES ($1,$2::uuid,$3,$4,$5,$6,1,$7,$7)`, targetID, record.TenantID, strings.TrimSpace(record.Code), strings.TrimSpace(record.DisplayName), record.Timezone, record.Status, now)
	case KindAsset:
		_, err = tx.Exec(ctx, `INSERT INTO core_registry.assets (id,tenant_id,site_id,code,display_name,asset_type,status,revision,created_at,updated_at) VALUES ($1,$2::uuid,$3,$4,$5,$6,$7,1,$8,$8)`, targetID, record.TenantID, siteID, strings.TrimSpace(record.Code), strings.TrimSpace(record.DisplayName), record.ResourceType, record.Status, now)
	case KindDevice:
		_, err = tx.Exec(ctx, `INSERT INTO core_registry.devices (id,tenant_id,site_id,code,display_name,device_type,status,revision,created_at,updated_at) VALUES ($1,$2::uuid,$3,$4,$5,$6,$7,1,$8,$8)`, targetID, record.TenantID, siteID, strings.TrimSpace(record.Code), strings.TrimSpace(record.DisplayName), record.ResourceType, record.Status, now)
	default:
		err = fmt.Errorf("unsupported record kind %q", record.Kind)
	}
	if err == nil {
		_, releaseErr := tx.Exec(ctx, `RELEASE SAVEPOINT legacy_business_insert`)
		return "", releaseErr
	}
	if _, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT legacy_business_insert`); rollbackErr != nil {
		return "", fmt.Errorf("rollback failed business insert: %w (original: %v)", rollbackErr, err)
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "22023":
			return "INVALID_TIMEZONE", nil
		case "23503":
			return "MISSING_PARENT", nil
		case "23505":
			return "DUPLICATE_BUSINESS_KEY", nil
		case "23514", "22P02":
			return "INVALID_RESOURCE_DATA", nil
		}
	}
	return "", fmt.Errorf("insert migrated business resource: %w", err)
}

func (migrator *PostgresMigrator) quarantine(ctx context.Context, tx pgx.Tx, now time.Time, record Record, existing *mapRecord, reason, siteID string) (RecordResult, error) {
	mappingID := ""
	if existing != nil {
		mappingID = existing.ID
	}
	metadata := map[string]any{
		"recordKind":       record.Kind,
		"relationEvidence": record.RelationEvidence,
	}
	if record.SiteRef != nil {
		metadata["siteRef"] = record.SiteRef
	}
	if existing != nil && existing.TargetResourceID != "" {
		metadata["previousTargetResourceId"] = existing.TargetResourceID
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return RecordResult{}, err
	}
	relationJSON, err := json.Marshal(record.RelationEvidence)
	if err != nil {
		return RecordResult{}, err
	}
	if mappingID == "" {
		mappingID, err = migrator.newID(now)
		if err != nil {
			return RecordResult{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO core_registry.legacy_resource_maps
			  (id, tenant_id, site_id, source_system, source_table, source_key, target_resource_type,
			   target_resource_id, mapping_state, transformation_version, batch_id, source_watermark,
			   source_row_hash, relation_evidence, created_at, updated_at)
			VALUES ($1,$2::uuid,NULLIF($3,'')::uuid,$4,$5,$6,$7,NULL,'DISCOVERED',$8,$9,$10,$11,$12::jsonb,$13,$13)`,
			mappingID, record.TenantID, siteID, record.SourceSystem, record.SourceTable, record.SourceKey,
			record.Kind, record.TransformationVersion, record.BatchID, record.SourceWatermark,
			record.SourceRowHash, string(relationJSON), now); err != nil {
			return RecordResult{}, fmt.Errorf("insert quarantined legacy map: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE core_registry.legacy_resource_maps
		SET site_id=NULLIF($2,'')::uuid, target_resource_type=$3, target_resource_id=NULL,
		    mapping_state='QUARANTINED', transformation_version=$4, batch_id=$5, source_watermark=$6,
		    source_row_hash=$7, relation_evidence=$8::jsonb, updated_at=$9
		WHERE id=$1`, mappingID, siteID, record.Kind, record.TransformationVersion,
		record.BatchID, record.SourceWatermark, record.SourceRowHash, string(relationJSON), now); err != nil {
		return RecordResult{}, fmt.Errorf("mark legacy map quarantined: %w", err)
	}
	quarantineID, err := migrator.newID(now)
	if err != nil {
		return RecordResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO core_registry.migration_quarantine
		  (id,tenant_id,source_system,source_table,source_key,reason_code,source_row_hash,payload_metadata,detected_at)
		VALUES ($1,$2::uuid,$3,$4,$5,$6,$7,$8::jsonb,$9)
		ON CONFLICT (source_system,source_table,source_key) WHERE resolved_at IS NULL
		DO UPDATE SET tenant_id=EXCLUDED.tenant_id, reason_code=EXCLUDED.reason_code,
		              source_row_hash=EXCLUDED.source_row_hash, payload_metadata=EXCLUDED.payload_metadata,
		              detected_at=EXCLUDED.detected_at`, quarantineID, record.TenantID, record.SourceSystem,
		record.SourceTable, record.SourceKey, reason, record.SourceRowHash, string(metadataJSON), now); err != nil {
		return RecordResult{}, fmt.Errorf("upsert migration quarantine: %w", err)
	}
	if err := migrator.insertProvenance(ctx, tx, now, record, mappingID, "", "QUARANTINED"); err != nil {
		return RecordResult{}, err
	}
	return RecordResult{SourceSystem: record.SourceSystem, SourceTable: record.SourceTable, SourceKey: record.SourceKey, Outcome: OutcomeQuarantined, ReasonCode: reason}, nil
}

func (migrator *PostgresMigrator) insertProvenance(ctx context.Context, tx pgx.Tx, now time.Time, record Record, mappingID, targetID, result string) error {
	provenanceID, err := migrator.newID(now)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO core_registry.migration_provenance
		  (id,tenant_id,legacy_resource_map_id,source_system,source_table,source_key,target_resource_type,
		   target_resource_id,transformation_version,batch_id,source_watermark,source_row_hash,result,created_at)
		VALUES ($1,$2::uuid,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid,$9,$10,$11,$12,$13,$14)`,
		provenanceID, record.TenantID, mappingID, record.SourceSystem, record.SourceTable, record.SourceKey,
		record.Kind, targetID, record.TransformationVersion, record.BatchID, record.SourceWatermark,
		record.SourceRowHash, result, now); err != nil {
		return fmt.Errorf("insert migration provenance: %w", err)
	}
	return nil
}

func lockSource(ctx context.Context, tx pgx.Tx, identity string) error {
	digest := sha256.Sum256([]byte(identity))
	key := int64(binary.BigEndian.Uint64(digest[:8]))
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, key); err != nil {
		return fmt.Errorf("lock legacy source record: %w", err)
	}
	return nil
}

func isUUIDv7(value string) bool {
	if value != strings.ToLower(value) || len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded := make([]byte, 16)
	if _, err := hex.Decode(decoded, []byte(compact)); err != nil {
		return false
	}
	return decoded[6]>>4 == 7 && decoded[8]>>6 == 2
}

func newUUIDv7(now time.Time) (string, error) {
	var value [16]byte
	milliseconds := uint64(now.UTC().UnixMilli())
	value[0] = byte(milliseconds >> 40)
	value[1] = byte(milliseconds >> 32)
	value[2] = byte(milliseconds >> 24)
	value[3] = byte(milliseconds >> 16)
	value[4] = byte(milliseconds >> 8)
	value[5] = byte(milliseconds)
	if _, err := rand.Read(value[6:]); err != nil {
		return "", fmt.Errorf("generate UUIDv7 entropy: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x70
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
