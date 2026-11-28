package commandmodel

import "github.com/quanlaihe/hvac-web/libs/uuidv7"

// IsUUIDv7 reports whether value is a canonical lowercase RFC 9562 UUIDv7.
func IsUUIDv7(value string) bool {
	return uuidv7.IsValidLowerTrimmed(value)
}
