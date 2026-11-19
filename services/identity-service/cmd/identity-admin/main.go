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
	os.Exit(run())
}

func run() int {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	databaseURL, code := required("IDENTITY_DATABASE_URL")
	if code != 0 {
		return code
	}
	operation := strings.TrimSpace(os.Getenv("IDENTITY_ADMIN_OPERATION"))
	if operation == "" {
		operation = "create"
	}
	username, code := required("IDENTITY_ADMIN_USERNAME")
	if code != 0 {
		return code
	}

	store, err := identity.OpenStore(ctx, databaseURL)
	if err != nil {
		return fail(err)
	}
	defer store.Close()

	switch operation {
	case "reset-password":
		password, code := required("IDENTITY_ADMIN_PASSWORD")
		if code != 0 {
			return code
		}
		if err := store.ResetPassword(ctx, username, password, time.Now().UTC()); err != nil {
			return fail(err)
		}
		fmt.Fprintln(os.Stdout, "password reset")
		return 0
	case "reset-password-random":
		password, err := randomPassword()
		if err != nil {
			return fail(err)
		}
		if err := store.ResetPassword(ctx, username, password, time.Now().UTC()); err != nil {
			return fail(err)
		}
		fmt.Fprintln(os.Stdout, password)
		return 0
	case "totp-start":
		keyFile, code := required("IDENTITY_MFA_ENCRYPTION_KEY_FILE")
		if code != 0 {
			return code
		}
		protector, err := identity.LoadMFAProtectorFile(keyFile)
		if err != nil {
			return fail(err)
		}
		issuer, code := required("IDENTITY_ISSUER")
		if code != 0 {
			return code
		}
		enrollment, err := store.StartTOTPEnrollment(ctx, username, issuer, protector, time.Now().UTC())
		if err != nil {
			return fail(err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(enrollment); err != nil {
			return fail(err)
		}
		return 0
	case "totp-activate":
		keyFile, code := required("IDENTITY_MFA_ENCRYPTION_KEY_FILE")
		if code != 0 {
			return code
		}
		protector, err := identity.LoadMFAProtectorFile(keyFile)
		if err != nil {
			return fail(err)
		}
		totpCode, code := required("IDENTITY_ADMIN_TOTP_CODE")
		if code != 0 {
			return code
		}
		if err := store.ActivateTOTP(ctx, username, totpCode, protector, time.Now().UTC()); err != nil {
			return fail(err)
		}
		fmt.Fprintln(os.Stdout, "TOTP enabled")
		return 0
	case "totp-disable":
		if err := store.DisableTOTP(ctx, username, time.Now().UTC()); err != nil {
			return fail(err)
		}
		fmt.Fprintln(os.Stdout, "TOTP disabled")
		return 0
	case "create":
	default:
		fmt.Fprintln(os.Stderr, "IDENTITY_ADMIN_OPERATION must be create, reset-password, reset-password-random, totp-start, totp-activate or totp-disable")
		return 2
	}

	password, code := required("IDENTITY_ADMIN_PASSWORD")
	if code != 0 {
		return code
	}
	displayName, code := required("IDENTITY_ADMIN_DISPLAY_NAME")
	if code != 0 {
		return code
	}
	email, code := required("IDENTITY_ADMIN_EMAIL")
	if code != 0 {
		return code
	}
	created, err := store.CreateUser(ctx, identity.CreateUserInput{
		Username: username, DisplayName: displayName, Email: email, Password: password, Now: time.Now().UTC(),
	})
	if err != nil {
		return fail(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(created); err != nil {
		return fail(fmt.Errorf("encode created identity user: %w", err))
	}
	return 0
}

func required(name string) (string, int) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		fmt.Fprintln(os.Stderr, name+" is required")
		return "", 2
	}
	return value, 0
}

func randomPassword() (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate identity password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "identity-admin failed:", err)
	return 1
}
