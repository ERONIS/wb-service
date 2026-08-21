package cardprepare_service

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
)

type Digest [sha256.Size]byte

func (digest Digest) String() string {
	return hex.EncodeToString(digest[:])
}

type OutcomeCode string

const (
	OutcomePrepared                     OutcomeCode = "prepared"
	OutcomeSubjectNotFound              OutcomeCode = "subject_not_found"
	OutcomeSubjectAmbiguous             OutcomeCode = "subject_ambiguous"
	OutcomeCharacteristicMissing        OutcomeCode = "characteristic_missing"
	OutcomeCharacteristicInvalid        OutcomeCode = "characteristic_invalid"
	OutcomeBrandNotFound                OutcomeCode = "brand_not_found"
	OutcomeDirectoryValueInvalid        OutcomeCode = "directory_value_invalid"
	OutcomeCardLimitExceeded            OutcomeCode = "card_limit_exceeded"
	OutcomePayloadTooLarge              OutcomeCode = "payload_too_large"
	OutcomeTargetTemporarilyUnavailable OutcomeCode = "target_temporarily_unavailable"
	OutcomeCatalogContractError         OutcomeCode = "catalog_contract_error"
)

func (code OutcomeCode) IsValid() bool {
	switch code {
	case OutcomePrepared,
		OutcomeSubjectNotFound,
		OutcomeSubjectAmbiguous,
		OutcomeCharacteristicMissing,
		OutcomeCharacteristicInvalid,
		OutcomeBrandNotFound,
		OutcomeDirectoryValueInvalid,
		OutcomeCardLimitExceeded,
		OutcomePayloadTooLarge,
		OutcomeTargetTemporarilyUnavailable,
		OutcomeCatalogContractError:
		return true
	default:
		return false
	}
}

type Outcome struct {
	Code         OutcomeCode
	ItemPosition int
	Field        string
}

type SourceGroup struct {
	Items []SourceItem
}

type SourceItem struct {
	Position int
	Card     cardimport_service.AggregatedCard
}

// CatalogSnapshot is immutable input obtained with safe WB reads. A nil
// directory slice means it was not fetched; an empty non-nil slice means WB
// returned an empty directory.
type CatalogSnapshot struct {
	Subjects        []contentapi.Subject
	Characteristics []contentapi.SubjectCharacteristic
	Brands          []contentapi.Brand
	Directories     DirectorySnapshot
	Limits          contentapi.CardsLimits
	ObservedAt      time.Time
}

type DirectorySnapshot struct {
	Colors    []string
	Kinds     []string
	Countries []string
	Seasons   []string
	VAT       []string
	TNVED     []string
}

type Proposal struct {
	SubjectID        int64
	Request          contentapi.UploadCardsRequest
	EncodedRequest   []byte
	MetadataSnapshot CatalogSnapshot
	SemanticDigest   Digest
	MetadataDigest   Digest
	ProposalRoot     Digest
	Limits           contentapi.CardsLimits
	LimitsObservedAt time.Time
}

type Result struct {
	Outcome  Outcome
	Proposal *Proposal
}
