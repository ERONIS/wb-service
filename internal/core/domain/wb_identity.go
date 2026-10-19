package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/ERONIS/wb-service/internal/core/canonicalhash"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

// SellerID представляет исходный идентификатор продавца WB из поля sid токена.
type SellerID string

// ClientGeneration представляет отпечаток конкретного API-токена кабинета WB.
type ClientGeneration [sha256.Size]byte

// CabinetAccess содержит снимок доступа без токена и правил конкретного переноса.
type CabinetAccess struct {
	SellerID            SellerID
	BindingRevision     int64
	CapabilityRevision  int64
	ContentRead         bool
	ContentWrite        bool
	CredentialExpiresAt time.Time
	ClientGeneration    ClientGeneration
}

// Validate проверяет нормализованный UUID продавца из токена WB.
func (id SellerID) Validate() error {
	if len(id) != 36 {
		return core_errors.InvalidArgument("invalid WB seller ID")
	}
	for position := 0; position < len(id); position++ {
		character := id[position]
		switch position {
		case 8, 13, 18, 23:
			if character != '-' {
				return core_errors.InvalidArgument("invalid WB seller ID")
			}
		default:
			if character < '0' || (character > '9' && character < 'a') || character > 'f' {
				return core_errors.InvalidArgument("invalid WB seller ID")
			}
		}
	}
	return nil
}

// NewClientGeneration вычисляет детерминированный канонический хэш поколения клиента по токену.
func NewClientGeneration(token string) ClientGeneration {
	builder := canonicalhash.NewSHA256("wb-client-generation:v1")
	builder.String(token)
	return ClientGeneration(builder.Sum())
}

// IsZero возвращает true, если поколение клиента не инициализировано.
func (g ClientGeneration) IsZero() bool {
	return g == ClientGeneration{}
}

// String возвращает шестнадцатеричное представление поколения клиента.
func (g ClientGeneration) String() string {
	return hex.EncodeToString(g[:])
}
