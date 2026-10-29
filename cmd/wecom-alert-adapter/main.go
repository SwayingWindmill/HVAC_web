// Command wecom-alert-adapter turns Alertmanager webhook posts into 企业微信 group-robot messages.
// The robot URL stays in a file on the host. When that file is missing, the process stays up and drops posts.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	health := flag.Bool("healthcheck", false, "exit 0 when /healthz on this address answers")
	addr := flag.String("addr", envOr("WECOM_ADAPTER_ADDR", ":9095"), "listen address")
	urlFile := flag.String("webhook-url-file", envOr("WECOM_WEBHOOK_URL_FILE", "/etc/alert-receiver/wecom-webhook-url"), "file containing the WeCom robot webhook URL")
	flag.Parse()
	if *health {
		os.Exit(healthcheck(*addr))
	}
	server := &http.Server{
		Addr:              *addr,
		Handler:           newHandler(*urlFile, &http.Client{Timeout: 8 * time.Second, CheckRedirect: refuseRedirect}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
	}
	log.Printf("wecom alert adapter listening on %s", *addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
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

func healthcheck(addr string) int {
	host := addr
	if strings.HasPrefix(addr, ":") {
		host = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + host + "/healthz")
	if err != nil {
		log.Printf("healthcheck: %v", err)
		return 1
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		log.Printf("healthcheck status %d", response.StatusCode)
		return 1
	}
	return 0
}

func newHandler(urlFile string, client *http.Client) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok\n"))
	})
	mux.HandleFunc("POST /alertmanager", func(writer http.ResponseWriter, request *http.Request) {
		deliver(writer, request, urlFile, client)
	})
	return mux
}

func deliver(writer http.ResponseWriter, request *http.Request, urlFile string, client *http.Client) {
	webhookURL, err := readWebhookURL(urlFile)
	if err != nil {
		log.Printf("wecom webhook file: %v", err)
		http.Error(writer, "wecom webhook file is invalid", http.StatusInternalServerError)
		return
	}
	if webhookURL == "" {
		log.Printf("dropped alertmanager webhook: wecom receiver is not configured")
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
		log.Printf("wecom post failed")
		http.Error(writer, "wecom unreachable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	reply, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		log.Printf("wecom http status %d", response.StatusCode)
		http.Error(writer, "wecom rejected the post", http.StatusBadGateway)
		return
	}
	var parsed struct {
		ErrCode int `json:"errcode"`
	}
	if json.Unmarshal(reply, &parsed) == nil && parsed.ErrCode != 0 {
		log.Printf("wecom errcode %d", parsed.ErrCode)
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
