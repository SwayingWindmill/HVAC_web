// Package alarmrules turns a Site's rules file (deploy/sites/<site>.alarm-rules.json) into the
// Alarm policies its Site assignment evaluates (#609).
package alarmrules

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
)

// SitePolicy is one rule as a policy family and the Site assignment that evaluates it. The policy
// carries no revision identity yet: publishing gives it the next revision when its content changed.
type SitePolicy struct {
	AssignmentID string
	Policy       alarmservice.AlarmPolicyRevision
}

type Compiled struct {
	TenantID string
	SiteID   string
	Policies []SitePolicy
}

type rulesFile struct {
	SchemaVersion      int           `json:"schemaVersion"`
	Note               string        `json:"note"`
	SiteFile           string        `json:"siteFile"`
	Rules              []rule        `json:"rules"`
	ReadingsNotUpdated *readingsRule `json:"readingsNotUpdated"`
	ReportsInterrupted *readingsRule `json:"reportsInterrupted"`
}

// A rule is a policy written with readings as "<device code>/<point code>".
type rule struct {
	Code             string                               `json:"code"`
	Title            string                               `json:"title"`
	Summary          string                               `json:"summary"`
	Severity         alarmmodel.Severity                  `json:"severity"`
	QualityPolicy    alarmservice.EvaluationQualityPolicy `json:"qualityPolicy"`
	FreshnessSeconds int64                                `json:"freshnessSeconds"`
	TriggerMode      alarmservice.TriggerMode             `json:"triggerMode"`
	DurationSeconds  int64                                `json:"durationSeconds"`
	RepeatCount      uint64                               `json:"repeatCount"`
	Raise            alarmservice.Condition               `json:"raise"`
	Clear            alarmservice.Condition               `json:"clear"`
}

// readingsRule words a rule over the Site's own readings. readingsNotUpdated raises, per device,
// when one of its active points has had no new reading for ageSeconds; reportsInterrupted raises
// when none of the Site's active points has (#615). {device}, {points} and {minutes} in the
// wording are the device and its points by name and ageSeconds in minutes.
type readingsRule struct {
	Title      string              `json:"title"`
	Summary    string              `json:"summary"`
	Severity   alarmmodel.Severity `json:"severity"`
	AgeSeconds int64               `json:"ageSeconds"`
}

type siteFile struct {
	TenantID string `json:"tenantId"`
	Site     struct {
		ID   string `json:"id"`
		Code string `json:"code"`
	} `json:"site"`
	Devices []struct {
		ID         string          `json:"id"`
		Code       string          `json:"code"`
		Name       string          `json:"name"`
		Points     json.RawMessage `json:"points"`
		MorePoints []sitePoint     `json:"morePoints"`
	} `json:"devices"`
	MeterPoints []sitePoint `json:"meterPoints"`
}

type sitePoint struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	ValueType string `json:"valueType"`
	Status    string `json:"status"`
}

type device struct {
	id     string
	name   string
	points []sitePoint
}

// A number within ±anyReading is a reading: the clear condition only asks that one arrived.
const anyReading = 1e12

// Kinds in the ids derived for a rule's policy family and its Site assignment.
const (
	policyKind     = "7a01"
	assignmentKind = "7a02"
)

func Load(rulesPath string) (Compiled, error) {
	var rules rulesFile
	if err := decodeStrict(rulesPath, &rules); err != nil {
		return Compiled{}, err
	}
	if rules.SchemaVersion != 1 {
		return Compiled{}, fmt.Errorf("%s: schemaVersion must be 1", rulesPath)
	}
	encoded, err := os.ReadFile(filepath.Join(filepath.Dir(rulesPath), rules.SiteFile))
	if err != nil {
		return Compiled{}, err
	}
	var site siteFile
	if err := json.Unmarshal(encoded, &site); err != nil {
		return Compiled{}, fmt.Errorf("%s: %w", rules.SiteFile, err)
	}

	// Only points the site file marks active are read; an inactive point has no source.
	devices := map[string]device{}
	var order []string
	for _, entry := range site.Devices {
		var points []sitePoint
		if string(entry.Points) == `"meter"` {
			points = append(points, site.MeterPoints...)
		} else if err := json.Unmarshal(entry.Points, &points); err != nil {
			return Compiled{}, fmt.Errorf("%s: points of %s: %w", rules.SiteFile, entry.Code, err)
		}
		active := []sitePoint{}
		for _, point := range append(points, entry.MorePoints...) {
			if point.Status == "" || point.Status == "ACTIVE" {
				active = append(active, point)
			}
		}
		devices[entry.Code] = device{id: entry.ID, name: plainName(entry.Name), points: active}
		order = append(order, entry.Code)
	}

	compiled := Compiled{TenantID: site.TenantID, SiteID: site.Site.ID}
	for _, entry := range rules.Rules {
		raise, err := resolve(entry.Raise, devices)
		if err != nil {
			return Compiled{}, fmt.Errorf("rule %s: %w", entry.Code, err)
		}
		clear, err := resolve(entry.Clear, devices)
		if err != nil {
			return Compiled{}, fmt.Errorf("rule %s: %w", entry.Code, err)
		}
		compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, entry.Code, alarmservice.AlarmPolicyRevision{
			Title: entry.Title, Summary: entry.Summary, Severity: entry.Severity, QualityPolicy: entry.QualityPolicy,
			FreshnessSeconds: entry.FreshnessSeconds, TriggerMode: entry.TriggerMode, DurationSeconds: entry.DurationSeconds,
			RepeatCount: entry.RepeatCount, Raise: raise, Clear: clear,
		}))
	}
	if readings := rules.ReadingsNotUpdated; readings != nil {
		for _, code := range order {
			entry := devices[code]
			if len(entry.points) == 0 {
				continue
			}
			noData, fresh := watched(entry, readings.AgeSeconds)
			var names []string
			for _, point := range entry.points {
				names = append(names, plainName(point.Name))
			}
			wording := strings.NewReplacer("{device}", entry.name, "{points}", strings.Join(names, "、"), "{minutes}", strconv.FormatInt(readings.AgeSeconds/60, 10))
			compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "readings-not-updated/"+code, alarmservice.AlarmPolicyRevision{
				Title: wording.Replace(readings.Title), Summary: wording.Replace(readings.Summary), Severity: readings.Severity,
				QualityPolicy: alarmservice.EvaluationQualityUsable, FreshnessSeconds: readings.AgeSeconds, TriggerMode: alarmservice.TriggerSimple,
				// Raised while any reading is overdue, cleared once every one is fresh again.
				Raise: either(alarmservice.ConditionOr, noData), Clear: either(alarmservice.ConditionAnd, fresh),
			}))
		}
	}
	if silence := rules.ReportsInterrupted; silence != nil {
		// Grouped by device, as a compound condition holds at most 32 others.
		var noData, fresh []alarmservice.Condition
		for _, code := range order {
			entry := devices[code]
			if len(entry.points) == 0 {
				continue
			}
			deviceNoData, deviceFresh := watched(entry, silence.AgeSeconds)
			noData = append(noData, either(alarmservice.ConditionAnd, deviceNoData))
			fresh = append(fresh, either(alarmservice.ConditionOr, deviceFresh))
		}
		wording := strings.NewReplacer("{minutes}", strconv.FormatInt(silence.AgeSeconds/60, 10))
		compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "reports-interrupted", alarmservice.AlarmPolicyRevision{
			Title: wording.Replace(silence.Title), Summary: wording.Replace(silence.Summary), Severity: silence.Severity,
			QualityPolicy: alarmservice.EvaluationQualityUsable, FreshnessSeconds: silence.AgeSeconds, TriggerMode: alarmservice.TriggerSimple,
			// Raised once every reading is overdue, cleared by the first fresh one.
			Raise: either(alarmservice.ConditionAnd, noData), Clear: either(alarmservice.ConditionOr, fresh),
		}))
	}
	return compiled, nil
}

func decodeStrict(path string, target any) error {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// resolve rewrites each "<device code>/<point code>" to the evaluator's "<device id>/<point code>".
func resolve(condition alarmservice.Condition, devices map[string]device) (alarmservice.Condition, error) {
	if len(condition.Children) > 0 {
		children := make([]alarmservice.Condition, len(condition.Children))
		for index, child := range condition.Children {
			resolved, err := resolve(child, devices)
			if err != nil {
				return alarmservice.Condition{}, err
			}
			children[index] = resolved
		}
		condition.Children = children
		return condition, nil
	}
	deviceCode, pointCode, _ := strings.Cut(condition.Input, "/")
	entry, ok := devices[deviceCode]
	if ok {
		for _, point := range entry.points {
			if point.Code == pointCode {
				condition.Input = entry.id + "/" + pointCode
				return condition, nil
			}
		}
	}
	return alarmservice.Condition{}, fmt.Errorf("%s is not an active point of the site file", condition.Input)
}

// watched is, for each of a device's active points, "no reading for ageSeconds" and "has a reading".
func watched(entry device, ageSeconds int64) (noData, fresh []alarmservice.Condition) {
	for _, point := range entry.points {
		input := entry.id + "/" + point.Code
		noData = append(noData, alarmservice.Condition{Kind: alarmservice.ConditionNoData, Input: input, AgeSeconds: ageSeconds})
		fresh = append(fresh, hasReading(input, point.ValueType))
	}
	return noData, fresh
}

func hasReading(input, valueType string) alarmservice.Condition {
	switch valueType {
	case "NUMBER":
		low, high := -anyReading, anyReading
		return alarmservice.Condition{Kind: alarmservice.ConditionRange, Input: input, Minimum: &low, Maximum: &high}
	case "BOOLEAN":
		return either(alarmservice.ConditionOr, []alarmservice.Condition{
			{Kind: alarmservice.ConditionCompare, Input: input, Operator: alarmservice.CompareEQ, Value: alarmservice.BooleanValue(true)},
			{Kind: alarmservice.ConditionCompare, Input: input, Operator: alarmservice.CompareEQ, Value: alarmservice.BooleanValue(false)},
		})
	default:
		return alarmservice.Condition{Kind: alarmservice.ConditionCompare, Input: input, Operator: alarmservice.CompareNE, Value: alarmservice.StringValue("")}
	}
}

// either joins conditions under kind; the evaluator wants two or more children, so one stands alone.
func either(kind alarmservice.ConditionKind, conditions []alarmservice.Condition) alarmservice.Condition {
	if len(conditions) == 1 {
		return conditions[0]
	}
	return alarmservice.Condition{Kind: kind, Children: conditions}
}

// sitePolicy names a rule's policy family and assignment after the Site and the rule's code, so
// publishing the file again finds the same ones.
func sitePolicy(siteID, siteCode, code string, policy alarmservice.AlarmPolicyRevision) SitePolicy {
	policy.SchemaVersion = alarmservice.AlarmPolicySchemaVersion
	policy.PolicyID = derivedID(siteID, policyKind, code)
	policy.AlarmType = code
	policy.SourceType = alarmmodel.SourceSiteRule
	policy.SourceReference = "alarm-rules:" + siteCode + "/" + code
	return SitePolicy{AssignmentID: derivedID(siteID, assignmentKind, code), Policy: policy}
}

func derivedID(siteID, kind, code string) string {
	digest := sha256.Sum256([]byte(code))
	return siteID[:8] + "-" + kind + "-7000-8000-" + hex.EncodeToString(digest[:6])
}

// plainName is a name without its bracketed detail: 「C 机组电表（3 号，UID 3）」→「C 机组电表」,
// 「冷却水进水温度（冷凝进，四台共用）」→「冷却水进水温度」.
func plainName(name string) string {
	before, _, _ := strings.Cut(name, "（")
	return strings.TrimSpace(before)
}
