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

func TestVerifyProductSchemaRejectsNilQuerier(t *testing.T) {
	if err := VerifyProductSchema(context.Background(), nil, "0.1.0"); err == nil {
		t.Fatal("nil querier was accepted")
	}
}

func TestVerifyEnvProductSchemaSkipsWhenUnsetOrBlank(t *testing.T) {
	for _, value := range []string{"", "   "} {
		t.Setenv("PHASE1_PRODUCT_VERSION", value)
		if err := VerifyEnvProductSchema(context.Background(), nil); err != nil {
			t.Fatalf("expected unset/blank PHASE1_PRODUCT_VERSION=%q to skip verification, got %v", value, err)
		}
	}
}

func TestVerifyEnvProductSchemaEnforcesMatchingVersion(t *testing.T) {
	t.Setenv("PHASE1_PRODUCT_VERSION", " 0.1.0 ")
	if err := VerifyEnvProductSchema(context.Background(), fakeQuerier{row: fakeRow{value: "0.1.0"}}); err != nil {
		t.Fatalf("matching trimmed version rejected: %v", err)
	}
}

func TestVerifyEnvProductSchemaRejectsMismatchedVersion(t *testing.T) {
	t.Setenv("PHASE1_PRODUCT_VERSION", "0.2.0")
	if err := VerifyEnvProductSchema(context.Background(), fakeQuerier{row: fakeRow{value: "0.1.0"}}); err == nil {
		t.Fatal("mismatched version was accepted when PHASE1_PRODUCT_VERSION was set")
	}
}

func TestVerifyEnvProductSchemaRejectsMissingRowWhenEnvSet(t *testing.T) {
	t.Setenv("PHASE1_PRODUCT_VERSION", "0.1.0")
	if err := VerifyEnvProductSchema(context.Background(), fakeQuerier{row: fakeRow{err: errors.New("no rows in result set")}}); err == nil {
		t.Fatal("missing schema row was accepted when PHASE1_PRODUCT_VERSION was set")
	}
}
