package commandformal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

type offlineTTLState struct {
	timeStep             uint8 // 0=T0, 1=T0+2s(before backoff), 2=T0+12s(after backoff, before TTL), 3=T0+120s(>=expiresAt)
	cmdStatus            commandmodel.IntentStatus
	deferred             bool
	woken                bool
	deviceFrozen         bool
	badShutdownQueued    bool
	claimedBeforeBackoff bool
	claimedAfterTTL      bool
}

type offlineTTLNode struct {
	state offlineTTLState
	trace []string
}

func offlineTimeAt(base time.Time, step uint8) time.Time {
	switch step {
	case 0:
		return base
	case 1:
		return base.Add(2 * time.Second)
	case 2:
		return base.Add(12 * time.Second)
	default:
		return base.Add(120 * time.Second)
	}
}

// CheckOfflineQueueAndTTLSafety exhaustively model-checks P8 across deliveryMode
// capability restrictions, FAIL_FAST offline rejection, QUEUE_IF_OFFLINE deferral &
// active wake-up without breaking OUTCOME_UNKNOWN freezes, and TTL expiry boundaries.
func CheckOfflineQueueAndTTLSafety(hooks FaultHooks) (SuiteReport, error) {
	now := baseModelTime
	expiresAt := now.Add(120 * time.Second)
	deferUntil := now.Add(10 * time.Second)

	failReport := func(visited map[offlineTTLState]bool, transitions int, cx *Counterexample) SuiteReport {
		return SuiteReport{
			Suite:          "OfflineQueueAndTTLSafety",
			VisitedStates:  len(visited),
			Transitions:    transitions,
			Passed:         false,
			Counterexample: cx,
		}
	}

	assertState := func(st offlineTTLState, trace []string) *Counterexample {
		curTime := offlineTimeAt(now, st.timeStep)
		if st.badShutdownQueued {
			return &Counterexample{
				Property: PropertyP8,
				Suite:    "OfflineQueueAndTTLSafety",
				Summary:  "QUEUE_IF_OFFLINE accepted discrete/shutdown capability instead of rejecting FAIL_FAST-only action",
				Trace:    trace,
			}
		}
		if st.claimedBeforeBackoff {
			return &Counterexample{
				Property: PropertyP8,
				Suite:    "OfflineQueueAndTTLSafety",
				Summary:  "deferred QUEUE_IF_OFFLINE intent claimed before nextDispatchAfter backoff without wake",
				Trace:    trace,
			}
		}
		if st.claimedAfterTTL {
			return &Counterexample{
				Property: PropertyP8,
				Suite:    "OfflineQueueAndTTLSafety",
				Summary:  "expired command claimed at or after expiresAt TTL deadline",
				Trace:    trace,
			}
		}
		if st.cmdStatus == commandmodel.IntentQueued {
			synth := makeSyntheticIntentForCapability(1, commandmodel.CapabilitySetTemperatureSetpoint, commandmodel.IntentQueued, true, now)
			synth.DeliveryMode = commandmodel.DeliveryModeQueueIfOffline
			ttl := 120
			synth.TTLSeconds = &ttl
			synth.ExpiresAt = &expiresAt
			if st.deferred && !st.woken {
				synth.NextDispatchAfter = &deferUntil
			}
			claimable := commandservice.CanClaimQueuedIntentWithPriority(synth, st.deviceFrozen, nil, nil, curTime)
			if hooks.BypassTTLExpiryOnClaim && !curTime.Before(expiresAt) && !st.deviceFrozen {
				claimable = true
			}
			if hooks.BypassDeferralBackoffOnClaim && st.deferred && !st.woken && curTime.Before(deferUntil) && !st.deviceFrozen {
				claimable = true
			}
			if !curTime.Before(expiresAt) && claimable {
				return &Counterexample{
					Property: PropertyP8,
					Suite:    "OfflineQueueAndTTLSafety",
					Summary:  fmt.Sprintf("queued intent remained claimable at t=%s (>= expiresAt=%s)", curTime.Format(time.RFC3339), expiresAt.Format(time.RFC3339)),
					Trace:    trace,
				}
			}
			if st.deferred && !st.woken && curTime.Before(deferUntil) && claimable {
				return &Counterexample{
					Property: PropertyP8,
					Suite:    "OfflineQueueAndTTLSafety",
					Summary:  fmt.Sprintf("deferred intent claimable at t=%s (< deferUntil=%s) without wake", curTime.Format(time.RFC3339), deferUntil.Format(time.RFC3339)),
					Trace:    trace,
				}
			}
			if st.deviceFrozen && claimable {
				return &Counterexample{
					Property: PropertyP8,
					Suite:    "OfflineQueueAndTTLSafety",
					Summary:  "woken QUEUE_IF_OFFLINE intent bypassed active OUTCOME_UNKNOWN device freeze",
					Trace:    trace,
				}
			}
		}
		return nil
	}

	initial := offlineTTLState{}
	queue := []offlineTTLNode{{state: initial}}
	visited := map[offlineTTLState]bool{initial: true}
	transitions := 0

	enqueue := func(next offlineTTLState, action string, parentTrace []string) (*Counterexample, error) {
		transitions++
		trace := make([]string, len(parentTrace)+1)
		copy(trace, parentTrace)
		trace[len(parentTrace)] = action
		if cx := assertState(next, trace); cx != nil {
			return cx, nil
		}
		if !visited[next] {
			visited[next] = true
			queue = append(queue, offlineTTLNode{state: next, trace: trace})
		}
		return nil, nil
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		st := cur.state
		curTime := offlineTimeAt(now, st.timeStep)

		// 1. Attempt submitting discrete/shutdown command with QUEUE_IF_OFFLINE.
		ttl120 := 120
		_, _, _, shutdownErr := commandmodel.ValidateDeliveryAndTTL(commandmodel.CapabilityStop, commandmodel.DeliveryModeQueueIfOffline, &ttl120, nil, now)
		if shutdownErr == nil || hooks.AllowShutdownOfflineQueue {
			next := st
			next.badShutdownQueued = true
			if cx, err := enqueue(next, "Submit(STOP, QUEUE_IF_OFFLINE)", cur.trace); err != nil {
				return SuiteReport{}, err
			} else if cx != nil {
				return failReport(visited, transitions, cx), nil
			}
		}

		// 2. Submit setpoint command with QUEUE_IF_OFFLINE while target is OFFLINE.
		if st.cmdStatus == "" && curTime.Before(expiresAt) {
			next := st
			next.cmdStatus = commandmodel.IntentQueued
			if cx, err := enqueue(next, "Submit(SETPOINT, QUEUE_IF_OFFLINE)", cur.trace); err != nil {
				return SuiteReport{}, err
			} else if cx != nil {
				return failReport(visited, transitions, cx), nil
			}
		}

		// 3. Inject / clear OUTCOME_UNKNOWN freeze to prove wake never overrides freeze.
		if !st.deviceFrozen {
			next := st
			next.deviceFrozen = true
			if cx, err := enqueue(next, "Freeze(OUTCOME_UNKNOWN)", cur.trace); err != nil {
				return SuiteReport{}, err
			} else if cx != nil {
				return failReport(visited, transitions, cx), nil
			}
		} else {
			next := st
			next.deviceFrozen = false
			if cx, err := enqueue(next, "ReconcileUnfreeze()", cur.trace); err != nil {
				return SuiteReport{}, err
			} else if cx != nil {
				return failReport(visited, transitions, cx), nil
			}
		}

		// 4. Advance clock through backoff & TTL boundaries.
		if st.timeStep < 3 {
			next := st
			next.timeStep++
			if cx, err := enqueue(next, fmt.Sprintf("AdvanceTime(step=%d)", next.timeStep), cur.trace); err != nil {
				return SuiteReport{}, err
			} else if cx != nil {
				return failReport(visited, transitions, cx), nil
			}
		}

		// 5. Claim queued command (guarded by TTL, backoff, and freeze).
		if st.cmdStatus == commandmodel.IntentQueued {
			synth := makeSyntheticIntentForCapability(1, commandmodel.CapabilitySetTemperatureSetpoint, commandmodel.IntentQueued, true, now)
			synth.DeliveryMode = commandmodel.DeliveryModeQueueIfOffline
			synth.TTLSeconds = &ttl120
			synth.ExpiresAt = &expiresAt
			if st.deferred && !st.woken {
				synth.NextDispatchAfter = &deferUntil
			}
			canClaim := commandservice.CanClaimQueuedIntentWithPriority(synth, st.deviceFrozen, nil, nil, curTime)
			if hooks.BypassDeferralBackoffOnClaim && st.deferred && !st.woken && curTime.Before(deferUntil) && !st.deviceFrozen {
				next := st
				next.claimedBeforeBackoff = true
				if cx, err := enqueue(next, "ClaimBeforeBackoff()", cur.trace); err != nil {
					return SuiteReport{}, err
				} else if cx != nil {
					return failReport(visited, transitions, cx), nil
				}
			}
			if hooks.BypassTTLExpiryOnClaim && !curTime.Before(expiresAt) && !st.deviceFrozen {
				next := st
				next.claimedAfterTTL = true
				if cx, err := enqueue(next, "ClaimAfterTTL()", cur.trace); err != nil {
					return SuiteReport{}, err
				} else if cx != nil {
					return failReport(visited, transitions, cx), nil
				}
			}
			if canClaim {
				next := st
				next.cmdStatus = commandmodel.IntentDispatching
				if cx, err := enqueue(next, "ClaimDispatch()", cur.trace); err != nil {
					return SuiteReport{}, err
				} else if cx != nil {
					return failReport(visited, transitions, cx), nil
				}
			}
			if !curTime.Before(expiresAt) {
				next := st
				next.cmdStatus = commandmodel.IntentExpired
				if cx, err := enqueue(next, "ExpireOnTTL()", cur.trace); err != nil {
					return SuiteReport{}, err
				} else if cx != nil {
					return failReport(visited, transitions, cx), nil
				}
			}
			if st.deferred && !st.woken && curTime.Before(expiresAt) {
				next := st
				next.woken = true
				if cx, err := enqueue(next, "WakeDeferredDispatches()", cur.trace); err != nil {
					return SuiteReport{}, err
				} else if cx != nil {
					return failReport(visited, transitions, cx), nil
				}
			}
		}

		// 6. Resolve DISPATCHING -> deferred offline retry OR terminal pre-send TTL expiration / success.
		if st.cmdStatus == commandmodel.IntentDispatching {
			synth := makeSyntheticIntentForCapability(1, commandmodel.CapabilitySetTemperatureSetpoint, commandmodel.IntentDispatching, true, now)
			synth.DeliveryMode = commandmodel.DeliveryModeQueueIfOffline
			synth.TTLSeconds = &ttl120
			synth.ExpiresAt = &expiresAt
			deferDec, err := commandservice.DecideDispatchResolutionForIntent(synth, commandmodel.ConnectorResult{
				Phase:       commandmodel.ConnectorPreSendRejected,
				FailureCode: "DISPATCH_SAFETY_DEVICE_SLEEPING",
			}, 1, curTime)
			if err != nil {
				return SuiteReport{}, err
			}
			if !curTime.Before(expiresAt) {
				if deferDec.IntentFinal != commandmodel.IntentExpired {
					return failReport(visited, transitions, &Counterexample{
						Property: PropertyP8,
						Suite:    "OfflineQueueAndTTLSafety",
						Summary:  fmt.Sprintf("post-TTL offline pre-send returned %s instead of EXPIRED", deferDec.IntentFinal),
						Trace:    append(append([]string(nil), cur.trace...), "Resolve(SLEEPING@postTTL)"),
					}), nil
				}
				next := st
				next.cmdStatus = commandmodel.IntentExpired
				if cx, err := enqueue(next, "ResolveExpired(SLEEPING@postTTL)", cur.trace); err != nil {
					return SuiteReport{}, err
				} else if cx != nil {
					return failReport(visited, transitions, cx), nil
				}
			} else {
				if deferDec.IntentFinal != commandmodel.IntentQueued || deferDec.NextDispatchAfter == nil || !deferDec.NextDispatchAfter.After(curTime) {
					return failReport(visited, transitions, &Counterexample{
						Property: PropertyP8,
						Suite:    "OfflineQueueAndTTLSafety",
						Summary:  fmt.Sprintf("expected QUEUE_IF_OFFLINE deferral with future NextDispatchAfter, got %+v", deferDec),
						Trace:    append(append([]string(nil), cur.trace...), "Resolve(SLEEPING)"),
					}), nil
				}
				next := st
				next.cmdStatus = commandmodel.IntentQueued
				next.deferred = true
				next.woken = false
				if cx, err := enqueue(next, "ResolveDefer(SLEEPING)", cur.trace); err != nil {
					return SuiteReport{}, err
				} else if cx != nil {
					return failReport(visited, transitions, cx), nil
				}
			}
			if curTime.Before(expiresAt) {
				next := st
				next.cmdStatus = commandmodel.IntentSucceeded
				if cx, err := enqueue(next, "ResolveAckAndVerify()", cur.trace); err != nil {
					return SuiteReport{}, err
				} else if cx != nil {
					return failReport(visited, transitions, cx), nil
				}
			}
		}
	}

	if cx, err := verifyRealServiceOfflineQueueAndTTL(now, hooks); err != nil {
		return SuiteReport{}, err
	} else if cx != nil {
		return failReport(visited, transitions, cx), nil
	}

	return SuiteReport{
		Suite:         "OfflineQueueAndTTLSafety",
		VisitedStates: len(visited),
		Transitions:   transitions,
		Passed:        true,
	}, nil
}

func verifyRealServiceOfflineQueueAndTTL(baseNow time.Time, hooks FaultHooks) (*Counterexample, error) {
	curNow := baseNow
	svc := commandservice.New(func() time.Time { return curNow })
	ttl := 120
	expiresAt := baseNow.Add(time.Duration(ttl) * time.Second)

	// 1. ControlGate rejects FAIL_FAST on OFFLINE/SLEEPING target, admits QUEUE_IF_OFFLINE on setpoint with snapshotRevision=0,
	// and rejects QUEUE_IF_OFFLINE on STOP/shutdown capabilities.
	offlineObs := &commandmodel.LivePointObservation{
		EvaluationAvailability: "AVAILABLE",
		Presence:               "SLEEPING",
		Readiness:              "CURRENT",
		Freshness:              "STALE",
		Quality:                "GOOD",
		BusinessRevision:       0,
		ObservedAt:             baseNow,
	}
	_, failFastRej := commandmodel.EvaluateControlGate(commandmodel.GateInput{
		Phase:              commandmodel.GatePhaseSubmit,
		AssetID:            "asset-1",
		DeviceID:           baseDeviceID,
		Capability:         commandmodel.CapabilitySetTemperatureSetpoint,
		CapabilityRevision: setpointCapabilityRev,
		ParameterKey:       commandmodel.ParameterSetpointC,
		Parameters:         commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 23.0},
		SiteMode:           commandmodel.SiteControlModeRemoteAuto,
		IdempotencyKey:     "gate-failfast-offline",
		PriorityTier:       commandmodel.PriorityManualOverride,
		CommandSource:      commandmodel.SourceOperator,
		DeliveryMode:       commandmodel.DeliveryModeFailFast,
		PreconditionMode:   commandmodel.PreconditionRequireCurrentState,
		TargetState:        offlineObs,
		Now:                baseNow,
	})
	if failFastRej == nil || failFastRej.Code != "COMMAND_CURRENT_STATE_UNSAFE" {
		return &Counterexample{
			Property: PropertyP8,
			Suite:    "OfflineQueueAndTTLSafety",
			Summary:  fmt.Sprintf("FAIL_FAST submit on SLEEPING target returned %+v, want COMMAND_CURRENT_STATE_UNSAFE", failFastRej),
		}, nil
	}
	_, preDispatchRej := commandmodel.EvaluateControlGate(commandmodel.GateInput{
		Phase:              commandmodel.GatePhasePreDispatch,
		AssetID:            "asset-1",
		DeviceID:           baseDeviceID,
		Capability:         commandmodel.CapabilitySetTemperatureSetpoint,
		CapabilityRevision: setpointCapabilityRev,
		ParameterKey:       commandmodel.ParameterSetpointC,
		Parameters:         commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 23.0},
		SiteMode:           commandmodel.SiteControlModeRemoteAuto,
		IdempotencyKey:     "gate-failfast-pre-dispatch",
		PriorityTier:       commandmodel.PriorityManualOverride,
		CommandSource:      commandmodel.SourceOperator,
		DeliveryMode:       commandmodel.DeliveryModeFailFast,
		PreconditionMode:   commandmodel.PreconditionRequireCurrentState,
		TargetState:        offlineObs,
		Now:                baseNow,
	})
	if preDispatchRej == nil || preDispatchRej.Code != "DISPATCH_SAFETY_DEVICE_SLEEPING" {
		return &Counterexample{
			Property: PropertyP8,
			Suite:    "OfflineQueueAndTTLSafety",
			Summary:  fmt.Sprintf("FAIL_FAST pre-dispatch on SLEEPING target returned %+v, want DISPATCH_SAFETY_DEVICE_SLEEPING", preDispatchRej),
		}, nil
	}

	_, queueRej := commandmodel.EvaluateControlGate(commandmodel.GateInput{
		Phase:              commandmodel.GatePhaseSubmit,
		AssetID:            "asset-1",
		DeviceID:           baseDeviceID,
		Capability:         commandmodel.CapabilitySetTemperatureSetpoint,
		CapabilityRevision: setpointCapabilityRev,
		ParameterKey:       commandmodel.ParameterSetpointC,
		Parameters:         commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 23.0},
		SiteMode:           commandmodel.SiteControlModeRemoteAuto,
		IdempotencyKey:     "gate-queue-offline",
		PriorityTier:       commandmodel.PriorityManualOverride,
		CommandSource:      commandmodel.SourceOperator,
		DeliveryMode:       commandmodel.DeliveryModeQueueIfOffline,
		TTLSeconds:         &ttl,
		ExpiresAt:          &expiresAt,
		PreconditionMode:   commandmodel.PreconditionRequireCurrentState,
		TargetState:        offlineObs,
		Now:                baseNow,
	})
	if queueRej != nil {
		return &Counterexample{
			Property: PropertyP8,
			Suite:    "OfflineQueueAndTTLSafety",
			Summary:  fmt.Sprintf("QUEUE_IF_OFFLINE rejected sleeping setpoint unexpectedly: %+v", queueRej),
		}, nil
	}

	// 2. Service.Submit rejects STOP + QUEUE_IF_OFFLINE and accepts offline setpoint with DeliveryModeQueueIfOffline.
	badReq := makeSubmitRequestForCapability("formal-stop-offline", commandmodel.CapabilityStop, baseNow)
	badReq.DeliveryMode = commandmodel.DeliveryModeQueueIfOffline
	badReq.TTLSeconds = &ttl
	badReq.ExpiresAt = &expiresAt
	if _, err := svc.Submit(badReq); err == nil && !hooks.AllowShutdownOfflineQueue {
		return &Counterexample{
			Property: PropertyP8,
			Suite:    "OfflineQueueAndTTLSafety",
			Summary:  "Service.Submit allowed STOP capability with QUEUE_IF_OFFLINE",
		}, nil
	}

	req := makeSubmitRequest("formal-offline-wake", 22.0, baseNow)
	req.DeliveryMode = commandmodel.DeliveryModeQueueIfOffline
	req.TTLSeconds = &ttl
	req.ExpiresAt = &expiresAt
	req.CurrentState = commandmodel.CurrentStateEvidence{
		EvaluationAvailability: "AVAILABLE",
		Presence:               "OFFLINE",
		Readiness:              "CURRENT",
		Quality:                "GOOD",
		BusinessRevision:       0,
		ObservedAt:             baseNow,
	}
	sub, err := svc.Submit(req)
	if err != nil {
		return nil, fmt.Errorf("submit offline queue intent: %w", err)
	}
	env1, err := svc.PrepareDispatch(sub.Intent.ID, "disp-formal-1", curNow.Add(dispatchLeaseWindow))
	if err != nil {
		return nil, fmt.Errorf("initial PrepareDispatch: %w", err)
	}
	if err := svc.ResolveDispatch(env1, commandmodel.ConnectorResult{
		Phase:       commandmodel.ConnectorPreSendRejected,
		FailureCode: "MQTT_GATEWAY_DISCONNECTED",
	}); err != nil {
		return nil, fmt.Errorf("ResolveDispatch offline defer: %w", err)
	}
	if _, err := svc.PrepareDispatch(sub.Intent.ID, "disp-formal-1", curNow.Add(dispatchLeaseWindow)); !errors.Is(err, commandservice.ErrCommandNotDispatchable) {
		return &Counterexample{
			Property: PropertyP8,
			Suite:    "OfflineQueueAndTTLSafety",
			Summary:  fmt.Sprintf("deferred command claim returned %v, want ErrCommandNotDispatchable", err),
		}, nil
	}
	woken, err := svc.WakeDeferredDispatches(context.Background(), baseTenantID, []string{baseDeviceID})
	if err != nil || woken != 1 {
		return &Counterexample{
			Property: PropertyP8,
			Suite:    "OfflineQueueAndTTLSafety",
			Summary:  fmt.Sprintf("WakeDeferredDispatches woken=%d err=%v, want woken=1 err=nil", woken, err),
		}, nil
	}

	// 3. Advancing past ExpiresAt expires QUEUED command via PrepareDispatch without allowing dispatch.
	curNow = expiresAt
	if _, err := svc.PrepareDispatch(sub.Intent.ID, "disp-formal-1", curNow.Add(dispatchLeaseWindow)); !errors.Is(err, commandservice.ErrCommandTTLExpired) {
		return &Counterexample{
			Property: PropertyP8,
			Suite:    "OfflineQueueAndTTLSafety",
			Summary:  fmt.Sprintf("post-TTL PrepareDispatch returned %v, want ErrCommandTTLExpired", err),
		}, nil
	}
	afterExpiry, err := svc.Get(sub.Intent.ID)
	if err != nil {
		return nil, fmt.Errorf("reload expired intent: %w", err)
	}
	if afterExpiry.Status != commandmodel.IntentExpired {
		return &Counterexample{
			Property: PropertyP8,
			Suite:    "OfflineQueueAndTTLSafety",
			Summary:  fmt.Sprintf("expired intent status=%s, want EXPIRED", afterExpiry.Status),
		}, nil
	}
	return nil, nil
}
