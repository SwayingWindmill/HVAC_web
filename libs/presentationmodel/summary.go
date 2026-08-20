package presentationmodel

import (
	"errors"
	"strings"
	"time"
	_ "time/tzdata"
)

type State string

const (
	StateReady         State = "READY"
	StateAttention     State = "ATTENTION"
	StateNoData        State = "NO_DATA"
	StatePartial       State = "PARTIAL"
	StateStale         State = "STALE"
	StateSuspect       State = "SUSPECT"
	StateUnavailable   State = "UNAVAILABLE"
	StateNotAuthorized State = "NOT_AUTHORIZED"
	StateNotIntegrated State = "NOT_INTEGRATED"
)

const DenominatorApplicableKnownPresence = "APPLICABLE_WITH_KNOWN_PRESENCE"

type DevicePopulationObservation struct {
	Applicability string
	Availability  string
	Presence      string
	DisplayState  string
	EvaluatedAt   time.Time
}

type DevicePopulation struct {
	State               State
	Registered          int
	Applicable          int
	Observable          int
	Online              int
	Offline             int
	Stale               int
	Unknown             int
	Unavailable         int
	DenominatorPolicy   string
	Denominator         *int
	AvailabilityPercent *float64
	EvaluatedAt         *time.Time
}

func SiteLocalDayBounds(asOf time.Time, timezone string) (time.Time, time.Time, error) {
	if asOf.IsZero() || strings.TrimSpace(timezone) == "" || timezone == "Local" {
		return time.Time{}, time.Time{}, errors.New("site local day requires asOf and IANA timezone")
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("site local day timezone is invalid")
	}
	local := asOf.In(location)
	from := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	to := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, location)
	return from, to, nil
}

func ProjectDevicePopulation(registered int, complete bool, observations []DevicePopulationObservation) DevicePopulation {
	result := DevicePopulation{
		Registered:        registered,
		DenominatorPolicy: DenominatorApplicableKnownPresence,
	}
	if registered == 0 && complete {
		result.State = StateNoData
		return result
	}
	if registered < 0 || len(observations) > registered {
		result.State = StateUnavailable
		return result
	}

	knownPresence := 0
	var oldest time.Time
	for _, observation := range observations {
		if observation.Applicability == "NOT_APPLICABLE" {
			continue
		}
		result.Applicable++
		if oldest.IsZero() || (!observation.EvaluatedAt.IsZero() && observation.EvaluatedAt.Before(oldest)) {
			oldest = observation.EvaluatedAt
		}
		if observation.Availability != "AVAILABLE" || observation.DisplayState == "UNAVAILABLE" {
			result.Unavailable++
			continue
		}
		switch observation.Presence {
		case "ONLINE":
			knownPresence++
			result.Observable++
			if observation.DisplayState == "STALE" {
				result.Stale++
			} else {
				result.Online++
			}
		case "OFFLINE":
			knownPresence++
			result.Observable++
			result.Offline++
		default:
			result.Unknown++
		}
	}
	if !oldest.IsZero() {
		copy := oldest
		result.EvaluatedAt = &copy
	}

	if !complete || len(observations) != registered || result.Unknown > 0 || result.Unavailable > 0 {
		result.State = StatePartial
		return result
	}
	if result.Stale > 0 {
		result.State = StateStale
	} else {
		result.State = StateReady
	}
	if result.Applicable == 0 {
		return result
	}
	denominator := knownPresence
	if denominator != result.Applicable {
		result.State = StatePartial
		return result
	}
	availability := float64(result.Online) * 100 / float64(denominator)
	result.Denominator = &denominator
	result.AvailabilityPercent = &availability
	return result
}

func WorstState(values ...State) State {
	priority := map[State]int{
		StateReady:         0,
		StateAttention:     1,
		StateNoData:        2,
		StateStale:         3,
		StateSuspect:       4,
		StatePartial:       5,
		StateNotIntegrated: 6,
		StateNotAuthorized: 7,
		StateUnavailable:   8,
	}
	worst := StateReady
	for _, value := range values {
		if priority[value] > priority[worst] {
			worst = value
		}
	}
	return worst
}
