package credentials

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	maxTokenPayloadBytes = 16 * 1024
	maxTokenBytes        = 16 * 1024
)

// Claims содержит сведения из токена WB.
type Claims struct {
	SellerID   string
	Properties TokenProperties
	ExpiresAt  time.Time
}

// TokenProperties хранит битовую маску прав токена.
type TokenProperties uint64

const (
	PermissionContent       TokenProperties = 1 << 1
	PermissionAnalytics     TokenProperties = 1 << 2
	PermissionPrices        TokenProperties = 1 << 3
	PermissionMarketplace   TokenProperties = 1 << 4
	PermissionStatistics    TokenProperties = 1 << 5
	PermissionPromotion     TokenProperties = 1 << 6
	PermissionFeedbacks     TokenProperties = 1 << 7
	PermissionBuyersChat    TokenProperties = 1 << 9
	PermissionSupplies      TokenProperties = 1 << 10
	PermissionBuyersReturns TokenProperties = 1 << 11
	PermissionDocuments     TokenProperties = 1 << 12
	PermissionFinance       TokenProperties = 1 << 13
	PermissionUsers         TokenProperties = 1 << 16
	PermissionReadOnly      TokenProperties = 1 << 30
)

// ParseToken разбирает поля токена; подлинность проверяется отдельно запросом к WB.
func ParseToken(token string, now time.Time) (Claims, error) {
	if err := ValidateTokenShape(token); err != nil {
		return Claims{}, err
	}
	parts := strings.Split(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) > maxTokenPayloadBytes {
		return Claims{}, ErrInvalidCredential
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return Claims{}, ErrInvalidCredential
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Claims{}, ErrInvalidCredential
	}
	if _, err := requiredUUIDClaim(values, "id"); err != nil {
		return Claims{}, ErrInvalidCredential
	}
	sellerID, err := requiredUUIDClaim(values, "sid")
	if err != nil {
		return Claims{}, ErrInvalidCredential
	}
	properties, err := requiredUintClaim(values, "s")
	if err != nil || properties > uint64(^uint64(0)>>1) {
		return Claims{}, ErrInvalidCredential
	}
	expiresUnix, err := requiredUintClaim(values, "exp")
	if err != nil || expiresUnix > uint64(^uint64(0)>>1) {
		return Claims{}, ErrInvalidCredential
	}
	expiresAt := time.Unix(int64(expiresUnix), 0).UTC()
	if !expiresAt.After(now.UTC()) {
		return Claims{}, ErrCredentialExpired
	}
	return Claims{
		SellerID:   sellerID,
		Properties: TokenProperties(properties),
		ExpiresAt:  expiresAt,
	}, nil
}

// ValidateTokenShape проверяет размер, пробелы и непустые части токена WB.
func ValidateTokenShape(token string) error {
	if token == "" || strings.TrimSpace(token) != token || len(token) > maxTokenBytes {
		return ErrInvalidCredential
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return ErrInvalidCredential
	}
	return nil
}

// Has проверяет наличие указанного права в битовой маске.
func (properties TokenProperties) Has(permission TokenProperties) bool {
	return permission != 0 && properties&permission != 0
}

// requiredUUIDClaim читает обязательный идентификатор из полей токена.
func requiredUUIDClaim(values map[string]any, name string) (string, error) {
	value, ok := values[name].(string)
	if !ok || !validCanonicalUUID(value) {
		return "", fmt.Errorf("token claim %s is not a canonical UUID", name)
	}
	return strings.ToLower(value), nil
}

// requiredUintClaim читает обязательное неотрицательное целое из полей токена.
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

// validCanonicalUUID проверяет каноническую запись идентификатора.
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
