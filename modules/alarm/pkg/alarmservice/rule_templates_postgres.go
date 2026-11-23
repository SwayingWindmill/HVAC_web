package alarmservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

func scanRuleBaselineTemplate(row pgx.Row, latestSpecJSON *[]byte) (RuleBaselineTemplate, error) {
	var tmpl RuleBaselineTemplate
	var alarmType, ruleKind string
	var boundJSON []byte
	var createdAt, updatedAt time.Time
	err := row.Scan(
		&tmpl.TemplateID,
		&tmpl.TemplateKey,
		&tmpl.DisplayName,
		&tmpl.Description,
		&tmpl.DeviceType,
		&tmpl.ThingModelCode,
		&alarmType,
		&ruleKind,
		&tmpl.DefaultSeverity,
		&tmpl.DefaultExecutionTarget,
		&boundJSON,
		&tmpl.LatestVersion,
		&tmpl.Status,
		&tmpl.CreatedBy,
		&tmpl.UpdatedBy,
		&createdAt,
		&updatedAt,
		latestSpecJSON,
	)
	if err != nil {
		return RuleBaselineTemplate{}, err
	}
	tmpl.AlarmType = alarmType
	tmpl.RuleKind = RuleKind(ruleKind)
	tmpl.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	tmpl.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
	if len(boundJSON) > 0 {
		if err := json.Unmarshal(boundJSON, &tmpl.BoundSiteIDs); err != nil {
			return RuleBaselineTemplate{}, fmt.Errorf("decode bound_site_ids: %w", err)
		}
	}
	if tmpl.BoundSiteIDs == nil {
		tmpl.BoundSiteIDs = []string{}
	}
	if latestSpecJSON != nil && len(*latestSpecJSON) > 0 {
		var spec RuleTemplateSpec
		if err := json.Unmarshal(*latestSpecJSON, &spec); err != nil {
			return RuleBaselineTemplate{}, fmt.Errorf("decode latest rule_spec: %w", err)
		}
		tmpl.LatestSpec = &spec
	}
	return tmpl, nil
}

const ruleTemplateSelectSQL = `
SELECT t.id::text, t.template_key, t.display_name, t.description, t.device_type, t.thing_model_code,
       t.alarm_type, t.rule_kind, t.default_severity, t.default_execution_target, t.bound_site_ids,
       t.latest_version, t.status, t.created_by, t.updated_by, t.created_at, t.updated_at,
       r.rule_spec
FROM alarm_runtime.rule_baseline_template t
LEFT JOIN alarm_runtime.rule_baseline_template_release r
  ON r.tenant_id = t.tenant_id AND r.template_id = t.id AND r.version = t.latest_version`

func (store *PostgresStore) ListRuleTemplates(ctx context.Context, tenantID, statusFilter string) ([]RuleBaselineTemplate, error) {
	if store == nil || store.pool == nil {
		return nil, ErrUnavailable
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	status := strings.TrimSpace(statusFilter)
	var rows pgx.Rows
	if status != "" {
		rows, err = tx.Query(ctx, ruleTemplateSelectSQL+`
WHERE t.tenant_id = $1 AND t.status = $2
ORDER BY t.updated_at DESC, t.template_key ASC`, tenantID, status)
	} else {
		rows, err = tx.Query(ctx, ruleTemplateSelectSQL+`
WHERE t.tenant_id = $1
ORDER BY t.updated_at DESC, t.template_key ASC`, tenantID)
	}
	if err != nil {
		return nil, fmt.Errorf("list alarm rule baseline templates: %w", err)
	}
	defer rows.Close()

	items := make([]RuleBaselineTemplate, 0)
	for rows.Next() {
		var specBytes []byte
		item, err := scanRuleBaselineTemplate(rows, &specBytes)
		if err != nil {
			return nil, fmt.Errorf("scan alarm rule baseline template: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate alarm rule baseline templates: %w", err)
	}
	return items, nil
}

func (store *PostgresStore) GetRuleTemplate(ctx context.Context, tenantID, templateID string) (RuleBaselineTemplateDetail, error) {
	if store == nil || store.pool == nil {
		return RuleBaselineTemplateDetail{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(templateID) {
		return RuleBaselineTemplateDetail{}, ErrNotFound
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return RuleBaselineTemplateDetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var specBytes []byte
	tmpl, err := scanRuleBaselineTemplate(tx.QueryRow(ctx, ruleTemplateSelectSQL+`
WHERE t.tenant_id = $1 AND t.id = $2`, tenantID, templateID), &specBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuleBaselineTemplateDetail{}, ErrNotFound
	}
	if err != nil {
		return RuleBaselineTemplateDetail{}, fmt.Errorf("get alarm rule baseline template: %w", err)
	}
	releases, err := listTemplateReleasesInTx(ctx, tx, tenantID, templateID, maxTemplateReleaseListLim)
	if err != nil {
		return RuleBaselineTemplateDetail{}, err
	}
	return RuleBaselineTemplateDetail{Template: tmpl, Releases: releases}, nil
}

func listTemplateReleasesInTx(ctx context.Context, tx pgx.Tx, tenantID, templateID string, limit int) ([]RuleBaselineTemplateRelease, error) {
	if limit <= 0 || limit > maxTemplateReleaseListLim {
		limit = maxTemplateReleaseListLim
	}
	rows, err := tx.Query(ctx, `
SELECT id::text, template_id::text, version, rule_spec, change_note, published_by, published_by_name, published_at
FROM alarm_runtime.rule_baseline_template_release
WHERE tenant_id = $1 AND template_id = $2
ORDER BY version DESC
LIMIT $3`, tenantID, templateID, limit)
	if err != nil {
		return nil, fmt.Errorf("list template releases: %w", err)
	}
	defer rows.Close()

	items := make([]RuleBaselineTemplateRelease, 0)
	for rows.Next() {
		var rel RuleBaselineTemplateRelease
		var specJSON []byte
		var publishedAt time.Time
		if err := rows.Scan(&rel.ReleaseID, &rel.TemplateID, &rel.Version, &specJSON, &rel.ChangeNote, &rel.PublishedBy, &rel.PublishedByName, &publishedAt); err != nil {
			return nil, fmt.Errorf("scan template release: %w", err)
		}
		if err := json.Unmarshal(specJSON, &rel.RuleSpec); err != nil {
			return nil, fmt.Errorf("decode template release rule_spec: %w", err)
		}
		rel.PublishedAt = publishedAt.UTC().Format(time.RFC3339Nano)
		items = append(items, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate template releases: %w", err)
	}
	return items, nil
}

func validateBoundSiteList(siteIDs []string) ([]string, error) {
	if len(siteIDs) > maxTemplateBoundSites {
		return nil, &RuleTemplateValidationError{
			Code:    "ALARM_TEMPLATE_INVALID",
			Message: fmt.Sprintf("boundSiteIds exceed maximum %d", maxTemplateBoundSites),
		}
	}
	seen := make(map[string]bool, len(siteIDs))
	cleaned := make([]string, 0, len(siteIDs))
	for _, raw := range siteIDs {
		sid := strings.TrimSpace(raw)
		if !alarmmodel.IsUUIDv7(sid) {
			return nil, &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "every bound siteId must be a valid UUIDv7"}
		}
		if !seen[sid] {
			seen[sid] = true
			cleaned = append(cleaned, sid)
		}
	}
	return cleaned, nil
}

func (store *PostgresStore) CreateRuleTemplate(
	ctx context.Context,
	tenantID string,
	req CreateRuleTemplateRequest,
	actorID, actorName string,
	now time.Time,
) (RuleBaselineTemplateDetail, error) {
	if store == nil || store.pool == nil || store.newID == nil {
		return RuleBaselineTemplateDetail{}, ErrUnavailable
	}
	key := strings.TrimSpace(req.TemplateKey)
	name := strings.TrimSpace(req.DisplayName)
	devType := strings.TrimSpace(req.DeviceType)
	alarmType := strings.TrimSpace(req.AlarmType)
	if alarmType == "" {
		alarmType = "template/" + key
	}
	if !boundedText(key, 128) || !boundedText(name, 256) || !boundedText(devType, 64) || !boundedText(alarmType, 128) {
		return RuleBaselineTemplateDetail{}, &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "templateKey, displayName, and deviceType are required"}
	}
	if !validAlarmSeverity(req.DefaultSeverity) {
		return RuleBaselineTemplateDetail{}, &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "defaultSeverity is invalid"}
	}
	execTarget := req.DefaultExecutionTarget
	if execTarget == "" {
		execTarget = ExecutionTargetCloud
	}
	if !validExecutionTarget(execTarget) {
		return RuleBaselineTemplateDetail{}, &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "defaultExecutionTarget is invalid"}
	}
	ruleKind := req.RuleKind
	if ruleKind == "" {
		ruleKind = RuleKindThreshold
	}
	if !validRuleKind(ruleKind) {
		return RuleBaselineTemplateDetail{}, &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "ruleKind is invalid"}
	}
	boundSites, err := validateBoundSiteList(req.BoundSiteIDs)
	if err != nil {
		return RuleBaselineTemplateDetail{}, err
	}
	tmplID, err := store.newID(now)
	if err != nil {
		return RuleBaselineTemplateDetail{}, err
	}
	by, byName := normalizeRuleActor(actorID, actorName)
	tmpl := RuleBaselineTemplate{
		TemplateID:             tmplID,
		TemplateKey:            key,
		DisplayName:            name,
		Description:            strings.TrimSpace(req.Description),
		DeviceType:             devType,
		ThingModelCode:         strings.TrimSpace(req.ThingModelCode),
		AlarmType:              alarmType,
		RuleKind:               ruleKind,
		DefaultSeverity:        req.DefaultSeverity,
		DefaultExecutionTarget: execTarget,
		BoundSiteIDs:           boundSites,
		LatestVersion:          0,
		Status:                 TemplateStatusActive,
		CreatedBy:              by,
		UpdatedBy:              by,
		CreatedAt:              now.UTC().Format(time.RFC3339Nano),
		UpdatedAt:              now.UTC().Format(time.RFC3339Nano),
	}

	var initialRel *RuleBaselineTemplateRelease
	if req.InitialSpec != nil {
		specCopy := *req.InitialSpec
		if err := normalizeAndValidateTemplateSpec(&specCopy, tmpl); err != nil {
			return RuleBaselineTemplateDetail{}, err
		}
		note, err := validateChangeNote(req.ChangeNote)
		if err != nil {
			return RuleBaselineTemplateDetail{}, &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: err.Error()}
		}
		relID, err := store.newID(now)
		if err != nil {
			return RuleBaselineTemplateDetail{}, err
		}
		tmpl.LatestVersion = 1
		tmpl.LatestSpec = &specCopy
		initialRel = &RuleBaselineTemplateRelease{
			ReleaseID:       relID,
			TemplateID:      tmplID,
			Version:         1,
			RuleSpec:        specCopy,
			ChangeNote:      note,
			PublishedBy:     by,
			PublishedByName: byName,
			PublishedAt:     now.UTC().Format(time.RFC3339Nano),
		}
	}

	boundJSON, err := json.Marshal(boundSites)
	if err != nil {
		return RuleBaselineTemplateDetail{}, err
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return RuleBaselineTemplateDetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
INSERT INTO alarm_runtime.rule_baseline_template (
	id, tenant_id, template_key, display_name, description, device_type, thing_model_code,
	alarm_type, rule_kind, default_severity, default_execution_target, bound_site_ids,
	latest_version, status, created_by, updated_by, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		tmpl.TemplateID, tenantID, tmpl.TemplateKey, tmpl.DisplayName, tmpl.Description,
		tmpl.DeviceType, tmpl.ThingModelCode, tmpl.AlarmType, string(tmpl.RuleKind),
		string(tmpl.DefaultSeverity), string(tmpl.DefaultExecutionTarget), boundJSON,
		tmpl.LatestVersion, tmpl.Status, by, by, now.UTC(), now.UTC())
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return RuleBaselineTemplateDetail{}, ErrRuleTemplateConflict
		}
		return RuleBaselineTemplateDetail{}, fmt.Errorf("create alarm rule template: %w", err)
	}

	releases := []RuleBaselineTemplateRelease{}
	if initialRel != nil {
		specJSON, err := json.Marshal(initialRel.RuleSpec)
		if err != nil {
			return RuleBaselineTemplateDetail{}, err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO alarm_runtime.rule_baseline_template_release (
	id, tenant_id, template_id, version, rule_spec, change_note, published_by, published_by_name, published_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			initialRel.ReleaseID, tenantID, tmplID, initialRel.Version, specJSON,
			initialRel.ChangeNote, by, byName, now.UTC()); err != nil {
			return RuleBaselineTemplateDetail{}, fmt.Errorf("create initial template release: %w", err)
		}
		releases = append(releases, *initialRel)
	}
	if err := tx.Commit(ctx); err != nil {
		return RuleBaselineTemplateDetail{}, fmt.Errorf("commit alarm rule template create: %w", err)
	}
	return RuleBaselineTemplateDetail{Template: tmpl, Releases: releases}, nil
}

func (store *PostgresStore) UpdateRuleTemplate(
	ctx context.Context,
	tenantID, templateID string,
	req UpdateRuleTemplateRequest,
	actorID, _ string,
	now time.Time,
) (RuleBaselineTemplate, error) {
	if store == nil || store.pool == nil {
		return RuleBaselineTemplate{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(templateID) {
		return RuleBaselineTemplate{}, ErrNotFound
	}
	name := strings.TrimSpace(req.DisplayName)
	devType := strings.TrimSpace(req.DeviceType)
	if !boundedText(name, 256) || !boundedText(devType, 64) {
		return RuleBaselineTemplate{}, &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "displayName and deviceType are required"}
	}
	if !validAlarmSeverity(req.DefaultSeverity) || !validExecutionTarget(req.DefaultExecutionTarget) {
		return RuleBaselineTemplate{}, &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "defaultSeverity or defaultExecutionTarget is invalid"}
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = TemplateStatusActive
	}
	if status != TemplateStatusActive && status != TemplateStatusArchived {
		return RuleBaselineTemplate{}, &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "status must be ACTIVE or ARCHIVED"}
	}
	by, _ := normalizeRuleActor(actorID, "")
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return RuleBaselineTemplate{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
UPDATE alarm_runtime.rule_baseline_template
SET display_name = $3, description = $4, device_type = $5, thing_model_code = $6,
    default_severity = $7, default_execution_target = $8, status = $9, updated_by = $10, updated_at = $11
WHERE tenant_id = $1 AND id = $2`,
		tenantID, templateID, name, strings.TrimSpace(req.Description), devType, strings.TrimSpace(req.ThingModelCode),
		string(req.DefaultSeverity), string(req.DefaultExecutionTarget), status, by, now.UTC())
	if err != nil {
		return RuleBaselineTemplate{}, fmt.Errorf("update alarm rule template: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return RuleBaselineTemplate{}, ErrNotFound
	}
	var specBytes []byte
	updated, err := scanRuleBaselineTemplate(tx.QueryRow(ctx, ruleTemplateSelectSQL+`
WHERE t.tenant_id = $1 AND t.id = $2`, tenantID, templateID), &specBytes)
	if err != nil {
		return RuleBaselineTemplate{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RuleBaselineTemplate{}, err
	}
	return updated, nil
}

func (store *PostgresStore) ListRuleTemplateReleases(ctx context.Context, tenantID, templateID string, limit int) ([]RuleBaselineTemplateRelease, error) {
	if store == nil || store.pool == nil {
		return nil, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(templateID) {
		return nil, ErrNotFound
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM alarm_runtime.rule_baseline_template WHERE tenant_id = $1 AND id = $2)`, tenantID, templateID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	return listTemplateReleasesInTx(ctx, tx, tenantID, templateID, limit)
}

func (store *PostgresStore) PublishRuleTemplateRelease(
	ctx context.Context,
	tenantID, templateID string,
	req PublishRuleTemplateReleaseRequest,
	actorID, actorName string,
	now time.Time,
) (RuleBaselineTemplateRelease, error) {
	if store == nil || store.pool == nil || store.newID == nil {
		return RuleBaselineTemplateRelease{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(templateID) {
		return RuleBaselineTemplateRelease{}, ErrNotFound
	}
	note, err := validateChangeNote(req.ChangeNote)
	if err != nil {
		return RuleBaselineTemplateRelease{}, &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: err.Error()}
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return RuleBaselineTemplateRelease{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var specBytes []byte
	tmpl, err := scanRuleBaselineTemplate(tx.QueryRow(ctx, ruleTemplateSelectSQL+`
WHERE t.tenant_id = $1 AND t.id = $2 FOR UPDATE OF t`, tenantID, templateID), &specBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuleBaselineTemplateRelease{}, ErrNotFound
	}
	if err != nil {
		return RuleBaselineTemplateRelease{}, err
	}
	specCopy := req.RuleSpec
	if err := normalizeAndValidateTemplateSpec(&specCopy, tmpl); err != nil {
		return RuleBaselineTemplateRelease{}, err
	}
	specJSON, err := json.Marshal(specCopy)
	if err != nil {
		return RuleBaselineTemplateRelease{}, err
	}
	nextVersion := tmpl.LatestVersion + 1
	relID, err := store.newID(now)
	if err != nil {
		return RuleBaselineTemplateRelease{}, err
	}
	by, byName := normalizeRuleActor(actorID, actorName)
	if _, err := tx.Exec(ctx, `
INSERT INTO alarm_runtime.rule_baseline_template_release (
	id, tenant_id, template_id, version, rule_spec, change_note, published_by, published_by_name, published_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		relID, tenantID, templateID, nextVersion, specJSON, note, by, byName, now.UTC()); err != nil {
		return RuleBaselineTemplateRelease{}, fmt.Errorf("insert template release: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE alarm_runtime.rule_baseline_template
SET latest_version = $3, rule_kind = $4, default_severity = $5, default_execution_target = $6,
    updated_by = $7, updated_at = $8
WHERE tenant_id = $1 AND id = $2`,
		tenantID, templateID, nextVersion, string(specCopy.RuleKind), string(specCopy.Severity),
		string(specCopy.ExecutionTarget), by, now.UTC()); err != nil {
		return RuleBaselineTemplateRelease{}, fmt.Errorf("advance template latest_version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RuleBaselineTemplateRelease{}, fmt.Errorf("commit template release: %w", err)
	}
	return RuleBaselineTemplateRelease{
		ReleaseID:       relID,
		TemplateID:      templateID,
		Version:         nextVersion,
		RuleSpec:        specCopy,
		ChangeNote:      note,
		PublishedBy:     by,
		PublishedByName: byName,
		PublishedAt:     now.UTC().Format(time.RFC3339Nano),
	}, nil
}

func (store *PostgresStore) UpdateRuleTemplateSites(
	ctx context.Context,
	tenantID, templateID string,
	siteIDs []string,
	actorID string,
	now time.Time,
) (RuleBaselineTemplate, error) {
	if store == nil || store.pool == nil {
		return RuleBaselineTemplate{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(templateID) {
		return RuleBaselineTemplate{}, ErrNotFound
	}
	cleaned, err := validateBoundSiteList(siteIDs)
	if err != nil {
		return RuleBaselineTemplate{}, err
	}
	boundJSON, err := json.Marshal(cleaned)
	if err != nil {
		return RuleBaselineTemplate{}, err
	}
	by, _ := normalizeRuleActor(actorID, "")
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return RuleBaselineTemplate{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
UPDATE alarm_runtime.rule_baseline_template
SET bound_site_ids = $3, updated_by = $4, updated_at = $5
WHERE tenant_id = $1 AND id = $2`, tenantID, templateID, boundJSON, by, now.UTC())
	if err != nil {
		return RuleBaselineTemplate{}, fmt.Errorf("update template bound_site_ids: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return RuleBaselineTemplate{}, ErrNotFound
	}
	var specBytes []byte
	updated, err := scanRuleBaselineTemplate(tx.QueryRow(ctx, ruleTemplateSelectSQL+`
WHERE t.tenant_id = $1 AND t.id = $2`, tenantID, templateID), &specBytes)
	if err != nil {
		return RuleBaselineTemplate{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RuleBaselineTemplate{}, err
	}
	return updated, nil
}

func loadTemplateReleaseInTx(ctx context.Context, tx pgx.Tx, tenantID, templateID string, version int) (RuleBaselineTemplate, RuleBaselineTemplateRelease, error) {
	var specBytes []byte
	tmpl, err := scanRuleBaselineTemplate(tx.QueryRow(ctx, ruleTemplateSelectSQL+`
WHERE t.tenant_id = $1 AND t.id = $2`, tenantID, templateID), &specBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuleBaselineTemplate{}, RuleBaselineTemplateRelease{}, ErrNotFound
	}
	if err != nil {
		return RuleBaselineTemplate{}, RuleBaselineTemplateRelease{}, err
	}
	targetVer := version
	if targetVer <= 0 {
		targetVer = tmpl.LatestVersion
	}
	if targetVer <= 0 {
		return tmpl, RuleBaselineTemplateRelease{}, &RuleTemplateValidationError{
			Code:    "ALARM_TEMPLATE_NO_RELEASE",
			Message: "template has no published baseline release yet",
		}
	}
	var rel RuleBaselineTemplateRelease
	var specJSON []byte
	var publishedAt time.Time
	err = tx.QueryRow(ctx, `
SELECT id::text, template_id::text, version, rule_spec, change_note, published_by, published_by_name, published_at
FROM alarm_runtime.rule_baseline_template_release
WHERE tenant_id = $1 AND template_id = $2 AND version = $3`,
		tenantID, templateID, targetVer).Scan(&rel.ReleaseID, &rel.TemplateID, &rel.Version, &specJSON, &rel.ChangeNote, &rel.PublishedBy, &rel.PublishedByName, &publishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return tmpl, RuleBaselineTemplateRelease{}, ErrNotFound
	}
	if err != nil {
		return tmpl, RuleBaselineTemplateRelease{}, err
	}
	if err := json.Unmarshal(specJSON, &rel.RuleSpec); err != nil {
		return tmpl, RuleBaselineTemplateRelease{}, err
	}
	rel.PublishedAt = publishedAt.UTC().Format(time.RFC3339Nano)
	return tmpl, rel, nil
}

func (store *PostgresStore) PreviewRuleTemplateRollout(
	ctx context.Context,
	tenantID, templateID string,
	req PreviewRuleTemplateRolloutRequest,
) (RuleTemplateRolloutPreview, error) {
	if store == nil || store.pool == nil {
		return RuleTemplateRolloutPreview{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(templateID) {
		return RuleTemplateRolloutPreview{}, ErrNotFound
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return RuleTemplateRolloutPreview{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tmpl, rel, err := loadTemplateReleaseInTx(ctx, tx, tenantID, templateID, req.Version)
	if err != nil {
		return RuleTemplateRolloutPreview{}, err
	}
	targets, err := normalizeTargetSites(tmpl.BoundSiteIDs, req.SiteIDs, req.Targets)
	if err != nil {
		return RuleTemplateRolloutPreview{}, err
	}
	preview := RuleTemplateRolloutPreview{
		TemplateID:      tmpl.TemplateID,
		TemplateKey:     tmpl.TemplateKey,
		DisplayName:     tmpl.DisplayName,
		TemplateVersion: rel.Version,
		Sites:           make([]RuleTemplateSitePreview, 0, len(targets)),
	}
	for _, target := range targets {
		meta, err := latestSiteRuleBundleMetaInTx(ctx, tx, tenantID, target.SiteID)
		if err != nil {
			return RuleTemplateRolloutPreview{}, err
		}
		active, err := siteRulesInTx(ctx, tx, tenantID, target.SiteID)
		if err != nil {
			return RuleTemplateRolloutPreview{}, err
		}
		sitePrev := RuleTemplateSitePreview{
			SiteID:         target.SiteID,
			BundleRevision: meta.BundleRevision,
			RuleDiffs:      []RuleTemplateSiteRuleDiff{},
		}
		for _, rule := range active {
			if rule.Policy.TemplateKey != tmpl.TemplateKey {
				continue
			}
			sitePrev.MatchedRuleCount++
			_, changedFields, preservedFields, conflicts, _, err := compareRuleWithTemplate(rule, rel.RuleSpec)
			if err != nil {
				return RuleTemplateRolloutPreview{}, err
			}
			action := "UNCHANGED"
			if len(changedFields) > 0 {
				action = "UPDATE"
				sitePrev.WillUpdateCount++
			}
			sitePrev.PreservedOverrideCount += len(preservedFields)
			sitePrev.ConflictCount += len(conflicts)
			sitePrev.RuleDiffs = append(sitePrev.RuleDiffs, RuleTemplateSiteRuleDiff{
				RuleID:             rule.AssignmentID,
				RuleTitle:          rule.Policy.Title,
				Action:             action,
				ChangedFields:      changedFields,
				PreservedOverrides: preservedFields,
				Conflicts:          conflicts,
			})
		}
		if sitePrev.MatchedRuleCount == 0 {
			if target.DeviceID != "" {
				sitePrev.WillCreateCount = 1
				sitePrev.RuleDiffs = append(sitePrev.RuleDiffs, RuleTemplateSiteRuleDiff{
					RuleID:             "",
					RuleTitle:          rel.RuleSpec.Title,
					Action:             "CREATE",
					ChangedFields:      []string{"title", "severity", "executionTarget", "raise", "clear"},
					PreservedOverrides: []string{},
					Conflicts:          []RuleTemplateFieldConflict{},
				})
			} else {
				sitePrev.Note = "站点暂无关联该模板的派生规则；指定默认设备或先在站点绑定后可自动创建"
			}
		}
		if sitePrev.WillUpdateCount > 0 || sitePrev.WillCreateCount > 0 {
			preview.TotalChangedSites++
		}
		preview.TotalPreservedOverrides += sitePrev.PreservedOverrideCount
		preview.TotalConflicts += sitePrev.ConflictCount
		preview.Sites = append(preview.Sites, sitePrev)
	}
	return preview, nil
}

type ruleCacheUpdate struct {
	siteID          string
	bundleRevision  uint64
	assignmentCount uint64
	rules           []SiteRule
}

func (store *PostgresStore) ExecuteRuleTemplateRollout(
	ctx context.Context,
	tenantID, templateID string,
	req ExecuteRuleTemplateRolloutRequest,
	actorID, actorName string,
	now time.Time,
) (RuleTemplateRolloutResult, error) {
	if store == nil || store.pool == nil || store.newID == nil {
		return RuleTemplateRolloutResult{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(templateID) {
		return RuleTemplateRolloutResult{}, ErrNotFound
	}
	note, err := validateChangeNote(req.ChangeNote)
	if err != nil {
		return RuleTemplateRolloutResult{}, &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: err.Error()}
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return RuleTemplateRolloutResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tmpl, rel, err := loadTemplateReleaseInTx(ctx, tx, tenantID, templateID, req.Version)
	if err != nil {
		return RuleTemplateRolloutResult{}, err
	}
	targets, err := normalizeTargetSites(tmpl.BoundSiteIDs, req.SiteIDs, req.Targets)
	if err != nil {
		return RuleTemplateRolloutResult{}, err
	}
	batchID, err := store.newID(now)
	if err != nil {
		return RuleTemplateRolloutResult{}, err
	}
	by, byName := normalizeRuleActor(actorID, actorName)
	targetSiteIDs := make([]string, 0, len(targets))
	for _, t := range targets {
		targetSiteIDs = append(targetSiteIDs, t.SiteID)
	}

	result := RuleTemplateRolloutResult{
		BatchID:         batchID,
		TemplateID:      tmpl.TemplateID,
		TemplateVersion: rel.Version,
		CreatedBy:       by,
		CreatedAt:       now.UTC().Format(time.RFC3339Nano),
		Outcomes:        make([]RuleTemplateSiteRolloutOutcome, 0, len(targets)),
	}
	var cacheUpdates []ruleCacheUpdate

	for _, target := range targets {
		if err := lockSiteRulesStreamInTx(ctx, tx, tenantID, target.SiteID); err != nil {
			return RuleTemplateRolloutResult{}, err
		}
		latestMeta, err := latestSiteRuleBundleMetaInTx(ctx, tx, tenantID, target.SiteID)
		if err != nil {
			return RuleTemplateRolloutResult{}, err
		}
		active, err := siteRulesInTx(ctx, tx, tenantID, target.SiteID)
		if err != nil {
			return RuleTemplateRolloutResult{}, err
		}
		desired := make([]SiteRule, 0, len(active)+1)
		matched := 0
		changedCount := 0
		createdCount := 0
		preservedCount := 0
		conflictCount := 0
		for _, rule := range active {
			if rule.Policy.TemplateKey != tmpl.TemplateKey {
				desired = append(desired, SiteRule{AssignmentID: rule.AssignmentID, Policy: cloneAlarmPolicyRevision(rule.Policy)})
				continue
			}
			matched++
			merged, changedFields, preservedFields, conflicts, _, err := compareRuleWithTemplate(rule, rel.RuleSpec)
			if err != nil {
				return RuleTemplateRolloutResult{}, err
			}
			preservedCount += len(preservedFields)
			conflictCount += len(conflicts)
			if len(changedFields) > 0 {
				changedCount++
			}
			desired = append(desired, SiteRule{AssignmentID: rule.AssignmentID, Policy: merged})
		}
		if matched == 0 && target.DeviceID != "" {
			ruleID, err := store.newID(now)
			if err != nil {
				return RuleTemplateRolloutResult{}, err
			}
			createdRule, err := instantiateNewSiteRuleFromTemplate(ruleID, tmpl, rel.RuleSpec, target.DeviceID)
			if err != nil {
				return RuleTemplateRolloutResult{}, err
			}
			desired = append(desired, createdRule)
			createdCount = 1
		}

		outcome := RuleTemplateSiteRolloutOutcome{
			SiteID:                 target.SiteID,
			Status:                 "SKIPPED",
			BundleRevision:         latestMeta.BundleRevision,
			PreservedOverrideCount: preservedCount,
			ConflictCount:          conflictCount,
			ChangedRuleCount:       changedCount,
			CreatedRuleCount:       createdCount,
		}
		if matched == 0 && createdCount == 0 {
			outcome.Detail = "站点暂无关联该模板的规则，已跳过"
		} else if changedCount == 0 && createdCount == 0 {
			outcome.Detail = "目标规则已与模板一致或全量保留站点个性化覆写，无需下发"
		} else {
			plan, err := PlanSiteRules(desired, active)
			if err != nil {
				return RuleTemplateRolloutResult{}, &RuleDraftValidationError{
					Code:    "ALARM_RULE_GRAPH_INVALID",
					Message: err.Error(),
					Issues:  []SiteRuleValidationIssue{graphErrorValidationIssue(err)},
				}
			}
			summary, err := applySiteRulePlanInTx(ctx, tx, store, tenantID, target.SiteID, plan, now, by)
			if err != nil {
				return RuleTemplateRolloutResult{}, err
			}
			nextRules, err := siteRulesInTx(ctx, tx, tenantID, target.SiteID)
			if err != nil {
				return RuleTemplateRolloutResult{}, err
			}
			revNote := note
			if revNote == "" {
				revNote = fmt.Sprintf("下发基准模板 %s v%d（保留 %d 处站点覆写）", tmpl.DisplayName, rel.Version, preservedCount)
			}
			newRev := latestMeta.BundleRevision + 1
			if _, _, err := insertSiteRuleBundleRevisionInTx(
				ctx, tx, tenantID, target.SiteID, newRev, RuleBundleSourceTemplateRollout, nil, revNote, summary, nextRules, now, by, byName,
			); err != nil {
				return RuleTemplateRolloutResult{}, err
			}
			stampRev, assignCount, err := store.siteRuleCacheStampInTx(ctx, tx, tenantID, target.SiteID)
			if err != nil {
				return RuleTemplateRolloutResult{}, err
			}
			cacheUpdates = append(cacheUpdates, ruleCacheUpdate{
				siteID:          target.SiteID,
				bundleRevision:  stampRev,
				assignmentCount: assignCount,
				rules:           nextRules,
			})
			outcome.Status = "APPLIED"
			outcome.BundleRevision = newRev
			outcome.Detail = fmt.Sprintf("已同步至模板 v%d（更新 %d 条 / 新增 %d 条）", rel.Version, changedCount, createdCount)
			result.TotalAppliedSites++
		}
		result.TotalPreservedOverrides += preservedCount
		result.TotalConflicts += conflictCount
		result.Outcomes = append(result.Outcomes, outcome)
	}

	targetsJSON, err := json.Marshal(targetSiteIDs)
	if err != nil {
		return RuleTemplateRolloutResult{}, err
	}
	summaryJSON, err := json.Marshal(map[string]int{
		"appliedSites":       result.TotalAppliedSites,
		"preservedOverrides": result.TotalPreservedOverrides,
		"conflicts":          result.TotalConflicts,
	})
	if err != nil {
		return RuleTemplateRolloutResult{}, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO alarm_runtime.site_template_rollout_batch (
	id, tenant_id, template_id, template_version, target_site_ids, summary, created_by, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		batchID, tenantID, tmpl.TemplateID, rel.Version, targetsJSON, summaryJSON, by, now.UTC()); err != nil {
		return RuleTemplateRolloutResult{}, fmt.Errorf("record rollout batch: %w", err)
	}
	for _, outcome := range result.Outcomes {
		itemID, err := store.newID(now)
		if err != nil {
			return RuleTemplateRolloutResult{}, err
		}
		var revPtr *int64
		if outcome.BundleRevision > 0 {
			v := int64(outcome.BundleRevision)
			revPtr = &v
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO alarm_runtime.site_template_rollout_item (
	id, batch_id, tenant_id, site_id, bundle_revision, status, preserved_override_count, conflict_count, detail, applied_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			itemID, batchID, tenantID, outcome.SiteID, revPtr, outcome.Status,
			outcome.PreservedOverrideCount, outcome.ConflictCount, outcome.Detail, now.UTC()); err != nil {
			return RuleTemplateRolloutResult{}, fmt.Errorf("record rollout item: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return RuleTemplateRolloutResult{}, fmt.Errorf("commit template rollout: %w", err)
	}
	for _, u := range cacheUpdates {
		store.ruleCache.Put(tenantID, u.siteID, u.bundleRevision, u.assignmentCount, u.rules)
	}
	return result, nil
}

func (store *PostgresStore) GetSiteRuleTemplateDrift(ctx context.Context, tenantID, siteID string) (SiteRuleTemplateDriftReport, error) {
	if store == nil || store.pool == nil {
		return SiteRuleTemplateDriftReport{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(siteID) {
		return SiteRuleTemplateDriftReport{}, ErrNotFound
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return SiteRuleTemplateDriftReport{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	meta, err := latestSiteRuleBundleMetaInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRuleTemplateDriftReport{}, err
	}
	active, err := siteRulesInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRuleTemplateDriftReport{}, err
	}

	report := SiteRuleTemplateDriftReport{
		SiteID:         siteID,
		BundleRevision: meta.BundleRevision,
		Items:          []SiteRuleTemplateDriftItem{},
	}
	cache := make(map[string]RuleBaselineTemplate)
	for _, rule := range active {
		key := strings.TrimSpace(rule.Policy.TemplateKey)
		if key == "" {
			continue
		}
		tmpl, ok := cache[key]
		if !ok {
			var specBytes []byte
			loaded, err := scanRuleBaselineTemplate(tx.QueryRow(ctx, ruleTemplateSelectSQL+`
WHERE t.tenant_id = $1 AND t.template_key = $2`, tenantID, key), &specBytes)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return SiteRuleTemplateDriftReport{}, err
			}
			cache[key] = loaded
			tmpl = loaded
		}
		if tmpl.LatestSpec == nil {
			continue
		}
		_, _, _, _, diffs, err := compareRuleWithTemplate(rule, *tmpl.LatestSpec)
		if err != nil {
			return SiteRuleTemplateDriftReport{}, err
		}
		driftedFields := make([]string, 0)
		for _, d := range diffs {
			if d.Drifted {
				driftedFields = append(driftedFields, d.Field)
			}
		}
		overridden := slices.Clone(rule.Policy.OverriddenFields)
		if overridden == nil {
			overridden = []string{}
		}
		if len(overridden) == 0 && len(driftedFields) == 0 {
			continue
		}
		report.Items = append(report.Items, SiteRuleTemplateDriftItem{
			RuleID:           rule.AssignmentID,
			RuleTitle:        rule.Policy.Title,
			TemplateID:       tmpl.TemplateID,
			TemplateKey:      tmpl.TemplateKey,
			TemplateName:     tmpl.DisplayName,
			TemplateVersion:  tmpl.LatestVersion,
			OverriddenFields: overridden,
			DriftedFields:    driftedFields,
			Diffs:            diffs,
		})
	}
	return report, nil
}

func (store *PostgresStore) ResetSiteRuleToTemplate(
	ctx context.Context,
	tenantID, siteID, ruleID string,
	req ResetSiteRuleToTemplateRequest,
	actorID, actorName string,
	now time.Time,
) (SiteRulePublishResult, error) {
	if store == nil || store.pool == nil || store.newID == nil {
		return SiteRulePublishResult{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(siteID) || !alarmmodel.IsUUIDv7(ruleID) {
		return SiteRulePublishResult{}, ErrNotFound
	}
	note, err := validateChangeNote(req.ChangeNote)
	if err != nil {
		return SiteRulePublishResult{}, &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: err.Error()}
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockSiteRulesStreamInTx(ctx, tx, tenantID, siteID); err != nil {
		return SiteRulePublishResult{}, err
	}
	latestMeta, err := latestSiteRuleBundleMetaInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	active, err := siteRulesInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	var targetIdx = -1
	for i, rule := range active {
		if rule.AssignmentID == ruleID {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return SiteRulePublishResult{}, ErrNotFound
	}
	targetRule := active[targetIdx]
	if strings.TrimSpace(targetRule.Policy.TemplateKey) == "" {
		return SiteRulePublishResult{}, &RuleTemplateValidationError{
			Code:    "ALARM_TEMPLATE_INVALID",
			Message: "target site rule is not derived from any baseline template",
		}
	}
	var specBytes []byte
	tmpl, err := scanRuleBaselineTemplate(tx.QueryRow(ctx, ruleTemplateSelectSQL+`
WHERE t.tenant_id = $1 AND t.template_key = $2`, tenantID, targetRule.Policy.TemplateKey), &specBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return SiteRulePublishResult{}, ErrNotFound
	}
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	if tmpl.LatestSpec == nil {
		return SiteRulePublishResult{}, &RuleTemplateValidationError{
			Code:    "ALARM_TEMPLATE_NO_RELEASE",
			Message: "template has no published release",
		}
	}
	nextPolicy, err := resetSiteRulePolicyFields(targetRule, *tmpl.LatestSpec, req.Fields)
	if err != nil {
		return SiteRulePublishResult{}, err
	}

	desired := cloneSiteRules(active)
	desired[targetIdx] = SiteRule{AssignmentID: ruleID, Policy: nextPolicy}
	plan, err := PlanSiteRules(desired, active)
	if err != nil {
		return SiteRulePublishResult{}, &RuleDraftValidationError{
			Code:    "ALARM_RULE_GRAPH_INVALID",
			Message: err.Error(),
			Issues:  []SiteRuleValidationIssue{graphErrorValidationIssue(err)},
		}
	}
	by, byName := normalizeRuleActor(actorID, actorName)
	summary := SummarizeSiteRulePlan(plan)
	if !hasSiteRulePlanChanges(summary) {
		report, err := ValidateSiteRuleGraph(active)
		if err != nil {
			return SiteRulePublishResult{}, err
		}
		views, err := buildSiteRuleViews(active, report)
		if err != nil {
			return SiteRulePublishResult{}, err
		}
		return SiteRulePublishResult{
			BundleRevision:  latestMeta.BundleRevision,
			BundleDigest:    latestMeta.BundleDigest,
			Source:          RuleBundleSourceTemplateRollout,
			Changed:         false,
			ChangeNote:      note,
			PublishedAt:     now.UTC().Format(time.RFC3339Nano),
			PublishedBy:     by,
			PublishedByName: byName,
			Summary:         summary,
			Graph:           buildSiteRuleGraphView(active, report, nil),
			Items:           views,
		}, nil
	}

	if _, err := applySiteRulePlanInTx(ctx, tx, store, tenantID, siteID, plan, now, by); err != nil {
		return SiteRulePublishResult{}, err
	}
	nextRules, err := siteRulesInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	finalReport, err := ValidateSiteRuleGraph(nextRules)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	newRevision := latestMeta.BundleRevision + 1
	revNote := note
	if revNote == "" {
		revNote = fmt.Sprintf("重置规则 %s 至模板 %s v%d 默认值", targetRule.Policy.Title, tmpl.DisplayName, tmpl.LatestVersion)
	}
	digest, storedNote, err := insertSiteRuleBundleRevisionInTx(
		ctx, tx, tenantID, siteID, newRevision, RuleBundleSourceTemplateRollout, nil, revNote, summary, nextRules, now, by, byName,
	)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	bundleStamp, assignmentCount, err := store.siteRuleCacheStampInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	views, err := buildSiteRuleViews(nextRules, finalReport)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SiteRulePublishResult{}, fmt.Errorf("commit reset site rule to template: %w", err)
	}
	store.ruleCache.Put(tenantID, siteID, bundleStamp, assignmentCount, nextRules)
	return SiteRulePublishResult{
		BundleRevision:  newRevision,
		BundleDigest:    digest,
		Source:          RuleBundleSourceTemplateRollout,
		Changed:         true,
		ChangeNote:      storedNote,
		PublishedAt:     now.UTC().Format(time.RFC3339Nano),
		PublishedBy:     by,
		PublishedByName: byName,
		Summary:         summary,
		Graph:           buildSiteRuleGraphView(nextRules, finalReport, nil),
		Items:           views,
	}, nil
}
