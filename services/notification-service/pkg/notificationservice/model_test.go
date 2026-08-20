package notificationservice

import (
	"testing"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

func TestReleasedNotificationArtifactsRejectTampering(t *testing.T) {
	template := testTemplate("01910000-8100-7000-8000-000000000001", "01910000-8100-7000-8000-000000000002", ChannelInApp)
	template.Subject = "tampered after release"
	if err := template.Validate(); err == nil {
		t.Fatal("tampered TemplateRevision retained a valid digest")
	}

	audience := testAudience("01910000-8200-7000-8000-000000000001", "01910000-8200-7000-8000-000000000002")
	audience.Recipients[0].PrincipalID = "principal:tampered"
	if err := audience.Validate(); err == nil {
		t.Fatal("tampered AudienceRevision retained a valid digest")
	}

	policy := NotificationPolicyRevision{
		SchemaVersion: ArtifactSchemaVersion,
		PolicyID:      "01910000-8300-7000-8000-000000000001", PolicyRevisionID: "01910000-8300-7000-8000-000000000002", Revision: 1,
		Name: "mandatory alarm", AlarmActions: []AlarmAction{AlarmCreated}, Severities: []alarmmodel.Severity{alarmmodel.SeverityCritical}, MandatorySafety: true,
		Stages: []EscalationStage{{Stage: 0, AudienceRevisionID: audience.AudienceRevisionID, TemplateRevisionID: template.TemplateRevisionID, Channel: ChannelInApp}},
	}
	sealPolicy(&policy)
	policy.MandatorySafety = false
	if err := policy.Validate(); err == nil {
		t.Fatal("tampered NotificationPolicyRevision retained a valid digest")
	}
}

func TestRenderTemplateFailsClosedOnMissingVariable(t *testing.T) {
	template := testTemplate("01910000-8100-7000-8000-000000000011", "01910000-8100-7000-8000-000000000012", ChannelInApp)
	template.Subject = "Alarm {{alarmId}} {{attributes.assetName}}"
	sealTemplate(&template)
	if _, _, err := RenderTemplate(template, map[string]string{"alarmId": "alarm-1"}); err == nil {
		t.Fatal("notification template silently rendered a missing source variable")
	}
}

func testTemplate(templateID, revisionID string, channel Channel) TemplateRevision {
	template := TemplateRevision{
		SchemaVersion: ArtifactSchemaVersion, TemplateID: templateID, TemplateRevisionID: revisionID, Revision: 1,
		Channel: channel, Subject: "{{action}} alarm {{alarmId}}", Body: "Severity {{currentSeverity}} at {{siteId}}",
	}
	sealTemplate(&template)
	return template
}

func testAudience(audienceID, revisionID string) AudienceRevision {
	audience := AudienceRevision{
		SchemaVersion: ArtifactSchemaVersion, AudienceID: audienceID, AudienceRevisionID: revisionID, Revision: 1,
		Recipients: []Recipient{{PrincipalID: "principal:operator", Address: "operator@example.com"}},
	}
	sealAudience(&audience)
	return audience
}

func sealTemplate(template *TemplateRevision)       { template.Digest = TemplateDigest(*template) }
func sealAudience(audience *AudienceRevision)       { audience.Digest = AudienceDigest(*audience) }
func sealPolicy(policy *NotificationPolicyRevision) { policy.Digest = PolicyDigest(*policy) }
