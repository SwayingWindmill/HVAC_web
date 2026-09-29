package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/registry/internal/core"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := strings.TrimSpace(os.Getenv("S1_CORE_DATABASE_URL"))
	tenantID := strings.TrimSpace(os.Getenv("ATV630_TEMPLATE_TENANT_ID"))
	principalID := strings.TrimSpace(os.Getenv("ATV630_TEMPLATE_RELEASE_PRINCIPAL_ID"))
	outputPath := strings.TrimSpace(os.Getenv("ATV630_TEMPLATE_REVISION_FILE"))
	if databaseURL == "" || tenantID == "" || principalID == "" || outputPath == "" {
		return errors.New("S1_CORE_DATABASE_URL, ATV630_TEMPLATE_TENANT_ID, ATV630_TEMPLATE_RELEASE_PRINCIPAL_ID and ATV630_TEMPLATE_REVISION_FILE are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := core.OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer store.Close()

	candidate := edgecontrol.ATV630ProtocolReleaseCandidate()
	revision, _, err := store.ReleaseTemplate(ctx, registryauth.GrantClaims{
		PrincipalID:    principalID,
		TenantID:       tenantID,
		Actions:        []registryauth.Action{registryauth.ActionTemplateManage},
		PolicyRevision: "atv630-acceptance-release:1",
	}, core.ReleaseTemplateRequest{
		TemplateKey:  "schneider.atv630.cia402-modbus-tcp",
		TemplateKind: core.TemplateDevice,
		Payload: map[string]any{
			"schemaVersion": 1, "manufacturer": candidate.Manufacturer, "model": candidate.Model,
			"transport": candidate.Transport, "controlProfile": candidate.ControlProfile,
			"hardwareCertified": false, "parameters": candidate.Parameters,
		},
		ReleaseReferences: map[string]string{
			"embeddedEthernetManual":  "EAV64327 v03",
			"communicationParameters": "EAV64332 v4.6 (2026-05-01)",
			"protocolConformance":     "HVAC_web#339 production Bridge + ATV630 DeviceAdapter + Virtual ATV630 real TCP",
		},
		Meta: core.MutationMeta{
			IdempotencyKey: "issue347-atv630-deployed-template-v1",
			Reason:         "bind WSL deployed protocol acceptance to the immutable #339-conformant ATV630 template",
		},
	})
	if err != nil {
		return err
	}
	if revision.Status != "RELEASED" {
		return fmt.Errorf("ATV630 TemplateRevision status is %s", revision.Status)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(outputPath, []byte(revision.ID+"\n"), 0o600); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(revision)
}
