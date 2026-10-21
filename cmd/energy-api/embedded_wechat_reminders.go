package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/services/notification-service/pkg/notificationservice"
)

// WeChat reminders of Alarms (#664, docs/specs/miniprogram-wechat.md). Notification Service runs
// here, in the Gateway's workload, and reaches three owners as that workload: IAM says who may
// get a reminder, the Registry gives the Site's name and time zone, identity-service sends.

// defaultAlarmTemplateID is 监控告警通知 in 微信公众平台 → 订阅消息 → 我的模板.
const defaultAlarmTemplateID = "E1fGHBeTTIC0bT2xd6Tb0S61UyvL5krxDZz12wkzkdE"

func newWeChatAlarmReminderWorker(scheduler *notificationservice.Scheduler) (*notificationservice.WeChatAlarmReminderWorker, error) {
	required := map[string]string{}
	for _, name := range []string{"IAM_URL", "IAM_SERVER_CA", "IAM_CLIENT_CERT", "IAM_CLIENT_KEY", "CORE_URL", "CORE_SERVER_CA", "OIDC_BACKCHANNEL_BASE_URL"} {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return nil, fmt.Errorf("%s is required for WeChat alarm reminders", name)
		}
		required[name] = value
	}
	certificate, err := tls.LoadX509KeyPair(required["IAM_CLIENT_CERT"], required["IAM_CLIENT_KEY"])
	if err != nil {
		return nil, err
	}
	signer, ok := certificate.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, errors.New("the Gateway workload key cannot sign grants")
	}
	iamRoots, err := loadCertPool(required["IAM_SERVER_CA"], "IAM server CA")
	if err != nil {
		return nil, err
	}
	coreRoots, err := loadCertPool(required["CORE_SERVER_CA"], "Core server CA")
	if err != nil {
		return nil, err
	}
	iamClient := &http.Client{Timeout: 5 * time.Second, Transport: workloadTransport(iamRoots, &certificate, envOr("IAM_SERVER_NAME", "localhost")),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	coreClient := &http.Client{Timeout: 5 * time.Second, Transport: workloadTransport(coreRoots, &certificate, envOr("CORE_SERVER_NAME", "localhost")),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	workload := envOr("GATEWAY_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway")
	return &notificationservice.WeChatAlarmReminderWorker{
		Scheduler:  scheduler,
		Recipients: iamAlarmRecipients{baseURL: strings.TrimRight(required["IAM_URL"], "/"), client: iamClient},
		Sites:      &registrySites{iamURL: strings.TrimRight(required["IAM_URL"], "/"), iamClient: iamClient, coreURL: strings.TrimRight(required["CORE_URL"], "/"), coreClient: coreClient, grants: map[string]registryGrant{}},
		Sender: identityWeChatSender{
			baseURL: strings.TrimRight(required["OIDC_BACKCHANNEL_BASE_URL"], "/"), client: &http.Client{Timeout: 15 * time.Second},
			signer: signer, workload: workload, policyRevision: envOr("IDENTITY_POLICY_REVISION", "policy-v1"),
		},
		TemplateID: envOr("NOTIFICATION_WECHAT_ALARM_TEMPLATE_ID", defaultAlarmTemplateID),
	}, nil
}

func runWeChatAlarmReminders(ctx context.Context, logger *slog.Logger, worker *notificationservice.WeChatAlarmReminderWorker) {
	for {
		if ctx.Err() != nil {
			return
		}
		worked, err := worker.RunOnce(ctx, time.Now().UTC())
		if err != nil {
			logger.Error("wechat_alarm_reminder_failed", "error_code", "WECHAT_ALARM_REMINDER_FAILED", "reason", err.Error())
		}
		if !worked || err != nil {
			if !notificationWait(ctx, time.Second) {
				return
			}
		}
	}
}

// exchangeJSON sends one JSON request and decodes a 200 answer.
func exchangeJSON(client *http.Client, request *http.Request, into any) (int, error) {
	response, err := client.Do(request)
	if err != nil {
		return 0, errors.New("the owner could not be reached")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return response.StatusCode, err
	}
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, fmt.Errorf("the owner answered %d", response.StatusCode)
	}
	if into != nil {
		if err := json.Unmarshal(body, into); err != nil {
			return response.StatusCode, fmt.Errorf("decode the owner's answer: %w", err)
		}
	}
	return response.StatusCode, nil
}

type iamAlarmRecipients struct {
	baseURL string
	client  *http.Client
}

func (iam iamAlarmRecipients) AllowedRecipients(ctx context.Context, tenantID, siteID, alarmID string, people []notificationservice.WeChatPerson) ([]notificationservice.WeChatPerson, error) {
	type recipient struct {
		SubjectIssuer string `json:"subjectIssuer"`
		Subject       string `json:"subject"`
	}
	asked := make([]recipient, 0, len(people))
	for _, person := range people {
		asked = append(asked, recipient{SubjectIssuer: person.SubjectIssuer, Subject: person.Subject})
	}
	body, err := json.Marshal(map[string]any{"tenantId": tenantID, "siteId": siteID, "alarmId": alarmID, "people": asked})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, iam.baseURL+"/internal/v1/alarm/recipient-decision", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	var answer struct {
		Allowed []recipient `json:"allowed"`
	}
	if _, err := exchangeJSON(iam.client, request, &answer); err != nil {
		return nil, err
	}
	allowed := make([]notificationservice.WeChatPerson, 0, len(answer.Allowed))
	for _, person := range answer.Allowed {
		allowed = append(allowed, notificationservice.WeChatPerson{TenantID: tenantID, SubjectIssuer: person.SubjectIssuer, Subject: person.Subject})
	}
	return allowed, nil
}

// registrySites reads a Site as the Gateway's own Workload Principal (ADR 0017), holding one
// short-lived Registry grant per Tenant.
type registrySites struct {
	iamURL     string
	iamClient  *http.Client
	coreURL    string
	coreClient *http.Client
	mu         sync.Mutex
	grants     map[string]registryGrant
}

type registryGrant struct {
	token     string
	expiresAt time.Time
}

func (sites *registrySites) Site(ctx context.Context, tenantID, siteID string) (notificationservice.SiteFacts, error) {
	grant, err := sites.grant(ctx, tenantID)
	if err != nil {
		return notificationservice.SiteFacts{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sites.coreURL+"/internal/v1/registry/sites/"+url.PathEscape(siteID), nil)
	if err != nil {
		return notificationservice.SiteFacts{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Delegation-Grant", grant)
	var site struct {
		DisplayName string `json:"displayName"`
		Timezone    string `json:"timezone"`
	}
	if _, err := exchangeJSON(sites.coreClient, request, &site); err != nil {
		return notificationservice.SiteFacts{}, fmt.Errorf("read the Site from the Registry: %w", err)
	}
	return notificationservice.SiteFacts{Name: site.DisplayName, Timezone: site.Timezone}, nil
}

func (sites *registrySites) grant(ctx context.Context, tenantID string) (string, error) {
	sites.mu.Lock()
	defer sites.mu.Unlock()
	if held, ok := sites.grants[tenantID]; ok && time.Now().Add(10*time.Second).Before(held.expiresAt) {
		return held.token, nil
	}
	body, err := json.Marshal(registryauth.DecisionRequest{TenantID: tenantID, Action: registryauth.ActionSiteRead})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, sites.iamURL+"/internal/v1/registry/workload-decision", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	var answer registryauth.DecisionResponse
	if _, err := exchangeJSON(sites.iamClient, request, &answer); err != nil {
		return "", fmt.Errorf("ask IAM for a Registry grant: %w", err)
	}
	if !answer.Decision.Allowed {
		return "", fmt.Errorf("IAM does not let the Gateway workload read Sites of Tenant %s: %s", tenantID, answer.Decision.ReasonCode)
	}
	expiresAt, err := time.Parse(time.RFC3339, answer.DelegationGrantExpiresAt)
	if err != nil {
		return "", fmt.Errorf("decode the Registry grant's expiry: %w", err)
	}
	sites.grants[tenantID] = registryGrant{token: answer.DelegationGrant, expiresAt: expiresAt}
	return answer.DelegationGrant, nil
}

// identityWeChatSender asks identity-service to send, with a grant the Gateway's workload signs
// for itself naming the one account.
type identityWeChatSender struct {
	baseURL        string
	client         *http.Client
	signer         crypto.Signer
	workload       string
	policyRevision string
}

func (sender identityWeChatSender) SendWeChatMessage(ctx context.Context, person notificationservice.WeChatPerson, message notificationservice.WeChatMessage) error {
	now := time.Now().UTC()
	tokenID := make([]byte, 12)
	if _, err := rand.Read(tokenID); err != nil {
		return err
	}
	grant, err := identitycontext.SignDelegation(sender.signer, identitycontext.DelegationClaims{
		Issuer: sender.workload, ExecutingService: sender.workload, Subject: sender.workload,
		SubjectIssuer: "spiffe://" + strings.SplitN(strings.TrimPrefix(sender.workload, "spiffe://"), "/", 2)[0],
		Audience:      "identity-service", TenantID: person.TenantID, Actions: []string{"wechat:send"},
		Scopes: []string{"account:" + person.Subject}, PolicyRevision: sender.policyRevision,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: hex.EncodeToString(tokenID),
	})
	if err != nil {
		return err
	}
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, sender.baseURL+"/app/wechat/messages", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Delegation-Grant", grant)
	response, err := sender.client.Do(request)
	if err != nil {
		return errors.New("identity-service could not be reached")
	}
	defer response.Body.Close()
	var refusal struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&refusal)
	switch {
	case response.StatusCode == http.StatusNoContent:
		return nil
	case refusal.Error == "wechat_reminder_refused":
		return notificationservice.ErrWeChatRefused
	case refusal.Error == "wechat_not_bound":
		return notificationservice.ErrWeChatNotBound
	default:
		return fmt.Errorf("identity-service answered %d %s", response.StatusCode, refusal.Error)
	}
}
