package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	output := strings.TrimSpace(os.Getenv("IAM_API_CREDENTIAL_PEPPER_OUT"))
	if output == "" {
		fmt.Fprintln(os.Stderr, "IAM_API_CREDENTIAL_PEPPER_OUT is required")
		os.Exit(2)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		fail(err)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		fail(fmt.Errorf("create IAM credential directory: %w", err))
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fail(fmt.Errorf("create IAM credential pepper file: %w", err))
	}
	if _, err := file.WriteString(base64.RawURLEncoding.EncodeToString(secret) + "\n"); err != nil {
		_ = file.Close()
		fail(fmt.Errorf("write IAM credential pepper: %w", err))
	}
	if err := file.Close(); err != nil {
		fail(fmt.Errorf("close IAM credential pepper: %w", err))
	}
	fmt.Println(output)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "api-credential-keygen failed:", err)
	os.Exit(1)
}
