package fixedpoint

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestDecimal6AssociativeAdditionEliminatesFloatDrift(t *testing.T) {
	a := MustFromFloat64(0.1)
	b := MustFromFloat64(0.2)
	want := MustFromFloat64(0.3)

	sum, err := a.Add(b)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if sum != want || sum.String() != "0.3" || sum.Int64Scaled() != 300_000 {
		t.Fatalf("0.1 + 0.2 = %v (scaled=%d), want %v", sum, sum.Int64Scaled(), want)
	}

	// Summing 10_000 intervals of 0.1 + 0.2 + 0.05 in different orders stays exact.
	items := make([]Decimal6, 0, 3000)
	for i := 0; i < 1000; i++ {
		items = append(items, MustFromFloat64(0.1), MustFromFloat64(0.2), MustFromFloat64(0.05))
	}
	forward, err := Sum(items)
	if err != nil {
		t.Fatalf("Sum forward failed: %v", err)
	}
	reversed := make([]Decimal6, len(items))
	for i := range items {
		reversed[i] = items[len(items)-1-i]
	}
	backward, err := Sum(reversed)
	if err != nil {
		t.Fatalf("Sum backward failed: %v", err)
	}
	if forward != backward || forward.String() != "350" || forward.Cents() != 35000 {
		t.Fatalf("associative sum mismatch: forward=%s backward=%s cents=%d", forward, backward, forward.Cents())
	}
}

func TestDecimal6LargeProductsAndOverflowProtection(t *testing.T) {
	// 12-digit kWh (999,999,999,999.123456) * rate (1.234567) must not overflow int64 intermediate.
	kwh, err := Parse("999999999999.123456")
	if err != nil {
		t.Fatalf("Parse 12-digit kWh failed: %v", err)
	}
	rate, err := Parse("1.234567")
	if err != nil {
		t.Fatalf("Parse rate failed: %v", err)
	}
	product, err := kwh.Mul(rate)
	if err != nil {
		t.Fatalf("12-digit Mul failed: %v", err)
	}
	// Exact: 999999999999.123456 * 1.234567 = 1234566999998.917847703552 -> 1234566999998.917848 (half-up).
	want, err := Parse("1234566999998.917848")
	if err != nil {
		t.Fatalf("Parse expected product failed: %v", err)
	}
	if product != want {
		t.Fatalf("12-digit Mul = %s, want %s", product, want)
	}

	// Mul / Div half-up rounding on exact .0000005 midpoint (positive and negative).
	halfUnit := FromInt64Scaled(1) // 0.000001
	half := MustFromFloat64(0.5)
	posMid, err := halfUnit.Mul(half)
	if err != nil {
		t.Fatalf("posMid Mul failed: %v", err)
	}
	if posMid != FromInt64Scaled(1) {
		t.Fatalf("0.000001 * 0.5 = %s, want 0.000001", posMid)
	}
	negMid, err := FromInt64Scaled(-1).Mul(half)
	if err != nil {
		t.Fatalf("negMid Mul failed: %v", err)
	}
	if negMid != FromInt64Scaled(-1) {
		t.Fatalf("-0.000001 * 0.5 = %s, want -0.000001", negMid)
	}

	// Overflow checks on Add, AddChecked, Sub, Mul, Div, Sum, FromFloat64.
	if _, err := MaxDecimal6.Add(FromInt64Scaled(1)); !errors.Is(err, ErrOverflow) {
		t.Fatalf("MaxDecimal6 + 1e-6 err = %v, want ErrOverflow", err)
	}
	if _, err := MaxDecimal6.AddChecked(FromInt64Scaled(1)); !errors.Is(err, ErrOverflow) {
		t.Fatalf("MaxDecimal6 AddChecked 1e-6 err = %v, want ErrOverflow", err)
	}
	if _, err := MinDecimal6.Sub(FromInt64Scaled(1)); !errors.Is(err, ErrOverflow) {
		t.Fatalf("MinDecimal6 - 1e-6 err = %v, want ErrOverflow", err)
	}
	if _, err := Zero.Sub(MinDecimal6); !errors.Is(err, ErrOverflow) {
		t.Fatalf("0 - MinDecimal6 err = %v, want ErrOverflow", err)
	}
	if _, err := MaxDecimal6.Mul(MustFromFloat64(2)); !errors.Is(err, ErrOverflow) {
		t.Fatalf("MaxDecimal6 * 2 err = %v, want ErrOverflow", err)
	}
	if _, err := MustFromFloat64(1).Div(Zero); !errors.Is(err, ErrDivisionByZero) {
		t.Fatalf("1 / 0 err = %v, want ErrDivisionByZero", err)
	}
	if _, err := MaxDecimal6.Div(FromInt64Scaled(500_000)); !errors.Is(err, ErrOverflow) {
		t.Fatalf("MaxDecimal6 / 0.5 err = %v, want ErrOverflow", err)
	}
	if _, err := Sum([]Decimal6{MaxDecimal6, MaxDecimal6, -MaxDecimal6}); err != nil {
		t.Fatalf("cancelling Sum near MaxDecimal6 unexpectedly failed: %v", err)
	}
	if _, err := Sum([]Decimal6{MaxDecimal6, FromInt64Scaled(1)}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflowing Sum err = %v, want ErrOverflow", err)
	}
	for _, badFloat := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 1e15, -1e15} {
		if _, err := FromFloat64(badFloat); err == nil {
			t.Fatalf("FromFloat64(%v) expected error, got nil", badFloat)
		}
	}
}

func TestDecimal6DivisionAndRoundingHalfUpVsBankers(t *testing.T) {
	// Division half-up checks.
	one := MustFromFloat64(1)
	three := MustFromFloat64(3)
	third, err := one.Div(three)
	if err != nil {
		t.Fatalf("1 / 3 failed: %v", err)
	}
	if third.String() != "0.333333" {
		t.Fatalf("1 / 3 = %s, want 0.333333", third)
	}
	twoThirds, err := MustFromFloat64(2).Div(three)
	if err != nil {
		t.Fatalf("2 / 3 failed: %v", err)
	}
	if twoThirds.String() != "0.666667" {
		t.Fatalf("2 / 3 = %s, want 0.666667", twoThirds)
	}

	// Exact .005 ties (positive and negative): RoundHalfUp vs RoundBankers.
	cases := []struct {
		raw     string
		halfUp  string
		bankers string
		cents   int64
	}{
		{"0.005", "0.01", "0.00", 1},
		{"0.015", "0.02", "0.02", 2},
		{"0.025", "0.03", "0.02", 3},
		{"1.005", "1.01", "1.00", 101},
		{"1.015", "1.02", "1.02", 102},
		{"-0.005", "-0.01", "0.00", -1},
		{"-0.015", "-0.02", "-0.02", -2},
		{"-0.025", "-0.03", "-0.02", -3},
		{"-1.005", "-1.01", "-1.00", -101},
		{"-1.015", "-1.02", "-1.02", -102},
	}
	for _, tc := range cases {
		d, err := Parse(tc.raw)
		if err != nil {
			t.Fatalf("Parse(%q) failed: %v", tc.raw, err)
		}
		if got := d.RoundHalfUp(2).FormatFixed(2); got != tc.halfUp {
			t.Fatalf("RoundHalfUp(2) on %s = %s, want %s", tc.raw, got, tc.halfUp)
		}
		if got := d.RoundBankers(2).FormatFixed(2); got != tc.bankers {
			t.Fatalf("RoundBankers(2) on %s = %s, want %s", tc.raw, got, tc.bankers)
		}
		if gotCents := d.Cents(); gotCents != tc.cents {
			t.Fatalf("Cents() on %s = %d, want %d", tc.raw, gotCents, tc.cents)
		}
	}

	// -0.001 formatted to 2 decimals must not produce "-0.00".
	tinyNeg, _ := Parse("-0.001")
	if got := tinyNeg.FormatFixed(2); got != "0.00" {
		t.Fatalf("FormatFixed(2) on -0.001 = %q, want 0.00", got)
	}
	if got := MustFromFloat64(1.5).FormatFixed(0); got != "2" {
		t.Fatalf("FormatFixed(0) on 1.5 = %q, want 2", got)
	}
	if got := MustFromFloat64(1.5).FormatFixed(8); got != "1.50000000" {
		t.Fatalf("FormatFixed(8) on 1.5 = %q, want 1.50000000", got)
	}
}

func TestDecimal6JSONAndSQLRoundTripsAndParseEdgeCases(t *testing.T) {
	type payload struct {
		Num Decimal6 `json:"num"`
		Str Decimal6 `json:"str"`
	}

	orig := MustFromFloat64(-12345.678901)
	encoded, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}
	if string(encoded) != "-12345.678901" {
		t.Fatalf("MarshalJSON = %s, want -12345.678901", encoded)
	}

	var decoded payload
	if err := json.Unmarshal([]byte(`{"num": 9876543210.123456, "str": "-0.000005"}`), &decoded); err != nil {
		t.Fatalf("UnmarshalJSON failed: %v", err)
	}
	if decoded.Num.String() != "9876543210.123456" || decoded.Str.String() != "-0.000005" {
		t.Fatalf("unexpected decoded JSON: %+v", decoded)
	}
	if err := json.Unmarshal([]byte(`{"num": 1.25e3, "str": "null"}`), &decoded); err == nil {
		t.Fatalf("expected quoted \"null\" string to fail Decimal6 parse")
	}
	var nullTarget = MustFromFloat64(42)
	if err := json.Unmarshal([]byte(`null`), &nullTarget); err != nil || nullTarget != MustFromFloat64(42) {
		t.Fatalf("unmarshal JSON null got (%v, %v), want unchanged 42", nullTarget, err)
	}

	// SQL Value / Scan round-trips.
	val, err := orig.Value()
	if err != nil {
		t.Fatalf("Value() failed: %v", err)
	}
	var scanned Decimal6
	for _, src := range []any{
		val,
		[]byte("-12345.678901"),
		int64(42),
		int(42),
		float64(0.3),
		json.Number("12.345678"),
	} {
		if err := scanned.Scan(src); err != nil {
			t.Fatalf("Scan(%T=%v) failed: %v", src, src, err)
		}
	}
	if err := scanned.Scan(val); err != nil || scanned != orig {
		t.Fatalf("SQL roundtrip got %v, want %v (err=%v)", scanned, orig, err)
	}
	if err := scanned.Scan(nil); err == nil {
		t.Fatal("Scan(nil) expected error, got nil")
	}
	if err := scanned.Scan(true); err == nil {
		t.Fatal("Scan(bool) expected error, got nil")
	}

	// Parse edge cases.
	validParses := map[string]string{
		"0":                      "0",
		"-0":                     "0",
		"-0.000000":              "0",
		"  +123.450000  ":        "123.45",
		".5":                     "0.5",
		"1.":                     "1",
		"0.0000005":              "0.000001",
		"-0.0000005":             "-0.000001",
		"0.000000499999":         "0",
		"0.9999995":              "1",
		"1e-6":                   "0.000001",
		"1.25e3":                 "1250",
		"1e-30":                  "0",
		"9223372036854.775807":   "9223372036854.775807",
		"-9223372036854.775808":  "-9223372036854.775808",
		"9223372036854.7758074":  "9223372036854.775807",
		"-9223372036854.7758084": "-9223372036854.775808",
	}
	for input, wantStr := range validParses {
		got, err := Parse(input)
		if err != nil {
			t.Fatalf("Parse(%q) unexpected error: %v", input, err)
		}
		if got.String() != wantStr {
			t.Fatalf("Parse(%q) = %q, want %q", input, got.String(), wantStr)
		}
	}

	invalidParses := []string{
		"", "   ", "+", "-", ".", "+.", "-.", "1.2.3", "12abc", "NaN", "Inf",
		"1_000", "1e", "e5", "1e+", "1e30", "9223372036854.775808", "-9223372036854.775809",
		"9223372036854.7758075", "-9223372036854.7758085",
	}
	for _, input := range invalidParses {
		if got, err := Parse(input); err == nil {
			t.Fatalf("Parse(%q) unexpectedly succeeded with %v", input, got)
		}
	}
}
