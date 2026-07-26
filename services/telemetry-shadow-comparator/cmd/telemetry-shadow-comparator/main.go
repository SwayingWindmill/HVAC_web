package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/quanlaihe/hvac-web/services/telemetry-shadow-comparator/internal/comparison"
)

const maximumInputBytes = 64 << 20

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "telemetry shadow comparison failed: %v\n", err)
		os.Exit(1)
	}
}

func run(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("telemetry-shadow-comparator", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputPath := flags.String("input", "", "read-only comparison input JSON")
	outputPath := flags.String("output", "", "optional audit report JSON path")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *inputPath == "" || flags.NArg() != 0 {
		return errors.New("exactly one --input path is required")
	}

	inputBytes, err := readBounded(*inputPath)
	if err != nil {
		return err
	}
	var input comparison.Input
	decoder := json.NewDecoder(bytes.NewReader(inputBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return fmt.Errorf("decode comparison input: %w", err)
	}
	if err := requireEOF(decoder); err != nil {
		return err
	}

	report, err := comparison.Compare(input)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode comparison report: %w", err)
	}
	encoded = append(encoded, '\n')
	if *outputPath == "" {
		_, err = stdout.Write(encoded)
		return err
	}
	return writeAtomic(*outputPath, encoded)
}

func readBounded(path string) ([]byte, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open comparison input: %w", err)
	}
	defer file.Close()
	reader := io.LimitReader(file, maximumInputBytes+1)
	value, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read comparison input: %w", err)
	}
	if len(value) > maximumInputBytes {
		return nil, errors.New("comparison input exceeds 64 MiB")
	}
	return value, nil
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode trailing comparison input: %w", err)
	}
	return errors.New("comparison input contains multiple JSON values")
}

func writeAtomic(path string, value []byte) error {
	clean := filepath.Clean(path)
	directory := filepath.Dir(clean)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create comparison report directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".shadow-report-*.json")
	if err != nil {
		return fmt.Errorf("create comparison report: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("restrict comparison report: %w", err)
	}
	if _, err := temporary.Write(value); err != nil {
		temporary.Close()
		return fmt.Errorf("write comparison report: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync comparison report: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close comparison report: %w", err)
	}
	if err := os.Rename(temporaryPath, clean); err != nil {
		return fmt.Errorf("publish comparison report: %w", err)
	}
	return nil
}
