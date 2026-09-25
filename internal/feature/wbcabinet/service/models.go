package wbcabinet_service

import (
	"fmt"
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

const MaxCabinetNameLength = 128

type CabinetID = domain.CabinetID
type SellerKey = domain.SellerKey
type ClientGeneration = domain.ClientGeneration

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

func (properties TokenProperties) Has(permission TokenProperties) bool {
	return permission != 0 && properties&permission != 0
}

type Status string

const (
	StatusActive             Status = "active"
	StatusExpired            Status = "expired"
	StatusVerificationFailed Status = "verification_failed"
	StatusIdentityMismatch   Status = "identity_mismatch"
)

func (status Status) IsValid() bool {
	switch status {
	case StatusActive, StatusExpired, StatusVerificationFailed, StatusIdentityMismatch:
		return true
	default:
		return false
	}
}

type Cabinet struct {
	ID                  CabinetID
	OwnerTelegramID     int64
	Name                string
	SellerKey           SellerKey
	BindingRevision     int64
	CapabilityRevision  int64
	ContentRead         bool
	ContentWrite        bool
	TokenProperties     TokenProperties
	CredentialExpiresAt time.Time
	ClientGeneration    ClientGeneration
	VerifiedAt          time.Time
	Status              Status
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (cabinet Cabinet) Validate() error {
	name := strings.TrimSpace(cabinet.Name)
	switch {
	case cabinet.ID == "" || strings.TrimSpace(string(cabinet.ID)) != string(cabinet.ID) ||
		len(cabinet.ID) > 128:
		return fmt.Errorf("invalid cabinet ID: %w", core_errors.ErrInvalidArgument)
	case cabinet.OwnerTelegramID <= 0:
		return fmt.Errorf("invalid cabinet owner: %w", core_errors.ErrInvalidArgument)
	case name == "" || len([]rune(name)) > MaxCabinetNameLength:
		return fmt.Errorf("invalid cabinet name: %w", core_errors.ErrInvalidArgument)
	case cabinet.SellerKey == (SellerKey{}):
		return fmt.Errorf("invalid seller key: %w", core_errors.ErrInvalidArgument)
	case cabinet.BindingRevision <= 0 || cabinet.CapabilityRevision <= 0:
		return fmt.Errorf("invalid cabinet revision: %w", core_errors.ErrInvalidArgument)
	case cabinet.ClientGeneration == (ClientGeneration{}):
		return fmt.Errorf("invalid client generation: %w", core_errors.ErrInvalidArgument)
	case cabinet.CredentialExpiresAt.IsZero(), cabinet.VerifiedAt.IsZero():
		return fmt.Errorf("invalid cabinet verification time: %w", core_errors.ErrInvalidArgument)
	case !cabinet.Status.IsValid():
		return fmt.Errorf("invalid cabinet status: %w", core_errors.ErrInvalidArgument)
	}
	return nil
}

type StoredCabinet struct {
	Cabinet
	Token string
}

type VerifiedCredential struct {
	CabinetID           CabinetID
	OwnerTelegramID     int64
	Name                string
	SellerKey           SellerKey
	ContentRead         bool
	ContentWrite        bool
	TokenProperties     TokenProperties
	CredentialExpiresAt time.Time
	ClientGeneration    ClientGeneration
	VerifiedAt          time.Time
	Token               string
}

type TargetCredential struct {
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

type AddCommand struct {
	OwnerTelegramID int64
	Name            string
	Token           string
	CabinetID       CabinetID
}
