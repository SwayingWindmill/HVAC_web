package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/quanlaihe/hvac-web/services/identity-service/internal/identity"
)

func main() {
	output := strings.TrimSpace(os.Getenv("IDENTITY_SIGNING_KEY_OUT"))
	if output == "" {
		fmt.Fprintln(os.Stderr, "IDENTITY_SIGNING_KEY_OUT is required")
		os.Exit(2)
	}
	encoded, err := identity.GenerateSigningKeyPEM()
	if err != nil {
		fail(err)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		fail(fmt.Errorf("create identity signing key directory: %w", err))
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		fail(fmt.Errorf("create identity signing key file: %w", err))
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		fail(fmt.Errorf("write identity signing key: %w", err))
	}
	if err := file.Close(); err != nil {
		fail(fmt.Errorf("close identity signing key: %w", err))
	}
	fmt.Println(output)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "signing-keygen failed:", err)
	os.Exit(1)
}
