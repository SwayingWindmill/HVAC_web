package ruleruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestStableExecutionWorkAndEffectIdentities(t *testing.T) {
	plan := mustCompile(t, alarmIntentRule(), CoreCatalogV1())
	store := NewMemoryStore()
	reader := fixedSnapshotReader{value: TypedValue{Type: PortSnapshot, Data: json.RawMessage(`{"quality":"GOOD","value":42}`)}}
	runtime := mustRuntime(t, plan, store, reader, nil, ModeLive)
	now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	binding, event := testBindingEvent(plan, now)

	first, created, err := runtime.Start(context.Background(), binding, event, now)
	if err != nil || !created {
		t.Fatalf("first Start() created=%v err=%v", created, err)
	}
	second, created, err := runtime.Start(context.Background(), binding, event, now.Add(time.Second))
	if err != nil || created {
		t.Fatalf("second Start() created=%v err=%v", created, err)
	}
	if first.Execution.ExecutionID != second.Execution.ExecutionID || first.Work[0].WorkItemID != second.Work[0].WorkItemID {
		t.Fatal("same event + rule + binding did not preserve execution/work identity")
	}

	blocked, err := runtime.Run(context.Background(), first.Execution.ExecutionID, "worker-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Execution.Status != ExecutionBlocked || len(blocked.Effects) != 1 {
		t.Fatalf("status=%s effects=%d, want BLOCKED_EFFECT and one intent", blocked.Execution.Status, len(blocked.Effects))
	}
	effect := blocked.Effects[0].Effect
	want := EffectID(blocked.Effects[0].WorkItemID, effect.OutputPort, 0, effect.PayloadDigest)
	if effect.EffectID != want {
		t.Fatalf("effect id=%s want=%s", effect.EffectID, want)
	}
}

func TestSameEventIDWithDifferentPayloadIsIdentityConflict(t *testing.T) {
	plan := mustCompile(t, validNumberRule(), CoreCatalogV1())
	store := NewMemoryStore()
	runtime := mustRuntime(t, plan, store, nil, nil, ModeLive)
	now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	binding, event := testBindingEvent(plan, now)
	if _, created, err := runtime.Start(context.Background(), binding, event, now); err != nil || !created {
		t.Fatalf("first Start() created=%v err=%v", created, err)
	}
	event.Payload = json.RawMessage(`{"value":99}`)
	event.PayloadDigest = ""
	if _, _, err := runtime.Start(context.Background(), binding, event, now.Add(time.Second)); !errors.Is(err, ErrExecutionIdentity) {
		t.Fatalf("second Start() err=%v want ErrExecutionIdentity", err)
	}
}

func TestContinuationSurvivesRuntimeRestart(t *testing.T) {
	plan := mustCompile(t, delayRule(), CoreCatalogV1())
	store := NewMemoryStore()
	now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	binding, event := testBindingEvent(plan, now)
	firstRuntime := mustRuntime(t, plan, store, nil, nil, ModeLive)
	seed, _, err := firstRuntime.Start(context.Background(), binding, event, now)
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := firstRuntime.Run(context.Background(), seed.Execution.ExecutionID, "worker-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Execution.Status != ExecutionWaiting || len(waiting.Continuations) != 1 || waiting.Continuations[0].Status != "PENDING" {
		t.Fatalf("continuation was not durably waiting: %+v", waiting)
	}
	continuationID := waiting.Continuations[0].ContinuationID

	restarted := mustRuntime(t, plan, store, nil, nil, ModeLive)
	completed, err := restarted.Run(context.Background(), seed.Execution.ExecutionID, "worker-b", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if completed.Execution.Status != ExecutionSucceeded {
		t.Fatalf("status=%s want SUCCEEDED", completed.Execution.Status)
	}
	if completed.Continuations[0].ContinuationID != continuationID || completed.Continuations[0].Status != "CONSUMED" {
		t.Fatal("restart did not consume the original durable continuation")
	}
}

func TestEffectIntentSurvivesRestartAndUsesSameIdentity(t *testing.T) {
	plan := mustCompile(t, alarmIntentRule(), CoreCatalogV1())
	store := NewMemoryStore()
	reader := fixedSnapshotReader{value: TypedValue{Type: PortSnapshot, Data: json.RawMessage(`{"quality":"GOOD","value":42}`)}}
	now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	binding, event := testBindingEvent(plan, now)
	beforeCrash := mustRuntime(t, plan, store, reader, nil, ModeLive)
	seed, _, _ := beforeCrash.Start(context.Background(), binding, event, now)
	blocked, err := beforeCrash.Run(context.Background(), seed.Execution.ExecutionID, "worker-a", now)
	if err != nil {
		t.Fatal(err)
	}
	effectID := blocked.Effects[0].Effect.EffectID

	sink := &recordingEffectSink{}
	afterRestart := mustRuntime(t, plan, store, reader, sink, ModeLive)
	afterEffect, err := afterRestart.DispatchEffects(context.Background(), seed.Execution.ExecutionID, "worker-b", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.ids) != 1 || sink.ids[0] != effectID {
		t.Fatalf("delivered effect ids=%v want [%s]", sink.ids, effectID)
	}
	if afterEffect.Effects[0].Status != "DELIVERED" {
		t.Fatalf("effect status=%s want DELIVERED", afterEffect.Effects[0].Status)
	}
	completed, err := afterRestart.Run(context.Background(), seed.Execution.ExecutionID, "worker-c", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if completed.Execution.Status != ExecutionSucceeded {
		t.Fatalf("status=%s want SUCCEEDED", completed.Execution.Status)
	}
}

func TestCrashAfterOwnerEffectDoesNotBlindlyReplay(t *testing.T) {
	plan := mustCompile(t, alarmIntentRule(), CoreCatalogV1())
	store := NewMemoryStore()
	reader := fixedSnapshotReader{value: TypedValue{Type: PortSnapshot, Data: json.RawMessage(`{"quality":"GOOD","value":42}`)}}
	now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	binding, event := testBindingEvent(plan, now)
	runtime := mustRuntime(t, plan, store, reader, nil, ModeLive)
	seed, _, _ := runtime.Start(context.Background(), binding, event, now)
	if _, err := runtime.Run(context.Background(), seed.Execution.ExecutionID, "worker-a", now); err != nil {
		t.Fatal(err)
	}

	firstSink := &recordingEffectSink{}
	crashingStore := &failSaveStore{ExecutionStore: store, failOn: 2}
	crashingRuntime := mustRuntime(t, plan, crashingStore, reader, firstSink, ModeLive)
	if _, err := crashingRuntime.DispatchEffects(context.Background(), seed.Execution.ExecutionID, "worker-b", now.Add(time.Second)); err == nil {
		t.Fatal("DispatchEffects() unexpectedly survived the simulated receipt-persistence crash")
	}
	if len(firstSink.ids) != 1 {
		t.Fatalf("owner calls=%v want exactly one call before crash", firstSink.ids)
	}

	secondSink := &recordingEffectSink{}
	restarted := mustRuntime(t, plan, store, reader, secondSink, ModeLive)
	blocked, err := restarted.DispatchEffects(context.Background(), seed.Execution.ExecutionID, "worker-c", now.Add(32*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(secondSink.ids) != 0 {
		t.Fatalf("restart blindly replayed an outcome-unknown effect: %v", secondSink.ids)
	}
	if blocked.Execution.Status != ExecutionBlocked || blocked.Effects[0].Status != "AMBIGUOUS" {
		t.Fatalf("restart state=%s effect=%s want BLOCKED_EFFECT/AMBIGUOUS", blocked.Execution.Status, blocked.Effects[0].Status)
	}
}

func TestDeadAndQuarantineNeverBecomeSuccess(t *testing.T) {
	t.Run("bounded transient retry becomes dead", func(t *testing.T) {
		catalog := catalogWithFailureNode(FailureTransient)
		plan := mustCompile(t, failureRule(catalog.Version, 3), catalog)
		store := NewMemoryStore()
		runtime := mustRuntime(t, plan, store, nil, nil, ModeLive)
		now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
		binding, event := testBindingEvent(plan, now)
		seed, _, _ := runtime.Start(context.Background(), binding, event, now)

		first, _ := runtime.Run(context.Background(), seed.Execution.ExecutionID, "worker-a", now)
		second, _ := runtime.Run(context.Background(), seed.Execution.ExecutionID, "worker-b", now.Add(time.Second))
		third, _ := runtime.Run(context.Background(), seed.Execution.ExecutionID, "worker-c", now.Add(3*time.Second))
		if first.Execution.Status != ExecutionWaiting || second.Execution.Status != ExecutionWaiting || third.Execution.Status != ExecutionDead {
			t.Fatalf("retry statuses=%s,%s,%s", first.Execution.Status, second.Execution.Status, third.Execution.Status)
		}
		again, err := runtime.Run(context.Background(), seed.Execution.ExecutionID, "worker-d", now.Add(time.Hour))
		if err != nil || again.Execution.Status != ExecutionDead {
			t.Fatalf("dead execution advanced: status=%s err=%v", again.Execution.Status, err)
		}
	})

	t.Run("poison event remains quarantined", func(t *testing.T) {
		catalog := catalogWithFailureNode(FailurePoison)
		plan := mustCompile(t, failureRule(catalog.Version, 3), catalog)
		store := NewMemoryStore()
		runtime := mustRuntime(t, plan, store, nil, nil, ModeLive)
		now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
		binding, event := testBindingEvent(plan, now)
		seed, _, _ := runtime.Start(context.Background(), binding, event, now)
		quarantined, err := runtime.Run(context.Background(), seed.Execution.ExecutionID, "worker-a", now)
		if err != nil || quarantined.Execution.Status != ExecutionQuarantined {
			t.Fatalf("status=%s err=%v", quarantined.Execution.Status, err)
		}
		again, err := runtime.Run(context.Background(), seed.Execution.ExecutionID, "worker-b", now.Add(time.Hour))
		if err != nil || again.Execution.Status != ExecutionQuarantined {
			t.Fatalf("quarantined execution advanced: status=%s err=%v", again.Execution.Status, err)
		}
	})
}

func TestReplayUsesFrozenFactsAndRejectsLiveEffectSink(t *testing.T) {
	plan := mustCompile(t, alarmIntentRule(), CoreCatalogV1())
	store := NewMemoryStore()
	reader := fixedSnapshotReader{value: TypedValue{Type: PortSnapshot, Data: json.RawMessage(`{"quality":"GOOD","value":42}`)}}
	if _, err := NewRuntime(plan, store, reader, &recordingEffectSink{}, ModeReplay); err == nil {
		t.Fatal("NewRuntime() accepted a live effect sink in REPLAY mode")
	}
	runtime := mustRuntime(t, plan, store, reader, nil, ModeReplay)
	now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	binding, event := testBindingEvent(plan, now)
	seed, _, _ := runtime.Start(context.Background(), binding, event, now)
	result, err := runtime.Run(context.Background(), seed.Execution.ExecutionID, "replay-worker", now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Execution.Status != ExecutionSucceeded {
		t.Fatalf("replay status=%s want SUCCEEDED", result.Execution.Status)
	}
	if len(result.Effects) != 1 || result.Effects[0].Status != "SIMULATED" {
		t.Fatalf("replay effect evidence=%+v", result.Effects)
	}
}

func TestReplayRejectsNonFrozenSnapshotReader(t *testing.T) {
	plan := mustCompile(t, alarmIntentRule(), CoreCatalogV1())
	store := NewMemoryStore()
	reader := liveSnapshotReader{value: TypedValue{Type: PortSnapshot, Data: json.RawMessage(`{"quality":"GOOD"}`)}}
	if _, err := NewRuntime(plan, store, reader, nil, ModeReplay); err == nil {
		t.Fatal("NewRuntime() accepted a live snapshot reader in REPLAY mode")
	}
}

func TestStateTransitionUsesSharedAtomicCompareAndSwap(t *testing.T) {
	plan := mustCompile(t, validNumberRule(), CoreCatalogV1())
	store := NewMemoryStore()
	runtime := mustRuntime(t, plan, store, nil, nil, ModeLive)
	now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	binding, event := testBindingEvent(plan, now)
	seed, _, err := runtime.Start(context.Background(), binding, event, now)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(context.Background(), seed.Execution.ExecutionID, "state-worker", now, defaultLeaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	key := RuleStateKey{TenantID: plan.Revision.TenantID, RuleRevisionID: plan.Revision.ID, NodeInstanceID: "stateful-node", ScopeKey: "point-a"}
	transition := StateTransition{ScopeKey: key.ScopeKey, SchemaVersion: 1, ExpectedRevision: 0, Value: json.RawMessage(`{"armed":true}`)}
	claimed.StateTransitions = append(claimed.StateTransitions, AppliedStateTransition{RuleStateKey: key, SchemaVersion: 1, ExpectedRevision: 0, ResultRevision: 1, ValueDigest: PayloadDigest(transition.Value)})
	state, err := store.SaveWithStateCAS(context.Background(), claimed, "state-worker", claimed.Execution.LeaseFence, now, key, transition)
	if err != nil || state.Revision != 1 {
		t.Fatalf("initial CAS state=%+v err=%v", state, err)
	}

	transition.ExpectedRevision = 1
	transition.Value = json.RawMessage(`{"armed":false}`)
	claimed.StateTransitions = append(claimed.StateTransitions, AppliedStateTransition{RuleStateKey: key, SchemaVersion: 1, ExpectedRevision: 1, ResultRevision: 2, ValueDigest: PayloadDigest(transition.Value)})
	state, err = store.SaveWithStateCAS(context.Background(), claimed, "state-worker", claimed.Execution.LeaseFence, now, key, transition)
	if err != nil || state.Revision != 2 {
		t.Fatalf("second CAS state=%+v err=%v", state, err)
	}
	persisted, found, err := store.ReadRuleState(context.Background(), key)
	if err != nil || !found || persisted.Revision != 2 {
		t.Fatalf("shared state=%+v found=%v err=%v", persisted, found, err)
	}

	transition.ExpectedRevision = 1
	if _, err := store.SaveWithStateCAS(context.Background(), claimed, "state-worker", claimed.Execution.LeaseFence, now, key, transition); !errors.Is(err, ErrStateCASConflict) {
		t.Fatalf("stale CAS err=%v want ErrStateCASConflict", err)
	}
}

func TestAmbiguousEffectDoesNotRetry(t *testing.T) {
	plan := mustCompile(t, alarmIntentRule(), CoreCatalogV1())
	store := NewMemoryStore()
	reader := fixedSnapshotReader{value: TypedValue{Type: PortSnapshot, Data: json.RawMessage(`{"quality":"GOOD"}`)}}
	sink := &recordingEffectSink{err: &EffectDeliveryError{Class: FailureAmbiguous, Code: "OWNER_OUTCOME_UNKNOWN", Err: errors.New("owner outcome unknown")}}
	runtime := mustRuntime(t, plan, store, reader, sink, ModeLive)
	now := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)
	binding, event := testBindingEvent(plan, now)
	seed, _, _ := runtime.Start(context.Background(), binding, event, now)
	_, _ = runtime.Run(context.Background(), seed.Execution.ExecutionID, "worker-a", now)
	blocked, err := runtime.DispatchEffects(context.Background(), seed.Execution.ExecutionID, "worker-b", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Execution.Status != ExecutionBlocked || blocked.Effects[0].Status != "AMBIGUOUS" || len(sink.ids) != 1 {
		t.Fatalf("ambiguous effect state=%+v calls=%v", blocked.Effects[0], sink.ids)
	}
	_, err = runtime.DispatchEffects(context.Background(), seed.Execution.ExecutionID, "worker-c", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.ids) != 1 {
		t.Fatalf("ambiguous effect was retried: %v", sink.ids)
	}
}

type failSaveStore struct {
	ExecutionStore
	failOn int
	calls  int
}

func (s *failSaveStore) Save(ctx context.Context, snapshot ExecutionSnapshot, owner string, fence int64, now time.Time) error {
	s.calls++
	if s.calls == s.failOn {
		return errors.New("simulated crash before receipt persistence")
	}
	return s.ExecutionStore.Save(ctx, snapshot, owner, fence, now)
}

type fixedSnapshotReader struct{ value TypedValue }

func (r fixedSnapshotReader) ReadSnapshot(context.Context, SnapshotRequest) (TypedValue, error) {
	return r.value, nil
}

func (fixedSnapshotReader) FrozenFactsRevision() string { return "test-fixture-v1" }

type liveSnapshotReader struct{ value TypedValue }

func (r liveSnapshotReader) ReadSnapshot(context.Context, SnapshotRequest) (TypedValue, error) {
	return r.value, nil
}

type recordingEffectSink struct {
	ids []string
	err error
}

func (s *recordingEffectSink) Deliver(_ context.Context, effect EffectIntent) (string, error) {
	s.ids = append(s.ids, effect.EffectID)
	if s.err != nil {
		return "", s.err
	}
	return "receipt:" + effect.EffectID, nil
}

func delayRule() RuleRevision {
	return RuleRevision{
		ID: "rule-revision-delay", RuleID: "rule-delay", TenantID: "tenant-a", Revision: 1,
		State: RevisionReleased, CatalogVersion: "core.v1", EntryNodeID: "delay",
		Nodes: []NodeInstance{
			{ID: "delay", DefinitionID: "delay_event", Config: json.RawMessage(`{"delayMillis":1000}`)},
			{ID: "end", DefinitionID: "terminal_event", Config: json.RawMessage(`{}`)},
		},
		Edges:    []Edge{{FromNode: "delay", FromPort: "resume", ToNode: "end", ToPort: "in"}},
		MaxNodes: 4, MaxDepth: 4, MaxFanout: 2, MaxResourceCost: 4, MaxAttempts: 3,
	}
}

func alarmIntentRule() RuleRevision {
	return RuleRevision{
		ID: "rule-revision-alarm", RuleID: "rule-alarm", TenantID: "tenant-a", Revision: 7,
		State: RevisionReleased, CatalogVersion: "core.v1", EntryNodeID: "read",
		Nodes: []NodeInstance{
			{ID: "read", DefinitionID: "owner_snapshot_read", Config: json.RawMessage(`{"ownerDomain":"TELEMETRY","kind":"POINT_CURRENT","revision":9}`)},
			{ID: "intent", DefinitionID: "alarm_intent", Config: json.RawMessage(`{"intentType":"ALARM_CONDITION_OBSERVATION"}`)},
			{ID: "end", DefinitionID: "terminal_intent", Config: json.RawMessage(`{}`)},
		},
		Edges: []Edge{
			{FromNode: "read", FromPort: "snapshot", ToNode: "intent", ToPort: "in"},
			{FromNode: "intent", FromPort: "intent", ToNode: "end", ToPort: "in"},
		},
		AllowedPermissions: []string{"owner.snapshot.read", "alarm.intent.publish"},
		MaxNodes:           6, MaxDepth: 6, MaxFanout: 2, MaxResourceCost: 8, MaxAttempts: 3,
	}
}

func catalogWithFailureNode(class FailureClass) Catalog {
	base := CoreCatalogV1()
	definitions := make(map[string]NodeDefinition, len(base.Definitions)+1)
	for key, value := range base.Definitions {
		definitions[key] = value
	}
	definitions["test_failure"] = NodeDefinition{
		ID: "test_failure", Version: 1, Inputs: map[string]PortType{"in": PortEvent}, Outputs: map[string]PortType{"out": PortEvent}, Deterministic: true, ResourceCost: 1,
		Evaluate: func(context.Context, NodeContext, json.RawMessage, NodeInput) (NodeOutcome, error) {
			return NodeOutcome{Failure: class, FailureCode: "TEST_FAILURE"}, nil
		},
	}
	return Catalog{Version: "test.v1", Definitions: definitions}
}

func failureRule(catalogVersion string, attempts int) RuleRevision {
	return RuleRevision{
		ID: "rule-revision-failure", RuleID: "rule-failure", TenantID: "tenant-a", Revision: 1,
		State: RevisionReleased, CatalogVersion: catalogVersion, EntryNodeID: "fail",
		Nodes: []NodeInstance{
			{ID: "fail", DefinitionID: "test_failure", Config: json.RawMessage(`{}`)},
			{ID: "end", DefinitionID: "terminal_event", Config: json.RawMessage(`{}`)},
		},
		Edges:    []Edge{{FromNode: "fail", FromPort: "out", ToNode: "end", ToPort: "in"}},
		MaxNodes: 4, MaxDepth: 4, MaxFanout: 2, MaxResourceCost: 4, MaxAttempts: attempts,
	}
}

func testBindingEvent(plan ExecutionPlan, now time.Time) (RuleBinding, RuleEventEnvelope) {
	binding := RuleBinding{ID: "binding-a", TenantID: plan.Revision.TenantID, Revision: 3, RuleRevisionID: plan.Revision.ID}
	event := RuleEventEnvelope{
		EventID: "event-a", Schema: "telemetry.point.observed.v1", TenantID: plan.Revision.TenantID, SiteID: "site-a",
		SubjectType: "POINT", SubjectID: "point-a", OccurredAt: now, ReceivedAt: now,
		Payload: json.RawMessage(`{"value":41}`),
	}
	return binding, event
}

func mustCompile(t *testing.T, rule RuleRevision, catalog Catalog) ExecutionPlan {
	t.Helper()
	plan, err := Compile(rule, catalog)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func mustRuntime(t *testing.T, plan ExecutionPlan, store ExecutionStore, reader SnapshotReader, sink EffectSink, mode ExecutionMode) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(plan, store, reader, sink, mode)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}
