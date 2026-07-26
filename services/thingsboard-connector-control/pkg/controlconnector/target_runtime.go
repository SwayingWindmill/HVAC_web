package controlconnector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const (
	approvedCohortSchemaVersion        = 1
	approvedMappingStatus              = "VERIFIED"
	approvedProviderContract           = "THINGSBOARD_CE_4.3.1.3"
	approvedSetpointCapabilityRevision = "capability:set-temperature-setpoint:v1"
	maximumCohortDocumentBytes         = int64(64 << 10)
	maximumCredentialBytes             = int64(64 << 10)
)

type ApprovedCohort struct {
	SchemaVersion         int                     `json:"schemaVersion"`
	OrganizationID        string                  `json:"organizationId"`
	SiteID                string                  `json:"siteId"`
	DeviceID              string                  `json:"deviceId"`
	IntegrationID         string                  `json:"integrationId"`
	ExternalDeviceID      string                  `json:"externalDeviceId"`
	BindingRevision       string                  `json:"bindingRevision"`
	Capability            commandmodel.Capability `json:"capability"`
	CapabilityRevision    string                  `json:"capabilityRevision"`
	MappingRevision       string                  `json:"mappingRevision"`
	MappingStatus         string                  `json:"mappingStatus"`
	ProviderContract      string                  `json:"providerContract"`
	ProviderMethod        string                  `json:"providerMethod"`
	ReportedStateKey      string                  `json:"reportedStateKey"`
	TimeoutMilliseconds   int64                   `json:"timeoutMilliseconds"`
	MaximumSetpointDeltaC float64                 `json:"maximumSetpointDeltaC"`
	CredentialReference   string                  `json:"credentialReference"`
}

func LoadApprovedCohort(path string) (ApprovedCohort, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return ApprovedCohort{}, errors.New("approved cohort path must be absolute")
	}
	file, err := os.Open(path)
	if err != nil {
		return ApprovedCohort{}, fmt.Errorf("open approved cohort: %w", err)
	}
	defer file.Close()
	body, err := readLimited(file, maximumCohortDocumentBytes)
	if err != nil {
		return ApprovedCohort{}, fmt.Errorf("read approved cohort: %w", err)
	}
	var cohort ApprovedCohort
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cohort); err != nil || ensureTargetRuntimeEOF(decoder) != nil {
		return ApprovedCohort{}, errors.New("approved cohort document is invalid")
	}
	if err := cohort.Validate(); err != nil {
		return ApprovedCohort{}, err
	}
	return cohort, nil
}

func (cohort ApprovedCohort) Validate() error {
	if cohort.SchemaVersion != approvedCohortSchemaVersion ||
		!commandmodel.IsUUIDv7(cohort.OrganizationID) || !commandmodel.IsUUIDv7(cohort.SiteID) || !commandmodel.IsUUIDv7(cohort.DeviceID) ||
		strings.TrimSpace(cohort.IntegrationID) == "" || strings.TrimSpace(cohort.ExternalDeviceID) == "" || strings.TrimSpace(cohort.BindingRevision) == "" ||
		cohort.Capability != commandmodel.CapabilitySetTemperatureSetpoint || cohort.CapabilityRevision != approvedSetpointCapabilityRevision ||
		strings.TrimSpace(cohort.MappingRevision) == "" || cohort.MappingStatus != approvedMappingStatus ||
		cohort.ProviderContract != approvedProviderContract || strings.TrimSpace(cohort.ProviderMethod) == "" || strings.TrimSpace(cohort.ReportedStateKey) == "" ||
		cohort.TimeoutMilliseconds < 1000 || cohort.TimeoutMilliseconds > 30000 || cohort.MaximumSetpointDeltaC != 1 ||
		!opaqueCredentialReference(cohort.CredentialReference) {
		return errors.New("approved cohort is incomplete or not production verified")
	}
	return nil
}

func (cohort ApprovedCohort) Mapping() Mapping {
	return Mapping{
		Capability:         cohort.Capability,
		CapabilityRevision: cohort.CapabilityRevision,
		MappingRevision:    cohort.MappingRevision,
		Status:             MappingProductionVerified,
		Method:             cohort.ProviderMethod,
		Timeout:            time.Duration(cohort.TimeoutMilliseconds) * time.Millisecond,
	}
}

type ApprovedCohortTargetResolver struct {
	cohort ApprovedCohort
}

func NewApprovedCohortTargetResolver(cohort ApprovedCohort) (*ApprovedCohortTargetResolver, error) {
	if err := cohort.Validate(); err != nil {
		return nil, err
	}
	return &ApprovedCohortTargetResolver{cohort: cohort}, nil
}

func (resolver *ApprovedCohortTargetResolver) ResolveThingsBoardTarget(_ context.Context, envelope commandmodel.DispatchEnvelope) (Target, error) {
	if resolver == nil || envelope.OrganizationID != resolver.cohort.OrganizationID || envelope.SiteID != resolver.cohort.SiteID ||
		envelope.DeviceID != resolver.cohort.DeviceID || envelope.Capability != resolver.cohort.Capability ||
		envelope.CapabilityRevision != resolver.cohort.CapabilityRevision || strings.TrimSpace(envelope.CommandID) == "" ||
		strings.TrimSpace(envelope.AttemptID) == "" || strings.TrimSpace(envelope.PayloadHash) == "" || envelope.ExecutionFence == 0 {
		return Target{}, ErrTargetUnavailable
	}
	return Target{
		IntegrationID:    resolver.cohort.IntegrationID,
		ExternalDeviceID: resolver.cohort.ExternalDeviceID,
		BindingRevision:  resolver.cohort.BindingRevision,
	}, nil
}

type FileCredentialConfig struct {
	Path                string
	CredentialReference string
	IntegrationID       string
}

type FileCredentialProvider struct {
	path                string
	credentialReference string
	integrationID       string
}

func NewFileCredentialProvider(config FileCredentialConfig) (*FileCredentialProvider, error) {
	path := strings.TrimSpace(config.Path)
	if path == "" || !filepath.IsAbs(path) || strings.TrimSpace(config.IntegrationID) == "" || !opaqueCredentialReference(config.CredentialReference) {
		return nil, errors.New("file credential provider configuration is invalid")
	}
	return &FileCredentialProvider{
		path:                path,
		credentialReference: strings.TrimSpace(config.CredentialReference),
		integrationID:       strings.TrimSpace(config.IntegrationID),
	}, nil
}

func (provider *FileCredentialProvider) ProviderCredential(_ context.Context, target Target) (string, error) {
	if provider == nil || target.IntegrationID != provider.integrationID || strings.TrimSpace(target.ExternalDeviceID) == "" || strings.TrimSpace(target.BindingRevision) == "" {
		return "", ErrTargetUnavailable
	}
	file, err := os.Open(provider.path)
	if err != nil {
		return "", ErrTargetUnavailable
	}
	defer file.Close()
	body, err := readLimited(file, maximumCredentialBytes)
	if err != nil {
		return "", ErrTargetUnavailable
	}
	credential := strings.TrimSpace(string(body))
	if credential == "" || strings.ContainsAny(credential, "\r\n\x00") {
		return "", ErrTargetUnavailable
	}
	return credential, nil
}

func (provider *FileCredentialProvider) CredentialReference() string {
	if provider == nil {
		return ""
	}
	return provider.credentialReference
}

func opaqueCredentialReference(reference string) bool {
	reference = strings.TrimSpace(reference)
	return strings.HasPrefix(reference, "workload"+"://") || strings.HasPrefix(reference, "secret"+"://")
}

func readLimited(reader io.Reader, maximum int64) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: maximum + 1}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maximum {
		return nil, errors.New("input exceeds size limit")
	}
	return body, nil
}

func ensureTargetRuntimeEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON")
		}
		return err
	}
	return nil
}
