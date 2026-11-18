package main

// POST /send: one Alarm message from energy-api's Alarm delivery (P0-4,
// docs/specs/alarm-delivery.md) by mail (SMTP, QQ mail first), as a WeCom app message to one
// person, or to the WeCom group robot. The secrets stay in files under the receiver directory;
// energy-api only holds a bearer token. Nothing here logs an address, a user id or a message.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type sendRequest struct {
	Channel string `json:"channel"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	DryRun  bool   `json:"dryRun"`
	Key     string `json:"key"`
}

// sender holds where the /send secrets are; it reads them at each request, so a rotated secret
// needs no restart.
type sender struct {
	tokenFile      string
	recipientsFile string
	receiverDir    string
	urlFile        string
	client         *http.Client
	logger         *slog.Logger
	// dialSMTP opens the connection to the mail server; tests replace it.
	dialSMTP func(ctx context.Context, host, port string) (net.Conn, error)
	now      func() time.Time

	mu         sync.Mutex
	wecomToken string
	wecomUntil time.Time
}

func (s *sender) log() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return newJSONLogger(os.Stdout, slog.LevelInfo)
}

var (
	errNotConfigured = errors.New("channel not configured")
	errRejected      = errors.New("recipient rejected")
)

func newSender(tokenFile, recipientsFile, receiverDir, urlFile string, client *http.Client) *sender {
	return &sender{
		tokenFile: tokenFile, recipientsFile: recipientsFile, receiverDir: receiverDir, urlFile: urlFile, client: client, now: time.Now,
		dialSMTP: func(ctx context.Context, host, port string) (net.Conn, error) {
			dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}
			return dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
		},
	}
}

func (s *sender) serve(writer http.ResponseWriter, request *http.Request) {
	token, err := os.ReadFile(s.tokenFile)
	if err != nil || strings.TrimSpace(string(token)) == "" {
		s.log().Warn("alarm_send_token_missing", "error_code", "ALARM_SEND_TOKEN_MISSING")
		http.Error(writer, "send is not configured", http.StatusServiceUnavailable)
		return
	}
	given := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(given), []byte(strings.TrimSpace(string(token)))) != 1 {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	var message sendRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil || strings.TrimSpace(message.Subject) == "" || strings.TrimSpace(message.Text) == "" ||
		strings.ContainsAny(message.Subject, "\r\n") || len(message.Key) > 700 {
		http.Error(writer, "invalid message", http.StatusBadRequest)
		return
	}
	key := oneLine(message.Key)
	switch message.Channel {
	case "smtp":
		err = s.sendMail(request.Context(), message)
	case "wecom_app":
		err = s.sendWeComApp(request.Context(), message)
	case "wecom_bot":
		err = s.sendWeComBot(request.Context(), message)
	default:
		http.Error(writer, "unknown channel", http.StatusBadRequest)
		return
	}
	switch {
	case err == nil:
		s.log().Info("alarm_send_succeeded", "channel", message.Channel, "key", key, "dry_run", message.DryRun)
		writer.WriteHeader(http.StatusNoContent)
	case errors.Is(err, errNotConfigured):
		s.log().Warn("alarm_send_unconfigured", "channel", message.Channel, "key", key, "error_code", "ALARM_CHANNEL_UNCONFIGURED", "cause", err.Error())
		http.Error(writer, "channel not configured", http.StatusNotImplemented)
	case errors.Is(err, errRejected):
		s.log().Warn("alarm_send_rejected", "channel", message.Channel, "key", key, "error_code", "ALARM_RECIPIENT_REJECTED", "cause", err.Error())
		http.Error(writer, "recipient rejected", http.StatusUnprocessableEntity)
	default:
		s.log().Error("alarm_send_failed", "channel", message.Channel, "key", key, "error_code", "ALARM_SEND_FAILED", "cause", err.Error())
		http.Error(writer, "upstream failed", http.StatusBadGateway)
	}
}

// readEnvFile reads KEY=VALUE lines (# comments) from the receiver directory.
func (s *sender) readEnvFile(name string) (map[string]string, error) {
	body, err := os.ReadFile(filepath.Join(s.receiverDir, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s is missing", errNotConfigured, name)
		}
		return nil, err
	}
	values := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(name)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return values, nil
}

func (s *sender) readSecret(name string) (string, error) {
	body, err := os.ReadFile(filepath.Join(s.receiverDir, name))
	if err != nil || strings.TrimSpace(string(body)) == "" {
		return "", fmt.Errorf("%w: %s is missing or empty", errNotConfigured, name)
	}
	return strings.TrimSpace(string(body)), nil
}

// sendMail sends over implicit TLS (port 465, as QQ mail and Alertmanager's smtp.env use) or
// STARTTLS (any other port; a server without STARTTLS is refused), with the same smtp.env and
// smtp-password as Alertmanager.
func (s *sender) sendMail(ctx context.Context, message sendRequest) error {
	env, err := s.readEnvFile("smtp.env")
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(env["SMTP_SMARTHOST"])
	if err != nil || env["SMTP_FROM"] == "" || env["SMTP_AUTH_USERNAME"] == "" {
		return fmt.Errorf("%w: smtp.env needs SMTP_SMARTHOST host:port, SMTP_FROM and SMTP_AUTH_USERNAME", errNotConfigured)
	}
	password, err := s.readSecret("smtp-password")
	if err != nil {
		return err
	}
	to, err := s.resolveMailRecipient(message.To)
	if err != nil {
		return err
	}
	if message.DryRun {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, err := s.dialSMTP(ctx, host, port)
	if err != nil {
		return fmt.Errorf("dial mail server: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mail server greeting: %w", err)
	}
	defer client.Close()
	if port != "465" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("%w: the mail server offers no STARTTLS", errNotConfigured)
		}
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if err := client.Auth(smtp.PlainAuth("", env["SMTP_AUTH_USERNAME"], password, host)); err != nil {
		return fmt.Errorf("%w: mail login refused", errNotConfigured)
	}
	if err := client.Mail(env["SMTP_FROM"]); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		var protocol *textproto.Error
		if errors.As(err, &protocol) && protocol.Code >= 500 {
			return fmt.Errorf("%w: mail server refused the recipient", errRejected)
		}
		return fmt.Errorf("rcpt: %w", err)
	}
	data, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := data.Write(mailBody(env["SMTP_FROM"], to, message.Subject, message.Text, s.now())); err != nil {
		return fmt.Errorf("write mail: %w", err)
	}
	if err := data.Close(); err != nil {
		return fmt.Errorf("mail not accepted: %w", err)
	}
	_ = client.Quit()
	return nil
}

func (s *sender) resolveMailRecipient(requested string) (string, error) {
	want := strings.TrimSpace(requested)
	if want == "" || !strings.Contains(want, "@") || strings.ContainsAny(want, " \r\n<>,") {
		return "", fmt.Errorf("%w: invalid mail address", errRejected)
	}
	parsedWant, err := mail.ParseAddress(want)
	if err != nil || parsedWant.Address != want {
		return "", fmt.Errorf("%w: invalid mail address", errRejected)
	}
	body, err := os.ReadFile(s.recipientsFile)
	if err != nil {
		return "", fmt.Errorf("%w: recipients file is missing or unreadable", errNotConfigured)
	}
	var directory struct {
		Recipients map[string]struct {
			Email string `json:"email"`
		} `json:"recipients"`
	}
	if err := json.Unmarshal(body, &directory); err != nil {
		return "", fmt.Errorf("%w: recipients file is invalid JSON", errNotConfigured)
	}
	for _, entry := range directory.Recipients {
		candidate := strings.TrimSpace(entry.Email)
		if candidate == "" || strings.ContainsAny(candidate, " \r\n<>,") {
			continue
		}
		parsed, err := mail.ParseAddress(candidate)
		if err != nil || parsed.Address != candidate {
			continue
		}
		if strings.EqualFold(parsed.Address, parsedWant.Address) {
			return parsed.Address, nil
		}
	}
	return "", fmt.Errorf("%w: recipient is not in recipients directory", errRejected)
}

// encodeRFC2047Base64 encodes every Subject chunk unconditionally with base64 UTF-8
// (<= 45 raw bytes -> <= 72 ASCII header chars per encoded-word, RFC 2047) so raw
// request text never appears directly in SMTP headers.
func encodeRFC2047Base64(value string) string {
	cleaned := strings.NewReplacer("\r", "", "\n", "", "\x00", "").Replace(strings.TrimSpace(value))
	if cleaned == "" {
		return ""
	}
	const maxChunkBytes = 45
	var words []string
	var chunk strings.Builder
	for _, r := range cleaned {
		size := utf8.RuneLen(r)
		if chunk.Len() > 0 && chunk.Len()+size > maxChunkBytes {
			words = append(words, "=?UTF-8?b?"+base64.StdEncoding.EncodeToString([]byte(chunk.String()))+"?=")
			chunk.Reset()
		}
		chunk.WriteRune(r)
	}
	if chunk.Len() > 0 {
		words = append(words, "=?UTF-8?b?"+base64.StdEncoding.EncodeToString([]byte(chunk.String()))+"?=")
	}
	return strings.Join(words, "\r\n ")
}

func mailBody(from, to, subject, text string, now time.Time) []byte {
	var id [12]byte
	_, _ = rand.Read(id[:])
	domain := "hvac.local"
	if at := strings.LastIndex(from, "@"); at >= 0 {
		domain = strings.Trim(from[at+1:], "> ")
	}
	var body bytes.Buffer
	fmt.Fprintf(&body, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@%s>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n",
		from, to, encodeRFC2047Base64(subject), now.Format(time.RFC1123Z), hex.EncodeToString(id[:]), domain)
	normalized := strings.ReplaceAll(strings.NewReplacer("\x00", "", "\r\n", "\n", "\r", "\n").Replace(text), "\n", "\r\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(normalized))
	for len(encoded) > 76 {
		body.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	body.WriteString(encoded + "\r\n")
	return body.Bytes()
}

// sendWeComApp sends a text message from the 企业微信 self-built app to one member. The server's
// public IP must be among the app's trusted IPs (企业可信IP), or WeCom answers 60020.
func (s *sender) sendWeComApp(ctx context.Context, message sendRequest) error {
	env, err := s.readEnvFile("wecom-app.env")
	if err != nil {
		return err
	}
	agentID, err := strconv.Atoi(env["WECOM_AGENT_ID"])
	if err != nil || env["WECOM_CORP_ID"] == "" {
		return fmt.Errorf("%w: wecom-app.env needs WECOM_CORP_ID and a numeric WECOM_AGENT_ID", errNotConfigured)
	}
	secret, err := s.readSecret("wecom-app-secret")
	if err != nil {
		return err
	}
	to := strings.TrimSpace(message.To)
	if to == "" || strings.ContainsAny(to, " \r\n|") {
		return fmt.Errorf("%w: invalid WeCom user id", errRejected)
	}
	if message.DryRun {
		return nil
	}
	base := strings.TrimRight(env["WECOM_API_BASE"], "/")
	if base == "" {
		base = "https://qyapi.weixin.qq.com"
	}
	if !strings.HasPrefix(base, "https://") && !strings.HasPrefix(base, "http://127.0.0.1") {
		return fmt.Errorf("%w: WECOM_API_BASE must be https", errNotConfigured)
	}
	payload, _ := json.Marshal(map[string]any{
		"touser": to, "msgtype": "text", "agentid": agentID, "safe": 0,
		"text": map[string]string{"content": truncateUTF8(message.Subject+"\n"+strings.TrimPrefix(message.Text, message.Subject+"\n"), 2000)},
	})
	for attempt := 0; attempt < 2; attempt++ {
		token, err := s.wecomAccessToken(ctx, base, env["WECOM_CORP_ID"], secret, attempt > 0)
		if err != nil {
			return err
		}
		var reply struct {
			ErrCode     int    `json:"errcode"`
			InvalidUser string `json:"invaliduser"`
		}
		if err := s.postJSON(ctx, base+"/cgi-bin/message/send?access_token="+url.QueryEscape(token), payload, &reply); err != nil {
			return err
		}
		switch {
		case reply.ErrCode == 40014 || reply.ErrCode == 42001 || reply.ErrCode == 40001:
			continue
		case reply.ErrCode == 60020:
			return fmt.Errorf("%w: WeCom 60020, this server's IP is not a trusted IP of the app", errNotConfigured)
		case reply.ErrCode == 81013 || reply.ErrCode == 40031 || reply.InvalidUser != "":
			return fmt.Errorf("%w: WeCom does not know the user", errRejected)
		case reply.ErrCode != 0:
			return fmt.Errorf("WeCom errcode %d", reply.ErrCode)
		}
		return nil
	}
	return errors.New("WeCom kept refusing the access token")
}

func (s *sender) wecomAccessToken(ctx context.Context, base, corpID, secret string, refresh bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !refresh && s.wecomToken != "" && s.now().Before(s.wecomUntil) {
		return s.wecomToken, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/cgi-bin/gettoken?corpid="+url.QueryEscape(corpID)+"&corpsecret="+url.QueryEscape(secret), nil)
	if err != nil {
		return "", err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return "", errors.New("WeCom gettoken unreachable")
	}
	defer drainAndCloseBody(response.Body)
	var reply struct {
		ErrCode     int    `json:"errcode"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&reply) != nil {
		return "", fmt.Errorf("WeCom gettoken status %d", response.StatusCode)
	}
	if reply.ErrCode != 0 || reply.AccessToken == "" {
		return "", fmt.Errorf("%w: WeCom gettoken errcode %d (corp id or app secret)", errNotConfigured, reply.ErrCode)
	}
	lifetime := time.Duration(reply.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = 2 * time.Hour
	}
	s.wecomToken, s.wecomUntil = reply.AccessToken, s.now().Add(lifetime-5*time.Minute)
	return s.wecomToken, nil
}

func (s *sender) sendWeComBot(ctx context.Context, message sendRequest) error {
	webhookURL, err := readWebhookURL(s.urlFile)
	if err != nil {
		return fmt.Errorf("%w: %v", errNotConfigured, err)
	}
	if webhookURL == "" {
		return fmt.Errorf("%w: no WeCom group robot", errNotConfigured)
	}
	if message.DryRun {
		return nil
	}
	payload, _ := json.Marshal(map[string]any{"msgtype": "text", "text": map[string]string{"content": truncateUTF8(message.Text, 1800)}})
	var reply struct {
		ErrCode int `json:"errcode"`
	}
	if err := s.postJSON(ctx, webhookURL, payload, &reply); err != nil {
		return err
	}
	if reply.ErrCode != 0 {
		return fmt.Errorf("WeCom robot errcode %d", reply.ErrCode)
	}
	return nil
}

func (s *sender) postJSON(ctx context.Context, target string, payload []byte, into any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return errors.New("WeCom unreachable")
	}
	defer drainAndCloseBody(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("WeCom http status %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(into)
}
