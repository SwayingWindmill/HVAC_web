package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const maxTagsPayloadBytes = 8192

var tagKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,63}\z`)

// ValidateTagKey checks that one label key obeys the Registry tag key grammar.
func ValidateTagKey(key string) error {
	if !tagKeyPattern.MatchString(key) {
		return fmt.Errorf("%w: invalid tag key %q", ErrInvalidMutation, key)
	}
	return nil
}

// ValidateTags checks cardinality, key grammar, trimmed UTF-8 value limits, and serialized JSON size.
func ValidateTags(tags map[string]string) error {
	if len(tags) > 64 {
		return fmt.Errorf("%w: at most 64 tags are allowed", ErrInvalidMutation)
	}
	if len(tags) == 0 {
		return nil
	}
	for key, value := range tags {
		if err := ValidateTagKey(key); err != nil {
			return err
		}
		if err := validateTagValue(key, value); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("%w: marshal tags: %v", ErrInvalidMutation, err)
	}
	// PostgreSQL jsonb::text inserts a space after each ":" and "," separator.
	if len(raw)+2*len(tags)-1 > maxTagsPayloadBytes {
		return fmt.Errorf("%w: serialized tags exceed %d bytes", ErrInvalidMutation, maxTagsPayloadBytes)
	}
	return nil
}

func validateTagValue(key, value string) error {
	if !utf8.ValidString(value) || len(value) > 256 {
		return fmt.Errorf("%w: invalid tag value length or encoding for %q", ErrInvalidMutation, key)
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed != value {
		return fmt.Errorf("%w: tag value for %q must be non-empty and trimmed", ErrInvalidMutation, key)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: tag value for %q contains control characters", ErrInvalidMutation, key)
		}
	}
	return nil
}

func marshalTagsOrEmpty(tags map[string]string) ([]byte, error) {
	if len(tags) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(tags)
}

func marshalOptionalTagsParam(tags map[string]string) (any, error) {
	if tags == nil {
		return nil, nil
	}
	raw, err := marshalTagsOrEmpty(tags)
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}

func unmarshalTags(raw []byte) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var parsed map[string]string
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode Registry entity tags: %w", err)
	}
	if len(parsed) == 0 {
		return nil, nil
	}
	return parsed, nil
}

// AssetFilter specifies optional multi-dimensional search criteria for Assets.
type AssetFilter struct {
	Status             string
	AssetType          string
	SpaceID            string
	TemplateKey        string
	TemplateRevisionID string
	Tags               map[string]string
	TagKeys            []string
}

// DeviceFilter specifies optional multi-dimensional search criteria for Devices.
type DeviceFilter struct {
	Status             string
	DeviceType         string
	SpaceID            string
	TemplateKey        string
	TemplateRevisionID string
	Tags               map[string]string
	TagKeys            []string
}

type resourceFilter struct {
	status             string
	entityType         string
	spaceID            string
	templateKey        string
	templateRevisionID string
	tags               map[string]string
	tagKeys            []string
}

func (filter AssetFilter) Validate() error {
	return filter.asResourceFilter().validate()
}

func (filter AssetFilter) CanonicalKey() string {
	return filter.asResourceFilter().canonicalKey()
}

func (filter AssetFilter) asResourceFilter() resourceFilter {
	return resourceFilter{
		status:             filter.Status,
		entityType:         filter.AssetType,
		spaceID:            filter.SpaceID,
		templateKey:        filter.TemplateKey,
		templateRevisionID: filter.TemplateRevisionID,
		tags:               filter.Tags,
		tagKeys:            filter.TagKeys,
	}
}

func (filter DeviceFilter) Validate() error {
	return filter.asResourceFilter().validate()
}

func (filter DeviceFilter) CanonicalKey() string {
	return filter.asResourceFilter().canonicalKey()
}

func (filter DeviceFilter) asResourceFilter() resourceFilter {
	return resourceFilter{
		status:             filter.Status,
		entityType:         filter.DeviceType,
		spaceID:            filter.SpaceID,
		templateKey:        filter.TemplateKey,
		templateRevisionID: filter.TemplateRevisionID,
		tags:               filter.Tags,
		tagKeys:            filter.TagKeys,
	}
}

func (filter resourceFilter) validate() error {
	switch filter.status {
	case "", "ACTIVE", "INACTIVE", "RETIRED":
	default:
		return ErrInvalidPage
	}
	if filter.entityType != "" {
		trimmed := strings.TrimSpace(filter.entityType)
		if trimmed == "" || trimmed != filter.entityType || len(trimmed) > 128 || containsControlChar(trimmed) {
			return ErrInvalidPage
		}
	}
	if filter.spaceID != "" && !validUUIDv7(filter.spaceID) {
		return ErrInvalidPage
	}
	if filter.templateRevisionID != "" && !validUUIDv7(filter.templateRevisionID) {
		return ErrInvalidPage
	}
	if filter.templateKey != "" && !templateKeyPattern.MatchString(filter.templateKey) {
		return ErrInvalidPage
	}
	if len(filter.tags)+len(filter.tagKeys) > 64 {
		return ErrInvalidPage
	}
	if len(filter.tags) > 0 {
		if err := ValidateTags(filter.tags); err != nil {
			return ErrInvalidPage
		}
	}
	for _, key := range filter.tagKeys {
		if err := ValidateTagKey(key); err != nil {
			return ErrInvalidPage
		}
	}
	return nil
}

func (filter resourceFilter) canonicalKey() string {
	if filter.status == "" &&
		filter.entityType == "" &&
		filter.spaceID == "" &&
		filter.templateKey == "" &&
		filter.templateRevisionID == "" &&
		len(filter.tags) == 0 &&
		len(filter.tagKeys) == 0 {
		return ""
	}
	type tagPair struct {
		Key   string `json:"k"`
		Value string `json:"v"`
	}
	pairs := make([]tagPair, 0, len(filter.tags))
	for key, value := range filter.tags {
		pairs = append(pairs, tagPair{Key: key, Value: value})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].Key < pairs[j].Key
	})

	seenKeys := make(map[string]struct{}, len(filter.tagKeys))
	uniqueKeys := make([]string, 0, len(filter.tagKeys))
	for _, key := range filter.tagKeys {
		if _, exists := seenKeys[key]; exists {
			continue
		}
		seenKeys[key] = struct{}{}
		uniqueKeys = append(uniqueKeys, key)
	}
	sort.Strings(uniqueKeys)

	raw, err := json.Marshal(struct {
		Status             string    `json:"s,omitempty"`
		EntityType         string    `json:"t,omitempty"`
		SpaceID            string    `json:"sp,omitempty"`
		TemplateKey        string    `json:"tk,omitempty"`
		TemplateRevisionID string    `json:"tr,omitempty"`
		Tags               []tagPair `json:"tv,omitempty"`
		TagKeys            []string  `json:"ke,omitempty"`
	}{
		Status:             filter.status,
		EntityType:         filter.entityType,
		SpaceID:            filter.spaceID,
		TemplateKey:        filter.templateKey,
		TemplateRevisionID: filter.templateRevisionID,
		Tags:               pairs,
		TagKeys:            uniqueKeys,
	})
	if err != nil {
		return ""
	}
	return string(raw)
}

func cursorParentWithFilter(parentID, canonicalFilter string) string {
	if canonicalFilter == "" {
		return parentID
	}
	base := parentID
	if base == "" {
		base = "_all"
	}
	return base + "|" + canonicalFilter
}

func containsControlChar(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func buildFilteredEntityListSQL(
	kind TemplateKind,
	columns string,
	claims registryauth.GrantClaims,
	siteID string,
	filter resourceFilter,
	page PageRequest,
	limit int,
) (string, []any, error) {
	var (
		tableName  string
		typeColumn string
		targetType string
	)
	switch kind {
	case TemplateAsset:
		tableName = "core_registry.assets"
		typeColumn = "e.asset_type"
		targetType = "ASSET"
	case TemplateDevice:
		tableName = "core_registry.devices"
		typeColumn = "e.device_type"
		targetType = "DEVICE"
	default:
		return "", nil, ErrInvalidPage
	}

	args := []any{claims.TenantID, postgresUUIDArray(claims.DeniedSiteIDs)}
	addArg := func(value any) int {
		args = append(args, value)
		return len(args)
	}

	var query strings.Builder
	if filter.spaceID != "" {
		spaceArg := addArg(filter.spaceID)
		fmt.Fprintf(&query, `WITH RECURSIVE space_subtree AS (
  SELECT id, ARRAY[id] AS path, 1 AS depth
  FROM core_registry.spaces
  WHERE tenant_id = $1::uuid
    AND id = $%d::uuid
    AND NOT (site_id = ANY($2::uuid[]))
  UNION ALL
  SELECT child.id, parent.path || child.id, parent.depth + 1
  FROM core_registry.spaces AS child
  JOIN space_subtree AS parent ON child.parent_space_id = parent.id
  WHERE child.tenant_id = $1::uuid
    AND NOT (child.site_id = ANY($2::uuid[]))
    AND NOT (child.id = ANY(parent.path))
    AND parent.depth < 64
)
`, spaceArg)
	}

	fmt.Fprintf(&query, "SELECT %s\nFROM %s AS e\nWHERE e.tenant_id = $1::uuid\n  AND NOT (e.site_id = ANY($2::uuid[]))\n", qualifyEntityColumns(columns, "e"), tableName)

	if siteID != "" {
		siteArg := addArg(siteID)
		fmt.Fprintf(&query, "  AND e.site_id = $%d::uuid\n", siteArg)
	}
	if filter.status != "" {
		statusArg := addArg(filter.status)
		fmt.Fprintf(&query, "  AND e.status = $%d\n", statusArg)
	}
	if filter.entityType != "" {
		typeArg := addArg(filter.entityType)
		fmt.Fprintf(&query, "  AND %s = $%d\n", typeColumn, typeArg)
	}
	if filter.spaceID != "" {
		if kind == TemplateAsset {
			query.WriteString(`  AND EXISTS (
    SELECT 1
    FROM core_registry.asset_space_bindings AS asb
    JOIN space_subtree AS ss ON ss.id = asb.space_id
    WHERE asb.tenant_id = e.tenant_id
      AND asb.site_id = e.site_id
      AND asb.asset_id = e.id
      AND asb.status = 'ACTIVE'
      AND asb.valid_to IS NULL
  )
`)
		} else {
			query.WriteString(`  AND (
    EXISTS (
      SELECT 1
      FROM core_registry.device_space_bindings AS dsb
      JOIN space_subtree AS ss ON ss.id = dsb.space_id
      WHERE dsb.tenant_id = e.tenant_id
        AND dsb.site_id = e.site_id
        AND dsb.device_id = e.id
        AND dsb.status = 'ACTIVE'
        AND dsb.valid_to IS NULL
    )
    OR EXISTS (
      SELECT 1
      FROM core_registry.device_bindings AS db
      JOIN core_registry.asset_space_bindings AS asb
        ON asb.tenant_id = db.tenant_id
       AND asb.site_id = db.site_id
       AND asb.asset_id = db.asset_id
      JOIN space_subtree AS ss ON ss.id = asb.space_id
      WHERE db.tenant_id = e.tenant_id
        AND db.site_id = e.site_id
        AND db.device_id = e.id
        AND db.status = 'ACTIVE'
        AND db.valid_to IS NULL
        AND asb.status = 'ACTIVE'
        AND asb.valid_to IS NULL
    )
  )
`)
		}
	}
	if filter.templateRevisionID != "" || filter.templateKey != "" {
		targetArg := addArg(targetType)
		fmt.Fprintf(&query, `  AND EXISTS (
    SELECT 1
    FROM core_registry.registry_template_assignments AS ta
    JOIN core_registry.registry_template_revisions AS tr
      ON tr.id = ta.template_revision_id
     AND tr.tenant_id = ta.tenant_id
    JOIN core_registry.registry_templates AS t
      ON t.id = tr.template_id
     AND t.tenant_id = tr.tenant_id
    WHERE ta.tenant_id = e.tenant_id
      AND ta.site_id = e.site_id
      AND ta.target_type = $%d
      AND ta.target_id = e.id
      AND ta.status = 'ACTIVE'
      AND ta.valid_to IS NULL
`, targetArg)
		if filter.templateRevisionID != "" {
			revArg := addArg(filter.templateRevisionID)
			fmt.Fprintf(&query, "      AND ta.template_revision_id = $%d::uuid\n", revArg)
		}
		if filter.templateKey != "" {
			keyArg := addArg(filter.templateKey)
			fmt.Fprintf(&query, "      AND t.template_key = $%d\n", keyArg)
		}
		query.WriteString("  )\n")
	}
	if len(filter.tags) > 0 {
		raw, err := json.Marshal(filter.tags)
		if err != nil {
			return "", nil, ErrInvalidPage
		}
		tagsArg := addArg(string(raw))
		fmt.Fprintf(&query, "  AND e.tags @> $%d::jsonb\n", tagsArg)
	}
	if len(filter.tagKeys) > 0 {
		sortedKeys := append([]string(nil), filter.tagKeys...)
		sort.Strings(sortedKeys)
		for _, key := range sortedKeys {
			keyArg := addArg(key)
			fmt.Fprintf(&query, "  AND e.tags ? $%d::text\n", keyArg)
		}
	}

	displayArg := addArg(page.DisplayName)
	idArg := addArg(nullableCursorID(page.ID))
	limitArg := addArg(limit + 1)
	fmt.Fprintf(&query, "  AND ($%d = '' OR (e.display_name COLLATE \"C\", e.id) > ($%d COLLATE \"C\", $%d::uuid))\n", displayArg, displayArg, idArg)
	fmt.Fprintf(&query, "ORDER BY e.display_name COLLATE \"C\", e.id\nLIMIT $%d\n", limitArg)
	return query.String(), args, nil
}

func qualifyEntityColumns(columns, alias string) string {
	parts := strings.Split(columns, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, alias+"."+strings.TrimSpace(part))
	}
	return strings.Join(out, ", ")
}
