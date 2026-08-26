package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/quanlaihe/hvac-web/modules/maintenance/pkg/maintenance"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: maintenance-admin <events|ack-event|tenant-usage|owner-result> ...")
	}
	openCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	store, err := maintenance.OpenStore(openCtx, os.Getenv("MAINTENANCE_POSTGRES_DSN"))
	if err != nil {
		return err
	}
	defer store.Close()

	switch args[0] {
	case "events":
		limit := 50
		if len(args) == 2 {
			limit, err = strconv.Atoi(args[1])
			if err != nil {
				return errors.New("events limit must be an integer")
			}
		} else if len(args) != 1 {
			return errors.New("usage: maintenance-admin events [limit]")
		}
		events, err := store.ListOpenEvents(ctx, limit)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(events)
	case "ack-event":
		if len(args) != 2 {
			return errors.New("usage: maintenance-admin ack-event <event-id>")
		}
		return store.AcknowledgeEvent(ctx, args[1], time.Now().UTC())
	case "tenant-usage":
		if len(args) != 2 {
			return errors.New("usage: maintenance-admin tenant-usage <tenant-id>")
		}
		usage, err := store.LoadTenantPolicyUsage(ctx, args[1])
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(usage)
	case "owner-result":
		if len(args) < 5 || len(args) > 6 {
			return errors.New("usage: maintenance-admin owner-result <retirement-id> <owner> <SUCCEEDED|FAILED> <proof-json> [error-code]")
		}
		var proof map[string]any
		if err := json.Unmarshal([]byte(args[4]), &proof); err != nil {
			return fmt.Errorf("proof-json must be a JSON object: %w", err)
		}
		succeeded := args[3] == "SUCCEEDED"
		if !succeeded && args[3] != "FAILED" {
			return errors.New("owner result must be SUCCEEDED or FAILED")
		}
		errorCode := ""
		if len(args) == 6 {
			errorCode = args[5]
		}
		return store.RecordOwnerResult(ctx, args[1], args[2], succeeded, proof, errorCode, time.Now().UTC())
	default:
		return fmt.Errorf("unknown maintenance-admin command %q", args[0])
	}
}
