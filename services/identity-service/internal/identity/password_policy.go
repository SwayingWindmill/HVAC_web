package identity

import (
	_ "embed"
	"errors"
	"strings"
	"unicode/utf8"
)

// Password rules follow NIST SP 800-63B-4 §3.1.1.2 with the 8-character minimum the product
// chose (spec docs/specs/iam-roles-and-users.md): no composition rules, no forced rotation,
// a blocklist of common passwords, and room for passphrases.
const (
	MinimumPasswordCharacters = 8
	maximumPasswordBytes      = 512
)

var (
	ErrPasswordTooShort        = errors.New("password is shorter than 8 characters")
	ErrPasswordTooLong         = errors.New("password is longer than 512 bytes")
	ErrPasswordCommon          = errors.New("password is a commonly used password")
	ErrPasswordMatchesUsername = errors.New("password is the same as the username")
)

//go:embed common-passwords.txt
var commonPasswordList string

var commonPasswords = func() map[string]struct{} {
	set := map[string]struct{}{}
	for _, line := range strings.Split(commonPasswordList, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			set[line] = struct{}{}
		}
	}
	return set
}()

// ValidatePassword reports why a new password is refused, or nil when it may be set.
func ValidatePassword(username, password string) error {
	if utf8.RuneCountInString(password) < MinimumPasswordCharacters {
		return ErrPasswordTooShort
	}
	if len(password) > maximumPasswordBytes {
		return ErrPasswordTooLong
	}
	folded := strings.ToLower(password)
	if folded == normalizeUsername(username) {
		return ErrPasswordMatchesUsername
	}
	if _, common := commonPasswords[folded]; common {
		return ErrPasswordCommon
	}
	return nil
}
