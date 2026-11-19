package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/settlement-service/internal/settlement"
)

type request struct {
	Operation   string `json:"operation"`
	TenantID    string `json:"tenantId"`
	SiteID      string `json:"siteId"`
	PeriodID    string `json:"periodId"`
	CandidateID string `json:"candidateId"`
	Reason      string `json:"reason"`
}

type settlementRunner interface {
	CalculatePeriod(context.Context, string, string, string) (settlement.Snapshot, error)
	ReconcilePeriod(context.Context, string, string, string, string) (string, error)
	ApproveCandidate(context.Context, string, string, string) error
	ApplyApprovedRevision(context.Context, string, string, string) (settlement.Snapshot, error)
}

func main() {
	// Settlement writes its result JSON strictly to os.Stdout; all logs MUST go to os.Stderr.
	logger := observability.NewJSONLogger(os.Stderr, slog.LevelInfo)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Stdin, os.Stdout, logger); err != nil {
		logger.Error("settlement_service_failed", "error_code", "SETTLEMENT_SERVICE_FAILED", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, stdin io.Reader, stdout io.Writer, logger *slog.Logger) error {
	if logger == nil {
		logger = observability.NewJSONLogger(os.Stderr, slog.LevelInfo)
	}
	repo, err := settlement.OpenPostgres(ctx, os.Getenv("SETTLEMENT_POSTGRES_DSN"))
	if err != nil {
		return err
	}
	defer repo.Close()

	facts, err := settlement.NewClickHouseStore(os.Getenv("SETTLEMENT_CLICKHOUSE_URL"), os.Getenv("SETTLEMENT_CLICKHOUSE_USER"), os.Getenv("SETTLEMENT_CLICKHOUSE_PASSWORD"), nil)
	if err != nil {
		return err
	}
	defer facts.CloseIdleConnections()

	engine, err := settlement.New(repo, facts)
	if err != nil {
		return err
	}
	var in request
	if err = json.NewDecoder(stdin).Decode(&in); err != nil {
		return fmt.Errorf("decode settlement request: %w", err)
	}
	return executeOperation(ctx, engine, in, stdout, logger)
}

func executeOperation(ctx context.Context, engine settlementRunner, in request, stdout io.Writer, logger *slog.Logger) error {
	if engine == nil {
		return errors.New("settlement engine is required")
	}
	enc := json.NewEncoder(stdout)
	switch in.Operation {
	case "calculate":
		out, err := engine.CalculatePeriod(ctx, in.TenantID, in.SiteID, in.PeriodID)
		if err != nil {
			return err
		}
		if err := enc.Encode(out); err != nil {
			return err
		}
		logger.Info("settlement_operation_completed", "operation", in.Operation, "tenant_id", in.TenantID, "site_id", in.SiteID, "period_id", in.PeriodID)
		return nil
	case "reconcile":
		id, err := engine.ReconcilePeriod(ctx, in.TenantID, in.SiteID, in.PeriodID, in.Reason)
		if err != nil {
			return err
		}
		if err := enc.Encode(map[string]string{"candidateId": id}); err != nil {
			return err
		}
		logger.Info("settlement_operation_completed", "operation", in.Operation, "tenant_id", in.TenantID, "site_id", in.SiteID, "candidate_id", id)
		return nil
	case "approve":
		if err := engine.ApproveCandidate(ctx, in.TenantID, in.SiteID, in.CandidateID); err != nil {
			return err
		}
		if err := enc.Encode(map[string]string{"candidateId": in.CandidateID, "status": "APPROVED"}); err != nil {
			return err
		}
		logger.Info("settlement_operation_completed", "operation", in.Operation, "tenant_id", in.TenantID, "site_id", in.SiteID, "candidate_id", in.CandidateID)
		return nil
	case "apply":
		out, err := engine.ApplyApprovedRevision(ctx, in.TenantID, in.SiteID, in.CandidateID)
		if err != nil {
			return err
		}
		if err := enc.Encode(out); err != nil {
			return err
		}
		logger.Info("settlement_operation_completed", "operation", in.Operation, "tenant_id", in.TenantID, "site_id", in.SiteID, "candidate_id", in.CandidateID)
		return nil
	default:
		return errors.New("unsupported settlement operation")
	}
}
