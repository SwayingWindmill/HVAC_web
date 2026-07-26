package commandservice

import "github.com/quanlaihe/hvac-web/libs/commandmodel"

type commandCohortScope struct {
	siteID   string
	deviceID string
	enforced bool
}

func unrestrictedCommandCohort() commandCohortScope {
	return commandCohortScope{}
}

func exactCommandCohort(siteID, deviceID string) (commandCohortScope, error) {
	if !commandmodel.IsUUIDv7(siteID) || !commandmodel.IsUUIDv7(deviceID) {
		return commandCohortScope{}, ErrInvalidRequest
	}
	return commandCohortScope{siteID: siteID, deviceID: deviceID, enforced: true}, nil
}

func (scope commandCohortScope) querySiteID() any {
	if !scope.enforced {
		return nil
	}
	return scope.siteID
}

func (scope commandCohortScope) queryDeviceID() any {
	if !scope.enforced {
		return nil
	}
	return scope.deviceID
}
