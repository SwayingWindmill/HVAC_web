package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/notification-service/pkg/notificationservice"
)

func newAlarmRoutingReloader(logger *slog.Logger, metrics *observability.Registry) (*notificationservice.RoutingReloader, error) {
	mode, err := notificationservice.ParseDeliveryMode(os.Getenv("ALARM_DELIVERY_MODE"))
	if err != nil {
		return nil, err
	}
	recipientsFile := envOr("ALARM_DELIVERY_RECIPIENTS_FILE", "/run/hvac/alarm-delivery/recipients.json")
	routingDir := envOr("ALARM_ROUTING_DIR", "/run/hvac/alarm-routing")

	if mode == notificationservice.DeliveryOff {
		if _, err := os.Stat(recipientsFile); err != nil {
			if logger != nil {
				logger.Info("alarm_routing_reloader_skipped_delivery_off")
			}
			return nil, nil
		}
		if _, err := os.Stat(routingDir); err != nil {
			if logger != nil {
				logger.Info("alarm_routing_reloader_skipped_delivery_off")
			}
			return nil, nil
		}
	}

	reloader, err := notificationservice.NewRoutingReloader(recipientsFile, routingDir, func(outcome string) {
		if metrics == nil {
			return
		}
		normalized := "failed"
		if outcome == "success" {
			normalized = "success"
		}
		_ = metrics.AddCounter(
			"hvac_alarm_routing_reloads_total",
			"Total alarm routing config reload attempts by outcome.",
			map[string]string{"outcome": normalized},
			1,
		)
	})
	if err != nil {
		return nil, err
	}
	if metrics != nil {
		for _, outcome := range []string{"success", "failed"} {
			_ = metrics.AddCounter(
				"hvac_alarm_routing_reloads_total",
				"Total alarm routing config reload attempts by outcome.",
				map[string]string{"outcome": outcome},
				0,
			)
		}
	}
	return reloader, nil
}

// Alarm delivery (P0-4, docs/specs/alarm-delivery.md): who is told of an Alarm, by mail or WeCom,
// with escalation, acknowledgement and recovery notices. ALARM_DELIVERY_MODE=off (the default)
// starts nothing. dry-run plans and records every message and has the adapter check its
// configuration without sending; live sends. A mode other than off with a broken routing or
// recipients file stops energy-api at start, so a bad file never silently tells nobody.
func newAlarmDeliveryDispatcher(logger *slog.Logger, scheduler *notificationservice.Scheduler) (*notificationservice.AlarmDeliveryDispatcher, error) {
	return newAlarmDeliveryDispatcherWithReloader(logger, scheduler, nil)
}

func newAlarmDeliveryDispatcherWithReloader(
	logger *slog.Logger,
	scheduler *notificationservice.Scheduler,
	reloader *notificationservice.RoutingReloader,
) (*notificationservice.AlarmDeliveryDispatcher, error) {
	mode, err := notificationservice.ParseDeliveryMode(os.Getenv("ALARM_DELIVERY_MODE"))
	if err != nil {
		return nil, err
	}
	if mode == notificationservice.DeliveryOff {
		logger.Info("alarm_delivery_off")
		return nil, nil
	}
	var routing notificationservice.AlarmRoutingTable
	var recipients notificationservice.RecipientDirectory
	if reloader != nil {
		if snap := reloader.Snapshot(); snap != nil {
			if snap.Routing != nil {
				routing = *snap.Routing
			}
			if snap.Recipients != nil {
				recipients = *snap.Recipients
			}
		}
	} else {
		recipientsFile := envOr("ALARM_DELIVERY_RECIPIENTS_FILE", "/run/hvac/alarm-delivery/recipients.json")
		recipients, err = notificationservice.LoadRecipientDirectory(recipientsFile)
		if err != nil {
			return nil, fmt.Errorf("alarm delivery recipients: %w", err)
		}
		routing, err = notificationservice.LoadAlarmRouting(envOr("ALARM_ROUTING_DIR", "/run/hvac/alarm-routing"), recipients)
		if err != nil {
			return nil, fmt.Errorf("alarm delivery routing: %w", err)
		}
	}
	sendURL := strings.TrimSpace(os.Getenv("ALARM_SEND_URL"))
	if sendURL == "" {
		return nil, fmt.Errorf("ALARM_SEND_URL is required when ALARM_DELIVERY_MODE is %s", mode)
	}
	tokenFile := envOr("ALARM_SEND_TOKEN_FILE", "/run/hvac/alarm-delivery/alarm-send-token")
	if token, err := os.ReadFile(tokenFile); err != nil || strings.TrimSpace(string(token)) == "" {
		return nil, fmt.Errorf("alarm delivery needs a readable, non-empty ALARM_SEND_TOKEN_FILE")
	}
	logger.Info("alarm_delivery_configured", "mode", string(mode), "sites", routing.Len(), "recipients", len(recipients.Recipients))
	return &notificationservice.AlarmDeliveryDispatcher{
		Scheduler: scheduler, Reloader: reloader, Routing: routing, Recipients: recipients, Mode: mode, Logger: logger,
		Sender: notificationservice.HTTPDeliverySender{
			URL: sendURL, TokenFile: tokenFile,
			Client: &http.Client{
				Timeout:       25 * time.Second,
				Transport:     cloneDefaultTransportWithTLS(nil),
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			},
		},
		ConsoleURL: strings.TrimRight(envOr("ALARM_DELIVERY_CONSOLE_URL", os.Getenv("PLATFORM_PUBLIC_ORIGIN")), "/"),
	}, nil
}

func runAlarmDelivery(ctx context.Context, logger *slog.Logger, dispatcher *notificationservice.AlarmDeliveryDispatcher) {
	if dispatcher == nil {
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		worked, err := dispatcher.RunOnce(ctx, time.Now().UTC())
		if err != nil {
			logger.Error("alarm_delivery_failed", "error_code", "ALARM_DELIVERY_FAILED", "error", err)
		}
		if !worked || err != nil {
			if !notificationWait(ctx, 2*time.Second) {
				return
			}
		}
	}
}
