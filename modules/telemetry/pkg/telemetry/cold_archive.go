package telemetry

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	coldArchiveManifestSchemaVersion = 1
	coldArchiveMaxManifestBytes      = 1 << 20
)

var coldArchiveSHA256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type coldArchiveTableSpec struct {
	table         string
	timeColumn    string
	paramType     string
	timeLayout    string
	nullableScope bool
	orderBy       string
	selectColumns string
}

var coldArchiveTables = map[string]coldArchiveTableSpec{
	"observations": {
		table:         "observations",
		timeColumn:    "sampled_at",
		paramType:     "DateTime64(3, 'UTC')",
		timeLayout:    "2006-01-02 15:04:05.000",
		nullableScope: true,
		orderBy:       "sampled_at ASC, observation_id ASC",
		selectColumns: strings.Join([]string{
			"observation_id", "tenant_id", "site_id", "device_id", "point_id", "sensor_id",
			"source_id", "source_event_id", "source_partition", "source_offset", "source_path",
			"telemetry_key", "point_type", "point_revision", "counter_decrease_mode",
			"counter_rollover_modulus", "value_type", "unit", "value_json", "value_number",
			"value_string", "value_boolean", "sampled_at", "received_at", "acceptance_status",
			"quality", "quality_reasons", "payload_sha256", "projected_at", "history_sequence",
		}, ", "),
	},
	"energy_interval_facts": {
		table:         "energy_interval_facts",
		timeColumn:    "period_end",
		paramType:     "DateTime64(3, 'UTC')",
		timeLayout:    "2006-01-02 15:04:05.000",
		nullableScope: false,
		orderBy:       "period_end ASC, fact_id ASC",
		selectColumns: strings.Join([]string{
			"fact_id", "tenant_id", "site_id", "meter_id", "meter_binding_id",
			"topology_version_id", "binding_version", "energy_type_id", "meter_role",
			"direction", "accounting_scope", "load_device_id", "device_id", "point_id",
			"sensor_id", "telemetry_key", "energy_type", "point_revision", "unit",
			"counter_decrease_mode", "counter_rollover_modulus", "period_start", "period_end",
			"energy_kwh", "transition_type", "quality", "quality_reasons",
			"source_previous_observation_id", "source_current_observation_id",
			"source_event_id", "source_partition", "source_offset", "dataset_revision",
			"data_watermark", "projected_at", "fact_revision", "rebuild_run_id",
		}, ", "),
	},
}

// ColdArchiveConfig configures pre-prune streaming exports from ClickHouse into local
// gzip-compressed JSONEachRow archive slices and SHA-256 manifests.
type ColdArchiveConfig struct {
	BaseURL    string
	Username   string
	Password   string
	ArchiveDir string
	HTTPClient *http.Client
	Now        func() time.Time
}

// ColdArchiveExportRequest selects one expiring ClickHouse table slice to archive.
type ColdArchiveExportRequest struct {
	Database   string
	Table      string
	Cutoff     time.Time
	LegalHolds []ClickHouseLegalHoldScope
}

// ColdArchiveManifest records checksum, byte length, row count, and cutoff metadata
// for a single compressed cold archive slice.
type ColdArchiveManifest struct {
	SchemaVersion int       `json:"schemaVersion"`
	Database      string    `json:"database"`
	Table         string    `json:"table"`
	CutoffUTC     time.Time `json:"cutoffUTC"`
	RowCount      int64     `json:"rowCount"`
	GzipByteSize  int64     `json:"gzipByteSize"`
	GzipSHA256    string    `json:"gzipSha256"`
	ExportedAtUTC time.Time `json:"exportedAtUTC"`
}

// ColdArchiveExportResult summarizes a completed or idempotently skipped cold archive export.
type ColdArchiveExportResult struct {
	Manifest     ColdArchiveManifest
	ArchivePath  string
	ManifestPath string
	Skipped      bool
}

// ColdArchiveExporter streams expiring raw ClickHouse rows to verified atomic archive slices.
type ColdArchiveExporter struct {
	baseURL    *url.URL
	username   string
	password   string
	archiveDir string
	httpClient *http.Client
	now        func() time.Time
}

// NewColdArchiveExporter validates ClickHouse HTTP and directory configuration for cold archival.
func NewColdArchiveExporter(config ColdArchiveConfig) (*ColdArchiveExporter, error) {
	parsed, err := validateClickHouseBaseURL(config.BaseURL, "ClickHouse cold archive")
	if err != nil {
		return nil, err
	}
	dir, err := validateColdArchiveDir(config.ArchiveDir)
	if err != nil {
		return nil, err
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	nowFn := config.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &ColdArchiveExporter{
		baseURL:    parsed,
		username:   config.Username,
		password:   config.Password,
		archiveDir: dir,
		httpClient: client,
		now:        nowFn,
	}, nil
}

// ExportBeforePrune streams rows strictly before req.Cutoff (excluding held tenants/sites)
// into an atomic .jsonl.gz archive and .manifest.json file, skipping when already verified.
func (exporter *ColdArchiveExporter) ExportBeforePrune(ctx context.Context, req ColdArchiveExportRequest) (ColdArchiveExportResult, error) {
	if exporter == nil || exporter.baseURL == nil || exporter.httpClient == nil || exporter.archiveDir == "" {
		return ColdArchiveExportResult{}, errors.New("ClickHouse cold archive exporter is closed")
	}
	database := strings.TrimSpace(req.Database)
	table := strings.TrimSpace(req.Table)
	if !clickHouseIdentifierPattern.MatchString(database) {
		return ColdArchiveExportResult{}, errors.New("ClickHouse cold archive database identifier is invalid")
	}
	spec, ok := coldArchiveTables[table]
	if !ok {
		return ColdArchiveExportResult{}, fmt.Errorf("unsupported ClickHouse cold archive table %q", table)
	}
	if req.Cutoff.IsZero() {
		return ColdArchiveExportResult{}, errors.New("ClickHouse cold archive cutoff is required")
	}
	cutoff := req.Cutoff.UTC()
	holds, err := normalizeClickHouseLegalHolds(req.LegalHolds)
	if err != nil {
		return ColdArchiveExportResult{}, err
	}

	sliceBase, archivePath, manifestPath, err := exporter.slicePaths(database, table, cutoff)
	if err != nil {
		return ColdArchiveExportResult{}, err
	}
	if existing, verified := verifyExistingColdArchive(manifestPath, archivePath, database, table, cutoff); verified {
		return ColdArchiveExportResult{
			Manifest:     existing,
			ArchivePath:  archivePath,
			ManifestPath: manifestPath,
			Skipped:      true,
		}, nil
	}

	if err := os.MkdirAll(exporter.archiveDir, 0o750); err != nil {
		return ColdArchiveExportResult{}, fmt.Errorf("create cold archive directory: %w", err)
	}

	sql, params := buildColdArchiveSelect(spec, database, cutoff, holds)
	bodyReader, err := exporter.openQueryStream(ctx, database+"."+table, sql, params)
	if err != nil {
		return ColdArchiveExportResult{}, err
	}
	defer bodyReader.Close()

	rowCount, gzipByteSize, gzipSHA256, err := streamJSONEachRowGzipAtomic(exporter.archiveDir, sliceBase, archivePath, bodyReader)
	if err != nil {
		return ColdArchiveExportResult{}, err
	}

	manifest := ColdArchiveManifest{
		SchemaVersion: coldArchiveManifestSchemaVersion,
		Database:      database,
		Table:         table,
		CutoffUTC:     cutoff,
		RowCount:      rowCount,
		GzipByteSize:  gzipByteSize,
		GzipSHA256:    gzipSHA256,
		ExportedAtUTC: exporter.now().UTC(),
	}
	if err := writeColdArchiveManifestAtomic(exporter.archiveDir, sliceBase, manifestPath, manifest); err != nil {
		return ColdArchiveExportResult{}, err
	}

	return ColdArchiveExportResult{
		Manifest:     manifest,
		ArchivePath:  archivePath,
		ManifestPath: manifestPath,
		Skipped:      false,
	}, nil
}

func validateColdArchiveDir(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.ContainsRune(trimmed, '\x00') {
		return "", errors.New("ClickHouse cold archive directory is required")
	}
	for _, part := range strings.FieldsFunc(trimmed, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", errors.New("ClickHouse cold archive directory must not contain path traversal segments")
		}
	}
	absDir, err := filepath.Abs(filepath.Clean(trimmed))
	if err != nil {
		return "", fmt.Errorf("resolve cold archive directory: %w", err)
	}
	info, statErr := os.Stat(absDir)
	if statErr == nil && !info.IsDir() {
		return "", errors.New("ClickHouse cold archive path must be a directory")
	}
	return absDir, nil
}

func (exporter *ColdArchiveExporter) slicePaths(database, table string, cutoff time.Time) (string, string, string, error) {
	sliceBase := fmt.Sprintf("%s__%s__%s", database, table, formatColdArchiveCutoff(cutoff))
	if strings.ContainsAny(sliceBase, `/\`) || strings.Contains(sliceBase, "..") {
		return "", "", "", errors.New("cold archive slice name contains unsafe path characters")
	}
	archivePath := filepath.Join(exporter.archiveDir, sliceBase+".jsonl.gz")
	manifestPath := filepath.Join(exporter.archiveDir, sliceBase+".manifest.json")
	if err := ensureWithinArchiveDir(exporter.archiveDir, archivePath); err != nil {
		return "", "", "", err
	}
	if err := ensureWithinArchiveDir(exporter.archiveDir, manifestPath); err != nil {
		return "", "", "", err
	}
	return sliceBase, archivePath, manifestPath, nil
}

func formatColdArchiveCutoff(cutoff time.Time) string {
	utc := cutoff.UTC()
	if utc.Nanosecond()%int(time.Millisecond) == 0 {
		return utc.Format("20060102T150405.000Z")
	}
	return utc.Format("20060102T150405.000000000Z")
}

func ensureWithinArchiveDir(dir, target string) error {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return errors.New("cold archive target escapes configured directory")
	}
	return nil
}

func buildColdArchiveSelect(spec coldArchiveTableSpec, database string, cutoff time.Time, holds []ClickHouseLegalHoldScope) (string, url.Values) {
	where, params := buildClickHouseRetentionWhereClause(spec.timeColumn, spec.paramType, spec.timeLayout, spec.nullableScope, cutoff, holds)
	sql := fmt.Sprintf("SELECT %s FROM %s.%s WHERE %s ORDER BY %s FORMAT JSONEachRow",
		spec.selectColumns, database, spec.table, where, spec.orderBy)
	return sql, params
}

func (exporter *ColdArchiveExporter) openQueryStream(ctx context.Context, qualifiedTable, sql string, params url.Values) (io.ReadCloser, error) {
	endpoint := *exporter.baseURL
	endpoint.RawQuery = params.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(sql))
	if err != nil {
		return nil, fmt.Errorf("create ClickHouse cold archive request for %s: %w", qualifiedTable, err)
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	request.Header.Set("Accept", "application/x-ndjson")
	if exporter.username != "" {
		request.SetBasicAuth(exporter.username, exporter.password)
	}
	response, err := exporter.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("ClickHouse cold archive query for %s: %w", qualifiedTable, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
		_ = response.Body.Close()
		message := strings.TrimSpace(string(body))
		if len(message) > 512 {
			message = message[:512]
		}
		return nil, fmt.Errorf("ClickHouse cold archive query for %s returned %d: %s", qualifiedTable, response.StatusCode, message)
	}
	return response.Body, nil
}

type byteCountWriter struct {
	w io.Writer
	n int64
}

func (c *byteCountWriter) Write(p []byte) (int, error) {
	written, err := c.w.Write(p)
	c.n += int64(written)
	return written, err
}

func streamJSONEachRowGzipAtomic(dir, sliceBase, finalPath string, body io.Reader) (int64, int64, string, error) {
	tmpFile, err := os.CreateTemp(dir, sliceBase+".jsonl.gz.*.tmp")
	if err != nil {
		return 0, 0, "", fmt.Errorf("create temporary cold archive file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	counter := &byteCountWriter{w: io.MultiWriter(tmpFile, hasher)}
	gzWriter := gzip.NewWriter(counter)

	reader := bufio.NewReaderSize(body, 64<<10)
	var rowCount int64
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) > 0 {
				if !json.Valid(trimmed) {
					_ = gzWriter.Close()
					_ = tmpFile.Close()
					return 0, 0, "", errors.New("ClickHouse cold archive stream returned invalid JSONEachRow line")
				}
				if _, writeErr := gzWriter.Write(trimmed); writeErr != nil {
					_ = gzWriter.Close()
					_ = tmpFile.Close()
					return 0, 0, "", fmt.Errorf("write compressed cold archive row: %w", writeErr)
				}
				if _, writeErr := gzWriter.Write([]byte{'\n'}); writeErr != nil {
					_ = gzWriter.Close()
					_ = tmpFile.Close()
					return 0, 0, "", fmt.Errorf("write compressed cold archive delimiter: %w", writeErr)
				}
				rowCount++
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = gzWriter.Close()
			_ = tmpFile.Close()
			return 0, 0, "", fmt.Errorf("read ClickHouse cold archive stream: %w", readErr)
		}
	}

	// Close gzip before finalizing size and SHA-256 so the trailer bytes are included.
	if err := gzWriter.Close(); err != nil {
		_ = tmpFile.Close()
		return 0, 0, "", fmt.Errorf("finalize compressed cold archive: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return 0, 0, "", fmt.Errorf("sync cold archive file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return 0, 0, "", fmt.Errorf("close cold archive file: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return 0, 0, "", fmt.Errorf("commit cold archive file: %w", err)
	}
	cleanup = false
	return rowCount, counter.n, hex.EncodeToString(hasher.Sum(nil)), nil
}

func writeColdArchiveManifestAtomic(dir, sliceBase, finalPath string, manifest ColdArchiveManifest) error {
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cold archive manifest: %w", err)
	}
	encoded = append(encoded, '\n')

	tmpFile, err := os.CreateTemp(dir, sliceBase+".manifest.json.*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary cold archive manifest: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(encoded); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write cold archive manifest: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync cold archive manifest: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close cold archive manifest: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("commit cold archive manifest: %w", err)
	}
	cleanup = false
	return nil
}

func verifyExistingColdArchive(manifestPath, archivePath, database, table string, cutoff time.Time) (ColdArchiveManifest, bool) {
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Size() > coldArchiveMaxManifestBytes {
		return ColdArchiveManifest{}, false
	}
	manifestFile, err := os.Open(manifestPath)
	if err != nil {
		return ColdArchiveManifest{}, false
	}
	raw, readErr := io.ReadAll(io.LimitReader(manifestFile, coldArchiveMaxManifestBytes+1))
	_ = manifestFile.Close()
	if readErr != nil || len(raw) > coldArchiveMaxManifestBytes {
		return ColdArchiveManifest{}, false
	}
	var manifest ColdArchiveManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return ColdArchiveManifest{}, false
	}
	if manifest.SchemaVersion != coldArchiveManifestSchemaVersion ||
		manifest.Database != database ||
		manifest.Table != table ||
		!manifest.CutoffUTC.UTC().Equal(cutoff.UTC()) ||
		manifest.RowCount < 0 ||
		manifest.GzipByteSize <= 0 ||
		!coldArchiveSHA256Pattern.MatchString(manifest.GzipSHA256) ||
		manifest.ExportedAtUTC.IsZero() {
		return ColdArchiveManifest{}, false
	}

	archiveInfo, err := os.Lstat(archivePath)
	if err != nil || !archiveInfo.Mode().IsRegular() || archiveInfo.Size() != manifest.GzipByteSize {
		return ColdArchiveManifest{}, false
	}
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return ColdArchiveManifest{}, false
	}
	hasher := sha256.New()
	copied, copyErr := io.Copy(hasher, archiveFile)
	_ = archiveFile.Close()
	if copyErr != nil || copied != manifest.GzipByteSize {
		return ColdArchiveManifest{}, false
	}
	if hex.EncodeToString(hasher.Sum(nil)) != manifest.GzipSHA256 {
		return ColdArchiveManifest{}, false
	}
	return manifest, true
}
