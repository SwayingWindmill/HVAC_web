package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	coldArchiveManifestSuffix        = ".manifest.json"
	coldArchiveFileSuffix            = ".jsonl.gz"
	coldArchiveTempSuffix            = ".tmp"
	coldArchiveManifestSchemaVersion = 1
	coldArchiveMaxManifestBytes      = 1 << 20
	coldArchiveStaleTempAge          = time.Hour
)

var (
	coldArchiveIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	coldArchiveSHA256Pattern     = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// ColdArchiveFindingCode classifies an integrity or temp-file anomaly in a cold archive directory.
type ColdArchiveFindingCode string

const (
	ColdArchiveFindingManifestCorrupt  ColdArchiveFindingCode = "COLD_ARCHIVE_MANIFEST_CORRUPT"
	ColdArchiveFindingArchiveMissing   ColdArchiveFindingCode = "COLD_ARCHIVE_FILE_MISSING"
	ColdArchiveFindingSizeMismatch     ColdArchiveFindingCode = "COLD_ARCHIVE_SIZE_MISMATCH"
	ColdArchiveFindingChecksumMismatch ColdArchiveFindingCode = "COLD_ARCHIVE_SHA256_MISMATCH"
	ColdArchiveFindingStaleTempFile    ColdArchiveFindingCode = "COLD_ARCHIVE_STALE_TEMP_FILE"
)

// ColdArchiveFinding records one integrity violation discovered during directory verification.
type ColdArchiveFinding struct {
	Code         ColdArchiveFindingCode
	RelativePath string
	Database     string
	Table        string
}

// ColdArchiveAuditReport summarizes manifest, checksum, and temp-file checks for a cold archive directory.
type ColdArchiveAuditReport struct {
	ManifestsChecked int
	VerifiedCount    int
	TempFilesScanned int
	StaleTempCount   int
	CorruptCount     int
	MissingCount     int
	Findings         []ColdArchiveFinding
}

// Healthy reports whether every manifest passed verification and no stale temp files were found.
func (r ColdArchiveAuditReport) Healthy() bool {
	return len(r.Findings) == 0
}

type coldArchiveManifestRecord struct {
	SchemaVersion int       `json:"schemaVersion"`
	Database      string    `json:"database"`
	Table         string    `json:"table"`
	CutoffUTC     time.Time `json:"cutoffUTC"`
	RowCount      int64     `json:"rowCount"`
	GzipByteSize  int64     `json:"gzipByteSize"`
	GzipSHA256    string    `json:"gzipSha256"`
	ExportedAtUTC time.Time `json:"exportedAtUTC"`
}

// VerifyColdArchiveDirectory scans dir for .manifest.json and .tmp files, verifies manifest schemas
// and linked .jsonl.gz sizes/SHA-256 checksums, and flags stale .tmp files older than 1 hour.
func VerifyColdArchiveDirectory(ctx context.Context, dir string, now time.Time) (ColdArchiveAuditReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ColdArchiveAuditReport{}, err
	}
	auditTime := normalizeAuditTime(now)

	cleanDir, err := validateColdArchiveRoot(dir)
	if err != nil {
		return ColdArchiveAuditReport{}, err
	}

	var report ColdArchiveAuditReport
	walkErr := filepath.WalkDir(cleanDir, func(path string, entry fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if strings.HasSuffix(entry.Name(), coldArchiveManifestSuffix) {
				report.ManifestsChecked++
				report.CorruptCount++
				rel, _ := filepath.Rel(cleanDir, path)
				report.Findings = append(report.Findings, ColdArchiveFinding{
					Code:         ColdArchiveFindingManifestCorrupt,
					RelativePath: filepath.ToSlash(rel),
				})
			} else if strings.HasSuffix(entry.Name(), coldArchiveTempSuffix) {
				report.TempFilesScanned++
				report.CorruptCount++
				rel, _ := filepath.Rel(cleanDir, path)
				report.Findings = append(report.Findings, ColdArchiveFinding{
					Code:         ColdArchiveFindingStaleTempFile,
					RelativePath: filepath.ToSlash(rel),
				})
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}

		name := entry.Name()
		rel, relErr := filepath.Rel(cleanDir, path)
		if relErr != nil {
			return relErr
		}
		relSlash := filepath.ToSlash(rel)

		switch {
		case strings.HasSuffix(name, coldArchiveManifestSuffix):
			report.ManifestsChecked++
			finding, ok := verifyManifestEntry(cleanDir, path, relSlash)
			if ok {
				report.VerifiedCount++
			} else {
				if finding.Code == ColdArchiveFindingArchiveMissing {
					report.MissingCount++
				} else {
					report.CorruptCount++
				}
				report.Findings = append(report.Findings, finding)
			}
		case strings.HasSuffix(name, coldArchiveTempSuffix):
			report.TempFilesScanned++
			info, infoErr := entry.Info()
			if infoErr != nil {
				return infoErr
			}
			if auditTime.Sub(info.ModTime().UTC()) > coldArchiveStaleTempAge {
				report.StaleTempCount++
				report.Findings = append(report.Findings, ColdArchiveFinding{
					Code:         ColdArchiveFindingStaleTempFile,
					RelativePath: relSlash,
				})
			}
		}
		return nil
	})
	if walkErr != nil {
		return ColdArchiveAuditReport{}, walkErr
	}
	return report, nil
}

func validateColdArchiveRoot(dir string) (string, error) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" || strings.ContainsRune(trimmed, '\x00') {
		return "", errors.New("cold archive directory is required")
	}
	for _, part := range strings.FieldsFunc(trimmed, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", errors.New("cold archive directory must not contain path traversal segments")
		}
	}
	cleanDir, err := filepath.Abs(filepath.Clean(trimmed))
	if err != nil {
		return "", fmt.Errorf("resolve cold archive directory: %w", err)
	}
	info, err := os.Stat(cleanDir)
	if err != nil {
		return "", fmt.Errorf("inspect cold archive directory: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("cold archive path must be a directory")
	}
	return cleanDir, nil
}

func verifyManifestEntry(rootDir, manifestPath, relSlash string) (ColdArchiveFinding, bool) {
	info, err := os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > coldArchiveMaxManifestBytes {
		return ColdArchiveFinding{Code: ColdArchiveFindingManifestCorrupt, RelativePath: relSlash}, false
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		return ColdArchiveFinding{Code: ColdArchiveFindingManifestCorrupt, RelativePath: relSlash}, false
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, coldArchiveMaxManifestBytes+1))
	_ = file.Close()
	if readErr != nil || len(raw) > coldArchiveMaxManifestBytes {
		return ColdArchiveFinding{Code: ColdArchiveFindingManifestCorrupt, RelativePath: relSlash}, false
	}

	var manifest coldArchiveManifestRecord
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return ColdArchiveFinding{Code: ColdArchiveFindingManifestCorrupt, RelativePath: relSlash}, false
	}
	if manifest.SchemaVersion != coldArchiveManifestSchemaVersion ||
		!coldArchiveIdentifierPattern.MatchString(manifest.Database) ||
		!coldArchiveIdentifierPattern.MatchString(manifest.Table) ||
		manifest.CutoffUTC.IsZero() ||
		manifest.RowCount < 0 ||
		manifest.GzipByteSize <= 0 ||
		!coldArchiveSHA256Pattern.MatchString(manifest.GzipSHA256) ||
		manifest.ExportedAtUTC.IsZero() {
		return ColdArchiveFinding{
			Code:         ColdArchiveFindingManifestCorrupt,
			RelativePath: relSlash,
			Database:     manifest.Database,
			Table:        manifest.Table,
		}, false
	}

	archivePath := strings.TrimSuffix(manifestPath, coldArchiveManifestSuffix) + coldArchiveFileSuffix
	if rel, err := filepath.Rel(rootDir, archivePath); err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return ColdArchiveFinding{
			Code:         ColdArchiveFindingManifestCorrupt,
			RelativePath: relSlash,
			Database:     manifest.Database,
			Table:        manifest.Table,
		}, false
	}

	archiveInfo, err := os.Lstat(archivePath)
	if errors.Is(err, os.ErrNotExist) {
		return ColdArchiveFinding{
			Code:         ColdArchiveFindingArchiveMissing,
			RelativePath: relSlash,
			Database:     manifest.Database,
			Table:        manifest.Table,
		}, false
	}
	if err != nil || !archiveInfo.Mode().IsRegular() {
		return ColdArchiveFinding{
			Code:         ColdArchiveFindingSizeMismatch,
			RelativePath: relSlash,
			Database:     manifest.Database,
			Table:        manifest.Table,
		}, false
	}
	if archiveInfo.Size() != manifest.GzipByteSize {
		return ColdArchiveFinding{
			Code:         ColdArchiveFindingSizeMismatch,
			RelativePath: relSlash,
			Database:     manifest.Database,
			Table:        manifest.Table,
		}, false
	}

	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return ColdArchiveFinding{
			Code:         ColdArchiveFindingChecksumMismatch,
			RelativePath: relSlash,
			Database:     manifest.Database,
			Table:        manifest.Table,
		}, false
	}
	hasher := sha256.New()
	copied, copyErr := io.Copy(hasher, archiveFile)
	_ = archiveFile.Close()
	if copyErr != nil || copied != manifest.GzipByteSize || hex.EncodeToString(hasher.Sum(nil)) != manifest.GzipSHA256 {
		return ColdArchiveFinding{
			Code:         ColdArchiveFindingChecksumMismatch,
			RelativePath: relSlash,
			Database:     manifest.Database,
			Table:        manifest.Table,
		}, false
	}
	return ColdArchiveFinding{}, true
}
