package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/settlement-service/internal/settlement"
)

type stubSettlementRunner struct{}

func (stubSettlementRunner) CalculatePeriod(context.Context, string, string, string) (settlement.Snapshot, error) {
	return settlement.Snapshot{ID: "snap-1"}, nil
}
func (stubSettlementRunner) ReconcilePeriod(context.Context, string, string, string, string) (string, error) {
	return "cand-1", nil
}
func (stubSettlementRunner) ApproveCandidate(context.Context, string, string, string) error {
	return nil
}
func (stubSettlementRunner) ApplyApprovedRevision(context.Context, string, string, string) (settlement.Snapshot, error) {
	return settlement.Snapshot{ID: "snap-2"}, nil
}

func TestExecuteOperationWritesOnlyResultJSONToStdoutAndLogsToStderr(t *testing.T) {
	origStdout := os.Stdout
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wOut
	t.Cleanup(func() { os.Stdout = origStdout })

	var stderrBuf bytes.Buffer
	logger := observability.NewJSONLogger(&stderrBuf, slog.LevelInfo)
	slog.SetDefault(logger)

	var resultOut bytes.Buffer
	req := request{
		Operation:   "approve",
		TenantID:    "tenant-1",
		SiteID:      "site-1",
		CandidateID: "cand-1",
	}
	if err := executeOperation(context.Background(), stubSettlementRunner{}, req, &resultOut, logger); err != nil {
		t.Fatalf("executeOperation returned error: %v", err)
	}

	_ = wOut.Close()
	leakedStdout, _ := io.ReadAll(rOut)
	if len(leakedStdout) != 0 {
		t.Fatalf("settlement logger leaked output to os.Stdout: %q", string(leakedStdout))
	}

	var decoded map[string]string
	if err := json.Unmarshal(resultOut.Bytes(), &decoded); err != nil {
		t.Fatalf("result output on stdout is not valid single JSON object: %v (raw=%q)", err, resultOut.String())
	}
	if decoded["candidateId"] != "cand-1" || decoded["status"] != "APPROVED" {
		t.Fatalf("unexpected stdout JSON payload: %#v", decoded)
	}

	stderrText := strings.TrimSpace(stderrBuf.String())
	var logEntry map[string]any
	if err := json.Unmarshal([]byte(stderrText), &logEntry); err != nil {
		t.Fatalf("expected structured JSON log on stderr, got %q (err=%v)", stderrText, err)
	}
	if logEntry["msg"] != "settlement_operation_completed" {
		t.Fatalf("unexpected stderr log msg: %#v", logEntry)
	}
}
