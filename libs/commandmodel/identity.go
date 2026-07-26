package commandmodel

import (
	"encoding/hex"
	"strings"
)

// IsUUIDv7 reports whether value is a canonical lowercase RFC 9562 UUIDv7.
func IsUUIDv7(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 36 || value != strings.ToLower(value) || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || value[14] != '7' {
		return false
	}
	if !strings.ContainsRune("89ab", rune(value[19])) {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	if len(compact) != 32 {
		return false
	}
	_, err := hex.DecodeString(compact)
	return err == nil
}
