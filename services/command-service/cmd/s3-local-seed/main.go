package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/services/command-service/pkg/commandservice"
)

const setpointCapabilityRevision = "capability:set-temperature-setpoint:v1"

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	databaseURL, err := loadSingleLineFile(requiredEnv("S3_LOCAL_DATABASE_URL_FILE"))
	if err != nil {
		fatal(err)
	}
	store, err := commandservice.OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		fatal(err)
	}
	defer store.Close()

	setpointC, err := strconv.ParseFloat(envOr("S3_LOCAL_SETPOINT_C", "24"), 64)
	if err != nil {
		fatal(errors.New("S3_LOCAL_SETPOINT_C is invalid"))
	}
	now := time.Now().UTC()
	organizationID := requiredEnv("S3_LOCAL_ORGANIZATION_ID")
	siteID := requiredEnv("S3_LOCAL_SITE_ID")
	deviceID := requiredEnv("S3_LOCAL_DEVICE_ID")
	principalID := envOr("S3_LOCAL_PRINCIPAL_ID", "018f3e00-5000-7000-8000-000000000001")
	grantID := envOr("S3_LOCAL_GRANT_ID", "018f3e00-9000-7000-8000-000000000001")

	result, err := store.Submit(ctx, commandmodel.SubmitRequest{
		OrganizationID: organizationID,
		SiteID:         siteID,
		DeviceID:       deviceID,
		PrincipalID:    principalID,
		IdempotencyKey: envOr("S3_LOCAL_IDEMPOTENCY_KEY", "s3-local-smoke-v1"),
		Capability:     commandmodel.CapabilitySetTemperatureSetpoint,
		SetpointC:      setpointC,
		CurrentState: commandmodel.CurrentStateEvidence{
			EvaluationAvailability: "AVAILABLE",
			Presence:               "ONLINE",
			Readiness:              "CURRENT",
			Quality:                "GOOD",
			BusinessRevision:       21,
			CurrentTemperatureC:    23,
			ObservedAt:             now.Add(-time.Second),
		},
		Authorization: commandmodel.AuthorizationSnapshot{
			GrantID:                     grantID,
			PolicyRevision:              "s3-local-policy-v1",
			Purpose:                     commandmodel.AuthorizationCommandSubmit,
			PrincipalID:                 principalID,
			OrganizationID:              organizationID,
			SiteID:                      siteID,
			DeviceID:                    deviceID,
			Capability:                  commandmodel.CapabilitySetTemperatureSetpoint,
			CapabilityRevision:          setpointCapabilityRevision,
			MaximumRisk:                 commandmodel.RiskHigh,
			EmergencyRevocationRevision: 1,
			IssuedAt:                    now.Add(-5 * time.Second),
			ExpiresAt:                   now.Add(25 * time.Second),
		},
	})
	if err != nil {
		fatal(err)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"commandId": result.Intent.ID,
		"status":    result.Intent.Status,
		"replayed":  result.Replayed,
		"setpointC": setpointC,
	})
}

func loadSingleLineFile(path string) (string, error) {
	body, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	if len(body) == 0 || len(body) > 64<<10 {
		return "", errors.New("database URL file size is invalid")
	}
	value := strings.TrimSpace(string(body))
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("database URL file content is invalid")
	}
	return value, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		fatal(fmt.Errorf("%s is required", name))
	}
	return value
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
