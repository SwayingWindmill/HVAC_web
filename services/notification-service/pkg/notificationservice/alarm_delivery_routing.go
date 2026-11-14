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
	// SuppressWhenRootCause: while an Alarm of RootCauseAlarmType is active at the Site, Alarms whose
	// type starts with one of these prefixes tell nobody (a gateway offline makes every reading stale).
	RootCauseAlarmType    string       `json:"rootCauseAlarmType"`
	SuppressWhenRootCause []string     `json:"suppressWhenRootCause"`
	Routes                []AlarmRoute `json:"routes"`
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
	Name              string              `json:"name"`
	MinSeverity       alarmmodel.Severity `json:"minSeverity"`
	AlarmTypePrefixes []string            `json:"alarmTypePrefixes,omitempty"`
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

// LoadAlarmRouting reads every *.alarm-routing.json in dir and checks each against the recipients.
func LoadAlarmRouting(dir string, recipients RecipientDirectory) (AlarmRoutingTable, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.alarm-routing.json"))
	if err != nil {
		return AlarmRoutingTable{}, err
	}
	sort.Strings(paths)
	table := AlarmRoutingTable{sites: map[string]SiteRouting{}}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			return AlarmRoutingTable{}, err
		}
		routing, err := ParseSiteRouting(body)
		if err != nil {
			return AlarmRoutingTable{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		if err := routing.checkRecipients(recipients); err != nil {
			return AlarmRoutingTable{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		key := strings.ToLower(routing.SiteID)
		if _, seen := table.sites[key]; seen {
			return AlarmRoutingTable{}, fmt.Errorf("%s: Site %s is routed twice", filepath.Base(path), routing.SiteID)
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
	var directory RecipientDirectory
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&directory); err != nil {
		return RecipientDirectory{}, errors.New("recipients file is not valid JSON of the documented shape")
	}
	for id, recipient := range directory.Recipients {
		if !recipientIDPattern.MatchString(id) {
			return RecipientDirectory{}, fmt.Errorf("recipient id %q is invalid", id)
		}
		if strings.TrimSpace(recipient.Email) == "" && strings.TrimSpace(recipient.WeComUserID) == "" {
			return RecipientDirectory{}, fmt.Errorf("recipient %s has neither email nor wecomUserId", id)
		}
		if recipient.Email != "" && (!strings.Contains(recipient.Email, "@") || strings.ContainsAny(recipient.Email, " \r\n<>,")) {
			return RecipientDirectory{}, fmt.Errorf("recipient %s has an invalid email", id)
		}
		if strings.ContainsAny(recipient.WeComUserID, " \r\n|") {
			return RecipientDirectory{}, fmt.Errorf("recipient %s has an invalid wecomUserId", id)
		}
	}
	return directory, nil
}

// ParseSiteRouting reads and checks one routing file.
func ParseSiteRouting(body []byte) (SiteRouting, error) {
	var routing SiteRouting
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&routing); err != nil {
		return SiteRouting{}, fmt.Errorf("routing file: %w", err)
	}
	if routing.SchemaVersion != 1 {
		return SiteRouting{}, errors.New("routing schemaVersion must be 1")
	}
	if !alarmmodel.IsUUIDv7(routing.SiteID) || strings.TrimSpace(routing.SiteName) == "" {
		return SiteRouting{}, errors.New("routing needs the Site's id and name")
	}
	if _, err := time.LoadLocation(routing.Timezone); err != nil || routing.Timezone == "" {
		return SiteRouting{}, errors.New("routing timezone is not an IANA zone")
	}
	if routing.HoldMinutes < 0 || routing.HoldMinutes > 60 {
		return SiteRouting{}, errors.New("holdMinutes must be 0 to 60")
	}
	if routing.MaxAgeHours <= 0 {
		routing.MaxAgeHours = 12
	}
	if routing.RateLimitPerHour < 0 {
		return SiteRouting{}, errors.New("rateLimitPerHour must not be negative")
	}
	if quiet := routing.QuietHours; quiet != nil {
		if _, err := clockMinutes(quiet.Start); err != nil {
			return SiteRouting{}, err
		}
		if _, err := clockMinutes(quiet.End); err != nil {
			return SiteRouting{}, err
		}
		if quiet.Start == quiet.End {
			return SiteRouting{}, errors.New("quiet hours start and end must differ")
		}
		if severityRank[quiet.BypassSeverity] == 0 {
			return SiteRouting{}, errors.New("quiet hours bypassSeverity is invalid")
		}
	}
	if len(routing.SuppressWhenRootCause) > 0 && strings.TrimSpace(routing.RootCauseAlarmType) == "" {
		return SiteRouting{}, errors.New("suppressWhenRootCause needs rootCauseAlarmType")
	}
	if len(routing.Routes) == 0 {
		return SiteRouting{}, errors.New("routing needs at least one route")
	}
	for index, route := range routing.Routes {
		if strings.TrimSpace(route.Name) == "" || severityRank[route.MinSeverity] == 0 {
			return SiteRouting{}, fmt.Errorf("route %d needs a name and a valid minSeverity", index)
		}
		if len(route.Steps) == 0 || len(route.Steps) > 5 {
			return SiteRouting{}, fmt.Errorf("route %s needs 1 to 5 steps", route.Name)
		}
		previous := -1
		for stepIndex, step := range route.Steps {
			if stepIndex == 0 && step.AfterMinutes != 0 {
				return SiteRouting{}, fmt.Errorf("route %s: the first step is sent when the hold ends (afterMinutes 0)", route.Name)
			}
			if step.AfterMinutes <= previous || step.AfterMinutes > 24*60 {
				return SiteRouting{}, fmt.Errorf("route %s: step afterMinutes must increase and stay within a day", route.Name)
			}
			previous = step.AfterMinutes
			if len(step.Channels) == 0 {
				return SiteRouting{}, fmt.Errorf("route %s step %d has no channel", route.Name, stepIndex)
			}
			personal := false
			for _, channel := range step.Channels {
				switch channel {
				case ChannelSMTP, ChannelWeComApp:
					personal = true
				case ChannelWeComBot:
				default:
					return SiteRouting{}, fmt.Errorf("route %s step %d: unknown channel %q", route.Name, stepIndex, channel)
				}
			}
			if personal && len(step.Recipients) == 0 {
				return SiteRouting{}, fmt.Errorf("route %s step %d sends mail or WeCom app messages to nobody", route.Name, stepIndex)
			}
			for _, recipient := range step.Recipients {
				if !recipientIDPattern.MatchString(recipient) {
					return SiteRouting{}, fmt.Errorf("route %s step %d: recipient id %q is invalid", route.Name, stepIndex, recipient)
				}
			}
		}
	}
	return routing, nil
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
	for _, route := range routing.Routes {
		if severityRank[severity] < severityRank[route.MinSeverity] {
			continue
		}
		if len(route.AlarmTypePrefixes) > 0 && !hasAnyPrefix(alarmType, route.AlarmTypePrefixes) {
			continue
		}
		return route, true
	}
	return AlarmRoute{}, false
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func clockMinutes(clock string) (int, error) {
	parsed, err := time.Parse("15:04", clock)
	if err != nil {
		return 0, fmt.Errorf("quiet hours time %q must be HH:MM", clock)
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}
