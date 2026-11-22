package connectivity

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

var (
	ErrProvisioningInvalid  = errors.New("provisioning request is invalid")
	ErrProvisioningConflict = errors.New("provisioning resource conflict")
	ErrProvisioningNotFound = errors.New("provisioning resource not found")
)

var (
	jitpProductKeyPattern     = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$")
	jitpHardwareSerialPattern = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9._:-]{1,127}$")
)

const (
	defaultProvisioningExpiresHours = 720
	maxProvisioningExpiresHours     = 8760
	maxProvisioningBatchDevices     = 100000
	maxBatchDevicesPerCall          = 500
)

type ProvisioningBatch struct {
	ID               string    `json:"id"`
	TenantID         string    `json:"tenantId"`
	SiteID           string    `json:"siteId"`
	BatchName        string    `json:"batchName"`
	ProductKey       string    `json:"productKey"`
	DeviceType       string    `json:"deviceType"`
	ConnectivityMode string    `json:"connectivityMode"`
	MaxDevices       int       `json:"maxDevices"`
	ActivatedCount   int       `json:"activatedCount"`
	Status           string    `json:"status"`
	ExpiresAt        time.Time `json:"expiresAt"`
	CreatedBy        string    `json:"createdBy"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type ProvisioningBatchCreated struct {
	Batch         ProvisioningBatch         `json:"batch"`
	ProductSecret string                    `json:"productSecret"`
	Devices       []ProvisioningBatchDevice `json:"devices,omitempty"`
}

type ProvisioningBatchDevice struct {
	ID             string     `json:"id"`
	BatchID        string     `json:"batchId"`
	TenantID       string     `json:"tenantId"`
	SiteID         string     `json:"siteId"`
	DeviceID       string     `json:"deviceId"`
	HardwareSerial string     `json:"hardwareSerial"`
	Status         string     `json:"status"`
	ActivatedAt    *time.Time `json:"activatedAt,omitempty"`
	CredentialID   *string    `json:"credentialId,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

type JITPDeviceEntry struct {
	DeviceID       string `json:"deviceId"`
	HardwareSerial string `json:"hardwareSerial"`
}

type CreateProvisioningBatchRequest struct {
	SiteID           string            `json:"siteId"`
	BatchName        string            `json:"batchName"`
	ProductKey       string            `json:"productKey"`
	DeviceType       string            `json:"deviceType"`
	ConnectivityMode string            `json:"connectivityMode,omitempty"`
	MaxDevices       int               `json:"maxDevices"`
	ExpiresInHours   int               `json:"expiresInHours,omitempty"`
	Devices          []JITPDeviceEntry `json:"devices,omitempty"`
}

type RegisterBatchDevicesRequest struct {
	Devices []JITPDeviceEntry `json:"devices"`
}

type JITPEnrollRequest struct {
	ProductKey     string `json:"productKey"`
	ProductSecret  string `json:"productSecret"`
	HardwareSerial string `json:"hardwareSerial"`
	CSRPEM         string `json:"csrPem"`
}

func normalizeLowerUUID(raw string) (string, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if !gatewayIDPattern.MatchString(trimmed) {
		return "", false
	}
	return trimmed, true
}

func validateProvisioningEntries(entries []JITPDeviceEntry, requireNonEmpty bool) ([]JITPDeviceEntry, error) {
	if requireNonEmpty && len(entries) == 0 {
		return nil, ErrProvisioningInvalid
	}
	if len(entries) > maxBatchDevicesPerCall {
		return nil, ErrProvisioningInvalid
	}
	seenSerials := make(map[string]struct{}, len(entries))
	seenDevices := make(map[string]struct{}, len(entries))
	out := make([]JITPDeviceEntry, 0, len(entries))
	for _, entry := range entries {
		devID, ok := normalizeLowerUUID(entry.DeviceID)
		if !ok {
			return nil, ErrProvisioningInvalid
		}
		serial := strings.TrimSpace(entry.HardwareSerial)
		if !jitpHardwareSerialPattern.MatchString(serial) {
			return nil, ErrProvisioningInvalid
		}
		if _, dup := seenSerials[serial]; dup {
			return nil, ErrProvisioningInvalid
		}
		if _, dup := seenDevices[devID]; dup {
			return nil, ErrProvisioningInvalid
		}
		seenSerials[serial] = struct{}{}
		seenDevices[devID] = struct{}{}
		out = append(out, JITPDeviceEntry{DeviceID: devID, HardwareSerial: serial})
	}
	return out, nil
}

func mapProvisioningDBError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrProvisioningConflict
		case "23503":
			return ErrProvisioningNotFound
		case "23514":
			return ErrProvisioningInvalid
		}
	}
	return err
}

func normalizeBatchUTC(b ProvisioningBatch) ProvisioningBatch {
	b.ExpiresAt = b.ExpiresAt.UTC()
	b.CreatedAt = b.CreatedAt.UTC()
	b.UpdatedAt = b.UpdatedAt.UTC()
	return b
}

func normalizeBatchDeviceUTC(d ProvisioningBatchDevice) ProvisioningBatchDevice {
	if d.ActivatedAt != nil {
		utc := d.ActivatedAt.UTC()
		d.ActivatedAt = &utc
	}
	d.CreatedAt = d.CreatedAt.UTC()
	d.UpdatedAt = d.UpdatedAt.UTC()
	return d
}

func (service *CredentialService) verifyAndInsertBatchDevicesTx(
	ctx context.Context,
	tx pgx.Tx,
	batch ProvisioningBatch,
	entries []JITPDeviceEntry,
	now time.Time,
) ([]ProvisioningBatchDevice, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	deviceIDs := make([]string, len(entries))
	for i, e := range entries {
		deviceIDs[i] = e.DeviceID
	}
	rows, err := tx.Query(ctx, `
SELECT gateway_id::text, device_type, connectivity_mode
FROM core_registry.gateway_directory_v1
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND gateway_id = ANY($3::uuid[])
  AND status = 'ACTIVE'
`, batch.TenantID, batch.SiteID, deviceIDs)
	if err != nil {
		return nil, fmt.Errorf("verify provisioning batch devices: %w", err)
	}
	type dirInfo struct {
		deviceType       string
		connectivityMode string
	}
	dirMap := make(map[string]dirInfo, len(entries))
	for rows.Next() {
		var id, dt, cm string
		if scanErr := rows.Scan(&id, &dt, &cm); scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		dirMap[strings.ToLower(strings.TrimSpace(id))] = dirInfo{deviceType: dt, connectivityMode: cm}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		return nil, rowsErr
	}
	rows.Close()

	for _, e := range entries {
		info, found := dirMap[e.DeviceID]
		if !found {
			return nil, ErrProvisioningNotFound
		}
		if info.deviceType != batch.DeviceType || info.connectivityMode != batch.ConnectivityMode {
			return nil, ErrProvisioningInvalid
		}
	}

	inserted := make([]ProvisioningBatchDevice, 0, len(entries))
	for _, e := range entries {
		rowID, idErr := newUUIDv7(now)
		if idErr != nil {
			return nil, idErr
		}
		var dev ProvisioningBatchDevice
		err := tx.QueryRow(ctx, `
INSERT INTO connectivity.provisioning_batch_devices (
  id, batch_id, tenant_id, site_id, device_id, hardware_serial, status, activated_at, credential_id, created_at, updated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, 'PENDING', NULL, NULL, $7, $7)
RETURNING id::text, batch_id::text, tenant_id::text, site_id::text, device_id::text, hardware_serial, status, activated_at, credential_id::text, created_at, updated_at
`, rowID, batch.ID, batch.TenantID, batch.SiteID, e.DeviceID, e.HardwareSerial, now).Scan(
			&dev.ID, &dev.BatchID, &dev.TenantID, &dev.SiteID, &dev.DeviceID, &dev.HardwareSerial,
			&dev.Status, &dev.ActivatedAt, &dev.CredentialID, &dev.CreatedAt, &dev.UpdatedAt,
		)
		if err != nil {
			return nil, mapProvisioningDBError(err)
		}
		inserted = append(inserted, normalizeBatchDeviceUTC(dev))
	}
	return inserted, nil
}

// CreateProvisioningBatch registers one JITP product-level provisioning batch with
// SHA-256 secret storage and optional initial hardware serial -> deviceId mappings.
func (service *CredentialService) CreateProvisioningBatch(
	ctx context.Context,
	claims registryauth.GrantClaims,
	req CreateProvisioningBatchRequest,
) (ProvisioningBatchCreated, error) {
	if service == nil || service.store == nil || service.store.pool == nil {
		return ProvisioningBatchCreated{}, errors.New("credential service is not configured")
	}
	siteID, ok := normalizeLowerUUID(req.SiteID)
	if !ok {
		return ProvisioningBatchCreated{}, ErrProvisioningInvalid
	}
	if _, ok := normalizeLowerUUID(claims.TenantID); !ok {
		return ProvisioningBatchCreated{}, ErrCredentialForbidden
	}
	if !registryauth.ScopeAllows(claims, siteID) {
		return ProvisioningBatchCreated{}, ErrCredentialForbidden
	}
	batchName := strings.TrimSpace(req.BatchName)
	if batchName == "" || utf8.RuneCountInString(batchName) > 128 {
		return ProvisioningBatchCreated{}, ErrProvisioningInvalid
	}
	productKey := strings.TrimSpace(req.ProductKey)
	if !jitpProductKeyPattern.MatchString(productKey) {
		return ProvisioningBatchCreated{}, ErrProvisioningInvalid
	}
	deviceType := strings.TrimSpace(req.DeviceType)
	if deviceType == "" || utf8.RuneCountInString(deviceType) > 64 {
		return ProvisioningBatchCreated{}, ErrProvisioningInvalid
	}
	connectivityMode := strings.TrimSpace(req.ConnectivityMode)
	if connectivityMode == "" {
		if deviceType == "GATEWAY" {
			connectivityMode = "GATEWAY_PROXIED"
		} else {
			connectivityMode = "DIRECT"
		}
	}
	if connectivityMode != "GATEWAY_PROXIED" && connectivityMode != "DIRECT" {
		return ProvisioningBatchCreated{}, ErrProvisioningInvalid
	}
	if deviceType != "GATEWAY" && connectivityMode != "DIRECT" {
		return ProvisioningBatchCreated{}, ErrProvisioningInvalid
	}
	if req.MaxDevices < 1 || req.MaxDevices > maxProvisioningBatchDevices {
		return ProvisioningBatchCreated{}, ErrProvisioningInvalid
	}
	expiresHours := req.ExpiresInHours
	if expiresHours == 0 {
		expiresHours = defaultProvisioningExpiresHours
	}
	if expiresHours < 1 || expiresHours > maxProvisioningExpiresHours {
		return ProvisioningBatchCreated{}, ErrProvisioningInvalid
	}
	entries, err := validateProvisioningEntries(req.Devices, false)
	if err != nil {
		return ProvisioningBatchCreated{}, err
	}
	if len(entries) > req.MaxDevices {
		return ProvisioningBatchCreated{}, ErrProvisioningConflict
	}

	var rawSecret [32]byte
	if _, err := rand.Read(rawSecret[:]); err != nil {
		return ProvisioningBatchCreated{}, err
	}
	productSecret := base64.RawURLEncoding.EncodeToString(rawSecret[:])
	secretDigest := hashCredential([]byte(productSecret))

	now := service.store.clock().UTC()
	batchID, err := newUUIDv7(now)
	if err != nil {
		return ProvisioningBatchCreated{}, err
	}
	expiresAt := now.Add(time.Duration(expiresHours) * time.Hour)
	actorID := claims.PrincipalID
	if normalizedActor, ok := normalizeLowerUUID(actorID); ok {
		actorID = normalizedActor
	} else {
		return ProvisioningBatchCreated{}, ErrCredentialForbidden
	}

	tx, err := service.beginTenant(ctx, claims.TenantID)
	if err != nil {
		return ProvisioningBatchCreated{}, err
	}
	defer tx.Rollback(ctx)

	var batch ProvisioningBatch
	err = tx.QueryRow(ctx, `
INSERT INTO connectivity.provisioning_batches (
  id, tenant_id, site_id, batch_name, product_key, product_secret_sha256,
  device_type, connectivity_mode, max_devices, activated_count, status, expires_at,
  created_by, created_at, updated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, 0, 'ACTIVE', $10, $11::uuid, $12, $12)
RETURNING id::text, tenant_id::text, site_id::text, batch_name, product_key, device_type,
          connectivity_mode, max_devices, activated_count, status, expires_at, created_by::text, created_at, updated_at
`, batchID, claims.TenantID, siteID, batchName, productKey, secretDigest,
		deviceType, connectivityMode, req.MaxDevices, expiresAt, actorID, now).Scan(
		&batch.ID, &batch.TenantID, &batch.SiteID, &batch.BatchName, &batch.ProductKey,
		&batch.DeviceType, &batch.ConnectivityMode, &batch.MaxDevices, &batch.ActivatedCount,
		&batch.Status, &batch.ExpiresAt, &batch.CreatedBy, &batch.CreatedAt, &batch.UpdatedAt,
	)
	if err != nil {
		return ProvisioningBatchCreated{}, mapProvisioningDBError(err)
	}
	batch = normalizeBatchUTC(batch)

	devices, err := service.verifyAndInsertBatchDevicesTx(ctx, tx, batch, entries, now)
	if err != nil {
		return ProvisioningBatchCreated{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProvisioningBatchCreated{}, err
	}
	return ProvisioningBatchCreated{
		Batch:         batch,
		ProductSecret: productSecret,
		Devices:       devices,
	}, nil
}

// ListProvisioningBatches lists JITP batches scoped strictly to claims.TenantID and authorized sites.
func (service *CredentialService) ListProvisioningBatches(
	ctx context.Context,
	claims registryauth.GrantClaims,
	siteIDFilter string,
) ([]ProvisioningBatch, error) {
	if _, ok := normalizeLowerUUID(claims.TenantID); !ok {
		return nil, ErrCredentialForbidden
	}
	var allowedSites []string
	trimmedFilter := strings.TrimSpace(siteIDFilter)
	if trimmedFilter != "" {
		normalizedFilter, ok := normalizeLowerUUID(trimmedFilter)
		if !ok {
			return nil, ErrProvisioningInvalid
		}
		if !registryauth.ScopeAllows(claims, normalizedFilter) {
			return nil, ErrCredentialForbidden
		}
		allowedSites = []string{normalizedFilter}
	} else {
		for _, candidate := range claims.AllowedSiteIDs {
			if registryauth.ScopeAllows(claims, candidate) {
				allowedSites = append(allowedSites, candidate)
			}
		}
	}
	if len(allowedSites) == 0 {
		return []ProvisioningBatch{}, nil
	}
	if service == nil || service.store == nil || service.store.pool == nil {
		return nil, errors.New("credential service is not configured")
	}

	tx, err := service.beginTenant(ctx, claims.TenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, batch_name, product_key, device_type,
       connectivity_mode, max_devices, activated_count, status, expires_at, created_by::text, created_at, updated_at
FROM connectivity.provisioning_batches
WHERE site_id = ANY($1::uuid[])
ORDER BY created_at DESC, id DESC
`, allowedSites)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	batches := make([]ProvisioningBatch, 0)
	for rows.Next() {
		var b ProvisioningBatch
		if scanErr := rows.Scan(
			&b.ID, &b.TenantID, &b.SiteID, &b.BatchName, &b.ProductKey,
			&b.DeviceType, &b.ConnectivityMode, &b.MaxDevices, &b.ActivatedCount,
			&b.Status, &b.ExpiresAt, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt,
		); scanErr != nil {
			return nil, scanErr
		}
		batches = append(batches, normalizeBatchUTC(b))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return batches, nil
}

// GetProvisioningBatch reads one JITP batch and its pre-registered devices after checking Site scope.
func (service *CredentialService) GetProvisioningBatch(
	ctx context.Context,
	claims registryauth.GrantClaims,
	rawBatchID string,
) (ProvisioningBatch, []ProvisioningBatchDevice, error) {
	batchID, ok := normalizeLowerUUID(rawBatchID)
	if !ok {
		return ProvisioningBatch{}, nil, ErrProvisioningInvalid
	}
	if _, ok := normalizeLowerUUID(claims.TenantID); !ok {
		return ProvisioningBatch{}, nil, ErrCredentialForbidden
	}
	if service == nil || service.store == nil || service.store.pool == nil {
		return ProvisioningBatch{}, nil, errors.New("credential service is not configured")
	}

	tx, err := service.beginTenant(ctx, claims.TenantID)
	if err != nil {
		return ProvisioningBatch{}, nil, err
	}
	defer tx.Rollback(ctx)

	var batch ProvisioningBatch
	err = tx.QueryRow(ctx, `
SELECT id::text, tenant_id::text, site_id::text, batch_name, product_key, device_type,
       connectivity_mode, max_devices, activated_count, status, expires_at, created_by::text, created_at, updated_at
FROM connectivity.provisioning_batches
WHERE id = $1::uuid
`, batchID).Scan(
		&batch.ID, &batch.TenantID, &batch.SiteID, &batch.BatchName, &batch.ProductKey,
		&batch.DeviceType, &batch.ConnectivityMode, &batch.MaxDevices, &batch.ActivatedCount,
		&batch.Status, &batch.ExpiresAt, &batch.CreatedBy, &batch.CreatedAt, &batch.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProvisioningBatch{}, nil, ErrProvisioningNotFound
	}
	if err != nil {
		return ProvisioningBatch{}, nil, err
	}
	if !registryauth.ScopeAllows(claims, batch.SiteID) {
		return ProvisioningBatch{}, nil, ErrCredentialForbidden
	}
	batch = normalizeBatchUTC(batch)

	rows, err := tx.Query(ctx, `
SELECT id::text, batch_id::text, tenant_id::text, site_id::text, device_id::text, hardware_serial,
       status, activated_at, credential_id::text, created_at, updated_at
FROM connectivity.provisioning_batch_devices
WHERE batch_id = $1::uuid
ORDER BY created_at ASC, id ASC
`, batchID)
	if err != nil {
		return ProvisioningBatch{}, nil, err
	}
	defer rows.Close()

	devices := make([]ProvisioningBatchDevice, 0)
	for rows.Next() {
		var dev ProvisioningBatchDevice
		if scanErr := rows.Scan(
			&dev.ID, &dev.BatchID, &dev.TenantID, &dev.SiteID, &dev.DeviceID, &dev.HardwareSerial,
			&dev.Status, &dev.ActivatedAt, &dev.CredentialID, &dev.CreatedAt, &dev.UpdatedAt,
		); scanErr != nil {
			return ProvisioningBatch{}, nil, scanErr
		}
		devices = append(devices, normalizeBatchDeviceUTC(dev))
	}
	if err := rows.Err(); err != nil {
		return ProvisioningBatch{}, nil, err
	}
	return batch, devices, nil
}

// RegisterBatchDevices appends pre-registered hardware serial -> deviceId mappings onto a batch.
func (service *CredentialService) RegisterBatchDevices(
	ctx context.Context,
	claims registryauth.GrantClaims,
	rawBatchID string,
	req RegisterBatchDevicesRequest,
) ([]ProvisioningBatchDevice, error) {
	batchID, ok := normalizeLowerUUID(rawBatchID)
	if !ok {
		return nil, ErrProvisioningInvalid
	}
	if _, ok := normalizeLowerUUID(claims.TenantID); !ok {
		return nil, ErrCredentialForbidden
	}
	entries, err := validateProvisioningEntries(req.Devices, true)
	if err != nil {
		return nil, err
	}
	if service == nil || service.store == nil || service.store.pool == nil {
		return nil, errors.New("credential service is not configured")
	}

	tx, err := service.beginTenant(ctx, claims.TenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var batch ProvisioningBatch
	err = tx.QueryRow(ctx, `
SELECT id::text, tenant_id::text, site_id::text, batch_name, product_key, device_type,
       connectivity_mode, max_devices, activated_count, status, expires_at, created_by::text, created_at, updated_at
FROM connectivity.provisioning_batches
WHERE id = $1::uuid
FOR UPDATE
`, batchID).Scan(
		&batch.ID, &batch.TenantID, &batch.SiteID, &batch.BatchName, &batch.ProductKey,
		&batch.DeviceType, &batch.ConnectivityMode, &batch.MaxDevices, &batch.ActivatedCount,
		&batch.Status, &batch.ExpiresAt, &batch.CreatedBy, &batch.CreatedAt, &batch.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProvisioningNotFound
	}
	if err != nil {
		return nil, err
	}
	if !registryauth.ScopeAllows(claims, batch.SiteID) {
		return nil, ErrCredentialForbidden
	}
	if batch.Status == "REVOKED" {
		return nil, ErrProvisioningConflict
	}

	var existingActiveCount int
	if err := tx.QueryRow(ctx, `
SELECT count(*)
FROM connectivity.provisioning_batch_devices
WHERE batch_id = $1::uuid AND status <> 'REVOKED'
`, batchID).Scan(&existingActiveCount); err != nil {
		return nil, err
	}
	if existingActiveCount+len(entries) > batch.MaxDevices {
		return nil, ErrProvisioningConflict
	}

	now := service.store.clock().UTC()
	devices, err := service.verifyAndInsertBatchDevicesTx(ctx, tx, batch, entries, now)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE connectivity.provisioning_batches SET updated_at = $2 WHERE id = $1::uuid`, batchID, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return devices, nil
}

// RevokeProvisioningBatch revokes one JITP batch and every remaining PENDING registration inside it.
func (service *CredentialService) RevokeProvisioningBatch(
	ctx context.Context,
	claims registryauth.GrantClaims,
	rawBatchID string,
) (ProvisioningBatch, error) {
	batchID, ok := normalizeLowerUUID(rawBatchID)
	if !ok {
		return ProvisioningBatch{}, ErrProvisioningInvalid
	}
	if _, ok := normalizeLowerUUID(claims.TenantID); !ok {
		return ProvisioningBatch{}, ErrCredentialForbidden
	}
	if service == nil || service.store == nil || service.store.pool == nil {
		return ProvisioningBatch{}, errors.New("credential service is not configured")
	}

	tx, err := service.beginTenant(ctx, claims.TenantID)
	if err != nil {
		return ProvisioningBatch{}, err
	}
	defer tx.Rollback(ctx)

	var batch ProvisioningBatch
	err = tx.QueryRow(ctx, `
SELECT id::text, tenant_id::text, site_id::text, batch_name, product_key, device_type,
       connectivity_mode, max_devices, activated_count, status, expires_at, created_by::text, created_at, updated_at
FROM connectivity.provisioning_batches
WHERE id = $1::uuid
FOR UPDATE
`, batchID).Scan(
		&batch.ID, &batch.TenantID, &batch.SiteID, &batch.BatchName, &batch.ProductKey,
		&batch.DeviceType, &batch.ConnectivityMode, &batch.MaxDevices, &batch.ActivatedCount,
		&batch.Status, &batch.ExpiresAt, &batch.CreatedBy, &batch.CreatedAt, &batch.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProvisioningBatch{}, ErrProvisioningNotFound
	}
	if err != nil {
		return ProvisioningBatch{}, err
	}
	if !registryauth.ScopeAllows(claims, batch.SiteID) {
		return ProvisioningBatch{}, ErrCredentialForbidden
	}

	now := service.store.clock().UTC()
	if err := tx.QueryRow(ctx, `
UPDATE connectivity.provisioning_batches
SET status = 'REVOKED', updated_at = $2
WHERE id = $1::uuid
RETURNING status, updated_at
`, batchID, now).Scan(&batch.Status, &batch.UpdatedAt); err != nil {
		return ProvisioningBatch{}, err
	}
	if _, err := tx.Exec(ctx, `
UPDATE connectivity.provisioning_batch_devices
SET status = 'REVOKED', updated_at = $2
WHERE batch_id = $1::uuid AND status = 'PENDING'
`, batchID, now); err != nil {
		return ProvisioningBatch{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProvisioningBatch{}, err
	}
	return normalizeBatchUTC(batch), nil
}

// EnrollJITP exchanges factory product credentials and a CSR (with CN = hardwareSerial or deviceId)
// for a unique per-device mTLS certificate bound to CN = deviceId.
func (service *CredentialService) EnrollJITP(ctx context.Context, req JITPEnrollRequest) (IssuedCredential, error) {
	productKey := strings.TrimSpace(req.ProductKey)
	if !jitpProductKeyPattern.MatchString(productKey) || len(req.ProductSecret) != 43 {
		return IssuedCredential{}, ErrEnrollmentRejected
	}
	hardwareSerial := strings.TrimSpace(req.HardwareSerial)
	if !jitpHardwareSerialPattern.MatchString(hardwareSerial) {
		return IssuedCredential{}, ErrEnrollmentRejected
	}
	if service == nil || service.authority == nil || service.store == nil || service.store.pool == nil {
		return IssuedCredential{}, errors.New("credential service is not configured")
	}

	var batchID, tenantID, siteID string
	err := service.store.pool.QueryRow(ctx, `
SELECT batch_id::text, tenant_id::text, site_id::text
FROM connectivity.resolve_provisioning_batch_tenant($1)
`, productKey).Scan(&batchID, &tenantID, &siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return IssuedCredential{}, ErrEnrollmentRejected
	}
	if err != nil {
		return IssuedCredential{}, err
	}

	tx, err := service.beginTenant(ctx, tenantID)
	if err != nil {
		return IssuedCredential{}, err
	}
	defer tx.Rollback(ctx)

	var secretHash, batchDeviceType, batchConnMode, batchStatus string
	var maxDevices, activatedCount int
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
SELECT product_secret_sha256, device_type, connectivity_mode, max_devices, activated_count, status, expires_at
FROM connectivity.provisioning_batches
WHERE id = $1::uuid
FOR UPDATE
`, batchID).Scan(&secretHash, &batchDeviceType, &batchConnMode, &maxDevices, &activatedCount, &batchStatus, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return IssuedCredential{}, ErrEnrollmentRejected
	}
	if err != nil {
		return IssuedCredential{}, err
	}

	presentedHash := hashCredential([]byte(req.ProductSecret))
	if subtle.ConstantTimeCompare([]byte(presentedHash), []byte(secretHash)) != 1 {
		return IssuedCredential{}, ErrEnrollmentRejected
	}
	now := service.store.clock().UTC()
	if batchStatus != "ACTIVE" || !expiresAt.After(now) || activatedCount >= maxDevices {
		return IssuedCredential{}, ErrEnrollmentRejected
	}

	var batchDeviceID, deviceID, batchDeviceStatus string
	err = tx.QueryRow(ctx, `
SELECT id::text, device_id::text, status
FROM connectivity.provisioning_batch_devices
WHERE batch_id = $1::uuid AND hardware_serial = $2
FOR UPDATE
`, batchID, hardwareSerial).Scan(&batchDeviceID, &deviceID, &batchDeviceStatus)
	if errors.Is(err, pgx.ErrNoRows) || batchDeviceStatus != "PENDING" {
		return IssuedCredential{}, ErrEnrollmentRejected
	}
	if err != nil {
		return IssuedCredential{}, err
	}

	if err := acquireEnrollmentAdvisoryLock(ctx, tx, deviceID); err != nil {
		return IssuedCredential{}, err
	}

	var dirDeviceType, dirConnMode, dirStatus string
	err = tx.QueryRow(ctx, `
SELECT device_type, connectivity_mode, status
FROM core_registry.gateway_directory_v1
WHERE gateway_id = $1::uuid AND tenant_id = $2::uuid AND site_id = $3::uuid
`, deviceID, tenantID, siteID).Scan(&dirDeviceType, &dirConnMode, &dirStatus)
	if errors.Is(err, pgx.ErrNoRows) || dirStatus != "ACTIVE" || dirDeviceType != batchDeviceType || dirConnMode != batchConnMode {
		return IssuedCredential{}, ErrEnrollmentRejected
	}
	if err != nil {
		return IssuedCredential{}, err
	}

	csr, err := parseJITPCSR(req.CSRPEM, hardwareSerial, deviceID)
	if err != nil {
		return IssuedCredential{}, err
	}

	var revokedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT revoked_at FROM connectivity.gateway_enrollments WHERE gateway_id = $1::uuid FOR UPDATE`, deviceID).Scan(&revokedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return IssuedCredential{}, err
	}
	if err == nil && revokedAt != nil {
		return IssuedCredential{}, ErrCredentialRevoked
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO connectivity.gateway_enrollments(tenant_id, gateway_id, code_sha256, expires_at, consumed_at, revoked_at, created_at, updated_at)
VALUES($1::uuid, $2::uuid, NULL, NULL, $3, NULL, $3, $3)
ON CONFLICT(tenant_id, gateway_id) DO UPDATE
SET code_sha256 = NULL, expires_at = NULL, consumed_at = EXCLUDED.consumed_at, updated_at = EXCLUDED.updated_at
WHERE connectivity.gateway_enrollments.revoked_at IS NULL
`, tenantID, deviceID, now); err != nil {
		return IssuedCredential{}, err
	}

	issued, cert, err := service.authority.issue(deviceID, csr, now)
	if err != nil {
		return IssuedCredential{}, err
	}
	credID, err := newUUIDv7(now)
	if err != nil {
		return IssuedCredential{}, err
	}
	issued.CredentialID = credID
	issued.DeviceID = deviceID

	if _, err := tx.Exec(ctx, `
INSERT INTO connectivity.gateway_credentials(id, tenant_id, gateway_id, certificate_fingerprint_sha256, certificate_serial_hex, status, valid_from, valid_until, created_at, updated_at)
VALUES($1, $2, $3, $4, $5, 'ACTIVE', $6, $7, $6, $6)
`, credID, tenantID, deviceID, hashCredential(cert.Raw), issued.SerialNumber, now, cert.NotAfter); err != nil {
		return IssuedCredential{}, err
	}

	if _, err := tx.Exec(ctx, `
UPDATE connectivity.provisioning_batch_devices
SET status = 'ACTIVATED', activated_at = $2, credential_id = $3::uuid, updated_at = $2
WHERE id = $1::uuid
`, batchDeviceID, now, credID); err != nil {
		return IssuedCredential{}, err
	}

	if _, err := tx.Exec(ctx, `
UPDATE connectivity.provisioning_batches
SET activated_count = activated_count + 1, updated_at = $2
WHERE id = $1::uuid
`, batchID, now); err != nil {
		return IssuedCredential{}, err
	}

	if err := credentialAuditWithID(ctx, tx, tenantID, deviceID, deviceID, "JITP_ACTIVATED", &credID, now); err != nil {
		return IssuedCredential{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return IssuedCredential{}, err
	}
	if service.store != nil {
		service.store.InvalidateGatewayCache(deviceID)
	}
	return issued, nil
}
