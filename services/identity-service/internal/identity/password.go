package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemoryKiB   uint32 = 64 * 1024
	argonIterations  uint32 = 3
	argonParallelism uint8  = 4
	argonSaltBytes          = 16
	argonKeyBytes    uint32 = 32
)

var errPasswordHashInvalid = errors.New("password hash is invalid")

func HashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 512 {
		return "", errors.New("password must be between 12 and 512 bytes")
	}
	salt := make([]byte, argonSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	return hashPasswordWithSalt(password, salt), nil
}

func hashPasswordWithSalt(password string, salt []byte) string {
	digest := argon2.IDKey([]byte(password), salt, argonIterations, argonMemoryKiB, argonParallelism, argonKeyBytes)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemoryKiB,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest),
	)
}

func VerifyPassword(password, encoded string) bool {
	params, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, params.iterations, params.memoryKiB, params.parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

type passwordParameters struct {
	memoryKiB   uint32
	iterations  uint32
	parallelism uint8
}

func parsePasswordHash(encoded string) (passwordParameters, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return passwordParameters{}, nil, nil, errPasswordHashInvalid
	}
	var params passwordParameters
	for _, item := range strings.Split(parts[3], ",") {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			return passwordParameters{}, nil, nil, errPasswordHashInvalid
		}
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return passwordParameters{}, nil, nil, errPasswordHashInvalid
		}
		switch key {
		case "m":
			params.memoryKiB = uint32(parsed)
		case "t":
			params.iterations = uint32(parsed)
		case "p":
			if parsed > 255 {
				return passwordParameters{}, nil, nil, errPasswordHashInvalid
			}
			params.parallelism = uint8(parsed)
		default:
			return passwordParameters{}, nil, nil, errPasswordHashInvalid
		}
	}
	if params.memoryKiB < 8 || params.iterations == 0 || params.parallelism == 0 {
		return passwordParameters{}, nil, nil, errPasswordHashInvalid
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return passwordParameters{}, nil, nil, errPasswordHashInvalid
	}
	digest, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(digest) < 16 {
		return passwordParameters{}, nil, nil, errPasswordHashInvalid
	}
	return params, salt, digest, nil
}

func dummyPasswordHash() string {
	return hashPasswordWithSalt("identity-dummy-password", make([]byte, argonSaltBytes))
}
