package gateway

import (
	"net/url"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
)

const (
	gatewayAlarmSiteID = "01910000-0001-7000-8000-000000000001"
	gatewayAlarmID     = "01910000-1000-7000-8000-000000000001"
)

func TestMatchPublicAlarmRoutesUsesDomainV1Shape(t *testing.T) {
	tests := []struct {
		path     string
		template string
		action   alarmauth.Action
		alarmID  string
	}{
		{path: "/api/v1/alarms", template: "/api/v1/alarms", action: alarmauth.ActionRead},
		{path: "/api/v1/alarms/" + gatewayAlarmID, template: "/api/v1/alarms/{alarmId}", action: alarmauth.ActionRead, alarmID: gatewayAlarmID},
		{path: "/api/v1/alarms/" + gatewayAlarmID + "/ack", template: "/api/v1/alarms/{alarmId}/ack", action: alarmauth.ActionAck, alarmID: gatewayAlarmID},
	}
	for _, test := range tests {
		route, ok := matchPublicAlarmRoute(test.path)
		if !ok || route.template != test.template || route.action != test.action || route.alarmID != test.alarmID {
			t.Fatalf("path=%s route=%#v matched=%v", test.path, route, ok)
		}
	}
	for _, legacy := range []string{
		"/api/v1/sites/" + gatewayAlarmSiteID + "/alarms",
		"/api/v1/sites/" + gatewayAlarmSiteID + "/alarms/" + gatewayAlarmID,
		"/api/v1/alarms/" + gatewayAlarmID + ":acknowledge",
	} {
		if route, ok := matchPublicAlarmRoute(legacy); ok {
			t.Fatalf("legacy Alarm route remained public: %s -> %#v", legacy, route)
		}
	}
}

func TestValidatePublicAlarmQueryRequiresSiteAndCursorBounds(t *testing.T) {
	valid := url.Values{
		"siteId":   {gatewayAlarmSiteID},
		"severity": {"MAJOR"},
		"status":   {"OPEN"},
		"limit":    {"200"},
		"cursor":   {"opaque-cursor"},
	}
	siteID, limit, ok := validatePublicAlarmQuery(valid)
	if !ok || siteID != gatewayAlarmSiteID || limit != 200 {
		t.Fatalf("valid Alarm query rejected: site=%q limit=%d ok=%v", siteID, limit, ok)
	}

	for name, query := range map[string]url.Values{
		"missing site": {"limit": {"50"}},
		"limit zero":   {"siteId": {gatewayAlarmSiteID}, "limit": {"0"}},
		"limit high":   {"siteId": {gatewayAlarmSiteID}, "limit": {"201"}},
		"unknown":      {"siteId": {gatewayAlarmSiteID}, "deviceId": {"caller-supplied"}},
		"long cursor":  {"siteId": {gatewayAlarmSiteID}, "cursor": {strings.Repeat("x", 4097)}},
	} {
		if _, _, ok := validatePublicAlarmQuery(query); ok {
			t.Fatalf("invalid Alarm query accepted: %s", name)
		}
	}
}
