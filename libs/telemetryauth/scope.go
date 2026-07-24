package telemetryauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
)

const (
	MaximumTargets       = 100
	MaximumKeysPerTarget = 64
	MaximumTotalKeys     = 2048
)

type Action string

const (
	ActionSnapshotRead       Action = "telemetry.snapshot.read"
	ActionBatchRead          Action = "telemetry.batch.read"
	ActionSubscribe          Action = "telemetry.subscribe"
	ActionResubscribe        Action = "telemetry.resubscribe"
	ActionRecoveryUse        Action = "telemetry.recovery.use"
	ActionRecoveryCheckpoint Action = "telemetry.recovery.checkpoint"
)

func (action Action) Valid() bool {
	switch action {
	case ActionSnapshotRead, ActionBatchRead, ActionSubscribe, ActionResubscribe, ActionRecoveryUse, ActionRecoveryCheckpoint:
		return true
	default:
		return false
	}
}

type Target struct {
	DeviceID string   `json:"deviceId"`
	Keys     []string `json:"keys"`
}

type DecisionRequest struct {
	ActingOrganizationID string   `json:"actingOrganizationId"`
	Action               Action   `json:"action"`
	Targets              []Target `json:"targets"`
}

func (request DecisionRequest) Validate() error {
	if !validUUIDv7(request.ActingOrganizationID) {
		return errors.New("acting organization must be a UUIDv7")
	}
	if !request.Action.Valid() {
		return errors.New("telemetry action is invalid")
	}
	_, err := CanonicalTargets(request.Targets)
	return err
}

var telemetryKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)

func CanonicalTargets(targets []Target) ([]Target, error) {
	if len(targets) == 0 || len(targets) > MaximumTargets {
		return nil, errors.New("telemetry target count is invalid")
	}
	canonical := make([]Target, len(targets))
	seenDevices := make(map[string]struct{}, len(targets))
	totalKeys := 0
	for index, target := range targets {
		if !validUUIDv7(target.DeviceID) {
			return nil, errors.New("telemetry target device must be a UUIDv7")
		}
		if _, duplicate := seenDevices[target.DeviceID]; duplicate {
			return nil, errors.New("telemetry target device is duplicated")
		}
		seenDevices[target.DeviceID] = struct{}{}
		if len(target.Keys) > MaximumKeysPerTarget {
			return nil, errors.New("telemetry target key count is invalid")
		}
		keys := append([]string(nil), target.Keys...)
		seenKeys := make(map[string]struct{}, len(keys))
		for _, key := range keys {
			if !telemetryKeyPattern.MatchString(key) {
				return nil, errors.New("telemetry key syntax is invalid")
			}
			if _, duplicate := seenKeys[key]; duplicate {
				return nil, errors.New("telemetry key is duplicated")
			}
			seenKeys[key] = struct{}{}
		}
		totalKeys += len(keys)
		if totalKeys > MaximumTotalKeys {
			return nil, errors.New("telemetry total key count is invalid")
		}
		slices.Sort(keys)
		canonical[index] = Target{DeviceID: target.DeviceID, Keys: keys}
	}
	slices.SortFunc(canonical, func(left, right Target) int { return strings.Compare(left.DeviceID, right.DeviceID) })
	return canonical, nil
}

func ScopeDigest(action Action, actingOrganizationID string, targets []Target) (string, error) {
	if !action.Valid() || !validUUIDv7(actingOrganizationID) {
		return "", errors.New("telemetry scope context is invalid")
	}
	canonical, err := CanonicalTargets(targets)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(struct {
		Action               Action   `json:"action"`
		ActingOrganizationID string   `json:"actingOrganizationId"`
		Targets              []Target `json:"targets"`
	}{Action: action, ActingOrganizationID: actingOrganizationID, Targets: canonical})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func validUUIDv7(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded, err := hex.DecodeString(compact)
	return err == nil && len(decoded) == 16 && decoded[6]>>4 == 7 && decoded[8]>>6 == 2
}
