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
	"github.com/quanlaihe/hvac-web/libs/nameplate"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
)

type Compiled struct {
	TenantID string
	SiteID   string
	Policies []alarmservice.SiteRule
}

type rulesFile struct {
	SchemaVersion      int              `json:"schemaVersion"`
	Note               string           `json:"note"`
	SiteFile           string           `json:"siteFile"`
	Rules              []rule           `json:"rules"`
	ReadingsNotUpdated *readingsRule    `json:"readingsNotUpdated"`
	ReportsInterrupted *readingsRule    `json:"reportsInterrupted"`
	ReadingsFlat       *flatRule        `json:"readingsFlat"`
	DataQuality        *dataQualityFile `json:"dataQuality"`
}

// dataQualityFile raises one alarm per point from that point's quality profile,
// instead of one INFO alarm for every temperature that sits still for six hours.
type dataQualityFile struct {
	Suspect  *suspectRule `json:"suspect"`
	Flatline *flatRule    `json:"flatline"`
}

type suspectRule struct {
	readingsRule
	Reasons       []string            `json:"reasons"`
	MajorSeverity alarmmodel.Severity `json:"majorSeverity"`
	MajorPoints   []string            `json:"majorPoints"`
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

// flatRule raises, per reading in unit, when the reading has kept exactly the same value for
// ageSeconds, and clears once it moves (#616). Only readings a device produces itself are watched:
// one copied from another device's channel ("from") would raise again for the same stuck probe.
// {device}, {point} and {hours} in the wording are the device, the reading by its full name and
// ageSeconds in hours.
type flatRule struct {
	readingsRule
	Unit string `json:"unit"`
}

// A reading older than this is not judged stuck: that it stopped updating is the
// readingsNotUpdated rule's to say.
const flatFreshnessSeconds = 1800

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
	MeterPoints     []sitePoint             `json:"meterPoints"`
	QualityProfiles map[string]pointQuality `json:"qualityProfiles"`
	PhysicsChecks   []physicsCheck          `json:"physicsChecks"`
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
	Pump *struct {
		Device string `json:"device"`
		Point  string `json:"point"`
		Equals string `json:"equals"`
	} `json:"pump"`
	Chillers []struct {
		Device string `json:"device"`
		Point  string `json:"point"`
		Equals string `json:"equals"`
	} `json:"chillers"`
}

type sitePoint struct {
	Code           string          `json:"code"`
	Name           string          `json:"name"`
	Unit           string          `json:"unit"`
	ValueType      string          `json:"valueType"`
	Status         string          `json:"status"`
	From           json.RawMessage `json:"from"`
	QualityProfile string          `json:"qualityProfile"`
}

type pointQuality struct {
	Range *struct {
		Min float64 `json:"min"`
		Max float64 `json:"max"`
	} `json:"range"`
	MaxStep                *float64  `json:"maxStep"`
	Sentinels              []float64 `json:"sentinels"`
	FlatlineSeconds        *int64    `json:"flatlineSeconds"`
	FlatlineRunningSeconds *int64    `json:"flatlineRunningSeconds"`
	Running                []struct {
		Device string `json:"device"`
		Point  string `json:"point"`
		Equals string `json:"equals"`
	} `json:"running"`
	Inputs []struct {
		Device string `json:"device"`
		Point  string `json:"point"`
	} `json:"inputs"`
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
	if err := appendDataQuality(&compiled, rules.DataQuality, site, devices, order); err != nil {
		return Compiled{}, err
	}
	if err := appendPhysics(&compiled, site, devices); err != nil {
		return Compiled{}, err
	}
	if flat := rules.ReadingsFlat; flat != nil {
		for _, code := range order {
			entry := devices[code]
			for _, point := range entry.points {
				if point.Unit != flat.Unit || len(point.From) > 0 {
					continue
				}
				if profile := site.QualityProfiles[point.QualityProfile]; point.QualityProfile != "" && (profile.FlatlineSeconds != nil || profile.FlatlineRunningSeconds != nil) {
					continue
				}
				input := entry.id + "/" + point.Code
				wording := strings.NewReplacer("{device}", entry.name, "{point}", point.Name, "{hours}", strconv.FormatInt(flat.AgeSeconds/3600, 10))
				compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "readings-flat/"+code+"/"+point.Code, alarmservice.AlarmPolicyRevision{
					Title: wording.Replace(flat.Title), Summary: wording.Replace(flat.Summary), Severity: flat.Severity,
					QualityPolicy: alarmservice.EvaluationQualityUsable, FreshnessSeconds: flatFreshnessSeconds, TriggerMode: alarmservice.TriggerSimple,
					Raise: alarmservice.Condition{Kind: alarmservice.ConditionUnchanged, Input: input, AgeSeconds: flat.AgeSeconds},
					Clear: alarmservice.Condition{Kind: alarmservice.ConditionChanged, Input: input, AgeSeconds: flat.AgeSeconds},
				}))
			}
		}
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

func appendDataQuality(compiled *Compiled, section *dataQualityFile, site siteFile, devices map[string]device, order []string) error {
	if section == nil {
		return nil
	}
	for _, code := range order {
		entry := devices[code]
		for _, point := range entry.points {
			if point.QualityProfile == "" || len(point.From) > 0 {
				continue
			}
			profile, ok := site.QualityProfiles[point.QualityProfile]
			if !ok {
				return fmt.Errorf("point %s/%s names unknown quality profile %s", code, point.Code, point.QualityProfile)
			}
			input := entry.id + "/" + point.Code
			if section.Suspect != nil && profileSuspect(profile) {
				reasons := section.Suspect.Reasons
				if len(reasons) == 0 {
					reasons = []string{"OUT_OF_RANGE", "SPIKE", "SENTINEL", "DERIVED_INPUT"}
				}
				reasons = append([]string(nil), reasons...)
				severity := section.Suspect.Severity
				for _, major := range section.Suspect.MajorPoints {
					if major == code+"/"+point.Code {
						severity = section.Suspect.MajorSeverity
					}
				}
				wording := strings.NewReplacer("{device}", entry.name, "{point}", point.Name)
				compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "data-quality/suspect/"+code+"/"+point.Code, alarmservice.AlarmPolicyRevision{
					Title: wording.Replace(section.Suspect.Title), Summary: wording.Replace(section.Suspect.Summary), Severity: severity,
					QualityPolicy: alarmservice.EvaluationQualityUsable, FreshnessSeconds: flatFreshnessSeconds, TriggerMode: alarmservice.TriggerSimple,
					Raise: alarmservice.Condition{Kind: alarmservice.ConditionReasons, Input: input, Match: alarmservice.ReasonMatchAny, Reasons: reasons},
					Clear: alarmservice.Condition{Kind: alarmservice.ConditionReasons, Input: input, Match: alarmservice.ReasonMatchNone, Reasons: reasons},
				}))
			}
			if section.Flatline == nil {
				continue
			}
			if profile.FlatlineRunningSeconds != nil && *profile.FlatlineRunningSeconds > 0 && len(profile.Running) > 0 {
				running, stopped, err := runningConditions(profile, devices)
				if err != nil {
					return err
				}
				minutes := strconv.FormatInt(*profile.FlatlineRunningSeconds/60, 10)
				text := strings.NewReplacer("{device}", entry.name, "{point}", point.Name, "{minutes}", minutes, "{hours}", strconv.FormatInt(*profile.FlatlineRunningSeconds/3600, 10), "{when}", "在有机组运行时")
				compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "data-quality/flat-running/"+code+"/"+point.Code, alarmservice.AlarmPolicyRevision{
					Title: text.Replace(section.Flatline.Title), Summary: text.Replace(section.Flatline.Summary), Severity: section.Flatline.Severity,
					QualityPolicy: alarmservice.EvaluationQualityUsable, FreshnessSeconds: flatFreshnessSeconds, TriggerMode: alarmservice.TriggerSimple,
					Raise: alarmservice.Condition{Kind: alarmservice.ConditionAnd, Children: []alarmservice.Condition{
						{Kind: alarmservice.ConditionUnchanged, Input: input, AgeSeconds: *profile.FlatlineRunningSeconds},
						either(alarmservice.ConditionOr, running),
					}},
					Clear: alarmservice.Condition{Kind: alarmservice.ConditionOr, Children: []alarmservice.Condition{
						{Kind: alarmservice.ConditionChanged, Input: input, AgeSeconds: *profile.FlatlineRunningSeconds},
						either(alarmservice.ConditionAnd, stopped),
					}},
				}))
			}
			if profile.FlatlineSeconds != nil && *profile.FlatlineSeconds > 0 {
				text := strings.NewReplacer("{device}", entry.name, "{point}", point.Name, "{minutes}", strconv.FormatInt(*profile.FlatlineSeconds/60, 10), "{hours}", strconv.FormatInt(*profile.FlatlineSeconds/3600, 10), "{when}", "")
				compiled.Policies = append(compiled.Policies, sitePolicy(site.Site.ID, site.Site.Code, "data-quality/flat/"+code+"/"+point.Code, alarmservice.AlarmPolicyRevision{
					Title: text.Replace(section.Flatline.Title), Summary: text.Replace(section.Flatline.Summary), Severity: section.Flatline.Severity,
					QualityPolicy: alarmservice.EvaluationQualityUsable, FreshnessSeconds: flatFreshnessSeconds, TriggerMode: alarmservice.TriggerSimple,
					Raise: alarmservice.Condition{Kind: alarmservice.ConditionUnchanged, Input: input, AgeSeconds: *profile.FlatlineSeconds},
					Clear: alarmservice.Condition{Kind: alarmservice.ConditionChanged, Input: input, AgeSeconds: *profile.FlatlineSeconds},
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

func runningConditions(profile pointQuality, devices map[string]device) (running, stopped []alarmservice.Condition, err error) {
	for _, item := range profile.Running {
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
