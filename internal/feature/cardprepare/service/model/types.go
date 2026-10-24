package model

import (
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

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
	Details      *OutcomeDetails
}

type OutcomeDetails struct {
	Reason                   string                             `json:"reason"`
	SourceField              string                             `json:"source_field"`
	VendorCode               string                             `json:"vendor_code"`
	SourceRows               []int                              `json:"source_rows"`
	Category                 string                             `json:"category"`
	SubjectID                int64                              `json:"subject_id"`
	CharacteristicID         int64                              `json:"characteristic_id"`
	CharacteristicName       string                             `json:"characteristic_name"`
	CharacteristicNameKey    string                             `json:"characteristic_name_key"`
	CharacteristicValue      any                                `json:"characteristic_value"`
	AvailableCharacteristics []contentapi.SubjectCharacteristic `json:"available_characteristics"`
	DirectoryValueCount      int                                `json:"directory_value_count,omitempty"`
	DirectoryValuesSample    []string                           `json:"directory_values_sample,omitempty"`
}

type SourceGroup struct {
	Items []SourceItem
}

type SourceItem struct {
	Position int
	Card     cardpipeline.Card
}

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
	Limits           contentapi.CardsLimits
	LimitsObservedAt time.Time
}

type Result struct {
	Outcome  Outcome
	Proposal *Proposal
}
