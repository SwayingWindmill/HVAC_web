package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/services/iam-service/internal/iam"
)

const maximumReconciliationInputBytes = 1 << 20

type reconciliationCommand struct {
	UserID string                      `json:"userId"`
	Seed   iam.LogtoReconciliationSeed `json:"seed"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx, os.Stdin, os.Stdout, os.Getenv); err != nil {
		fmt.Fprintf(os.Stderr, "iam-reconciler failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, input io.Reader, output io.Writer, getenv func(string) string) error {
	command, err := decodeCommand(input)
	if err != nil {
		return err
	}
	databaseURL, err := requiredEnvironment(getenv, "IAM_RECONCILER_DATABASE_URL")
	if err != nil {
		return err
	}
	endpoint, err := requiredEnvironment(getenv, "LOGTO_MANAGEMENT_ENDPOINT")
	if err != nil {
		return err
	}
	clientID, err := requiredEnvironment(getenv, "LOGTO_MANAGEMENT_CLIENT_ID")
	if err != nil {
		return err
	}
	clientCredential, err := requiredEnvironment(getenv, "LOGTO_MANAGEMENT_CLIENT_SECRET")
	if err != nil {
		return err
	}
	resource, err := requiredEnvironment(getenv, "LOGTO_MANAGEMENT_RESOURCE")
	if err != nil {
		return err
	}
	scope, err := requiredEnvironment(getenv, "LOGTO_MANAGEMENT_SCOPE")
	if err != nil {
		return err
	}
	issuer, err := requiredEnvironment(getenv, "LOGTO_ISSUER")
	if err != nil {
		return err
	}
	management, err := iam.NewLogtoManagementClient(iam.LogtoManagementConfig{
		Endpoint: endpoint, ClientID: clientID, ClientSecret: clientCredential,
		Resource: resource, Scope: scope,
	})
	if err != nil {
		return err
	}
	store, err := iam.OpenPostgresReconciliationStore(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	reconciler, err := iam.NewLogtoReconciler(management, store, issuer)
	if err != nil {
		return err
	}
	result, err := reconciler.ReconcileUser(ctx, command.UserID, command.Seed)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("encode reconciliation result: %w", err)
	}
	return nil
}

func decodeCommand(input io.Reader) (reconciliationCommand, error) {
	body, err := io.ReadAll(io.LimitReader(input, maximumReconciliationInputBytes+1))
	if err != nil {
		return reconciliationCommand{}, fmt.Errorf("read reconciliation input: %w", err)
	}
	if len(body) > maximumReconciliationInputBytes {
		return reconciliationCommand{}, errors.New("reconciliation input exceeded size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var command reconciliationCommand
	if err := decoder.Decode(&command); err != nil {
		return reconciliationCommand{}, fmt.Errorf("decode reconciliation input: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return reconciliationCommand{}, errors.New("reconciliation input must contain one JSON object")
	}
	command.UserID = strings.TrimSpace(command.UserID)
	if command.UserID == "" {
		return reconciliationCommand{}, errors.New("reconciliation userId is required")
	}
	return command, nil
}

func requiredEnvironment(getenv func(string) string, name string) (string, error) {
	value := strings.TrimSpace(getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}
