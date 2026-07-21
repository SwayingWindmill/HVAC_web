package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	logtopoc "github.com/quanlaihe/hvac-web/pocs/logto-sdk-adoption"
)

func main() {
	output := flag.String("output", "", "optional JSON output path")
	flag.Parse()

	report, err := logtopoc.Evaluate(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	payload = append(payload, '\n')
	if *output == "" {
		_, _ = os.Stdout.Write(payload)
		return
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*output, payload, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(logtopoc.Summary(report))
}
