package schemagate

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

type fakeRow struct {
	value string
	err   error
}

func (row fakeRow) Scan(dest ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(dest) != 1 {
		return errors.New("fake row expects exactly one destination")
	}
	*(dest[0].(*string)) = row.value
	return nil
}

type fakeQuerier struct{ row pgx.Row }

func (db fakeQuerier) QueryRow(context.Context, string, ...any) pgx.Row { return db.row }

func TestVerifyProductSchemaAcceptsMatchingVersion(t *testing.T) {
	err := VerifyProductSchema(context.Background(), fakeQuerier{row: fakeRow{value: "0.1.0"}}, "0.1.0")
	if err != nil {
		t.Fatalf("matching version rejected: %v", err)
	}
}

func TestVerifyProductSchemaRejectsMismatchedVersion(t *testing.T) {
	err := VerifyProductSchema(context.Background(), fakeQuerier{row: fakeRow{value: "0.0.9"}}, "0.1.0")
	if err == nil {
		t.Fatal("mismatched version was accepted")
	}
}

func TestVerifyProductSchemaRejectsMissingRow(t *testing.T) {
	err := VerifyProductSchema(context.Background(), fakeQuerier{row: fakeRow{err: errors.New("no rows in result set")}}, "0.1.0")
	if err == nil {
		t.Fatal("missing schema row was accepted")
	}
}
