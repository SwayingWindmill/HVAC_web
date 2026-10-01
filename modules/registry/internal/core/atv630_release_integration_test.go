package core

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestPostgresATV630ReleaseCandidatePublishesImmutableDeviceTemplate(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)
	claims := writerIntegrationClaims(registryauth.ActionTemplateManage)
	candidate := edgecontrol.ATV630ProtocolReleaseCandidate()
	references := map[string]string{
		"embeddedEthernetManual":  "EAV64327 v03",
		"communicationParameters": "EAV64332 v4.6 (2026-05-01)",
		"protocolConformance":      "HVAC_web#339 production Bridge + ATV630 DeviceAdapter + Virtual ATV630 real TCP",
	}
	payload := map[string]any{
		"schemaVersion":     1,
		"manufacturer":      candidate.Manufacturer,
		"model":             candidate.Model,
		"transport":         candidate.Transport,
		"controlProfile":    candidate.ControlProfile,
		"hardwareCertified": false,
		"parameters":        candidate.Parameters,
	}

	released, _, err := store.ReleaseTemplate(ctx, claims, ReleaseTemplateRequest{
		TemplateKey:       "schneider.atv630.cia402-modbus-tcp",
		TemplateKind:      TemplateDevice,
		Payload:           payload,
		ReleaseReferences: references,
		Meta: MutationMeta{
			IdempotencyKey: "issue339-atv630-template-v1",
			Reason:         "release exact ATV630 mapping after production real-TCP conformance",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if released.Status != "RELEASED" || released.TemplateKind != TemplateDevice || released.TemplateKey != "schneider.atv630.cia402-modbus-tcp" {
		t.Fatalf("unexpected ATV630 template release: %#v", released)
	}

	var storedPayloadJSON []byte
	var storedReferencesJSON []byte
	var storedStatus string
	err = store.withWriteTransaction(ctx, claims, registryauth.ActionTemplateManage, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT payload, release_references, status FROM core_registry.registry_template_revisions WHERE tenant_id=$1::uuid AND id=$2::uuid`, testTenantA, released.ID).Scan(&storedPayloadJSON, &storedReferencesJSON, &storedStatus)
	})
	if err != nil {
		t.Fatal(err)
	}
	if storedStatus != "RELEASED" {
		t.Fatalf("ATV630 template revision was not persisted as RELEASED: %s", storedStatus)
	}
	var storedPayload struct {
		SchemaVersion     int                                   `json:"schemaVersion"`
		Manufacturer      string                                `json:"manufacturer"`
		Model             string                                `json:"model"`
		Transport         string                                `json:"transport"`
		ControlProfile    string                                `json:"controlProfile"`
		HardwareCertified bool                                  `json:"hardwareCertified"`
		Parameters        []edgecontrol.ATV630ProtocolParameter `json:"parameters"`
	}
	if err := json.Unmarshal(storedPayloadJSON, &storedPayload); err != nil {
		t.Fatal(err)
	}
	if storedPayload.SchemaVersion != 1 || storedPayload.Manufacturer != "Schneider Electric" || storedPayload.Model != "ATV630" || storedPayload.Transport != "EMBEDDED_MODBUS_TCP" || storedPayload.ControlProfile != "CIA402_DRIVECOM" || storedPayload.HardwareCertified {
		t.Fatalf("stored ATV630 template identity/certification drifted: %#v", storedPayload)
	}
	if !reflect.DeepEqual(storedPayload.Parameters, candidate.Parameters) {
		t.Fatalf("released ATV630 mapping differs from conformance candidate:\n got: %#v\nwant: %#v", storedPayload.Parameters, candidate.Parameters)
	}
	var storedReferences map[string]string
	if err := json.Unmarshal(storedReferencesJSON, &storedReferences); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(storedReferences, references) {
		t.Fatalf("ATV630 release references drifted: got=%#v want=%#v", storedReferences, references)
	}

	err = store.withWriteTransaction(ctx, claims, registryauth.ActionTemplateManage, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE core_registry.registry_template_revisions SET payload='{}'::jsonb WHERE tenant_id=$1::uuid AND id=$2::uuid`, testTenantA, released.ID)
		return mapRegistryWriteError(err)
	})
	if !errors.Is(err, ErrTemplateImmutable) {
		t.Fatalf("released ATV630 TemplateRevision mutation error=%v", err)
	}
}
