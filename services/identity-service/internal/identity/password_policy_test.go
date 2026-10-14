package identity

import (
	"errors"
	"strings"
	"testing"
)

func TestValidatePasswordAppliesTheProductRules(t *testing.T) {
	cases := []struct {
		name     string
		username string
		password string
		want     error
	}{
		{"eight characters", "operator1", "tide-pool", nil},
		{"eight Chinese characters count as eight", "operator1", "冷站巡检每日记录", nil},
		{"no composition rule", "operator1", "lowercaseonly", nil},
		{"seven characters", "operator1", "tidepoo", ErrPasswordTooShort},
		{"longer than 512 bytes", "operator1", strings.Repeat("x", 513), ErrPasswordTooLong},
		{"the username itself", "Operator1", "operator1", ErrPasswordMatchesUsername},
		{"a common password, any case", "operator1", "Password1", ErrPasswordCommon},
		{"a common numeric password", "operator1", "12345678", ErrPasswordCommon},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := ValidatePassword(testCase.username, testCase.password); !errors.Is(err, testCase.want) {
				t.Fatalf("ValidatePassword() = %v, want %v", err, testCase.want)
			}
		})
	}
}
