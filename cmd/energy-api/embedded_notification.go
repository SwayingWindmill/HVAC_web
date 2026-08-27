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

	var deliveryPort *notificationservice.S15DeliveryPort
	if path := strings.TrimSpace(os.Getenv("OUTBOUND_DELIVERY_DATABASE_URL_FILE")); path != "" {
		deliveryDatabaseURL, loadErr := embeddedLoadValueFile(path, 64<<10)
		if loadErr != nil {
			relay.Close()
			scheduler.Close()
			store.Close()
			return nil, nil, func() {}, loadErr
		}
		openContext, cancelOpen = context.WithTimeout(ctx, 10*time.Second)
		deliveryPort, err = notificationservice.OpenS15DeliveryPort(openContext, deliveryDatabaseURL)
		cancelOpen()
		if err != nil {
			relay.Close()
			scheduler.Close()
			store.Close()
			return nil, nil, func() {}, err
		}
	}

	gatewaySPIFFE := envOr("NOTIFICATION_GATEWAY_SPIFFE", notificationservice.DefaultGatewaySPIFFEID)
	handler, err := notificationservice.NewHTTPHandler(notificationservice.HTTPConfig{
		Store: store, GatewayPublicKey: gatewayPublicKey, GatewaySPIFFEID: gatewaySPIFFE,
		Audience: envOr("NOTIFICATION_SERVICE_AUDIENCE", notificationservice.DefaultAudience),
	})
	if err != nil {
		if deliveryPort != nil {
			deliveryPort.Close()
		}
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
	go runNotificationScheduler(ctx, logger, scheduler, store, deliveryPort)
	closeAll := func() {
		if deliveryPort != nil {
			deliveryPort.Close()
		}
		relay.Close()
		scheduler.Close()
		store.Close()
	}
	logger.Info("energy_api_notification_configured", "address", server.Addr, "external_delivery_enabled", deliveryPort != nil)
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
		_, err = store.ProcessAlarmEvent(ctx, notificationservice.AlarmEvent{
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

func runNotificationScheduler(ctx context.Context, logger *slog.Logger, scheduler *notificationservice.Scheduler, store *notificationservice.PostgresStore, deliveryPort *notificationservice.S15DeliveryPort) {
	workerID := replicaWorkerID("NOTIFICATION_STAGE_WORKER_ID", "notification-stage-worker")
	for {
		if ctx.Err() != nil {
			return
		}
		if deliveryPort != nil {
			if handoff, err := scheduler.NextUnboundExternalHandoff(ctx); err == nil {
				if _, err := store.ResumeExternalHandoff(ctx, handoff.TenantID, handoff.IntentID, deliveryPort, time.Now().UTC()); err != nil {
					logger.Error("notification_external_handoff_recovery_failed", "error_code", "NOTIFICATION_EXTERNAL_HANDOFF_RECOVERY_FAILED", "notification_intent_id", handoff.IntentID)
				}
			}
			if pending, err := scheduler.NextPendingExternalDisposition(ctx); err == nil {
				delivery, lookupErr := deliveryPort.GetDeliveryIntent(ctx, pending.TenantID, pending.ExternalDeliveryIntentID)
				if lookupErr != nil {
					logger.Error("notification_external_delivery_lookup_failed", "error_code", "NOTIFICATION_EXTERNAL_DELIVERY_LOOKUP_FAILED", "notification_intent_id", pending.IntentID)
				} else if err := store.RecordExternalDisposition(ctx, pending.TenantID, pending.IntentID, pending.ExternalDeliveryIntentID, delivery.State, time.Now().UTC()); err != nil {
					logger.Error("notification_external_disposition_update_failed", "error_code", "NOTIFICATION_EXTERNAL_DISPOSITION_UPDATE_FAILED", "notification_intent_id", pending.IntentID)
				}
			}
		}
		claim, err := scheduler.ClaimDue(ctx, workerID, time.Now().UTC(), 30*time.Second)
		if errors.Is(err, notificationservice.ErrNotFound) {
			if !notificationWait(ctx, 250*time.Millisecond) {
				return
			}
			continue
		}
		if err != nil {
			logger.Error("notification_stage_claim_failed", "error_code", "NOTIFICATION_STAGE_CLAIM_FAILED")
			if !notificationWait(ctx, time.Second) {
				return
			}
			continue
		}
		if claim.Channel == notificationservice.ChannelInApp {
			if _, err := store.MaterializeInApp(ctx, *claim, time.Now().UTC()); err != nil && !errors.Is(err, notificationservice.ErrClaimLost) {
				logger.Error("notification_in_app_materialization_failed", "error_code", "NOTIFICATION_IN_APP_MATERIALIZATION_FAILED", "notification_intent_id", claim.IntentID)
			}
			continue
		}
		if deliveryPort == nil {
			logger.Error("notification_external_delivery_unconfigured", "error_code", "NOTIFICATION_EXTERNAL_DELIVERY_UNCONFIGURED", "notification_intent_id", claim.IntentID)
			continue
		}
		if _, err := store.SubmitExternal(ctx, *claim, deliveryPort, time.Now().UTC()); err != nil && !errors.Is(err, notificationservice.ErrClaimLost) {
			logger.Error("notification_external_handoff_failed", "error_code", "NOTIFICATION_EXTERNAL_HANDOFF_FAILED", "notification_intent_id", claim.IntentID)
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
