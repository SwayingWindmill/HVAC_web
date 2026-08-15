package migration

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	KindSite      = "SITE"
	KindEquipment = "EQUIPMENT"
	KindDevice    = "DEVICE"

	maxRecordBytes = 1 << 20
	maxInputBytes  = 256 << 20
	maxRecords     = 100_000
	maxMetadata    = 16 << 10
)

type SourceRef struct {
	SourceSystem string `json:"sourceSystem"`
	SourceTable  string `json:"sourceTable"`
	SourceKey    string `json:"sourceKey"`
}

type Record struct {
	TenantID              string         `json:"tenantId"`
	Kind                  string         `json:"kind"`
	SourceSystem          string         `json:"sourceSystem"`
	SourceTable           string         `json:"sourceTable"`
	SourceKey             string         `json:"sourceKey"`
	SourceWatermark       string         `json:"sourceWatermark"`
	SourceRowHash         string         `json:"sourceRowHash"`
	TransformationVersion string         `json:"transformationVersion"`
	BatchID               string         `json:"batchId"`
	Code                  string         `json:"code"`
	DisplayName           string         `json:"displayName"`
	Status                string         `json:"status"`
	Timezone              string         `json:"timezone,omitempty"`
	ResourceType          string         `json:"resourceType,omitempty"`
	SiteRef               *SourceRef     `json:"siteRef,omitempty"`
	RelationEvidence      map[string]any `json:"relationEvidence,omitempty"`
}

type Outcome string

const (
	OutcomeImported    Outcome = "IMPORTED"
	OutcomeSkipped     Outcome = "SKIPPED"
	OutcomeQuarantined Outcome = "QUARANTINED"
	OutcomeResolved    Outcome = "RESOLVED"
	OutcomeRetired     Outcome = "RETIRED"
)

type RecordResult struct {
	SourceSystem string  `json:"sourceSystem"`
	SourceTable  string  `json:"sourceTable"`
	SourceKey    string  `json:"sourceKey"`
	Outcome      Outcome `json:"outcome"`
	ReasonCode   string  `json:"reasonCode,omitempty"`
	TargetID     string  `json:"targetId,omitempty"`
}

type Summary struct {
	Imported    int            `json:"imported"`
	Skipped     int            `json:"skipped"`
	Quarantined int            `json:"quarantined"`
	Resolved    int            `json:"resolved"`
	Retired     int            `json:"retired"`
	Results     []RecordResult `json:"results"`
}

func (summary *Summary) Add(result RecordResult) {
	summary.Results = append(summary.Results, result)
	switch result.Outcome {
	case OutcomeImported:
		summary.Imported++
	case OutcomeSkipped:
		summary.Skipped++
	case OutcomeQuarantined:
		summary.Quarantined++
	case OutcomeResolved:
		summary.Resolved++
	case OutcomeRetired:
		summary.Retired++
	}
}

func ReadRecords(reader io.Reader) ([]Record, error) {
	if reader == nil {
		return nil, errors.New("migration input is required")
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), maxRecordBytes)
	records := make([]Record, 0)
	totalBytes := 0
	for line := 1; scanner.Scan(); line++ {
		totalBytes += len(scanner.Bytes()) + 1
		raw := bytes.TrimSpace(scanner.Bytes())
		if totalBytes > maxInputBytes {
			return nil, fmt.Errorf("migration input exceeds %d bytes", maxInputBytes)
		}
		if len(raw) == 0 {
			continue
		}
		if len(records) >= maxRecords {
			return nil, fmt.Errorf("migration input exceeds %d records", maxRecords)
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var record Record
		if err := decoder.Decode(&record); err != nil {
			return nil, fmt.Errorf("decode migration record line %d: %w", line, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("migration record line %d contains trailing JSON", line)
		}
		record = normalizeRecord(record)
		if err := record.ValidateEnvelope(); err != nil {
			return nil, fmt.Errorf("validate migration record line %d: %w", line, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read migration input: %w", err)
	}
	sort.SliceStable(records, func(i, j int) bool {
		left, right := kindRank(records[i].Kind), kindRank(records[j].Kind)
		if left != right {
			return left < right
		}
		return records[i].SourceIdentity() < records[j].SourceIdentity()
	})
	return records, nil
}

func normalizeRecord(record Record) Record {
	record.TenantID = strings.TrimSpace(record.TenantID)
	record.Kind = strings.TrimSpace(record.Kind)
	record.SourceSystem = strings.TrimSpace(record.SourceSystem)
	record.SourceTable = strings.TrimSpace(record.SourceTable)
	record.SourceKey = strings.TrimSpace(record.SourceKey)
	record.SourceWatermark = strings.TrimSpace(record.SourceWatermark)
	record.TransformationVersion = strings.TrimSpace(record.TransformationVersion)
	record.BatchID = strings.TrimSpace(record.BatchID)
	record.Code = strings.TrimSpace(record.Code)
	record.DisplayName = strings.TrimSpace(record.DisplayName)
	record.Status = strings.TrimSpace(record.Status)
	record.Timezone = strings.TrimSpace(record.Timezone)
	record.ResourceType = strings.TrimSpace(record.ResourceType)
	record.SiteRef = normalizeRef(record.SiteRef)
	if record.RelationEvidence == nil {
		record.RelationEvidence = map[string]any{}
	}
	return record
}

func (record Record) ValidateEnvelope() error {
	record = normalizeRecord(record)
	if kindRank(record.Kind) == 99 {
		return fmt.Errorf("unsupported record kind %q", record.Kind)
	}
	if !isUUIDv7(record.TenantID) {
		return errors.New("tenantId must be UUIDv7")
	}
	for name, field := range map[string]struct {
		value string
		limit int
	}{
		"sourceSystem":          {record.SourceSystem, 128},
		"sourceTable":           {record.SourceTable, 128},
		"sourceKey":             {record.SourceKey, 512},
		"sourceWatermark":       {record.SourceWatermark, 256},
		"transformationVersion": {record.TransformationVersion, 128},
		"batchId":               {record.BatchID, 128},
	} {
		trimmed := strings.TrimSpace(field.value)
		if trimmed == "" || len(trimmed) > field.limit || strings.ContainsRune(trimmed, '\x00') {
			return fmt.Errorf("%s is invalid", name)
		}
	}
	if len(record.SourceRowHash) != 64 {
		return errors.New("sourceRowHash must be lowercase SHA-256 hex")
	}
	decoded := make([]byte, 32)
	if _, err := hex.Decode(decoded, []byte(record.SourceRowHash)); err != nil || record.SourceRowHash != strings.ToLower(record.SourceRowHash) {
		return errors.New("sourceRowHash must be lowercase SHA-256 hex")
	}
	if err := validateRef("siteRef", record.SiteRef); err != nil {
		return err
	}
	metadata, err := json.Marshal(record.RelationEvidence)
	if err != nil {
		return fmt.Errorf("encode relationEvidence: %w", err)
	}
	if len(metadata) > maxMetadata {
		return fmt.Errorf("relationEvidence exceeds %d bytes", maxMetadata)
	}
	if err := validateMetadata(record.RelationEvidence); err != nil {
		return err
	}
	return nil
}

func (record Record) SourceIdentity() string {
	return fmt.Sprintf("%d:%s%d:%s%d:%s%d:%s", len(record.TenantID), record.TenantID, len(record.SourceSystem), record.SourceSystem, len(record.SourceTable), record.SourceTable, len(record.SourceKey), record.SourceKey)
}

func (record Record) TargetResourceType() string {
	return record.Kind
}

func (record Record) BusinessReason() string {
	if strings.TrimSpace(record.Code) == "" || len(strings.TrimSpace(record.Code)) > 128 || strings.ContainsRune(record.Code, '\x00') {
		return "INVALID_CODE"
	}
	if strings.TrimSpace(record.DisplayName) == "" || len(strings.TrimSpace(record.DisplayName)) > 256 || strings.ContainsRune(record.DisplayName, '\x00') {
		return "INVALID_DISPLAY_NAME"
	}
	switch record.Kind {
	case KindSite:
		if strings.TrimSpace(record.Timezone) == "" || len(record.Timezone) > 128 {
			return "INVALID_TIMEZONE"
		}
		if !oneOf(record.Status, "ACTIVE", "INACTIVE", "RETIRED") {
			return "INVALID_STATUS"
		}
	case KindEquipment:
		if record.SiteRef == nil {
			return "MISSING_SITE_PARENT"
		}
		if strings.TrimSpace(record.ResourceType) == "" || len(record.ResourceType) > 128 {
			return "INVALID_RESOURCE_TYPE"
		}
		if strings.EqualFold(strings.TrimSpace(record.SourceTable), "asset") {
			verified, _ := record.RelationEvidence["verifiedEquipmentRelation"].(bool)
			if !verified {
				return "AMBIGUOUS_ASSET_EQUIPMENT_RELATION"
			}
		}
		if !oneOf(record.Status, "ACTIVE", "INACTIVE", "RETIRED") {
			return "INVALID_STATUS"
		}
	case KindDevice:
		if record.SiteRef == nil {
			return "MISSING_SITE_PARENT"
		}
		if strings.TrimSpace(record.ResourceType) == "" || len(record.ResourceType) > 128 {
			return "INVALID_RESOURCE_TYPE"
		}
		if !oneOf(record.Status, "ACTIVE", "INACTIVE", "RETIRED") {
			return "INVALID_STATUS"
		}
	}
	return ""
}

func normalizeRef(ref *SourceRef) *SourceRef {
	if ref == nil {
		return nil
	}
	return &SourceRef{
		SourceSystem: strings.TrimSpace(ref.SourceSystem),
		SourceTable:  strings.TrimSpace(ref.SourceTable),
		SourceKey:    strings.TrimSpace(ref.SourceKey),
	}
}

func validateRef(name string, ref *SourceRef) error {
	if ref == nil {
		return nil
	}
	for field, value := range map[string]struct {
		text  string
		limit int
	}{
		"sourceSystem": {ref.SourceSystem, 128},
		"sourceTable":  {ref.SourceTable, 128},
		"sourceKey":    {ref.SourceKey, 512},
	} {
		if value.text == "" || len(value.text) > value.limit || strings.ContainsRune(value.text, '\x00') {
			return fmt.Errorf("%s.%s is invalid", name, field)
		}
	}
	return nil
}

func validateMetadata(value any) error {
	var walk func(any, int) error
	walk = func(current any, depth int) error {
		if depth > 8 {
			return errors.New("relationEvidence nesting is too deep")
		}
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				lower := strings.ToLower(key)
				for _, blocked := range []string{"password", "secret", "token", "cookie", "authorization", "credential"} {
					if strings.Contains(lower, blocked) {
						return fmt.Errorf("relationEvidence key %q is not allowed", key)
					}
				}
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range typed {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		case string:
			if len(typed) > 2048 || strings.ContainsRune(typed, '\x00') {
				return errors.New("relationEvidence string is invalid")
			}
		case nil, bool, float64:
			return nil
		default:
			return fmt.Errorf("relationEvidence contains unsupported value %T", current)
		}
		return nil
	}
	return walk(value, 0)
}

func kindRank(kind string) int {
	switch kind {
	case KindSite:
		return 0
	case KindEquipment:
		return 1
	case KindDevice:
		return 2
	default:
		return 99
	}
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
