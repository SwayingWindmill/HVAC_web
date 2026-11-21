package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/eclipse/paho.golang/paho"

	"github.com/quanlaihe/hvac-web/libs/mqttclient"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	// BrokerCRLControlTopic is the Mosquitto $CONTROL topic handled by mosquitto_hvac_crl.so.
	BrokerCRLControlTopic        = "$CONTROL/hvac-crl/v1"
	brokerCRLActionReload        = "reload"
	brokerCRLActionReloadAndKick = "reload_and_kick"
	brokerCRLPublishTimeout      = 5 * time.Second
	defaultCRLRefreshInterval    = 6 * time.Hour

	brokerCRLControlMetricName = "hvac_mqtt_crl_control_events_total"
	brokerCRLControlMetricHelp = "Total MQTT broker CRL control notifications published by connectivity."
)

type brokerCRLPublisher interface {
	Publish(ctx context.Context, pub *paho.Publish) (*paho.PublishResponse, error)
}

type brokerCRLControlPayload struct {
	Action            string `json:"action"`
	GatewayID         string `json:"gatewayId,omitempty"`
	FingerprintSHA256 string `json:"fingerprintSha256,omitempty"`
	RevokeAll         bool   `json:"revokeAll,omitempty"`
	WithWill          bool   `json:"withWill,omitempty"`
}

type brokerCRLSyncer struct {
	publisher brokerCRLPublisher
	logger    *slog.Logger
	metrics   *observability.Registry
	timeout   time.Duration
}

func newBrokerCRLSyncer(publisher brokerCRLPublisher, logger *slog.Logger, metrics *observability.Registry) *brokerCRLSyncer {
	if typed, ok := publisher.(*mqttclient.SharedSession); ok && typed == nil {
		publisher = nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &brokerCRLSyncer{
		publisher: publisher,
		logger:    logger,
		metrics:   metrics,
		timeout:   brokerCRLPublishTimeout,
	}
}

func (s *brokerCRLSyncer) NotifyGatewayRevoked(ctx context.Context, gatewayID string) error {
	return s.publish(ctx, brokerCRLControlPayload{
		Action:    brokerCRLActionReloadAndKick,
		GatewayID: strings.TrimSpace(gatewayID),
		RevokeAll: true,
		WithWill:  true,
	})
}

func (s *brokerCRLSyncer) NotifyCertificateRevoked(ctx context.Context, gatewayID, fingerprintSHA256 string, hasRemainingActive bool) error {
	return s.publish(ctx, brokerCRLControlPayload{
		Action:            brokerCRLActionReloadAndKick,
		GatewayID:         strings.TrimSpace(gatewayID),
		FingerprintSHA256: strings.ToLower(strings.TrimSpace(fingerprintSHA256)),
		RevokeAll:         false,
		WithWill:          !hasRemainingActive,
	})
}

func (s *brokerCRLSyncer) NotifyReload(ctx context.Context) error {
	return s.publish(ctx, brokerCRLControlPayload{
		Action: brokerCRLActionReload,
	})
}

func (s *brokerCRLSyncer) publish(ctx context.Context, payload brokerCRLControlPayload) error {
	if s == nil || s.publisher == nil {
		return nil
	}
	if payload.Action != brokerCRLActionReload && payload.Action != brokerCRLActionReloadAndKick {
		payload.Action = "unknown"
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		s.recordOutcome(payload.Action, "failed")
		s.logger.Warn("gateway_crl_broker_notify_failed", "action", payload.Action, "gateway_id", payload.GatewayID, "error", err.Error())
		return err
	}

	if ctx == nil {
		ctx = context.Background()
	}
	timeout := s.timeout
	if timeout <= 0 {
		timeout = brokerCRLPublishTimeout
	}
	pubCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if _, err := s.publisher.Publish(pubCtx, &paho.Publish{
		QoS:     1,
		Topic:   BrokerCRLControlTopic,
		Payload: raw,
	}); err != nil {
		s.recordOutcome(payload.Action, "failed")
		s.logger.Warn("gateway_crl_broker_notify_failed", "action", payload.Action, "gateway_id", payload.GatewayID, "error", err.Error())
		return err
	}

	s.recordOutcome(payload.Action, "success")
	return nil
}

func (s *brokerCRLSyncer) recordOutcome(action, outcome string) {
	if s == nil || s.metrics == nil {
		return
	}
	_ = s.metrics.AddCounter(
		brokerCRLControlMetricName,
		brokerCRLControlMetricHelp,
		map[string]string{
			"action":  action,
			"outcome": outcome,
		},
		1,
	)
}

func startCRLRefreshLoop(ctx context.Context, logger *slog.Logger, metrics *observability.Registry, interval time.Duration, refresh func()) <-chan struct{} {
	done := make(chan struct{})
	if refresh == nil || interval <= 0 {
		close(done)
		return done
	}
	if ctx == nil {
		ctx = context.Background()
	}
	observability.GoSafe(logger, metrics, "connectivity.crl_periodic_refresh", func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refresh()
			}
		}
	})
	return done
}
