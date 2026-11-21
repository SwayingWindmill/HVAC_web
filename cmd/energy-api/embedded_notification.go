package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
	"github.com/quanlaihe/hvac-web/services/notification-service/pkg/notificationservice"
)

const (
	notificationStepTimeout   = 30 * time.Second
	notificationMaxBatch      = 32
	notificationBatchPause    = 20 * time.Millisecond
	notificationErrorBaseWait = time.Second
	notificationErrorMaxWait  = 30 * time.Second
	notificationStableWindow  = 30 * time.Second
)

func newEmbeddedNotificationServer(ctx context.Context, logger *slog.Logger, bgWG *sync.WaitGroup) (*http.Server, *observability.Runtime, func(), error) {
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

	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "energy-api-notification", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond, Logger: logger,
	})
	telemetry.SetDependencies(observability.Dependency{Name: "postgres", Required: true, Check: store.Ping})

	reloader, err := newAlarmRoutingReloader(logger, telemetry.Metrics)
	if err != nil {
		relay.Close()
		scheduler.Close()
		store.Close()
		return nil, nil, func() {}, err
	}

	gatewaySPIFFE := envOr("NOTIFICATION_GATEWAY_SPIFFE", notificationservice.DefaultGatewaySPIFFEID)
	handler, err := notificationservice.NewHTTPHandler(notificationservice.HTTPConfig{
		Reminders: store, Routing: reloader, GatewayPublicKey: gatewayPublicKey, GatewaySPIFFEID: gatewaySPIFFE,
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
	delivery, err := newAlarmDeliveryDispatcherWithReloader(logger, scheduler, reloader)
	if err != nil {
		relay.Close()
		scheduler.Close()
		store.Close()
		return nil, nil, func() {}, err
	}
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
	launchEmbeddedWorker(bgWG, func() {
		runAlarmNotificationRelay(ctx, logger, telemetry.Metrics, relay, store)
	})
	launchEmbeddedWorker(bgWG, func() {
		runWeChatReminders(ctx, logger, telemetry.Metrics, reminders)
	})
	if reloader != nil {
		launchEmbeddedWorker(bgWG, func() {
			runSupervisedNotificationLoop(ctx, logger, telemetry.Metrics, "notification.routing_reloader", func() {
				reloader.Run(ctx, 30*time.Second, logger)
			})
		})
		launchEmbeddedWorker(bgWG, func() {
			sighupCh := make(chan os.Signal, 1)
			signal.Notify(sighupCh, syscall.SIGHUP)
			defer signal.Stop(sighupCh)
			for {
				select {
				case <-ctx.Done():
					return
				case <-sighupCh:
					_, _, _ = reloader.ReloadNow(ctx)
				}
			}
		})
	}
	if delivery != nil {
		launchEmbeddedWorker(bgWG, func() {
			runSupervisedNotificationLoop(ctx, logger, telemetry.Metrics, "notification.alarm_delivery", func() {
				runAlarmDelivery(ctx, logger, delivery)
			})
		})
	}
	closeAll := func() {
		relay.Close()
		scheduler.Close()
		store.Close()
	}
	logger.Info("energy_api_notification_configured", "address", server.Addr)
	return server, telemetry, closeAll, nil
}

func runAlarmNotificationRelay(ctx context.Context, logger *slog.Logger, metrics *observability.Registry, relay *alarmservice.NotificationRelay, store *notificationservice.PostgresStore) {
	if relay == nil || store == nil {
		return
	}
	if logger == nil {
		logger = observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	}
	workerID := replicaWorkerID("NOTIFICATION_ALARM_RELAY_WORKER_ID", "notification-alarm-relay")
	consecutiveErrors := 0
	runSupervisedNotificationLoop(ctx, logger, metrics, "notification.alarm_relay", func() {
		for {
			if ctx.Err() != nil {
				return
			}
			idle, err := drainAlarmNotificationRelayBatch(ctx, logger, relay, store, workerID, notificationMaxBatch)
			if err != nil {
				consecutiveErrors++
				if !notificationWait(ctx, notificationRetryBackoff(consecutiveErrors, notificationErrorBaseWait, notificationErrorMaxWait)) {
					return
				}
				continue
			}
			consecutiveErrors = 0
			waitFor := notificationBatchPause
			if idle {
				waitFor = 250 * time.Millisecond
			}
			if !notificationWait(ctx, waitFor) {
				return
			}
		}
	})
}

func drainAlarmNotificationRelayBatch(ctx context.Context, logger *slog.Logger, relay *alarmservice.NotificationRelay, store *notificationservice.PostgresStore, workerID string, maxBatch int) (bool, error) {
	for range maxBatch {
		if ctx.Err() != nil {
			return false, nil
		}
		event, err := claimRelayNotificationEvent(ctx, relay, workerID)
		if errors.Is(err, alarmservice.ErrNotFound) {
			return true, nil
		}
		if err != nil {
			logger.Error("notification_alarm_relay_claim_failed", "error_code", "NOTIFICATION_ALARM_RELAY_CLAIM_FAILED", "error", err)
			return false, err
		}
		if err := recordRelayNotificationEvent(ctx, store, event); err != nil {
			logger.Error("notification_alarm_relay_process_failed", "error_code", "NOTIFICATION_ALARM_RELAY_PROCESS_FAILED", "source_event_id", event.SourceEventID, "error", err)
			return false, err
		}
		if err := completeRelayNotificationEvent(ctx, relay, event); err != nil {
			logger.Error("notification_alarm_relay_complete_failed", "error_code", "NOTIFICATION_ALARM_RELAY_COMPLETE_FAILED", "source_event_id", event.SourceEventID, "error", err)
			return false, err
		}
	}
	return false, nil
}

func claimRelayNotificationEvent(ctx context.Context, relay *alarmservice.NotificationRelay, workerID string) (alarmservice.NotificationOutboxEvent, error) {
	claimCtx, cancel := context.WithTimeout(ctx, notificationStepTimeout)
	defer cancel()
	return relay.Claim(claimCtx, workerID, time.Now().UTC(), 30*time.Second)
}

func recordRelayNotificationEvent(ctx context.Context, store *notificationservice.PostgresStore, event alarmservice.NotificationOutboxEvent) error {
	recordCtx, cancel := context.WithTimeout(ctx, notificationStepTimeout)
	defer cancel()
	return store.RecordAlarmEvent(recordCtx, notificationservice.AlarmEvent{
		TenantID: event.TenantID, SiteID: event.SiteID, SourceEventID: event.SourceEventID, AlarmID: event.AlarmID, AlarmVersion: int64(event.AlarmVersion),
		IncidentCorrelationID: event.IncidentCorrelationID, Action: notificationservice.AlarmAction(event.Action),
		CurrentSeverity: event.CurrentSeverity, PeakSeverity: event.PeakSeverity, Condition: event.Condition,
		OccurredAt: event.OccurredAt, Attributes: event.Attributes,
	}, time.Now().UTC())
}

func completeRelayNotificationEvent(ctx context.Context, relay *alarmservice.NotificationRelay, event alarmservice.NotificationOutboxEvent) error {
	completeCtx, cancel := context.WithTimeout(ctx, notificationStepTimeout)
	defer cancel()
	return relay.Complete(completeCtx, event.SourceEventID, event.LeaseOwner, event.LeaseFence, time.Now().UTC())
}

// runSupervisedNotificationLoop isolates panics inside a background worker and restarts it with
// bounded backoff until ctx is canceled.
func runSupervisedNotificationLoop(ctx context.Context, logger *slog.Logger, metrics *observability.Registry, component string, fn func()) {
	if logger == nil {
		logger = observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	}
	consecutivePanics := 0
	for {
		if ctx.Err() != nil {
			return
		}
		started := time.Now()
		panicked := false
		func() {
			defer observability.Recover(logger, metrics, component, func(any) {
				panicked = true
			})
			fn()
		}()
		if !panicked {
			return
		}
		if time.Since(started) >= notificationStableWindow {
			consecutivePanics = 0
		}
		consecutivePanics++
		if !notificationWait(ctx, notificationRetryBackoff(consecutivePanics, notificationErrorBaseWait, notificationErrorMaxWait)) {
			return
		}
	}
}

func notificationRetryBackoff(attempt int, base, maxDelay time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if base <= 0 {
		base = time.Second
	}
	if maxDelay < base {
		maxDelay = base
	}
	delay := base
	for i := 1; i < attempt && delay < maxDelay; i++ {
		if delay > maxDelay/2 {
			delay = maxDelay
			break
		}
		delay *= 2
	}
	jitterRange := delay / 5
	if jitterRange > 0 {
		offset := time.Duration(rand.Int64N(int64(jitterRange)*2+1)) - jitterRange
		delay += offset
	}
	if delay > maxDelay {
		return maxDelay
	}
	return delay
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
