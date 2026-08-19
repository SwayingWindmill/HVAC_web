package outbounddelivery

import (
	"context"
	"testing"
	"time"
)

type recordingStore struct {
	events *[]string
	claim  *ClaimedDelivery
	result AdapterResult
}

func (store *recordingStore) RecoverExpired(context.Context, Scope, time.Time) (int, error) {
	*store.events = append(*store.events, "recover")
	return 0, nil
}

func (store *recordingStore) ClaimNext(context.Context, Scope, string, time.Time, time.Duration) (*ClaimedDelivery, error) {
	*store.events = append(*store.events, "claim-committed")
	return store.claim, nil
}

func (store *recordingStore) CompleteAttempt(_ context.Context, _ Scope, _, _ string, result AdapterResult, _ time.Time) error {
	*store.events = append(*store.events, "complete")
	store.result = result
	return nil
}

type recordingAdapter struct {
	events *[]string
}

func (adapter recordingAdapter) Deliver(context.Context, ClaimedDelivery) AdapterResult {
	*adapter.events = append(*adapter.events, "external-effect")
	return AdapterResult{Outcome: OutcomeDelivered}
}

func TestWorkerPersistsClaimBeforeExternalEffect(t *testing.T) {
	events := []string{}
	store := &recordingStore{
		events: &events,
		claim: &ClaimedDelivery{
			Attempt:     DeliveryAttempt{ID: "attempt-1"},
			Integration: IntegrationDefinition{AdapterType: AdapterRESTWebhook},
		},
	}
	worker, err := NewWorker(store, map[AdapterType]Adapter{
		AdapterRESTWebhook: recordingAdapter{events: &events},
	}, "worker-1", time.Minute, func() time.Time { return time.Date(2026, 8, 19, 4, 30, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}

	processed, err := worker.ProcessOne(context.Background(), Scope{TenantID: "tenant-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !processed {
		t.Fatal("expected one claimed delivery")
	}
	want := []string{"recover", "claim-committed", "external-effect", "complete"}
	if len(events) != len(want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events = %#v, want %#v", events, want)
		}
	}
	if store.result.Outcome != OutcomeDelivered {
		t.Fatalf("completion result = %#v", store.result)
	}
}
