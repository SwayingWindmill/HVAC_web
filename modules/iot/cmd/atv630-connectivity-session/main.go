package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/modules/iot/pkg/connectivity"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := strings.TrimSpace(os.Getenv("ATV630_CONNECTIVITY_DATABASE_URL"))
	tenantID := strings.TrimSpace(os.Getenv("ATV630_TEMPLATE_TENANT_ID"))
	integrationInstanceID := strings.TrimSpace(os.Getenv("ATV630_INTEGRATION_INSTANCE_ID"))
	credentialRefID := strings.TrimSpace(os.Getenv("ATV630_CONNECTIVITY_CREDENTIAL_REF_ID"))
	gatewayExternalID := strings.TrimSpace(os.Getenv("ATV630_GATEWAY_EXTERNAL_ID"))
	if databaseURL == "" || tenantID == "" || integrationInstanceID == "" || credentialRefID == "" || gatewayExternalID == "" {
		return errors.New("ATV630 connectivity session configuration is incomplete")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := connectivity.Open(ctx, databaseURL, tenantID)
	if err != nil {
		return err
	}
	defer store.Close()

	now := time.Now().UTC()
	sessionID, err := newUUIDv7(now)
	if err != nil {
		return err
	}
	expiresAt := now.Add(24 * time.Hour)
	if err := store.OpenSession(ctx, connectivity.SessionInput{
		ID:                    sessionID,
		IntegrationInstanceID: integrationInstanceID,
		CredentialRefID:       credentialRefID,
		GatewayExternalID:     gatewayExternalID,
		ExpiresAt:             expiresAt,
	}); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"sessionId": sessionID,
		"expiresAt": expiresAt,
	})
}

func newUUIDv7(now time.Time) (string, error) {
	millis := now.UnixMilli()
	if millis < 0 || millis >= 1<<48 {
		return "", errors.New("session timestamp is outside UUIDv7 range")
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate session UUID entropy: %w", err)
	}
	raw[0] = byte(uint64(millis) >> 40)
	raw[1] = byte(uint64(millis) >> 32)
	raw[2] = byte(uint64(millis) >> 24)
	raw[3] = byte(uint64(millis) >> 16)
	raw[4] = byte(uint64(millis) >> 8)
	raw[5] = byte(millis)
	raw[6] = raw[6]&0x0f | 0x70
	raw[8] = raw[8]&0x3f | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
