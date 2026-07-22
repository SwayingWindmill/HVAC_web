package iam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxLogtoManagementResponseBytes = 1 << 20

type LogtoManagementConfig struct {
	Endpoint                  string
	ClientID                  string
	ClientSecret              string
	Resource                  string
	Scope                     string
	HTTPClient                *http.Client
	allowInsecureHTTPForTests bool
}

type LogtoManagementClient struct {
	endpoint     string
	clientID     string
	clientSecret string
	resource     string
	scope        string
	httpClient   *http.Client

	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time
	now         func() time.Time
}

type LogtoUser struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	PrimaryEmail string `json:"primaryEmail"`
	Name         string `json:"name"`
	IsSuspended  bool   `json:"isSuspended"`
	UpdatedAt    int64  `json:"updatedAt"`
}

type LogtoOrganization struct {
	ID                string                  `json:"id"`
	Name              string                  `json:"name"`
	Description       string                  `json:"description"`
	OrganizationRoles []LogtoOrganizationRole `json:"organizationRoles"`
}

type LogtoOrganizationRole struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

func NewLogtoManagementClient(config LogtoManagementConfig) (*LogtoManagementClient, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(config.Endpoint), "/")
	if endpoint == "" || strings.TrimSpace(config.ClientID) == "" || strings.TrimSpace(config.ClientSecret) == "" || strings.TrimSpace(config.Resource) == "" || strings.TrimSpace(config.Scope) == "" {
		return nil, errors.New("Logto Management endpoint, M2M credentials, resource and scope are required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Logto Management endpoint must be an absolute HTTPS URL without credentials, query or fragment")
	}
	if parsed.Scheme != "https" && (!config.allowInsecureHTTPForTests || !loopbackHost(parsed.Hostname())) {
		return nil, errors.New("Logto Management endpoint must use HTTPS")
	}
	baseClient := config.HTTPClient
	if baseClient == nil {
		baseClient = &http.Client{Timeout: 10 * time.Second}
	}
	client := *baseClient
	if client.Timeout <= 0 {
		client.Timeout = 10 * time.Second
	}
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &LogtoManagementClient{
		endpoint: endpoint, clientID: strings.TrimSpace(config.ClientID), clientSecret: config.ClientSecret,
		resource: strings.TrimSpace(config.Resource), scope: strings.TrimSpace(config.Scope), httpClient: &client, now: time.Now,
	}, nil
}

func (client *LogtoManagementClient) User(ctx context.Context, userID string) (LogtoUser, error) {
	if client == nil {
		return LogtoUser{}, errors.New("Logto Management client is nil")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return LogtoUser{}, errors.New("Logto user id is required")
	}
	token, err := client.token(ctx)
	if err != nil {
		return LogtoUser{}, err
	}
	endpoint := client.endpoint + "/api/users/" + url.PathEscape(userID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return LogtoUser{}, fmt.Errorf("create Logto user request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return LogtoUser{}, fmt.Errorf("fetch Logto user: %w", err)
	}
	defer response.Body.Close()
	body, err := readBoundedResponse(response.Body)
	if err != nil {
		return LogtoUser{}, fmt.Errorf("read Logto user response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return LogtoUser{}, fmt.Errorf("Logto user endpoint returned HTTP %d", response.StatusCode)
	}
	var user LogtoUser
	if err := json.Unmarshal(body, &user); err != nil {
		return LogtoUser{}, fmt.Errorf("decode Logto user response: %w", err)
	}
	return user, nil
}

func (client *LogtoManagementClient) UserOrganizations(ctx context.Context, userID string) ([]LogtoOrganization, error) {
	if client == nil {
		return nil, errors.New("Logto Management client is nil")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, errors.New("Logto user id is required")
	}
	token, err := client.token(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := client.endpoint + "/api/users/" + url.PathEscape(userID) + "/organizations"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create Logto user organizations request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch Logto user organizations: %w", err)
	}
	defer response.Body.Close()
	body, err := readBoundedResponse(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read Logto user organizations response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Logto user organizations returned HTTP %d", response.StatusCode)
	}
	var organizations []LogtoOrganization
	if err := json.Unmarshal(body, &organizations); err != nil {
		return nil, fmt.Errorf("decode Logto user organizations response: %w", err)
	}
	return organizations, nil
}

func (client *LogtoManagementClient) token(ctx context.Context) (string, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	now := client.now()
	if client.accessToken != "" && now.Add(30*time.Second).Before(client.expiresAt) {
		return client.accessToken, nil
	}
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("resource", client.resource)
	form.Set("scope", client.scope)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint+"/oidc/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create Logto M2M token request: %w", err)
	}
	request.SetBasicAuth(client.clientID, client.clientSecret)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("fetch Logto M2M token: %w", err)
	}
	defer response.Body.Close()
	body, err := readBoundedResponse(response.Body)
	if err != nil {
		return "", fmt.Errorf("read Logto M2M token response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Logto M2M token endpoint returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode Logto M2M token response: %w", err)
	}
	if strings.TrimSpace(payload.AccessToken) == "" || !strings.EqualFold(payload.TokenType, "Bearer") || payload.ExpiresIn <= 0 {
		return "", errors.New("Logto M2M token response is incomplete")
	}
	client.accessToken = payload.AccessToken
	client.expiresAt = now.Add(time.Duration(payload.ExpiresIn) * time.Second)
	return client.accessToken, nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func readBoundedResponse(reader io.Reader) ([]byte, error) {
	limited := io.LimitReader(reader, maxLogtoManagementResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(body) > maxLogtoManagementResponseBytes {
		return nil, errors.New("response exceeded size limit")
	}
	return body, nil
}
