// Package uuidv7 is the canonical RFC 9562 UUIDv7 generator and validator for the platform.
package uuidv7

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const maxUnixMilli = (1 << 48) - 1

// ErrTimestampOutOfRange reports a timestamp outside the 48-bit Unix millisecond range.
var ErrTimestampOutOfRange = errors.New("UUIDv7 timestamp is out of range")

// New generates a canonical lowercase RFC 9562 UUIDv7 stamped at the current UTC time.
func New() (string, error) {
	return NewAt(time.Now().UTC())
}

// MustNew calls New and panics if the system CSPRNG fails.
func MustNew() string {
	id, err := New()
	if err != nil {
		panic(err)
	}
	return id
}

// NewAt generates a canonical lowercase RFC 9562 UUIDv7 stamped at at.UTC().UnixMilli().
func NewAt(at time.Time) (string, error) {
	millis := at.UTC().UnixMilli()
	if millis < 0 || millis > maxUnixMilli {
		return "", ErrTimestampOutOfRange
	}
	var entropy [10]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return formatMillisAndEntropy(uint64(millis), entropy), nil
}

// MustNewAt calls NewAt and panics on timestamp range or CSPRNG failure.
func MustNewAt(at time.Time) string {
	id, err := NewAt(at)
	if err != nil {
		panic(err)
	}
	return id
}

// FormatAt produces a deterministic lowercase RFC 9562 UUIDv7 from at and caller entropy/hash bytes.
func FormatAt(at time.Time, entropy [10]byte) (string, error) {
	millis := at.UTC().UnixMilli()
	if millis < 0 || millis > maxUnixMilli {
		return "", ErrTimestampOutOfRange
	}
	return formatMillisAndEntropy(uint64(millis), entropy), nil
}

// MustFormatAt calls FormatAt and panics if at is outside the 48-bit millisecond range.
func MustFormatAt(at time.Time, entropy [10]byte) string {
	id, err := FormatAt(at, entropy)
	if err != nil {
		panic(err)
	}
	return id
}

// FormatRaw stamps RFC 9562 version 7 and variant 10 bits onto raw and formats canonical lowercase 8-4-4-4-12 hex.
func FormatRaw(raw [16]byte) string {
	raw[6] = (raw[6] & 0x0f) | 0x70
	raw[8] = (raw[8] & 0x3f) | 0x80
	var encoded [36]byte
	hex.Encode(encoded[0:8], raw[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], raw[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], raw[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], raw[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], raw[10:16])
	return string(encoded[:])
}

func formatMillisAndEntropy(millis uint64, entropy [10]byte) string {
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], millis<<16)
	copy(raw[6:], entropy[:])
	return FormatRaw(raw)
}

// IsValid reports whether value is a canonical lowercase RFC 9562 UUIDv7 string (8-4-4-4-12, version 7, variant 8/9/a/b).
func IsValid(value string) bool {
	return isValid(value, false)
}

// IsValidAnyCase reports whether value is an 8-4-4-4-12 RFC 9562 UUIDv7 string in lowercase or uppercase hex.
func IsValidAnyCase(value string) bool {
	return isValid(value, true)
}

func isValid(value string, allowUpper bool) bool {
	if len(value) != 36 {
		return false
	}
	if value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	if value[14] != '7' {
		return false
	}
	switch value[19] {
	case '8', '9', 'a', 'b':
	case 'A', 'B':
		if !allowUpper {
			return false
		}
	default:
		return false
	}
	for i := 0; i < 36; i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		c := value[i]
		isLowerHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		isUpperHex := allowUpper && c >= 'A' && c <= 'F'
		if !isLowerHex && !isUpperHex {
			return false
		}
	}
	return true
}

// IsValidLowerTrimmed trims outer whitespace before checking IsValid.
func IsValidLowerTrimmed(value string) bool {
	return IsValid(strings.TrimSpace(value))
}
