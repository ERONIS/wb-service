package identity

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	contentCapabilityBit = uint64(1) << 1
	readOnlyBit          = uint64(1) << 30
	maxTokenPayloadBytes = 16 * 1024
)

type tokenClaims struct {
	TokenID    string
	SellerID   string
	Properties uint64
	ExpiresAt  time.Time
}

func decodeCredentialToken(token string, now time.Time) (tokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return tokenClaims{}, errors.New("token is not a compact JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return tokenClaims{}, errors.New("token payload is not base64url")
	}
	if len(payload) > maxTokenPayloadBytes {
		return tokenClaims{}, errors.New("token payload is too large")
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return tokenClaims{}, errors.New("token payload is not valid JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return tokenClaims{}, errors.New("token payload contains trailing JSON")
	}

	tokenID, err := requiredUUIDClaim(values, "id")
	if err != nil {
		return tokenClaims{}, err
	}
	sellerID, err := requiredUUIDClaim(values, "sid")
	if err != nil {
		return tokenClaims{}, err
	}
	properties, err := requiredUintClaim(values, "s")
	if err != nil {
		return tokenClaims{}, err
	}
	expiresUnix, err := requiredUintClaim(values, "exp")
	if err != nil || expiresUnix > uint64(^uint64(0)>>1) {
		return tokenClaims{}, errors.New("token claim exp is invalid")
	}
	expiresAt := time.Unix(int64(expiresUnix), 0).UTC()
	if !expiresAt.After(now.UTC()) {
		return tokenClaims{}, errors.New("token is expired")
	}

	return tokenClaims{
		TokenID:    tokenID,
		SellerID:   sellerID,
		Properties: properties,
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

func canonicalSellerID(value string) (string, error) {
	if !validCanonicalUUID(value) {
		return "", errors.New("WB seller ID is not a canonical UUID")
	}
	return strings.ToLower(value), nil
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

// Generation returns a safe digest used to pin operations to the exact token
// that was verified during startup.
func Generation(token string) ClientGeneration {
	return credentialDigest[ClientGeneration]("wb-client-generation:v1", token)
}

func credentialDigest[T ~[sha256.Size]byte](domain, value string) T {
	hasher := sha256.New()
	writeDigestPart(hasher, domain)
	writeDigestPart(hasher, value)
	var result T
	copy(result[:], hasher.Sum(nil))
	return result
}

type digestWriter interface {
	Write([]byte) (int, error)
}

func writeDigestPart(writer digestWriter, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}
