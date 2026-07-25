package observability

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var s2MetricNamePattern = regexp.MustCompile(`^hvac_s2_[a-z0-9_]+(?:_total|_seconds|_bytes|_ratio|_timestamp_seconds)$`)
var hmacReferencePattern = regexp.MustCompile(`^hmac-sha256:[a-z0-9_]+:[0-9a-f]{32}$`)

var s2ForbiddenLabelFragments = []string{
	"organization", "tenant", "site", "device", "subscription", "cursor", "revision",
	"telemetry_key", "key", "value", "token", "channel", "authorization", "cookie", "csrf",
	"credential", "request_id", "trace_id", "event_id", "principal", "subject", "session",
}

var sensitiveOperationalKeyFragments = []string{
	"authorization", "cookie", "csrf", "token", "cursor", "channel", "telemetry_value", "source_credential",
	"password", "secret", "device_id", "site_id", "organization_id", "subscription_id", "business_revision", "event_id", "request_id",
}

type S2MetricDefinition struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Unit         string   `json:"unit"`
	Labels       []string `json:"labels"`
	SeriesBudget int      `json:"seriesBudget"`
}

type S2MetricCatalog struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Namespace     string               `json:"namespace"`
	LabelPolicy   S2MetricLabelPolicy  `json:"labelPolicy"`
	Families      []S2MetricDefinition `json:"families"`
	SampleValues  map[string][]string  `json:"sampleValues"`
}

type S2MetricLabelPolicy struct {
	Allowed   []string `json:"allowed"`
	Forbidden []string `json:"forbidden"`
}

type MetricCardinalityFamily struct {
	Name                string   `json:"name"`
	Labels              []string `json:"labels"`
	ObservedCardinality int      `json:"observedCardinality"`
	MaximumCardinality  int      `json:"maximumCardinality"`
	SeriesBudget        int      `json:"seriesBudget"`
	WithinBudget        bool     `json:"withinBudget"`
}

type ReferenceHasher struct {
	key []byte
}

func NewReferenceHasher(key []byte) (*ReferenceHasher, error) {
	if len(key) < 32 {
		return nil, errors.New("observability HMAC key must contain at least 32 bytes")
	}
	return &ReferenceHasher{key: append([]byte(nil), key...)}, nil
}

func (hasher *ReferenceHasher) Reference(kind, raw string) (string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	raw = strings.TrimSpace(raw)
	if hasher == nil || len(hasher.key) < 32 || kind == "" || raw == "" {
		return "", errors.New("reference kind, raw value and HMAC key are required")
	}
	digest := hmac.New(sha256.New, hasher.key)
	_, _ = digest.Write([]byte(kind))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(raw))
	return "hmac-sha256:" + kind + ":" + hex.EncodeToString(digest.Sum(nil)[:16]), nil
}

func IsHMACReference(value string) bool {
	return hmacReferencePattern.MatchString(strings.TrimSpace(value))
}

func ValidateS2MetricCatalog(catalog S2MetricCatalog) ([]MetricCardinalityFamily, error) {
	if catalog.SchemaVersion != 1 || catalog.Namespace != "hvac_s2" {
		return nil, errors.New("unsupported S2 metric catalog")
	}
	allowed := stringSet(catalog.LabelPolicy.Allowed)
	forbidden := stringSet(catalog.LabelPolicy.Forbidden)
	seenNames := map[string]struct{}{}
	reports := make([]MetricCardinalityFamily, 0, len(catalog.Families))
	for _, family := range catalog.Families {
		if err := validateS2MetricDefinition(family, allowed, forbidden); err != nil {
			return nil, err
		}
		if _, duplicate := seenNames[family.Name]; duplicate {
			return nil, fmt.Errorf("duplicate S2 metric family %q", family.Name)
		}
		seenNames[family.Name] = struct{}{}
		maximum := 1
		for _, label := range family.Labels {
			values := catalog.SampleValues[label]
			if len(values) == 0 {
				return nil, fmt.Errorf("metric %q label %q has no bounded value set", family.Name, label)
			}
			maximum *= len(values)
		}
		observed := len(family.Labels) + 1
		if observed > maximum {
			observed = maximum
		}
		reports = append(reports, MetricCardinalityFamily{
			Name: family.Name, Labels: append([]string(nil), family.Labels...),
			ObservedCardinality: observed, MaximumCardinality: maximum,
			SeriesBudget: family.SeriesBudget, WithinBudget: maximum <= family.SeriesBudget,
		})
		if maximum > family.SeriesBudget {
			return nil, fmt.Errorf("metric %q maximum cardinality %d exceeds series budget %d", family.Name, maximum, family.SeriesBudget)
		}
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].Name < reports[j].Name })
	return reports, nil
}

func RecordS2MetricSample(registry *Registry, family S2MetricDefinition, labels map[string]string, value float64) error {
	if registry == nil {
		return errors.New("S2 metric registry is required")
	}
	if len(labels) != len(family.Labels) {
		return fmt.Errorf("metric %q label count drifted", family.Name)
	}
	expected := stringSet(family.Labels)
	for label := range labels {
		if _, ok := expected[normalizeFieldName(label)]; !ok {
			return fmt.Errorf("metric %q received unexpected label %q", family.Name, label)
		}
	}
	switch family.Type {
	case "counter":
		return registry.AddCounter(family.Name, "S2 release-gate metric", labels, value)
	case "gauge":
		return registry.SetGauge(family.Name, "S2 release-gate metric", labels, value)
	case "histogram":
		return registry.ObserveHistogram(family.Name, "S2 release-gate metric", labels, value, nil)
	default:
		return fmt.Errorf("metric %q has unsupported type %q", family.Name, family.Type)
	}
}

func ValidateOperationalRecord(fields map[string]any) error {
	for key, value := range fields {
		normalized := normalizeFieldName(key)
		if containsFragment(normalized, sensitiveOperationalKeyFragments) {
			stringValue := strings.TrimSpace(fmt.Sprint(value))
			if !IsHMACReference(stringValue) {
				return fmt.Errorf("operational field %q contains a raw sensitive value", key)
			}
		}
		stringValue := strings.TrimSpace(fmt.Sprint(value))
		lower := strings.ToLower(stringValue)
		for _, marker := range []string{"bearer ", "basic ", "recovery-cursor:", "centrifugo:subscription:", "thingsboard-credential:"} {
			if strings.Contains(lower, marker) {
				return fmt.Errorf("operational field %q contains forbidden marker %q", key, marker)
			}
		}
	}
	return nil
}

func HMACOperationalReferences(hasher *ReferenceHasher, raw map[string]string) (map[string]any, error) {
	result := make(map[string]any, len(raw))
	for kind, value := range raw {
		reference, err := hasher.Reference(kind, value)
		if err != nil {
			return nil, err
		}
		result[kind+"_ref"] = reference
	}
	if err := ValidateOperationalRecord(result); err != nil {
		return nil, err
	}
	return result, nil
}

func validateS2MetricDefinition(family S2MetricDefinition, allowed, forbidden map[string]struct{}) error {
	if !s2MetricNamePattern.MatchString(family.Name) {
		return fmt.Errorf("metric %q does not use the S2 namespace and a base-unit suffix", family.Name)
	}
	switch family.Type {
	case "counter":
		if !strings.HasSuffix(family.Name, "_total") {
			return fmt.Errorf("counter %q must end in _total", family.Name)
		}
	case "histogram", "gauge":
		if !strings.HasSuffix(family.Name, "_seconds") && !strings.HasSuffix(family.Name, "_bytes") && !strings.HasSuffix(family.Name, "_ratio") && !strings.HasSuffix(family.Name, "_timestamp_seconds") {
			return fmt.Errorf("metric %q must use a base-unit suffix", family.Name)
		}
	default:
		return fmt.Errorf("metric %q has unsupported type %q", family.Name, family.Type)
	}
	if family.SeriesBudget <= 0 {
		return fmt.Errorf("metric %q has no positive series budget", family.Name)
	}
	seen := map[string]struct{}{}
	for _, label := range family.Labels {
		normalized := normalizeFieldName(label)
		if _, ok := allowed[normalized]; !ok {
			return fmt.Errorf("metric %q label %q is not allowlisted", family.Name, label)
		}
		if _, blocked := forbidden[normalized]; blocked || containsFragment(normalized, s2ForbiddenLabelFragments) {
			return fmt.Errorf("metric %q label %q is forbidden", family.Name, label)
		}
		if _, duplicate := seen[normalized]; duplicate {
			return fmt.Errorf("metric %q repeats label %q", family.Name, label)
		}
		seen[normalized] = struct{}{}
	}
	return nil
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[normalizeFieldName(value)] = struct{}{}
	}
	return result
}

func normalizeFieldName(value string) string {
	return strings.ToLower(strings.NewReplacer("-", "_", ".", "_").Replace(strings.TrimSpace(value)))
}

func containsFragment(value string, fragments []string) bool {
	for _, fragment := range fragments {
		if value == fragment || strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}
