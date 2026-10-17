package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/canonicalhash"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

// CabinetID представляет логический строковый идентификатор кабинета WB.
type CabinetID string

// SellerKey представляет 32-байтный канонический хэш идентификатора селлера WB.
type SellerKey [sha256.Size]byte

// ClientGeneration представляет 32-байтный канонический хэш API-токена кабинета WB.
type ClientGeneration [sha256.Size]byte

// CabinetAccess содержит снимок доступа без токена и правил конкретного переноса.
type CabinetAccess struct {
	CabinetID           CabinetID  
	Name                string
	SellerKey           SellerKey
	BindingRevision     int64
	CapabilityRevision  int64
	ContentRead         bool
	ContentWrite        bool
	CredentialExpiresAt time.Time
	ClientGeneration    ClientGeneration
}

// Validate проверяет общий формат локального идентификатора, включая старые строковые ID.
func (c CabinetID) Validate() error {
	if c == "" || len(c) > 128 || strings.TrimSpace(string(c)) != string(c) {
		return core_errors.InvalidArgument("invalid WB cabinet ID")
	}
	return nil
}

// IsZero возвращает true, если идентификатор кабинета пустой.
func (c CabinetID) IsZero() bool {
	return c == ""
}

// NewSellerKey вычисляет детерминированный канонический хэш ключа селлера WB.
func NewSellerKey(sellerID string) SellerKey {
	builder := canonicalhash.NewSHA256("wb-seller-key:v1")
	builder.String(sellerID)
	return SellerKey(builder.Sum())
}

// IsZero возвращает true, если ключ селлера не инициализирован.
func (k SellerKey) IsZero() bool {
	return k == SellerKey{}
}

// String возвращает шестнадцатеричное представление ключа селлера.
func (k SellerKey) String() string {
	return hex.EncodeToString(k[:])
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
