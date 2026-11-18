package optimization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
)

const (
	iamCommandWorkloadDecisionPath = "/internal/v1/command/workload-decision"
)

type WorkloadCommandClientConfig struct {
	CertificatePath string
	PrivateKeyPath  string
	CAPath          string
	IAMURL          string
	GatewayURL      string
	IAMClient       *http.Client
	GatewayClient   *http.Client
}

type WorkloadCommandClient struct {
	iamURL        string
	gatewayURL    string
	iamClient     *http.Client
	gatewayClient *http.Client
}

func NewWorkloadCommandClient(config WorkloadCommandClientConfig) (*WorkloadCommandClient, error) {
	iamOrigin := strings.TrimRight(strings.TrimSpace(config.IAMURL), "/")
	gatewayOrigin := strings.TrimRight(strings.TrimSpace(config.GatewayURL), "/")
	if iamOrigin == "" || gatewayOrigin == "" {
		return nil, errors.New("workload command client requires IAM and Gateway URLs")
	}
	iamClient := config.IAMClient
	gatewayClient := config.GatewayClient
	if iamClient == nil || gatewayClient == nil {
		certs := &workloadtls.CertificateFiles{
			CertificatePath: config.CertificatePath,
			PrivateKeyPath:  config.PrivateKeyPath,
		}
		if iamClient == nil {
			parsed, err := url.Parse(iamOrigin)
			if err != nil || parsed.Hostname() == "" {
				return nil, fmt.Errorf("invalid IAM URL %q", iamOrigin)
			}
			iamClient, err = workloadtls.NewHTTPClient(workloadtls.ClientConfig{
				CertificateFiles: certs,
				ServerCAPath:     config.CAPath,
				ServerName:       parsed.Hostname(),
				Timeout:          10 * time.Second,
			})
			if err != nil {
				return nil, err
			}
		}
		if gatewayClient == nil {
			parsed, err := url.Parse(gatewayOrigin)
			if err != nil || parsed.Hostname() == "" {
				return nil, fmt.Errorf("invalid Gateway URL %q", gatewayOrigin)
			}
			if parsed.Scheme == "https" && strings.TrimSpace(config.CAPath) != "" {
				gatewayClient, err = workloadtls.NewHTTPClient(workloadtls.ClientConfig{
					CertificateFiles: certs,
					ServerCAPath:     config.CAPath,
					ServerName:       parsed.Hostname(),
					Timeout:          15 * time.Second,
				})
				if err != nil {
					return nil, err
				}
			} else {
				gatewayClient = &http.Client{
					Timeout: 15 * time.Second,
					CheckRedirect: func(*http.Request, []*http.Request) error {
						return http.ErrUseLastResponse
					},
				}
			}
		}
	}
	return &WorkloadCommandClient{
		iamURL:        iamOrigin,
		gatewayURL:    gatewayOrigin,
		iamClient:     iamClient,
		gatewayClient: gatewayClient,
	}, nil
}

func (client *WorkloadCommandClient) RequestCommandGrant(ctx context.Context, req commandauth.WorkloadDecisionRequest) (string, commandauth.Decision, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return "", commandauth.Decision{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, client.iamURL+iamCommandWorkloadDecisionPath, bytes.NewReader(payload))
	if err != nil {
		return "", commandauth.Decision{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, application/problem+json")
	resp, err := client.iamClient.Do(httpReq)
	if err != nil {
		return "", commandauth.Decision{}, fmt.Errorf("request IAM workload command decision: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return "", commandauth.Decision{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", commandauth.Decision{}, fmt.Errorf("IAM workload command decision returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out commandauth.DecisionResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", commandauth.Decision{}, fmt.Errorf("decode IAM workload command decision: %w", err)
	}
	return out.DelegationGrant, out.Decision, nil
}

type autoCommandCurrentState struct {
	EvaluationAvailability string   `json:"evaluationAvailability"`
	Presence               string   `json:"presence"`
	Readiness              string   `json:"readiness"`
	Quality                string   `json:"quality"`
	BusinessRevision       uint64   `json:"businessRevision"`
	CurrentValue           *float64 `json:"currentValue,omitempty"`
	ObservedAt             string   `json:"observedAt,omitempty"`
}

type autoCommandPayload struct {
	TenantID             string                         `json:"tenantId"`
	DeviceID             string                         `json:"deviceId"`
	PointID              string                         `json:"pointId"`
	Capability           commandmodel.Capability        `json:"capability"`
	Parameters           commandmodel.CommandParameters `json:"parameters"`
	VerificationPointKey string                         `json:"verificationPointKey"`
	IdempotencyKey       string                         `json:"idempotencyKey"`
	IsRollback           bool                           `json:"isRollback"`
	PreconditionMode     commandmodel.PreconditionMode  `json:"preconditionMode,omitempty"`
	BypassReason         string                         `json:"bypassReason,omitempty"`
	CommandGrant         string                         `json:"commandGrant"`
	CurrentState         autoCommandCurrentState        `json:"currentState"`
}

func (client *WorkloadCommandClient) SubmitAutoCommand(ctx context.Context, grant string, input AutoControlDispatchInput) (string, error) {
	if client == nil || client.gatewayClient == nil {
		return "", errors.New("workload command client is not configured")
	}
	availability := strings.TrimSpace(input.EvaluationAvailability)
	presence := strings.TrimSpace(input.Presence)
	readiness := strings.TrimSpace(input.Readiness)
	quality := strings.TrimSpace(input.Quality)
	if !input.IsRollback {
		if input.PreconditionMode == commandmodel.PreconditionEmergencyBypass {
			return "", errors.New("automated command cannot use emergency precondition bypass")
		}
		if availability != "AVAILABLE" ||
			presence != "ONLINE" ||
			readiness != "CURRENT" ||
			quality != "GOOD" ||
			input.BusinessRevision == 0 ||
			input.ObservedAt.IsZero() ||
			input.CurrentValue == nil ||
			!finite(*input.CurrentValue) {
			return "", errors.New("automated command requires verified live telemetry current state")
		}
	} else if input.PreconditionMode == commandmodel.PreconditionEmergencyBypass && strings.TrimSpace(input.BypassReason) == "" {
		return "", errors.New("emergency bypass rollback requires bypass reason")
	}

	var observedAt string
	if !input.ObservedAt.IsZero() {
		observedAt = input.ObservedAt.UTC().Format(time.RFC3339Nano)
	}
	var wirePreconditionMode commandmodel.PreconditionMode
	var wireBypassReason string
	if input.IsRollback && input.PreconditionMode == commandmodel.PreconditionEmergencyBypass {
		wirePreconditionMode = commandmodel.PreconditionEmergencyBypass
		wireBypassReason = strings.TrimSpace(input.BypassReason)
	}

	body := autoCommandPayload{
		TenantID:             input.TenantID,
		DeviceID:             input.DeviceID,
		PointID:              input.PointID,
		Capability:           input.Capability,
		Parameters:           input.Parameters,
		VerificationPointKey: input.VerificationPointKey,
		IdempotencyKey:       input.IdempotencyKey,
		IsRollback:           input.IsRollback,
		PreconditionMode:     wirePreconditionMode,
		BypassReason:         wireBypassReason,
		CommandGrant:         grant,
		CurrentState: autoCommandCurrentState{
			EvaluationAvailability: availability,
			Presence:               presence,
			Readiness:              readiness,
			Quality:                quality,
			BusinessRevision:       input.BusinessRevision,
			CurrentValue:           input.CurrentValue,
			ObservedAt:             observedAt,
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	endpoint := client.gatewayURL + "/api/v1/sites/" + url.PathEscape(input.SiteID) + "/optimization/auto-commands"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, application/problem+json")
	resp, err := client.gatewayClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("submit auto optimization command: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gateway auto-command returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var accepted struct {
		CommandID string `json:"commandId"`
	}
	if err := json.Unmarshal(raw, &accepted); err != nil || strings.TrimSpace(accepted.CommandID) == "" {
		return "", errors.New("gateway auto-command returned invalid commandId")
	}
	return accepted.CommandID, nil
}
