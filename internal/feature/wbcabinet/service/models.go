package wbcabinet_service

import (
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	credentials "github.com/ERONIS/wb-service/internal/core/transport/wb/credentials"
)

const MaxCabinetNameLength = 128

type SellerID = domain.SellerID
type ClientGeneration = domain.ClientGeneration

type TokenProperties = credentials.TokenProperties

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
	ID                  SellerID
	OwnerTelegramID     int64
	Name                string
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
	case cabinet.ID.Validate() != nil:
		return core_errors.InvalidArgument("invalid seller ID")
	case cabinet.OwnerTelegramID <= 0:
		return core_errors.InvalidArgument("invalid cabinet owner")
	case name == "" || len([]rune(name)) > MaxCabinetNameLength:
		return core_errors.InvalidArgument("invalid cabinet name")
	case cabinet.BindingRevision <= 0 || cabinet.CapabilityRevision <= 0:
		return core_errors.InvalidArgument("invalid cabinet revision")
	case cabinet.ClientGeneration.IsZero():
		return core_errors.InvalidArgument("invalid client generation")
	case cabinet.CredentialExpiresAt.IsZero(), cabinet.VerifiedAt.IsZero():
		return core_errors.InvalidArgument("invalid cabinet verification time")
	case !cabinet.Status.IsValid():
		return core_errors.InvalidArgument("invalid cabinet status")
	}
	return nil
}

type StoredCabinet struct {
	Cabinet
	Token string
}

type VerifiedCredential struct {
	SellerID            SellerID
	OwnerTelegramID     int64
	Name                string
	ContentRead         bool
	ContentWrite        bool
	TokenProperties     TokenProperties
	CredentialExpiresAt time.Time
	ClientGeneration    ClientGeneration
	VerifiedAt          time.Time
	Token               string
}

type TargetCredential = domain.CabinetAccess

type AddCommand struct {
	OwnerTelegramID int64
	Name            string
	Token           string
	SellerID        SellerID
}
