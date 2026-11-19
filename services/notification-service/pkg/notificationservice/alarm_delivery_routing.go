package notificationservice

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

// Who is told of a Site's Alarms, over which channels, and when (P0-4,
// docs/specs/alarm-delivery.md). One routing file per Site sits in the repository
// (deploy/sites/<site>.alarm-routing.json) and names recipients only by id; their mail
// addresses and WeCom user ids live in one recipients file on the server, outside Git.

// DeliveryChannel is how one message goes out.
type DeliveryChannel string

const (
	ChannelSMTP     DeliveryChannel = "smtp"
	ChannelWeComApp DeliveryChannel = "wecom_app"
	ChannelWeComBot DeliveryChannel = "wecom_bot"
)

// wecomBotRecipient is the one WeCom group robot the adapter knows (its URL file).
const wecomBotRecipient = "group"

var severityRank = map[alarmmodel.Severity]int{
	alarmmodel.SeverityInfo: 1, alarmmodel.SeverityWarning: 2, alarmmodel.SeverityMinor: 3, alarmmodel.SeverityMajor: 4, alarmmodel.SeverityCritical: 5,
}

var recipientIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// SiteRouting is one Site's routing file.
type SiteRouting struct {
	SchemaVersion int    `json:"schemaVersion"`
	SiteID        string `json:"siteId"`
	SiteName      string `json:"siteName"`
	// Timezone is the Site's IANA zone, for quiet hours and the times in messages.
	Timezone string `json:"timezone"`
	// HoldMinutes waits this long after an Alarm begins before the first message, so an Alarm that
	// clears at once, or whose root cause arrives a moment later, tells nobody.
	HoldMinutes int `json:"holdMinutes"`
	// MaxAgeHours: an Alarm older than this when first seen (delivery was off, Notification was
	// down) is not announced any more.
	MaxAgeHours int         `json:"maxAgeHours"`
	QuietHours  *QuietHours `json:"quietHours,omitempty"`
	// RateLimitPerHour caps first and escalation messages per recipient per hour; CRITICAL is never capped.
	RateLimitPerHour int `json:"rateLimitPerHour"`
	// CooldownMinutes suppresses repeated FIRE messages for the same alarmType per recipient within
	// the site default window; CRITICAL is never suppressed.
	CooldownMinutes int `json:"cooldownMinutes,omitempty"`
	// SuppressWhenRootCause: while an Alarm of RootCauseAlarmType is active at the Site, Alarms whose
	// type starts with one of these prefixes tell nobody (a gateway offline makes every reading stale).
	RootCauseAlarmType    string          `json:"rootCauseAlarmType"`
	SuppressWhenRootCause []string        `json:"suppressWhenRootCause"`
	RootCauseRules        []RootCauseRule `json:"rootCauseRules,omitempty"`
	Routes                []AlarmRoute    `json:"routes"`
}

// RootCauseRule holds back downstream notifications while a matching root-cause Alarm is active.
type RootCauseRule struct {
	WhenActivePrefix  string   `json:"whenActivePrefix"`
	WhenDeviceID      string   `json:"whenDeviceId,omitempty"`
	ScopeSameDevice   bool     `json:"scopeSameDevice,omitempty"`
	SuppressPrefixes  []string `json:"suppressPrefixes"`
	SuppressDeviceIDs []string `json:"suppressDeviceIds,omitempty"`
}

// QuietHours are a daily window in the Site's time; Alarms below BypassSeverity wait until it ends.
type QuietHours struct {
	Start          string              `json:"start"`
	End            string              `json:"end"`
	BypassSeverity alarmmodel.Severity `json:"bypassSeverity"`
}

// AlarmRoute matches Alarms of at least MinSeverity (and, when given, of one of AlarmTypePrefixes).
// The first matching route of the file decides.
type AlarmRoute struct {
	Name               string              `json:"name"`
	MinSeverity        alarmmodel.Severity `json:"minSeverity"`
	AlarmTypePrefixes  []string            `json:"alarmTypePrefixes,omitempty"`
	CooldownMinutes    int                 `json:"cooldownMinutes,omitempty"`
	GroupWindowSeconds int                 `json:"groupWindowSeconds,omitempty"`
	GroupMaxItems      int                 `json:"groupMaxItems,omitempty"`
	GroupBy            string              `json:"groupBy,omitempty"`
	// Steps: the first is sent when the hold ends; each later one AfterMinutes after the first,
	// only while nobody acknowledged and the Alarm is still active.
	Steps          []EscalationStep `json:"steps"`
	NotifyAck      bool             `json:"notifyAck"`
	NotifyRecovery bool             `json:"notifyRecovery"`
}

type EscalationStep struct {
	AfterMinutes int               `json:"afterMinutes"`
	Recipients   []string          `json:"recipients"`
	Channels     []DeliveryChannel `json:"channels"`
}

// Recipient is one person's contacts, from the recipients file.
type Recipient struct {
	Name        string `json:"name"`
	Email       string `json:"email,omitempty"`
	WeComUserID string `json:"wecomUserId,omitempty"`
}

// RecipientDirectory is the recipients file: {"recipients": {"<id>": {...}}}.
type RecipientDirectory struct {
	Recipients map[string]Recipient `json:"recipients"`
}

// AlarmRoutingTable is every Site's routing, by Site id.
type AlarmRoutingTable struct {
	sites map[string]SiteRouting
}

func (table AlarmRoutingTable) Site(siteID string) (SiteRouting, bool) {
	routing, ok := table.sites[strings.ToLower(siteID)]
	return routing, ok
}

func (table AlarmRoutingTable) Len() int { return len(table.sites) }

// NewAlarmRoutingTable is a table of already checked routings (tests, one-off tools).
func NewAlarmRoutingTable(routings ...SiteRouting) AlarmRoutingTable {
	table := AlarmRoutingTable{sites: map[string]SiteRouting{}}
	for _, routing := range routings {
		table.sites[strings.ToLower(routing.SiteID)] = routing
	}
	return table
}

const (
	maxGroupWindowSeconds = 900
	defaultGroupMaxItems  = 10
	minGroupMaxItems      = 2
	maxGroupMaxItems      = 50
)

var (
	errRecipientsInvalid       = errors.New("recipients configuration is invalid")
	errRoutingInvalid          = errors.New("routing configuration is invalid")
	errRoutingRecipientInvalid = errors.New("routing recipient mapping is invalid")
)

type namedRoutingFile struct {
	base string
	body []byte
}

// LoadAlarmRouting reads every *.alarm-routing.json in dir and checks each against the recipients.
func LoadAlarmRouting(dir string, recipients RecipientDirectory) (AlarmRoutingTable, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.alarm-routing.json"))
	if err != nil {
		return AlarmRoutingTable{}, err
	}
	sort.Strings(paths)
	files := make([]namedRoutingFile, 0, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			return AlarmRoutingTable{}, err
		}
		files = append(files, namedRoutingFile{base: filepath.Base(path), body: body})
	}
	return loadAlarmRoutingFiles(files, recipients)
}

func loadAlarmRoutingFiles(files []namedRoutingFile, recipients RecipientDirectory) (AlarmRoutingTable, error) {
	table := AlarmRoutingTable{sites: map[string]SiteRouting{}}
	for _, file := range files {
		routing, err := ParseSiteRouting(file.body)
		if err != nil {
			return AlarmRoutingTable{}, fmt.Errorf("%s: %w", file.base, fmt.Errorf("%w: %w", errRoutingInvalid, err))
		}
		if err := routing.checkRecipients(recipients); err != nil {
			return AlarmRoutingTable{}, fmt.Errorf("%s: %w", file.base, fmt.Errorf("%w: %w", errRoutingRecipientInvalid, err))
		}
		key := strings.ToLower(routing.SiteID)
		if _, seen := table.sites[key]; seen {
			return AlarmRoutingTable{}, fmt.Errorf("%s: %w: Site %s is routed twice", file.base, errRoutingInvalid, routing.SiteID)
		}
		table.sites[key] = routing
	}
	return table, nil
}

// LoadRecipientDirectory reads the recipients file. It holds personal contacts: never log it.
func LoadRecipientDirectory(path string) (RecipientDirectory, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return RecipientDirectory{}, err
	}
	return parseRecipientDirectory(body)
}

func parseRecipientDirectory(body []byte) (RecipientDirectory, error) {
	var directory RecipientDirectory
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&directory); err != nil || decoder.More() {
		return RecipientDirectory{}, fmt.Errorf("%w: recipients file is not valid JSON of the documented shape", errRecipientsInvalid)
	}
	for id, recipient := range directory.Recipients {
		if !recipientIDPattern.MatchString(id) {
			return RecipientDirectory{}, fmt.Errorf("%w: recipient id %q is invalid", errRecipientsInvalid, id)
		}
		if strings.TrimSpace(recipient.Email) == "" && strings.TrimSpace(recipient.WeComUserID) == "" {
			return RecipientDirectory{}, fmt.Errorf("%w: recipient %s has neither email nor wecomUserId", errRecipientsInvalid, id)
		}
		if recipient.Email != "" && (!strings.Contains(recipient.Email, "@") || strings.ContainsAny(recipient.Email, " \r\n<>,")) {
			return RecipientDirectory{}, fmt.Errorf("%w: recipient %s has an invalid email", errRecipientsInvalid, id)
		}
		if strings.ContainsAny(recipient.WeComUserID, " \r\n|") {
			return RecipientDirectory{}, fmt.Errorf("%w: recipient %s has an invalid wecomUserId", errRecipientsInvalid, id)
		}
	}
	return directory, nil
}

// ParseSiteRouting reads and checks one routing file.
func ParseSiteRouting(body []byte) (SiteRouting, error) {
	var routing SiteRouting
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&routing); err != nil || decoder.More() {
		return SiteRouting{}, fmt.Errorf("routing file: %w", errors.New("invalid JSON"))
	}
	if routing.MaxAgeHours <= 0 {
		routing.MaxAgeHours = 12
	}
	if err := routing.Validate(); err != nil {
		return SiteRouting{}, err
	}
	return routing, nil
}

// Validate checks every field and route bound of a SiteRouting configuration.
func (routing SiteRouting) Validate() error {
	if routing.SchemaVersion != 1 {
		return errors.New("routing schemaVersion must be 1")
	}
	if !alarmmodel.IsUUIDv7(routing.SiteID) || strings.TrimSpace(routing.SiteName) == "" {
		return errors.New("routing needs the Site's id and name")
	}
	if _, err := time.LoadLocation(routing.Timezone); err != nil || routing.Timezone == "" {
		return errors.New("routing timezone is not an IANA zone")
	}
	if routing.HoldMinutes < 0 || routing.HoldMinutes > 60 {
		return errors.New("holdMinutes must be 0 to 60")
	}
	if routing.MaxAgeHours < 0 {
		return errors.New("maxAgeHours must not be negative")
	}
	if routing.RateLimitPerHour < 0 {
		return errors.New("rateLimitPerHour must not be negative")
	}
	if routing.CooldownMinutes < 0 || routing.CooldownMinutes > 10080 {
		return errors.New("cooldownMinutes must be 0 to 10080")
	}
	if quiet := routing.QuietHours; quiet != nil {
		if _, err := clockMinutes(quiet.Start); err != nil {
			return err
		}
		if _, err := clockMinutes(quiet.End); err != nil {
			return err
		}
		if quiet.Start == quiet.End {
			return errors.New("quiet hours start and end must differ")
		}
		if severityRank[quiet.BypassSeverity] == 0 {
			return errors.New("quiet hours bypassSeverity is invalid")
		}
	}
	if len(routing.SuppressWhenRootCause) > 0 && strings.TrimSpace(routing.RootCauseAlarmType) == "" {
		return errors.New("suppressWhenRootCause needs rootCauseAlarmType")
	}
	for index, rule := range routing.RootCauseRules {
		if strings.TrimSpace(rule.WhenActivePrefix) == "" {
			return fmt.Errorf("rootCauseRules[%d] needs whenActivePrefix", index)
		}
		if rule.WhenDeviceID != "" && strings.TrimSpace(rule.WhenDeviceID) == "" {
			return fmt.Errorf("rootCauseRules[%d] has a blank whenDeviceId", index)
		}
		if rule.ScopeSameDevice && !strings.HasSuffix(rule.WhenActivePrefix, "/") {
			return fmt.Errorf("rootCauseRules[%d] with scopeSameDevice needs trailing / on whenActivePrefix", index)
		}
		if len(rule.SuppressPrefixes) == 0 {
			return fmt.Errorf("rootCauseRules[%d] needs suppressPrefixes", index)
		}
		for _, prefix := range rule.SuppressPrefixes {
			if strings.TrimSpace(prefix) == "" {
				return fmt.Errorf("rootCauseRules[%d] has an empty suppressPrefix", index)
			}
		}
		for _, deviceID := range rule.SuppressDeviceIDs {
			if strings.TrimSpace(deviceID) == "" {
				return fmt.Errorf("rootCauseRules[%d] has an empty suppressDeviceId", index)
			}
		}
	}
	if len(routing.Routes) == 0 {
		return errors.New("routing needs at least one route")
	}
	for index, route := range routing.Routes {
		if strings.TrimSpace(route.Name) == "" || severityRank[route.MinSeverity] == 0 {
			return fmt.Errorf("route %d needs a name and a valid minSeverity", index)
		}
		if route.CooldownMinutes < 0 || route.CooldownMinutes > 10080 {
			return fmt.Errorf("route %s cooldownMinutes must be 0 to 10080", route.Name)
		}
		if route.GroupWindowSeconds < 0 || route.GroupWindowSeconds > maxGroupWindowSeconds {
			return fmt.Errorf("route %s groupWindowSeconds must be 0 to %d", route.Name, maxGroupWindowSeconds)
		}
		if route.GroupMaxItems != 0 && (route.GroupMaxItems < minGroupMaxItems || route.GroupMaxItems > maxGroupMaxItems) {
			return fmt.Errorf("route %s groupMaxItems must be 0 or %d to %d", route.Name, minGroupMaxItems, maxGroupMaxItems)
		}
		if !isValidGroupBy(route.GroupBy) {
			return fmt.Errorf("route %s groupBy %q must be site, severity, alarmType or empty", route.Name, route.GroupBy)
		}
		if len(route.Steps) == 0 || len(route.Steps) > 5 {
			return fmt.Errorf("route %s needs 1 to 5 steps", route.Name)
		}
		previous := -1
		for stepIndex, step := range route.Steps {
			if stepIndex == 0 && step.AfterMinutes != 0 {
				return fmt.Errorf("route %s: the first step is sent when the hold ends (afterMinutes 0)", route.Name)
			}
			if step.AfterMinutes <= previous || step.AfterMinutes > 24*60 {
				return fmt.Errorf("route %s: step afterMinutes must increase and stay within a day", route.Name)
			}
			previous = step.AfterMinutes
			if len(step.Channels) == 0 {
				return fmt.Errorf("route %s step %d has no channel", route.Name, stepIndex)
			}
			personal := false
			for _, channel := range step.Channels {
				switch channel {
				case ChannelSMTP, ChannelWeComApp:
					personal = true
				case ChannelWeComBot:
				default:
					return fmt.Errorf("route %s step %d: unknown channel %q", route.Name, stepIndex, channel)
				}
			}
			if personal && len(step.Recipients) == 0 {
				return fmt.Errorf("route %s step %d sends mail or WeCom app messages to nobody", route.Name, stepIndex)
			}
			for _, recipient := range step.Recipients {
				if !recipientIDPattern.MatchString(recipient) {
					return fmt.Errorf("route %s step %d: recipient id %q is invalid", route.Name, stepIndex, recipient)
				}
			}
		}
	}
	return nil
}

func isValidGroupBy(groupBy string) bool {
	switch groupBy {
	case "", "site", "severity", "alarmType":
		return true
	default:
		return false
	}
}

func clampRouteGrouping(route AlarmRoute) AlarmRoute {
	if route.GroupWindowSeconds < 0 {
		route.GroupWindowSeconds = 0
	} else if route.GroupWindowSeconds > maxGroupWindowSeconds {
		route.GroupWindowSeconds = maxGroupWindowSeconds
	}
	if route.GroupWindowSeconds > 0 {
		if route.GroupMaxItems == 0 {
			route.GroupMaxItems = defaultGroupMaxItems
		} else if route.GroupMaxItems < minGroupMaxItems {
			route.GroupMaxItems = minGroupMaxItems
		} else if route.GroupMaxItems > maxGroupMaxItems {
			route.GroupMaxItems = maxGroupMaxItems
		}
	} else if route.GroupMaxItems != 0 {
		if route.GroupMaxItems < minGroupMaxItems {
			route.GroupMaxItems = minGroupMaxItems
		} else if route.GroupMaxItems > maxGroupMaxItems {
			route.GroupMaxItems = maxGroupMaxItems
		}
	}
	if !isValidGroupBy(route.GroupBy) {
		route.GroupBy = ""
	}
	return route
}

func (routing SiteRouting) checkRecipients(directory RecipientDirectory) error {
	for _, route := range routing.Routes {
		for stepIndex, step := range route.Steps {
			for _, id := range step.Recipients {
				recipient, ok := directory.Recipients[id]
				if !ok {
					return fmt.Errorf("route %s step %d names recipient %s, who is not in the recipients file", route.Name, stepIndex, id)
				}
				for _, channel := range step.Channels {
					if (channel == ChannelSMTP && recipient.Email == "") || (channel == ChannelWeComApp && recipient.WeComUserID == "") {
						return fmt.Errorf("route %s step %d: recipient %s has no contact for %s", route.Name, stepIndex, id, channel)
					}
				}
			}
		}
	}
	return nil
}

// Route is the first route the Alarm matches.
func (routing SiteRouting) Route(alarmType string, severity alarmmodel.Severity) (AlarmRoute, bool) {
	route, _, ok := routing.routeWithIndex(alarmType, severity)
	return route, ok
}

func (routing SiteRouting) routeWithIndex(alarmType string, severity alarmmodel.Severity) (AlarmRoute, int, bool) {
	for index, route := range routing.Routes {
		if severityRank[severity] < severityRank[route.MinSeverity] {
			continue
		}
		if len(route.AlarmTypePrefixes) > 0 && !hasAnyPrefix(alarmType, route.AlarmTypePrefixes) {
			continue
		}
		return clampRouteGrouping(route), index, true
	}
	return AlarmRoute{}, -1, false
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func effectiveCooldownMinutes(routing SiteRouting, route AlarmRoute) int {
	if route.CooldownMinutes > 0 {
		return route.CooldownMinutes
	}
	return max(0, routing.CooldownMinutes)
}

func effectiveGroupMaxItems(route AlarmRoute) int {
	if route.GroupWindowSeconds <= 0 {
		return 1
	}
	if route.GroupMaxItems >= minGroupMaxItems && route.GroupMaxItems <= maxGroupMaxItems {
		return route.GroupMaxItems
	}
	if route.GroupMaxItems == 0 {
		return defaultGroupMaxItems
	}
	if route.GroupMaxItems < minGroupMaxItems {
		return minGroupMaxItems
	}
	return maxGroupMaxItems
}

func effectiveGroupBy(route AlarmRoute) string {
	switch route.GroupBy {
	case "severity", "alarmType", "site":
		return route.GroupBy
	default:
		return "site"
	}
}

// IsSuppressedByRootCauseForAlarm reports whether an active root-cause Alarm suppresses notifications for alarm.
func (routing SiteRouting) IsSuppressedByRootCauseForAlarm(alarm DeliveryAlarm, facts DeliveryFacts) bool {
	if facts.RootCauseActive && alarm.AlarmType != routing.RootCauseAlarmType && hasAnyPrefix(alarm.AlarmType, routing.SuppressWhenRootCause) {
		return true
	}
	activeAlarms := activeAlarmsFromFacts(facts)
	for _, rule := range routing.RootCauseRules {
		if !hasAnyPrefix(alarm.AlarmType, rule.SuppressPrefixes) {
			continue
		}
		if len(rule.SuppressDeviceIDs) > 0 && !alarmMatchesAnyDevice(alarm, rule.SuppressDeviceIDs) {
			continue
		}
		whenDevice := strings.TrimSpace(rule.WhenDeviceID)
		for _, active := range activeAlarms {
			if whenDevice == "" {
				if active.AlarmType == alarm.AlarmType {
					continue
				}
			} else if active.AlarmType == alarm.AlarmType && strings.EqualFold(strings.TrimSpace(alarm.DeviceID), strings.TrimSpace(active.DeviceID)) {
				continue
			}
			suffix, ok := strings.CutPrefix(active.AlarmType, rule.WhenActivePrefix)
			if !ok {
				continue
			}
			if whenDevice != "" && !alarmMatchesDevice(DeliveryAlarm{AlarmType: active.AlarmType, DeviceID: active.DeviceID}, whenDevice) {
				continue
			}
			if !rule.ScopeSameDevice {
				return true
			}
			device := strings.TrimPrefix(suffix, "/")
			if slash := strings.IndexByte(device, '/'); slash >= 0 {
				device = device[:slash]
			}
			if alarmMatchesDevice(alarm, device) {
				return true
			}
			if activeDev := strings.TrimSpace(active.DeviceID); activeDev != "" && strings.TrimSpace(alarm.DeviceID) != "" && strings.EqualFold(activeDev, strings.TrimSpace(alarm.DeviceID)) {
				return true
			}
		}
	}
	return false
}

func activeAlarmsFromFacts(facts DeliveryFacts) []ActiveAlarmFact {
	if len(facts.ActiveAlarmTypes) == 0 {
		return facts.ActiveAlarms
	}
	alarms := make([]ActiveAlarmFact, 0, len(facts.ActiveAlarms)+len(facts.ActiveAlarmTypes))
	seenTypes := map[string]bool{}
	for _, active := range facts.ActiveAlarms {
		alarms = append(alarms, active)
		seenTypes[active.AlarmType] = true
	}
	for _, alarmType := range facts.ActiveAlarmTypes {
		if !seenTypes[alarmType] {
			alarms = append(alarms, ActiveAlarmFact{AlarmType: alarmType})
		}
	}
	return alarms
}

func alarmMatchesAnyDevice(alarm DeliveryAlarm, deviceIDs []string) bool {
	for _, deviceID := range deviceIDs {
		if alarmMatchesDevice(alarm, deviceID) {
			return true
		}
	}
	return false
}

func alarmMatchesDevice(alarm DeliveryAlarm, deviceID string) bool {
	target := strings.TrimSpace(deviceID)
	if target == "" {
		return false
	}
	if dev := strings.TrimSpace(alarm.DeviceID); dev != "" {
		if strings.EqualFold(dev, target) {
			return true
		}
		// Only fall back to alarmType matching when deviceId is a UUID and target is a device code.
		if alarmmodel.IsUUIDv7(strings.ToLower(dev)) && !alarmmodel.IsUUIDv7(strings.ToLower(target)) {
			return alarmBelongsToDevice(alarm.AlarmType, target)
		}
		return false
	}
	return alarmBelongsToDevice(alarm.AlarmType, target)
}

func alarmBelongsToDevice(alarmType, device string) bool {
	if device == "" {
		return false
	}
	return alarmType == device ||
		strings.HasPrefix(alarmType, device+"/") ||
		strings.HasSuffix(alarmType, "/"+device) ||
		strings.Contains(alarmType, "/"+device+"/")
}

func clockMinutes(clock string) (int, error) {
	parsed, err := time.Parse("15:04", clock)
	if err != nil {
		return 0, fmt.Errorf("quiet hours time %q must be HH:MM", clock)
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}
