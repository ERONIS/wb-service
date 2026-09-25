package wbcabinet_service

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/canonicalhash"
)

const (
	maxTokenPayloadBytes = 16 * 1024
	maxTokenBytes        = 16 * 1024
)

type tokenClaims struct {
	SellerID   string
	Properties TokenProperties
	ExpiresAt  time.Time
}

func decodeCredentialToken(token string, now time.Time) (tokenClaims, error) {
	if token == "" || strings.TrimSpace(token) != token || len(token) > maxTokenBytes {
		return tokenClaims{}, ErrInvalidCredential
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return tokenClaims{}, ErrInvalidCredential
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) > maxTokenPayloadBytes {
		return tokenClaims{}, ErrInvalidCredential
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return tokenClaims{}, ErrInvalidCredential
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return tokenClaims{}, ErrInvalidCredential
	}
	if _, err := requiredUUIDClaim(values, "id"); err != nil {
		return tokenClaims{}, ErrInvalidCredential
	}
	sellerID, err := requiredUUIDClaim(values, "sid")
	if err != nil {
		return tokenClaims{}, ErrInvalidCredential
	}
	properties, err := requiredUintClaim(values, "s")
	if err != nil || properties > uint64(^uint64(0)>>1) {
		return tokenClaims{}, ErrInvalidCredential
	}
	expiresUnix, err := requiredUintClaim(values, "exp")
	if err != nil || expiresUnix > uint64(^uint64(0)>>1) {
		return tokenClaims{}, ErrInvalidCredential
	}
	expiresAt := time.Unix(int64(expiresUnix), 0).UTC()
	if !expiresAt.After(now.UTC()) {
		return tokenClaims{}, ErrCredentialExpired
	}
	return tokenClaims{
		SellerID:   sellerID,
		Properties: TokenProperties(properties),
		ExpiresAt:  expiresAt,
	}, nil
}

func requiredUUIDClaim(values map[string]any, name string) (string, error) {
	value, ok := values[name].(string)
	if !ok || !validCanonicalUUID(value) {
		return "", fmt.Errorf("token claim %s is not a canonical UUID", name)
	}
	return strings.ToLower(value), nil
}

func requiredUintClaim(values map[string]any, name string) (uint64, error) {
	number, ok := values[name].(json.Number)
	if !ok {
		return 0, fmt.Errorf("token claim %s is not an integer", name)
	}
	value, err := strconv.ParseUint(string(number), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("token claim %s is not an unsigned integer", name)
	}
	return value, nil
}

func validCanonicalUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		switch index {
		case 8, 13, 18, 23:
			if character != '-' {
				return false
			}
		default:
			if !((character >= '0' && character <= '9') ||
				(character >= 'a' && character <= 'f') ||
				(character >= 'A' && character <= 'F')) {
				return false
			}
		}
	}
	return true
}

func sellerKey(sellerID string) SellerKey {
	return credentialDigest[SellerKey]("wb-seller-key:v1", sellerID)
}

func credentialGeneration(token string) ClientGeneration {
	return credentialDigest[ClientGeneration]("wb-client-generation:v1", token)
}

func credentialDigest[T ~[sha256.Size]byte](domain, value string) T {
	builder := canonicalhash.NewSHA256(domain)
	builder.String(value)
	return T(builder.Sum())
}
