package mqttconnector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

func TestRecoveredCorrelationNeverRepublishesAfterCommitPoint(t *testing.T) {
	tests := []struct {
		name  string
		state commandmodel.CorrelationState
		want  ResumeAction
	}{
		{name: "crash before commit point may publish", state: commandmodel.CorrelationPrepared, want: ResumePublish},
		{name: "crash after commit point is outcome unknown", state: commandmodel.CorrelationMayCommit, want: ResumeOutcomeUnknown},
		{name: "durable reply is reused", state: commandmodel.CorrelationReplied, want: ResumeUseReply},
		{name: "resolved reply is reused", state: commandmodel.CorrelationResolved, want: ResumeUseReply},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ResumeActionForCorrelation(test.state); got != test.want {
				t.Fatalf("resume action=%d want=%d", got, test.want)
			}
		})
	}
}

type fakeTransportState struct {
	mu           sync.Mutex
	correlations map[string]commandmodel.CommandCorrelation
}

func (s *fakeTransportState) ResolveCommandRoute(context.Context, commandmodel.DispatchEnvelope) (commandmodel.DeviceRoute, error) {
	return commandmodel.DeviceRoute{}, nil
}

func (s *fakeTransportState) GatewayTenant(context.Context, string) (string, error) {
	return "tenant-1", nil
}

func (s *fakeTransportState) Tenants(context.Context) ([]string, error) {
	return []string{"tenant-1"}, nil
}

func (s *fakeTransportState) PrepareCommandCorrelation(_ context.Context, c commandmodel.CommandCorrelation) (commandmodel.CommandCorrelation, error) {
	return c, nil
}

func (s *fakeTransportState) ArmCommandCorrelation(context.Context, string, string, uint64, time.Time) error {
	return nil
}

func (s *fakeTransportState) RecordCommandReply(context.Context, string, string, string, uint64, string, string, time.Time, string, *commandmodel.EdgeExecutionEvidence, time.Time) (commandmodel.CommandCorrelation, error) {
	return commandmodel.CommandCorrelation{}, nil
}

func (s *fakeTransportState) RecoverCommandReplies(_ context.Context, tenantID string, limit int) ([]commandmodel.CommandCorrelation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]commandmodel.CommandCorrelation, 0, len(s.correlations))
	for _, corr := range s.correlations {
		if corr.Envelope.TenantID == tenantID && corr.State == commandmodel.CorrelationReplied {
			out = append(out, corr)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].RepliedAt.Equal(out[j].RepliedAt) {
			return out[i].RepliedAt.Before(out[j].RepliedAt)
		}
		return out[i].Envelope.AttemptID < out[j].Envelope.AttemptID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *fakeTransportState) put(corr commandmodel.CommandCorrelation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fmt.Sprintf("%s|%s|%d", corr.Envelope.TenantID, corr.Envelope.AttemptID, corr.Envelope.ExecutionFence)
	s.correlations[key] = corr
}

func (s *fakeTransportState) MarkCommandCorrelationResolved(_ context.Context, tenantID, attemptID string, executionFence uint64, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fmt.Sprintf("%s|%s|%d", tenantID, attemptID, executionFence)
	corr, ok := s.correlations[key]
	if ok {
		corr.State = commandmodel.CorrelationResolved
		s.correlations[key] = corr
	}
	return nil
}

type fakeEvidenceStore struct{}

func (fakeEvidenceStore) Prepare(context.Context, commandmodel.PreparedConnectorEvidence) error {
	return nil
}

func (fakeEvidenceStore) Complete(context.Context, commandmodel.CompletedConnectorEvidence) error {
	return nil
}

type fakeLateResultSink struct {
	service *commandservice.Service
}

func (s fakeLateResultSink) ResolveDispatch(_ context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error {
	return s.service.ResolveDispatch(envelope, result)
}

func TestRecoverTenantRepliesResolvesStaleFenceWithoutStarvingLiveReplies(t *testing.T) {
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	service := commandservice.New(func() time.Time { return now })
	transport := &fakeTransportState{correlations: make(map[string]commandmodel.CommandCorrelation)}
	connector, err := NewOffline(Config{
		TransportState: transport,
		EvidenceStore:  fakeEvidenceStore{},
		LateResultSink: fakeLateResultSink{service: service},
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	requested := commandmodel.NumberScalar(24)
	effective := commandmodel.NumberScalar(24)
	edgeExec := &commandmodel.EdgeExecutionEvidence{
		Requested: requested, Effective: &effective, Applied: &effective,
		WinnerControllerID: "cloud-command-intent", Cycle: 1,
	}
	submitForDevice := func(deviceID, key string) commandmodel.CommandIntent {
		current := 23.0
		sub, err := service.Submit(commandmodel.SubmitRequest{
			TenantID: "tenant-1", SiteID: "site-1", DeviceID: deviceID, PointID: "point-1",
			PrincipalID: "principal-1", IdempotencyKey: key,
			Capability:           commandmodel.CapabilitySetTemperatureSetpoint,
			Parameters:           commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24},
			VerificationPointKey: "zone.temperature_setpoint",
			CurrentState: commandmodel.CurrentStateEvidence{
				EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Quality: "GOOD",
				BusinessRevision: 17, CurrentValue: &current, ObservedAt: now,
			},
			Authorization: commandmodel.AuthorizationSnapshot{
				GrantID: "grant-" + key, PolicyRevision: "command-policy-1", Purpose: commandmodel.AuthorizationCommandSubmit,
				PrincipalID: "principal-1", TenantID: "tenant-1", SiteID: "site-1", DeviceID: deviceID,
				Capability: commandmodel.CapabilitySetTemperatureSetpoint, MaximumRisk: commandmodel.RiskHigh,
				CapabilityRevision: "capability:set-temperature-setpoint:v1", EmergencyRevocationRevision: 1,
				IssuedAt: now.Add(-5 * time.Second), ExpiresAt: now.Add(25 * time.Second),
			},
		})
		if err != nil {
			t.Fatalf("submit %s: %v", key, err)
		}
		return sub.Intent
	}

	for i := 0; i < 49; i++ {
		intent := submitForDevice(fmt.Sprintf("device-%02d", i), fmt.Sprintf("stale-%02d", i))
		env, err := service.PrepareDispatch(intent.ID, "dispatcher-old", now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if err := service.ResolveDispatch(env, commandmodel.ConnectorResult{
			Phase: commandmodel.ConnectorRequestCommitted, EvidenceID: fmt.Sprintf("timeout-%02d", i),
		}); err != nil {
			t.Fatal(err)
		}
		transport.put(commandmodel.CommandCorrelation{
			Envelope: env, GatewayID: "gateway-1", ExternalDeviceID: "ext-1",
			State: commandmodel.CorrelationReplied, ReplyStatus: "EXECUTED",
			ReplyEventTime: now, RepliedAt: now.Add(time.Duration(i) * time.Millisecond), EdgeExecution: edgeExec,
		})
	}
	// Seed 1 missing-command REPLIED correlation (ErrCommandNotFound) so staleCount=50 covers both stale-fence and unregistered attempts.
	transport.put(commandmodel.CommandCorrelation{
		Envelope: commandmodel.DispatchEnvelope{
			TenantID:       "tenant-1",
			CommandID:      "cmd-missing",
			AttemptID:      "attempt-missing",
			ExecutionFence: 1,
		},
		GatewayID: "gateway-1", ExternalDeviceID: "ext-1",
		State: commandmodel.CorrelationReplied, ReplyStatus: "EXECUTED",
		ReplyEventTime: now, RepliedAt: now.Add(49 * time.Millisecond), EdgeExecution: edgeExec,
	})

	liveIntent := submitForDevice("device-live", "live-cmd")
	liveEnv, err := service.PrepareDispatch(liveIntent.ID, "dispatcher-live", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	transport.put(commandmodel.CommandCorrelation{
		Envelope: liveEnv, GatewayID: "gateway-1", ExternalDeviceID: "ext-1",
		State: commandmodel.CorrelationReplied, ReplyStatus: "EXECUTED",
		ReplyEventTime: now, RepliedAt: now.Add(time.Second), EdgeExecution: edgeExec,
	})

	for pass := 0; pass < 2; pass++ {
		if err := connector.RecoverTenantReplies(context.Background(), "tenant-1", 50); err != nil {
			t.Fatalf("pass %d RecoverTenantReplies failed: %v", pass+1, err)
		}
	}
	remaining, err := transport.RecoverCommandReplies(context.Background(), "tenant-1", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected all REPLIED correlations resolved, %d remained", len(remaining))
	}
	settledLive, err := service.Get(liveIntent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settledLive.Attempts[0].Status != commandmodel.AttemptAcknowledged {
		t.Fatalf("live attempt=%s want=%s", settledLive.Attempts[0].Status, commandmodel.AttemptAcknowledged)
	}
}

func TestBuildCommandEnvelopeActuationFields(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	expireAt := now.Add(15 * time.Second)
	env := commandmodel.DispatchEnvelope{
		CommandID:      "cmd-1",
		AttemptID:      "att-1",
		Capability:     commandmodel.CapabilityStart,
		Parameters:     commandmodel.CommandParameters{},
		ExecutionFence: 3,
		PayloadHash:    "hash-1",
	}

	t.Run("legacy simulator route omits optional actuation fields", func(t *testing.T) {
		msg := buildCommandEnvelope(env, commandmodel.DeviceRoute{
			GatewayID: "gw-1", ExternalDeviceID: "SIM-CHILLER-01", BindingRevision: 1,
		}, "SIM-CHILLER-01", now, expireAt, 15*time.Second)
		body, err := json.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		var legacy struct {
			SchemaVersion  string                         `json:"schemaVersion"`
			MessageID      string                         `json:"messageId"`
			CommandID      string                         `json:"commandId"`
			TraceID        string                         `json:"traceId,omitempty"`
			IssuedAt       int64                          `json:"issuedAt"`
			ExpireAt       int64                          `json:"expireAt"`
			DeviceID       string                         `json:"deviceId"`
			CommandCode    string                         `json:"commandCode"`
			Params         commandmodel.CommandParameters `json:"params"`
			Policy         commandPolicy                  `json:"policy"`
			ExecutionFence uint64                         `json:"executionFence"`
			PayloadHash    string                         `json:"payloadHash"`
		}
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&legacy); err != nil {
			t.Fatalf("unexpected non-legacy JSON field in %s: %v", string(body), err)
		}
	})

	t.Run("level coil 0 serializes explicit zero coilAddress", func(t *testing.T) {
		msg := buildCommandEnvelope(env, commandmodel.DeviceRoute{
			GatewayID: "gw-1", ExternalDeviceID: "ctrl-chwp-1", BindingRevision: 1,
			PointCode: "start", ActuationMode: "LEVEL", Channel: "u11", CoilAddress: 0, HasCoilAddress: true,
		}, "ctrl-chwp-1", now, expireAt, 15*time.Second)
		body, err := json.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(body, []byte(`"coilAddress":0`)) || !bytes.Contains(body, []byte(`"actuationMode":"LEVEL"`)) || !bytes.Contains(body, []byte(`"channel":"u11"`)) {
			t.Fatalf("unexpected LEVEL envelope json=%s", string(body))
		}
	})

	t.Run("pulse coil 16 serializes pulseDurationMs and omits coilAddress for unit sequence", func(t *testing.T) {
		pulseEnv := env
		pulseEnv.Capability = commandmodel.CapabilityStop
		pulseMsg := buildCommandEnvelope(pulseEnv, commandmodel.DeviceRoute{
			GatewayID: "gw-1", ExternalDeviceID: "ctrl-chiller-a", BindingRevision: 1,
			PointCode: "stop", ActuationMode: "PULSE", Channel: "u51", CoilAddress: 16, HasCoilAddress: true, PulseDurationMs: 500,
		}, "ctrl-chiller-a", now, expireAt, 15*time.Second)
		pulseBody, err := json.Marshal(pulseMsg)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"pointCode":"stop"`, `"actuationMode":"PULSE"`, `"channel":"u51"`, `"coilAddress":16`, `"pulseDurationMs":500`} {
			if !bytes.Contains(pulseBody, []byte(want)) {
				t.Fatalf("missing %s in pulse json=%s", want, string(pulseBody))
			}
		}

		seqEnv := env
		seqEnv.Capability = commandmodel.CapabilityStartUnitSequence
		seqMsg := buildCommandEnvelope(seqEnv, commandmodel.DeviceRoute{
			GatewayID: "gw-1", ExternalDeviceID: "ctrl-chiller-a", BindingRevision: 1,
			PointCode: "start_unit_sequence", ActuationMode: "UNIT_SEQUENCE", Channel: "unit-a", PulseDurationMs: 500,
		}, "ctrl-chiller-a", now, expireAt, 15*time.Second)
		seqBody, err := json.Marshal(seqMsg)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(seqBody, []byte(`"coilAddress"`)) || !bytes.Contains(seqBody, []byte(`"actuationMode":"UNIT_SEQUENCE"`)) || !bytes.Contains(seqBody, []byte(`"channel":"unit-a"`)) {
			t.Fatalf("unexpected UNIT_SEQUENCE json=%s", string(seqBody))
		}
	})

	t.Run("EMERGENCY_DO_RELEASE omits coilAddress and serializes pointCode actuationMode and all-do channel", func(t *testing.T) {
		eStopEnv := env
		eStopEnv.Capability = commandmodel.CapabilityStop
		eStopMsg := buildCommandEnvelope(eStopEnv, commandmodel.DeviceRoute{
			GatewayID: "gw-1", ExternalDeviceID: "et1010", BindingRevision: 1,
			PointCode: "emergency_do_release", ActuationMode: "EMERGENCY_DO_RELEASE", Channel: "all-do",
		}, "et1010", now, expireAt, 15*time.Second)
		eStopBody, err := json.Marshal(eStopMsg)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(eStopBody, []byte(`"coilAddress"`)) {
			t.Fatalf("EMERGENCY_DO_RELEASE must omit coilAddress, json=%s", string(eStopBody))
		}
		for _, want := range []string{`"pointCode":"emergency_do_release"`, `"actuationMode":"EMERGENCY_DO_RELEASE"`, `"channel":"all-do"`} {
			if !bytes.Contains(eStopBody, []byte(want)) {
				t.Fatalf("missing %s in EMERGENCY_DO_RELEASE json=%s", want, string(eStopBody))
			}
		}
	})
}

func TestBuildCommandEnvelopeUsesCapabilityVerificationWindowAndValidateConfigTimeout(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	expireAt := now.Add(25 * time.Second)

	seqMsg := buildCommandEnvelope(commandmodel.DispatchEnvelope{
		CommandID: "cmd-seq", AttemptID: "att-seq", Capability: commandmodel.CapabilityStopUnitSequence,
		Parameters: commandmodel.CommandParameters{}, ExecutionFence: 1, PayloadHash: "h1",
	}, commandmodel.DeviceRoute{GatewayID: "gw-1", ExternalDeviceID: "ctrl-chiller-a", BindingRevision: 1}, "ctrl-chiller-a", now, expireAt, commandmodel.VerificationWindowFor(commandmodel.CapabilityStopUnitSequence))
	if seqMsg.Policy.VerificationWindowMS != 600000 {
		t.Fatalf("STOP_UNIT_SEQUENCE verificationWindowMs=%d want 600000", seqMsg.Policy.VerificationWindowMS)
	}

	setMsg := buildCommandEnvelope(commandmodel.DispatchEnvelope{
		CommandID: "cmd-set", AttemptID: "att-set", Capability: commandmodel.CapabilitySetTemperatureSetpoint,
		Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24}, ExecutionFence: 2, PayloadHash: "h2",
	}, commandmodel.DeviceRoute{GatewayID: "gw-1", ExternalDeviceID: "ctrl-chiller-a", BindingRevision: 1}, "ctrl-chiller-a", now, expireAt, commandmodel.VerificationWindowFor(commandmodel.CapabilitySetTemperatureSetpoint))
	if setMsg.Policy.VerificationWindowMS != 120000 {
		t.Fatalf("SET_TEMPERATURE_SETPOINT verificationWindowMs=%d want 120000", setMsg.Policy.VerificationWindowMS)
	}

	baseCfg := Config{
		BrokerURL:      "tls://mqtt-broker:8883",
		ClientID:       "connectivity-commands",
		CAFile:         "/tmp/ca.crt",
		CertFile:       "/tmp/client.crt",
		KeyFile:        "/tmp/client.key",
		ServerName:     "mqtt-broker",
		TransportState: &fakeTransportState{correlations: map[string]commandmodel.CommandCorrelation{}},
		EvidenceStore:  fakeEvidenceStore{},
		ReplyTimeout:   25 * time.Second,
	}
	if err := validateConfig(baseCfg); err != nil {
		t.Fatalf("expected 25s replyTimeout to be valid, got %v", err)
	}
	baseCfg.ReplyTimeout = 61 * time.Second
	if err := validateConfig(baseCfg); err == nil {
		t.Fatal("expected replyTimeout > 60s to fail validation")
	}
}

type routeAndReplyTransportState struct {
	fakeTransportState
	route commandmodel.DeviceRoute
}

func (s *routeAndReplyTransportState) ResolveCommandRoute(context.Context, commandmodel.DispatchEnvelope) (commandmodel.DeviceRoute, error) {
	return s.route, nil
}

type failingEvidenceStore struct {
	mu          sync.Mutex
	completeErr error
	completeCnt int
}

func (s *failingEvidenceStore) Prepare(context.Context, commandmodel.PreparedConnectorEvidence) error {
	return nil
}

func (s *failingEvidenceStore) Complete(context.Context, commandmodel.CompletedConnectorEvidence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completeCnt++
	return s.completeErr
}

func (s *failingEvidenceStore) setError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completeErr = err
}

func TestExecuteCapsWireExpireAtAndSetsMessageExpiry(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	route := commandmodel.DeviceRoute{GatewayID: "01900000-0000-7000-8000-000000000100", ExternalDeviceID: "CHILLER-01", BindingRevision: 1}
	transport := &routeAndReplyTransportState{
		fakeTransportState: fakeTransportState{correlations: make(map[string]commandmodel.CommandCorrelation)},
		route:              route,
	}

	var published []*paho.Publish
	connector, err := NewOffline(Config{
		TransportState: transport,
		EvidenceStore:  fakeEvidenceStore{},
		ReplyTimeout:   25 * time.Second,
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	connector.rootContext = context.Background()
	requested := commandmodel.NumberScalar(24)
	edgeExec := &commandmodel.EdgeExecutionEvidence{
		Requested: requested, Effective: &requested, Applied: &requested,
		WinnerControllerID: "cloud-command-intent", Cycle: 1,
	}
	connector.publish = func(_ context.Context, pub *paho.Publish) (*paho.PublishResponse, error) {
		published = append(published, pub)
		var env commandEnvelope
		if err := json.Unmarshal(pub.Payload, &env); err != nil {
			return nil, err
		}
		key := replyKey(env.CommandID, env.ExecutionFence)
		connector.mu.Lock()
		waiter := connector.waiters[key]
		connector.mu.Unlock()
		waiter <- commandmodel.CommandCorrelation{
			Envelope: commandmodel.DispatchEnvelope{
				CommandID: env.CommandID, AttemptID: env.MessageID, TenantID: "tenant-1",
				SiteID: "site-1", DeviceID: "device-1", ExecutionFence: env.ExecutionFence,
			},
			State: commandmodel.CorrelationReplied, ReplyStatus: "EXECUTED",
			ReplyEventTime: now, RepliedAt: now, EdgeExecution: edgeExec,
		}
		return &paho.PublishResponse{}, nil
	}

	envLongLease := commandmodel.DispatchEnvelope{
		CommandID: "cmd-cap", AttemptID: "att-cap", TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1",
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, CapabilityRevision: "capability:set-temperature-setpoint:v1",
		Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24}, PayloadHash: "h1",
		ExecutionFence: 1, LeaseUntil: now.Add(45 * time.Second),
	}
	res, err := connector.Execute(context.Background(), envLongLease)
	if err != nil || !res.Acknowledged {
		t.Fatalf("Execute failed: res=%#v err=%v", res, err)
	}
	if len(published) != 1 || published[0].Properties == nil || published[0].Properties.MessageExpiry == nil {
		t.Fatalf("missing PublishProperties.MessageExpiry: %#v", published)
	}
	if *published[0].Properties.MessageExpiry != 25 {
		t.Fatalf("MessageExpiry=%d want 25", *published[0].Properties.MessageExpiry)
	}
	var wire commandEnvelope
	if err := json.Unmarshal(published[0].Payload, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.ExpireAt != now.Add(25*time.Second).UnixMilli() {
		t.Fatalf("wire ExpireAt=%d want %d", wire.ExpireAt, now.Add(25*time.Second).UnixMilli())
	}

	envShortLease := envLongLease
	envShortLease.AttemptID = "att-short"
	envShortLease.ExecutionFence = 2
	envShortLease.LeaseUntil = now.Add(10*time.Second + 200*time.Millisecond)
	res, err = connector.Execute(context.Background(), envShortLease)
	if err != nil || !res.Acknowledged {
		t.Fatalf("Execute short lease failed: res=%#v err=%v", res, err)
	}
	if *published[1].Properties.MessageExpiry != 11 {
		t.Fatalf("short-lease MessageExpiry=%d want 11", *published[1].Properties.MessageExpiry)
	}
	if err := json.Unmarshal(published[1].Payload, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.ExpireAt != envShortLease.LeaseUntil.UnixMilli() {
		t.Fatalf("short-lease ExpireAt=%d want %d", wire.ExpireAt, envShortLease.LeaseUntil.UnixMilli())
	}
}

func TestRecoverRepliesDrainsBeyondLimit50AndStopsOnDisconnect(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	service := commandservice.New(func() time.Time { return now })
	transport := &fakeTransportState{correlations: make(map[string]commandmodel.CommandCorrelation)}
	connector, err := NewOffline(Config{
		TransportState: transport,
		EvidenceStore:  fakeEvidenceStore{},
		LateResultSink: fakeLateResultSink{service: service},
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	requested := commandmodel.NumberScalar(24)
	edgeExec := &commandmodel.EdgeExecutionEvidence{
		Requested: requested, Effective: &requested, Applied: &requested,
		WinnerControllerID: "cloud-command-intent", Cycle: 1,
	}
	for i := 0; i < 120; i++ {
		transport.put(commandmodel.CommandCorrelation{
			Envelope: commandmodel.DispatchEnvelope{
				TenantID: "tenant-1", CommandID: fmt.Sprintf("missing-%03d", i), AttemptID: fmt.Sprintf("att-missing-%03d", i), ExecutionFence: 1,
			},
			GatewayID: "gateway-1", ExternalDeviceID: "ext-1",
			State: commandmodel.CorrelationReplied, ReplyStatus: "EXECUTED",
			ReplyEventTime: now, RepliedAt: now.Add(time.Duration(i) * time.Millisecond), EdgeExecution: edgeExec,
		})
	}
	current := 23.0
	liveSub, err := service.Submit(commandmodel.SubmitRequest{
		TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-live", PointID: "point-1",
		PrincipalID: "principal-1", IdempotencyKey: "drain-live",
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24},
		VerificationPointKey: "zone.temperature_setpoint",
		CurrentState: commandmodel.CurrentStateEvidence{
			EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Quality: "GOOD",
			BusinessRevision: 17, CurrentValue: &current, ObservedAt: now,
		},
		Authorization: commandmodel.AuthorizationSnapshot{
			GrantID: "grant-live", PolicyRevision: "command-policy-1", Purpose: commandmodel.AuthorizationCommandSubmit,
			PrincipalID: "principal-1", TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-live",
			Capability: commandmodel.CapabilitySetTemperatureSetpoint, MaximumRisk: commandmodel.RiskHigh,
			CapabilityRevision: "capability:set-temperature-setpoint:v1", EmergencyRevocationRevision: 1,
			IssuedAt: now.Add(-5 * time.Second), ExpiresAt: now.Add(25 * time.Second),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	liveEnv, err := service.PrepareDispatch(liveSub.Intent.ID, "dispatcher-live", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	transport.put(commandmodel.CommandCorrelation{
		Envelope: liveEnv, GatewayID: "gateway-1", ExternalDeviceID: "ext-1",
		State: commandmodel.CorrelationReplied, ReplyStatus: "EXECUTED",
		ReplyEventTime: now, RepliedAt: now.Add(time.Second), EdgeExecution: edgeExec,
	})

	connector.recoverReplies(context.Background())
	remaining, err := transport.RecoverCommandReplies(context.Background(), "tenant-1", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected all 121 REPLIED correlations drained in one sweep, %d remained", len(remaining))
	}
	settledLive, err := service.Get(liveSub.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settledLive.Attempts[0].Status != commandmodel.AttemptAcknowledged {
		t.Fatalf("live attempt=%s want=%s", settledLive.Attempts[0].Status, commandmodel.AttemptAcknowledged)
	}

	loopCtx, loopCancel := context.WithCancel(context.Background())
	connector.stopRecovery = loopCancel
	connector.recoveryDone = make(chan struct{})
	live2Sub, err := service.Submit(commandmodel.SubmitRequest{
		TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-live-2", PointID: "point-1",
		PrincipalID: "principal-1", IdempotencyKey: "ticker-live",
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24},
		VerificationPointKey: "zone.temperature_setpoint",
		CurrentState: commandmodel.CurrentStateEvidence{
			EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Quality: "GOOD",
			BusinessRevision: 17, CurrentValue: &current, ObservedAt: now,
		},
		Authorization: commandmodel.AuthorizationSnapshot{
			GrantID: "grant-live-2", PolicyRevision: "command-policy-1", Purpose: commandmodel.AuthorizationCommandSubmit,
			PrincipalID: "principal-1", TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-live-2",
			Capability: commandmodel.CapabilitySetTemperatureSetpoint, MaximumRisk: commandmodel.RiskHigh,
			CapabilityRevision: "capability:set-temperature-setpoint:v1", EmergencyRevocationRevision: 1,
			IssuedAt: now.Add(-5 * time.Second), ExpiresAt: now.Add(25 * time.Second),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	live2Env, err := service.PrepareDispatch(live2Sub.Intent.ID, "dispatcher-live", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	transport.put(commandmodel.CommandCorrelation{
		Envelope: live2Env, GatewayID: "gateway-1", ExternalDeviceID: "ext-1",
		State: commandmodel.CorrelationReplied, ReplyStatus: "EXECUTED",
		ReplyEventTime: now, RepliedAt: now.Add(2 * time.Second), EdgeExecution: edgeExec,
	})
	go connector.runReplyRecoveryLoop(loopCtx, 10*time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, err := service.Get(live2Sub.Intent.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Attempts) > 0 && got.Attempts[0].Status == commandmodel.AttemptAcknowledged {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := connector.Disconnect(context.Background()); err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}
	got2, err := service.Get(live2Sub.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got2.Attempts) == 0 || got2.Attempts[0].Status != commandmodel.AttemptAcknowledged {
		t.Fatalf("periodic recovery ticker did not settle live2 attempt: %#v", got2.Attempts)
	}
}

func TestCompleteRecoveredReplyPropagatesEvidenceStoreErrorInsteadOfOutcomeUnknown(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	service := commandservice.New(func() time.Time { return now })
	evidenceStore := &failingEvidenceStore{completeErr: errors.New("evidence store temporarily unavailable")}
	transport := &routeAndReplyTransportState{
		fakeTransportState: fakeTransportState{correlations: make(map[string]commandmodel.CommandCorrelation)},
		route:              commandmodel.DeviceRoute{GatewayID: "01900000-0000-7000-8000-000000000100", ExternalDeviceID: "CHILLER-01", BindingRevision: 1},
	}
	connector, err := NewOffline(Config{
		TransportState: transport,
		EvidenceStore:  evidenceStore,
		LateResultSink: fakeLateResultSink{service: service},
		ReplyTimeout:   25 * time.Second,
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	current := 23.0
	sub, err := service.Submit(commandmodel.SubmitRequest{
		TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1", PointID: "point-1",
		PrincipalID: "principal-1", IdempotencyKey: "evidence-err",
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24},
		VerificationPointKey: "zone.temperature_setpoint",
		CurrentState: commandmodel.CurrentStateEvidence{
			EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Quality: "GOOD",
			BusinessRevision: 17, CurrentValue: &current, ObservedAt: now,
		},
		Authorization: commandmodel.AuthorizationSnapshot{
			GrantID: "grant-ev", PolicyRevision: "command-policy-1", Purpose: commandmodel.AuthorizationCommandSubmit,
			PrincipalID: "principal-1", TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1",
			Capability: commandmodel.CapabilitySetTemperatureSetpoint, MaximumRisk: commandmodel.RiskHigh,
			CapabilityRevision: "capability:set-temperature-setpoint:v1", EmergencyRevocationRevision: 1,
			IssuedAt: now.Add(-5 * time.Second), ExpiresAt: now.Add(25 * time.Second),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	env, err := service.PrepareDispatch(sub.Intent.ID, "dispatcher-a", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	requested := commandmodel.NumberScalar(24)
	edgeExec := &commandmodel.EdgeExecutionEvidence{
		Requested: requested, Effective: &requested, Applied: &requested,
		WinnerControllerID: "cloud-command-intent", Cycle: 1,
	}
	corr := commandmodel.CommandCorrelation{
		Envelope: env, GatewayID: "gateway-1", ExternalDeviceID: "CHILLER-01",
		State: commandmodel.CorrelationReplied, ReplyStatus: "EXECUTED",
		ReplyEventTime: now, RepliedAt: now, EdgeExecution: edgeExec,
	}
	transport.put(corr)

	connector.publish = func(_ context.Context, pub *paho.Publish) (*paho.PublishResponse, error) {
		key := replyKey(env.CommandID, env.ExecutionFence)
		connector.mu.Lock()
		waiter := connector.waiters[key]
		connector.mu.Unlock()
		waiter <- corr
		return &paho.PublishResponse{}, nil
	}
	if _, execErr := connector.Execute(context.Background(), env); execErr == nil {
		t.Fatal("expected Execute to propagate EvidenceStore.Complete error")
	}

	if recErr := connector.ResolveCorrelationReply(context.Background(), corr); recErr == nil {
		t.Fatal("expected ResolveCorrelationReply to propagate EvidenceStore.Complete error")
	}
	intentAfterErr, err := service.Get(sub.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if intentAfterErr.Status != commandmodel.IntentDispatching || len(intentAfterErr.Attempts) == 0 || intentAfterErr.Attempts[0].Status != commandmodel.AttemptPrepared {
		t.Fatalf("EvidenceStore failure froze or mutated intent: status=%s attempts=%#v", intentAfterErr.Status, intentAfterErr.Attempts)
	}
	remaining, err := transport.RecoverCommandReplies(context.Background(), "tenant-1", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected REPLIED correlation preserved for retry, got %d", len(remaining))
	}

	evidenceStore.setError(nil)
	if recErr := connector.DrainTenantReplies(context.Background(), "tenant-1", 50); recErr != nil {
		t.Fatalf("expected recovery after EvidenceStore healed, got %v", recErr)
	}
	intentHealed, err := service.Get(sub.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(intentHealed.Attempts) == 0 || intentHealed.Attempts[0].Status != commandmodel.AttemptAcknowledged {
		t.Fatalf("healed attempts=%#v want=%s", intentHealed.Attempts, commandmodel.AttemptAcknowledged)
	}
}

type panickingTenantsTransportState struct {
	fakeTransportState
}

func (s *panickingTenantsTransportState) Tenants(context.Context) ([]string, error) {
	panic("simulated transport failure")
}

func TestExecuteEmitsNormalizedTraceIDInMQTTEnvelope(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	service := commandservice.New(func() time.Time { return now })
	transport := &routeAndReplyTransportState{
		fakeTransportState: fakeTransportState{correlations: make(map[string]commandmodel.CommandCorrelation)},
		route:              commandmodel.DeviceRoute{GatewayID: "01900000-0000-7000-8000-000000000100", ExternalDeviceID: "CHILLER-01", BindingRevision: 1},
	}
	connector, err := NewOffline(Config{
		TransportState: transport,
		EvidenceStore:  &failingEvidenceStore{},
		ReplyTimeout:   25 * time.Second,
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	submitAndInspect := func(key, rawTraceID string) map[string]any {
		t.Helper()
		current := 23.0
		sub, err := service.Submit(commandmodel.SubmitRequest{
			TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1", PointID: "point-1",
			PrincipalID: "principal-1", IdempotencyKey: key, TraceID: rawTraceID,
			Capability: commandmodel.CapabilitySetTemperatureSetpoint, Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24},
			VerificationPointKey: "zone.temperature_setpoint",
			CurrentState: commandmodel.CurrentStateEvidence{
				EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Quality: "GOOD",
				BusinessRevision: 17, CurrentValue: &current, ObservedAt: now,
			},
			Authorization: commandmodel.AuthorizationSnapshot{
				GrantID: "grant-" + key, PolicyRevision: "command-policy-1", Purpose: commandmodel.AuthorizationCommandSubmit,
				PrincipalID: "principal-1", TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1",
				Capability: commandmodel.CapabilitySetTemperatureSetpoint, MaximumRisk: commandmodel.RiskHigh,
				CapabilityRevision: "capability:set-temperature-setpoint:v1", EmergencyRevocationRevision: 1,
				IssuedAt: now.Add(-5 * time.Second), ExpiresAt: now.Add(25 * time.Second),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		env, err := service.PrepareDispatch(sub.Intent.ID, "dispatcher-a", now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		env.LeaseOwner = "dispatcher-a"
		env.LeaseUntil = now.Add(time.Minute)
		var publishedPayload []byte
		connector.publish = func(_ context.Context, pub *paho.Publish) (*paho.PublishResponse, error) {
			publishedPayload = append([]byte(nil), pub.Payload...)
			key := replyKey(env.CommandID, env.ExecutionFence)
			connector.mu.Lock()
			waiter := connector.waiters[key]
			connector.mu.Unlock()
			waiter <- commandmodel.CommandCorrelation{
				Envelope: env, GatewayID: "01900000-0000-7000-8000-000000000100", ExternalDeviceID: "CHILLER-01",
				State: commandmodel.CorrelationReplied, ReplyStatus: "EXECUTED", ReplyEventTime: now, RepliedAt: now,
				EdgeExecution: &commandmodel.EdgeExecutionEvidence{
					Requested: commandmodel.NumberScalar(24), Effective: func() *commandmodel.ScalarValue { v := commandmodel.NumberScalar(24); return &v }(),
					Applied:            func() *commandmodel.ScalarValue { v := commandmodel.NumberScalar(24); return &v }(),
					WinnerControllerID: "cloud-command-intent", Cycle: 1,
				},
			}
			return &paho.PublishResponse{}, nil
		}
		res, execErr := connector.Execute(context.Background(), env)
		if execErr != nil {
			t.Fatalf("Execute failed: %v", execErr)
		}
		if err := service.ResolveDispatch(env, res); err != nil {
			t.Fatalf("ResolveDispatch failed: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(publishedPayload, &decoded); err != nil {
			t.Fatalf("decode MQTT payload failed: %v", err)
		}
		return decoded
	}

	withTrace := submitAndInspect("mqtt-trace-valid", " 4BF92F3577B34DA6A3CE929D0E0E4736 ")
	if got, _ := withTrace["traceId"].(string); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("MQTT payload traceId=%q, want 4bf92f3577b34da6a3ce929d0e0e4736", got)
	}

	withInvalidTrace := submitAndInspect("mqtt-trace-invalid", "00000000000000000000000000000000")
	if _, exists := withInvalidTrace["traceId"]; exists {
		t.Fatalf("expected invalid all-zero traceId to be omitted, got %#v", withInvalidTrace["traceId"])
	}
}

func TestRecoverRepliesRecoversFromPanic(t *testing.T) {
	service := commandservice.New(nil)
	transport := &panickingTenantsTransportState{
		fakeTransportState: fakeTransportState{correlations: make(map[string]commandmodel.CommandCorrelation)},
	}
	connector, err := NewOffline(Config{
		TransportState: transport,
		EvidenceStore:  &failingEvidenceStore{},
		LateResultSink: fakeLateResultSink{service: service},
		ReplyTimeout:   25 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	connector.recoverReplies(context.Background())
	if !connector.recoverMu.TryLock() {
		t.Fatal("expected recoverMu to be unlocked after panic recovery")
	}
	connector.recoverMu.Unlock()
}
