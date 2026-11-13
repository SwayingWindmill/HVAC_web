package mqttconnector

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

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
