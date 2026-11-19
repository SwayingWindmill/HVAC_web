// Package fixedpoint provides 6-decimal fixed-point arithmetic for energy and financial billing.
package fixedpoint

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/bits"
	"strconv"
	"strings"
)

const (
	// Scale is the fixed-point multiplier corresponding to 6 fractional digits.
	Scale = 1_000_000

	scalePlaces = 6

	// Zero represents 0.000000.
	Zero = Decimal6(0)

	// MaxDecimal6 is the largest representable Decimal6 value (9,223,372,036,854.775807).
	MaxDecimal6 = Decimal6(math.MaxInt64)

	// MinDecimal6 is the smallest representable Decimal6 value (-9,223,372,036,854.775808).
	MinDecimal6 = Decimal6(math.MinInt64)
)

var (
	// ErrOverflow indicates a Decimal6 value or intermediate result exceeded int64 bounds.
	ErrOverflow = errors.New("fixedpoint: decimal6 overflow")

	// ErrDivisionByZero indicates division by a zero Decimal6 divisor.
	ErrDivisionByZero = errors.New("fixedpoint: division by zero")

	// ErrInvalidDecimal indicates a malformed or non-finite Decimal6 input.
	ErrInvalidDecimal = errors.New("fixedpoint: invalid decimal6 value")
)

var pow10Table = [...]uint64{
	1_000_000, // 0 fractional places
	100_000,   // 1 fractional place
	10_000,    // 2 fractional places
	1_000,     // 3 fractional places
	100,       // 4 fractional places
	10,        // 5 fractional places
	1,         // 6 fractional places
}

// Decimal6 stores a signed decimal value scaled by 1_000_000 (6 fractional digits).
type Decimal6 int64

// FromInt64Scaled wraps a pre-scaled integer value (1 unit = 10^-6).
func FromInt64Scaled(scaled int64) Decimal6 {
	return Decimal6(scaled)
}

// FromFloat64 converts a finite float64 into Decimal6 rounded half away from zero to 6 decimals.
func FromFloat64(value float64) (Decimal6, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, ErrInvalidDecimal
	}
	scaled := math.Round(value * float64(Scale))
	if math.IsNaN(scaled) || math.IsInf(scaled, 0) || scaled >= float64(1<<63) || scaled < -float64(1<<63) {
		return 0, ErrOverflow
	}
	return Decimal6(int64(scaled)), nil
}

// MustFromFloat64 converts a constant float64 into Decimal6 and panics if invalid or out of range.
func MustFromFloat64(value float64) Decimal6 {
	parsed, err := FromFloat64(value)
	if err != nil {
		panic(err)
	}
	return parsed
}

// Parse parses a decimal string into Decimal6 without any floating-point intermediary, rounding
// fractional digits beyond the 6th place half away from zero.
func Parse(raw string) (Decimal6, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, ErrInvalidDecimal
	}

	neg := false
	switch s[0] {
	case '-':
		neg = true
		s = s[1:]
	case '+':
		s = s[1:]
	}
	if s == "" {
		return 0, ErrInvalidDecimal
	}

	mantissa := s
	exp := 0
	if expIdx := strings.IndexAny(s, "eE"); expIdx >= 0 {
		mantissa = s[:expIdx]
		expStr := s[expIdx+1:]
		parsedExp, err := parseExponent(expStr)
		if err != nil {
			return 0, err
		}
		exp = parsedExp
	}

	intDigits, fracDigits, err := splitMantissa(mantissa)
	if err != nil {
		return 0, err
	}

	combined := intDigits + fracDigits
	if allZeroDigits(combined) {
		return Zero, nil
	}

	// Shift the decimal point by exp without allocating a giant zero-padded buffer for extreme exponents.
	decPos := len(intDigits) + exp
	if decPos > 20 {
		return 0, ErrOverflow
	}
	if decPos < -7 {
		// Maximum single non-zero digit is at <= 10^-8, which always rounds to zero at 10^-6.
		return Zero, nil
	}

	var normalizedInt string
	var normalizedFrac string
	switch {
	case decPos <= 0:
		normalizedFrac = strings.Repeat("0", -decPos) + combined
	case decPos >= len(combined):
		normalizedInt = combined + strings.Repeat("0", decPos-len(combined))
	default:
		normalizedInt = combined[:decPos]
		normalizedFrac = combined[decPos:]
	}

	maxMag := uint64(math.MaxInt64)
	if neg {
		maxMag = uint64(1) << 63
	}

	whole, err := parseUintDigitsBounded(normalizedInt, maxMag/Scale)
	if err != nil {
		return 0, err
	}

	var frac uint64
	for i := 0; i < scalePlaces; i++ {
		digit := uint64(0)
		if i < len(normalizedFrac) {
			digit = uint64(normalizedFrac[i] - '0')
		}
		frac = frac*10 + digit
	}
	if len(normalizedFrac) > scalePlaces && normalizedFrac[scalePlaces] >= '5' {
		frac++
	}

	mag := whole*Scale + frac
	if mag > maxMag {
		return 0, ErrOverflow
	}
	if neg {
		if mag == uint64(1)<<63 {
			return MinDecimal6, nil
		}
		return Decimal6(-int64(mag)), nil
	}
	return Decimal6(int64(mag)), nil
}

func parseExponent(raw string) (int, error) {
	if raw == "" {
		return 0, ErrInvalidDecimal
	}
	neg := false
	switch raw[0] {
	case '-':
		neg = true
		raw = raw[1:]
	case '+':
		raw = raw[1:]
	}
	if raw == "" || !allASCII(raw) {
		return 0, ErrInvalidDecimal
	}
	val := 0
	const capExp = 1_000_000
	for i := 0; i < len(raw); i++ {
		val = val*10 + int(raw[i]-'0')
		if val > capExp {
			val = capExp
		}
	}
	if neg {
		return -val, nil
	}
	return val, nil
}

func splitMantissa(mantissa string) (string, string, error) {
	if mantissa == "" {
		return "", "", ErrInvalidDecimal
	}
	dotIdx := strings.IndexByte(mantissa, '.')
	if dotIdx < 0 {
		if !allASCII(mantissa) {
			return "", "", ErrInvalidDecimal
		}
		return mantissa, "", nil
	}
	intPart := mantissa[:dotIdx]
	fracPart := mantissa[dotIdx+1:]
	if strings.IndexByte(fracPart, '.') >= 0 {
		return "", "", ErrInvalidDecimal
	}
	if len(intPart)+len(fracPart) == 0 || !allASCII(intPart) || !allASCII(fracPart) {
		return "", "", ErrInvalidDecimal
	}
	return intPart, fracPart, nil
}

func allASCII(digits string) bool {
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	return true
}

func allZeroDigits(digits string) bool {
	for i := 0; i < len(digits); i++ {
		if digits[i] != '0' {
			return false
		}
	}
	return true
}

func parseUintDigitsBounded(digits string, maxAllowed uint64) (uint64, error) {
	var acc uint64
	for i := 0; i < len(digits); i++ {
		d := uint64(digits[i] - '0')
		if acc > (maxAllowed-d)/10 {
			return 0, ErrOverflow
		}
		acc = acc*10 + d
	}
	return acc, nil
}

// Float64 converts the Decimal6 value to float64.
func (d Decimal6) Float64() float64 {
	return float64(d) / float64(Scale)
}

// Int64Scaled returns the raw scaled int64 representation (1 unit = 10^-6).
func (d Decimal6) Int64Scaled() int64 {
	return int64(d)
}

// String formats Decimal6 as an exact compact decimal string without trailing fractional zeros.
func (d Decimal6) String() string {
	if d == 0 {
		return "0"
	}
	u := absUint64(int64(d))
	intPart := u / Scale
	fracPart := u % Scale

	var b strings.Builder
	b.Grow(24)
	if d < 0 {
		b.WriteByte('-')
	}
	b.WriteString(strconv.FormatUint(intPart, 10))
	if fracPart == 0 {
		return b.String()
	}
	b.WriteByte('.')
	var fracBuf [scalePlaces]byte
	for i := scalePlaces - 1; i >= 0; i-- {
		fracBuf[i] = byte('0' + fracPart%10)
		fracPart /= 10
	}
	end := scalePlaces
	for end > 0 && fracBuf[end-1] == '0' {
		end--
	}
	b.Write(fracBuf[:end])
	return b.String()
}

// FormatFixed rounds d half away from zero to places fractional digits and formats with exact padding.
func (d Decimal6) FormatFixed(places int) string {
	if places < 0 {
		places = 0
	}
	rounded := d.RoundHalfUp(places)
	if rounded == 0 {
		if places == 0 {
			return "0"
		}
		return "0." + strings.Repeat("0", places)
	}
	u := absUint64(int64(rounded))
	intPart := u / Scale
	fracPart := u % Scale

	var b strings.Builder
	b.Grow(24 + places)
	if rounded < 0 {
		b.WriteByte('-')
	}
	b.WriteString(strconv.FormatUint(intPart, 10))
	if places == 0 {
		return b.String()
	}
	b.WriteByte('.')
	var fracBuf [scalePlaces]byte
	for i := scalePlaces - 1; i >= 0; i-- {
		fracBuf[i] = byte('0' + fracPart%10)
		fracPart /= 10
	}
	if places <= scalePlaces {
		b.Write(fracBuf[:places])
		return b.String()
	}
	b.Write(fracBuf[:])
	for i := scalePlaces; i < places; i++ {
		b.WriteByte('0')
	}
	return b.String()
}

// MarshalJSON encodes Decimal6 as an exact JSON number.
func (d Decimal6) MarshalJSON() ([]byte, error) {
	return []byte(d.String()), nil
}

// UnmarshalJSON decodes either a JSON number or a JSON string into Decimal6 without float loss.
func (d *Decimal6) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return ErrInvalidDecimal
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return err
		}
		parsed, err := Parse(text)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	}
	if trimmed[0] == '+' {
		return ErrInvalidDecimal
	}
	parsed, err := Parse(string(trimmed))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// Value implements database/sql/driver.Valuer by returning an exact decimal string for SQL NUMERIC.
func (d Decimal6) Value() (driver.Value, error) {
	return d.String(), nil
}

// Scan implements database/sql.Scanner for SQL NUMERIC text, bytes, integer, and float values.
func (d *Decimal6) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		return errors.New("fixedpoint: cannot scan NULL into Decimal6")
	case string:
		parsed, err := Parse(v)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	case []byte:
		parsed, err := Parse(string(v))
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	case int64:
		if v > math.MaxInt64/Scale || v < math.MinInt64/Scale {
			return ErrOverflow
		}
		*d = Decimal6(v * Scale)
		return nil
	case int:
		return d.Scan(int64(v))
	case int32:
		return d.Scan(int64(v))
	case float64:
		parsed, err := FromFloat64(v)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	case float32:
		parsed, err := FromFloat64(float64(v))
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	case Decimal6:
		*d = v
		return nil
	case fmt.Stringer:
		parsed, err := Parse(v.String())
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	default:
		return fmt.Errorf("fixedpoint: unsupported Scan type %T", src)
	}
}

// Add returns d + other, returning ErrOverflow if the sum exceeds int64 bounds.
func (d Decimal6) Add(other Decimal6) (Decimal6, error) {
	a := int64(d)
	b := int64(other)
	sum := a + b
	if (sum^a)&(sum^b) < 0 {
		return 0, ErrOverflow
	}
	return Decimal6(sum), nil
}

// AddChecked is an explicit alias for Add.
func (d Decimal6) AddChecked(other Decimal6) (Decimal6, error) {
	return d.Add(other)
}

// Sub returns d - other, returning ErrOverflow if the difference exceeds int64 bounds.
func (d Decimal6) Sub(other Decimal6) (Decimal6, error) {
	a := int64(d)
	b := int64(other)
	diff := a - b
	if (a^b)&(a^diff) < 0 {
		return 0, ErrOverflow
	}
	return Decimal6(diff), nil
}

// Sum returns the exact sum of values, tolerating intermediate cancelling spikes via big.Int fallback.
func Sum(values []Decimal6) (Decimal6, error) {
	var acc int64
	for i, v := range values {
		next := acc + int64(v)
		if (next^acc)&(next^int64(v)) < 0 {
			bigAcc := big.NewInt(acc)
			for _, rem := range values[i:] {
				bigAcc.Add(bigAcc, big.NewInt(int64(rem)))
			}
			if !bigAcc.IsInt64() {
				return 0, ErrOverflow
			}
			return Decimal6(bigAcc.Int64()), nil
		}
		acc = next
	}
	return Decimal6(acc), nil
}

// Mul multiplies d and other using 128-bit unsigned intermediates and rounds half away from zero to 6 decimals.
func (d Decimal6) Mul(other Decimal6) (Decimal6, error) {
	if d == 0 || other == 0 {
		return Zero, nil
	}
	neg := (d < 0) != (other < 0)
	ua := absUint64(int64(d))
	ub := absUint64(int64(other))

	hi, lo := bits.Mul64(ua, ub)
	if hi >= Scale {
		return 0, ErrOverflow
	}
	q, rem := bits.Div64(hi, lo, Scale)
	if rem >= Scale/2 {
		if q == math.MaxUint64 {
			return 0, ErrOverflow
		}
		q++
	}
	return unsignedToDecimal6(q, neg)
}

// Div divides d by other using 128-bit unsigned intermediates and rounds half away from zero to 6 decimals.
func (d Decimal6) Div(other Decimal6) (Decimal6, error) {
	if other == 0 {
		return 0, ErrDivisionByZero
	}
	if d == 0 {
		return Zero, nil
	}
	neg := (d < 0) != (other < 0)
	ua := absUint64(int64(d))
	ub := absUint64(int64(other))

	hi, lo := bits.Mul64(ua, Scale)
	if hi >= ub {
		return 0, ErrOverflow
	}
	q, rem := bits.Div64(hi, lo, ub)
	if rem >= ub-rem {
		if q == math.MaxUint64 {
			return 0, ErrOverflow
		}
		q++
	}
	return unsignedToDecimal6(q, neg)
}

// RoundHalfUp rounds d half away from zero to places fractional digits (0..6).
func (d Decimal6) RoundHalfUp(places int) Decimal6 {
	return d.round(places, false)
}

// RoundBankers rounds d half-to-even (banker's rounding) to places fractional digits (0..6).
func (d Decimal6) RoundBankers(places int) Decimal6 {
	return d.round(places, true)
}

// Cents returns d rounded half away from zero to 2 decimal places as integer cents.
func (d Decimal6) Cents() int64 {
	const centStep = uint64(10_000) // Scale / 100
	if d == 0 {
		return 0
	}
	neg := d < 0
	u := absUint64(int64(d))
	q := u / centStep
	if u%centStep >= centStep/2 {
		q++
	}
	if neg {
		return -int64(q)
	}
	return int64(q)
}

func (d Decimal6) round(places int, bankers bool) Decimal6 {
	if places < 0 {
		places = 0
	}
	if places >= scalePlaces || d == 0 {
		return d
	}
	step := pow10Table[places]
	neg := d < 0
	u := absUint64(int64(d))
	q := u / step
	rem := u % step
	half := step / 2

	roundUp := false
	if bankers {
		roundUp = rem > half || (rem == half && q%2 == 1)
	} else {
		roundUp = rem >= half
	}
	if roundUp {
		q++
	}

	maxAllowed := uint64(math.MaxInt64)
	if neg {
		maxAllowed = uint64(1) << 63
	}
	if q > maxAllowed/step {
		if neg {
			return MinDecimal6
		}
		return MaxDecimal6
	}
	rounded := q * step
	if neg {
		if rounded == uint64(1)<<63 {
			return MinDecimal6
		}
		return Decimal6(-int64(rounded))
	}
	return Decimal6(int64(rounded))
}

func absUint64(v int64) uint64 {
	if v < 0 {
		return -uint64(v)
	}
	return uint64(v)
}

func unsignedToDecimal6(mag uint64, neg bool) (Decimal6, error) {
	if neg {
		if mag > uint64(1)<<63 {
			return 0, ErrOverflow
		}
		if mag == uint64(1)<<63 {
			return MinDecimal6, nil
		}
		return Decimal6(-int64(mag)), nil
	}
	if mag > math.MaxInt64 {
		return 0, ErrOverflow
	}
	return Decimal6(int64(mag)), nil
}
