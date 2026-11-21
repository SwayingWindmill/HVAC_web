// Package alarmrules turns a Site's rules file (deploy/sites/<site>.alarm-rules.json) into the
// Alarm policies its Site assignment evaluates (#609).
package alarmrules

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/nameplate"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/rulegraph"
)

type Compiled struct {
	TenantID string
	SiteID   string
	Policies []alarmservice.SiteRule
}

type rulesFile struct {
	SchemaVersion      int                  `json:"schemaVersion"`
	Note               string               `json:"note"`
	SiteFile           string               `json:"siteFile"`
	Templates          []ruleTemplate       `json:"templates,omitempty"`
	Rules              []rule               `json:"rules"`
	ReadingGaps        *readingsRule        `json:"readingGaps"`
	ReadingsNotUpdated *readingsRule        `json:"readingsNotUpdated"`
	ReportsInterrupted *readingsRule        `json:"reportsInterrupted"`
	ReadingsFlat       *flatRule            `json:"readingsFlat"`
	DataQuality        *dataQualityFile     `json:"dataQuality"`
	DeviceUntrusted    *deviceUntrustedRule `json:"deviceUntrusted"`
}

type ruleTemplate struct {
	Key              string                                `json:"key"`
	Extends          string                                `json:"extends,omitempty"`
	Template         string                                `json:"template,omitempty"`
	RuleKind         alarmservice.RuleKind                 `json:"ruleKind,omitempty"`
	ExecutionTarget  alarmservice.ExecutionTarget          `json:"executionTarget,omitempty"`
	DependencyGate   *alarmservice.DependencyGateMode      `json:"dependencyGate,omitempty"`
	Actions          *[]alarmservice.RuleAction            `json:"actions,omitempty"`
	Title            *string                               `json:"title,omitempty"`
	Summary          *string                               `json:"summary,omitempty"`
	Severity         *alarmmodel.Severity                  `json:"severity,omitempty"`
	QualityPolicy    *alarmservice.EvaluationQualityPolicy `json:"qualityPolicy,omitempty"`
	FreshnessSeconds *int64                                `json:"freshnessSeconds,omitempty"`
	TriggerMode      *alarmservice.TriggerMode             `json:"triggerMode,omitempty"`
	DurationSeconds  *int64                                `json:"durationSeconds,omitempty"`
	RepeatCount      *uint64                               `json:"repeatCount,omitempty"`
	Raise            *alarmservice.Condition               `json:"raise,omitempty"`
	Clear            *alarmservice.Condition               `json:"clear,omitempty"`
}

// dataQualityFile raises one alarm per point from that point's quality profile,
// instead of one INFO alarm for every temperature that sits still for six hours.
type dataQualityFile struct {
	Suspect  *suspectRule `json:"suspect"`
	Flatline *flatRule    `json:"flatline"`
}

type suspectRule struct {
	readingsRule
	DurationSeconds int64               `json:"durationSeconds"`
	Reasons         []string            `json:"reasons"`
	MajorSeverity   alarmmodel.Severity `json:"majorSeverity"`
	MajorPoints     []string            `json:"majorPoints"`
}

type deviceUntrustedRule struct {
	Enabled         bool                `json:"enabled"`
	Title           string              `json:"title"`
	Summary         string              `json:"summary"`
	Severity        alarmmodel.Severity `json:"severity"`
	DurationSeconds int64               `json:"durationSeconds"`
	MinPoints       int                 `json:"minPoints"`
	Reasons         []string            `json:"reasons"`
}

// A rule is a policy written with readings as "<device code>/<point code>".
type rule struct {
	Code             string                                `json:"code"`
	Template         string                                `json:"template,omitempty"`
	Device           string                                `json:"device,omitempty"`
	RuleKind         alarmservice.RuleKind                 `json:"ruleKind,omitempty"`
	ExecutionTarget  alarmservice.ExecutionTarget          `json:"executionTarget,omitempty"`
	DependsOn        []string                              `json:"dependsOn,omitempty"`
	DependencyGate   *alarmservice.DependencyGateMode      `json:"dependencyGate,omitempty"`
	Actions          *[]alarmservice.RuleAction            `json:"actions,omitempty"`
	Title            *string                               `json:"title,omitempty"`
	Summary          *string                               `json:"summary,omitempty"`
	Severity         *alarmmodel.Severity                  `json:"severity,omitempty"`
	QualityPolicy    *alarmservice.EvaluationQualityPolicy `json:"qualityPolicy,omitempty"`
	FreshnessSeconds *int64                                `json:"freshnessSeconds,omitempty"`
	TriggerMode      *alarmservice.TriggerMode             `json:"triggerMode,omitempty"`
	DurationSeconds  *int64                                `json:"durationSeconds,omitempty"`
	RepeatCount      *uint64                               `json:"repeatCount,omitempty"`
	Raise            *alarmservice.Condition               `json:"raise,omitempty"`
	Clear            *alarmservice.Condition               `json:"clear,omitempty"`
}

// readingsRule words a rule over the Site's own readings. readingGaps raises, per critical own
// active point, when that point has had no new reading for ageSeconds (#796); readingsNotUpdated
// raises, per device, when one of its remaining own active points has had no new reading for
// ageSeconds; reportsInterrupted raises when none of the Site's active points has (#615). {device},
// {point}, {points} and {minutes} in the wording are the device, the point or points by plain name
// and ageSeconds in minutes.
type readingsRule struct {
	Title      string              `json:"title"`
	Summary    string              `json:"summary"`
	Severity   alarmmodel.Severity `json:"severity"`
	AgeSeconds int64               `json:"ageSeconds"`
}

// flatRule raises, per reading in unit, when the reading has kept exactly the same value for
// ageSeconds, and clears once it moves (#616). Only readings a device produces itself are watched:
// one copied from another device's channel ("from") would raise again for the same stuck probe.
// {device}, {point} and {hours} in the wording are the device, the reading by its full name and
// ageSeconds in hours.
type flatRule struct {
	readingsRule
	Unit            string `json:"unit"`
	DurationSeconds int64  `json:"durationSeconds"`
}

// A reading older than this is not judged stuck: that it stopped updating is the
// readingsNotUpdated rule's to say.
const flatFreshnessSeconds = 1800

var (
	defaultUntrustedReasons = []string{"OUT_OF_RANGE", "SENTINEL", "SOURCE_UNTRUSTED"}
	defaultSuspectReasons   = []string{"OUT_OF_RANGE", "SPIKE", "SENTINEL", "SOURCE_UNTRUSTED", "DERIVED_INPUT"}
)

type runningRef struct {
	Device string `json:"device"`
	Point  string `json:"point"`
	Equals string `json:"equals"`
}

type siteFile struct {
	TenantID string `json:"tenantId"`
	Site     struct {
		ID   string `json:"id"`
		Code string `json:"code"`
	} `json:"site"`
	Devices []struct {
		ID         string          `json:"id"`
		SourceKey  string          `json:"sourceKey"`
		Code       string          `json:"code"`
		Name       string          `json:"name"`
		Points     json.RawMessage `json:"points"`
		MorePoints []sitePoint     `json:"morePoints"`
		Derive     json.RawMessage `json:"derive"`
		Running    []runningRef    `json:"running"`
	} `json:"devices"`
	MeterPoints     []sitePoint             `json:"meterPoints"`
	QualityProfiles map[string]pointQuality `json:"qualityProfiles"`
	PhysicsChecks   []physicsCheck          `json:"physicsChecks"`
	ProbePairs      []probePair             `json:"probePairs"`
}

// probePair is one supply/return pair. The offset itself is estimated by telemetry;
// the alarm fires when that estimate's health reason is PROBE_OFFSET.
type probePair struct {
	Code   string `json:"code"`
	Supply *struct {
		Device string `json:"device"`
		Point  string `json:"point"`
	} `json:"supply"`
	Alarm *struct {
		Title    string              `json:"title"`
		Summary  string              `json:"summary"`
		Severity alarmmodel.Severity `json:"severity"`
	} `json:"alarm"`
}

// physicsCheck is one site-file physics check. Optional checks without a subject
// stay in the file until the points exist; they do not become an alarm.
type physicsCheck struct {
	Code     string `json:"code"`
	Kind     string `json:"kind"`
	Optional bool   `json:"optional"`
	Subject  *struct {
		Device string `json:"device"`
		Point  string `json:"point"`
	} `json:"subject"`
	Alarm *struct {
		Title           string              `json:"title"`
		Summary         string              `json:"summary"`
		Severity        alarmmodel.Severity `json:"severity"`
		DurationSeconds int64               `json:"durationSeconds"`
	} `json:"alarm"`
	Pump     *runningRef  `json:"pump"`
	Chillers []runningRef `json:"chillers"`
	Running  []runningRef `json:"running"`
}

type sitePoint struct {
	Code           string          `json:"code"`
	Name           string          `json:"name"`
	PointType      string          `json:"pointType"`
	Unit           string          `json:"unit"`
	ValueType      string          `json:"valueType"`
	Status         string          `json:"status"`
	Critical       bool            `json:"critical"`
	From           json.RawMessage `json:"from"`
	QualityProfile string          `json:"qualityProfile"`
	Running        []runningRef    `json:"running"`
}

type pointQuality struct {
	Range *struct {
		Min float64 `json:"min"`
		Max float64 `json:"max"`
	} `json:"range"`
	MaxStep                  *float64     `json:"maxStep"`
	Sentinels                []float64    `json:"sentinels"`
	FlatlineSeconds          *int64       `json:"flatlineSeconds"`
	FlatlineRunningSeconds   *int64       `json:"flatlineRunningSeconds"`
	FlatlineStabilizeSeconds *int64       `json:"flatlineStabilizeSeconds"`
	Running                  []runningRef `json:"running"`
	Inputs                   []struct {
		Device string `json:"device"`
		Point  string `json:"point"`
	} `json:"inputs"`
}

type device struct {
	id         string
	code       string
	name       string
	sourceKey  string
	isDerived  bool
	deriveKind string
	deriveDeps []string
	running    []runningRef
	points     []sitePoint
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
			if point.PointType == "COMMAND" {
				continue
			}
			if point.Status == "" || point.Status == "ACTIVE" {
				active = append(active, point)
			}
		}
		isDerived, deriveKind, deriveDeps := parseDerive(entry.Derive)
		devices[entry.Code] = device{
			id: entry.ID, code: entry.Code, name: plainName(entry.Name), sourceKey: entry.SourceKey,
			isDerived: isDerived, deriveKind: deriveKind, deriveDeps: deriveDeps,
			running: entry.Running, points: active,
		}
		order = append(order, entry.Code)
	}

	templates, err := resolveTemplates(rules.Templates)
	if err != nil {
		return Compiled{}, err
	}

	compiled := Compiled{TenantID: site.TenantID, SiteID: site.Site.ID}
	ruleIndices := make(map[string]int, len(rules.Rules))
	ruleEntries := make([]rule, 0, len(rules.Rules))
	for _, entry := range rules.Rules {
		if strings.TrimSpace(entry.Code) == "" {
			return Compiled{}, fmt.Errorf("%s: rule code is required", rulesPath)
		}
		if _, duplicate := ruleIndices[entry.Code]; duplicate {
			return Compiled{}, fmt.Errorf("duplicate rule code %s", entry.Code)
		}
		policy, err := compileRuleEntry(entry, templates, devices)
		if err != nil {
			return Compiled{}, fmt.Errorf("rule %s: %w", entry.Code, err)
		}
		ruleIndices[entry.Code] = len(compiled.Policies)
		ruleEntries = append(ruleEntries, entry)
		compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, entry.Code, policy))
	}
	if gaps := rules.ReadingGaps; gaps != nil {
		minutes := strconv.FormatInt(gaps.AgeSeconds/60, 10)
		for _, code := range order {
			entry := devices[code]
			for _, point := range entry.points {
				if !point.Critical || !point.isReading() || point.isCopied() {
					continue
				}
				noData, fresh := watched(entry.id, []sitePoint{point}, gaps.AgeSeconds)
				wording := strings.NewReplacer("{device}", entry.name, "{point}", plainName(point.Name), "{minutes}", minutes)
				compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "reading-gap/"+code+"/"+point.Code, alarmservice.AlarmPolicyRevision{
					Title: wording.Replace(gaps.Title), Summary: wording.Replace(gaps.Summary), Severity: gaps.Severity,
					QualityPolicy: alarmservice.EvaluationQualityUsable, FreshnessSeconds: gaps.AgeSeconds, TriggerMode: alarmservice.TriggerSimple,
					Raise: noData[0], Clear: fresh[0],
				}))
			}
		}
	} else {
		for _, code := range order {
			for _, point := range devices[code].points {
				if point.Critical && point.isReading() && !point.isCopied() {
					return Compiled{}, fmt.Errorf("%s: critical point %s/%s requires readingGaps", rulesPath, code, point.Code)
				}
			}
		}
	}
	if readings := rules.ReadingsNotUpdated; readings != nil {
		untrusted := effectiveUntrustedReasons(rules.DeviceUntrusted)
		for _, code := range order {
			entry := devices[code]
			if entry.deriveKind == "cooling" || code == "btu-calc" {
				continue
			}
			var nonCriticalOwn []sitePoint
			for _, point := range entry.points {
				if point.isReading() && !point.isCopied() && !point.Critical {
					nonCriticalOwn = append(nonCriticalOwn, point)
				}
			}
			if len(nonCriticalOwn) == 0 {
				continue
			}
			noData, fresh := watched(entry.id, nonCriticalOwn, readings.AgeSeconds)
			gatedNoData := make([]alarmservice.Condition, len(noData))
			gatedFresh := make([]alarmservice.Condition, len(fresh))
			var names []string
			for index, point := range nonCriticalOwn {
				input := entry.id + "/" + point.Code
				gatedNoData[index] = alarmservice.Condition{
					Kind: alarmservice.ConditionAnd,
					Children: []alarmservice.Condition{
						noData[index],
						{Kind: alarmservice.ConditionReasons, Input: input, Match: alarmservice.ReasonMatchNone, Reasons: append([]string(nil), untrusted...)},
					},
				}
				gatedFresh[index] = alarmservice.Condition{
					Kind: alarmservice.ConditionOr,
					Children: []alarmservice.Condition{
						fresh[index],
						{Kind: alarmservice.ConditionReasons, Input: input, Match: alarmservice.ReasonMatchAny, Reasons: append([]string(nil), untrusted...)},
					},
				}
				names = append(names, plainName(point.Name))
			}
			wording := strings.NewReplacer("{device}", entry.name, "{points}", strings.Join(names, "、"), "{minutes}", strconv.FormatInt(readings.AgeSeconds/60, 10))
			compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "readings-not-updated/"+code, alarmservice.AlarmPolicyRevision{
				Title: wording.Replace(readings.Title), Summary: wording.Replace(readings.Summary), Severity: readings.Severity,
				QualityPolicy: alarmservice.EvaluationQualityUsable, FreshnessSeconds: readings.AgeSeconds, TriggerMode: alarmservice.TriggerSimple,
				// Raised while any reading is overdue and not already flagged untrusted, cleared once every one recovers.
				Raise: joinConditions(alarmservice.ConditionOr, gatedNoData), Clear: joinConditions(alarmservice.ConditionAnd, gatedFresh),
			}))
		}
	}
	if silence := rules.ReportsInterrupted; silence != nil {
		// Grouped by device, as a compound condition holds at most 32 others.
		var noData, fresh []alarmservice.Condition
		for _, code := range order {
			entry := devices[code]
			var readings []sitePoint
			for _, point := range entry.points {
				if point.isReading() {
					readings = append(readings, point)
				}
			}
			if len(readings) == 0 {
				continue
			}
			deviceNoData, deviceFresh := watched(entry.id, readings, silence.AgeSeconds)
			noData = append(noData, either(alarmservice.ConditionAnd, deviceNoData))
			fresh = append(fresh, either(alarmservice.ConditionOr, deviceFresh))
		}
		if len(noData) > 0 {
			wording := strings.NewReplacer("{minutes}", strconv.FormatInt(silence.AgeSeconds/60, 10))
			compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "reports-interrupted", alarmservice.AlarmPolicyRevision{
				Title: wording.Replace(silence.Title), Summary: wording.Replace(silence.Summary), Severity: silence.Severity,
				QualityPolicy: alarmservice.EvaluationQualityUsable, FreshnessSeconds: silence.AgeSeconds, TriggerMode: alarmservice.TriggerSimple,
				// Raised once every reading is overdue, cleared by the first fresh one.
				Raise: either(alarmservice.ConditionAnd, noData), Clear: either(alarmservice.ConditionOr, fresh),
			}))
		}
	}
	if err := appendDataQuality(&compiled, &rules, site, devices, order); err != nil {
		return Compiled{}, err
	}
	if err := appendPhysics(&compiled, site, devices); err != nil {
		return Compiled{}, err
	}
	if err := appendProbeOffsets(&compiled, site, devices); err != nil {
		return Compiled{}, err
	}
	if err := appendDeviceUntrusted(&compiled, &rules, site, devices, order); err != nil {
		return Compiled{}, err
	}
	if flat := rules.ReadingsFlat; flat != nil {
		for _, code := range order {
			entry := devices[code]
			if entry.isDerived {
				continue
			}
			for _, point := range entry.points {
				if point.Unit != flat.Unit || point.isCopied() {
					continue
				}
				profile := site.QualityProfiles[point.QualityProfile]
				if point.QualityProfile != "" && (profile.FlatlineSeconds != nil || profile.FlatlineRunningSeconds != nil) {
					continue
				}
				input := entry.id + "/" + point.Code
				runningSpecs := pointRunning(point, entry, profile)
				var running, stopped []alarmservice.Condition
				if len(runningSpecs) > 0 {
					var runErr error
					running, stopped, runErr = runningConditions(runningSpecs, devices)
					if runErr != nil {
						return Compiled{}, runErr
					}
				}
				raise, clear := flatlineCondition(input, flat.AgeSeconds, running, stopped)
				mode := alarmservice.TriggerSimple
				duration := flat.DurationSeconds
				if len(runningSpecs) > 0 && profile.FlatlineStabilizeSeconds != nil && *profile.FlatlineStabilizeSeconds > 0 {
					duration = *profile.FlatlineStabilizeSeconds
				}
				if duration > 0 {
					mode = alarmservice.TriggerDuration
				}
				wording := strings.NewReplacer("{device}", entry.name, "{point}", point.Name, "{hours}", strconv.FormatInt(flat.AgeSeconds/3600, 10))
				compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "readings-flat/"+code+"/"+point.Code, alarmservice.AlarmPolicyRevision{
					Title: wording.Replace(flat.Title), Summary: wording.Replace(flat.Summary), Severity: flat.Severity,
					QualityPolicy: alarmservice.EvaluationQualityValidOnly, FreshnessSeconds: flatFreshnessSeconds,
					TriggerMode: mode, DurationSeconds: duration,
					Raise: raise, Clear: clear,
				}))
			}
		}
	}
	if err := resolveRuleDependencies(&compiled, ruleEntries, ruleIndices); err != nil {
		return Compiled{}, err
	}
	if _, err := alarmservice.ValidateSiteRuleGraph(compiled.Policies); err != nil {
		if errors.Is(err, rulegraph.ErrRuleCycleDetected) {
			return Compiled{}, fmt.Errorf("cyclic rule dependency graph: %w", err)
		}
		return Compiled{}, err
	}
	return compiled, nil
}

func resolveTemplates(rawTemplates []ruleTemplate) (map[string]ruleTemplate, error) {
	byKey := make(map[string]ruleTemplate, len(rawTemplates))
	for _, tmpl := range rawTemplates {
		if strings.TrimSpace(tmpl.Key) == "" {
			return nil, fmt.Errorf("rule template key is required")
		}
		if _, dup := byKey[tmpl.Key]; dup {
			return nil, fmt.Errorf("duplicate rule template key %s", tmpl.Key)
		}
		if tmpl.Extends != "" && tmpl.Template != "" && tmpl.Extends != tmpl.Template {
			return nil, fmt.Errorf("rule template %s specifies conflicting extends and template", tmpl.Key)
		}
		byKey[tmpl.Key] = tmpl
	}

	resolved := make(map[string]ruleTemplate, len(rawTemplates))
	visiting := make(map[string]bool, len(rawTemplates))
	var resolveOne func(key string) (ruleTemplate, error)
	resolveOne = func(key string) (ruleTemplate, error) {
		if cached, ok := resolved[key]; ok {
			return cached, nil
		}
		if visiting[key] {
			return ruleTemplate{}, fmt.Errorf("rule template %s has cyclic inheritance", key)
		}
		tmpl, ok := byKey[key]
		if !ok {
			return ruleTemplate{}, fmt.Errorf("unknown rule template %s", key)
		}
		visiting[key] = true
		parentKey := tmpl.Extends
		if parentKey == "" {
			parentKey = tmpl.Template
		}
		var out ruleTemplate
		if parentKey != "" {
			parent, err := resolveOne(parentKey)
			if err != nil {
				return ruleTemplate{}, err
			}
			out = parent
		}
		out.Key = tmpl.Key
		out.Extends = ""
		out.Template = ""
		mergeTemplateOverrides(&out, tmpl)
		visiting[key] = false
		resolved[key] = out
		return out, nil
	}

	for _, tmpl := range rawTemplates {
		if _, err := resolveOne(tmpl.Key); err != nil {
			return nil, err
		}
	}
	return resolved, nil
}

func mergeTemplateOverrides(dst *ruleTemplate, src ruleTemplate) {
	if src.RuleKind != "" {
		dst.RuleKind = src.RuleKind
	}
	if src.ExecutionTarget != "" {
		dst.ExecutionTarget = src.ExecutionTarget
	}
	if src.DependencyGate != nil {
		v := *src.DependencyGate
		dst.DependencyGate = &v
	}
	if src.Actions != nil {
		cloned := cloneRuleActions(*src.Actions)
		dst.Actions = &cloned
	}
	if src.Title != nil {
		v := *src.Title
		dst.Title = &v
	}
	if src.Summary != nil {
		v := *src.Summary
		dst.Summary = &v
	}
	if src.Severity != nil {
		v := *src.Severity
		dst.Severity = &v
	}
	if src.QualityPolicy != nil {
		v := *src.QualityPolicy
		dst.QualityPolicy = &v
	}
	if src.FreshnessSeconds != nil {
		v := *src.FreshnessSeconds
		dst.FreshnessSeconds = &v
	}
	if src.TriggerMode != nil {
		v := *src.TriggerMode
		dst.TriggerMode = &v
	}
	if src.DurationSeconds != nil {
		v := *src.DurationSeconds
		dst.DurationSeconds = &v
	}
	if src.RepeatCount != nil {
		v := *src.RepeatCount
		dst.RepeatCount = &v
	}
	if src.Raise != nil {
		cloned := cloneCondition(*src.Raise)
		dst.Raise = &cloned
	}
	if src.Clear != nil {
		cloned := cloneCondition(*src.Clear)
		dst.Clear = &cloned
	}
}

func compileRuleEntry(entry rule, templates map[string]ruleTemplate, devices map[string]device) (alarmservice.AlarmPolicyRevision, error) {
	var merged ruleTemplate
	var overridden []string
	if entry.Template != "" {
		tmpl, ok := templates[entry.Template]
		if !ok {
			return alarmservice.AlarmPolicyRevision{}, fmt.Errorf("unknown template %s", entry.Template)
		}
		merged = tmpl
	}

	policy := alarmservice.AlarmPolicyRevision{
		RuleKind:        merged.RuleKind,
		ExecutionTarget: merged.ExecutionTarget,
		TemplateKey:     entry.Template,
	}
	if entry.RuleKind != "" {
		policy.RuleKind = entry.RuleKind
		overridden = append(overridden, "ruleKind")
	}
	if entry.ExecutionTarget != "" {
		policy.ExecutionTarget = entry.ExecutionTarget
		overridden = append(overridden, "executionTarget")
	}
	if merged.Title != nil {
		policy.Title = *merged.Title
	}
	if merged.Summary != nil {
		policy.Summary = *merged.Summary
	}
	if merged.Severity != nil {
		policy.Severity = *merged.Severity
	}
	if merged.QualityPolicy != nil {
		policy.QualityPolicy = *merged.QualityPolicy
	}
	if merged.FreshnessSeconds != nil {
		policy.FreshnessSeconds = *merged.FreshnessSeconds
	}
	if merged.TriggerMode != nil {
		policy.TriggerMode = *merged.TriggerMode
	}
	if merged.DurationSeconds != nil {
		policy.DurationSeconds = *merged.DurationSeconds
	}
	if merged.RepeatCount != nil {
		policy.RepeatCount = *merged.RepeatCount
	}
	if merged.DependencyGate != nil {
		policy.DependencyGate = *merged.DependencyGate
	}
	if merged.Actions != nil {
		policy.Actions = cloneRuleActions(*merged.Actions)
	}
	var raiseCond, clearCond alarmservice.Condition
	if merged.Raise != nil {
		raiseCond = cloneCondition(*merged.Raise)
	}
	if merged.Clear != nil {
		clearCond = cloneCondition(*merged.Clear)
	}

	if entry.DependencyGate != nil {
		policy.DependencyGate = *entry.DependencyGate
		overridden = append(overridden, "dependencyGate")
	}
	if entry.Actions != nil {
		policy.Actions = cloneRuleActions(*entry.Actions)
		overridden = append(overridden, "actions")
	}
	if entry.Title != nil {
		policy.Title = *entry.Title
		overridden = append(overridden, "title")
	}
	if entry.Summary != nil {
		policy.Summary = *entry.Summary
		overridden = append(overridden, "summary")
	}
	if entry.Severity != nil {
		policy.Severity = *entry.Severity
		overridden = append(overridden, "severity")
	}
	if entry.QualityPolicy != nil {
		policy.QualityPolicy = *entry.QualityPolicy
		overridden = append(overridden, "qualityPolicy")
	}
	if entry.FreshnessSeconds != nil {
		policy.FreshnessSeconds = *entry.FreshnessSeconds
		overridden = append(overridden, "freshnessSeconds")
	}
	if entry.TriggerMode != nil {
		policy.TriggerMode = *entry.TriggerMode
		overridden = append(overridden, "triggerMode")
		if *entry.TriggerMode == alarmservice.TriggerSimple {
			policy.DurationSeconds = 0
			policy.RepeatCount = 0
		}
	}
	if entry.DurationSeconds != nil {
		policy.DurationSeconds = *entry.DurationSeconds
		overridden = append(overridden, "durationSeconds")
	}
	if entry.RepeatCount != nil {
		policy.RepeatCount = *entry.RepeatCount
		overridden = append(overridden, "repeatCount")
	}
	if entry.Raise != nil {
		raiseCond = cloneCondition(*entry.Raise)
		overridden = append(overridden, "raise")
	}
	if entry.Clear != nil {
		clearCond = cloneCondition(*entry.Clear)
		overridden = append(overridden, "clear")
	}

	if entry.Template != "" && len(overridden) > 0 {
		sort.Strings(overridden)
		policy.OverriddenFields = overridden
	}

	if entry.Device != "" {
		dev, ok := devices[entry.Device]
		if !ok {
			return alarmservice.AlarmPolicyRevision{}, fmt.Errorf("unknown device %s", entry.Device)
		}
		policy.Title = strings.ReplaceAll(policy.Title, "{device}", dev.name)
		policy.Summary = strings.ReplaceAll(policy.Summary, "{device}", dev.name)
		raiseCond = substituteConditionDevice(raiseCond, dev.code)
		clearCond = substituteConditionDevice(clearCond, dev.code)
		policy.Actions = substituteActionsDevice(policy.Actions, dev.name, dev.code)
	} else {
		if strings.Contains(policy.Title, "{device}") || strings.Contains(policy.Summary, "{device}") ||
			conditionContainsPlaceholder(raiseCond, "{device}") || conditionContainsPlaceholder(clearCond, "{device}") ||
			actionsContainPlaceholder(policy.Actions, "{device}") {
			return alarmservice.AlarmPolicyRevision{}, fmt.Errorf("uses {device} placeholder without specifying device")
		}
	}

	if len(policy.Actions) > alarmservice.MaxActionsPerRule {
		return alarmservice.AlarmPolicyRevision{}, fmt.Errorf("actions exceed maximum of %d", alarmservice.MaxActionsPerRule)
	}
	for index := range policy.Actions {
		if policy.Actions[index].Kind == alarmservice.RuleActionProtectionCommand {
			rawDevice := policy.Actions[index].TargetDeviceID
			if targetDev, ok := devices[rawDevice]; ok {
				policy.Actions[index].TargetDeviceID = targetDev.id
			} else if !alarmmodel.IsUUIDv7(rawDevice) {
				return alarmservice.AlarmPolicyRevision{}, fmt.Errorf("action %d: unknown targetDeviceId %q", index, rawDevice)
			}
		}
		if err := policy.Actions[index].Validate(); err != nil {
			return alarmservice.AlarmPolicyRevision{}, fmt.Errorf("action %d: %w", index, err)
		}
	}
	switch policy.DependencyGate {
	case "", alarmservice.DependencyGateNone, alarmservice.DependencyGateAllActive, alarmservice.DependencyGateAnyActive:
	default:
		return alarmservice.AlarmPolicyRevision{}, fmt.Errorf("invalid dependencyGate %q", policy.DependencyGate)
	}

	raise, err := resolve(raiseCond, devices)
	if err != nil {
		return alarmservice.AlarmPolicyRevision{}, err
	}
	clear, err := resolve(clearCond, devices)
	if err != nil {
		return alarmservice.AlarmPolicyRevision{}, err
	}
	if policy.RuleKind != "" {
		switch policy.RuleKind {
		case alarmservice.RuleKindThreshold, alarmservice.RuleKindSlope, alarmservice.RuleKindWindow, alarmservice.RuleKindExpression:
		default:
			return alarmservice.AlarmPolicyRevision{}, fmt.Errorf("invalid ruleKind %q", policy.RuleKind)
		}
	}
	if policy.ExecutionTarget != "" {
		switch policy.ExecutionTarget {
		case alarmservice.ExecutionTargetCloud, alarmservice.ExecutionTargetEdge, alarmservice.ExecutionTargetCloudAndEdge:
		default:
			return alarmservice.AlarmPolicyRevision{}, fmt.Errorf("invalid executionTarget %q", policy.ExecutionTarget)
		}
	}
	if err := raise.Validate(); err != nil {
		return alarmservice.AlarmPolicyRevision{}, fmt.Errorf("raise: %w", err)
	}
	if err := clear.Validate(); err != nil {
		return alarmservice.AlarmPolicyRevision{}, fmt.Errorf("clear: %w", err)
	}
	policy.Raise = raise
	policy.Clear = clear
	return policy, nil
}

func resolveRuleDependencies(compiled *Compiled, entries []rule, ruleIndices map[string]int) error {
	assignmentByCode := make(map[string]string, len(compiled.Policies))
	for _, siteRule := range compiled.Policies {
		assignmentByCode[siteRule.Policy.AlarmType] = siteRule.AssignmentID
	}

	for _, entry := range entries {
		if len(entry.DependsOn) == 0 {
			continue
		}
		if len(entry.DependsOn) > 16 {
			return fmt.Errorf("rule %s: dependsOn exceeds maximum of 16", entry.Code)
		}
		seen := make(map[string]bool, len(entry.DependsOn))
		depIDs := make([]string, 0, len(entry.DependsOn))
		for _, depCode := range entry.DependsOn {
			if entry.Device != "" {
				depCode = strings.ReplaceAll(depCode, "{device}", entry.Device)
			} else if strings.Contains(depCode, "{device}") {
				return fmt.Errorf("rule %s: dependsOn uses {device} placeholder without specifying device", entry.Code)
			}
			if depCode == entry.Code {
				return fmt.Errorf("rule %s: cannot depend on itself", entry.Code)
			}
			if seen[depCode] {
				return fmt.Errorf("rule %s: duplicate dependsOn %s", entry.Code, depCode)
			}
			seen[depCode] = true
			upstreamID, ok := assignmentByCode[depCode]
			if !ok {
				return fmt.Errorf("rule %s: dependsOn unknown rule %s", entry.Code, depCode)
			}
			depIDs = append(depIDs, upstreamID)
		}
		idx := ruleIndices[entry.Code]
		compiled.Policies[idx].Policy.DependsOnRuleIDs = depIDs
	}
	for _, entry := range entries {
		idx := ruleIndices[entry.Code]
		policy := compiled.Policies[idx].Policy
		if (policy.DependencyGate == alarmservice.DependencyGateAllActive || policy.DependencyGate == alarmservice.DependencyGateAnyActive) && len(policy.DependsOnRuleIDs) == 0 {
			return fmt.Errorf("rule %s: dependencyGate %s requires non-empty dependsOn", entry.Code, policy.DependencyGate)
		}
	}
	return nil
}

func cloneRuleActions(actions []alarmservice.RuleAction) []alarmservice.RuleAction {
	if actions == nil {
		return nil
	}
	cloned := make([]alarmservice.RuleAction, len(actions))
	for index, action := range actions {
		cloned[index] = action.Clone()
	}
	return cloned
}

func substituteActionsDevice(actions []alarmservice.RuleAction, deviceName, deviceCode string) []alarmservice.RuleAction {
	if len(actions) == 0 {
		return actions
	}
	updated := cloneRuleActions(actions)
	for index := range updated {
		if updated[index].WorkOrderTitle != "" {
			updated[index].WorkOrderTitle = strings.ReplaceAll(updated[index].WorkOrderTitle, "{device}", deviceName)
		}
		if updated[index].TargetDeviceID != "" {
			updated[index].TargetDeviceID = strings.ReplaceAll(updated[index].TargetDeviceID, "{device}", deviceCode)
		}
	}
	return updated
}

func actionsContainPlaceholder(actions []alarmservice.RuleAction, placeholder string) bool {
	for _, action := range actions {
		if strings.Contains(action.WorkOrderTitle, placeholder) || strings.Contains(action.TargetDeviceID, placeholder) {
			return true
		}
	}
	return false
}

func cloneCondition(condition alarmservice.Condition) alarmservice.Condition {
	cloned := condition
	if condition.Minimum != nil {
		v := *condition.Minimum
		cloned.Minimum = &v
	}
	if condition.Maximum != nil {
		v := *condition.Maximum
		cloned.Maximum = &v
	}
	if condition.Delta != nil {
		v := *condition.Delta
		cloned.Delta = &v
	}
	if condition.Threshold != nil {
		v := *condition.Threshold
		cloned.Threshold = &v
	}
	if condition.Reasons != nil {
		cloned.Reasons = append([]string(nil), condition.Reasons...)
	}
	if condition.ExprInputs != nil {
		cloned.ExprInputs = make(map[string]string, len(condition.ExprInputs))
		for k, v := range condition.ExprInputs {
			cloned.ExprInputs[k] = v
		}
	}
	if len(condition.Children) > 0 {
		cloned.Children = make([]alarmservice.Condition, len(condition.Children))
		for i, child := range condition.Children {
			cloned.Children[i] = cloneCondition(child)
		}
	}
	return cloned
}

func substituteConditionDevice(condition alarmservice.Condition, deviceCode string) alarmservice.Condition {
	if len(condition.Children) > 0 {
		children := make([]alarmservice.Condition, len(condition.Children))
		for i, child := range condition.Children {
			children[i] = substituteConditionDevice(child, deviceCode)
		}
		condition.Children = children
		return condition
	}
	if condition.Kind == alarmservice.ConditionExpression && condition.ExprInputs != nil {
		replaced := make(map[string]string, len(condition.ExprInputs))
		for alias, ref := range condition.ExprInputs {
			replaced[alias] = strings.ReplaceAll(ref, "{device}", deviceCode)
		}
		condition.ExprInputs = replaced
		return condition
	}
	if condition.Input != "" {
		condition.Input = strings.ReplaceAll(condition.Input, "{device}", deviceCode)
	}
	return condition
}

func conditionContainsPlaceholder(condition alarmservice.Condition, placeholder string) bool {
	if len(condition.Children) > 0 {
		for _, child := range condition.Children {
			if conditionContainsPlaceholder(child, placeholder) {
				return true
			}
		}
		return false
	}
	if condition.Kind == alarmservice.ConditionExpression {
		for _, ref := range condition.ExprInputs {
			if strings.Contains(ref, placeholder) {
				return true
			}
		}
		return false
	}
	return strings.Contains(condition.Input, placeholder)
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
	if condition.Kind == alarmservice.ConditionExpression {
		if len(condition.ExprInputs) > 0 {
			resolvedInputs := make(map[string]string, len(condition.ExprInputs))
			for alias, rawRef := range condition.ExprInputs {
				resolvedRef, err := resolveInputRef(rawRef, devices)
				if err != nil {
					return alarmservice.Condition{}, err
				}
				resolvedInputs[alias] = resolvedRef
			}
			condition.ExprInputs = resolvedInputs
		}
		return condition, nil
	}
	resolvedRef, err := resolveInputRef(condition.Input, devices)
	if err != nil {
		return alarmservice.Condition{}, err
	}
	condition.Input = resolvedRef
	return condition, nil
}

func resolveInputRef(inputRef string, devices map[string]device) (string, error) {
	deviceCode, pointCode, _ := strings.Cut(inputRef, "/")
	entry, ok := devices[deviceCode]
	if ok {
		for _, point := range entry.points {
			if point.Code == pointCode {
				return entry.id + "/" + pointCode, nil
			}
		}
	}
	return "", fmt.Errorf("%s is not an active point of the site file", inputRef)
}

// isCopied reports whether a point is copied from another device's channel ("from") rather than
// read by this device itself.
func (point sitePoint) isCopied() bool {
	trimmed := bytes.TrimSpace(point.From)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// isReading reports whether a point carries a telemetry or counter reading rather than a logical
// controller state ("STATE") or write target ("COMMAND").
func (point sitePoint) isReading() bool {
	return point.PointType == "" || point.PointType == "TELEMETRY" || point.PointType == "COUNTER"
}

// watched is, for each of the given points on deviceID, "no reading for ageSeconds" and "has a reading".
func watched(deviceID string, points []sitePoint, ageSeconds int64) (noData, fresh []alarmservice.Condition) {
	for _, point := range points {
		input := deviceID + "/" + point.Code
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

// either joins conditions under kind, folding >8 children into balanced chunks so every compound
// node stays within the evaluator's child-count limit.
func either(kind alarmservice.ConditionKind, conditions []alarmservice.Condition) alarmservice.Condition {
	return joinConditions(kind, conditions)
}

func joinConditions(kind alarmservice.ConditionKind, conditions []alarmservice.Condition) alarmservice.Condition {
	if len(conditions) == 0 {
		return alarmservice.Condition{}
	}
	if len(conditions) == 1 {
		return conditions[0]
	}
	if len(conditions) <= 8 {
		return alarmservice.Condition{Kind: kind, Children: append([]alarmservice.Condition(nil), conditions...)}
	}
	groups := (len(conditions) + 7) / 8
	base := len(conditions) / groups
	extra := len(conditions) % groups
	parents := make([]alarmservice.Condition, 0, groups)
	offset := 0
	for index := 0; index < groups; index++ {
		size := base
		if index < extra {
			size++
		}
		chunk := append([]alarmservice.Condition(nil), conditions[offset:offset+size]...)
		parents = append(parents, alarmservice.Condition{Kind: kind, Children: chunk})
		offset += size
	}
	return joinConditions(kind, parents)
}

func effectiveUntrustedReasons(rule *deviceUntrustedRule) []string {
	if rule != nil && len(rule.Reasons) > 0 {
		return append([]string(nil), rule.Reasons...)
	}
	return append([]string(nil), defaultUntrustedReasons...)
}

func minUntrustedPoints(rule *deviceUntrustedRule) int {
	if rule != nil && rule.MinPoints >= 2 {
		return rule.MinPoints
	}
	return 2
}

func monitoredQualityPoints(entry device, site siteFile) ([]sitePoint, error) {
	if entry.isDerived {
		return nil, nil
	}
	var monitored []sitePoint
	for _, point := range entry.points {
		if point.QualityProfile == "" || point.isCopied() {
			continue
		}
		profile, ok := site.QualityProfiles[point.QualityProfile]
		if !ok {
			return nil, fmt.Errorf("point %s/%s names unknown quality profile %s", entry.code, point.Code, point.QualityProfile)
		}
		if profileSuspect(profile) {
			monitored = append(monitored, point)
		}
	}
	return monitored, nil
}

func flatlineCondition(input string, ageSeconds int64, running, stopped []alarmservice.Condition) (alarmservice.Condition, alarmservice.Condition) {
	untrusted := append([]string(nil), defaultUntrustedReasons...)
	raiseChildren := []alarmservice.Condition{
		{Kind: alarmservice.ConditionUnchanged, Input: input, AgeSeconds: ageSeconds},
		{Kind: alarmservice.ConditionReasons, Input: input, Match: alarmservice.ReasonMatchNone, Reasons: untrusted},
	}
	clearChildren := []alarmservice.Condition{
		{Kind: alarmservice.ConditionChanged, Input: input, AgeSeconds: ageSeconds},
		{Kind: alarmservice.ConditionReasons, Input: input, Match: alarmservice.ReasonMatchAny, Reasons: append([]string(nil), untrusted...)},
	}
	if len(running) > 0 {
		raiseChildren = append(raiseChildren, joinConditions(alarmservice.ConditionOr, running))
		clearChildren = append(clearChildren, joinConditions(alarmservice.ConditionAnd, stopped))
	}
	return joinConditions(alarmservice.ConditionAnd, raiseChildren), joinConditions(alarmservice.ConditionOr, clearChildren)
}

func pointRunning(point sitePoint, entry device, profile pointQuality) []runningRef {
	if len(point.Running) > 0 {
		return point.Running
	}
	if len(entry.running) > 0 {
		return entry.running
	}
	return profile.Running
}

func appendDataQuality(compiled *Compiled, rules *rulesFile, site siteFile, devices map[string]device, order []string) error {
	section := rules.DataQuality
	if section == nil {
		return nil
	}
	untrusted := effectiveUntrustedReasons(rules.DeviceUntrusted)
	untrustedEnabled := rules.DeviceUntrusted != nil && rules.DeviceUntrusted.Enabled && section.Suspect != nil
	minPoints := minUntrustedPoints(rules.DeviceUntrusted)

	for _, code := range order {
		entry := devices[code]
		if entry.isDerived {
			continue
		}
		monitored, err := monitoredQualityPoints(entry, site)
		if err != nil {
			return err
		}
		qualifiesUntrusted := untrustedEnabled && len(monitored) >= minPoints

		for _, point := range entry.points {
			if point.QualityProfile == "" || point.isCopied() {
				continue
			}
			profile := site.QualityProfiles[point.QualityProfile]
			input := entry.id + "/" + point.Code
			if section.Suspect != nil && profileSuspect(profile) {
				reasons := section.Suspect.Reasons
				if len(reasons) == 0 {
					reasons = append([]string(nil), defaultSuspectReasons...)
				} else {
					reasons = append([]string(nil), reasons...)
				}
				severity := section.Suspect.Severity
				for _, major := range section.Suspect.MajorPoints {
					if major == code+"/"+point.Code && section.Suspect.MajorSeverity != "" {
						severity = section.Suspect.MajorSeverity
					}
				}
				raise := alarmservice.Condition{Kind: alarmservice.ConditionReasons, Input: input, Match: alarmservice.ReasonMatchAny, Reasons: reasons}
				clear := alarmservice.Condition{Kind: alarmservice.ConditionReasons, Input: input, Match: alarmservice.ReasonMatchNone, Reasons: reasons}
				if qualifiesUntrusted {
					var peerNotUntrusted, peerUntrusted []alarmservice.Condition
					for _, peer := range monitored {
						if peer.Code == point.Code {
							continue
						}
						peerInput := entry.id + "/" + peer.Code
						peerNotUntrusted = append(peerNotUntrusted, alarmservice.Condition{
							Kind: alarmservice.ConditionReasons, Input: peerInput, Match: alarmservice.ReasonMatchNone, Reasons: append([]string(nil), untrusted...),
						})
						peerUntrusted = append(peerUntrusted, alarmservice.Condition{
							Kind: alarmservice.ConditionReasons, Input: peerInput, Match: alarmservice.ReasonMatchAny, Reasons: append([]string(nil), untrusted...),
						})
					}
					raise = alarmservice.Condition{
						Kind:     alarmservice.ConditionAnd,
						Children: []alarmservice.Condition{raise, joinConditions(alarmservice.ConditionOr, peerNotUntrusted)},
					}
					clear = alarmservice.Condition{
						Kind:     alarmservice.ConditionOr,
						Children: []alarmservice.Condition{clear, joinConditions(alarmservice.ConditionAnd, peerUntrusted)},
					}
				}
				mode := alarmservice.TriggerSimple
				if section.Suspect.DurationSeconds > 0 {
					mode = alarmservice.TriggerDuration
				}
				wording := strings.NewReplacer("{device}", entry.name, "{point}", point.Name)
				compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "data-quality/suspect/"+code+"/"+point.Code, alarmservice.AlarmPolicyRevision{
					Title: wording.Replace(section.Suspect.Title), Summary: wording.Replace(section.Suspect.Summary), Severity: severity,
					QualityPolicy: alarmservice.EvaluationQualityUsable, FreshnessSeconds: flatFreshnessSeconds,
					TriggerMode: mode, DurationSeconds: section.Suspect.DurationSeconds,
					Raise: raise, Clear: clear,
				}))
			}
			if section.Flatline == nil {
				continue
			}
			runningSpecs := pointRunning(point, entry, profile)
			if profile.FlatlineRunningSeconds != nil && *profile.FlatlineRunningSeconds > 0 && len(runningSpecs) > 0 {
				running, stopped, err := runningConditions(runningSpecs, devices)
				if err != nil {
					return err
				}
				raise, clear := flatlineCondition(input, *profile.FlatlineRunningSeconds, running, stopped)
				mode := alarmservice.TriggerSimple
				duration := section.Flatline.DurationSeconds
				if profile.FlatlineStabilizeSeconds != nil && *profile.FlatlineStabilizeSeconds > 0 {
					duration = *profile.FlatlineStabilizeSeconds
				}
				if duration > 0 {
					mode = alarmservice.TriggerDuration
				}
				minutes := strconv.FormatInt(*profile.FlatlineRunningSeconds/60, 10)
				text := strings.NewReplacer("{device}", entry.name, "{point}", point.Name, "{minutes}", minutes, "{hours}", strconv.FormatInt(*profile.FlatlineRunningSeconds/3600, 10), "{when}", "在有机组运行时")
				compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "data-quality/flat-running/"+code+"/"+point.Code, alarmservice.AlarmPolicyRevision{
					Title: text.Replace(section.Flatline.Title), Summary: text.Replace(section.Flatline.Summary), Severity: section.Flatline.Severity,
					QualityPolicy: alarmservice.EvaluationQualityValidOnly, FreshnessSeconds: flatFreshnessSeconds,
					TriggerMode: mode, DurationSeconds: duration,
					Raise: raise, Clear: clear,
				}))
			}
			if profile.FlatlineSeconds != nil && *profile.FlatlineSeconds > 0 {
				raise, clear := flatlineCondition(input, *profile.FlatlineSeconds, nil, nil)
				text := strings.NewReplacer("{device}", entry.name, "{point}", point.Name, "{minutes}", strconv.FormatInt(*profile.FlatlineSeconds/60, 10), "{hours}", strconv.FormatInt(*profile.FlatlineSeconds/3600, 10), "{when}", "")
				compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "data-quality/flat/"+code+"/"+point.Code, alarmservice.AlarmPolicyRevision{
					Title: text.Replace(section.Flatline.Title), Summary: text.Replace(section.Flatline.Summary), Severity: section.Flatline.Severity,
					QualityPolicy: alarmservice.EvaluationQualityValidOnly, FreshnessSeconds: flatFreshnessSeconds, TriggerMode: alarmservice.TriggerSimple,
					Raise: raise, Clear: clear,
				}))
			}
		}
	}
	return nil
}

const physicsFreshnessSeconds = 900

func appendPhysics(compiled *Compiled, site siteFile, devices map[string]device) error {
	for _, check := range site.PhysicsChecks {
		if check.Alarm == nil || check.Alarm.Title == "" {
			continue
		}
		if check.Kind == nameplate.KindPumpWithoutChiller {
			if err := appendPumpWithoutChiller(compiled, site, devices, check); err != nil {
				return err
			}
			continue
		}
		reason, ok := nameplate.QualityReason(check.Kind)
		if !ok || check.Subject == nil {
			if check.Optional {
				continue
			}
			return fmt.Errorf("physics check %s is not a point-quality check", check.Code)
		}
		entry, ok := devices[check.Subject.Device]
		if !ok {
			return fmt.Errorf("physics check %s names unknown device %s", check.Code, check.Subject.Device)
		}
		if check.Kind == nameplate.KindChilledDirection && entry.isDerived {
			continue
		}
		found := false
		for _, point := range entry.points {
			if point.Code == check.Subject.Point && len(point.From) == 0 {
				found = true
			}
		}
		if !found {
			if check.Optional {
				continue
			}
			return fmt.Errorf("physics check %s names %s/%s, which is not an active reading of that device", check.Code, check.Subject.Device, check.Subject.Point)
		}
		input := entry.id + "/" + check.Subject.Point
		mode := alarmservice.TriggerSimple
		if check.Alarm.DurationSeconds > 0 {
			mode = alarmservice.TriggerDuration
		}
		severity := check.Alarm.Severity
		if severity == "" {
			severity = alarmmodel.SeverityWarning
		}
		compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "physics/"+check.Code, alarmservice.AlarmPolicyRevision{
			Title: check.Alarm.Title, Summary: check.Alarm.Summary, Severity: severity,
			QualityPolicy: alarmservice.EvaluationQualityUsable, FreshnessSeconds: physicsFreshnessSeconds,
			TriggerMode: mode, DurationSeconds: check.Alarm.DurationSeconds,
			Raise: alarmservice.Condition{Kind: alarmservice.ConditionReasons, Input: input, Match: alarmservice.ReasonMatchAny, Reasons: []string{reason}},
			Clear: alarmservice.Condition{Kind: alarmservice.ConditionReasons, Input: input, Match: alarmservice.ReasonMatchNone, Reasons: []string{reason}},
		}))
	}
	return nil
}

func appendPumpWithoutChiller(compiled *Compiled, site siteFile, devices map[string]device, check physicsCheck) error {
	if check.Pump == nil || len(check.Chillers) == 0 {
		if check.Optional {
			return nil
		}
		return fmt.Errorf("physics check %s needs a pump and the chillers", check.Code)
	}
	pump, err := runningInput(devices, check.Pump.Device, check.Pump.Point, check.Pump.Equals, true)
	if err != nil {
		return fmt.Errorf("physics check %s: %w", check.Code, err)
	}
	stopped := []alarmservice.Condition{pump}
	started := []alarmservice.Condition{{Kind: alarmservice.ConditionCompare, Input: pump.Input, Operator: alarmservice.CompareNE, Value: pump.Value}}
	for _, chiller := range check.Chillers {
		running, err := runningInput(devices, chiller.Device, chiller.Point, chiller.Equals, true)
		if err != nil {
			return fmt.Errorf("physics check %s: %w", check.Code, err)
		}
		stopped = append(stopped, alarmservice.Condition{Kind: alarmservice.ConditionCompare, Input: running.Input, Operator: alarmservice.CompareNE, Value: running.Value})
		started = append(started, running)
	}
	mode := alarmservice.TriggerSimple
	if check.Alarm.DurationSeconds > 0 {
		mode = alarmservice.TriggerDuration
	}
	severity := check.Alarm.Severity
	if severity == "" {
		severity = alarmmodel.SeverityWarning
	}
	compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "physics/"+check.Code, alarmservice.AlarmPolicyRevision{
		Title: check.Alarm.Title, Summary: check.Alarm.Summary, Severity: severity,
		QualityPolicy: alarmservice.EvaluationQualityUsable, FreshnessSeconds: physicsFreshnessSeconds,
		TriggerMode: mode, DurationSeconds: check.Alarm.DurationSeconds,
		Raise: alarmservice.Condition{Kind: alarmservice.ConditionAnd, Children: stopped},
		Clear: alarmservice.Condition{Kind: alarmservice.ConditionOr, Children: started},
	}))
	return nil
}

func appendProbeOffsets(compiled *Compiled, site siteFile, devices map[string]device) error {
	for _, pair := range site.ProbePairs {
		if pair.Alarm == nil || pair.Alarm.Title == "" || pair.Supply == nil {
			continue
		}
		entry, ok := devices[pair.Supply.Device]
		if !ok {
			return fmt.Errorf("probe pair %s names unknown device %s", pair.Code, pair.Supply.Device)
		}
		found := false
		for _, point := range entry.points {
			if point.Code == pair.Supply.Point && len(point.From) == 0 {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("probe pair %s names %s/%s, which is not an active reading of that device", pair.Code, pair.Supply.Device, pair.Supply.Point)
		}
		input := entry.id + "/" + pair.Supply.Point
		severity := pair.Alarm.Severity
		if severity == "" {
			severity = alarmmodel.SeverityWarning
		}
		compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "probe-offset/"+pair.Code, alarmservice.AlarmPolicyRevision{
			Title: pair.Alarm.Title, Summary: pair.Alarm.Summary, Severity: severity,
			QualityPolicy: alarmservice.EvaluationQualityUsable, FreshnessSeconds: physicsFreshnessSeconds,
			TriggerMode: alarmservice.TriggerSimple,
			Raise:       alarmservice.Condition{Kind: alarmservice.ConditionReasons, Input: input, Match: alarmservice.ReasonMatchAny, Reasons: []string{"PROBE_OFFSET"}},
			Clear:       alarmservice.Condition{Kind: alarmservice.ConditionReasons, Input: input, Match: alarmservice.ReasonMatchNone, Reasons: []string{"PROBE_OFFSET"}},
		}))
	}
	return nil
}

func appendDeviceUntrusted(compiled *Compiled, rules *rulesFile, site siteFile, devices map[string]device, order []string) error {
	du := rules.DeviceUntrusted
	if du == nil || !du.Enabled || rules.DataQuality == nil || rules.DataQuality.Suspect == nil {
		return nil
	}
	minPoints := minUntrustedPoints(du)
	untrusted := effectiveUntrustedReasons(du)
	severity := du.Severity
	if severity == "" {
		severity = alarmmodel.SeverityMajor
	}
	mode := alarmservice.TriggerSimple
	if du.DurationSeconds > 0 {
		mode = alarmservice.TriggerDuration
	}
	origins := buildDeviceOriginMap(site, devices)

	for _, code := range order {
		entry := devices[code]
		monitored, err := monitoredQualityPoints(entry, site)
		if err != nil {
			return err
		}
		if len(monitored) < minPoints {
			continue
		}
		raiseConds := make([]alarmservice.Condition, 0, len(monitored))
		clearConds := make([]alarmservice.Condition, 0, len(monitored))
		pointNames := make([]string, 0, len(monitored))
		for _, point := range monitored {
			input := entry.id + "/" + point.Code
			raiseConds = append(raiseConds, alarmservice.Condition{
				Kind: alarmservice.ConditionReasons, Input: input, Match: alarmservice.ReasonMatchAny, Reasons: append([]string(nil), untrusted...),
			})
			clearConds = append(clearConds, alarmservice.Condition{
				Kind: alarmservice.ConditionReasons, Input: input, Match: alarmservice.ReasonMatchNone, Reasons: append([]string(nil), untrusted...),
			})
			pointNames = append(pointNames, plainName(point.Name))
		}
		affected := affectedRuleTitles(compiled.Policies, code, origins)
		titleTemplate := du.Title
		if titleTemplate == "" {
			titleTemplate = "{device}多个点位数据不可信"
		}
		summaryTemplate := du.Summary
		if summaryTemplate == "" {
			summaryTemplate = "{device}的全部 {count} 个监测点位同时不可信，关联告警规则：{rules}。"
		}
		title, summary := renderDeviceUntrustedCopy(titleTemplate, summaryTemplate, entry.name, len(monitored), du.DurationSeconds, pointNames, affected)
		compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "device-untrusted/"+code, alarmservice.AlarmPolicyRevision{
			Title: title, Summary: summary, Severity: severity,
			QualityPolicy: alarmservice.EvaluationQualityUsable, FreshnessSeconds: flatFreshnessSeconds,
			TriggerMode: mode, DurationSeconds: du.DurationSeconds,
			Raise: joinConditions(alarmservice.ConditionAnd, raiseConds),
			Clear: joinConditions(alarmservice.ConditionOr, clearConds),
		}))
	}
	return nil
}

func renderDeviceUntrustedCopy(titleTemplate, summaryTemplate, deviceName string, count int, durationSeconds int64, pointNames, affected []string) (string, string) {
	render := func(pointsText, rulesText string) (string, string) {
		replacer := strings.NewReplacer(
			"{device}", deviceName,
			"{count}", strconv.Itoa(count),
			"{points}", pointsText,
			"{rules}", rulesText,
			"{minutes}", strconv.FormatInt(durationSeconds/60, 10),
			"{seconds}", strconv.FormatInt(durationSeconds, 10),
		)
		return replacer.Replace(titleTemplate), replacer.Replace(summaryTemplate)
	}
	pointsText := strings.Join(pointNames, "、")
	rulesText := "无"
	if len(affected) > 0 {
		rulesText = strings.Join(affected, "、")
	}
	title, summary := render(pointsText, rulesText)
	if len(summary) <= 2048 {
		return title, summary
	}
	chosenRules := rulesText
	for kept := len(affected) - 1; kept >= 0; kept-- {
		short := fmt.Sprintf("等 %d 条规则", len(affected))
		if kept > 0 {
			short = strings.Join(affected[:kept], "、") + fmt.Sprintf("等 %d 条规则", len(affected))
		}
		chosenRules = short
		title, summary = render(pointsText, chosenRules)
		if len(summary) <= 2048 {
			return title, summary
		}
	}
	for kept := len(pointNames) - 1; kept >= 0; kept-- {
		shortPoints := fmt.Sprintf("等 %d 个点位", len(pointNames))
		if kept > 0 {
			shortPoints = strings.Join(pointNames[:kept], "、") + shortPoints
		}
		title, summary = render(shortPoints, chosenRules)
		if len(summary) <= 2048 {
			return title, summary
		}
	}
	return title, summary
}

type deviceOriginMap struct {
	inputOrigins map[string]map[string]bool
	idToCode     map[string]string
}

func buildDeviceOriginMap(site siteFile, devices map[string]device) deviceOriginMap {
	bySourceKey := map[string]string{}
	idToCode := map[string]string{}
	for code, entry := range devices {
		idToCode[entry.id] = code
		if entry.sourceKey != "" {
			bySourceKey[entry.sourceKey] = code
		}
	}
	resolveCode := func(ref string) string {
		if code, ok := bySourceKey[ref]; ok {
			return code
		}
		if _, ok := devices[ref]; ok {
			return ref
		}
		return ""
	}
	upstream := map[string]map[string]bool{}
	var collectUpstream func(code string, visited map[string]bool) map[string]bool
	collectUpstream = func(code string, visited map[string]bool) map[string]bool {
		if cached, ok := upstream[code]; ok {
			return cached
		}
		if visited[code] {
			return nil
		}
		visited[code] = true
		result := map[string]bool{code: true}
		for _, dep := range devices[code].deriveDeps {
			depCode := resolveCode(dep)
			if depCode == "" {
				continue
			}
			for origin := range collectUpstream(depCode, visited) {
				result[origin] = true
			}
		}
		upstream[code] = result
		return result
	}
	for code := range devices {
		collectUpstream(code, map[string]bool{})
	}
	inputOrigins := map[string]map[string]bool{}
	for code, entry := range devices {
		for _, point := range entry.points {
			key := entry.id + "/" + point.Code
			set := map[string]bool{}
			for origin := range upstream[code] {
				set[origin] = true
			}
			if srcKey := parseFromDevice(point.From); srcKey != "" {
				if srcCode := resolveCode(srcKey); srcCode != "" {
					for origin := range upstream[srcCode] {
						set[origin] = true
					}
				}
			}
			if point.QualityProfile != "" {
				for _, in := range site.QualityProfiles[point.QualityProfile].Inputs {
					if inCode := resolveCode(in.Device); inCode != "" {
						for origin := range upstream[inCode] {
							set[origin] = true
						}
					}
				}
			}
			inputOrigins[key] = set
		}
	}
	return deviceOriginMap{inputOrigins: inputOrigins, idToCode: idToCode}
}

func affectedRuleTitles(policies []alarmservice.SiteRule, deviceCode string, origins deviceOriginMap) []string {
	seen := map[string]bool{}
	var titles []string
	for _, rule := range policies {
		alarmType := rule.Policy.AlarmType
		if strings.HasPrefix(alarmType, "data-quality/") ||
			strings.HasPrefix(alarmType, "device-untrusted/") ||
			strings.HasPrefix(alarmType, "reading-gap/") ||
			strings.HasPrefix(alarmType, "readings-not-updated/") ||
			strings.HasPrefix(alarmType, "reports-interrupted") ||
			strings.HasPrefix(alarmType, "readings-flat/") {
			continue
		}
		if !conditionTouchesDevice(rule.Policy.Raise, deviceCode, origins) && !conditionTouchesDevice(rule.Policy.Clear, deviceCode, origins) {
			continue
		}
		if !seen[rule.Policy.Title] {
			seen[rule.Policy.Title] = true
			titles = append(titles, rule.Policy.Title)
		}
	}
	return titles
}

func conditionTouchesDevice(condition alarmservice.Condition, deviceCode string, origins deviceOriginMap) bool {
	if len(condition.Children) > 0 {
		for _, child := range condition.Children {
			if conditionTouchesDevice(child, deviceCode, origins) {
				return true
			}
		}
		return false
	}
	if condition.Kind == alarmservice.ConditionExpression {
		for _, inputKey := range condition.ExprInputs {
			if inputTouchesDevice(inputKey, deviceCode, origins) {
				return true
			}
		}
		return false
	}
	return inputTouchesDevice(condition.Input, deviceCode, origins)
}

func inputTouchesDevice(inputKey, deviceCode string, origins deviceOriginMap) bool {
	if origins.inputOrigins[inputKey][deviceCode] {
		return true
	}
	devID, _, ok := strings.Cut(inputKey, "/")
	return ok && origins.idToCode[devID] == deviceCode
}

func parseDerive(raw json.RawMessage) (isDerived bool, kind string, deps []string) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false, "", nil
	}
	var spec struct {
		Kind   string   `json:"kind"`
		From   []string `json:"from"`
		Flow   []string `json:"flow"`
		Supply []string `json:"supply"`
		Return []string `json:"return"`
		Power  string   `json:"power"`
	}
	if err := json.Unmarshal(trimmed, &spec); err != nil {
		return true, "", nil
	}
	deps = append(deps, spec.From...)
	for _, ref := range [][]string{spec.Flow, spec.Supply, spec.Return} {
		if len(ref) > 0 && ref[0] != "" {
			deps = append(deps, ref[0])
		}
	}
	if spec.Power != "" {
		deps = append(deps, spec.Power)
	}
	return true, spec.Kind, deps
}

func parseFromDevice(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var pair []string
	if err := json.Unmarshal(trimmed, &pair); err != nil || len(pair) == 0 {
		return ""
	}
	return pair[0]
}

func runningInput(devices map[string]device, code, point, equals string, require bool) (alarmservice.Condition, error) {
	entry, ok := devices[code]
	if !ok {
		return alarmservice.Condition{}, fmt.Errorf("unknown device %s", code)
	}
	found := false
	for _, candidate := range entry.points {
		if candidate.Code == point {
			found = true
		}
	}
	if !found && require {
		return alarmservice.Condition{}, fmt.Errorf("%s has no active point %s", code, point)
	}
	return alarmservice.Condition{Kind: alarmservice.ConditionCompare, Input: entry.id + "/" + point, Operator: alarmservice.CompareEQ, Value: alarmservice.StringValue(equals)}, nil
}

func profileSuspect(profile pointQuality) bool {
	return profile.Range != nil || profile.MaxStep != nil || len(profile.Sentinels) > 0 || len(profile.Inputs) > 0
}

func runningConditions(specs []runningRef, devices map[string]device) (running, stopped []alarmservice.Condition, err error) {
	for _, item := range specs {
		entry, ok := devices[item.Device]
		if !ok {
			return nil, nil, fmt.Errorf("quality profile running device %s is not in the site file", item.Device)
		}
		found := false
		for _, point := range entry.points {
			if point.Code == item.Point {
				found = true
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("quality profile running point %s/%s is not active", item.Device, item.Point)
		}
		input := entry.id + "/" + item.Point
		running = append(running, alarmservice.Condition{Kind: alarmservice.ConditionCompare, Input: input, Operator: alarmservice.CompareEQ, Value: alarmservice.StringValue(item.Equals)})
		stopped = append(stopped, alarmservice.Condition{Kind: alarmservice.ConditionCompare, Input: input, Operator: alarmservice.CompareNE, Value: alarmservice.StringValue(item.Equals)})
	}
	return running, stopped, nil
}

// sitePolicy names a rule's policy family and assignment after the Site and the rule's code, so
// publishing the file again finds the same ones.
func sitePolicy(siteID, siteCode, code string, policy alarmservice.AlarmPolicyRevision) alarmservice.SiteRule {
	policy.SchemaVersion = alarmservice.AlarmPolicySchemaVersion
	policy.PolicyID = derivedID(siteID, policyKind, code)
	policy.AlarmType = code
	policy.SourceType = alarmmodel.SourceSiteRule
	policy.SourceReference = "alarm-rules:" + siteCode + "/" + code
	return alarmservice.SiteRule{AssignmentID: derivedID(siteID, assignmentKind, code), Policy: policy}
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
