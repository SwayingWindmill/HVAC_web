package connectivity

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
)

type errRowScanner struct {
	err error
}

func (r errRowScanner) Scan(...any) error {
	return r.err
}

func TestRunSerializableTenantOpRetries40001And40P01(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
	}{
		{name: "serialization_failure 40001", code: "40001"},
		{name: "deadlock_detected 40P01", code: "40P01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			got, err := runSerializableTenantOp(context.Background(), func() (string, error) {
				attempts++
				if attempts < 3 {
					return "", fmt.Errorf("wrapped write: %w", &pgconn.PgError{Code: tc.code})
				}
				return "ok", nil
			})
			if err != nil || got != "ok" || attempts != 3 {
				t.Fatalf("got=(%q, %v) attempts=%d, want (ok, nil) attempts=3", got, err, attempts)
			}

			exhausted := 0
			_, err = runSerializableTenantOp(context.Background(), func() (string, error) {
				exhausted++
				return "", &pgconn.PgError{Code: tc.code}
			})
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != tc.code || exhausted != 4 {
				t.Fatalf("exhausted=%d err=%v, want 4 attempts and code %s", exhausted, err, tc.code)
			}
		})
	}
}

func TestRunSerializableTenantOpDoesNotRetryPermanentOrCanceledErrors(t *testing.T) {
	permanent := []struct {
		name string
		err  error
	}{
		{name: "ErrNotFound", err: ErrNotFound},
		{name: "wrapped ErrCommandCorrelationNotFound", err: fmt.Errorf("%w: %w", ErrNotFound, commandmodel.ErrCommandCorrelationNotFound)},
		{name: "ErrCorrelationMismatch", err: ErrCorrelationMismatch},
		{name: "commandmodel.ErrCommandCorrelationMismatch", err: commandmodel.ErrCommandCorrelationMismatch},
		{name: "unique_violation 23505", err: &pgconn.PgError{Code: "23505"}},
		{name: "generic error", err: errors.New("permanent failure")},
	}
	for _, tc := range permanent {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			_, err := runSerializableTenantOp(context.Background(), func() (int, error) {
				attempts++
				return 0, tc.err
			})
			if !errors.Is(err, tc.err) || attempts != 1 {
				t.Fatalf("attempts=%d err=%v, want attempts=1 and %v", attempts, err, tc.err)
			}
		})
	}

	t.Run("canceled context stops immediately", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		attempts := 0
		_, err := runSerializableTenantOp(ctx, func() (int, error) {
			attempts++
			cancel()
			return 0, &pgconn.PgError{Code: "40001"}
		})
		if !errors.Is(err, context.Canceled) || attempts != 1 {
			t.Fatalf("attempts=%d err=%v, want attempts=1 and context.Canceled", attempts, err)
		}
	})
}

func TestCorrelationErrorsAndGatewayTenantCacheLookup(t *testing.T) {
	if !errors.Is(ErrCorrelationMismatch, commandmodel.ErrCommandCorrelationMismatch) {
		t.Fatal("ErrCorrelationMismatch must align with commandmodel.ErrCommandCorrelationMismatch")
	}

	_, scanErr := scanCorrelation(errRowScanner{err: pgx.ErrNoRows})
	if !errors.Is(scanErr, ErrNotFound) || !errors.Is(scanErr, commandmodel.ErrCommandCorrelationNotFound) {
		t.Fatalf("scanCorrelation ErrNoRows=%v must match both ErrNotFound and ErrCommandCorrelationNotFound", scanErr)
	}

	now := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	store := &Store{now: func() time.Time { return now }}
	gwID := "01900000-0000-7000-8000-000000000011"
	tenantID := "01900000-0000-7000-8000-000000000001"

	cacheSetIfEpoch(
		&store.cache,
		&store.cache.gateways,
		gwID,
		gatewayLookupResult{gateway: adapter.Gateway{ID: gwID, TenantID: tenantID}},
		now.Add(time.Minute),
		now,
		gwID,
		0,
	)
	gotTenant, err := store.GatewayTenant(context.Background(), gwID)
	if err != nil || gotTenant != tenantID {
		t.Fatalf("GatewayTenant cache hit=(%q, %v), want (%q, nil)", gotTenant, err, tenantID)
	}

	// Negative or empty cached entries must not bypass PostgreSQL.
	negGW := "01900000-0000-7000-8000-000000000012"
	cacheSetIfEpoch(
		&store.cache,
		&store.cache.gateways,
		negGW,
		gatewayLookupResult{gateway: adapter.Gateway{ID: negGW, TenantID: tenantID}, err: adapter.ErrGatewayCredentialInactive},
		now.Add(time.Minute),
		now,
		negGW,
		0,
	)
	if _, err := store.GatewayTenant(context.Background(), negGW); err == nil {
		t.Fatal("expected cached error entry not to bypass DB lookup")
	}

	emptyTenantGW := "01900000-0000-7000-8000-000000000013"
	cacheSetIfEpoch(
		&store.cache,
		&store.cache.gateways,
		emptyTenantGW,
		gatewayLookupResult{gateway: adapter.Gateway{ID: emptyTenantGW, TenantID: ""}},
		now.Add(time.Minute),
		now,
		emptyTenantGW,
		0,
	)
	if _, err := store.GatewayTenant(context.Background(), emptyTenantGW); err == nil {
		t.Fatal("expected empty TenantID cache entry not to bypass DB lookup")
	}
}
