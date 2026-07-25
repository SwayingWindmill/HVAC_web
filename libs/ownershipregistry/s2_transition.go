package ownershipregistry

import (
	"context"
	"errors"
)

const S2CurrentStateCohortGroup = "s2-current-state-v1"

type S2SessionInvalidation struct {
	CohortGroup              string `json:"cohortGroup"`
	PreviousRegistryRevision int64  `json:"previousRegistryRevision"`
	NextRegistryRevision     int64  `json:"nextRegistryRevision"`
	PreviousRouteRevision    int64  `json:"previousRouteRevision"`
	NextRouteRevision        int64  `json:"nextRouteRevision"`
	PreviousPhase            string `json:"previousPhase"`
	NextPhase                string `json:"nextPhase"`
	PreviousOwner            string `json:"previousOwner"`
	NextOwner                string `json:"nextOwner"`
	DisconnectOrExpire       bool   `json:"disconnectOrExpire"`
	FreshSnapshotRequired    bool   `json:"freshSnapshotRequired"`
	DatabaseAction           string `json:"databaseAction"`
	Rollback                 bool   `json:"rollback"`
}

type S2SessionInvalidator interface {
	InvalidateS2Sessions(context.Context, S2SessionInvalidation) error
}

type S2TransitionResult struct {
	RegistryRevision int64                  `json:"registryRevision"`
	RouteRevision    int64                  `json:"routeRevision"`
	MigrationPhase   string                 `json:"migrationPhase"`
	Invalidation     *S2SessionInvalidation `json:"invalidation,omitempty"`
}

func (manager *Manager) ReloadS2(
	ctx context.Context,
	input []byte,
	meta PolicyChangeContext,
	invalidator S2SessionInvalidator,
) (S2TransitionResult, error) {
	candidate, err := Parse(input)
	if err != nil {
		return S2TransitionResult{}, err
	}
	current := manager.current.Load()
	if err := validateRevisionTransition(current, candidate); err != nil {
		return S2TransitionResult{}, err
	}
	previous, previousOK := current.s2CohortState(S2CurrentStateCohortGroup)
	next, nextOK := candidate.s2CohortState(S2CurrentStateCohortGroup)
	if previousOK != nextOK {
		return S2TransitionResult{}, errors.New("S2 current-state cohort group cannot be added or removed during reload")
	}

	var command *S2SessionInvalidation
	if previousOK && s2TransitionInvalidatesSessions(previous, next) {
		if invalidator == nil {
			return S2TransitionResult{}, errors.New("S2 route transition requires a live-session invalidator")
		}
		previousRank, _ := s2PhaseRank(previous.MigrationPhase)
		nextRank, _ := s2PhaseRank(next.MigrationPhase)
		value := S2SessionInvalidation{
			CohortGroup:              S2CurrentStateCohortGroup,
			PreviousRegistryRevision: current.registry.RegistryRevision,
			NextRegistryRevision:     candidate.registry.RegistryRevision,
			PreviousRouteRevision:    previous.Revision,
			NextRouteRevision:        next.Revision,
			PreviousPhase:            previous.MigrationPhase,
			NextPhase:                next.MigrationPhase,
			PreviousOwner:            previous.Owner,
			NextOwner:                next.Owner,
			DisconnectOrExpire:       true,
			FreshSnapshotRequired:    true,
			DatabaseAction:           "EXPAND_ONLY_NO_DOWN_MIGRATION",
			Rollback:                 nextRank < previousRank,
		}
		if err := invalidator.InvalidateS2Sessions(ctx, value); err != nil {
			return S2TransitionResult{}, errors.New("S2 live-session invalidation failed")
		}
		command = &value
	}
	if err := manager.reload(ctx, input, meta, true); err != nil {
		return S2TransitionResult{}, err
	}
	return S2TransitionResult{
		RegistryRevision: candidate.registry.RegistryRevision,
		RouteRevision:    next.Revision,
		MigrationPhase:   next.MigrationPhase,
		Invalidation:     command,
	}, nil
}

func (snapshot *Snapshot) s2CohortState(group string) (RouteEntry, bool) {
	for _, route := range snapshot.registry.Routes {
		if route.CohortGroup == group {
			return route, true
		}
	}
	return RouteEntry{}, false
}

func s2SnapshotsTransitionInvalidatesSessions(current, candidate *Snapshot) bool {
	previous, previousOK := current.s2CohortState(S2CurrentStateCohortGroup)
	next, nextOK := candidate.s2CohortState(S2CurrentStateCohortGroup)
	return previousOK && nextOK && s2TransitionInvalidatesSessions(previous, next)
}

func s2TransitionInvalidatesSessions(previous, next RouteEntry) bool {
	previousRank, previousOK := s2PhaseRank(previous.MigrationPhase)
	if !previousOK || previousRank < 3 {
		return false
	}
	return previous.Revision != next.Revision || previous.Owner != next.Owner || previous.Rollout != next.Rollout || previous.MigrationPhase != next.MigrationPhase
}
