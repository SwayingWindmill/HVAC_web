package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const defaultWeChatMPAPIBase = "https://api.weixin.qq.com"

var wechatMPTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type wechatMPConfig struct {
	AppID       string
	AppSecret   string
	TemplateID  string
	OpenIDs     []string
	ClickURL    string
	APIBase     string
	KeyTitle    string
	KeySeverity string
	KeySummary  string
	KeyTime     string
}

type wechatMPDeliverer struct {
	receiverDir string
	client      *http.Client
	now         func() time.Time

	mu       sync.Mutex
	token    string
	tokenKey string
	until    time.Time
}

func newWeChatMPDeliverer(receiverDir string, client *http.Client) *wechatMPDeliverer {
	return &wechatMPDeliverer{
		receiverDir: receiverDir,
		client:      client,
		now:         time.Now,
	}
}

func (d *wechatMPDeliverer) loadConfig() (*wechatMPConfig, error) {
	if d == nil || strings.TrimSpace(d.receiverDir) == "" {
		return nil, nil
	}
	envPath := filepath.Join(d.receiverDir, "wechat-mp.env")
	values, err := readEnvFilePath(envPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errNotConfigured) {
			return nil, nil
		}
		return nil, err
	}
	secret, err := readSecretPath(filepath.Join(d.receiverDir, "wechat-mp-secret"))
	if err != nil {
		return nil, fmt.Errorf("wechat-mp-secret is missing or unreadable")
	}
	if strings.ContainsAny(secret, " \t\r\n") {
		return nil, fmt.Errorf("wechat-mp-secret must not contain whitespace")
	}

	appID := strings.TrimSpace(values["WECHAT_MP_APP_ID"])
	templateID := strings.TrimSpace(values["WECHAT_MP_TEMPLATE_ID"])
	rawOpenIDs := strings.TrimSpace(values["WECHAT_MP_OPENIDS"])
	clickURL := strings.TrimSpace(values["WECHAT_MP_URL"])
	apiBase := strings.TrimSpace(values["WECHAT_MP_API_BASE"])

	if !wechatMPTokenPattern.MatchString(appID) {
		return nil, fmt.Errorf("WECHAT_MP_APP_ID is required")
	}
	if !wechatMPTokenPattern.MatchString(templateID) {
		return nil, fmt.Errorf("WECHAT_MP_TEMPLATE_ID is required")
	}
	openIDs, err := parseWeChatMPOpenIDs(rawOpenIDs)
	if err != nil {
		return nil, err
	}
	if clickURL != "" {
		parsedClick, parseErr := url.Parse(clickURL)
		if parseErr != nil || parsedClick.Scheme != "https" || parsedClick.Host == "" {
			return nil, fmt.Errorf("WECHAT_MP_URL must be an https URL")
		}
	}
	validatedBase, err := validateWeChatMPAPIBase(apiBase)
	if err != nil {
		return nil, err
	}

	return &wechatMPConfig{
		AppID:       appID,
		AppSecret:   secret,
		TemplateID:  templateID,
		OpenIDs:     openIDs,
		ClickURL:    clickURL,
		APIBase:     validatedBase,
		KeyTitle:    resolveTemplateKey(values["WECHAT_MP_KEY_TITLE"], "title"),
		KeySeverity: resolveTemplateKey(values["WECHAT_MP_KEY_SEVERITY"], "severity"),
		KeySummary:  resolveTemplateKey(values["WECHAT_MP_KEY_SUMMARY"], "summary"),
		KeyTime:     resolveTemplateKey(values["WECHAT_MP_KEY_TIME"], "time"),
	}, nil
}

func parseWeChatMPOpenIDs(raw string) ([]string, error) {
	if raw == "" {
		return nil, fmt.Errorf("WECHAT_MP_OPENIDS is required")
	}
	parts := strings.Split(raw, ",")
	openIDs := make([]string, 0, len(parts))
	for _, part := range parts {
		id := strings.TrimSpace(part)
		if !wechatMPTokenPattern.MatchString(id) {
			return nil, fmt.Errorf("WECHAT_MP_OPENIDS contains an invalid entry")
		}
		openIDs = append(openIDs, id)
	}
	return openIDs, nil
}

func validateWeChatMPAPIBase(raw string) (string, error) {
	if raw == "" {
		return defaultWeChatMPAPIBase, nil
	}
	cleaned := strings.TrimRight(raw, "/")
	parsed, err := url.Parse(cleaned)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("WECHAT_MP_API_BASE is invalid")
	}
	if (parsed.Scheme == "https" && parsed.Hostname() == "api.weixin.qq.com") ||
		(parsed.Scheme == "http" && parsed.Hostname() == "127.0.0.1") {
		return cleaned, nil
	}
	return "", fmt.Errorf("WECHAT_MP_API_BASE must target https://api.weixin.qq.com")
}

func resolveTemplateKey(override, fallback string) string {
	trimmed := strings.TrimSpace(override)
	if trimmed == "" {
		return fallback
	}
	if trimmed == "-" {
		return ""
	}
	return trimmed
}

func (d *wechatMPDeliverer) deliverAlerts(ctx context.Context, cfg *wechatMPConfig, body []byte, logger *slog.Logger) error {
	if cfg == nil {
		return nil
	}
	if logger == nil {
		logger = newJSONLogger(os.Stdout, slog.LevelInfo)
	}
	data, err := formatWeChatMPTemplateData(body, cfg, d.now())
	if err != nil {
		return fmt.Errorf("parse alertmanager payload: %w", err)
	}

	var failures int
	for _, openID := range cfg.OpenIDs {
		if err := d.sendOne(ctx, cfg, openID, data); err != nil {
			failures++
			logger.Warn("wechat_mp_send_failed", "error_code", "WECHAT_MP_SEND_FAILED", "cause", err.Error())
		}
	}
	if failures > 0 {
		return fmt.Errorf("wechat-mp failed for %d of %d recipient(s)", failures, len(cfg.OpenIDs))
	}
	logger.Info("wechat_mp_send_succeeded", "recipient_count", len(cfg.OpenIDs))
	return nil
}

func (d *wechatMPDeliverer) sendOne(ctx context.Context, cfg *wechatMPConfig, openID string, data map[string]map[string]string) error {
	payload := map[string]any{
		"touser":      openID,
		"template_id": cfg.TemplateID,
		"data":        data,
	}
	if cfg.ClickURL != "" {
		payload["url"] = cfg.ClickURL
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	for attempt := 0; attempt < 2; attempt++ {
		token, err := d.accessToken(ctx, cfg, attempt > 0)
		if err != nil {
			return err
		}
		reply, err := d.postTemplate(ctx, cfg.APIBase+"/cgi-bin/message/template/send?access_token="+url.QueryEscape(token), encoded)
		if err != nil {
			return err
		}
		switch reply.ErrCode {
		case 0:
			return nil
		case 40001, 40014, 42001:
			continue
		default:
			return fmt.Errorf("wechat-mp template errcode %d", reply.ErrCode)
		}
	}
	return errors.New("wechat-mp kept rejecting the access token")
}

type wechatMPAPIResponse struct {
	ErrCode int `json:"errcode"`
}

func (d *wechatMPDeliverer) postTemplate(ctx context.Context, endpoint string, encoded []byte) (wechatMPAPIResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(encoded)))
	if err != nil {
		return wechatMPAPIResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return wechatMPAPIResponse{}, errors.New("wechat-mp API unreachable")
	}
	defer drainAndCloseBody(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return wechatMPAPIResponse{}, fmt.Errorf("wechat-mp http status %d", resp.StatusCode)
	}
	var reply wechatMPAPIResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&reply); err != nil {
		return wechatMPAPIResponse{}, fmt.Errorf("wechat-mp invalid response json")
	}
	return reply, nil
}

func (d *wechatMPDeliverer) accessToken(ctx context.Context, cfg *wechatMPConfig, refresh bool) (string, error) {
	cacheKey := cfg.APIBase + "|" + cfg.AppID + "|" + cfg.AppSecret
	d.mu.Lock()
	defer d.mu.Unlock()
	if !refresh && d.token != "" && d.tokenKey == cacheKey && d.now().Before(d.until) {
		return d.token, nil
	}

	endpoint := cfg.APIBase + "/cgi-bin/token?grant_type=client_credential&appid=" +
		url.QueryEscape(cfg.AppID) + "&secret=" + url.QueryEscape(cfg.AppSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return "", errors.New("wechat-mp token endpoint unreachable")
	}
	defer drainAndCloseBody(resp.Body)

	var reply struct {
		ErrCode     int    `json:"errcode"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&reply) != nil {
		return "", fmt.Errorf("wechat-mp token http status %d", resp.StatusCode)
	}
	if reply.ErrCode != 0 || reply.AccessToken == "" {
		return "", fmt.Errorf("wechat-mp token errcode %d", reply.ErrCode)
	}
	lifetime := time.Duration(reply.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = 2 * time.Hour
	}
	margin := 5 * time.Minute
	if lifetime <= margin {
		margin = lifetime / 2
	}
	d.token = reply.AccessToken
	d.tokenKey = cacheKey
	d.until = d.now().Add(lifetime - margin)
	return d.token, nil
}

func formatWeChatMPTemplateData(body []byte, cfg *wechatMPConfig, now time.Time) (map[string]map[string]string, error) {
	var payload alertWebhook
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	statusLabel := strings.ToUpper(oneLine(payload.Status))
	if statusLabel == "" {
		statusLabel = "FIRING"
	}

	title := fmt.Sprintf("[%s] 平台告警 (%d)", statusLabel, len(payload.Alerts))
	severity := "page"
	summary := "没有告警明细"
	if len(payload.Alerts) > 0 {
		first := payload.Alerts[0]
		name := oneLine(first.Labels["alertname"])
		if name == "" {
			name = "未命名告警"
		}
		if len(payload.Alerts) == 1 {
			title = fmt.Sprintf("[%s] %s", statusLabel, name)
		} else {
			title = fmt.Sprintf("[%s] %s 等%d条", statusLabel, name, len(payload.Alerts))
		}
		if sev := oneLine(first.Labels["severity"]); sev != "" {
			severity = sev
		}
		parts := make([]string, 0, min(len(payload.Alerts), 3))
		for i, a := range payload.Alerts {
			if i >= 3 {
				break
			}
			line := oneLine(a.Annotations["summary"])
			if line == "" {
				line = oneLine(a.Annotations["description"])
			}
			if line == "" {
				line = oneLine(a.Labels["alertname"])
			}
			if line != "" {
				parts = append(parts, line)
			}
		}
		if len(parts) > 0 {
			summary = strings.Join(parts, "；")
		}
	}

	cst := time.FixedZone("CST", 8*3600)
	timestamp := now.In(cst).Format("2006-01-02 15:04:05")

	data := make(map[string]map[string]string, 4)
	setTemplateField(data, cfg.KeyTitle, title)
	setTemplateField(data, cfg.KeySeverity, severity)
	setTemplateField(data, cfg.KeySummary, summary)
	setTemplateField(data, cfg.KeyTime, timestamp)
	return data, nil
}

func setTemplateField(data map[string]map[string]string, key, value string) {
	if key == "" {
		return
	}
	limit := 120
	if strings.HasPrefix(key, "thing") {
		limit = 20
	}
	data[key] = map[string]string{"value": truncateRunes(value, limit)}
}

func truncateRunes(value string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	if maxRunes <= 1 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-1]) + "…"
}
