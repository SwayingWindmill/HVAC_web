package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
	"github.com/quanlaihe/hvac-web/services/notification-service/pkg/notificationservice"
)

func newEmbeddedNotificationServer(ctx context.Context, logger *slog.Logger) (*http.Server, *observability.Runtime, func(), error) {
	certificate, err := tls.LoadX509KeyPair(energyRequiredEnv("NOTIFICATION_TLS_CERT"), energyRequiredEnv("NOTIFICATION_TLS_KEY"))
	if err != nil {
		return nil, nil, func() {}, err
	}
	clientCAs, err := loadCertPool(energyRequiredEnv("NOTIFICATION_CLIENT_CA"), "Notification client CA")
	if err != nil {
		return nil, nil, func() {}, err
	}
	gatewayPublicKey, err := embeddedCertificatePublicKey(energyRequiredEnv("NOTIFICATION_GATEWAY_DELEGATION_CERT"))
	if err != nil {
		return nil, nil, func() {}, err
	}
	databaseURL, err := embeddedLoadValueFile(energyRequiredEnv("NOTIFICATION_DATABASE_URL_FILE"), 64<<10)
	if err != nil {
		return nil, nil, func() {}, err
	}
	alarmDatabaseURL, err := embeddedLoadValueFile(energyRequiredEnv("ALARM_DATABASE_URL_FILE"), 64<<10)
	if err != nil {
		return nil, nil, func() {}, err
	}
	openContext, cancelOpen := context.WithTimeout(ctx, 10*time.Second)
	store, err := notificationservice.OpenPostgresStore(openContext, databaseURL)
	cancelOpen()
	if err != nil {
		return nil, nil, func() {}, err
	}
	openContext, cancelOpen = context.WithTimeout(ctx, 10*time.Second)
	scheduler, err := notificationservice.OpenScheduler(openContext, databaseURL)
	cancelOpen()
	if err != nil {
		store.Close()
		return nil, nil, func() {}, err
	}
	openContext, cancelOpen = context.WithTimeout(ctx, 10*time.Second)
	relay, err := alarmservice.OpenNotificationRelay(openContext, alarmDatabaseURL)
	cancelOpen()
	if err != nil {
		scheduler.Close()
		store.Close()
		return nil, nil, func() {}, err
	}

	gatewaySPIFFE := envOr("NOTIFICATION_GATEWAY_SPIFFE", notificationservice.DefaultGatewaySPIFFEID)
	handler, err := notificationservice.NewHTTPHandler(notificationservice.HTTPConfig{
		Reminders: store, GatewayPublicKey: gatewayPublicKey, GatewaySPIFFEID: gatewaySPIFFE,
		Audience: envOr("NOTIFICATION_SERVICE_AUDIENCE", notificationservice.DefaultAudience),
	})
	if err != nil {
		relay.Close()
		scheduler.Close()
		store.Close()
		return nil, nil, func() {}, err
	}
	reminders, err := newWeChatReminderWorker(logger, scheduler)
	if err != nil {
		relay.Close()
		scheduler.Close()
		store.Close()
		return nil, nil, func() {}, err
	}
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "energy-api-notification", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	telemetry.SetDependencies(observability.Dependency{Name: "postgres", Required: true, Check: store.Ping})
	router := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if embeddedPeerSPIFFE(request) != gatewaySPIFFE {
			embeddedWriteProblem(writer, http.StatusForbidden, "NOTIFICATION_GATEWAY_WORKLOAD_FORBIDDEN")
			return
		}
		handler.ServeHTTP(writer, request)
	})
	server := &http.Server{
		Addr: envOr("NOTIFICATION_SERVICE_ADDR", ":8450"), Handler: router,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert,
		},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
	}
	go runAlarmNotificationRelay(ctx, logger, relay, store)
	go runWeChatReminders(ctx, logger, reminders)
	closeAll := func() {
		relay.Close()
		scheduler.Close()
		store.Close()
	}
	logger.Info("energy_api_notification_configured", "address", server.Addr)
	return server, telemetry, closeAll, nil
}

func runAlarmNotificationRelay(ctx context.Context, logger *slog.Logger, relay *alarmservice.NotificationRelay, store *notificationservice.PostgresStore) {
	workerID := replicaWorkerID("NOTIFICATION_ALARM_RELAY_WORKER_ID", "notification-alarm-relay")
	for {
		if ctx.Err() != nil {
			return
		}
		now := time.Now().UTC()
		event, err := relay.Claim(ctx, workerID, now, 30*time.Second)
		if errors.Is(err, alarmservice.ErrNotFound) {
			if !notificationWait(ctx, 250*time.Millisecond) {
				return
			}
			continue
		}
		if err != nil {
			logger.Error("notification_alarm_relay_claim_failed", "error_code", "NOTIFICATION_ALARM_RELAY_CLAIM_FAILED")
			if !notificationWait(ctx, time.Second) {
				return
			}
			continue
		}
		err = store.RecordAlarmEvent(ctx, notificationservice.AlarmEvent{
			TenantID: event.TenantID, SiteID: event.SiteID, SourceEventID: event.SourceEventID, AlarmID: event.AlarmID,
			IncidentCorrelationID: event.IncidentCorrelationID, Action: notificationservice.AlarmAction(event.Action),
			CurrentSeverity: event.CurrentSeverity, PeakSeverity: event.PeakSeverity, Condition: event.Condition,
			OccurredAt: event.OccurredAt, Attributes: event.Attributes,
		}, time.Now().UTC())
		if err != nil {
			logger.Error("notification_alarm_relay_process_failed", "error_code", "NOTIFICATION_ALARM_RELAY_PROCESS_FAILED", "source_event_id", event.SourceEventID)
			continue
		}
		if err := relay.Complete(ctx, event.SourceEventID, event.LeaseOwner, event.LeaseFence, time.Now().UTC()); err != nil {
			logger.Error("notification_alarm_relay_complete_failed", "error_code", "NOTIFICATION_ALARM_RELAY_COMPLETE_FAILED", "source_event_id", event.SourceEventID)
		}
	}
}

func replicaWorkerID(name, prefix string) string {
	if configured := strings.TrimSpace(os.Getenv(name)); configured != "" {
		return configured
	}
	hostname, _ := os.Hostname()
	return prefix + ":" + hostname
}

func notificationWait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
