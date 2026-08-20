package ruleruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const defaultLeaseTTL = 30 * time.Second

type Runtime struct {
	plan           ExecutionPlan
	store          ExecutionStore
	snapshotReader SnapshotReader
	effectSink     EffectSink
	mode           ExecutionMode
	leaseTTL       time.Duration
}

type pendingStateCAS struct {
	Key        RuleStateKey
	Transition StateTransition
}

type EffectDeliveryError struct {
	Class FailureClass
	Code  string
	Err   error
}

func (e *EffectDeliveryError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

func NewRuntime(plan ExecutionPlan, store ExecutionStore, snapshots SnapshotReader, effects EffectSink, mode ExecutionMode) (*Runtime, error) {
	if store == nil {
		return nil, errors.New("rule execution store is required")
	}
	if plan.Revision.State != RevisionReleased {
		return nil, errors.New("runtime requires a released rule revision")
	}
	if plan.Digest == "" || plan.Revision.Digest != plan.Digest {
		return nil, errors.New("runtime requires a compiled immutable rule digest")
	}
	if mode != ModeLive && mode != ModeReplay {
		return nil, errors.New("rule execution mode must be LIVE or REPLAY")
	}
	if mode == ModeReplay {
		if effects != nil {
			return nil, errors.New("replay runtime cannot accept a live effect sink")
		}
		if snapshots != nil {
			frozen, ok := snapshots.(FrozenSnapshotReader)
			if !ok || frozen.FrozenFactsRevision() == "" {
				return nil, errors.New("replay snapshot reader must expose a frozen facts revision")
			}
		}
	}
	return &Runtime{plan: plan, store: store, snapshotReader: snapshots, effectSink: effects, mode: mode, leaseTTL: defaultLeaseTTL}, nil
}

func (r *Runtime) Start(ctx context.Context, binding RuleBinding, event RuleEventEnvelope, now time.Time) (ExecutionSnapshot, bool, error) {
	if binding.RuleRevisionID != r.plan.Revision.ID || binding.TenantID != r.plan.Revision.TenantID || event.TenantID != binding.TenantID {
		return ExecutionSnapshot{}, false, errors.New("binding, rule revision and event tenant must match exactly")
	}
	if binding.Revision <= 0 {
		return ExecutionSnapshot{}, false, errors.New("binding revision must be positive")
	}
	if event.EventID == "" || event.SubjectType == "" || event.SubjectID == "" || event.Schema == "" {
		return ExecutionSnapshot{}, false, errors.New("event identity, schema and subject are required")
	}
	actualPayloadDigest := PayloadDigest(event.Payload)
	if event.PayloadDigest == "" {
		event.PayloadDigest = actualPayloadDigest
	} else if event.PayloadDigest != actualPayloadDigest {
		return ExecutionSnapshot{}, false, errors.New("event payload digest mismatch")
	}
	executionID := ExecutionID(r.plan.Revision.ID, binding.Revision, event.EventID)
	envelopePayload, err := json.Marshal(event)
	if err != nil {
		return ExecutionSnapshot{}, false, fmt.Errorf("marshal rule event envelope: %w", err)
	}
	entryInput := NodeInput{Port: "in", Value: TypedValue{Type: PortEvent, Data: envelopePayload}, Path: "entry"}
	entryWorkID := WorkItemID(executionID, entryInput.Path, r.plan.Revision.EntryNodeID, PayloadDigest(entryInput.Value.Data))
	orderingKey := event.TenantID + "/" + event.SiteID + "/" + event.SubjectType + "/" + event.SubjectID
	seed := ExecutionSnapshot{
		Execution: ExecutionRecord{
			ExecutionID: executionID, TenantID: event.TenantID, SiteID: event.SiteID,
			RuleRevisionID: r.plan.Revision.ID, BindingID: binding.ID, BindingRevision: binding.Revision,
			EventID: event.EventID, OrderingKey: orderingKey, Status: ExecutionReady,
			AttemptBudget: r.plan.Revision.MaxAttempts, CreatedAt: now, UpdatedAt: now,
		},
		Event: event, RuleDigest: r.plan.Digest,
		Work: []WorkRecord{{WorkItemID: entryWorkID, ExecutionID: executionID, NodeID: r.plan.Revision.EntryNodeID, Input: entryInput, Status: "READY"}},
	}
	return r.store.CreateOrLoad(ctx, seed)
}

func (r *Runtime) Run(ctx context.Context, executionID, workerID string, now time.Time) (ExecutionSnapshot, error) {
	snapshot, err := r.store.Claim(ctx, executionID, workerID, now, r.leaseTTL)
	if err != nil {
		return ExecutionSnapshot{}, err
	}
	if terminal(snapshot.Execution.Status) {
		return snapshot, nil
	}
	fence := snapshot.Execution.LeaseFence

	if err := r.resumeDueContinuations(&snapshot, now); err != nil {
		return ExecutionSnapshot{}, err
	}
	if err := r.store.Save(ctx, snapshot, workerID, fence, now); err != nil {
		return ExecutionSnapshot{}, err
	}

	for {
		index := nextRunnableWork(snapshot, now)
		if index < 0 {
			break
		}
		beforeWork := cloneSnapshot(snapshot)
		stateCAS, err := r.processWork(ctx, &snapshot, index, now)
		if err != nil {
			return ExecutionSnapshot{}, err
		}
		if stateCAS != nil {
			if _, err := r.store.SaveWithStateCAS(ctx, snapshot, workerID, fence, now, stateCAS.Key, stateCAS.Transition); err != nil {
				if !errors.Is(err, ErrStateCASConflict) {
					return ExecutionSnapshot{}, err
				}
				snapshot = beforeWork
				work := &snapshot.Work[index]
				work.Attempt++
				outcome := NodeOutcome{Failure: FailureTransient, FailureCode: "STATE_CAS_CONFLICT"}
				snapshot.Trace = append(snapshot.Trace, TraceRecord{ExecutionID: snapshot.Execution.ExecutionID, WorkItemID: work.WorkItemID, NodeID: work.NodeID, Attempt: work.Attempt, Outcome: outcome})
				r.applyFailure(&snapshot, work, outcome.Failure, outcome.FailureCode, now)
				if err := r.store.Save(ctx, snapshot, workerID, fence, now); err != nil {
					return ExecutionSnapshot{}, err
				}
			}
		} else if err := r.store.Save(ctx, snapshot, workerID, fence, now); err != nil {
			return ExecutionSnapshot{}, err
		}
		if terminal(snapshot.Execution.Status) || snapshot.Execution.Status == ExecutionBlocked || snapshot.Execution.Status == ExecutionWaiting {
			break
		}
	}
	r.recalculateStatus(&snapshot, now)
	if err := r.store.Save(ctx, snapshot, workerID, fence, now); err != nil {
		return ExecutionSnapshot{}, err
	}
	if err := r.store.Release(ctx, executionID, workerID, fence, now); err != nil {
		return ExecutionSnapshot{}, err
	}
	return r.store.Load(ctx, executionID)
}

func (r *Runtime) DispatchEffects(ctx context.Context, executionID, workerID string, now time.Time) (ExecutionSnapshot, error) {
	if r.mode == ModeReplay {
		return ExecutionSnapshot{}, errors.New("replay runtime never dispatches effects")
	}
	if r.effectSink == nil {
		return ExecutionSnapshot{}, errors.New("live effect sink is required")
	}
	snapshot, err := r.store.Claim(ctx, executionID, workerID, now, r.leaseTTL)
	if err != nil {
		return ExecutionSnapshot{}, err
	}
	if terminal(snapshot.Execution.Status) {
		return snapshot, nil
	}
	fence := snapshot.Execution.LeaseFence
	for index := range snapshot.Effects {
		effect := &snapshot.Effects[index]
		if effect.Status == "DISPATCHING" {
			effect.Status = "AMBIGUOUS"
			effect.Failure = FailureAmbiguous
			effect.FailureCode = "EFFECT_OUTCOME_UNKNOWN_AFTER_RESTART"
			snapshot.Execution.Status = ExecutionBlocked
			snapshot.Execution.TerminalCode = "AMBIGUOUS_EFFECT"
			if err := r.store.Save(ctx, snapshot, workerID, fence, now); err != nil {
				return ExecutionSnapshot{}, err
			}
			break
		}
		if effect.Status != "PENDING" || effect.RetryAt.After(now) {
			continue
		}
		effect.Attempts++
		effect.Status = "DISPATCHING"
		effect.RetryAt = time.Time{}
		effect.Failure = FailureNone
		effect.FailureCode = ""
		if err := r.store.Save(ctx, snapshot, workerID, fence, now); err != nil {
			return ExecutionSnapshot{}, err
		}

		receipt, deliverErr := r.effectSink.Deliver(ctx, effect.Effect)
		if deliverErr != nil {
			class, code := classifyDeliveryError(deliverErr)
			effect.Failure = class
			effect.FailureCode = code
			switch class {
			case FailureTransient:
				if effect.Attempts >= snapshot.Execution.AttemptBudget {
					effect.Status = "DEAD"
					snapshot.Execution.Status = ExecutionDead
					snapshot.Execution.TerminalCode = "EFFECT_ATTEMPTS_EXHAUSTED"
				} else {
					effect.Status = "PENDING"
					effect.RetryAt = now.Add(retryDelay(effect.Attempts))
					snapshot.Execution.Status = ExecutionBlocked
				}
			case FailurePolicy, FailureSafetyDenied, FailureValidation:
				effect.Status = "REJECTED"
				snapshot.Execution.Status = ExecutionFailed
				snapshot.Execution.TerminalCode = code
			default:
				effect.Status = "AMBIGUOUS"
				snapshot.Execution.Status = ExecutionBlocked
				snapshot.Execution.TerminalCode = "AMBIGUOUS_EFFECT"
			}
			if err := r.store.Save(ctx, snapshot, workerID, fence, now); err != nil {
				return ExecutionSnapshot{}, err
			}
			break
		}
		effect.Status = "DELIVERED"
		effect.Receipt = receipt
		if allWorkEffectsDelivered(snapshot, effect.WorkItemID) {
			workIndex := findWork(snapshot, effect.WorkItemID)
			if workIndex >= 0 && snapshot.Work[workIndex].Status == "BLOCKED_EFFECT" {
				snapshot.Work[workIndex].Status = "SUCCEEDED"
				for _, output := range effect.Outputs {
					r.routeOutput(&snapshot, snapshot.Work[workIndex], output)
				}
			}
		}
		if err := r.store.Save(ctx, snapshot, workerID, fence, now); err != nil {
			return ExecutionSnapshot{}, err
		}
	}
	r.recalculateStatus(&snapshot, now)
	if err := r.store.Save(ctx, snapshot, workerID, fence, now); err != nil {
		return ExecutionSnapshot{}, err
	}
	if err := r.store.Release(ctx, executionID, workerID, fence, now); err != nil {
		return ExecutionSnapshot{}, err
	}
	return r.store.Load(ctx, executionID)
}

func (r *Runtime) processWork(ctx context.Context, snapshot *ExecutionSnapshot, index int, now time.Time) (*pendingStateCAS, error) {
	work := &snapshot.Work[index]
	node, exists := r.plan.Nodes[work.NodeID]
	if !exists {
		return nil, fmt.Errorf("execution references node %q outside pinned plan", work.NodeID)
	}
	definition := r.plan.Catalog.Definitions[node.DefinitionID]
	if work.Input.Type() != definition.Inputs[work.Input.Port] {
		return nil, fmt.Errorf("persisted input type does not match pinned node port")
	}
	work.Attempt++
	work.Status = "RUNNING"
	context := NodeContext{
		Mode: r.mode, TenantID: snapshot.Event.TenantID, SiteID: snapshot.Event.SiteID,
		SubjectType: snapshot.Event.SubjectType, SubjectID: snapshot.Event.SubjectID,
		ExecutionID: snapshot.Execution.ExecutionID, RuleRevisionID: snapshot.Execution.RuleRevisionID, NodeInstanceID: work.NodeID,
		BindingRevision: snapshot.Execution.BindingRevision, WorkItemID: work.WorkItemID,
		OccurredAt: snapshot.Event.OccurredAt, SnapshotReader: r.snapshotReader, StateReader: r.store,
	}
	outcome, err := definition.Evaluate(ctx, context, node.Config, work.Input)
	if err != nil {
		outcome = NodeOutcome{Failure: FailureTransient, FailureCode: "NODE_EVALUATION_ERROR"}
	} else if err := validateNodeOutcome(definition, outcome); err != nil {
		outcome = NodeOutcome{Failure: FailureValidation, FailureCode: "NODE_OUTCOME_INVALID"}
	}
	var stateCAS *pendingStateCAS
	if outcome.State != nil {
		key := RuleStateKey{
			TenantID: snapshot.Execution.TenantID, RuleRevisionID: snapshot.Execution.RuleRevisionID,
			NodeInstanceID: work.NodeID, ScopeKey: outcome.State.ScopeKey,
		}
		stateCAS = &pendingStateCAS{Key: key, Transition: *outcome.State}
		snapshot.StateTransitions = append(snapshot.StateTransitions, AppliedStateTransition{
			RuleStateKey: key, SchemaVersion: outcome.State.SchemaVersion, ExpectedRevision: outcome.State.ExpectedRevision,
			ResultRevision: outcome.State.ExpectedRevision + 1, ValueDigest: PayloadDigest(outcome.State.Value), ExpiresAt: outcome.State.ExpiresAt,
		})
	}
	for ordinal := range outcome.Effects {
		effect := &outcome.Effects[ordinal]
		effect.PayloadDigest = PayloadDigest(effect.Payload)
		effect.EffectID = EffectID(work.WorkItemID, effect.OutputPort, ordinal, effect.PayloadDigest)
	}
	if outcome.Continuation != nil {
		continuation := outcome.Continuation
		continuation.ContinuationID = ContinuationID(work.WorkItemID, continuation.WakeAt.UnixNano(), continuation.OutputPort, PayloadDigest(continuation.Payload))
	}
	snapshot.Trace = append(snapshot.Trace, TraceRecord{ExecutionID: snapshot.Execution.ExecutionID, WorkItemID: work.WorkItemID, NodeID: work.NodeID, Attempt: work.Attempt, Outcome: outcome})

	if outcome.Failure != FailureNone {
		r.applyFailure(snapshot, work, outcome.Failure, outcome.FailureCode, now)
		return nil, nil
	}
	if outcome.Continuation != nil {
		outputType, ok := definition.Outputs[outcome.Continuation.OutputPort]
		if !ok {
			return nil, fmt.Errorf("node returned unknown continuation output port %q", outcome.Continuation.OutputPort)
		}
		work.Status = "WAITING"
		snapshot.Continuations = append(snapshot.Continuations, ContinuationRecord{
			ContinuationID: outcome.Continuation.ContinuationID, WorkItemID: work.WorkItemID, NodeID: work.NodeID, Path: work.Input.Path,
			WakeAt: outcome.Continuation.WakeAt, OutputPort: outcome.Continuation.OutputPort,
			Value: TypedValue{Type: outputType, Data: append(json.RawMessage(nil), outcome.Continuation.Payload...)}, Status: "PENDING",
		})
		snapshot.Execution.Status = ExecutionWaiting
		return stateCAS, nil
	}
	if len(outcome.Effects) > 0 {
		if r.mode == ModeReplay {
			for _, effect := range outcome.Effects {
				snapshot.Effects = append(snapshot.Effects, EffectRecord{Effect: effect, WorkItemID: work.WorkItemID, NodeID: work.NodeID, Path: work.Input.Path, Outputs: cloneOutputs(outcome.Outputs), Status: "SIMULATED"})
			}
			work.Status = "SUCCEEDED"
			for _, output := range outcome.Outputs {
				r.routeOutput(snapshot, *work, output)
			}
			return stateCAS, nil
		}
		for _, effect := range outcome.Effects {
			snapshot.Effects = append(snapshot.Effects, EffectRecord{Effect: effect, WorkItemID: work.WorkItemID, NodeID: work.NodeID, Path: work.Input.Path, Outputs: cloneOutputs(outcome.Outputs), Status: "PENDING"})
		}
		work.Status = "BLOCKED_EFFECT"
		snapshot.Execution.Status = ExecutionBlocked
		return stateCAS, nil
	}
	work.Status = "SUCCEEDED"
	for _, output := range outcome.Outputs {
		r.routeOutput(snapshot, *work, output)
	}
	return stateCAS, nil
}

func (r *Runtime) applyFailure(snapshot *ExecutionSnapshot, work *WorkRecord, class FailureClass, code string, now time.Time) {
	work.Failure = class
	work.FailureCode = code
	switch class {
	case FailurePoison, FailureSchemaUnknown:
		work.Status = "QUARANTINED"
		snapshot.Execution.Status = ExecutionQuarantined
		snapshot.Execution.TerminalCode = code
	case FailureBudgetExhausted:
		work.Status = "DEAD"
		snapshot.Execution.Status = ExecutionDead
		snapshot.Execution.TerminalCode = code
	case FailureTransient, FailureTimeout:
		if work.Attempt >= snapshot.Execution.AttemptBudget {
			work.Status = "DEAD"
			snapshot.Execution.Status = ExecutionDead
			snapshot.Execution.TerminalCode = "ATTEMPTS_EXHAUSTED"
			return
		}
		work.Status = "RETRY_WAIT"
		work.RetryAt = now.Add(retryDelay(work.Attempt))
		snapshot.Execution.Status = ExecutionWaiting
	case FailureAmbiguous:
		work.Status = "BLOCKED_EFFECT"
		snapshot.Execution.Status = ExecutionBlocked
		snapshot.Execution.TerminalCode = code
	default:
		work.Status = "FAILED"
		snapshot.Execution.Status = ExecutionFailed
		snapshot.Execution.TerminalCode = code
	}
}

func (r *Runtime) resumeDueContinuations(snapshot *ExecutionSnapshot, now time.Time) error {
	for index := range snapshot.Continuations {
		continuation := &snapshot.Continuations[index]
		if continuation.Status != "PENDING" || continuation.WakeAt.After(now) {
			continue
		}
		workIndex := findWork(*snapshot, continuation.WorkItemID)
		if workIndex < 0 {
			return errors.New("continuation references missing work item")
		}
		continuation.Status = "CONSUMED"
		snapshot.Work[workIndex].Status = "SUCCEEDED"
		output := NodeOutput{Port: continuation.OutputPort, Value: continuation.Value}
		r.routeOutput(snapshot, snapshot.Work[workIndex], output)
	}
	return nil
}

func (r *Runtime) routeOutput(snapshot *ExecutionSnapshot, source WorkRecord, output NodeOutput) {
	for _, edge := range r.plan.Outgoing[source.NodeID] {
		if edge.FromPort != output.Port {
			continue
		}
		path := source.Input.Path + "/" + source.NodeID + ":" + output.Port + "->" + edge.ToNode + ":" + edge.ToPort
		input := NodeInput{Port: edge.ToPort, Value: output.Value, Path: path}
		workID := WorkItemID(snapshot.Execution.ExecutionID, path, edge.ToNode, PayloadDigest(output.Value.Data))
		if findWork(*snapshot, workID) >= 0 {
			continue
		}
		snapshot.Work = append(snapshot.Work, WorkRecord{WorkItemID: workID, ExecutionID: snapshot.Execution.ExecutionID, NodeID: edge.ToNode, Input: input, Status: "READY"})
	}
}

func (r *Runtime) recalculateStatus(snapshot *ExecutionSnapshot, now time.Time) {
	if terminal(snapshot.Execution.Status) {
		return
	}
	for _, effect := range snapshot.Effects {
		if effect.Status == "AMBIGUOUS" || effect.Status == "DISPATCHING" || effect.Status == "PENDING" {
			snapshot.Execution.Status = ExecutionBlocked
			return
		}
	}
	for _, work := range snapshot.Work {
		if work.Status == "READY" || (work.Status == "RETRY_WAIT" && !work.RetryAt.After(now)) {
			snapshot.Execution.Status = ExecutionRunning
			return
		}
		if work.Status == "RETRY_WAIT" {
			snapshot.Execution.Status = ExecutionWaiting
			return
		}
	}
	for _, continuation := range snapshot.Continuations {
		if continuation.Status == "PENDING" {
			snapshot.Execution.Status = ExecutionWaiting
			return
		}
	}
	for _, work := range snapshot.Work {
		if work.Status != "SUCCEEDED" {
			snapshot.Execution.Status = ExecutionRunning
			return
		}
	}
	snapshot.Execution.Status = ExecutionSucceeded
	snapshot.Execution.TerminalCode = "COMPLETED"
}

func nextRunnableWork(snapshot ExecutionSnapshot, now time.Time) int {
	for index, work := range snapshot.Work {
		if work.Status == "READY" || (work.Status == "RETRY_WAIT" && !work.RetryAt.After(now)) {
			return index
		}
	}
	return -1
}

func findWork(snapshot ExecutionSnapshot, workItemID string) int {
	for index := range snapshot.Work {
		if snapshot.Work[index].WorkItemID == workItemID {
			return index
		}
	}
	return -1
}

func allWorkEffectsDelivered(snapshot ExecutionSnapshot, workItemID string) bool {
	found := false
	for _, effect := range snapshot.Effects {
		if effect.WorkItemID != workItemID {
			continue
		}
		found = true
		if effect.Status != "DELIVERED" {
			return false
		}
	}
	return found
}

func validateNodeOutcome(definition NodeDefinition, outcome NodeOutcome) error {
	if outcome.Failure != FailureNone && (len(outcome.Outputs) > 0 || len(outcome.Effects) > 0 || outcome.Continuation != nil || outcome.State != nil) {
		return errors.New("failed node outcome must not also emit work")
	}
	for _, output := range outcome.Outputs {
		portType, ok := definition.Outputs[output.Port]
		if !ok || portType != output.Value.Type {
			return fmt.Errorf("node emitted invalid output port %q", output.Port)
		}
	}
	if len(outcome.Effects) > 0 {
		if definition.EffectOwner == "" {
			return errors.New("pure node emitted an effect")
		}
		for _, effect := range outcome.Effects {
			portType, portExists := definition.Outputs[effect.OutputPort]
			if !portExists || portType != PortIntent || effect.OwnerDomain != definition.EffectOwner || effect.IntentType == "" {
				return errors.New("node effect does not match its declared typed owner port")
			}
		}
	}
	if outcome.Continuation != nil {
		if len(outcome.Outputs) > 0 || len(outcome.Effects) > 0 {
			return errors.New("continuation outcome must be an exclusive durable boundary")
		}
		if _, ok := definition.Outputs[outcome.Continuation.OutputPort]; !ok {
			return fmt.Errorf("node emitted invalid continuation port %q", outcome.Continuation.OutputPort)
		}
	}
	if outcome.State != nil {
		if definition.StateSchemaVersion <= 0 || outcome.State.SchemaVersion != definition.StateSchemaVersion || outcome.State.ScopeKey == "" || !json.Valid(outcome.State.Value) {
			return errors.New("node state transition does not match its declared state schema")
		}
	}
	return nil
}

func classifyDeliveryError(err error) (FailureClass, string) {
	var deliveryError *EffectDeliveryError
	if errors.As(err, &deliveryError) {
		switch deliveryError.Class {
		case FailureTransient, FailurePolicy, FailureSafetyDenied, FailureValidation, FailureAmbiguous:
			return deliveryError.Class, deliveryError.Code
		default:
			return FailureAmbiguous, deliveryError.Code
		}
	}
	return FailureAmbiguous, "EFFECT_OUTCOME_UNKNOWN"
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 6 {
		shift = 6
	}
	return time.Second * time.Duration(1<<shift)
}

func cloneOutputs(outputs []NodeOutput) []NodeOutput {
	result := make([]NodeOutput, len(outputs))
	copy(result, outputs)
	for index := range result {
		result[index].Value.Data = append(json.RawMessage(nil), result[index].Value.Data...)
	}
	return result
}

func (input NodeInput) Type() PortType {
	return input.Value.Type
}
