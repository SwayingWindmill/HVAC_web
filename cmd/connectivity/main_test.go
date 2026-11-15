package main

import (
	"errors"
	"testing"

	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/tunnel"
)

func TestCredentialServerRejectsNonLoopbackTunnelAddressFailClosed(t *testing.T) {
	t.Setenv("CONNECTIVITY_TUNNEL_LOOPBACK_ADDR", "0.0.0.0:18800")
	_, _, err := credentialServer(nil)
	if !errors.Is(err, tunnel.ErrNonLoopbackAddress) {
		t.Fatalf("expected ErrNonLoopbackAddress for 0.0.0.0:18800, got %v", err)
	}
}
