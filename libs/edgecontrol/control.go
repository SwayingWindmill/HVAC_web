package edgecontrol

import (
	"errors"
	"fmt"
	"sort"
)

var ErrConstraintConflict = errors.New("numeric constraints have no feasible interval")

type ConstraintEvidence struct {
	ControllerID string
	Reason       string
}

type Decision struct {
	Address           string
	ControllerID      string
	Requested         Value
	Effective         *Value
	Accepted          bool
	ConstraintReasons []ConstraintEvidence
}

type numericBounds struct {
	min      *float64
	max      *float64
	evidence []ConstraintEvidence
}

type targetState struct {
	denied   []ConstraintEvidence
	bounds   numericBounds
	decision *Decision
}

type ControlPlan struct {
	image   ProcessImage
	targets map[string]*targetState
}

func NewControlPlan(image ProcessImage) *ControlPlan {
	return &ControlPlan{image: image, targets: map[string]*targetState{}}
}

func (plan *ControlPlan) target(address string) (*targetState, ChannelDescriptor, error) {
	if plan == nil {
		return nil, ChannelDescriptor{}, errors.New("control plan is nil")
	}
	snapshot, ok := plan.image.Get(address)
	if !ok {
		return nil, ChannelDescriptor{}, fmt.Errorf("channel %s is not in the process image", address)
	}
	state := plan.targets[address]
	if state == nil {
		state = &targetState{}
		plan.targets[address] = state
	}
	return state, snapshot.Descriptor, nil
}

func writable(descriptor ChannelDescriptor) bool {
	return descriptor.Access == AccessReadWrite || descriptor.Access == AccessWriteOnly
}

// Deny blocks any lower-priority request for a writable channel. Once a higher-priority
// controller has already produced a decision, lower-priority controllers cannot change it.
func (plan *ControlPlan) Deny(address, controllerID, reason string) (bool, error) {
	state, descriptor, err := plan.target(address)
	if err != nil {
		return false, err
	}
	if !writable(descriptor) {
		return false, fmt.Errorf("channel %s is read-only", address)
	}
	if state.decision != nil {
		return false, nil
	}
	if controllerID == "" || reason == "" {
		return false, errors.New("controller ID and deny reason are required")
	}
	state.denied = append(state.denied, ConstraintEvidence{ControllerID: controllerID, Reason: reason})
	return true, nil
}

// ConstrainNumber narrows the feasible numeric interval for the current cycle. Like the
// pinned OpenEMS constraint lifecycle, a later constraint is still validated even if an earlier
// controller already established a target. A conflicting later constraint is rejected instead of
// silently replacing or ignoring the already feasible higher-priority target.
func (plan *ControlPlan) ConstrainNumber(address, controllerID, reason string, min, max *float64) (bool, error) {
	state, descriptor, err := plan.target(address)
	if err != nil {
		return false, err
	}
	if !writable(descriptor) {
		return false, fmt.Errorf("channel %s is read-only", address)
	}
	if descriptor.DataType != DataTypeDouble {
		return false, fmt.Errorf("channel %s is not numeric", address)
	}
	if controllerID == "" || reason == "" {
		return false, errors.New("controller ID and constraint reason are required")
	}
	if min == nil && max == nil {
		return false, errors.New("at least one numeric bound is required")
	}
	if min != nil && max != nil && *min > *max {
		return false, errors.New("minimum bound exceeds maximum bound")
	}

	nextMin := state.bounds.min
	nextMax := state.bounds.max
	if min != nil && (nextMin == nil || *min > *nextMin) {
		value := *min
		nextMin = &value
	}
	if max != nil && (nextMax == nil || *max < *nextMax) {
		value := *max
		nextMax = &value
	}
	evidence := ConstraintEvidence{ControllerID: controllerID, Reason: reason}
	if nextMin != nil && nextMax != nil && *nextMin > *nextMax {
		return false, ErrConstraintConflict
	}
	if state.decision != nil && state.decision.Accepted && state.decision.Effective != nil {
		effective := state.decision.Effective.Double
		if (nextMin != nil && effective < *nextMin) || (nextMax != nil && effective > *nextMax) {
			// Match OpenEMS addConstraintAndValidate(): a later conflicting constraint
			// is rejected and the previously feasible cycle state remains unchanged.
			return false, ErrConstraintConflict
		}
	}
	state.bounds.min = nextMin
	state.bounds.max = nextMax
	state.bounds.evidence = append(state.bounds.evidence, evidence)
	if state.decision != nil {
		state.decision.ConstraintReasons = append(state.decision.ConstraintReasons, evidence)
	}
	return true, nil
}

// Request submits the current controller's desired value. The first request wins because
// Scheduler runs controllers from highest to lowest priority. Numeric values are clamped to
// constraints established by earlier controllers. A prior deny produces a rejected decision.
func (plan *ControlPlan) Request(address, controllerID string, requested Value) (Decision, bool, error) {
	state, descriptor, err := plan.target(address)
	if err != nil {
		return Decision{}, false, err
	}
	if !writable(descriptor) {
		return Decision{}, false, fmt.Errorf("channel %s is read-only", address)
	}
	if err := requested.validate(descriptor.DataType); err != nil {
		return Decision{}, false, err
	}
	if controllerID == "" {
		return Decision{}, false, errors.New("controller ID is required")
	}
	if state.decision != nil {
		return *state.decision, false, nil
	}

	decision := Decision{Address: address, ControllerID: controllerID, Requested: requested}
	if len(state.denied) > 0 {
		decision.Accepted = false
		decision.ConstraintReasons = append([]ConstraintEvidence(nil), state.denied...)
		state.decision = &decision
		return decision, true, nil
	}

	effective := requested
	if requested.Type == DataTypeDouble {
		if state.bounds.min != nil && effective.Double < *state.bounds.min {
			effective.Double = *state.bounds.min
		}
		if state.bounds.max != nil && effective.Double > *state.bounds.max {
			effective.Double = *state.bounds.max
		}
		decision.ConstraintReasons = append(decision.ConstraintReasons, state.bounds.evidence...)
	}
	decision.Accepted = true
	decision.Effective = &effective
	state.decision = &decision
	return decision, true, nil
}

func (plan *ControlPlan) Decisions() []Decision {
	if plan == nil {
		return nil
	}
	addresses := make([]string, 0, len(plan.targets))
	for address, state := range plan.targets {
		if state.decision != nil {
			addresses = append(addresses, address)
		}
	}
	sort.Strings(addresses)
	out := make([]Decision, 0, len(addresses))
	for _, address := range addresses {
		decision := *plan.targets[address].decision
		decision.ConstraintReasons = append([]ConstraintEvidence(nil), decision.ConstraintReasons...)
		if decision.Effective != nil {
			value := *decision.Effective
			decision.Effective = &value
		}
		out = append(out, decision)
	}
	return out
}
