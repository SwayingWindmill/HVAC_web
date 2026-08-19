package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/services/identity-service/internal/identity"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	databaseURL := required("IDENTITY_DATABASE_URL")
	operation := strings.TrimSpace(os.Getenv("IDENTITY_ADMIN_OPERATION"))
	if operation == "" {
		operation = "create"
	}
	username := required("IDENTITY_ADMIN_USERNAME")

	store, err := identity.OpenStore(ctx, databaseURL)
	if err != nil {
		fail(err)
	}
	defer store.Close()

	switch operation {
	case "reset-password":
		password := required("IDENTITY_ADMIN_PASSWORD")
		if err := store.ResetPassword(ctx, username, password, time.Now().UTC()); err != nil {
			fail(err)
		}
		fmt.Fprintln(os.Stdout, "password reset")
		return
	case "reset-password-random":
		password, err := randomPassword()
		if err != nil {
			fail(err)
		}
		if err := store.ResetPassword(ctx, username, password, time.Now().UTC()); err != nil {
			fail(err)
		}
		fmt.Fprintln(os.Stdout, password)
		return
	case "totp-start":
		protector, err := identity.LoadMFAProtectorFile(required("IDENTITY_MFA_ENCRYPTION_KEY_FILE"))
		if err != nil {
			fail(err)
		}
		enrollment, err := store.StartTOTPEnrollment(ctx, username, required("IDENTITY_ISSUER"), protector, time.Now().UTC())
		if err != nil {
			fail(err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(enrollment); err != nil {
			fail(err)
		}
		return
	case "totp-activate":
		protector, err := identity.LoadMFAProtectorFile(required("IDENTITY_MFA_ENCRYPTION_KEY_FILE"))
		if err != nil {
			fail(err)
		}
		if err := store.ActivateTOTP(ctx, username, required("IDENTITY_ADMIN_TOTP_CODE"), protector, time.Now().UTC()); err != nil {
			fail(err)
		}
		fmt.Fprintln(os.Stdout, "TOTP enabled")
		return
	case "totp-disable":
		if err := store.DisableTOTP(ctx, username, time.Now().UTC()); err != nil {
			fail(err)
		}
		fmt.Fprintln(os.Stdout, "TOTP disabled")
		return
	case "create":
	default:
		fmt.Fprintln(os.Stderr, "IDENTITY_ADMIN_OPERATION must be create, reset-password, reset-password-random, totp-start, totp-activate or totp-disable")
		os.Exit(2)
	}

	password := required("IDENTITY_ADMIN_PASSWORD")
	displayName := required("IDENTITY_ADMIN_DISPLAY_NAME")
	email := required("IDENTITY_ADMIN_EMAIL")
	created, err := store.CreateUser(ctx, identity.CreateUserInput{
		Username: username, DisplayName: displayName, Email: email, Password: password, Now: time.Now().UTC(),
	})
	if err != nil {
		fail(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(created); err != nil {
		fail(fmt.Errorf("encode created identity user: %w", err))
	}
}

func required(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		fmt.Fprintln(os.Stderr, name+" is required")
		os.Exit(2)
	}
	return value
}

func randomPassword() (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate identity password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "identity-admin failed:", err)
	os.Exit(1)
}
