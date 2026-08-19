package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/quanlaihe/hvac-web/services/identity-service/internal/identity"
)

func main() {
	output := strings.TrimSpace(os.Getenv("IDENTITY_MFA_KEY_OUT"))
	if output == "" {
		fmt.Fprintln(os.Stderr, "IDENTITY_MFA_KEY_OUT is required")
		os.Exit(2)
	}
	encoded, err := identity.GenerateMFAEncryptionKey()
	if err != nil {
		fail(err)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		fail(fmt.Errorf("create identity MFA key directory: %w", err))
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fail(fmt.Errorf("create identity MFA key file: %w", err))
	}
	if _, err := file.WriteString(encoded + "\n"); err != nil {
		_ = file.Close()
		fail(fmt.Errorf("write identity MFA key: %w", err))
	}
	if err := file.Close(); err != nil {
		fail(fmt.Errorf("close identity MFA key: %w", err))
	}
	fmt.Println(output)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "mfa-keygen failed:", err)
	os.Exit(1)
}
