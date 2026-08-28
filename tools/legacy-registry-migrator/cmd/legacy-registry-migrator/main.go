package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/tools/legacy-registry-migrator/internal/migration"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: legacy-registry-migrator <apply|resolve> [flags]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	switch args[0] {
	case "apply":
		flags := flag.NewFlagSet("apply", flag.ContinueOnError)
		dsn := flags.String("dsn", os.Getenv("S1_LEGACY_MIGRATION_DSN"), "PostgreSQL connection string")
		inputPath := flags.String("input", "-", "JSONL input file or - for stdin")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		reader, closeReader, err := openInput(*inputPath, stdin)
		if err != nil {
			return err
		}
		defer closeReader()
		records, err := migration.ReadRecords(reader)
		if err != nil {
			return err
		}
		store, err := migration.OpenPostgres(ctx, *dsn)
		if err != nil {
			return err
		}
		defer store.Close()
		summary, err := store.Apply(ctx, records)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(summary)

	case "resolve":
		flags := flag.NewFlagSet("resolve", flag.ContinueOnError)
		dsn := flags.String("dsn", os.Getenv("S1_LEGACY_MIGRATION_DSN"), "PostgreSQL connection string")
		inputPath := flags.String("input", "-", "corrected JSONL record or - for stdin")
		quarantineID := flags.String("quarantine-id", "", "open quarantine UUIDv7")
		action := flags.String("action", migration.ResolutionApply, "apply or retire")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		reader, closeReader, err := openInput(*inputPath, stdin)
		if err != nil {
			return err
		}
		defer closeReader()
		records, err := migration.ReadRecords(reader)
		if err != nil {
			return err
		}
		if len(records) != 1 {
			return fmt.Errorf("resolve requires exactly one corrected record; got %d", len(records))
		}
		store, err := migration.OpenPostgres(ctx, *dsn)
		if err != nil {
			return err
		}
		defer store.Close()
		result, err := store.Resolve(ctx, strings.TrimSpace(*quarantineID), strings.TrimSpace(*action), records[0])
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(result)
	default:
		return fmt.Errorf("unsupported command %q", args[0])
	}
}

func openInput(path string, stdin io.Reader) (io.Reader, func(), error) {
	if strings.TrimSpace(path) == "" || path == "-" {
		return stdin, func() {}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open migration input: %w", err)
	}
	return file, func() { _ = file.Close() }, nil
}
