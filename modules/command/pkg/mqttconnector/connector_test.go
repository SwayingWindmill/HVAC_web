package mqttconnector

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/packets"
	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
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

func TestSharedSubscriptionConfigAndReplyGatewayID(t *testing.T) {
	const validGW = "01900000-0000-7000-8000-000000000101"

	base := Config{
		BrokerURL:      "tls://mqtt-broker:8883",
		ClientID:       "connectivity-commands-pod-1",
		CAFile:         "/tmp/ca.crt",
		CertFile:       "/tmp/client.crt",
		KeyFile:        "/tmp/client.key",
		ServerName:     "mqtt-broker",
		TransportState: &fakeTransportState{correlations: map[string]commandmodel.CommandCorrelation{}},
		EvidenceStore:  fakeEvidenceStore{},
		ReplyTimeout:   15 * time.Second,
	}
	if err := validateConfig(base); err != nil {
		t.Fatalf("plain config invalid: %v", err)
	}
	if got := base.SubscriptionTopicFilter(); got != ReplyTopicFilter {
		t.Fatalf("SubscriptionTopicFilter() = %q, want %q", got, ReplyTopicFilter)
	}

	shared := base
	shared.SharedSubscriptionGroup = "command-dispatcher_1"
	if err := validateConfig(shared); err != nil {
		t.Fatalf("shared config invalid: %v", err)
	}
	wantFilter := "$share/command-dispatcher_1/" + ReplyTopicFilter
	if got := shared.SubscriptionTopicFilter(); got != wantFilter {
		t.Fatalf("shared SubscriptionTopicFilter() = %q, want %q", got, wantFilter)
	}

	for _, badGroup := range []string{"bad/group", "bad+group", " has-space", strings.Repeat("a", 65)} {
		cfg := base
		cfg.SharedSubscriptionGroup = badGroup
		if err := validateConfig(cfg); err == nil {
			t.Fatalf("expected invalid SharedSubscriptionGroup %q to fail validation", badGroup)
		}
	}

	for _, validTopic := range []string{
		"hvac/v1/" + validGW + "/up/reply",
		"$share/command-dispatcher/hvac/v1/" + validGW + "/up/reply",
	} {
		got, ok := replyGatewayID(validTopic)
		if !ok || got != validGW {
			t.Fatalf("replyGatewayID(%q) = (%q, %v), want (%q, true)", validTopic, got, ok, validGW)
		}
	}

	for _, invalidTopic := range []string{
		"hvac/v1/" + validGW + "/up/telemetry",
		"$share//hvac/v1/" + validGW + "/up/reply",
		"$share/group/hvac/v1/not-a-uuid/up/reply",
		"hvac/v1/ " + validGW + "/up/reply",
	} {
		if got, ok := replyGatewayID(invalidTopic); ok {
			t.Fatalf("replyGatewayID(%q) = (%q, true), want false", invalidTopic, got)
		}
	}
}

type fakeCommandBroker struct {
	listener    net.Listener
	conns       chan *fakeBrokerConn
	baseConfig  Config
	connCounter atomic.Int32
	onSubscribe func(connIndex int, sub *packets.Subscribe) []byte
}

type fakeBrokerConn struct {
	conn          net.Conn
	writeMu       sync.Mutex
	subOnce       sync.Once
	subscribed    chan struct{}
	subscriptions []string
	acks          chan uint16
	disconnected  chan struct{}
}

func newFakeCommandBroker(t *testing.T, onSubscribe func(connIndex int, sub *packets.Subscribe) []byte) *fakeCommandBroker {
	t.Helper()
	cfg, serverTLS := writeCommandTestPKI(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	cfg.BrokerURL = "tls://" + listener.Addr().String()
	cfg.ClientID = "connectivity-commands-test"
	broker := &fakeCommandBroker{
		listener:    listener,
		conns:       make(chan *fakeBrokerConn, 16),
		baseConfig:  cfg,
		onSubscribe: onSubscribe,
	}
	t.Cleanup(func() { _ = listener.Close() })
	go broker.accept()
	return broker
}

func (broker *fakeCommandBroker) accept() {
	for {
		conn, err := broker.listener.Accept()
		if err != nil {
			return
		}
		idx := int(broker.connCounter.Add(1))
		go broker.serve(conn, idx)
	}
}

func (broker *fakeCommandBroker) serve(conn net.Conn, connIndex int) {
	connect, err := packets.ReadPacket(conn)
	if err != nil || connect.Type != packets.CONNECT {
		_ = conn.Close()
		return
	}
	client := &fakeBrokerConn{
		conn:         conn,
		subscribed:   make(chan struct{}),
		acks:         make(chan uint16, 128),
		disconnected: make(chan struct{}),
	}
	defer close(client.disconnected)
	connack := packets.NewControlPacket(packets.CONNACK)
	connack.Content.(*packets.Connack).SessionPresent = true
	if err := client.write(connack); err != nil {
		return
	}
	broker.conns <- client
	for {
		received, err := packets.ReadPacket(conn)
		if err != nil {
			return
		}
		switch received.Type {
		case packets.SUBSCRIBE:
			subscribe := received.Content.(*packets.Subscribe)
			var reasons []byte
			if broker.onSubscribe != nil {
				reasons = broker.onSubscribe(connIndex, subscribe)
			} else {
				reasons = make([]byte, len(subscribe.Subscriptions))
				for i := range reasons {
					reasons[i] = 1
				}
			}
			suback := packets.NewControlPacket(packets.SUBACK)
			suback.Content.(*packets.Suback).PacketID = subscribe.PacketID
			suback.Content.(*packets.Suback).Reasons = reasons
			for _, sub := range subscribe.Subscriptions {
				client.subscriptions = append(client.subscriptions, sub.Topic)
			}
			_ = client.write(suback)
			client.subOnce.Do(func() { close(client.subscribed) })
		case packets.PINGREQ:
			_ = client.write(packets.NewControlPacket(packets.PINGRESP))
		case packets.PUBACK:
			packetID := received.Content.(*packets.Puback).PacketID
			client.acks <- packetID
		}
	}
}

func (broker *fakeCommandBroker) awaitConnection(t *testing.T) *fakeBrokerConn {
	t.Helper()
	select {
	case client := <-broker.conns:
		select {
		case <-client.subscribed:
			return client
		case <-time.After(3 * time.Second):
			t.Fatal("command connector connected but never subscribed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("command connector never connected to broker")
	}
	return nil
}

func (client *fakeBrokerConn) write(packet *packets.ControlPacket) error {
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	_, err := packet.WriteTo(client.conn)
	return err
}

func (client *fakeBrokerConn) deliver(t *testing.T, packetID uint16, topic string, payload []byte, redelivered bool) {
	t.Helper()
	pkt := packets.NewControlPacket(packets.PUBLISH)
	pub := pkt.Content.(*packets.Publish)
	pub.Topic = topic
	pub.Payload = payload
	pub.QoS = 1
	pub.PacketID = packetID
	pub.Duplicate = redelivered
	if err := client.write(pkt); err != nil {
		t.Fatalf("broker write publish packet %d: %v", packetID, err)
	}
}

func (client *fakeBrokerConn) awaitAcks(t *testing.T, timeout time.Duration, packetIDs ...uint16) {
	t.Helper()
	pending := make(map[uint16]bool, len(packetIDs))
	for _, id := range packetIDs {
		pending[id] = true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for len(pending) > 0 {
		select {
		case id := <-client.acks:
			delete(pending, id)
		case <-timer.C:
			t.Fatalf("broker timed out waiting for PUBACK for packets %v", pending)
		}
	}
}

func (client *fakeBrokerConn) ensureNoAck(t *testing.T, wait time.Duration) {
	t.Helper()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case id := <-client.acks:
		t.Fatalf("unexpected PUBACK received for packet %d", id)
	case <-timer.C:
	}
}

func (client *fakeBrokerConn) awaitDisconnect(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-client.disconnected:
	case <-time.After(timeout):
		t.Fatal("broker connection stayed open instead of disconnecting")
	}
}

func writeCommandTestPKI(t *testing.T) (Config, *tls.Config) {
	t.Helper()
	dir := t.TempDir()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	issue := func(serial int64, name string, usage x509.ExtKeyUsage) tls.Certificate {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
		}
		der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}
	writePEM := func(name, kind string, der []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	client := issue(2, "connectivity-test", x509.ExtKeyUsageClientAuth)
	clientKey, _ := x509.MarshalECPrivateKey(client.PrivateKey.(*ecdsa.PrivateKey))
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	return Config{
		CAFile:     writePEM("ca.pem", "CERTIFICATE", caDER),
		CertFile:   writePEM("client.pem", "CERTIFICATE", client.Certificate[0]),
		KeyFile:    writePEM("client.key", "EC PRIVATE KEY", clientKey),
		ServerName: "mqtt.local",
	}, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{issue(3, "mqtt.local", x509.ExtKeyUsageServerAuth)},
		ClientCAs:    roots,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
}

type hookTransportState struct {
	fakeTransportState
	gatewayTenantCalls atomic.Int32
	recordCalls        atomic.Int32
	onGatewayTenant    func(ctx context.Context, gatewayID string) (string, error)
	onRecordReply      func(ctx context.Context, call int32, tenantID, gatewayID, commandID string, executionFence uint64) (commandmodel.CommandCorrelation, error)
}

func (s *hookTransportState) GatewayTenant(ctx context.Context, gatewayID string) (string, error) {
	s.gatewayTenantCalls.Add(1)
	if s.onGatewayTenant != nil {
		return s.onGatewayTenant(ctx, gatewayID)
	}
	return "tenant-1", nil
}

func (s *hookTransportState) RecordCommandReply(
	ctx context.Context,
	tenantID, gatewayID, commandID string,
	executionFence uint64,
	replySHA256, replyStatus string,
	replyEventTime time.Time,
	replyReasonCode string,
	edgeExecution *commandmodel.EdgeExecutionEvidence,
	repliedAt time.Time,
) (commandmodel.CommandCorrelation, error) {
	call := s.recordCalls.Add(1)
	if s.onRecordReply != nil {
		return s.onRecordReply(ctx, call, tenantID, gatewayID, commandID, executionFence)
	}
	return commandmodel.CommandCorrelation{
		Envelope: commandmodel.DispatchEnvelope{
			TenantID: tenantID, CommandID: commandID, AttemptID: "attempt-" + commandID, ExecutionFence: executionFence,
		},
		GatewayID:       gatewayID,
		State:           commandmodel.CorrelationReplied,
		ReplySHA256:     replySHA256,
		ReplyStatus:     replyStatus,
		ReplyEventTime:  replyEventTime,
		ReplyReasonCode: replyReasonCode,
		EdgeExecution:   edgeExecution,
		RepliedAt:       repliedAt,
	}, nil
}

type hookLateResultSink struct {
	onResolve func(ctx context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error
}

func (s hookLateResultSink) ResolveDispatch(ctx context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error {
	if s.onResolve != nil {
		return s.onResolve(ctx, envelope, result)
	}
	return nil
}

func validTerminalReplyJSON(t *testing.T, commandID string, fence uint64, eventTimeMs int64) []byte {
	t.Helper()
	val := commandmodel.NumberScalar(24)
	body, err := json.Marshal(commandReply{
		SchemaVersion:  commandSchemaVersion,
		MessageID:      "msg-" + commandID,
		CommandID:      commandID,
		EventTime:      eventTimeMs,
		EdgeStatus:     "EXECUTED",
		ExecutionFence: fence,
		ExecutionEvidence: &commandmodel.EdgeExecutionEvidence{
			Requested:          val,
			Effective:          &val,
			Applied:            &val,
			WinnerControllerID: "cloud-command-intent",
			Cycle:              1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func scrapeMetricsText(t *testing.T, reg *observability.Registry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

func TestConnectorTransientRecordReplyFailureDoesNotPubackAndClosesThenRecovers(t *testing.T) {
	const gwID = "01900000-0000-7000-8000-000000000201"
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	metrics := observability.NewRegistry()
	transport := &hookTransportState{
		fakeTransportState: fakeTransportState{correlations: map[string]commandmodel.CommandCorrelation{}},
		onRecordReply: func(_ context.Context, call int32, tenantID, gatewayID, commandID string, executionFence uint64) (commandmodel.CommandCorrelation, error) {
			if call <= 3 {
				return commandmodel.CommandCorrelation{}, errors.New("transient database timeout")
			}
			return commandmodel.CommandCorrelation{
				Envelope: commandmodel.DispatchEnvelope{
					TenantID: tenantID, CommandID: commandID, AttemptID: "att-1", ExecutionFence: executionFence,
				},
				GatewayID: gatewayID, State: commandmodel.CorrelationReplied, ReplyStatus: "EXECUTED", ReplyEventTime: now, RepliedAt: now,
			}, nil
		},
	}

	broker := newFakeCommandBroker(t, nil)
	cfg := broker.baseConfig
	cfg.TransportState = transport
	cfg.EvidenceStore = fakeEvidenceStore{}
	cfg.SharedSubscriptionGroup = "command-dispatcher"
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg.Metrics = metrics
	cfg.Now = func() time.Time { return now }

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	connector, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		_ = connector.Disconnect(shutdownCtx)
	})

	conn1 := broker.awaitConnection(t)
	if len(conn1.subscriptions) != 1 || conn1.subscriptions[0] != "$share/command-dispatcher/"+ReplyTopicFilter {
		t.Fatalf("conn1 subscriptions=%v", conn1.subscriptions)
	}
	payload := validTerminalReplyJSON(t, "cmd-transient-1", 1, now.UnixMilli())
	conn1.deliver(t, 1, "hvac/v1/"+gwID+"/up/reply", payload, false)
	conn1.awaitDisconnect(t, 3*time.Second)
	conn1.ensureNoAck(t, 50*time.Millisecond)

	conn2 := broker.awaitConnection(t)
	conn2.deliver(t, 2, "hvac/v1/"+gwID+"/up/reply", payload, true)
	conn2.awaitAcks(t, 3*time.Second, 2)

	body := scrapeMetricsText(t, metrics)
	for _, want := range []string{
		`hvac_mqtt_command_replies_total{outcome="transient_error"} 1`,
		`hvac_mqtt_command_replies_total{outcome="persisted"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing metric %q in:\n%s", want, body)
		}
	}
}

func TestConnectorPermanentUnmatchedReplyPubacksWithoutClosingConnection(t *testing.T) {
	const gwID = "01900000-0000-7000-8000-000000000202"
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	metrics := observability.NewRegistry()
	transport := &hookTransportState{
		fakeTransportState: fakeTransportState{correlations: map[string]commandmodel.CommandCorrelation{}},
		onRecordReply: func(_ context.Context, call int32, _, _, _ string, _ uint64) (commandmodel.CommandCorrelation, error) {
			if call == 1 {
				return commandmodel.CommandCorrelation{}, fmt.Errorf("lookup failed: %w", commandmodel.ErrCommandCorrelationNotFound)
			}
			return commandmodel.CommandCorrelation{}, commandmodel.ErrCommandCorrelationMismatch
		},
	}

	broker := newFakeCommandBroker(t, nil)
	cfg := broker.baseConfig
	cfg.TransportState = transport
	cfg.EvidenceStore = fakeEvidenceStore{}
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg.Metrics = metrics
	cfg.Now = func() time.Time { return now }

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	connector, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		_ = connector.Disconnect(shutdownCtx)
	})

	conn := broker.awaitConnection(t)
	conn.deliver(t, 10, "hvac/v1/"+gwID+"/up/reply", validTerminalReplyJSON(t, "cmd-missing", 1, now.UnixMilli()), false)
	conn.deliver(t, 11, "hvac/v1/"+gwID+"/up/reply", validTerminalReplyJSON(t, "cmd-mismatch", 2, now.UnixMilli()), false)
	conn.awaitAcks(t, 3*time.Second, 10, 11)

	select {
	case <-conn.disconnected:
		t.Fatal("permanent domain rejection must not close MQTT connection")
	case <-time.After(100 * time.Millisecond):
	}
	if calls := transport.recordCalls.Load(); calls != 2 {
		t.Fatalf("RecordCommandReply calls=%d, want 2 (no transient retries on permanent errors)", calls)
	}
	body := scrapeMetricsText(t, metrics)
	if !strings.Contains(body, `hvac_mqtt_command_replies_total{outcome="unmatched"} 2`) {
		t.Fatalf("missing unmatched metric in:\n%s", body)
	}
}

func TestConnectorOversizedTrailingAndNonReplyPacketsPubackWithoutDBOrWedge(t *testing.T) {
	const gwID = "01900000-0000-7000-8000-000000000203"
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	metrics := observability.NewRegistry()
	transport := &hookTransportState{
		fakeTransportState: fakeTransportState{correlations: map[string]commandmodel.CommandCorrelation{}},
	}

	broker := newFakeCommandBroker(t, nil)
	cfg := broker.baseConfig
	cfg.TransportState = transport
	cfg.EvidenceStore = fakeEvidenceStore{}
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg.Metrics = metrics
	cfg.Now = func() time.Time { return now }

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	connector, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		_ = connector.Disconnect(shutdownCtx)
	})

	conn := broker.awaitConnection(t)
	oversized := bytes.Repeat([]byte("x"), maxCommandReplyPayloadBytes+1)
	valid := validTerminalReplyJSON(t, "cmd-trailing", 1, now.UnixMilli())
	trailingJSON := append(append([]byte(nil), valid...), []byte(`{"extra":1}`)...)
	writingProgress, _ := json.Marshal(commandReply{
		SchemaVersion: commandSchemaVersion, MessageID: "msg-prog", CommandID: "cmd-prog",
		EventTime: now.UnixMilli(), EdgeStatus: "WRITING", ExecutionFence: 1,
	})
	emptyCmdID, _ := json.Marshal(commandReply{
		SchemaVersion: commandSchemaVersion, MessageID: "msg-empty", CommandID: "",
		EventTime: now.UnixMilli(), EdgeStatus: "FAILED", ExecutionFence: 1,
	})

	conn.deliver(t, 20, "hvac/v1/"+gwID+"/up/reply", oversized, false)
	conn.deliver(t, 21, "hvac/v1/"+gwID+"/up/reply", trailingJSON, false)
	conn.deliver(t, 22, "hvac/v1/"+gwID+"/up/telemetry", []byte(`{"ignored":true}`), false)
	conn.deliver(t, 23, "hvac/v1/"+gwID+"/up/reply", writingProgress, false)
	conn.deliver(t, 24, "hvac/v1/"+gwID+"/up/reply", emptyCmdID, false)
	conn.deliver(t, 25, "hvac/v1/"+gwID+"/up/reply", validTerminalReplyJSON(t, "cmd-after-bad", 5, now.UnixMilli()), false)

	conn.awaitAcks(t, 3*time.Second, 20, 21, 22, 23, 24, 25)
	if calls := transport.gatewayTenantCalls.Load(); calls != 1 {
		t.Fatalf("GatewayTenant calls=%d, want 1 (packets 20-24 must bypass DB)", calls)
	}
	if calls := transport.recordCalls.Load(); calls != 1 {
		t.Fatalf("RecordCommandReply calls=%d, want 1", calls)
	}
	body := scrapeMetricsText(t, metrics)
	for _, want := range []string{
		`hvac_mqtt_command_replies_total{outcome="invalid"} 3`,
		`hvac_mqtt_command_replies_total{outcome="ignored"} 2`,
		`hvac_mqtt_command_replies_total{outcome="persisted"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing metric %q in:\n%s", want, body)
		}
	}
}

func TestConnectorSlowResolveOnGatewayADoesNotBlockGatewayBWaiter(t *testing.T) {
	const gwA = "01900000-0000-7000-8000-000000000301"
	var gwB string
	for i := 2; i < 100; i++ {
		candidate := fmt.Sprintf("01900000-0000-7000-8000-%012d", i+300)
		if replyShardIndex(candidate) != replyShardIndex(gwA) {
			gwB = candidate
			break
		}
	}
	if gwB == "" {
		t.Fatal("failed to pick distinct shard gateway B")
	}

	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	enteredA := make(chan struct{})
	releaseA := make(chan struct{})
	var closeAOnce sync.Once
	unlockA := func() {
		closeAOnce.Do(func() { close(releaseA) })
	}
	t.Cleanup(unlockA)

	transport := &hookTransportState{
		fakeTransportState: fakeTransportState{correlations: map[string]commandmodel.CommandCorrelation{}},
	}
	sink := hookLateResultSink{
		onResolve: func(_ context.Context, envelope commandmodel.DispatchEnvelope, _ commandmodel.ConnectorResult) error {
			if envelope.CommandID == "cmd-slow-a" {
				close(enteredA)
				<-releaseA
			}
			return nil
		},
	}

	broker := newFakeCommandBroker(t, nil)
	cfg := broker.baseConfig
	cfg.TransportState = transport
	cfg.EvidenceStore = fakeEvidenceStore{}
	cfg.LateResultSink = sink
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg.Metrics = observability.NewRegistry()
	cfg.Now = func() time.Time { return now }

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	connector, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		unlockA()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		_ = connector.Disconnect(shutdownCtx)
	})

	conn := broker.awaitConnection(t)
	waiterB := make(chan commandmodel.CommandCorrelation, 1)
	keyB := replyKey("cmd-fast-b", 2)
	connector.mu.Lock()
	connector.waiters[keyB] = waiterB
	connector.mu.Unlock()
	defer func() {
		connector.mu.Lock()
		delete(connector.waiters, keyB)
		connector.mu.Unlock()
	}()

	conn.deliver(t, 30, "hvac/v1/"+gwA+"/up/reply", validTerminalReplyJSON(t, "cmd-slow-a", 1, now.UnixMilli()), false)
	select {
	case <-enteredA:
	case <-time.After(3 * time.Second):
		t.Fatal("gateway A never reached slow ResolveDispatch")
	}
	// PUBACK for gateway A must happen right after REPLIED custody, before slow ResolveDispatch finishes.
	conn.awaitAcks(t, 2*time.Second, 30)

	conn.deliver(t, 31, "hvac/v1/"+gwB+"/up/reply", validTerminalReplyJSON(t, "cmd-fast-b", 2, now.UnixMilli()), false)
	select {
	case corr := <-waiterB:
		if corr.Envelope.CommandID != "cmd-fast-b" {
			t.Fatalf("unexpected waiter correlation: %+v", corr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gateway B waiter was blocked by slow ResolveCorrelationReply on gateway A")
	}
	conn.awaitAcks(t, 2*time.Second, 31)
}

func TestConnectorSuback0x87ClosesConnectionAndRecoversOnReconnect(t *testing.T) {
	metrics := observability.NewRegistry()
	broker := newFakeCommandBroker(t, func(connIndex int, sub *packets.Subscribe) []byte {
		reasons := make([]byte, len(sub.Subscriptions))
		for i := range reasons {
			if connIndex == 1 {
				reasons[i] = 0x87
			} else {
				reasons[i] = 1
			}
		}
		return reasons
	})

	cfg := broker.baseConfig
	cfg.TransportState = &hookTransportState{fakeTransportState: fakeTransportState{correlations: map[string]commandmodel.CommandCorrelation{}}}
	cfg.EvidenceStore = fakeEvidenceStore{}
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg.Metrics = metrics

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	connector, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		_ = connector.Disconnect(shutdownCtx)
	})

	conn1 := broker.awaitConnection(t)
	conn1.awaitDisconnect(t, 3*time.Second)

	_ = broker.awaitConnection(t)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		body := scrapeMetricsText(t, metrics)
		if strings.Contains(body, `hvac_mqtt_command_subscriptions_total{outcome="failed"} 1`) &&
			strings.Contains(body, `hvac_mqtt_command_subscriptions_total{outcome="success"} 1`) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected failed=1 and success=1 subscription metrics, got:\n%s", scrapeMetricsText(t, metrics))
}

func TestConnectorBackpressureShedClosesConnectionWithoutAck(t *testing.T) {
	const gwID = "01900000-0000-7000-8000-000000000401"
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unlock := func() {
		releaseOnce.Do(func() { close(release) })
	}
	t.Cleanup(unlock)

	metrics := observability.NewRegistry()
	var firstOnce sync.Once
	transport := &hookTransportState{
		fakeTransportState: fakeTransportState{correlations: map[string]commandmodel.CommandCorrelation{}},
		onGatewayTenant: func(_ context.Context, _ string) (string, error) {
			firstOnce.Do(func() {
				close(entered)
				<-release
			})
			return "tenant-1", nil
		},
	}

	broker := newFakeCommandBroker(t, nil)
	cfg := broker.baseConfig
	cfg.TransportState = transport
	cfg.EvidenceStore = fakeEvidenceStore{}
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg.Metrics = metrics
	cfg.Now = func() time.Time { return now }

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	connector, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		unlock()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		_ = connector.Disconnect(shutdownCtx)
	})

	conn := broker.awaitConnection(t)
	conn.deliver(t, 1, "hvac/v1/"+gwID+"/up/reply", validTerminalReplyJSON(t, "cmd-hold", 1, now.UnixMilli()), false)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not enter GatewayTenant")
	}

	for i := 0; i < replyShardQueueCapacity+1; i++ {
		handled, onErr := connector.onReply(paho.PublishReceived{
			Packet: &paho.Publish{
				Topic:   "hvac/v1/" + gwID + "/up/reply",
				Payload: validTerminalReplyJSON(t, fmt.Sprintf("cmd-fill-%03d", i), uint64(i+2), now.UnixMilli()),
			},
		})
		if !handled || onErr != nil {
			t.Fatalf("onReply(%d) = (%v, %v), want (true, nil)", i, handled, onErr)
		}
	}

	conn.awaitDisconnect(t, 3*time.Second)
	body := scrapeMetricsText(t, metrics)
	if !strings.Contains(body, `hvac_mqtt_command_replies_total{outcome="shed"} 1`) {
		t.Fatalf("missing shed metric in:\n%s", body)
	}
}
