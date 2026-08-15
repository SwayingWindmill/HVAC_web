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

	parameterValue, err := strconv.ParseFloat(envOr("S3_LOCAL_PARAMETER_VALUE", "24"), 64)
	if err != nil {
		fatal(errors.New("S3_LOCAL_PARAMETER_VALUE is invalid"))
	}
	currentValue, err := strconv.ParseFloat(envOr("S3_LOCAL_CURRENT_VALUE", "23"), 64)
	if err != nil {
		fatal(errors.New("S3_LOCAL_CURRENT_VALUE is invalid"))
	}
	currentRevision, err := strconv.ParseUint(envOr("S3_LOCAL_CURRENT_BUSINESS_REVISION", "21"), 10, 64)
	if err != nil || currentRevision == 0 {
		fatal(errors.New("S3_LOCAL_CURRENT_BUSINESS_REVISION is invalid"))
	}
	capability := commandmodel.Capability(strings.TrimSpace(envOr("S3_LOCAL_CAPABILITY", string(commandmodel.CapabilitySetTemperatureSetpoint))))
	profile, ok := commandmodel.CapabilityProfileFor(capability)
	if !ok {
		fatal(errors.New("S3_LOCAL_CAPABILITY is unsupported"))
	}
	now := time.Now().UTC()
	tenantID := requiredEnv("S3_LOCAL_TENANT_ID")
	siteID := requiredEnv("S3_LOCAL_SITE_ID")
	deviceID := requiredEnv("S3_LOCAL_DEVICE_ID")
	pointID := requiredEnv("S3_LOCAL_COMMAND_POINT_ID")
	verificationPointKey := requiredEnv("S3_LOCAL_VERIFICATION_POINT_KEY")
	principalID := envOr("S3_LOCAL_PRINCIPAL_ID", "018f3e00-5000-7000-8000-000000000001")
	grantID := envOr("S3_LOCAL_GRANT_ID", "018f3e00-9000-7000-8000-000000000001")
	parameters := commandmodel.CommandParameters{}
	var currentValuePointer *float64
	if profile.ParameterKey != "" {
		parameters[profile.ParameterKey] = parameterValue
		currentValuePointer = &currentValue
	}

	result, err := store.Submit(ctx, commandmodel.SubmitRequest{
		TenantID:             tenantID,
		SiteID:               siteID,
		DeviceID:             deviceID,
		PointID:              pointID,
		PrincipalID:          principalID,
		IdempotencyKey:       envOr("S3_LOCAL_IDEMPOTENCY_KEY", "s3-local-smoke-v1"),
		Capability:           capability,
		Parameters:           parameters,
		VerificationPointKey: verificationPointKey,
		CurrentState: commandmodel.CurrentStateEvidence{
			EvaluationAvailability: "AVAILABLE",
			Presence:               "ONLINE",
			Readiness:              "CURRENT",
			Quality:                "GOOD",
			BusinessRevision:       currentRevision,
			CurrentValue:           currentValuePointer,
			ObservedAt:             now.Add(-time.Second),
		},
		Authorization: commandmodel.AuthorizationSnapshot{
			GrantID:                     grantID,
			PolicyRevision:              "s3-local-policy-v1",
			Purpose:                     commandmodel.AuthorizationCommandSubmit,
			PrincipalID:                 principalID,
			TenantID:                    tenantID,
			SiteID:                      siteID,
			DeviceID:                    deviceID,
			Capability:                  capability,
			CapabilityRevision:          profile.Revision,
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
		"commandId":  result.Intent.ID,
		"status":     result.Intent.Status,
		"replayed":   result.Replayed,
		"capability": capability,
		"parameters": parameters,
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
