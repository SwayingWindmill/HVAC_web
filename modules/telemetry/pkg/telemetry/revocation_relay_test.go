package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

type recordingRevocationSource struct {
	facts []RevocationFact
	polls []RevocationCursor
}

func (source *recordingRevocationSource) PollRevocations(_ context.Context, tenantID string, afterSequence int64, _ int) ([]RevocationFact, error) {
	source.polls = append(source.polls, RevocationCursor{TenantID: tenantID, AfterSequence: afterSequence})
	pending := []RevocationFact{}
	for _, fact := range source.facts {
		if fact.TenantID == tenantID && fact.Sequence > afterSequence {
			pending = append(pending, fact)
		}
	}
	return pending, nil
}

type memoryRevocationCursors struct {
	cursors map[string]int64
}

func (store *memoryRevocationCursors) RevocationCursors(context.Context, time.Time) ([]RevocationCursor, error) {
	result := []RevocationCursor{}
	for tenantID, sequence := range store.cursors {
		result = append(result, RevocationCursor{TenantID: tenantID, AfterSequence: sequence})
	}
	return result, nil
}

func (store *memoryRevocationCursors) SaveRevocationCursor(_ context.Context, tenantID string, sequence int64, _ time.Time) error {
	store.cursors[tenantID] = sequence
	return nil
}

func TestRevocationRelayWithdrawsOnlyEarlierAuthorizations(t *testing.T) {
	now := time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)
	repository := NewMemoryRealtimeRepository()
	transport := &RecordingRealtimeTransport{}
	service := newRealtimeTestService(t, repository, transport, &now)
	access := realtimeTestAccess()
	bootstrap, err := service.Bootstrap(context.Background(), access, telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []telemetryapi.SubscriptionTargetRequest{
		{ClientSubscriptionId: "zone-a", DeviceId: realtimeTestDevice1, Keys: []telemetryapi.TelemetryKey{"temperature"}},
	}})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	channel := string(bootstrap.Subscriptions[0].Channel)

	source := &recordingRevocationSource{facts: []RevocationFact{
		// A role change IAM already reflected when it authorized this subscription.
		{Sequence: 7, TenantID: access.TenantID, PrincipalID: access.PrincipalID, OccurredAt: now.Add(-time.Minute)},
	}}
	cursors := &memoryRevocationCursors{cursors: map[string]int64{access.TenantID: 0}}
	relay, err := NewRevocationRelay(source, cursors, service, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if revoked, err := relay.RelayOnce(context.Background()); err != nil || revoked != 0 {
		t.Fatalf("earlier IAM change revoked a later authorization: count=%d err=%v", revoked, err)
	}
	if _, err := service.AuthorizeSubscribe(context.Background(), access.PrincipalID, channel); err != nil {
		t.Fatalf("subscription authorized after the change was rejected: %v", err)
	}
	if cursors.cursors[access.TenantID] != 7 {
		t.Fatalf("cursor did not advance past the applied fact: %d", cursors.cursors[access.TenantID])
	}

	source.facts = append(source.facts, RevocationFact{Sequence: 9, TenantID: access.TenantID, PrincipalID: access.PrincipalID, DeviceID: realtimeTestDevice1, OccurredAt: now.Add(time.Second)})
	if revoked, err := relay.RelayOnce(context.Background()); err != nil || revoked != 1 || len(transport.Unsubscribes) != 1 {
		t.Fatalf("later IAM change did not end the stream: count=%d unsubscribes=%d err=%v", revoked, len(transport.Unsubscribes), err)
	}
	if _, err := service.AuthorizeSubscribe(context.Background(), access.PrincipalID, channel); err == nil {
		t.Fatal("revoked subscription remained subscribable")
	}

	source.facts = append(source.facts, RevocationFact{Sequence: 8, TenantID: access.TenantID, PrincipalID: access.PrincipalID, OccurredAt: now})
	cursors.cursors[access.TenantID] = 7
	if _, err := relay.RelayOnce(context.Background()); err == nil || cursors.cursors[access.TenantID] != 7 {
		t.Fatalf("out-of-order facts were applied: cursor=%d err=%v", cursors.cursors[access.TenantID], err)
	}
}

func TestHTTPRevocationSourceReadsIAMFacts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var input map[string]any
		if request.URL.Path != iamRevocationPollPath || json.NewDecoder(request.Body).Decode(&input) != nil || input["tenantId"] != realtimeTestOrg || input["afterSequence"] != float64(3) {
			http.Error(writer, "unexpected request", http.StatusBadRequest)
			return
		}
		_, _ = writer.Write([]byte(`{"facts":[{"sequence":4,"principalId":"` + realtimeTestPrincipal + `","tenantId":"` + realtimeTestOrg + `","sourceType":"ROLE_BINDING","sourceId":"018f2e00-9000-7000-8000-000000000001","action":"telemetry.*","policyRevision":"telemetry-access:2","reasonCode":"ROLE_BINDING_CHANGED","occurredAt":"2026-07-24T15:00:00.123Z"}],"nextSequence":4}`))
	}))
	defer server.Close()
	source, err := NewHTTPRevocationSource(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	facts, err := source.PollRevocations(context.Background(), realtimeTestOrg, 3, 50)
	if err != nil || len(facts) != 1 {
		t.Fatalf("facts=%+v err=%v", facts, err)
	}
	// The instant is reported at millisecond resolution and is rounded up.
	if want := time.Date(2026, 7, 24, 15, 0, 0, 124_000_000, time.UTC); facts[0].Sequence != 4 || facts[0].PrincipalID != realtimeTestPrincipal || facts[0].DeviceID != "" || !facts[0].OccurredAt.Equal(want) {
		t.Fatalf("unexpected fact %+v", facts[0])
	}
}
