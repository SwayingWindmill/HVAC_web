package core

import (
	"context"
	"errors"
	"testing"
)

type spyTemplateDriverClient struct {
	calls []struct {
		gatewayID string
		req       RelayGatewayTemplateDriverRequest
	}
}

func (s *spyTemplateDriverClient) UpsertGatewayTemplateDriver(_ context.Context, gatewayID string, req RelayGatewayTemplateDriverRequest) error {
	s.calls = append(s.calls, struct {
		gatewayID string
		req       RelayGatewayTemplateDriverRequest
	}{gatewayID: gatewayID, req: req})
	return nil
}

func TestDeliverBindingClosedAndReboundUnit(t *testing.T) {
	t.Parallel()

	client := &spyTemplateDriverClient{}
	relay := &OutboxRelay{connectivityDriver: client}

	payloadErr := errors.New("decode outbox event payload: bad json")
	if err := relay.deliverEvent(context.Background(), claimedOutboxEvent{PayloadErr: payloadErr}); !errors.Is(err, payloadErr) {
		t.Fatalf("expected deliverEvent to return PayloadErr, got %v", err)
	}

	err := relay.deliverBindingClosed(context.Background(), claimedOutboxEvent{
		EventID:   "evt-closed-1",
		TenantID:  "0193f000-0000-7000-8000-000000000001",
		EventType: "registry.binding.closed",
		Payload: map[string]any{
			"kind":      string(BindingGatewaySourceKey),
			"gatewayId": "0193f000-1000-7000-8000-000000000010",
			"sourceKey": "chiller-01",
		},
	})
	if err != nil {
		t.Fatalf("deliverBindingClosed failed: %v", err)
	}
	if len(client.calls) != 1 || !client.calls[0].req.Remove || client.calls[0].req.DeviceKey != "chiller-01" {
		t.Fatalf("unexpected deliverBindingClosed calls: %+v", client.calls)
	}

	if err := relay.deliverBindingClosed(context.Background(), claimedOutboxEvent{
		EventID:   "evt-closed-bad",
		TenantID:  "0193f000-0000-7000-8000-000000000001",
		EventType: "registry.binding.closed",
		Payload:   map[string]any{"kind": string(BindingGatewaySourceKey)},
	}); err == nil {
		t.Fatal("expected malformed registry.binding.closed payload to error")
	}

	prev := extractPreviousGatewayBindings(map[string]any{
		"previousBindings": []any{
			map[string]any{"gatewayId": "gw-a", "sourceKey": "old-key"},
			map[string]any{"gatewayId": "gw-a", "sourceKey": "old-key"},
			map[string]any{"gatewayId": "gw-b", "sourceKey": "other-key"},
		},
	})
	if len(prev) != 2 || prev[0].GatewayID != "gw-a" || prev[1].GatewayID != "gw-b" {
		t.Fatalf("unexpected deduplicated previousBindings: %+v", prev)
	}
}
