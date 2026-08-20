package notificationservice

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/services/outbound-delivery-service/pkg/outbounddelivery"
)

const (
	schedulerTenantID = "0190f000-0005-7000-8000-000000000001"
	schedulerSiteID   = "01910000-0005-7000-8000-000000000001"
	replayTenantID   = "0190f000-0010-7000-8000-000000000001"
	replaySiteID     = "01910000-0010-7000-8000-000000000001"
	safetyTenantID   = "0190f000-0020-7000-8000-000000000001"
	safetySiteID     = "01910000-0020-7000-8000-000000000001"
	externalTenantID = "0190f000-0030-7000-8000-000000000001"
	externalSiteID   = "01910000-0030-7000-8000-000000000001"
)

func TestPostgresNotificationSchedulerClaimsAcrossTenantsAndTenantOwnerMaterializes(t *testing.T) {
	store := openNotificationTestStore(t)
	defer store.Close()
	databaseURL := os.Getenv("S16_NOTIFICATION_TEST_DATABASE_URL")
	scheduler, err := OpenScheduler(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer scheduler.Close()
	base := time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC)
	template := testTemplate("01910000-8105-7000-8000-000000000001", "01910000-8105-7000-8000-000000000002", ChannelInApp)
	audience := testAudienceWithPrincipal("01910000-8205-7000-8000-000000000001", "01910000-8205-7000-8000-000000000002", "principal:s16-scheduler")
	policy := singleStagePolicy("01910000-8305-7000-8000-000000000001", "01910000-8305-7000-8000-000000000002", "scheduler", audience, template, true, ChannelInApp, "")
	releaseNotificationFixture(t, store, schedulerTenantID, schedulerSiteID, template, audience, policy, "01910000-8405-7000-8000-000000000001", base)
	event := alarmEvent(schedulerTenantID, schedulerSiteID, "01910000-8505-7000-8000-000000000001", "01910000-8605-7000-8000-000000000001", "01910000-8705-7000-8000-000000000001", AlarmCreated, alarmmodel.ConditionActive, base)
	if _, err := store.ProcessAlarmEvent(context.Background(), event, base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	claim, err := scheduler.ClaimDue(context.Background(), "s16-cross-tenant-scheduler", base.Add(2*time.Second), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if claim.TenantID != schedulerTenantID || claim.SiteID != schedulerSiteID {
		t.Fatalf("cross-Tenant scheduler claimed the wrong durable stage: %#v", claim)
	}
	items, err := store.MaterializeInApp(context.Background(), *claim, base.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].TenantID != schedulerTenantID || items[0].PrincipalID != "principal:s16-scheduler" {
		t.Fatalf("Tenant owner did not materialize the scheduler claim in exact scope: %#v", items)
	}
}

func TestPostgresNotificationReplayFreezesSnapshotAndAckCancelsClaimedFutureStage(t *testing.T) {
	store := openNotificationTestStore(t)
	defer store.Close()
	ctx := context.Background()
	base := time.Date(2026, 8, 19, 16, 0, 0, 0, time.UTC)
	principal := "principal:s16-replay-operator"
	template := testTemplate("01910000-8110-7000-8000-000000000001", "01910000-8110-7000-8000-000000000002", ChannelInApp)
	audience := testAudienceWithPrincipal("01910000-8210-7000-8000-000000000001", "01910000-8210-7000-8000-000000000002", principal)
	policy := NotificationPolicyRevision{
		SchemaVersion: ArtifactSchemaVersion,
		PolicyID:      "01910000-8310-7000-8000-000000000001", PolicyRevisionID: "01910000-8310-7000-8000-000000000002", Revision: 1,
		Name: "frozen escalation", AlarmActions: []AlarmAction{AlarmCreated}, MandatorySafety: false,
		Stages: []EscalationStage{
			{Stage: 0, DelaySeconds: 0, AudienceRevisionID: audience.AudienceRevisionID, TemplateRevisionID: template.TemplateRevisionID, Channel: ChannelInApp},
			{Stage: 1, DelaySeconds: 600, AudienceRevisionID: audience.AudienceRevisionID, TemplateRevisionID: template.TemplateRevisionID, Channel: ChannelInApp},
		},
	}
	sealPolicy(&policy)
	releaseNotificationFixture(t, store, replayTenantID, replaySiteID, template, audience, policy, "01910000-8410-7000-8000-000000000001", base)

	created := alarmEvent(replayTenantID, replaySiteID, "01910000-8510-7000-8000-000000000001", "01910000-8610-7000-8000-000000000001", "01910000-8710-7000-8000-000000000001", AlarmCreated, alarmmodel.ConditionActive, base)
	intents, err := store.ProcessAlarmEvent(ctx, created, base.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 2 || intents[0].Stage != 0 || intents[1].Stage != 1 {
		t.Fatalf("expected one frozen intent per escalation stage, got %#v", intents)
	}
	replay, err := store.ProcessAlarmEvent(ctx, created, base.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(replay) != 2 || replay[0].IntentID != intents[0].IntentID || replay[1].IntentID != intents[1].IntentID {
		t.Fatalf("source event replay created a second notification business identity: first=%#v replay=%#v", intents, replay)
	}

	if _, err := store.SetAdvisoryPreference(ctx, replayTenantID, principal, ChannelInApp, false, base.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	claim0, err := store.ClaimDue(ctx, replayTenantID, "s16-worker-stage0", base.Add(4*time.Second), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.MaterializeInApp(ctx, *claim0, base.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].PrincipalID != principal {
		t.Fatalf("post-trigger preference edit changed the frozen recipient snapshot: %#v", items)
	}
	read, err := store.MarkRead(ctx, replayTenantID, principal, items[0].InboxItemID, base.Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if read.Status != InboxRead {
		t.Fatalf("notification Inbox read did not remain a Notification-local state change: %#v", read)
	}

	claim1, err := store.ClaimDue(ctx, replayTenantID, "s16-worker-stage1", base.Add(10*time.Minute), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if claim1.Stage != 1 {
		t.Fatalf("expected delayed escalation claim, got %#v", claim1)
	}
	ack := alarmEvent(replayTenantID, replaySiteID, "01910000-8510-7000-8000-000000000002", created.AlarmID, created.IncidentCorrelationID, AlarmAcknowledged, alarmmodel.ConditionActive, base.Add(10*time.Minute+time.Second))
	if _, err := store.ProcessAlarmEvent(ctx, ack, base.Add(10*time.Minute+2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MaterializeInApp(ctx, *claim1, base.Add(10*time.Minute+3*time.Second)); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("ACK did not fence the already-claimed future escalation stage: %v", err)
	}
	finalIntents, err := store.ListIntentsForSource(ctx, replayTenantID, created.SourceEventID)
	if err != nil {
		t.Fatal(err)
	}
	if finalIntents[1].Status != IntentCancelled || finalIntents[1].Disposition != DispositionCancelled {
		t.Fatalf("future escalation did not retain durable cancellation evidence: %#v", finalIntents[1])
	}
}

func TestPostgresMandatorySafetyIgnoresOrdinaryOptOut(t *testing.T) {
	store := openNotificationTestStore(t)
	defer store.Close()
	ctx := context.Background()
	base := time.Date(2026, 8, 19, 17, 0, 0, 0, time.UTC)
	principal := "principal:s16-safety-operator"
	if _, err := store.SetAdvisoryPreference(ctx, safetyTenantID, principal, ChannelInApp, false, base); err != nil {
		t.Fatal(err)
	}

	mandatoryTemplate := testTemplate("01910000-8120-7000-8000-000000000001", "01910000-8120-7000-8000-000000000002", ChannelInApp)
	mandatoryAudience := testAudienceWithPrincipal("01910000-8220-7000-8000-000000000001", "01910000-8220-7000-8000-000000000002", principal)
	mandatoryPolicy := singleStagePolicy("01910000-8320-7000-8000-000000000001", "01910000-8320-7000-8000-000000000002", "mandatory safety", mandatoryAudience, mandatoryTemplate, true, ChannelInApp, "")
	releaseNotificationFixture(t, store, safetyTenantID, safetySiteID, mandatoryTemplate, mandatoryAudience, mandatoryPolicy, "01910000-8420-7000-8000-000000000001", base.Add(time.Second))

	advisoryTemplate := testTemplate("01910000-8120-7000-8000-000000000011", "01910000-8120-7000-8000-000000000012", ChannelInApp)
	advisoryAudience := testAudienceWithPrincipal("01910000-8220-7000-8000-000000000011", "01910000-8220-7000-8000-000000000012", principal)
	advisoryPolicy := singleStagePolicy("01910000-8320-7000-8000-000000000011", "01910000-8320-7000-8000-000000000012", "advisory", advisoryAudience, advisoryTemplate, false, ChannelInApp, "")
	releaseNotificationFixture(t, store, safetyTenantID, safetySiteID, advisoryTemplate, advisoryAudience, advisoryPolicy, "01910000-8420-7000-8000-000000000011", base.Add(2*time.Second))

	event := alarmEvent(safetyTenantID, safetySiteID, "01910000-8520-7000-8000-000000000001", "01910000-8620-7000-8000-000000000001", "01910000-8720-7000-8000-000000000001", AlarmCreated, alarmmodel.ConditionActive, base.Add(3*time.Second))
	intents, err := store.ProcessAlarmEvent(ctx, event, base.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 2 {
		t.Fatalf("expected mandatory and advisory policy evidence, got %#v", intents)
	}
	var mandatory, advisory *NotificationIntent
	for index := range intents {
		if intents[index].MandatorySafety {
			mandatory = &intents[index]
		} else {
			advisory = &intents[index]
		}
	}
	if mandatory == nil || len(mandatory.Recipients) != 1 || mandatory.Status != IntentScheduled {
		t.Fatalf("ordinary preference suppressed a mandatory safety notification: %#v", mandatory)
	}
	if advisory == nil || len(advisory.Recipients) != 0 || advisory.Status != IntentCancelled || advisory.Disposition != DispositionCancelled {
		t.Fatalf("advisory preference did not suppress only the advisory notification: %#v", advisory)
	}
}

func TestPostgresExternalHandoffCommitsBeforeS15AndRecoversIdempotently(t *testing.T) {
	store := openNotificationTestStore(t)
	defer store.Close()
	ctx := context.Background()
	base := time.Date(2026, 8, 19, 18, 0, 0, 0, time.UTC)
	principal := "principal:s16-external-operator"
	template := testTemplate("01910000-8130-7000-8000-000000000001", "01910000-8130-7000-8000-000000000002", ChannelEmail)
	audience := testAudienceWithPrincipal("01910000-8230-7000-8000-000000000001", "01910000-8230-7000-8000-000000000002", principal)
	integrationID := "01910000-8830-7000-8000-000000000001"
	policy := singleStagePolicy("01910000-8330-7000-8000-000000000001", "01910000-8330-7000-8000-000000000002", "external safety", audience, template, true, ChannelEmail, integrationID)
	releaseNotificationFixture(t, store, externalTenantID, externalSiteID, template, audience, policy, "01910000-8430-7000-8000-000000000001", base)

	event := alarmEvent(externalTenantID, externalSiteID, "01910000-8530-7000-8000-000000000001", "01910000-8630-7000-8000-000000000001", "01910000-8730-7000-8000-000000000001", AlarmCreated, alarmmodel.ConditionActive, base)
	intents, err := store.ProcessAlarmEvent(ctx, event, base.Add(time.Second))
	if err != nil || len(intents) != 1 {
		t.Fatalf("create external intent: intents=%#v err=%v", intents, err)
	}
	claim, err := store.ClaimDue(ctx, externalTenantID, "s16-external-worker", base.Add(2*time.Second), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	port := &fakeDeliveryPort{failFirst: true, deliveryID: "01910000-8930-7000-8000-000000000001"}
	if _, err := store.SubmitExternal(ctx, *claim, port, base.Add(3*time.Second)); err == nil {
		t.Fatal("expected first S15 submission seam to fail after durable handoff")
	}

	ack := alarmEvent(externalTenantID, externalSiteID, "01910000-8530-7000-8000-000000000002", event.AlarmID, event.IncidentCorrelationID, AlarmAcknowledged, alarmmodel.ConditionActive, base.Add(4*time.Second))
	if _, err := store.ProcessAlarmEvent(ctx, ack, base.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	committed, err := store.ListIntentsForSource(ctx, externalTenantID, event.SourceEventID)
	if err != nil {
		t.Fatal(err)
	}
	if committed[0].Status != IntentExternalSubmitted || committed[0].ExternalDeliveryIntentID != "" {
		t.Fatalf("ACK crossed the committed external handoff boundary: %#v", committed[0])
	}
	deliveryID, err := store.ResumeExternalHandoff(ctx, externalTenantID, committed[0].IntentID, port, base.Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if deliveryID != port.deliveryID || len(port.requests) != 2 || port.requests[0].IdempotencyKey != committed[0].IntentID || port.requests[1].IdempotencyKey != committed[0].IntentID {
		t.Fatalf("external recovery did not reuse the frozen S15 idempotency identity: delivery=%s requests=%#v", deliveryID, port.requests)
	}
	if second, err := store.ResumeExternalHandoff(ctx, externalTenantID, committed[0].IntentID, port, base.Add(7*time.Second)); err != nil || second != deliveryID || len(port.requests) != 2 {
		t.Fatalf("bound S15 intent was resubmitted instead of reused: delivery=%s err=%v requests=%d", second, err, len(port.requests))
	}
}

func openNotificationTestStore(t *testing.T) *PostgresStore {
	t.Helper()
	databaseURL := os.Getenv("S16_NOTIFICATION_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S16_NOTIFICATION_TEST_DATABASE_URL is not configured")
	}
	store, err := OpenPostgresStore(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func releaseNotificationFixture(t *testing.T, store *PostgresStore, tenantID, siteID string, template TemplateRevision, audience AudienceRevision, policy NotificationPolicyRevision, assignmentID string, releasedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := store.ReleaseTemplate(ctx, tenantID, template, releasedAt, "principal:s16-admin"); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseAudience(ctx, tenantID, audience, releasedAt, "principal:s16-admin"); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleasePolicy(ctx, tenantID, policy, releasedAt, "principal:s16-admin"); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignPolicy(ctx, PolicyAssignment{TenantID: tenantID, SiteID: siteID, AssignmentID: assignmentID, AssignmentRevision: 1, PolicyRevisionID: policy.PolicyRevisionID, AssignedAt: releasedAt.Add(time.Millisecond), AssignedBy: "principal:s16-admin"}); err != nil {
		t.Fatal(err)
	}
}

func testAudienceWithPrincipal(audienceID, revisionID, principal string) AudienceRevision {
	audience := AudienceRevision{SchemaVersion: ArtifactSchemaVersion, AudienceID: audienceID, AudienceRevisionID: revisionID, Revision: 1, Recipients: []Recipient{{PrincipalID: principal, Address: principal + "@example.com"}}}
	sealAudience(&audience)
	return audience
}

func singleStagePolicy(policyID, revisionID, name string, audience AudienceRevision, template TemplateRevision, mandatory bool, channel Channel, integrationID string) NotificationPolicyRevision {
	policy := NotificationPolicyRevision{
		SchemaVersion: ArtifactSchemaVersion, PolicyID: policyID, PolicyRevisionID: revisionID, Revision: 1, Name: name,
		AlarmActions: []AlarmAction{AlarmCreated}, MandatorySafety: mandatory,
		Stages: []EscalationStage{{Stage: 0, AudienceRevisionID: audience.AudienceRevisionID, TemplateRevisionID: template.TemplateRevisionID, Channel: channel, IntegrationID: integrationID}},
	}
	sealPolicy(&policy)
	return policy
}

func alarmEvent(tenantID, siteID, sourceEventID, alarmID, incidentID string, action AlarmAction, condition alarmmodel.Condition, occurredAt time.Time) AlarmEvent {
	return AlarmEvent{
		TenantID: tenantID, SiteID: siteID, SourceEventID: sourceEventID, AlarmID: alarmID, IncidentCorrelationID: incidentID,
		Action: action, CurrentSeverity: alarmmodel.SeverityCritical, PeakSeverity: alarmmodel.SeverityCritical, Condition: condition, OccurredAt: occurredAt,
		Attributes: map[string]string{"assetName": "Central plant"},
	}
}

type fakeDeliveryPort struct {
	failFirst  bool
	deliveryID string
	requests   []outbounddelivery.SubmitIntentRequest
}

func (port *fakeDeliveryPort) SubmitNotification(_ context.Context, request outbounddelivery.SubmitIntentRequest) (outbounddelivery.DeliveryIntent, error) {
	port.requests = append(port.requests, request)
	if port.failFirst {
		port.failFirst = false
		return outbounddelivery.DeliveryIntent{}, errors.New("simulated S15 handoff failure")
	}
	return outbounddelivery.DeliveryIntent{ID: port.deliveryID, TenantID: request.TenantID, SiteID: request.SiteID, IntegrationID: request.IntegrationID, IdempotencyKey: request.IdempotencyKey}, nil
}
