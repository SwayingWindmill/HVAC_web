// Command wecom-alert-adapter turns Alertmanager webhook posts into 企业微信 group-robot messages,
// and sends energy-api's Alarm messages (POST /send, send.go) by mail, WeCom app or group robot.
// The robot URL stays in a file on the host. When that file is missing, the process stays up and drops posts.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	os.Exit(run())
}

func run() int {
	health := flag.Bool("healthcheck", false, "exit 0 when /healthz on this address answers")
	addr := flag.String("addr", envOr("WECOM_ADAPTER_ADDR", ":9095"), "listen address")
	urlFile := flag.String("webhook-url-file", envOr("WECOM_WEBHOOK_URL_FILE", "/etc/alert-receiver/wecom-webhook-url"), "file containing the WeCom robot webhook URL")
	sendTokenFile := flag.String("send-token-file", envOr("ALARM_SEND_TOKEN_FILE", "/etc/alarm-delivery/alarm-send-token"), "file containing the bearer token energy-api presents to POST /send")
	recipientsFile := flag.String("recipients-file", envOr("ALARM_DELIVERY_RECIPIENTS_FILE", "/etc/alarm-delivery/recipients.json"), "file listing allowed alarm delivery recipients")
	receiverDir := flag.String("receiver-dir", envOr("ALERT_RECEIVER_DIR", "/etc/alert-receiver"), "directory with smtp.env, smtp-password, wecom-app.env and wecom-app-secret")
	flag.Parse()

	logger := newJSONLogger(os.Stdout, slog.LevelInfo)
	if *health {
		return healthcheck(*addr, logger)
	}

	client := newBoundedHTTPClient(8*time.Second, refuseRedirect)
	send := newSender(*sendTokenFile, *recipientsFile, *receiverDir, *urlFile, client)
	send.logger = logger
	server := &http.Server{
		Addr:              *addr,
		Handler:           newHandlerWithLogger(*urlFile, client, send, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := serveWithGracefulShutdown(sigCtx, server, logger, 10*time.Second); err != nil {
		return 1
	}
	return 0
}

func serveWithGracefulShutdown(ctx context.Context, server *http.Server, logger *slog.Logger, shutdownTimeout time.Duration) error {
	if logger == nil {
		logger = newJSONLogger(os.Stdout, slog.LevelInfo)
	}
	serveErr := make(chan error, 1)
	var httpWG sync.WaitGroup
	httpWG.Add(1)
	go func() {
		defer httpWG.Done()
		logger.Info("wecom_alert_adapter_listening", "address", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	var runErr error
	select {
	case <-ctx.Done():
		logger.Info("wecom_alert_adapter_shutting_down")
	case err := <-serveErr:
		logger.Error("wecom_alert_adapter_stopped_unexpectedly", "error_code", "WECOM_ADAPTER_SERVE_FAILED", "cause", err.Error())
		runErr = err
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warn("wecom_alert_adapter_shutdown_failed", "error_code", "WECOM_ADAPTER_SHUTDOWN_FAILED", "cause", err.Error())
		_ = server.Close()
	}
	httpWG.Wait()
	logger.Info("wecom_alert_adapter_stopped")
	return runErr
}

func newJSONLogger(writer io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redactLogAttribute,
	}))
}

func redactLogAttribute(_ []string, attribute slog.Attr) slog.Attr {
	key := strings.ReplaceAll(strings.ToLower(attribute.Key), "-", "_")
	for _, fragment := range []string{
		"authorization",
		"cookie",
		"token",
		"grant",
		"secret",
		"password",
		"body",
		"connection_string",
		"database_url",
		"dsn",
		"api_key",
		"apikey",
		"private_key",
		"privatekey",
		"credential",
	} {
		if strings.Contains(key, fragment) {
			return slog.String(attribute.Key, "[REDACTED]")
		}
	}
	if key == "error" {
		return slog.String(attribute.Key, "[REDACTED]")
	}
	return attribute
}

func newBoundedHTTPClient(timeout time.Duration, checkRedirect func(*http.Request, []*http.Request) error) *http.Client {
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.DialContext = (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.IdleConnTimeout = 90 * time.Second
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 16
	return &http.Client{
		Timeout:       timeout,
		Transport:     transport,
		CheckRedirect: checkRedirect,
	}
}

func drainAndCloseBody(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64<<10))
	_ = body.Close()
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func refuseRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func healthcheck(addr string, logger *slog.Logger) int {
	if logger == nil {
		logger = newJSONLogger(os.Stdout, slog.LevelInfo)
	}
	host := addr
	if strings.HasPrefix(addr, ":") {
		host = "127.0.0.1" + addr
	}
	client := newBoundedHTTPClient(2*time.Second, refuseRedirect)
	response, err := client.Get("http://" + host + "/healthz")
	if err != nil {
		logger.Error("wecom_healthcheck_failed", "error_code", "WECOM_HEALTHCHECK_FAILED", "cause", err.Error())
		return 1
	}
	defer drainAndCloseBody(response.Body)
	if response.StatusCode != http.StatusOK {
		logger.Error("wecom_healthcheck_bad_status", "error_code", "WECOM_HEALTHCHECK_STATUS", "status", response.StatusCode)
		return 1
	}
	return 0
}

func newHandler(urlFile string, client *http.Client, send *sender) http.Handler {
	var logger *slog.Logger
	if send != nil {
		logger = send.logger
	}
	return newHandlerWithLogger(urlFile, client, send, logger)
}

func newHandlerWithLogger(urlFile string, client *http.Client, send *sender, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = newJSONLogger(os.Stdout, slog.LevelInfo)
	}
	mux := http.NewServeMux()
	if send != nil {
		mux.HandleFunc("POST /send", send.serve)
	}
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok\n"))
	})
	mux.HandleFunc("POST /alertmanager", func(writer http.ResponseWriter, request *http.Request) {
		deliverWithLogger(writer, request, urlFile, client, logger)
	})
	return mux
}

func deliver(writer http.ResponseWriter, request *http.Request, urlFile string, client *http.Client) {
	deliverWithLogger(writer, request, urlFile, client, newJSONLogger(os.Stdout, slog.LevelInfo))
}

func deliverWithLogger(writer http.ResponseWriter, request *http.Request, urlFile string, client *http.Client, logger *slog.Logger) {
	if logger == nil {
		logger = newJSONLogger(os.Stdout, slog.LevelInfo)
	}
	webhookURL, err := readWebhookURL(urlFile)
	if err != nil {
		logger.Error("wecom_webhook_file_invalid", "error_code", "WECOM_WEBHOOK_FILE_INVALID", "cause", err.Error())
		http.Error(writer, "wecom webhook file is invalid", http.StatusInternalServerError)
		return
	}
	if webhookURL == "" {
		logger.Info("alertmanager_webhook_dropped", "reason", "wecom_receiver_not_configured")
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if err != nil {
		http.Error(writer, "read body", http.StatusBadRequest)
		return
	}
	text, err := formatAlerts(body)
	if err != nil {
		http.Error(writer, "alertmanager payload", http.StatusBadRequest)
		return
	}
	payload, err := json.Marshal(map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": text},
	})
	if err != nil {
		http.Error(writer, "encode", http.StatusInternalServerError)
		return
	}
	outbound, err := http.NewRequestWithContext(request.Context(), http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		http.Error(writer, "request", http.StatusInternalServerError)
		return
	}
	outbound.Header.Set("Content-Type", "application/json")
	response, err := client.Do(outbound)
	if err != nil {
		logger.Warn("wecom_post_failed", "error_code", "WECOM_POST_FAILED")
		http.Error(writer, "wecom unreachable", http.StatusBadGateway)
		return
	}
	defer drainAndCloseBody(response.Body)
	reply, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		logger.Warn("wecom_http_error", "error_code", "WECOM_HTTP_STATUS", "status", response.StatusCode)
		http.Error(writer, "wecom rejected the post", http.StatusBadGateway)
		return
	}
	var parsed struct {
		ErrCode int `json:"errcode"`
	}
	if json.Unmarshal(reply, &parsed) == nil && parsed.ErrCode != 0 {
		logger.Warn("wecom_errcode_rejected", "error_code", "WECOM_ERRCODE", "wecom_errcode", parsed.ErrCode)
		http.Error(writer, "wecom rejected the message", http.StatusBadGateway)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func readWebhookURL(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	url := strings.TrimSpace(string(body))
	if url == "" {
		return "", nil
	}
	const prefix = "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key="
	if !strings.HasPrefix(url, prefix) || strings.ContainsAny(url, " \t\r\n") || len(url) == len(prefix) {
		return "", fmt.Errorf("refusing a URL that is not a WeCom group-robot webhook")
	}
	return url, nil
}

type alertWebhook struct {
	Status string `json:"status"`
	Alerts []struct {
		Status      string            `json:"status"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	} `json:"alerts"`
}

func formatAlerts(body []byte) (string, error) {
	var payload alertWebhook
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if len(payload.Alerts) == 0 {
		return truncateUTF8("【告警】"+payload.Status+"（没有明细）", 1800), nil
	}
	var builder strings.Builder
	_, _ = fmt.Fprintf(&builder, "【告警】%s 共 %d 条\n", payload.Status, len(payload.Alerts))
	limit := len(payload.Alerts)
	if limit > 8 {
		limit = 8
	}
	for _, alert := range payload.Alerts[:limit] {
		name := alert.Labels["alertname"]
		if name == "" {
			name = "未命名"
		}
		severity := alert.Labels["severity"]
		if severity == "" {
			severity = "unknown"
		}
		summary := alert.Annotations["summary"]
		if summary == "" {
			summary = alert.Annotations["description"]
		}
		_, _ = fmt.Fprintf(&builder, "- %s [%s] %s\n", name, severity, oneLine(summary))
		if runbook := alert.Annotations["runbook"]; runbook != "" {
			_, _ = fmt.Fprintf(&builder, "  处理：%s\n", oneLine(runbook))
		}
	}
	if len(payload.Alerts) > limit {
		_, _ = fmt.Fprintf(&builder, "…其余 %d 条见 Alertmanager\n", len(payload.Alerts)-limit)
	}
	return truncateUTF8(builder.String(), 1800), nil
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && value[cut]&0xC0 == 0x80 {
		cut--
	}
	return value[:cut] + "\n…(截断)"
}
