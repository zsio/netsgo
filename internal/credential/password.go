package credential

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

type HashParams struct {
	Memory  uint32
	Time    uint32
	Threads uint8
	KeyLen  uint32
}

var DefaultHashParams = HashParams{
	Memory:  64 * 1024,
	Time:    3,
	Threads: 1,
	KeyLen:  32,
}

func HashPassword(password string) (string, error) {
	return HashPasswordWithParams(password, DefaultHashParams)
}

func HashPasswordWithParams(password string, params HashParams) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password cannot be empty")
	}
	if params.Memory == 0 || params.Time == 0 || params.Threads == 0 || params.KeyLen == 0 {
		return "", fmt.Errorf("invalid credential hash parameters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate credential salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, params.Time, params.Memory, params.Threads, params.KeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		params.Memory,
		params.Time,
		params.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func VerifyPassword(encoded, password string) bool {
	params, salt, expected, err := ParsePasswordHash(encoded)
	if err != nil || password == "" {
		return false
	}
	key := argon2.IDKey([]byte(password), salt, params.Time, params.Memory, params.Threads, uint32(len(expected)))
	return subtle.ConstantTimeCompare(key, expected) == 1
}

func ParsePasswordHash(encoded string) (HashParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return HashParams{}, nil, nil, fmt.Errorf("invalid password hash format")
	}
	var params HashParams
	for _, item := range strings.Split(parts[3], ",") {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			return HashParams{}, nil, nil, fmt.Errorf("invalid password hash parameters")
		}
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return HashParams{}, nil, nil, fmt.Errorf("invalid password hash parameter %s", key)
		}
		switch key {
		case "m":
			params.Memory = uint32(parsed)
		case "t":
			params.Time = uint32(parsed)
		case "p":
			if parsed > 255 {
				return HashParams{}, nil, nil, fmt.Errorf("invalid password hash parallelism")
			}
			params.Threads = uint8(parsed)
		default:
			return HashParams{}, nil, nil, fmt.Errorf("unknown password hash parameter %s", key)
		}
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return HashParams{}, nil, nil, fmt.Errorf("decode password hash salt: %w", err)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return HashParams{}, nil, nil, fmt.Errorf("decode password hash key: %w", err)
	}
	if params.Memory == 0 || params.Time == 0 || params.Threads == 0 || len(salt) == 0 || len(key) == 0 {
		return HashParams{}, nil, nil, fmt.Errorf("invalid password hash")
	}
	return params, salt, key, nil
}
